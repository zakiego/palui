package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/auth/authtest"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// better-auth.test.ts: only the routes the dashboard needs exist, writes
// must come from APP_URL. Plus the passkey ceremonies over HTTP.

func TestOnlyTheDashboardsAuthRoutesExist(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/api/auth/session", "/api/auth/passkey/authenticate-options"} {
		if res := e.do("GET", p, ""); res.StatusCode != 200 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	for _, p := range []string{"/api/auth/sign-in/username", "/api/auth/sign-in/email", "/api/auth/sign-up/email", "/api/auth/change-password", "/api/auth/passkey/delete-passkey", "/api/auth/SESSION", "/api/auth/get-session", "/api/auth/%73ession", "/api/auth/list-sessions", "/api/auth/revoke-sessions", "/api/auth/passkey%2Flist"} {
		res := e.do("POST", p, "{}", sameOrigin())
		if res.StatusCode != 404 {
			t.Errorf("%s: %d", p, res.StatusCode)
			continue
		}
		if m := decode(t, res); m["code"] != "NOT_FOUND" {
			t.Errorf("%s: %v", p, m)
		}
	}
	if res := e.do("GET", "/api/auth/sign-out", ""); res.StatusCode != 405 {
		t.Fatalf("GET sign-out: %d", res.StatusCode)
	}
}

func TestAuthRefusesCrossOriginWrites(t *testing.T) {
	e := newEnv(t, nil)
	res := e.do("POST", "/api/auth/passkey/authenticate", `{"response":{}}`, header("origin", "https://evil.example"))
	if res.StatusCode != 403 || decode(t, res)["code"] != "INVALID_ORIGIN" {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("POST", "/api/auth/sign-out", ""); res.StatusCode != 403 {
		t.Fatalf("no origin: %d", res.StatusCode)
	}
	if res := e.do("GET", "/api/auth/session", "", header("origin", "https://evil.example")); res.StatusCode != 200 {
		t.Fatalf("a GET isn't a write: %d", res.StatusCode)
	}
}

func TestAuthRateLimitsPerClientIP(t *testing.T) {
	e := newEnv(t, nil)
	ip := header("cf-connecting-ip", "203.0.113.7")
	for i := 0; i < 10; i++ {
		if res := e.do("GET", "/api/auth/passkey/authenticate-options", "", ip); res.StatusCode != 200 {
			t.Fatalf("request %d: %d", i+1, res.StatusCode)
		}
	}
	res := e.do("GET", "/api/auth/passkey/authenticate-options", "", ip)
	if res.StatusCode != 429 || res.Header.Get("X-Retry-After") == "" {
		t.Fatalf("11th: %d", res.StatusCode)
	}
	if m := decode(t, res); m["message"] != "Too many requests. Please try again later." {
		t.Fatal(m)
	}
	// A client can't pick its bucket with x-palui-client-ip.
	if res := e.do("GET", "/api/auth/passkey/authenticate-options", "", ip, header(auth.ClientIPHeader, "1.1.1.1")); res.StatusCode != 429 {
		t.Fatalf("spoofed: %d", res.StatusCode)
	}
	if res := e.do("GET", "/api/auth/passkey/authenticate-options", "", header("cf-connecting-ip", "198.51.100.1")); res.StatusCode != 200 {
		t.Fatalf("other ip: %d", res.StatusCode)
	}
}

func challenge(t *testing.T, body string) string {
	t.Helper()
	var o struct {
		Challenge string `json:"challenge"`
	}
	testutil.Must(t, json.Unmarshal([]byte(body), &o))
	return o.Challenge
}

func challengeCookie(res *http.Response) string {
	for _, c := range res.Cookies() {
		if c.Name == auth.ChallengeCookie {
			return c.Value
		}
	}
	return ""
}

func TestPasskeyCeremoniesOverHTTP(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	a := authtest.New("localhost", appURL)

	// Signed out: no register options, a null session.
	if res := e.do("GET", "/api/auth/passkey/register-options", ""); res.StatusCode != 401 {
		t.Fatalf("register options without a session: %d", res.StatusCode)
	}
	if body := readBody(e.do("GET", "/api/auth/session", "")); body != "null" {
		t.Fatal(body)
	}

	res := e.do("GET", "/api/auth/passkey/register-options", "", cookie(token))
	opts := readBody(res)
	ch := challengeCookie(res)
	if res.StatusCode != 200 || ch == "" || !strings.Contains(opts, `"name":"Palui Agent"`) {
		t.Fatalf("%d %s", res.StatusCode, opts)
	}
	body, _ := json.Marshal(map[string]any{"response": json.RawMessage(a.Attestation(challenge(t, opts))), "name": "Laptop"})
	res = e.do("POST", "/api/auth/passkey/register", string(body), sameOrigin(), cookie(token), withCookie(auth.ChallengeCookie, ch))
	pk := decode(t, res)
	if res.StatusCode != 200 || pk["name"] != "Laptop" || pk["deviceType"] != "singleDevice" {
		t.Fatalf("%d %v", res.StatusCode, pk)
	}

	// Sign in with it from a fresh browser.
	res = e.do("GET", "/api/auth/passkey/authenticate-options", "")
	opts = readBody(res)
	ch = challengeCookie(res)
	body, _ = json.Marshal(map[string]any{"response": json.RawMessage(a.Assertion(challenge(t, opts)))})
	res = e.do("POST", "/api/auth/passkey/authenticate", string(body), sameOrigin(), withCookie(auth.ChallengeCookie, ch))
	signed := decode(t, res)
	c := sessionCookieOf(res)
	if res.StatusCode != 200 || c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || signed["user"].(map[string]any)["username"] != "owner" {
		t.Fatalf("%d %v %+v", res.StatusCode, signed, c)
	}
	sess := decode(t, e.do("GET", "/api/auth/session", "", cookie(c.Value)))
	if sess["user"].(map[string]any)["name"] != "Owner" {
		t.Fatal(sess)
	}
	// The challenge cookie is cleared after use, and a replay fails.
	res = e.do("POST", "/api/auth/passkey/authenticate", string(body), sameOrigin(), withCookie(auth.ChallengeCookie, ch))
	if m := decode(t, res); res.StatusCode != 400 || m["code"] != "CHALLENGE_NOT_FOUND" {
		t.Fatalf("%d %v", res.StatusCode, m)
	}

	// List and rename.
	var list []map[string]any
	testutil.Must(t, json.Unmarshal([]byte(readBody(e.do("GET", "/api/auth/passkey/list", "", cookie(c.Value)))), &list))
	if len(list) != 1 {
		t.Fatal(list)
	}
	res = e.do("POST", "/api/auth/passkey/rename", `{"id":"`+list[0]["id"].(string)+`","name":"Phone"}`, sameOrigin(), cookie(c.Value))
	if m := decode(t, res); m["passkey"].(map[string]any)["name"] != "Phone" {
		t.Fatal(m)
	}
	if res := e.do("POST", "/api/auth/passkey/rename", `{"id":"x"}`, sameOrigin(), cookie(c.Value)); res.StatusCode != 400 {
		t.Fatal(res.StatusCode)
	}
	if res := e.do("GET", "/api/auth/passkey/list", ""); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}

	// Sign out ends the session.
	res = e.do("POST", "/api/auth/sign-out", "", sameOrigin(), cookie(c.Value))
	if m := decode(t, res); m["success"] != true || sessionCookieOf(res).MaxAge >= 0 {
		t.Fatal(m)
	}
	if body := readBody(e.do("GET", "/api/auth/session", "", cookie(c.Value))); body != "null" {
		t.Fatal(body)
	}
}

func TestSecureCookiesOnHTTPS(t *testing.T) {
	e := newEnv(t, func(o *httpapi.Options) { o.Config.AppURL = "https://palui.example.com" })
	token := e.signIn()
	res := e.do("POST", "/api/auth/sign-out", "", header("origin", "https://palui.example.com"), cookie(token))
	if c := sessionCookieOf(res); c == nil || !c.Secure {
		t.Fatalf("%+v", c)
	}
}
