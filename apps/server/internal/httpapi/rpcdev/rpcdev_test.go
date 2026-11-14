package rpcdev_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/fixtures"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcdev"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func expect(t *testing.T, status int, body string, wantStatus int, wantBody string) {
	t.Helper()
	if status != wantStatus || body != wantBody {
		t.Fatalf("got %d %s\nwant %d %s", status, body, wantStatus, wantBody)
	}
}

func TestRegistered(t *testing.T) {
	want := map[string]string{"getFixtureState": http.MethodGet, "setFixture": http.MethodPost}
	for _, h := range httpapi.Handlers() {
		if m, ok := want[h.Name]; ok {
			if h.Method != m {
				t.Fatalf("%s is %s", h.Name, h.Method)
			}
			delete(want, h.Name)
		}
	}
	if len(want) > 0 {
		t.Fatalf("not registered: %v", want)
	}
}

func TestFixtureToggle(t *testing.T) {
	e := apitest.New(t, nil)
	token := e.SignIn()

	// A database no fixture filled: the toggle stays hidden.
	s, b := e.Get(token, "getFixtureState", "")
	expect(t, s, b, 200, `{"current":null}`)

	s, b = e.Post(token, "setFixture", `{"name":"worst"}`)
	expect(t, s, b, 200, `{"current":"worst"}`)
	// Sign-in rows are left alone, so the session survives the swap.
	s, b = e.Get(token, "getFixtureState", "")
	expect(t, s, b, 200, `{"current":"worst"}`)
	var n int64
	testutil.Must(t, e.DB.QueryRow("SELECT count(*) FROM passkey WHERE id LIKE 'fx_%'").Scan(&n))
	if n != 6 {
		t.Fatalf("%d fixture passkeys for the signed-in owner", n)
	}

	s, b = e.Post(token, "setFixture", `{"name":"empty"}`)
	expect(t, s, b, 200, `{"current":"empty"}`)
	testutil.Must(t, e.DB.QueryRow("SELECT count(*) FROM connectors").Scan(&n))
	if n != 0 {
		t.Fatalf("%d connectors after empty", n)
	}

	s, b = e.Post(token, "setFixture", `{"name":"nope"}`)
	if s != 400 || !strings.Contains(b, `"code":"VALIDATION"`) {
		t.Fatalf("%d %s", s, b)
	}
}

func TestSetFixtureRefusesRealData(t *testing.T) {
	e := apitest.New(t, nil)
	token := e.SignIn()
	_, err := connectors.Upsert(e.DB, connectors.UpsertInput{ID: "telegram", Manifest: connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}}, testutil.TestKey, e.Now.Load())
	testutil.Must(t, err)
	s, b := e.Post(token, "setFixture", `{"name":"demo"}`)
	expect(t, s, b, 500, `{"error":"`+fixtures.RealDataError.Error()+`","code":"ERROR"}`)
	s, b = e.Get(token, "getFixtureState", "")
	expect(t, s, b, 200, `{"current":null}`)
}

func TestOutsideDevMode(t *testing.T) {
	e := apitest.New(t, func(o *httpapi.Options) { o.Config.Mode = "prod" })
	token := e.SignIn()
	// Even a database a fixture filled reads as none.
	testutil.Must(t, fixtures.Apply(e.DB, "one", fixtures.Options{MasterKey: testutil.TestKey}))
	s, b := e.Get(token, "getFixtureState", "")
	expect(t, s, b, 200, `{"current":null}`)
	s, b = e.Post(token, "setFixture", `{"name":"demo"}`)
	expect(t, s, b, 500, `{"error":"Fixtures only exist in a dev build.","code":"ERROR"}`)
	cur, err := fixtures.Current(e.DB)
	testutil.Must(t, err)
	if cur != "one" {
		t.Fatalf("current %q", cur)
	}
}

func TestGuards(t *testing.T) {
	e := apitest.New(t, nil)
	s, b := e.Get("", "getFixtureState", "")
	if s != 401 || !strings.Contains(b, `"code":"UNAUTHORIZED"`) {
		t.Fatalf("GET without a session: %d %s", s, b)
	}
	s, b = e.Post("", "setFixture", `{"name":"demo"}`)
	if s != 401 || !strings.Contains(b, `"code":"UNAUTHORIZED"`) {
		t.Fatalf("POST without a session: %d %s", s, b)
	}
	token := e.SignIn()
	res := e.Do(http.MethodPost, "/api/rpc/setFixture", `{"name":"demo"}`, apitest.Cookie(token))
	if b := apitest.Body(res); res.StatusCode != 403 || !strings.Contains(b, `"code":"FORBIDDEN"`) {
		t.Fatalf("POST without the same origin: %d %s", res.StatusCode, b)
	}
	res = e.Do(http.MethodPost, "/api/rpc/setFixture", `{"name":"demo"}`, apitest.Cookie(token), apitest.Header("origin", "https://evil.example"))
	if res.StatusCode != 403 {
		t.Fatalf("POST from another origin: %d", res.StatusCode)
	}
	var n int64
	testutil.Must(t, e.DB.QueryRow("SELECT count(*) FROM connectors").Scan(&n))
	if n != 0 {
		t.Fatalf("a refused POST wrote %d connectors", n)
	}
	// GET is not a write: setFixture refuses it.
	s, _ = e.Get(token, "setFixture", `{"name":"demo"}`)
	if s != 405 {
		t.Fatalf("GET setFixture: %d", s)
	}
}
