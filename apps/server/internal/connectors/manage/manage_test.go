package manage_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/manage"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// The wizard and state cases of apps/app/src/server/connectors.test.ts (the
// remove cases go with M8b's connector endpoints).

const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDppqq"

func secrets(kv ...string) *jsonx.Map[string] {
	m := jsonx.NewMap[string]()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

func config(kv ...any) *jsonx.Object {
	m := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

func TestSaveDraftFieldByFieldAndPointsAtTheBadOne(t *testing.T) {
	d := testutil.DB(t)
	_, err := manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "telegram", Secrets: secrets("botToken", "nope")}, testutil.TestKey, 1000)
	me, ok := manage.IsError(err)
	if !ok || me.Message != "Some fields need a fix." {
		t.Fatalf("%v", err)
	}
	if msg, _ := me.Fields.Get("botToken"); !strings.Contains(strings.ToLower(msg), "bot token") {
		t.Fatalf("field message %q", msg)
	}
	if !strings.Contains(me.FieldsText(), "botToken: ") {
		t.Fatal(me.FieldsText())
	}
	row, err := manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "telegram", Secrets: secrets("botToken", token)}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	if row.Status != "not_set_up" {
		t.Fatal(row.Status)
	}
	got, _ := connectors.Secrets(row, testutil.TestKey)
	if v, _ := got.Get("botToken"); v != token {
		t.Fatal(v)
	}
	items, err := manage.ListItems(d, testutil.TestKey)
	testutil.Must(t, err)
	if v, _ := items[0].Secrets.Get("botToken"); v != "••••ppqq" {
		t.Fatal(v)
	}
	// Unknown types, bad ids and a type clash.
	if _, err := manage.SaveDraft(d, manage.DraftInput{ID: "x", Type: "nope"}, testutil.TestKey, 1000); err == nil || err.Error() != `Unknown connector type "nope".` {
		t.Fatal(err)
	}
	if _, err := manage.SaveDraft(d, manage.DraftInput{ID: "Bad ID", Type: "telegram"}, testutil.TestKey, 1000); err == nil || err.Error() != "Connector ids use lowercase letters, digits and dashes." {
		t.Fatal(err)
	}
	if _, err := manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "cliproxy"}, testutil.TestKey, 1000); err == nil || err.Error() != `"telegram" is already used by another connector.` {
		t.Fatal(err)
	}
}

func TestConnectNeedsTheRequiredFieldsThenMarksItConnecting(t *testing.T) {
	d := testutil.DB(t)
	_, err := manage.SaveDraft(d, manage.DraftInput{ID: "cliproxy", Type: "cliproxy", Config: config("baseUrl", "http://localhost:8317")}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	err = manage.Connect(d, "cliproxy", testutil.TestKey, 1000)
	me, ok := manage.IsError(err)
	if !ok || me.Message != "Fill in the required fields first." {
		t.Fatal(err)
	}
	if v, _ := me.Fields.Get("apiKey"); v != "Required" {
		t.Fatal(v)
	}
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "cliproxy", Type: "cliproxy", Secrets: secrets("apiKey", "sk-123456789")}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	testutil.Must(t, manage.Connect(d, "cliproxy", testutil.TestKey, 2000))
	row, _, _ := connectors.Get(d, "cliproxy")
	if !row.Enabled || row.Status != "connecting" {
		t.Fatalf("%+v", row)
	}
	// An emptied field clears the stored value; other keys stay.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "cliproxy", Type: "cliproxy", Config: config("baseUrl", "")}, testutil.TestKey, 3000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "cliproxy")
	if _, has := row.Config.Get("baseUrl"); has {
		t.Fatal("baseUrl kept")
	}
	if err := manage.Connect(d, "nope", testutil.TestKey, 1000); err == nil || !strings.Contains(err.Error(), `No connector "nope"`) {
		t.Fatal(err)
	}
}

func TestAgentScopedConnectors(t *testing.T) {
	d := testutil.DB(t)
	chef, err := agents.Create(d, agents.Input{Slug: "chef", Name: testutil.Ptr("Chef"), Persona: testutil.Ptr("p")}, 1000)
	testutil.Must(t, err)
	id := chef.ID
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", Config: config("url", "https://crm.example.com/mcp"), AgentIDs: []string{id}, AgentIDsSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ := connectors.Get(d, "mcp-crm")
	if strings.Join(row.AgentIDs, ",") != chef.ID {
		t.Fatal("owner not saved")
	}
	gone := "agt_gone"
	if _, err := manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", AgentIDs: []string{id, gone}, AgentIDsSet: true}, testutil.TestKey, 1000); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatal(err)
	}
	// Left out, the owner stays; null makes it global.
	label := "CRM"
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", Label: &label}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "mcp-crm")
	if strings.Join(row.AgentIDs, ",") != chef.ID || row.Label != "CRM" {
		t.Fatal("owner lost")
	}
	// Several agents, blanks and repeats dropped, in the order given.
	sales, err := agents.Create(d, agents.Input{Slug: "sales", Name: testutil.Ptr("Sales"), Persona: testutil.Ptr("p")}, 1000)
	testutil.Must(t, err)
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", AgentIDs: []string{sales.ID, "", chef.ID, sales.ID}, AgentIDsSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "mcp-crm")
	if strings.Join(row.AgentIDs, ",") != sales.ID+","+chef.ID || !connectors.ServesAgent(row, chef.ID) || !connectors.ServesAgent(row, sales.ID) || connectors.ServesAgent(row, "agt_other") {
		t.Fatal(row.AgentIDs)
	}
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", AgentIDsSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "mcp-crm")
	if row.AgentIDs != nil {
		t.Fatal("not global")
	}
	// A channel never gets an owner.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "telegram", Secrets: secrets("botToken", token), AgentIDs: []string{id}, AgentIDsSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "telegram")
	if row.AgentIDs != nil {
		t.Fatal("channel owned")
	}
}

// The owner vouched for the server at one address: pointing it elsewhere
// drops the no-private-data mark. Anything else keeps it.
func TestSaveDraftClearsTheNoPrivateDataMarkOnANewURL(t *testing.T) {
	d := testutil.DB(t)
	mark := func() {
		t.Helper()
		_, _, err := connectors.SetNoPrivateData(d, "mcp-x", true, 1500)
		testutil.Must(t, err)
	}
	marked := func() bool {
		t.Helper()
		row, _, err := connectors.Get(d, "mcp-x")
		testutil.Must(t, err)
		return connectors.NoPrivateData(row)
	}
	_, err := manage.SaveDraft(d, manage.DraftInput{ID: "mcp-x", Type: "mcp", Config: config("url", "https://prayer.example.com/mcp")}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	mark()

	// The same url, or only tool preferences: the mark stays.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-x", Type: "mcp", Config: config("url", "https://prayer.example.com/mcp")}, testutil.TestKey, 2000)
	testutil.Must(t, err)
	if !marked() {
		t.Fatal("same url cleared the mark")
	}
	prefs := jsonx.NewMap[any]()
	prefs.Set("get_times", config("enabled", true, "class", "read"))
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-x", Type: "mcp", Config: config("toolPrefs", prefs)}, testutil.TestKey, 3000)
	testutil.Must(t, err)
	if !marked() {
		t.Fatal("tool prefs cleared the mark")
	}

	// A new url clears it, even when the draft sends the mark too.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-x", Type: "mcp", Config: config("url", "https://other.example.com/mcp", connectors.NoPrivateDataKey, true)}, testutil.TestKey, 4000)
	testutil.Must(t, err)
	row, _, _ := connectors.Get(d, "mcp-x")
	if _, has := row.Config.Get(connectors.NoPrivateDataKey); has {
		t.Fatal(row.Config)
	}
	if u, _ := row.Config.Get("url"); u != "https://other.example.com/mcp" {
		t.Fatal(row.Config)
	}
	// The clear leaves a trail, like setConnectorDataFn.
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM events WHERE kind = 'mcp.data' AND connector_id = 'mcp-x' AND at = 4000").Scan(&n))
	if n != 1 {
		t.Fatalf("mcp.data events at the clear: %d", n)
	}

	// An emptied url clears it too.
	mark()
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-x", Type: "mcp", Config: config("url", "")}, testutil.TestKey, 5000)
	testutil.Must(t, err)
	if marked() {
		t.Fatal("emptied url kept the mark")
	}
}

func TestPauseAndResume(t *testing.T) {
	d := testutil.DB(t)
	_, err := manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "telegram", Secrets: secrets("botToken", token)}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	if err := manage.Pause(d, "telegram", "cli", 1000); err == nil || err.Error() != "Telegram isn't set up yet, so there's nothing to pause." {
		t.Fatal(err)
	}
	if err := manage.Resume(d, "telegram", "cli", 1000); err == nil || err.Error() != "Telegram isn't set up yet. Open it on the Connectors page and connect it first." {
		t.Fatal(err)
	}
	testutil.Must(t, manage.Connect(d, "telegram", testutil.TestKey, 1000))
	testutil.Must(t, manage.Pause(d, "telegram", "cli", 2000))
	row, _, _ := connectors.Get(d, "telegram")
	if row.Status != "paused" || row.Enabled {
		t.Fatalf("%+v", row)
	}
	testutil.Must(t, manage.Resume(d, "telegram", "cli", 3000))
	row, _, _ = connectors.Get(d, "telegram")
	if row.Status != "connecting" || !row.Enabled {
		t.Fatalf("%+v", row)
	}
	var msg string
	testutil.Must(t, d.QueryRow("SELECT message FROM events WHERE kind = 'pause'").Scan(&msg))
	if msg != "Paused from cli" {
		t.Fatal(msg)
	}
}

func TestDuplicateCopiesEverythingIntoAPausedDraft(t *testing.T) {
	d := testutil.DB(t)
	chef, err := agents.Create(d, agents.Input{Slug: "chef", Name: testutil.Ptr("Chef"), Persona: testutil.Ptr("p")}, 1000)
	testutil.Must(t, err)
	trust := "external"
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm-2", Type: "mcp", Label: testutil.Ptr("CRM"), Trust: &trust,
		Config: config("url", "https://crm.example.com/mcp", "authMode", "bearer"), Secrets: secrets("token", "sk-crm-secret"),
		AgentIDs: []string{chef.ID}, AgentIDsSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	testutil.Must(t, connectors.PatchConfig(d, "mcp-crm-2", config("addedFromChat", true), 1000))
	testutil.Must(t, manage.Connect(d, "mcp-crm-2", testutil.TestKey, 1000))
	// mcp-crm-3 is taken, so the copy of mcp-crm-2 is mcp-crm-4.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm-3", Type: "mcp"}, testutil.TestKey, 1000)
	testutil.Must(t, err)

	copied, err := manage.Duplicate(d, "mcp-crm-2", testutil.TestKey, 2000)
	testutil.Must(t, err)
	if copied.ID != "mcp-crm-4" || copied.Label != "CRM (copy)" || copied.Trust != "external" || copied.Status != "not_set_up" || copied.Enabled {
		t.Fatalf("%+v", copied)
	}
	if strings.Join(copied.AgentIDs, ",") != chef.ID {
		t.Fatal(copied.AgentIDs)
	}
	if url, _ := copied.Config.Get("url"); url != "https://crm.example.com/mcp" {
		t.Fatal(jsonx.Stringify(copied.Config))
	}
	if _, ok := copied.Config.Get("addedFromChat"); ok {
		t.Fatal("a dashboard copy can't be removed from chat")
	}
	s, err := connectors.Secrets(copied, testutil.TestKey)
	testutil.Must(t, err)
	if tok, _ := s.Get("token"); tok != "sk-crm-secret" {
		t.Fatal(tok)
	}
	// Changing the copy leaves the original alone.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: copied.ID, Type: "mcp", Secrets: secrets("token", "sk-other"), AgentIDsSet: true}, testutil.TestKey, 3000)
	testutil.Must(t, err)
	orig, _, _ := connectors.Get(d, "mcp-crm-2")
	s, _ = connectors.Secrets(orig, testutil.TestKey)
	if tok, _ := s.Get("token"); tok != "sk-crm-secret" || strings.Join(orig.AgentIDs, ",") != chef.ID {
		t.Fatal("original changed")
	}

	// A type there's only one of can't be copied.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "telegram", Secrets: secrets("botToken", token)}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	if _, err := manage.Duplicate(d, "telegram", testutil.TestKey, 2000); err == nil || !strings.Contains(err.Error(), "can't be duplicated") {
		t.Fatal(err)
	}
	if _, err := manage.Duplicate(d, "nope", testutil.TestKey, 2000); err == nil {
		t.Fatal("missing connector copied")
	}
}

func TestDeletingAnAgentLeavesItsSharedConnectorsWithTheOthers(t *testing.T) {
	d := testutil.DB(t)
	mk := func(slug string) string {
		a, err := agents.Create(d, agents.Input{Slug: slug, Name: testutil.Ptr(slug), Persona: testutil.Ptr("p")}, 1000)
		testutil.Must(t, err)
		return a.ID
	}
	chef, sales := mk("chef"), mk("sales")
	for id, ids := range map[string][]string{"mcp-solo": {chef}, "mcp-shared": {chef, sales}, "mcp-other": {sales}} {
		_, err := manage.SaveDraft(d, manage.DraftInput{ID: id, Type: "mcp", Config: config("url", "https://"+id+".example.com/mcp"), AgentIDs: ids, AgentIDsSet: true}, testutil.TestKey, 1000)
		testutil.Must(t, err)
		testutil.Must(t, connectors.Connect(d, id, 1000))
	}
	_, err := agents.Delete(d, chef, 2000)
	testutil.Must(t, err)
	row := func(id string) (string, bool, string) {
		r, _, _ := connectors.Get(d, id)
		var first *string
		testutil.Must(t, d.QueryRow("SELECT agent_id FROM connectors WHERE id = ?", id).Scan(&first))
		f := ""
		if first != nil {
			f = *first
		}
		return strings.Join(r.AgentIDs, ","), r.Enabled, f
	}
	// Only chef used it: paused, still chef's, so it serves nobody.
	if ids, on, first := row("mcp-solo"); ids != chef || on || first != chef {
		t.Fatal("solo", ids, on, first)
	}
	if ids, on, first := row("mcp-shared"); ids != sales || !on || first != sales {
		t.Fatal("shared", ids, on, first)
	}
	if ids, on, _ := row("mcp-other"); ids != sales || !on {
		t.Fatal("other", ids, on)
	}
}

// rpt_0mwmyqranb28lw1ivey: a voice connector added after the per-agent switch
// shipped left the owner's own agent off. Adding the first transcriber now
// turns voice notes on for the default agent; other agents and later saves
// leave the switch alone.
func TestNewVoiceConnectorTurnsOnTheDefaultAgent(t *testing.T) {
	d := testutil.DB(t)
	def, err := agents.GetDefault(d, 1000)
	testutil.Must(t, err)
	other, err := agents.Create(d, agents.Input{Slug: "kebun", Name: ptr("Kebun"), Persona: ptr("Kamu agent kebun.")}, 1000)
	testutil.Must(t, err)
	if def.TranscribeVoice || other.TranscribeVoice {
		t.Fatal("agents start off")
	}
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "voice", Type: "voice", Secrets: secrets("elevenLabsKey", "sk_1234567890abcdef")}, testutil.TestKey, 2000)
	testutil.Must(t, err)
	def, _, _ = agents.Get(d, def.ID)
	other, _, _ = agents.Get(d, other.ID)
	if !def.TranscribeVoice || other.TranscribeVoice {
		t.Fatalf("default %v (want on), other %v (want off)", def.TranscribeVoice, other.TranscribeVoice)
	}
	// The owner turns it off again; editing the connector keeps it off.
	no := false
	_, err = agents.Update(d, def.ID, agents.Input{TranscribeVoice: &no}, 3000)
	testutil.Must(t, err)
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "voice", Type: "voice", Config: config("language", "en")}, testutil.TestKey, 4000)
	testutil.Must(t, err)
	def, _, _ = agents.Get(d, def.ID)
	if def.TranscribeVoice {
		t.Fatal("a later save turned it back on")
	}
}
func ptr[T any](v T) *T { return &v }
