package httpreq

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

func TestParseDomains(t *testing.T) {
	got, err := ParseDomains("api.example.com, *.example.com api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []Domain{{Host: "api.example.com"}, {Host: "example.com", Wildcard: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if d, err := ParseDomains("*"); err != nil || !d[0].Any {
		t.Fatal(d, err)
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "a" + string(rune('a'+i)) + ".example.com"
	}
	for _, bad := range []string{"https://api.example.com", "api.example.com/v1", "api.example.com:8443", "localhost", "10.0.0.1", "*.com", "", strings.Join(many, ",")} {
		if _, err := ParseDomains(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestAllowed(t *testing.T) {
	list, _ := ParseDomains("*.example.com, api.other.com")
	for host, want := range map[string]bool{"a.b.example.com": true, "x.example.com": true, "example.com": false, "badexample.com": false, "api.other.com": true, "other.com": false} {
		if Allowed(list, host) != want {
			t.Errorf("%s: want %v", host, want)
		}
	}
	any, _ := ParseDomains("*")
	if !Allowed(any, "anything.test") {
		t.Error("* didn't match")
	}
}

func TestConfigFrom(t *testing.T) {
	cfg, err := ConfigFrom(kit.O("domains", "api.example.com"), "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth != "header" || cfg.HeaderName != "Authorization" || cfg.ValueTemplate != "Bearer {value}" || cfg.Policy != "ask" {
		t.Fatalf("%+v", cfg)
	}
	cfg, err = ConfigFrom(kit.O("domains", "api.example.com", "policy", "sometimes"), "secret-key")
	if err != nil || cfg.Policy != "ask" {
		t.Fatal(cfg, err)
	}
	for _, c := range []struct {
		cfg    []any
		secret string
		want   string
	}{
		{[]any{}, "secret-key", "Set the allowed domains."},
		{[]any{"domains", "api.example.com", "auth", "query"}, "secret-key", "Set the query parameter name."},
		{[]any{"domains", "api.example.com"}, "", "This connector needs a key."},
		{[]any{"domains", "*"}, "secret-key", "A key needs named domains. * only works with auth none."},
	} {
		_, err := ConfigFrom(kit.O(c.cfg...), c.secret)
		if err == nil || err.Error() != c.want {
			t.Errorf("%v: %v, want %q", c.cfg, err, c.want)
		}
	}
	if _, err := ConfigFrom(kit.O("domains", "*", "auth", "none"), ""); err != nil {
		t.Fatal(err)
	}
}

func TestManifestParseConfigNeedsOnlyDomains(t *testing.T) {
	_, err := Manifest.ParseConfig(nil)
	var ze *zod.Error
	if !errors.As(err, &ze) || len(ze.Issues) != 1 || ze.Issues[0].PathString() != "domains" {
		t.Fatal(err)
	}
}

func mustURL(t *testing.T, s string) *safenet.URL {
	t.Helper()
	u, err := safenet.ParseURL(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAttach(t *testing.T) {
	header := Config{Auth: "header", HeaderName: "xi-api-key", ValueTemplate: "{value}"}
	href, out := Attach(header, "k123", mustURL(t, "https://api.example.com/v1?a=1"), map[string]string{"accept": "application/json"})
	if href != "https://api.example.com/v1?a=1" || out["xi-api-key"] != "k123" || out["accept"] != "application/json" {
		t.Fatal(href, out)
	}
	bearer := Config{Auth: "header", HeaderName: "Authorization", ValueTemplate: "Bearer {value}"}
	if _, out := Attach(bearer, "k123", mustURL(t, "https://api.example.com/"), nil); out["authorization"] != "Bearer k123" {
		t.Fatal(out)
	}
	query := Config{Auth: "query", QueryParam: "api_key"}
	href, _ = Attach(query, "a b&c", mustURL(t, "https://api.example.com/v1?z=1&api_key=mine&a=2&api%5Fkey=x"), nil)
	if href != "https://api.example.com/v1?z=1&a=2&api_key=a+b%26c" {
		t.Fatal(href)
	}
	href, _ = Attach(query, "k123", mustURL(t, "https://api.example.com/v1"), nil)
	if href != "https://api.example.com/v1?api_key=k123" {
		t.Fatal(href)
	}
}

func TestCheckHeaders(t *testing.T) {
	cfg := Config{Auth: "header", HeaderName: "Authorization"}
	if err := CheckHeaders(cfg, map[string]string{"accept": "application/json", "x-trace": "a\tb"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Host", "content-length", "Transfer-Encoding", "connection", "upgrade", "TE", "trailer", "Proxy-Authorization", "proxy-connection", "authorization", "AUTHORIZATION", "bad header", "bad:name"} {
		err := CheckHeaders(cfg, map[string]string{name: "x"})
		if err == nil || err.Error() != `Header "`+name+`" can't be set here.` {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, value := range []string{"a\r\nb", strings.Repeat("x", 2001)} {
		if CheckHeaders(cfg, map[string]string{"x-a": value}) == nil {
			t.Errorf("value %q passed", value[:5])
		}
	}
	// With a query key, Authorization is the model's to set.
	if err := CheckHeaders(Config{Auth: "query"}, map[string]string{"Authorization": "x"}); err != nil {
		t.Fatal(err)
	}
	many := map[string]string{}
	for i := 0; i < 31; i++ {
		many["x-h"+string(rune('a'+i))] = "1"
	}
	if CheckHeaders(cfg, many) == nil {
		t.Error("31 headers passed")
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("key=abcdefgh1 and abcdefgh1", "abcdefgh1"); got != "key=[redacted] and [redacted]" {
		t.Fatal(got)
	}
	if got := Redact("path /v1/a+b%2Fc/x and query a%2Bb%2Fc", "a+b/c"); got != "path /v1/[redacted]/x and query [redacted]" {
		t.Fatal(got)
	}
	if got := Redact(`{"echo":"sk\"quoted<1>"}`, `sk"quoted<1>`); got != `{"echo":"[redacted]"}` {
		t.Fatal(got)
	}
	if got := Redact(`{"echo":"sk\"quoted<1>"}`, `sk"quoted<1>`); got != `{"echo":"[redacted]"}` {
		t.Fatal(got)
	}
	if Redact("text", "") != "text" {
		t.Fatal("empty secret changed text")
	}
}
