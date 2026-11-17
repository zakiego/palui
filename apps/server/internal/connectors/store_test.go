package connectors_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/connectors/store.test.ts (the settings and redact
// cases live in the settings and logx tests).

const KEY = "test-master-key-test-master-key-1234"

var manifest = connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}

func secrets(kv ...string) *jsonx.Map[string] {
	m := jsonx.NewMap[string]()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

func upsert(t *testing.T, d db.Q, in connectors.UpsertInput) db.Connector {
	t.Helper()
	row, err := connectors.Upsert(d, in, KEY, 1)
	testutil.Must(t, err)
	return row
}

func get(t *testing.T, d db.Q, id string) db.Connector {
	t.Helper()
	row, ok, err := connectors.Get(d, id)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	return row
}

func plain(t *testing.T, row db.Connector) string {
	t.Helper()
	s, err := connectors.Secrets(row, KEY)
	testutil.Must(t, err)
	return jsonx.Stringify(s)
}

func TestCreatesADraftMergesSecretsAndHashesOnlyOnChange(t *testing.T) {
	d := testutil.DB(t)
	a := upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest, Secrets: secrets("botToken", "123:abc")})
	if a.Status != "not_set_up" || a.Enabled || strings.Contains(*a.Secrets, "123:abc") {
		t.Fatalf("%+v", a)
	}
	cfg := jsonx.NewMap[any]()
	cfg.Set("x", 1.0)
	b := upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest, Config: cfg})
	if b.ConfigHash == a.ConfigHash || plain(t, b) != `{"botToken":"123:abc"}` {
		t.Fatalf("%+v", b)
	}
	c := upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest, Secrets: secrets("botToken", "")})
	if c.Secrets != nil {
		t.Fatal("secrets kept")
	}
	if m, _ := connectors.MaskedSecrets(c, KEY); m.Len() != 0 {
		t.Fatal("masked")
	}
}

func TestPatchesRuntimeLearnedSecretsWithoutChangingTheHash(t *testing.T) {
	d := testutil.DB(t)
	a := upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest, Secrets: secrets("botToken", "123:abc")})
	testutil.Must(t, connectors.PatchSecrets(d, "telegram", secrets("token", "t3-token"), KEY, 2))
	b := get(t, d, "telegram")
	if plain(t, b) != `{"botToken":"123:abc","token":"t3-token"}` || strings.Contains(*b.Secrets, "t3-token") || b.ConfigHash != a.ConfigHash {
		t.Fatalf("%+v", b)
	}
	testutil.Must(t, connectors.PatchSecrets(d, "telegram", secrets("token", ""), KEY, 3))
	if plain(t, get(t, d, "telegram")) != `{"botToken":"123:abc"}` {
		t.Fatal("not removed")
	}
	m, _ := connectors.MaskedSecrets(get(t, d, "telegram"), KEY)
	if v, _ := m.Get("botToken"); v != "••••" {
		t.Fatal(v)
	}
}

func TestMovesThroughConnectPauseResumeRemoveWithEvents(t *testing.T) {
	d := testutil.DB(t)
	upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest})
	testutil.Must(t, connectors.Connect(d, "telegram", 1))
	if r := get(t, d, "telegram"); !r.Enabled || r.Status != "connecting" {
		t.Fatalf("%+v", r)
	}
	testutil.Must(t, connectors.SetStatus(d, "telegram", "on", testutil.Ptr("@palui_bot"), 2))
	testutil.Must(t, connectors.Pause(d, "telegram", "chat", 3))
	if r := get(t, d, "telegram"); r.Enabled || r.Status != "paused" {
		t.Fatalf("%+v", r)
	}
	testutil.Must(t, connectors.Resume(d, "telegram", "", 4))
	if r := get(t, d, "telegram"); !r.Enabled || r.Status != "connecting" {
		t.Fatalf("%+v", r)
	}
	testutil.Must(t, connectors.Delete(d, "telegram", 5))
	if _, ok, _ := connectors.Get(d, "telegram"); ok {
		t.Fatal("still there")
	}
	list, _ := events.List(d, events.ListOptions{})
	var kinds []string
	for _, e := range list {
		kinds = append(kinds, e.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"remove", "resume", "pause", "status", "connect"}) {
		t.Fatal(kinds)
	}
	if list[2].Message != "Paused from chat" || list[1].Message != "Resumed from dashboard" || list[3].Message != "on: @palui_bot" {
		t.Fatalf("%+v", list)
	}
}

func TestRecordsErrorSinceOnceAndClearsItOnRecovery(t *testing.T) {
	d := testutil.DB(t)
	upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest})
	testutil.Must(t, connectors.SetStatus(d, "telegram", "error", testutil.Ptr("boom"), 1000))
	testutil.Must(t, connectors.SetStatus(d, "telegram", "error", testutil.Ptr("boom again"), 5000))
	if r := get(t, d, "telegram"); *r.ErrorSince != 1000 {
		t.Fatal(*r.ErrorSince)
	}
	testutil.Must(t, connectors.SetStatus(d, "telegram", "on", nil, 6000))
	if r := get(t, d, "telegram"); r.ErrorSince != nil {
		t.Fatal(*r.ErrorSince)
	}
}

func TestRemovesRowsOfRetiredTypes(t *testing.T) {
	d := testutil.DB(t)
	upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest})
	upsert(t, d, connectors.UpsertInput{ID: "routines", Manifest: connectors.RowManifest{ID: "routines", Kind: "skill", Name: "Routines"}})
	if n, _ := connectors.RemoveRetired(d, 1); n != 1 {
		t.Fatal(n)
	}
	if _, ok, _ := connectors.Get(d, "routines"); ok {
		t.Fatal("routines kept")
	}
	get(t, d, "telegram")
	if n, _ := connectors.RemoveRetired(d, 1); n != 0 {
		t.Fatal(n)
	}
}

func TestConfigHashAndPatchConfig(t *testing.T) {
	d := testutil.DB(t)
	cfg := jsonx.NewMap[any]()
	cfg.Set("b", 1.0)
	cfg.Set("a", "x")
	row := upsert(t, d, connectors.UpsertInput{ID: "telegram", Manifest: manifest, Config: cfg})
	if row.ConfigHash != connectors.ConfigHash(cfg, nil) || len(row.ConfigHash) != 16 {
		t.Fatal(row.ConfigHash)
	}
	patch := jsonx.NewMap[any]()
	patch.Set("learned", true)
	testutil.Must(t, connectors.PatchConfig(d, "telegram", patch, 2))
	r := get(t, d, "telegram")
	if jsonx.Stringify(r.Config) != `{"b":1,"a":"x","learned":true}` || r.ConfigHash != row.ConfigHash {
		t.Fatal(jsonx.Stringify(r.Config))
	}
	if !connectors.ServesAgent(r, "agt_1") {
		t.Fatal("global")
	}
	owned, _, _ := connectors.SetOwner(d, "telegram", testutil.Ptr("agt_2"), 3)
	if connectors.ServesAgent(owned, "agt_1") || !connectors.ServesAgent(owned, "agt_2") {
		t.Fatal("owned")
	}
}

func TestResolveData(t *testing.T) {
	owner := "agt_sales"
	empty := ""
	global := db.Connector{ID: "g"}
	blank := db.Connector{ID: "b", AgentID: &empty}
	owned := db.Connector{ID: "o", AgentID: &owner}
	cases := []struct {
		declared string
		row      db.Connector
		want     string
	}{
		{connectors.DataChat, global, connectors.DataChat},
		{connectors.DataChat, owned, connectors.DataChat},
		{connectors.DataNone, global, connectors.DataNone},
		{connectors.DataNone, owned, connectors.DataNone},
		{connectors.DataAgent, global, connectors.DataAgent},
		{connectors.DataAgent, owned, connectors.DataAgent},
		{connectors.DataShared, global, connectors.DataShared},
		{connectors.DataShared, owned, connectors.DataShared},
		{"", global, connectors.DataShared},
		{"", blank, connectors.DataShared},
		{"", owned, connectors.DataAgent},
		{"chats", global, connectors.DataShared},
		{"None", owned, connectors.DataAgent},
	}
	for _, c := range cases {
		if got := connectors.ResolveData(c.declared, c.row); got != c.want {
			t.Errorf("ResolveData(%q, %v) = %q, want %q", c.declared, c.row.AgentID, got, c.want)
		}
	}

	// The owner's mark on an MCP server: only the boolean true counts.
	marked := func(typ string, agentID *string, v any, set bool) db.Connector {
		cfg := jsonx.NewMap[any]()
		if set {
			cfg.Set(connectors.NoPrivateDataKey, v)
		}
		return db.Connector{ID: "m", Type: typ, AgentID: agentID, Config: cfg}
	}
	markCases := []struct {
		name     string
		declared string
		row      db.Connector
		want     string
	}{
		{"global true", "", marked("mcp", nil, true, true), connectors.DataNone},
		{"owned true", "", marked("mcp", &owner, true, true), connectors.DataNone},
		{"string", "", marked("mcp", nil, "true", true), connectors.DataShared},
		{"number", "", marked("mcp", &owner, float64(1), true), connectors.DataAgent},
		{"false", "", marked("mcp", nil, false, true), connectors.DataShared},
		{"missing", "", marked("mcp", &owner, nil, false), connectors.DataAgent},
		{"not mcp", "", marked("gcal", nil, true, true), connectors.DataShared},
		{"nil config", "", db.Connector{ID: "m", Type: "mcp"}, connectors.DataShared},
		{"declared shared stands", connectors.DataShared, marked("mcp", nil, true, true), connectors.DataShared},
		{"declared agent stands", connectors.DataAgent, marked("mcp", nil, true, true), connectors.DataAgent},
	}
	for _, c := range markCases {
		if got := connectors.ResolveData(c.declared, c.row); got != c.want {
			t.Errorf("%s: ResolveData = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSetNoPrivateData(t *testing.T) {
	d := testutil.DB(t)
	cfg := jsonx.NewMap[any]()
	cfg.Set("url", "https://prayer.example.com/mcp")
	row, err := connectors.Upsert(d, connectors.UpsertInput{ID: "mcp-x", Manifest: connectors.RowManifest{ID: "mcp", Kind: "tools", Name: "Prayer"}, Config: cfg}, KEY, 1)
	if err != nil {
		t.Fatal(err)
	}
	on, ok, err := connectors.SetNoPrivateData(d, "mcp-x", true, 2)
	if err != nil || !ok || !connectors.NoPrivateData(on) || on.ConfigHash != row.ConfigHash || on.UpdatedAt != 2 {
		t.Fatal(on, ok, err)
	}
	if u, _ := on.Config.Get("url"); u != "https://prayer.example.com/mcp" {
		t.Fatal(on.Config)
	}
	stored, _, _ := connectors.Get(d, "mcp-x")
	if !connectors.NoPrivateData(stored) {
		t.Fatal(stored.Config)
	}
	off, ok, err := connectors.SetNoPrivateData(d, "mcp-x", false, 3)
	if err != nil || !ok || connectors.NoPrivateData(off) || off.ConfigHash != row.ConfigHash {
		t.Fatal(off, ok, err)
	}
	if _, has := off.Config.Get(connectors.NoPrivateDataKey); has {
		t.Fatal(off.Config)
	}
	if _, ok, err := connectors.SetNoPrivateData(d, "mcp-gone", true, 4); ok || err != nil {
		t.Fatal(ok, err)
	}
}
