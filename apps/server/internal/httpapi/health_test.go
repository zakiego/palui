package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/version"
)

// health.test.ts. The runtime runs in process now: the body's `runtime` is
// the old /live answer about it, or "unreachable" while it isn't running.

func TestHealthIs200WithRuntimeAndConnectorStateInTheBody(t *testing.T) {
	var e *env
	e = newEnv(t, func(o *httpapi.Options) { o.Runtime = &httpapi.Runtime{DB: o.DB} })
	_, err := connectors.Upsert(e.d, connectors.UpsertInput{ID: "telegram", Manifest: connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}}, testutil.TestKey, 1)
	testutil.Must(t, err)
	res := e.doFrom("127.0.0.1:5555", "GET", "/health")
	if res.StatusCode != 200 || res.Header.Get("cache-control") != "no-store" {
		t.Fatal(res.StatusCode)
	}
	body := readBody(res)
	last := db.Migrations()[len(db.Migrations())-1].Name
	want := `{"ok":true,"version":"` + version.Version + `","schema":"` + last + `","runtime":{"ok":true,"schema":"` + last + `","version":"` + version.Version + `"},"connectors":[{"id":"telegram","status":"not_set_up"}]}`
	if body != want {
		t.Fatalf("\n got %s\nwant %s", body, want)
	}
}

func TestHealthHidesConnectorsFromStrangers(t *testing.T) {
	e := newEnv(t, nil)
	_, err := connectors.Upsert(e.d, connectors.UpsertInput{ID: "telegram", Manifest: connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}}, testutil.TestKey, 1)
	testutil.Must(t, err)
	count := func(res *http.Response) int { return len(decode(t, res)["connectors"].([]any)) }
	if n := count(e.do("GET", "/health", "")); n != 0 {
		t.Fatalf("remote stranger saw %d connectors", n)
	}
	if n := count(e.doFrom("127.0.0.1:5555", "GET", "/health", header("cf-connecting-ip", "198.51.100.7"))); n != 0 {
		t.Fatalf("tunnelled stranger saw %d connectors", n)
	}
	if n := count(e.do("GET", "/health", "", cookie(e.signIn()))); n != 1 {
		t.Fatalf("owner saw %d connectors", n)
	}
}

func TestHealthStays200WhenTheRuntimeIsDown(t *testing.T) {
	e := newEnv(t, nil)
	res := e.do("GET", "/health", "")
	m := decode(t, res)
	if res.StatusCode != 200 || m["ok"] != true || m["runtime"] != "unreachable" || len(m["connectors"].([]any)) != 0 {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
}

func TestHealthAnswers503WhenANewerBuildMigratedTheDatabase(t *testing.T) {
	e := newEnv(t, nil)
	_, err := e.d.Exec("INSERT INTO _migrations (name, applied_at) VALUES ('9999_from_the_future', 1)")
	testutil.Must(t, err)
	res := e.do("GET", "/health", "")
	m := decode(t, res)
	if res.StatusCode != 503 || m["ok"] != false || m["error"] != "db_newer_than_app" || m["unknownMigrations"].([]any)[0] != "9999_from_the_future" {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
	if !strings.Contains(m["message"].(string), "newer version migrated it") {
		t.Fatal(m["message"])
	}
}

func TestHealthReportsASchemaThatIsBehind(t *testing.T) {
	e := newEnv(t, nil)
	last := db.Migrations()[len(db.Migrations())-1].Name
	_, err := e.d.Exec("DELETE FROM _migrations WHERE name = ?", last)
	testutil.Must(t, err)
	m := decode(t, e.do("GET", "/health", ""))
	if m["schema"] != "behind ("+last+")" {
		t.Fatal(m["schema"])
	}
}

func TestAdminReconcileNeedsLoopbackAndTheToken(t *testing.T) {
	token, _ := secret.ControlToken(testutil.TestKey)
	e := newEnv(t, func(o *httpapi.Options) { o.AdminToken = token })
	res := e.do("POST", "/api/admin/reconcile", "", header(httpapi.AdminHeader, token))
	if res.StatusCode != 401 {
		t.Fatalf("remote caller: %d", res.StatusCode)
	}
	res = e.doFrom("127.0.0.1:5555", "POST", "/api/admin/reconcile", header(httpapi.AdminHeader, "wrong"))
	if res.StatusCode != 401 {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	// No runtime: the token is right but nothing runs to reconcile.
	res = e.doFrom("[::1]:5555", "POST", "/api/admin/reconcile", header(httpapi.AdminHeader, token))
	if res.StatusCode != 503 {
		t.Fatalf("no runtime: %d", res.StatusCode)
	}
}
