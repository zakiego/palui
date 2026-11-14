package httpapi_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/auth/authtest"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// guards.test.ts: every server function checks the session except the ones
// the login and invite pages need, and every POST checks the origin.
var public = map[string]bool{"getSession": true, "getInvite": true, "acceptInvite": true}

func TestEveryHandlerHasAGuard(t *testing.T) {
	list := httpapi.Handlers()
	if len(list) < 5 {
		t.Fatalf("only %d handlers", len(list))
	}
	for _, h := range list {
		if err := httpapi.CheckHandler(h); err != nil {
			t.Error(err)
		}
		if h.Guard == httpapi.Public && !public[h.Name] {
			t.Errorf("%s is public", h.Name)
		}
		if h.Method == http.MethodPost && h.Guard != httpapi.Writer && h.Guard != httpapi.Public {
			t.Errorf("%s: POST without Writer", h.Name)
		}
	}
	fn := func(*httpapi.Call) (any, error) { return nil, nil }
	if err := httpapi.CheckHandler(httpapi.Handler{Name: "x", Method: http.MethodGet, Fn: fn}); err == nil || !strings.Contains(err.Error(), "no guard") {
		t.Fatalf("a handler without a guard passed: %v", err)
	}
	if err := httpapi.CheckHandler(httpapi.Handler{Name: "x", Method: http.MethodGet, Guard: httpapi.Writer, Fn: fn}); err == nil {
		t.Fatal("a GET writer passed")
	}
	if err := httpapi.CheckHandler(httpapi.Handler{Name: "x", Method: http.MethodPost, Guard: httpapi.User, Fn: fn}); err == nil {
		t.Fatal("a POST user handler passed")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Register took a handler without a guard")
		}
	}()
	httpapi.Register(httpapi.Handler{Name: "unguarded", Method: http.MethodGet, Fn: fn})
}

func rpcGet(e *env, name, input string, opts ...reqOpt) *http.Response {
	path := "/api/rpc/" + name
	if input != "" {
		path += "?input=" + url.QueryEscape(input)
	}
	return e.do("GET", path, "", opts...)
}

func TestGetSessionAndInvite(t *testing.T) {
	e := newEnv(t, nil)
	if body := readBody(rpcGet(e, "getSession", "")); body != `{"viewer":null,"configured":true,"ownerExists":false}` {
		t.Fatal(body)
	}
	inv, err := auth.CreateOwnerInvite(e.d, appURL, e.now.Load())
	testutil.Must(t, err)
	if body := readBody(rpcGet(e, "getInvite", `{"token":"`+inv.Token+`"}`)); body != `{"ok":true,"expiresAt":`+itoa(inv.ExpiresAt)+`,"recovery":false}` {
		t.Fatal(body)
	}
	if body := readBody(rpcGet(e, "getInvite", `{"token":"nope-nope-nope"}`)); body != `{"ok":false,"problem":"This link isn't valid."}` {
		t.Fatal(body)
	}
	// Validation: one error shape, zod's message and issues.
	res := rpcGet(e, "getInvite", `{"token":"short"}`)
	m := decode(t, res)
	if res.StatusCode != 400 || m["code"] != "VALIDATION" || !strings.Contains(m["error"].(string), `"too_small"`) || len(m["issues"].([]any)) != 1 {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
	if res := rpcGet(e, "getInvite", ""); res.StatusCode != 400 {
		t.Fatal(res.StatusCode)
	}
	if res := rpcGet(e, "getInvite", "{not json"); res.StatusCode != 400 || decode(t, res)["error"] != "Input isn't valid JSON" {
		t.Fatal(res.StatusCode)
	}

	// Accepting signs in.
	res = e.do("POST", "/api/rpc/acceptInvite", `{"token":"`+inv.Token+`"}`, sameOrigin())
	c := sessionCookieOf(res)
	if body := readBody(res); res.StatusCode != 200 || body != `{"ok":true,"created":true}` || c == nil {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	body := readBody(rpcGet(e, "getSession", "", cookie(c.Value)))
	if !strings.Contains(body, `"ownerExists":true`) || !strings.Contains(body, `"username":"owner"`) || !strings.Contains(body, `"sessionId":"ses_`) {
		t.Fatal(body)
	}
	// Used up: the invite error text, as the page shows it.
	res = e.do("POST", "/api/rpc/acceptInvite", `{"token":"`+inv.Token+`"}`, sameOrigin())
	m = decode(t, res)
	if res.StatusCode != 400 || m["error"] != "This link is no longer valid. Run `palui owner invite` for a new one." {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
	// A public POST still needs the origin.
	inv2, _ := auth.CreateOwnerInvite(e.d, appURL, e.now.Load())
	res = e.do("POST", "/api/rpc/acceptInvite", `{"token":"`+inv2.Token+`"}`, header("origin", "https://evil.example"))
	if m := decode(t, res); res.StatusCode != 403 || m["error"] != "cross-origin request refused" || m["code"] != "FORBIDDEN" {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
}

func TestPasskeyFunctionsNeedTheSessionAndKeepTheLastPasskey(t *testing.T) {
	e := newEnv(t, nil)
	res := rpcGet(e, "listPasskeys", "")
	if m := decode(t, res); res.StatusCode != 401 || m["error"] != "UNAUTHORIZED" || m["code"] != "UNAUTHORIZED" {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
	token := e.signIn()
	if body := readBody(rpcGet(e, "listPasskeys", "", cookie(token))); body != "[]" {
		t.Fatal(body)
	}
	// Register one passkey straight through the auth package.
	rp, _ := auth.NewRP(appURL)
	f, _ := auth.GetSession(e.d, token, false, e.now.Load())
	a := authtest.New("localhost", appURL)
	opts, ch, err := rp.RegisterOptions(e.d, f.Viewer(), e.now.Load())
	testutil.Must(t, err)
	pk, err := rp.Register(e.d, f.Viewer(), ch, a.Attestation(opts.Challenge.String()), "Phone", e.now.Load())
	testutil.Must(t, err)

	res = e.do("POST", "/api/rpc/removePasskey", `{"id":"`+pk.ID+`"}`, sameOrigin(), cookie(token))
	if m := decode(t, res); res.StatusCode != 400 || m["error"] != "This is your only passkey. Add another one before removing it." {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
	if res := e.do("POST", "/api/rpc/removePasskey", `{"id":"`+pk.ID+`"}`, header("origin", "https://evil.example"), cookie(token)); res.StatusCode != 403 {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("POST", "/api/rpc/removePasskey", `{"id":"`+pk.ID+`"}`, sameOrigin()); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("POST", "/api/rpc/removePasskey", `{"id":""}`, sameOrigin(), cookie(token)); res.StatusCode != 400 || decode(t, res)["code"] != "VALIDATION" {
		t.Fatal(res.StatusCode)
	}
	list := readBody(rpcGet(e, "listPasskeys", "", cookie(token)))
	if !strings.Contains(list, `"name":"Phone"`) {
		t.Fatal(list)
	}
}

func TestRPCRoutingErrors(t *testing.T) {
	e := newEnv(t, nil)
	res := e.do("GET", "/api/rpc/noSuchFunction", "")
	if m := decode(t, res); res.StatusCode != 404 || m["code"] != "NOT_FOUND" {
		t.Fatal(m)
	}
	res = e.do("POST", "/api/rpc/getSession", "", sameOrigin())
	if res.StatusCode != 405 || res.Header.Get("allow") != "GET" {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("GET", "/api/nothing-here", ""); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
}

// A handler's errors: *Error keeps status and fields, plain errors are 500
// with their text, and a panic is a 500 too.
func TestRPCErrorShapes(t *testing.T) {
	httpapi.Register(httpapi.Handler{Name: "testFail", Method: http.MethodPost, Guard: httpapi.Public, Input: zod.Object(zod.F("kind", zod.String())), Fn: func(c *httpapi.Call) (any, error) {
		var in struct{ Kind string }
		if err := c.Decode(&in); err != nil {
			return nil, err
		}
		switch in.Kind {
		case "fail":
			return nil, httpapi.Fail("Nope: %s", "bad")
		case "plain":
			return nil, errAny("something broke")
		case "panic":
			panic("boom")
		}
		return map[string]string{"kind": in.Kind}, nil
	}})
	e := newEnv(t, nil)
	cases := map[string]struct {
		status int
		body   string
	}{
		"fail":  {400, `{"error":"Nope: bad","code":"BAD_REQUEST"}`},
		"plain": {500, `{"error":"something broke","code":"ERROR"}`},
		"panic": {500, `{"error":"Internal error"}`},
		"ok":    {200, `{"kind":"ok"}`},
	}
	for kind, want := range cases {
		res := e.do("POST", "/api/rpc/testFail", `{"kind":"`+kind+`"}`, sameOrigin())
		if body := readBody(res); res.StatusCode != want.status || body != want.body {
			t.Errorf("%s: %d %s", kind, res.StatusCode, body)
		}
	}
}

type errAny string

func (e errAny) Error() string { return string(e) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
