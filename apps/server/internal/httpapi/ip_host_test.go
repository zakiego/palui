package httpapi_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

// WebAuthn refuses an IP address as the relying party id. With such an
// APP_URL the server still starts: one warning in the log, health, the owner
// invite and sessions work, and every passkey route says why it can't run.
func TestIPHostAppURLTurnsPasskeysOff(t *testing.T) {
	for _, appURL := range []string{"http://127.0.0.1:4895", "http://[::1]:4895", "http://192.168.1.20:3000"} {
		t.Run(appURL, func(t *testing.T) {
			var mu sync.Mutex
			var lines []string
			log := logx.New(nil, func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }, logx.Debug)
			e := newEnv(t, func(o *httpapi.Options) {
				o.Config.AppURL = appURL
				o.Log = log
			})

			mu.Lock()
			var warns []string
			for _, l := range lines {
				if strings.Contains(l, `"level":"warn"`) && strings.Contains(l, "passkeys are off") {
					warns = append(warns, l)
				}
			}
			mu.Unlock()
			if len(warns) != 1 || !strings.Contains(warns[0], "IP address") || !strings.Contains(warns[0], "host name") {
				t.Fatalf("want one passkey warning, got %q", lines)
			}

			if res := e.do("GET", "/health", ""); res.StatusCode != 200 || !strings.Contains(readBody(res), `"ok":true`) {
				t.Fatalf("health: %d", res.StatusCode)
			}

			// The owner invite signs in without a passkey.
			inv, err := auth.CreateOwnerInvite(e.d, appURL, e.now.Load())
			if err != nil {
				t.Fatal(err)
			}
			res := e.do("POST", "/api/rpc/acceptInvite", `{"token":"`+inv.Token+`"}`, header("origin", strings.TrimSuffix(appURL, "/")))
			c := sessionCookieOf(res)
			if body := readBody(res); res.StatusCode != 200 || c == nil {
				t.Fatalf("acceptInvite: %d %s", res.StatusCode, body)
			}
			if body := readBody(rpcGet(e, "getSession", "", cookie(c.Value))); !strings.Contains(body, `"username":"owner"`) {
				t.Fatal(body)
			}

			// Every ceremony answers 503 with the reason, signed in or not.
			check := func(method, path, body string, opts ...reqOpt) {
				t.Helper()
				res := e.do(method, path, body, opts...)
				m := decode(t, res)
				if res.StatusCode != 503 || m["code"] != "PASSKEYS_UNAVAILABLE" || !strings.Contains(m["message"].(string), "not an IP address") {
					t.Fatalf("%s %s: %d %v", method, path, res.StatusCode, m)
				}
			}
			origin := header("origin", appURL)
			check("GET", "/api/auth/passkey/register-options", "", cookie(c.Value))
			check("POST", "/api/auth/passkey/register", `{"response":{"id":"x"}}`, origin, cookie(c.Value))
			check("GET", "/api/auth/passkey/authenticate-options", "")
			check("POST", "/api/auth/passkey/authenticate", `{"response":{"id":"x"}}`, origin)

			// Listing and signing out don't need WebAuthn.
			if res := e.do("GET", "/api/auth/passkey/list", "", cookie(c.Value)); res.StatusCode != 200 {
				t.Fatalf("list: %d", res.StatusCode)
			}
			if res := e.do("POST", "/api/auth/sign-out", "", origin, cookie(c.Value)); res.StatusCode != 200 {
				t.Fatalf("sign-out: %d", res.StatusCode)
			}
		})
	}
}

// A host name keeps passkeys on and logs nothing about them.
func TestHostNameAppURLKeepsPasskeysOn(t *testing.T) {
	var lines []string
	log := logx.New(nil, func(l string) { lines = append(lines, l) }, logx.Debug)
	e := newEnv(t, func(o *httpapi.Options) { o.Log = log })
	for _, l := range lines {
		if strings.Contains(l, "passkeys are off") {
			t.Fatal(l)
		}
	}
	token := e.signIn()
	if res := e.do("GET", "/api/auth/passkey/register-options", "", cookie(token)); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
}
