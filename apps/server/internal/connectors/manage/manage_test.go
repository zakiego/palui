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
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", Config: config("url", "https://crm.example.com/mcp"), AgentID: &id, AgentIDSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ := connectors.Get(d, "mcp-crm")
	if row.AgentID == nil || *row.AgentID != chef.ID {
		t.Fatal("owner not saved")
	}
	gone := "agt_gone"
	if _, err := manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", AgentID: &gone, AgentIDSet: true}, testutil.TestKey, 1000); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatal(err)
	}
	// Left out, the owner stays; null makes it global.
	label := "CRM"
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", Label: &label}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "mcp-crm")
	if row.AgentID == nil || *row.AgentID != chef.ID || row.Label != "CRM" {
		t.Fatal("owner lost")
	}
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "mcp-crm", Type: "mcp", AgentIDSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "mcp-crm")
	if row.AgentID != nil {
		t.Fatal("not global")
	}
	// A channel never gets an owner.
	_, err = manage.SaveDraft(d, manage.DraftInput{ID: "telegram", Type: "telegram", Secrets: secrets("botToken", token), AgentID: &id, AgentIDSet: true}, testutil.TestKey, 1000)
	testutil.Must(t, err)
	row, _, _ = connectors.Get(d, "telegram")
	if row.AgentID != nil {
		t.Fatal("channel owned")
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
