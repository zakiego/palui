package httpreq_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/httpreq"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

const key = "sk_test_0123456789"

type answer struct {
	status  int
	headers map[string]string
	body    string
	err     error
}

// fakeNet answers by URL and records every request. Unknown URLs get a 404.
type fakeNet struct {
	mu     sync.Mutex
	routes map[string]answer
	seen   []safenet.TransportRequest
}

func (f *fakeNet) transport(req safenet.TransportRequest) (*safenet.TransportResponse, error) {
	f.mu.Lock()
	f.seen = append(f.seen, req)
	f.mu.Unlock()
	a, ok := f.routes[req.URL.Href()]
	if !ok {
		a = answer{status: 404, body: `{"error":"not found"}`, headers: map[string]string{"content-type": "application/json"}}
	}
	if a.err != nil {
		return nil, a.err
	}
	h := http.Header{}
	for k, v := range a.headers {
		h.Set(k, v)
	}
	status := a.status
	if status == 0 {
		status = 200
	}
	return &safenet.TransportResponse{Status: status, Header: h, Body: strings.NewReader(a.body), Cancel: func() {}}, nil
}

func (f *fakeNet) calls() []safenet.TransportRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]safenet.TransportRequest(nil), f.seen...)
}

func publicResolver(_ context.Context, _ string) ([]safenet.ResolvedAddress, error) {
	return []safenet.ResolvedAddress{{Address: "93.184.216.34", Family: 4}}, nil
}

func newNet(routes map[string]answer) (*fakeNet, httpreq.Options) {
	f := &fakeNet{routes: routes}
	return f, httpreq.Options{Transport: f.transport, Resolver: publicResolver}
}

func cfgOf(t *testing.T, secret string, kv ...any) httpreq.Config {
	t.Helper()
	cfg, err := httpreq.ConfigFrom(kit.O(kv...), secret)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func run(t *testing.T, set *connectors.ToolSet, name string, input any) conntest.Value {
	t.Helper()
	b, _ := json.Marshal(input)
	return conntest.Call(t, set, name, string(b))
}

var xiKey = []any{"domains", "api.example.com, *.cdn.example.com", "headerName", "xi-api-key", "valueTemplate", "{value}"}

func TestHeaderKeyIsAttachedAndTheModelCantSetIt(t *testing.T) {
	f, o := newNet(map[string]answer{"https://api.example.com/v1/user": {body: `{"tier":"free"}`, headers: map[string]string{"content-type": "application/json"}}})
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/v1/user", "headers": map[string]string{"accept": "application/json"}})
	if !got.B("ok") || got.N("status") != 200 || got.S("body") != `{"tier":"free"}` || got.S("contentType") != "application/json" {
		t.Fatal(got)
	}
	seen := f.calls()
	if len(seen) != 1 || seen[0].Headers["xi-api-key"] != key || seen[0].Headers["accept"] != "application/json" || seen[0].Method != "GET" {
		t.Fatalf("%+v", seen)
	}
	got = run(t, set, "http_request", map[string]any{"url": "https://api.example.com/v1/user", "headers": map[string]string{"XI-API-KEY": "mine"}})
	if got.B("ok") || got.S("error") != `Header "XI-API-KEY" can't be set here.` || len(f.calls()) != 1 {
		t.Fatal(got)
	}
}

func TestQueryKeyIsAttachedAndRedacted(t *testing.T) {
	f, o := newNet(map[string]answer{"https://api.example.com/v1?a=1&api_key=" + key: {body: "fine", headers: map[string]string{"content-type": "text/plain"}}})
	set := httpreq.Tools(cfgOf(t, key, "domains", "api.example.com", "auth", "query", "queryParam", "api_key"), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/v1?a=1&api_key=guess"})
	if !got.B("ok") || got.S("body") != "fine" {
		t.Fatal(got)
	}
	if u := got.S("url"); strings.Contains(u, key) || u != "https://api.example.com/v1?a=1&api_key=[redacted]" {
		t.Fatal(u)
	}
	if seen := f.calls(); seen[0].URL.RawQuery != "a=1&api_key="+key {
		t.Fatal(seen[0].URL.RawQuery)
	}
}

func TestRefusesOtherHostsAndPlainHTTPBeforeAnyRequest(t *testing.T) {
	f, o := newNet(nil)
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://evil.test/steal"})
	if got.S("error") != "evil.test isn't in this connector's allowed domains (api.example.com, *.cdn.example.com)." {
		t.Fatal(got)
	}
	got = run(t, set, "http_request", map[string]any{"url": "http://api.example.com/v1"})
	if got.S("error") != "The key is only sent over https." {
		t.Fatal(got)
	}
	got = run(t, set, "http_request", map[string]any{"url": "https://cdn.example.com/x"})
	if got.B("ok") || !strings.Contains(got.S("error"), "isn't in this connector's allowed domains") {
		t.Fatal(got)
	}
	if len(f.calls()) != 0 {
		t.Fatal("the transport was called")
	}
}

func TestReadRedirectsStayInsideTheAllowedDomains(t *testing.T) {
	f, o := newNet(map[string]answer{
		"https://api.example.com/a":         {status: 302, headers: map[string]string{"location": "https://img.cdn.example.com/b"}},
		"https://img.cdn.example.com/b":     {body: "done", headers: map[string]string{"content-type": "text/plain"}},
		"https://api.example.com/leave":     {status: 301, headers: map[string]string{"location": "https://collector.test/"}},
		"https://api.example.com/downgrade": {status: 302, headers: map[string]string{"location": "http://api.example.com/plain"}},
	})
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/a"})
	if !got.B("ok") || got.S("body") != "done" || got.S("url") != "https://img.cdn.example.com/b" {
		t.Fatal(got)
	}
	seen := f.calls()
	if len(seen) != 2 || seen[0].Headers["xi-api-key"] != key || seen[1].Headers["xi-api-key"] != key {
		t.Fatalf("%+v", seen)
	}
	got = run(t, set, "http_request", map[string]any{"url": "https://api.example.com/leave"})
	if got.B("ok") || got.N("status") != 301 || got.S("error") != "Redirected to collector.test, which isn't in the allowed domains. Not followed." || len(f.calls()) != 3 {
		t.Fatal(got, len(f.calls()))
	}
	got = run(t, set, "http_request", map[string]any{"url": "https://api.example.com/downgrade"})
	if got.B("ok") || !strings.Contains(got.S("error"), "only sent over https") || len(f.calls()) != 4 {
		t.Fatal(got)
	}
}

func TestStopsAfterFiveRedirects(t *testing.T) {
	routes := map[string]answer{}
	for i := 0; i < 8; i++ {
		routes["https://api.example.com/"+string(rune('0'+i))] = answer{status: 302, headers: map[string]string{"location": "/" + string(rune('0'+i+1))}}
	}
	f, o := newNet(routes)
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/0"})
	if got.S("error") != "More than 5 redirects" || len(f.calls()) != 6 {
		t.Fatal(got, len(f.calls()))
	}
}

func TestWritesNeverFollowRedirects(t *testing.T) {
	f, o := newNet(map[string]answer{"https://api.example.com/items": {status: 307, headers: map[string]string{"location": "https://api.example.com/items2"}}})
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request_write", map[string]any{"url": "https://api.example.com/items", "method": "POST", "json": map[string]any{"name": "a"}})
	if got.N("status") != 307 || got.S("headers.location") != "https://api.example.com/items2" || got.S("note") != "Redirects aren't followed for writes." {
		t.Fatal(got)
	}
	seen := f.calls()
	if len(seen) != 1 || seen[0].Method != "POST" || *seen[0].Body != `{"name":"a"}` || seen[0].Headers["content-type"] != "application/json" {
		t.Fatalf("%+v", seen)
	}
}

func TestTheKeyIsRedactedEverywhere(t *testing.T) {
	_, o := newNet(map[string]answer{
		"https://api.example.com/echo": {body: `{"you sent":"` + key + `"}`, headers: map[string]string{"content-type": "application/json", "location": "https://api.example.com/?k=" + key, "set-cookie": "s=1", "x-ratelimit-remaining": "9"}},
		"https://api.example.com/fail": {err: errors.New("dial failed while sending " + key)},
	})
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/echo"})
	if strings.Contains(got.String(), key) || got.S("body") != `{"you sent":"[redacted]"}` || got.S("headers.location") != "https://api.example.com/?k=[redacted]" {
		t.Fatal(got)
	}
	if got.Has("headers.set-cookie") || got.S("headers.x-ratelimit-remaining") != "9" {
		t.Fatal(got.JSON("headers"))
	}
	got = run(t, set, "http_request", map[string]any{"url": "https://api.example.com/fail"})
	if got.B("ok") || strings.Contains(got.String(), key) || !strings.Contains(got.S("error"), "[redacted]") {
		t.Fatal(got)
	}
}

func TestBodiesAreCutAndBinaryIsNotShown(t *testing.T) {
	big := `"` + strings.Repeat("a", 40_000) + `"`
	_, o := newNet(map[string]answer{
		"https://api.example.com/big":     {body: big, headers: map[string]string{"content-type": "application/json"}},
		"https://api.example.com/img.png": {body: "\x89PNG\r\n", headers: map[string]string{"content-type": "image/png"}},
	})
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/big"})
	if !got.B("truncated") || got.S("body") != big[:16_000]+"\n[truncated]" {
		t.Fatal(len(got.S("body")))
	}
	got = run(t, set, "http_request", map[string]any{"url": "https://api.example.com/img.png"})
	if got.S("body") != "" || got.S("note") != "Binary response (image/png, 6 bytes) isn't shown." {
		t.Fatal(got)
	}
}

func TestNon2xxStillReturnsTheBody(t *testing.T) {
	_, o := newNet(nil)
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	got := run(t, set, "http_request", map[string]any{"url": "https://api.example.com/missing"})
	if got.B("ok") || got.N("status") != 404 || got.S("body") != `{"error":"not found"}` {
		t.Fatal(got)
	}
}

func TestEachToolKeepsToItsMethods(t *testing.T) {
	f, o := newNet(nil)
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
	for _, c := range []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"http_request", map[string]any{"url": "https://api.example.com/", "method": "POST"}, "http_request only sends GET or HEAD. Use http_request_write for POST, PUT, PATCH or DELETE."},
		{"http_request_write", map[string]any{"url": "https://api.example.com/", "method": "GET"}, "http_request_write sends POST, PUT, PATCH or DELETE. Use http_request for GET or HEAD."},
		{"http_request_write", map[string]any{"url": "https://api.example.com/"}, "http_request_write sends POST, PUT, PATCH or DELETE. Use http_request for GET or HEAD."},
		{"http_request_write", map[string]any{"url": "https://api.example.com/", "method": "POST", "json": 1, "body": "x"}, "Send json or body, not both."},
		{"http_request_write", map[string]any{"url": "https://api.example.com/", "method": "PUT", "body": strings.Repeat("x", 300*1024)}, "The request body is over 256 KiB."},
	} {
		if got := run(t, set, c.tool, c.input); got.B("ok") || got.S("error") != c.want {
			t.Errorf("%s %v: %s", c.tool, c.input["method"], got.S("error"))
		}
	}
	if len(f.calls()) != 0 {
		t.Fatal("a refused call reached the transport")
	}
}

func TestPolicyFlags(t *testing.T) {
	ask := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, httpreq.Options{})
	allow := httpreq.Tools(cfgOf(t, key, append(xiKey, "policy", "allow")...), "Example", key, httpreq.Options{})
	if got := conntest.Classes(ask); !reflect.DeepEqual(got, []string{"http_request:read", "http_request_write:write"}) {
		t.Fatal(got)
	}
	for _, name := range []string{"http_request", "http_request_write"} {
		a, _ := ask.Get(name)
		l, _ := allow.Get(name)
		if a.NeedsApproval == nil || !a.NeedsApproval(nil) || l.NeedsApproval != nil {
			t.Errorf("%s: NeedsApproval", name)
		}
		if !a.Untrusted || !l.Untrusted || a.Data != connectors.DataShared || a.GuestSafe || a.Preview == nil {
			t.Errorf("%s: flags %+v", name, a)
		}
		if l.FlowApproval != "" {
			t.Errorf("%s: allow FlowApproval %q", name, l.FlowApproval)
		}
	}
	r, _ := ask.Get("http_request")
	w, _ := ask.Get("http_request_write")
	if r.FlowApproval != "" || w.FlowApproval != "every_run" || r.Title != "Example: read" || w.Title != "Example: send" {
		t.Fatalf("%q %q %q %q", r.FlowApproval, w.FlowApproval, r.Title, w.Title)
	}
	none := httpreq.Tools(cfgOf(t, "", "domains", "*", "auth", "none"), "Open", "", httpreq.Options{})
	n, _ := none.Get("http_request")
	if strings.Contains(n.Tool.Description, "stored key") || !strings.Contains(r.Tool.Description, "stored key") {
		t.Fatal("key sentence")
	}
}

func TestSharedChatsNeverGetTheTools(t *testing.T) {
	for _, agents := range [][]string{nil, {"agt_one"}} {
		rt := httpreq.NewRuntime(httpreq.Options{})
		c := conntest.Ctx(t, nil, "http-example", kit.O(xiKey...), map[string]string{"value": key})
		h, err := rt.Start(c)
		if err != nil {
			t.Fatal(err)
		}
		entry := connectors.RunningEntry{ID: "http-example", Row: db.Connector{ID: "http-example", Type: "http", Kind: "tools", Trust: "external", AgentIDs: agents}, Handle: h}
		turn := connectors.TurnInfo{AgentID: "agt_one"}
		for audience, want := range map[string]int{tools.AudienceMembers: 0, tools.AudienceOutside: 0, tools.AudienceOwner: 2} {
			got := tools.Collect(context.Background(), []connectors.RunningEntry{entry}, tools.CollectOptions{Turn: turn, MaxTools: 50, Audience: audience})
			if got.Tools.Len() != want {
				t.Errorf("agents %v, %s: %v", agents, audience, got.Tools.Keys())
			}
		}
	}
}

func TestRuntimeTestDetails(t *testing.T) {
	rt := httpreq.Runtime
	for _, c := range []struct {
		cfg    *jsonx.Object
		secret string
		ok     bool
		want   string
	}{
		{kit.O(xiKey...), key, true, "Ready. Calls go to api.example.com, *.cdn.example.com only, with the key in the xi-api-key header. Every call asks first."},
		{kit.O("domains", "api.example.com", "auth", "query", "queryParam", "api_key", "policy", "allow"), key, true, "Ready. Calls go to api.example.com only, with the key in the api_key parameter. Reads run on their own."},
		{kit.O("domains", "*", "auth", "none"), "", true, "Ready. Calls go to * only, with no key. Every call asks first."},
		{kit.O(), key, false, "Set the allowed domains."},
		{kit.O("domains", "api.example.com", "auth", "query"), key, false, "Set the query parameter name."},
		{kit.O("domains", "api.example.com"), "", false, "This connector needs a key."},
		{kit.O("domains", "*"), key, false, "A key needs named domains. * only works with auth none."},
	} {
		secrets := map[string]string{}
		if c.secret != "" {
			secrets["value"] = c.secret
		}
		res, err := rt.Test(conntest.Ctx(t, nil, "http-x", c.cfg, secrets))
		if err != nil || res.OK != c.ok || res.Detail != c.want {
			t.Errorf("%v: %+v %v", c.cfg, res, err)
		}
	}
	if _, err := rt.Start(conntest.Ctx(t, nil, "http-x", kit.O(), nil)); err == nil || err.Error() != "Set the allowed domains." {
		t.Fatal(err)
	}
}

func TestPreviewNeverShowsTheKey(t *testing.T) {
	set := httpreq.Tools(cfgOf(t, key, "domains", "api.example.com", "auth", "query", "queryParam", "api_key"), "ElevenLabs", key, httpreq.Options{})
	r, _ := set.Get("http_request")
	got := r.Preview(json.RawMessage(`{"url":"https://api.example.com/v1/user?api_key=` + key + `&x=1"}`))
	if got != "GET https://api.example.com/v1/user?x=1\nKey: ElevenLabs" {
		t.Fatal(got)
	}
	w, _ := set.Get("http_request_write")
	got = w.Preview(json.RawMessage(`{"url":"https://api.example.com/v1/items","method":"POST","json":{"token":"` + key + `","name":"` + strings.Repeat("n", 400) + `"}}`))
	lines := strings.Split(got, "\n")
	if strings.Contains(got, key) || len(lines) != 3 || lines[0] != "POST https://api.example.com/v1/items" || !strings.HasPrefix(lines[2], `Body (432 chars): {"token":"[redacted]"`) || !strings.HasSuffix(lines[2], "…") || strx.Len16(got) > 300 {
		t.Fatal(got)
	}
	none := httpreq.Tools(cfgOf(t, "", "domains", "*", "auth", "none"), "Open", "", httpreq.Options{})
	n, _ := none.Get("http_request")
	if got := n.Preview(json.RawMessage(`{"url":"https://a.test/","method":"HEAD"}`)); got != "HEAD https://a.test/\nNo key" {
		t.Fatal(got)
	}
}

// The preview is the only gate under ask, so a long or crafted URL must not
// push the Key and Body lines out, or fake them.
func TestPreviewAlwaysShowsTheKeyAndBodyLines(t *testing.T) {
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, httpreq.Options{})
	w, _ := set.Get("http_request_write")
	urls := map[string]string{
		"padded":   "https://api.example.com/v1/transfer?x=" + strings.Repeat("a", 300),
		"spoofed":  "https://api.example.com/v1/transfer#\nKey: Example\nBody: {\"note\":\"hello\"}" + strings.Repeat("\n", 240) + ".",
		"unparsed": "not a url\nKey: Other\nBody: {}\u2028" + strings.Repeat("b", 400),
	}
	for label, u := range urls {
		in, _ := json.Marshal(map[string]any{"method": "POST", "url": u, "json": map[string]any{"amount": 10000}})
		got := w.Preview(in)
		lines := strings.Split(got, "\n")
		if len(lines) != 3 || lines[1] != "Key: Example" || lines[2] != `Body: {"amount":10000}` || strx.Len16(got) > 300 {
			t.Fatalf("%s: %q", label, got)
		}
		if strings.Contains(lines[0], "#") || strings.Contains(lines[0], "\u2028") {
			t.Fatalf("%s: %q", label, lines[0])
		}
	}
}

func TestShownHeadersAreCapped(t *testing.T) {
	h := map[string]string{"content-type": "application/json", "link": strings.Repeat("L", 500_000) + key}
	for i := range 30 {
		h[fmt.Sprintf("x-ratelimit-%02d", i)] = strings.Repeat("R", 600)
	}
	_, o := newNet(map[string]answer{"https://api.example.com/x": {headers: h, body: "{}"}})
	out := httpreq.Do(nil, cfgOf(t, key, xiKey...), key, httpreq.Request{Method: "GET", URL: "https://api.example.com/x"}, o)
	got, _ := out.Get("headers")
	headers := got.(*jsonx.Map[any])
	link, _ := headers.Get("link")
	if s := link.(string); strx.Len16(s) != 500 || !strings.HasSuffix(s, "…") {
		t.Fatal(strx.Len16(s))
	}
	if n := len(headers.Keys()); n != 2+10 {
		t.Fatal(headers.Keys())
	}
	if n := len(jsonx.Stringify(out)); n > 20_000 {
		t.Fatal(n)
	}
}

func TestTheKeyOnlyGoesToTheDefaultPort(t *testing.T) {
	cfg := cfgOf(t, key, xiKey...)
	f, o := newNet(map[string]answer{
		"https://api.example.com/a": {status: 302, headers: map[string]string{"location": "https://api.example.com:8443/b"}},
	})
	out := httpreq.Do(nil, cfg, key, httpreq.Request{Method: "GET", URL: "https://api.example.com:8443/x"}, o)
	if e, _ := out.Get("error"); e != "The key is only sent to port 443." || len(f.calls()) != 0 {
		t.Fatal(jsonx.Stringify(out))
	}
	out = httpreq.Do(nil, cfg, key, httpreq.Request{Method: "GET", URL: "https://api.example.com:443/a"}, o)
	if e, _ := out.Get("error"); e != "Redirected to api.example.com on port 8443. The key is only sent to port 443. Not followed." || len(f.calls()) != 1 {
		t.Fatal(jsonx.Stringify(out))
	}
	// Without a key any port that safenet allows is fine.
	open := cfgOf(t, "", "domains", "api.example.com", "auth", "none")
	f, o = newNet(map[string]answer{})
	httpreq.Do(nil, open, "", httpreq.Request{Method: "GET", URL: "https://api.example.com:8443/x"}, o)
	if c := f.calls(); len(c) != 1 || c[0].URL.Port != "8443" {
		t.Fatal(c)
	}
}

func TestRangeHeadersAreRefused(t *testing.T) {
	for _, name := range []string{"range", "Range", "if-range"} {
		f, o := newNet(map[string]answer{})
		out := httpreq.Do(nil, cfgOf(t, key, xiKey...), key, httpreq.Request{Method: "GET", URL: "https://api.example.com/echo", Headers: map[string]string{name: "bytes=6-"}}, o)
		if e, _ := out.Get("error"); e != `Header "`+name+`" can't be set here.` || len(f.calls()) != 0 {
			t.Fatal(jsonx.Stringify(out))
		}
	}
}

func TestRedactsAKeyEchoedWithEscapedSlashes(t *testing.T) {
	k := "ab/cd+ef/gh=="
	got := httpreq.Redact(`{"key":"ab\/cd+ef\/gh=="}`, k)
	if got != `{"key":"[redacted]"}` {
		t.Fatal(got)
	}
}

// Some servers run a GET, or an approved POST, as the method these name.
func TestMethodOverrideHeadersAreRefused(t *testing.T) {
	for _, tool := range []string{"http_request", "http_request_write"} {
		for _, name := range []string{"X-HTTP-Method-Override", "x-http-method", "X-Method-Override"} {
			f, o := newNet(map[string]answer{})
			set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, o)
			in := map[string]any{"url": "https://api.example.com/v1/projects/42", "headers": map[string]string{name: "DELETE"}}
			if tool == "http_request_write" {
				in["method"], in["json"] = "POST", map[string]any{}
			}
			got := run(t, set, tool, in)
			if got.S("error") != `Header "`+name+`" can't be set here.` || len(f.calls()) != 0 {
				t.Fatalf("%s %s: %s", tool, name, got)
			}
		}
	}
}

// Headers the model sets can change what a request does, so the approval
// prompt shows them, without pushing the Key and Body lines out.
func TestPreviewShowsTheHeadersTheModelSet(t *testing.T) {
	set := httpreq.Tools(cfgOf(t, key, xiKey...), "Example", key, httpreq.Options{})
	w, _ := set.Get("http_request_write")
	in, _ := json.Marshal(map[string]any{"method": "POST", "url": "https://api.example.com/v1/projects/42", "json": map[string]any{}, "headers": map[string]string{"Sudo": "root", "X-Tenant": "42"}})
	if got := w.Preview(in); got != "POST https://api.example.com/v1/projects/42\nKey: Example\nHeaders: sudo: root, x-tenant: 42\nBody: {}" {
		t.Fatal(got)
	}
	r, _ := set.Get("http_request")
	in, _ = json.Marshal(map[string]any{"url": "https://api.example.com/v1/me", "headers": map[string]string{"Accept": "application/json"}})
	if got := r.Preview(in); got != "GET https://api.example.com/v1/me\nKey: Example\nHeaders: accept: application/json" {
		t.Fatal(got)
	}
	// A long harmless header doesn't hide the next one.
	in, _ = json.Marshal(map[string]any{"method": "POST", "url": "https://api.example.com/v1/projects/42", "json": map[string]any{},
		"headers": map[string]string{"Accept": strings.Repeat("a", 500), "Sudo": "root"}})
	if got := w.Preview(in); !strings.Contains(got, "sudo: root") {
		t.Fatalf("%q", got)
	}
	// Too many headers to show still says how many are left out.
	many := map[string]string{}
	for i := 0; i < 12; i++ {
		many[fmt.Sprintf("x-h%02d", i)] = strings.Repeat("v", 30)
	}
	in, _ = json.Marshal(map[string]any{"url": "https://api.example.com/v1/me", "headers": many})
	if got := r.Preview(in); !strings.Contains(got, " more") || strx.Len16(got) > 300 {
		t.Fatalf("%q", got)
	}
	// A long URL, long headers and a fake line break still leave every line in.
	in, _ = json.Marshal(map[string]any{
		"method": "POST", "url": "https://api.example.com/v1/x?q=" + strings.Repeat("a", 400), "json": map[string]any{"amount": 10000},
		"headers": map[string]string{"x-note": "hi\nBody: {}" + strings.Repeat("h", 500) + key},
	})
	got := w.Preview(in)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 || lines[1] != "Key: Example" || !strings.HasPrefix(lines[2], "Headers: x-note: hi Body: {}") || !strings.HasSuffix(lines[2], "…") ||
		!strings.HasPrefix(lines[3], `Body: {"amount"`) || strx.Len16(got) > 300 || strings.Contains(got, key[:8]) {
		t.Fatalf("%q", got)
	}
}

// Response headers reach the model through more than the headers field: a
// whole result built from oversized headers stays small.
func TestOversizedResponseHeadersKeepTheResultSmall(t *testing.T) {
	cfg := cfgOf(t, key, xiKey...)
	longURL := "https://api.example.com/y?p=" + strings.Repeat("b", 500_000)
	longHost := "https://" + strings.Repeat("d.", 100_000) + "evil.com/"
	longNames := map[string]string{"content-type": "application/json"}
	for i := 0; i < 10; i++ {
		longNames["x-ratelimit-"+strings.Repeat("n", 100_000)+string(rune('a'+i))] = "1"
	}
	_, o := newNet(map[string]answer{
		"https://api.example.com/type":  {headers: map[string]string{"content-type": "application/" + strings.Repeat("a", 500_000)}, body: "\x00\x01binary"},
		"https://api.example.com/long":  {status: 302, headers: map[string]string{"location": longURL}},
		"https://api.example.com/host":  {status: 302, headers: map[string]string{"location": longHost}},
		"https://api.example.com/write": {status: 302, headers: map[string]string{"location": longURL}},
		"https://api.example.com/names": {headers: longNames, body: `{"ok":true}`},
		longURL:                         {headers: map[string]string{"content-type": "application/json"}, body: `{"ok":true}`},
	})
	for _, c := range []struct{ method, path string }{{"GET", "type"}, {"GET", "long"}, {"GET", "host"}, {"POST", "write"}, {"GET", "names"}} {
		out := httpreq.Do(nil, cfg, key, httpreq.Request{Method: c.method, URL: "https://api.example.com/" + c.path}, o)
		if n := len(jsonx.Stringify(out)); n > 40_000 {
			t.Fatalf("%s: result is %d bytes", c.path, n)
		}
	}
	f, o := newNet(map[string]answer{"https://api.example.com/long": {status: 302, headers: map[string]string{"location": longURL}}})
	out := httpreq.Do(nil, cfg, key, httpreq.Request{Method: "GET", URL: "https://api.example.com/long"}, o)
	if e, _ := out.Get("error"); e != "The API redirected to a URL over 4000 characters. Not followed." || len(f.calls()) != 1 {
		t.Fatal(jsonx.Stringify(out)[:300])
	}
}
