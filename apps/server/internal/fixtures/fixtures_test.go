package fixtures_test

// apps/app/src/dev/fixtures/index.test.ts, case for case, plus the stand-in
// runtimes of `palui dev serve`.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/fixtures"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func count(t testing.TB, d *db.DB, table string) int64 {
	t.Helper()
	var n int64
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func apply(t testing.TB, d *db.DB, name string, force bool) error {
	t.Helper()
	return fixtures.Apply(d, name, fixtures.Options{Force: force, MasterKey: testutil.TestKey})
}

func current(t testing.TB, d *db.DB) string {
	t.Helper()
	cur, err := fixtures.Current(d)
	testutil.Must(t, err)
	return cur
}

func TestDevFixtures(t *testing.T) {
	// Applying is the test: `worst` panics if one of its values is over the
	// limit the dashboard's own forms accept.
	for _, name := range fixtures.Names {
		t.Run("applies "+name+" and records it", func(t *testing.T) {
			d := testutil.DB(t)
			addOwner(t, d, time.Now().UnixMilli())
			testutil.Must(t, apply(t, d, name, false))
			if got := current(t, d); got != name {
				t.Fatalf("current %q", got)
			}
		})
	}

	t.Run("replaces the previous fixture instead of adding to it", func(t *testing.T) {
		d := testutil.DB(t)
		testutil.Must(t, apply(t, d, "demo", false))
		if count(t, d, "connectors") == 0 {
			t.Fatal("demo wrote no connectors")
		}
		testutil.Must(t, apply(t, d, "empty", false))
		for _, table := range []string{"connectors", "messages", "events"} {
			if n := count(t, d, table); n != 0 {
				t.Fatalf("%s has %d rows", table, n)
			}
		}
	})

	t.Run("keeps the owner, and only removes the passkeys it added", func(t *testing.T) {
		d := testutil.DB(t)
		addOwner(t, d, time.Now().UnixMilli())
		testutil.Must(t, apply(t, d, "worst", false))
		if n := count(t, d, "passkey"); n <= 1 {
			t.Fatalf("%d passkeys", n)
		}
		testutil.Must(t, apply(t, d, "empty", false))
		if n := count(t, d, "user"); n != 1 {
			t.Fatalf("%d users", n)
		}
		var ids []string
		rows, err := d.Query("SELECT id FROM passkey")
		testutil.Must(t, err)
		for rows.Next() {
			var id string
			testutil.Must(t, rows.Scan(&id))
			ids = append(ids, id)
		}
		rows.Close()
		if strings.Join(ids, ",") != "pk_real" {
			t.Fatalf("passkeys %v", ids)
		}
	})

	t.Run("refuses a database with data that no fixture put there", func(t *testing.T) {
		d := testutil.DB(t)
		_, err := connectors.Upsert(d, connectors.UpsertInput{ID: "telegram", Manifest: connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}}, testutil.TestKey, time.Now().UnixMilli())
		testutil.Must(t, err)
		real, err := fixtures.HoldsRealData(d)
		testutil.Must(t, err)
		if !real {
			t.Fatal("holdsRealData false")
		}
		err = apply(t, d, "worst", false)
		if err == nil || !strings.Contains(err.Error(), "didn't come from a fixture") {
			t.Fatalf("err %v", err)
		}
		if n := count(t, d, "connectors"); n != 1 {
			t.Fatalf("%d connectors", n)
		}
		testutil.Must(t, apply(t, d, "worst", true))
		if got := current(t, d); got != "worst" {
			t.Fatalf("current %q", got)
		}
	})

	t.Run("fills the huge fixture past every list limit", func(t *testing.T) {
		d := testutil.DB(t)
		testutil.Must(t, apply(t, d, "huge", false))
		h := fixtures.Huge
		if n := count(t, d, "chats"); n != h.Chats+1+h.FlowChats {
			t.Fatalf("%d chats", n)
		}
		if n := count(t, d, "flows"); n != h.Flows {
			t.Fatalf("%d flows", n)
		}
		if n := count(t, d, "reminders"); n != h.Upcoming+h.Recurring+h.Done {
			t.Fatalf("%d reminders", n)
		}
		if n := count(t, d, "events"); n < h.Events {
			t.Fatalf("%d events", n)
		}
	})
}

func TestCurrentIgnoresAnUnknownMarker(t *testing.T) {
	d := testutil.DB(t)
	if got := current(t, d); got != "" {
		t.Fatalf("fresh database: %q", got)
	}
	_, err := d.Exec("INSERT INTO settings (key, value, updated_at) VALUES ('devFixture', '\"nope\"', 1)")
	testutil.Must(t, err)
	if got := current(t, d); got != "" {
		t.Fatalf("unknown marker: %q", got)
	}
	// A database marked with an unknown fixture holds no real data only if it
	// has no connectors or messages.
	real, err := fixtures.HoldsRealData(d)
	testutil.Must(t, err)
	if real {
		t.Fatal("empty database holds real data")
	}
	if err := apply(t, d, "nope", true); err == nil {
		t.Fatal("unknown fixture applied")
	}
}

func TestApplyRollsBackOnAFailure(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, apply(t, d, "one", false))
	// A table the fill writes to is gone: the whole apply rolls back.
	_, err := d.Exec("DROP TABLE preferences")
	testutil.Must(t, err)
	if err := apply(t, d, "demo", false); err == nil {
		t.Fatal("demo applied without a preferences table")
	}
	if got := current(t, d); got != "one" {
		t.Fatalf("current %q after a failed apply", got)
	}
}

// statusOf reads a connector's stored status.
func statusOf(t testing.TB, d *db.DB, id string) (string, *string) {
	t.Helper()
	var status string
	var detail *string
	testutil.Must(t, d.QueryRow("SELECT status, status_detail FROM connectors WHERE id = ?", id).Scan(&status, &detail))
	return status, detail
}

func TestStandInsKeepTheStoredStatus(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, apply(t, d, "worst", false))
	reg, err := fixtures.StandIns(d, registry.Manifests(), 20*time.Millisecond)
	testutil.Must(t, err)
	for _, m := range registry.Manifests() {
		if reg[m.ID] == nil {
			t.Fatalf("no stand-in for %s", m.ID)
		}
	}
	// The web chat is the real one, so a dashboard message gets its answer.
	if reg["web"] != web.Runtime {
		t.Fatalf("web is a stand-in, want the real web channel")
	}
	sup := connectors.NewSupervisor(connectors.SupervisorOptions{DB: d, Registry: reg, MasterKey: testutil.TestKey})
	t.Cleanup(sup.Stop)
	want := map[string]string{}
	for _, id := range []string{"whatsapp", "ai-gateway", "telegram", "recall", "mcp-de"} {
		s, _ := statusOf(t, d, id)
		want[id] = s
	}
	sup.Reconcile()
	for id, w := range want {
		if s, _ := statusOf(t, d, id); s != w {
			t.Fatalf("%s: %s, want the stored %s", id, s, w)
		}
	}
	if _, detail := statusOf(t, d, "whatsapp"); detail == nil || !strings.HasPrefix(*detail, "Connection closed (440)") {
		t.Fatalf("whatsapp detail %v", detail)
	}
	if res := sup.Test("telegram"); !res.OK || res.Detail != "Reachable (demo runtime)" {
		t.Fatalf("test %+v", res)
	}

	// A paused connector that is resumed goes from connecting to on; recall
	// would stay connecting.
	_, err = d.Exec("UPDATE connectors SET enabled = 1 WHERE id = 'mcp-fox'")
	testutil.Must(t, err)
	sup.Reconcile()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s, _ := statusOf(t, d, "mcp-fox"); s == "on" {
			break
		}
		if time.Now().After(deadline) {
			s, _ := statusOf(t, d, "mcp-fox")
			t.Fatalf("mcp-fox stayed %s", s)
		}
		time.Sleep(10 * time.Millisecond)
	}

	out, err := sup.Action(context.Background(), "mcp-linear", "listTools", jsonx.NewMap[any]())
	testutil.Must(t, err)
	if s := jsonx.Stringify(out); !strings.Contains(s, `"name":"disk_free","title":"Free disk space on the host"`) || !strings.Contains(s, `"saved":false,"flowApproval":"version"`) {
		t.Fatalf("listTools %s", s)
	}
	models, err := sup.Action(context.Background(), "cliproxy", "listModels", nil)
	testutil.Must(t, err)
	if !strings.Contains(jsonx.Stringify(models), "claude-sonnet-5-5") {
		t.Fatalf("listModels %s", jsonx.Stringify(models))
	}
}

// The agent page's Tools tab lists demo tools, as demo-runtime.ts's agentTools
// did: the fake MCP list for an MCP connector that is on, the built-in list for
// a skill that is on, nothing for a connector that isn't on.
func TestStandInsListDemoTools(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, apply(t, d, "worst", false))
	reg, err := fixtures.StandIns(d, registry.Manifests(), 20*time.Millisecond)
	testutil.Must(t, err)
	sup := connectors.NewSupervisor(connectors.SupervisorOptions{DB: d, Registry: reg, MasterKey: testutil.TestKey})
	t.Cleanup(sup.Stop)
	sup.Reconcile()
	got := map[string][]string{}
	for _, e := range sup.Running() {
		if e.Handle.Tools == nil {
			continue
		}
		set, err := e.Handle.Tools(context.Background(), connectors.TurnInfo{})
		testutil.Must(t, err)
		for _, name := range set.Keys() {
			pt, _ := set.Get(name)
			got[e.ID] = append(got[e.ID], name+":"+pt.Class)
		}
	}
	want := map[string]string{
		"reminders":    "buat_pengingat:write list_reminders:read hapus_pengingat:destructive",
		"mcp-linear":   "disk_free:read uptime:read restart_service:destructive list_deals:write send_email_to_a_customer_with_a_long_tool_name:write",
		"webfetch":     "fetch_page:read",
		"telegram":     "",
		"mcp-notion-2": "",
	}
	for id, w := range want {
		if g := strings.Join(got[id], " "); g != w {
			t.Errorf("%s: %q, want %q", id, g, w)
		}
	}
}
