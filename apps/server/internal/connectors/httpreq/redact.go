package httpreq

import (
	"bytes"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

const Redacted = "[redacted]"

// Redact removes the key from text the model or the owner will see: the raw
// value, its query-escaped and path-escaped forms, and its JSON-escaped form
// (also with "/" written as "\/", as PHP's json_encode does).
func Redact(s, secret string) string {
	if secret == "" || s == "" {
		return s
	}
	forms := []string{secret, url.QueryEscape(secret), url.PathEscape(secret), jsonEscaped(secret, false), jsonEscaped(secret, true)}
	for _, f := range forms[3:] {
		forms = append(forms, strings.ReplaceAll(f, "/", `\/`))
	}
	// Longest first, so a longer escaped form isn't cut up by a shorter one.
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	seen := map[string]bool{}
	for _, f := range forms {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		s = strings.ReplaceAll(s, f, Redacted)
	}
	return s
}

// jsonEscaped is the key as it appears inside a JSON string, without quotes.
func jsonEscaped(secret string, escapeHTML bool) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(escapeHTML)
	if err := enc.Encode(secret); err != nil {
		return ""
	}
	out := strings.TrimSuffix(b.String(), "\n")
	return strings.TrimSuffix(strings.TrimPrefix(out, `"`), `"`)
}
