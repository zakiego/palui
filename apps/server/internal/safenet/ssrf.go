// Package safenet ports packages/core/src/net: the SSRF guard (ssrf.ts) and
// safeFetch (fetch.ts). Every fetch the agent can steer goes through here. A
// URL a model supplies is a request this server makes on its behalf, so only
// http(s) is allowed, credentials in the URL are refused, and any address that
// only means something from inside this machine or its network is refused.
// The resolved address is checked, not the hostname string, and a redirect
// never walks past the check.
package safenet

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Error codes, as in the TS SsrfErrorCode and SafeFetchErrorCode.
const (
	CodeInvalidURL         = "invalid_url"
	CodeBlockedProtocol    = "blocked_protocol"
	CodeBlockedCredentials = "blocked_credentials"
	CodeBlockedHost        = "blocked_host"
	CodeBlockedAddress     = "blocked_address"
	CodeBlockedPort        = "blocked_port"
	CodeDNS                = "dns"
	CodeTooManyRedirects   = "too_many_redirects"
	CodeTimeout            = "timeout"
	CodeAborted            = "aborted"
	CodeTooLarge           = "too_large"
	CodeContentType        = "content_type"
	CodeNetwork            = "network"
)

// Error is an SsrfError or a SafeFetchError: a code plus a sentence.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Ports that speak something other than HTTP and have a history of SSRF abuse.
var blockedPorts = map[int]bool{}

func init() {
	for _, p := range []int{
		0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117,
		119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636,
		989, 990, 993, 995, 1719, 1720, 1723, 2049, 3306, 3659, 4045, 5060, 5061, 5432, 6000, 6379, 6566, 6665, 6666, 6667, 6668, 6669, 6697,
		10080, 11211, 27017,
	} {
		blockedPorts[p] = true
	}
}

// --- IPv4 ------------------------------------------------------------------------------

type v4Range struct {
	start, end uint32
	why        string
}

var v4Ranges = func() []v4Range {
	blocks := []struct {
		base string
		bits int
		why  string
	}{
		{"0.0.0.0", 8, "this network"},
		{"10.0.0.0", 8, "private"},
		{"100.64.0.0", 10, "carrier-grade NAT"},
		{"127.0.0.0", 8, "loopback"},
		{"169.254.0.0", 16, "link-local (cloud metadata)"},
		{"172.16.0.0", 12, "private"},
		{"192.0.0.0", 24, "IETF protocol assignments"},
		{"192.0.2.0", 24, "documentation"},
		{"192.88.99.0", 24, "6to4 relay"},
		{"192.168.0.0", 16, "private"},
		{"198.18.0.0", 15, "benchmarking"},
		{"198.51.100.0", 24, "documentation"},
		{"203.0.113.0", 24, "documentation"},
		{"224.0.0.0", 4, "multicast"},
		{"240.0.0.0", 4, "reserved"},
	}
	out := make([]v4Range, len(blocks))
	for i, b := range blocks {
		a := netip.MustParseAddr(b.base).As4()
		start := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
		size := uint64(1) << (32 - b.bits)
		out[i] = v4Range{start: start, end: uint32(uint64(start) + size - 1), why: b.why}
	}
	return out
}()

func blockedV4(n uint32) string {
	for _, r := range v4Ranges {
		if n >= r.start && n <= r.end {
			return r.why
		}
	}
	return ""
}

func v4Int(hi, lo uint16) uint32 { return uint32(hi)<<16 | uint32(lo) }

// --- IPv6 ------------------------------------------------------------------------------

func blockedV6(a netip.Addr) string {
	b := a.As16()
	var g [8]uint16
	for i := range g {
		g[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	zeroPrefix := g[0] == 0 && g[1] == 0 && g[2] == 0 && g[3] == 0 && g[4] == 0
	if zeroPrefix && g[5] == 0 && g[6] == 0 && g[7] == 0 {
		return "unspecified"
	}
	if zeroPrefix && g[5] == 0 && g[6] == 0 && g[7] == 1 {
		return "loopback"
	}
	// IPv4-mapped (::ffff:a.b.c.d) and the deprecated IPv4-compatible (::a.b.c.d).
	if zeroPrefix && (g[5] == 0xffff || g[5] == 0) {
		if inner := blockedV4(v4Int(g[6], g[7])); inner != "" {
			return "IPv4-mapped " + inner
		}
		return ""
	}
	// IPv4-translated (::ffff:0:a.b.c.d, RFC 2765).
	if g[0] == 0 && g[1] == 0 && g[2] == 0 && g[3] == 0 && g[4] == 0xffff && g[5] == 0 {
		if inner := blockedV4(v4Int(g[6], g[7])); inner != "" {
			return "IPv4-translated " + inner
		}
		return ""
	}
	// Local-use NAT64 (64:ff9b:1::/48, RFC 8215): block the whole prefix.
	if g[0] == 0x64 && g[1] == 0xff9b && g[2] == 1 {
		return "local-use NAT64 (64:ff9b:1::/48)"
	}
	// NAT64 (64:ff9b::/96) embeds an IPv4 address too.
	if g[0] == 0x64 && g[1] == 0xff9b && g[2] == 0 && g[3] == 0 && g[4] == 0 && g[5] == 0 {
		if inner := blockedV4(v4Int(g[6], g[7])); inner != "" {
			return "NAT64 " + inner
		}
		return ""
	}
	// 6to4 (2002::/16) embeds the IPv4 address in groups 1 and 2.
	if g[0] == 0x2002 {
		if inner := blockedV4(v4Int(g[1], g[2])); inner != "" {
			return "6to4 " + inner
		}
		return ""
	}
	switch {
	case g[0]&0xfe00 == 0xfc00:
		return "unique local (fc00::/7)"
	case g[0]&0xffc0 == 0xfe80:
		return "link-local (fe80::/10)"
	case g[0]&0xffc0 == 0xfec0:
		return "site-local (fec0::/10)"
	case g[0]&0xff00 == 0xff00:
		return "multicast"
	case g[0] == 0x2001 && g[1] == 0x0db8:
		return "documentation"
	case g[0] == 0x2001 && g[1] == 0:
		// Teredo tunnels to an IPv4 address hidden in the suffix.
		return "Teredo (2001::/32)"
	case g[0] == 0x0100 && g[1] == 0 && g[2] == 0 && g[3] == 0:
		return "discard-only"
	}
	return ""
}

// ipFamily is node:net isIP: 4, 6 (zone ids allowed) or 0.
func ipFamily(s string) (netip.Addr, int) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, 0
	}
	if a.Is4() {
		return a, 4
	}
	return a, 6
}

// BlockedAddressReason says why an address is off limits, "" for a public
// unicast address.
func BlockedAddressReason(ip string) string {
	bare := strings.TrimSuffix(strings.TrimPrefix(ip, "["), "]")
	a, family := ipFamily(bare)
	switch family {
	case 4:
		b := a.As4()
		return blockedV4(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
	case 6:
		return blockedV6(a.WithZone(""))
	}
	return "not an IP address"
}

func IsBlockedAddress(ip string) bool { return BlockedAddressReason(ip) != "" }

// --- URLs ------------------------------------------------------------------------------

var blockedSuffixes = []string{".localhost", ".internal", ".local", ".localdomain", ".home.arpa", ".lan"}

// AssertPublicURL runs the checks that need no DNS: scheme, credentials, port,
// obviously local names and literal IPs. It returns the parsed URL, normalized
// the way the WHATWG URL parser does (numeric hosts like 2130706433, 0x7f.1 and
// 127.1 become dotted IPv4).
func AssertPublicURL(raw string) (*URL, error) {
	u, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errf(CodeBlockedProtocol, "Only http and https are allowed, not %s:", u.Scheme)
	}
	if u.Username != "" || u.Password != "" {
		return nil, errf(CodeBlockedCredentials, "URLs with credentials are not allowed")
	}
	port := u.EffectivePort()
	if blockedPorts[port] {
		return nil, errf(CodeBlockedPort, "Port %d is not allowed", port)
	}
	host := u.HostOf()
	if host == "" {
		return nil, errf(CodeInvalidURL, "URL has no host")
	}
	if host == "localhost" || host == "metadata.google.internal" || hasBlockedSuffix(host) {
		return nil, errf(CodeBlockedHost, "Host %s is internal", host)
	}
	if _, family := ipFamily(host); family != 0 {
		if why := BlockedAddressReason(host); why != "" {
			return nil, errf(CodeBlockedAddress, "Address %s is %s", host, why)
		}
	} else if !strings.Contains(host, ".") {
		// Single-label names (http://router/) resolve through the local search domain.
		return nil, errf(CodeBlockedHost, "Host %s is not a public name", host)
	}
	return u, nil
}

func hasBlockedSuffix(host string) bool {
	for _, s := range blockedSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// ResolvedAddress is one DNS answer.
type ResolvedAddress struct {
	Address string `json:"address"`
	Family  int    `json:"family"`
}

// Resolver is dns.lookup(host, { all: true }) shaped, injectable for tests.
type Resolver func(ctx context.Context, hostname string) ([]ResolvedAddress, error)

// SystemResolver asks the OS resolver (hosts file included), in its order.
func SystemResolver(ctx context.Context, hostname string) ([]ResolvedAddress, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedAddress, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap()
		family := 6
		if a.Is4() {
			family = 4
		}
		out = append(out, ResolvedAddress{Address: a.String(), Family: family})
	}
	return out, nil
}

// ResolvePublic resolves a host and refuses it when ANY of its addresses is
// internal: a name that answers with both a public and a private address is a
// rebinding setup. It returns the first address.
func ResolvePublic(ctx context.Context, hostname string, resolver Resolver) (ResolvedAddress, error) {
	if resolver == nil {
		resolver = SystemResolver
	}
	if _, family := ipFamily(hostname); family != 0 {
		if why := BlockedAddressReason(hostname); why != "" {
			return ResolvedAddress{}, errf(CodeBlockedAddress, "Address %s is %s", hostname, why)
		}
		return ResolvedAddress{Address: hostname, Family: family}, nil
	}
	rows, err := resolver(ctx, hostname)
	if err != nil {
		return ResolvedAddress{}, errf(CodeDNS, "Could not resolve %s: %s", hostname, err.Error())
	}
	if len(rows) == 0 {
		return ResolvedAddress{}, errf(CodeDNS, "%s has no addresses", hostname)
	}
	for _, r := range rows {
		if why := BlockedAddressReason(r.Address); why != "" {
			return ResolvedAddress{}, errf(CodeBlockedAddress, "%s resolves to %s (%s)", hostname, r.Address, why)
		}
	}
	return rows[0], nil
}

// publicOnlyTransport refuses internal targets on every request.
type publicOnlyTransport struct {
	base     http.RoundTripper
	resolver Resolver
}

func (t publicOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := AssertPublicURL(req.URL.String())
	if err != nil {
		return nil, err
	}
	if _, err := ResolvePublic(req.Context(), u.HostOf(), t.resolver); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// ErrRedirect is what a public-only client returns instead of following a redirect.
var ErrRedirect = fmt.Errorf("redirect mode is set to error")

// PublicOnlyClient is an http.Client for long-lived clients (an MCP connection)
// whose URL came from chat. The host is re-resolved each call, so a name that
// later starts pointing inside is refused too. Redirects are not followed: a
// public server could otherwise bounce the request inward.
func PublicOnlyClient(base http.RoundTripper, resolver Resolver) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport: publicOnlyTransport{base: base, resolver: resolver},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirect
		},
	}
}
