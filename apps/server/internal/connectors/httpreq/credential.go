package httpreq

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/safenet"
)

const (
	maxHeaders        = 30
	maxHeaderValueLen = 2000
)

// Headers the model may never set: hop-by-hop and framing headers, the
// proxy's credentials, ranges (a slice of an echoed key gets past Redact),
// and method overrides (a GET or an approved POST that some servers run as
// a DELETE).
var reservedHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true, "connection": true, "upgrade": true,
	"te": true, "trailer": true, "proxy-authorization": true, "proxy-connection": true,
	"range": true, "if-range": true,
	"x-http-method-override": true, "x-http-method": true, "x-method-override": true,
}

// Attach returns the URL and headers to send: the key in its header, or in
// its query parameter (any parameter of that name the caller sent is dropped).
func Attach(cfg Config, secret string, u *safenet.URL, headers map[string]string) (href string, out map[string]string) {
	out = make(map[string]string, len(headers)+1)
	for k, v := range headers {
		out[k] = v
	}
	switch cfg.Auth {
	case "header":
		for k := range out {
			if strings.EqualFold(k, cfg.HeaderName) {
				delete(out, k)
			}
		}
		out[strings.ToLower(cfg.HeaderName)] = strings.ReplaceAll(cfg.ValueTemplate, "{value}", secret)
	case "query":
		c := *u
		q := dropParam(c.RawQuery, cfg.QueryParam)
		if q != "" {
			q += "&"
		}
		c.RawQuery = q + cfg.QueryParam + "=" + url.QueryEscape(secret)
		return c.Href(), out
	}
	return u.Href(), out
}

// dropParam removes every name=value pair whose decoded name is name, keeping
// the other pairs as written and in order.
func dropParam(rawQuery, name string) string {
	if rawQuery == "" {
		return ""
	}
	var kept []string
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		key, _, _ := strings.Cut(pair, "=")
		if dec, err := url.QueryUnescape(key); err == nil {
			key = dec
		}
		if key == name {
			continue
		}
		kept = append(kept, pair)
	}
	return strings.Join(kept, "&")
}

// CheckHeaders refuses headers the model may not set.
func CheckHeaders(cfg Config, headers map[string]string) error {
	if len(headers) > maxHeaders {
		return fmt.Errorf("Send at most %d headers.", maxHeaders)
	}
	for name, value := range headers {
		lower := strings.ToLower(name)
		bad := reservedHeaders[lower] ||
			(cfg.Auth == "header" && strings.EqualFold(name, cfg.HeaderName)) ||
			!headerNameRe.MatchString(name) ||
			len(value) > maxHeaderValueLen ||
			strings.IndexFunc(value, func(r rune) bool { return (r < 0x20 && r != '\t') || r == 0x7f }) >= 0
		if bad {
			return fmt.Errorf("Header %q can't be set here.", name)
		}
	}
	return nil
}
