package safenet_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/safenet"
)

// Ports packages/core/src/net/ssrf.test.ts.

func TestBlocksInternalAddresses(t *testing.T) {
	for _, ip := range []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1", "100.127.255.255",
		"0.0.0.0", "0.1.2.3", "224.0.0.1", "239.255.255.250", "255.255.255.255", "::1", "::", "fc00::1", "fd12:3456::1", "fe80::1", "fe80::1%eth0",
		"ff02::1", "::ffff:127.0.0.1", "::ffff:7f00:1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::127.0.0.1", "64:ff9b::a9fe:a9fe",
		"2002:7f00:1::", "[::1]", "::ffff:0:127.0.0.1", "::ffff:0:10.0.0.1", "::ffff:0:a9fe:a9fe", "64:ff9b:1::a9fe:a9fe", "64:ff9b:1:abcd::808:808",
		"2001::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2",
	} {
		if !safenet.IsBlockedAddress(ip) {
			t.Errorf("allowed %s", ip)
		}
	}
}

func TestAllowsPublicAddresses(t *testing.T) {
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "172.32.0.1", "100.128.0.1", "93.184.216.34", "2606:4700:4700::1111", "::ffff:8.8.8.8", "2002:808:808::", "::ffff:0:8.8.8.8", "2001:4860:4860::8888"} {
		if why := safenet.BlockedAddressReason(ip); why != "" {
			t.Errorf("blocked %s: %s", ip, why)
		}
	}
}

func TestTreatsGarbageAsBlocked(t *testing.T) {
	if !safenet.IsBlockedAddress("example.com") || !safenet.IsBlockedAddress("1.2.3") {
		t.Fatal("garbage")
	}
}

func code(raw string) string {
	_, err := safenet.AssertPublicURL(raw)
	if err == nil {
		return "ok"
	}
	var e *safenet.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return err.Error()
}

func expectCodes(t *testing.T, cases map[string]string) {
	t.Helper()
	for u, want := range cases {
		if got := code(u); got != want {
			t.Errorf("%s: %s, want %s", u, got, want)
		}
	}
}

func TestAllowsPublicHTTPURLs(t *testing.T) {
	expectCodes(t, map[string]string{"https://example.com/a?b=1": "ok", "http://example.com:8080/": "ok"})
}

func TestRefusesOtherSchemesAndCredentials(t *testing.T) {
	expectCodes(t, map[string]string{
		"file:///etc/passwd":             "blocked_protocol",
		"ftp://example.com/":             "blocked_protocol",
		"gopher://example.com/":          "blocked_protocol",
		"https://user:pass@example.com/": "blocked_credentials",
		"not a url":                      "invalid_url",
	})
}

func TestRefusesInternalNamesAndLiteralInternalAddresses(t *testing.T) {
	expectCodes(t, map[string]string{
		"http://localhost/":                       "blocked_host",
		"http://api.localhost/":                   "blocked_host",
		"http://host.docker.internal:4800/":       "blocked_host",
		"http://printer.local/":                   "blocked_host",
		"http://router/":                          "blocked_host",
		"http://127.0.0.1/":                       "blocked_address",
		"http://169.254.169.254/latest/meta-data": "blocked_address",
		"http://[::1]/":                           "blocked_address",
		"http://[::ffff:127.0.0.1]/":              "blocked_address",
	})
}

func TestCatchesNumericHostTricks(t *testing.T) {
	expectCodes(t, map[string]string{
		"http://2130706433/": "blocked_address",
		"http://0x7f.0.0.1/": "blocked_address",
		"http://0177.0.0.1/": "blocked_address",
		"http://127.1/":      "blocked_address",
	})
}

func TestRefusesNonHTTPServicePorts(t *testing.T) {
	expectCodes(t, map[string]string{"http://example.com:22/": "blocked_port", "http://example.com:6379/": "blocked_port", "http://example.com:25/": "blocked_port"})
}

func TestURLNormalization(t *testing.T) {
	for in, want := range map[string]string{
		"HTTPS://Example.COM":             "https://example.com/",
		"https://example.com:443/a":       "https://example.com/a",
		"http://example.com/a/../b?x=1#f": "http://example.com/b?x=1#f",
		"https://bücher.example/":         "https://xn--bcher-kva.example/",
	} {
		u, err := safenet.ParseURL(in)
		if err != nil || u.Href() != want {
			t.Errorf("%s: %v %v", in, u, err)
		}
	}
	base, _ := safenet.ParseURL("https://a.test/x/y")
	for ref, want := range map[string]string{"/b": "https://a.test/b", "c": "https://a.test/x/c", "https://c.test/final": "https://c.test/final", "//d.test/z": "https://d.test/z"} {
		u, err := base.Resolve(ref)
		if err != nil || u.Href() != want {
			t.Errorf("%s: %v %v", ref, u, err)
		}
	}
}

func resolver(m map[string][]string) safenet.Resolver {
	return func(_ context.Context, h string) ([]safenet.ResolvedAddress, error) {
		rows, ok := m[h]
		if !ok {
			return nil, errors.New("ENOTFOUND")
		}
		out := []safenet.ResolvedAddress{}
		for _, a := range rows {
			f := 4
			if strings.Contains(a, ":") {
				f = 6
			}
			out = append(out, safenet.ResolvedAddress{Address: a, Family: f})
		}
		return out, nil
	}
}

func resolveCode(host string, r safenet.Resolver) string {
	_, err := safenet.ResolvePublic(context.Background(), host, r)
	var e *safenet.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "ok"
}

func TestResolvePublic(t *testing.T) {
	got, err := safenet.ResolvePublic(context.Background(), "example.com", resolver(map[string][]string{"example.com": {"93.184.216.34"}}))
	if err != nil || got != (safenet.ResolvedAddress{Address: "93.184.216.34", Family: 4}) {
		t.Fatal(got, err)
	}
	if c := resolveCode("evil.test", resolver(map[string][]string{"evil.test": {"127.0.0.1"}})); c != "blocked_address" {
		t.Fatal(c)
	}
	if c := resolveCode("mixed.test", resolver(map[string][]string{"mixed.test": {"93.184.216.34", "10.0.0.5"}})); c != "blocked_address" {
		t.Fatal(c)
	}
	if c := resolveCode("v6.test", resolver(map[string][]string{"v6.test": {"::ffff:192.168.0.1"}})); c != "blocked_address" {
		t.Fatal(c)
	}
	if c := resolveCode("nope.test", resolver(nil)); c != "dns" {
		t.Fatal(c)
	}
	if c := resolveCode("empty.test", resolver(map[string][]string{"empty.test": {}})); c != "dns" {
		t.Fatal(c)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okResponse(r *http.Request) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: r}
}

func TestPublicOnlyClientPassesPublicTargetsWithoutFollowingRedirects(t *testing.T) {
	var seen *http.Request
	base := roundTrip(func(r *http.Request) (*http.Response, error) {
		seen = r
		if r.URL.Path == "/redirect" {
			return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": {"http://127.0.0.1/"}}, Request: r}, nil
		}
		return okResponse(r), nil
	})
	c := safenet.PublicOnlyClient(base, resolver(map[string][]string{"example.com": {"93.184.216.34"}}))
	res, err := c.Post("https://example.com/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if string(b) != "ok" || seen.Method != "POST" {
		t.Fatal(string(b))
	}
	if _, err := c.Get("https://example.com/redirect"); !errors.Is(err, safenet.ErrRedirect) {
		t.Fatal(err)
	}
}

func TestPublicOnlyClientRechecksTheAddressOnEveryCall(t *testing.T) {
	address := "93.184.216.34"
	r := func(context.Context, string) ([]safenet.ResolvedAddress, error) {
		return []safenet.ResolvedAddress{{Address: address, Family: 4}}, nil
	}
	c := safenet.PublicOnlyClient(roundTrip(func(r *http.Request) (*http.Response, error) { return okResponse(r), nil }), r)
	if _, err := c.Get("https://example.com/mcp"); err != nil {
		t.Fatal(err)
	}
	address = "192.168.1.10"
	var e *safenet.Error
	if _, err := c.Get("https://example.com/mcp"); !errors.As(err, &e) {
		t.Fatal(err)
	}
	if _, err := c.Get("http://127.0.0.1/mcp"); !errors.As(err, &e) {
		t.Fatal(err)
	}
}
