package httpreq

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Domain is one allowed host.
type Domain struct {
	Host     string // lowercase ASCII, no trailing dot
	Wildcard bool   // *.host: any subdomain, not the apex
	Any      bool   // "*"
}

func (d Domain) String() string {
	switch {
	case d.Any:
		return "*"
	case d.Wildcard:
		return "*." + d.Host
	}
	return d.Host
}

const maxDomains = 20

var (
	hostRe      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	allDigitsRe = regexp.MustCompile(`^[0-9]+$`)
	splitRe     = regexp.MustCompile(`[,\s]+`)
)

// ParseDomains reads a list like "api.example.com, *.example.com": split on
// commas and whitespace, at most 20 entries, duplicates dropped.
func ParseDomains(s string) ([]Domain, error) {
	var out []Domain
	seen := map[Domain]bool{}
	for _, raw := range splitRe.Split(s, -1) {
		if raw == "" {
			continue
		}
		d, err := parseDomain(raw)
		if err != nil {
			return nil, err
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("Set the allowed domains.")
	}
	if len(out) > maxDomains {
		return nil, fmt.Errorf("Use at most %d domains.", maxDomains)
	}
	return out, nil
}

func parseDomain(raw string) (Domain, error) {
	entry := strings.TrimSuffix(strings.ToLower(raw), ".")
	if entry == "*" {
		return Domain{Any: true}, nil
	}
	d := Domain{Host: entry}
	if rest, ok := strings.CutPrefix(entry, "*."); ok {
		d = Domain{Host: rest, Wildcard: true}
	}
	labels := strings.Split(d.Host, ".")
	if !hostRe.MatchString(d.Host) || allDigitsRe.MatchString(labels[len(labels)-1]) {
		return Domain{}, fmt.Errorf("%q isn't a host name like api.example.com or *.example.com.", raw)
	}
	return d, nil
}

// Allowed reports whether host (as safenet's URL.HostOf gives it: IDNA ASCII,
// lowercase) is in the list.
func Allowed(domains []Domain, host string) bool {
	host = strings.TrimSuffix(host, ".")
	for _, d := range domains {
		switch {
		case d.Any:
			return true
		case d.Wildcard:
			if strings.HasSuffix(host, "."+d.Host) {
				return true
			}
		case host == d.Host:
			return true
		}
	}
	return false
}

func validDomains(s string) bool {
	_, err := ParseDomains(s)
	return err == nil
}

func joinDomains(domains []Domain) string {
	parts := make([]string, len(domains))
	for i, d := range domains {
		parts[i] = d.String()
	}
	return strings.Join(parts, ", ")
}
