package settings_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/cliproxy"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	settingsconn "github.com/zakiego/palui/apps/server/internal/connectors/settings"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

var KEY = strings.Repeat("0", 64)

func setup(t *testing.T) *db.DB {
	d := testutil.DB(t)
	sec := jsonx.NewMap[string]()
	sec.Set("apiKey", "sk-very-secret")
	cfg := jsonx.NewMap[any]()
	cfg.Set("baseUrl", "http://127.0.0.1:8317/v1")
	_, err := connectors.Upsert(d, connectors.UpsertInput{ID: "cliproxy", Manifest: cliproxy.Manifest.Row(), Config: cfg, Secrets: sec}, KEY, 1)
	testutil.Must(t, err)
	return d
}

func tools(t *testing.T, d *db.DB, clock func() int64, agentID string) *connectors.ToolSet {
	set, err := settingsconn.Tools(d, clock, agentID)
	testutil.Must(t, err)
	return set
}

func hasSettingsEvent(t *testing.T, d *db.DB) bool {
	list, err := events.List(d, events.ListOptions{Limit: 5})
	testutil.Must(t, err)
	for _, e := range list {
		if e.Kind == "settings.update" {
			return true
		}
	}
	return false
}

func TestClassesReadingIsReadChangingIsDestructive(t *testing.T) {
	if got := strings.Join(conntest.Classes(tools(t, testutil.DB(t), nil, "")), ","); got != "get_settings:read,update_settings:destructive" {
		t.Fatal(got)
	}
}

func TestReadsTheSetupWithoutAnySecret(t *testing.T) {
	d := setup(t)
	out := conntest.Call(t, tools(t, d, nil, ""), "get_settings", `{}`)
	defs := settings.Defaults()
	if !out.B("ok") || out.S("settings.timezone") != defs.Timezone {
		t.Fatal(out)
	}
	if out.JSON("settings.budgets") != conntest.Of(defs.Budgets).String() {
		t.Fatal(out.JSON("settings.budgets"))
	}
	if ids := out.Strs("models", "id"); len(ids) != 1 || ids[0] != "cliproxy" {
		t.Fatal(ids)
	}
	if out.N("usage.tokensToday") != 0 || out.Get("usage.pausedUntil") != nil || !out.Has("usage.pausedUntil") {
		t.Fatal(out.JSON("usage"))
	}
	if _, ok := out.Get("version").(string); !ok {
		t.Fatal("no version")
	}
	if strings.Contains(out.String(), "sk-very-secret") || out.Has("settings.persona") {
		t.Fatal("leaked")
	}
}

func TestChangesOnlyTheKeysGivenAndMergesPartialBudgets(t *testing.T) {
	d := setup(t)
	out := conntest.Call(t, tools(t, d, func() int64 { return 1000 }, ""), "update_settings", `{"timezone":"Asia/Jakarta","budgets":{"dailyTokenCap":3000000}}`)
	if !out.B("ok") {
		t.Fatal(out)
	}
	keys := out.Get("changed").(*jsonx.Object).Keys()
	if strings.Join(keys, ",") != "timezone,budgets" {
		t.Fatal(keys)
	}
	s, err := settings.All(d)
	testutil.Must(t, err)
	want := settings.Defaults().Budgets
	want.DailyTokenCap = 3_000_000
	if s.Timezone != "Asia/Jakarta" || s.Budgets != want || s.Language != settings.Defaults().Language {
		t.Fatalf("%+v", s)
	}
	if !hasSettingsEvent(t, d) {
		t.Fatal("no event")
	}
}

func TestReplacesOneModelRoleAndKeepsTheOthers(t *testing.T) {
	d := setup(t)
	out := conntest.Call(t, tools(t, d, nil, ""), "update_settings", `{"modelRoles":{"fast":[{"connectorId":"cliproxy","model":"claude-haiku-4-5"}]}}`)
	if !out.B("ok") {
		t.Fatal(out)
	}
	s, _ := settings.All(d)
	if len(s.ModelRoles.Fast) != 1 || s.ModelRoles.Fast[0].Model != "claude-haiku-4-5" || conntest.Of(s.ModelRoles.Default).String() != conntest.Of(settings.Defaults().ModelRoles.Default).String() {
		t.Fatalf("%+v", s.ModelRoles)
	}
}

func TestRefusesAnUnknownModelConnectorABadTimezoneAndAnOutOfRangeBudget(t *testing.T) {
	d := setup(t)
	set := tools(t, d, nil, "")
	model := conntest.Call(t, set, "update_settings", `{"modelRoles":{"fast":[{"connectorId":"openai","model":"gpt"}]}}`)
	if model.B("ok") || !strings.Contains(model.S("error"), "cliproxy") {
		t.Fatal(model)
	}
	if conntest.Call(t, set, "update_settings", `{"timezone":"Mars/Olympus"}`).B("ok") {
		t.Fatal("bad zone")
	}
	budget := conntest.Call(t, set, "update_settings", `{"budgets":{"maxSteps":500}}`)
	if budget.B("ok") || !strings.Contains(budget.S("error"), "budgets.maxSteps") {
		t.Fatal(budget)
	}
	s, _ := settings.All(d)
	if s.Timezone != settings.Defaults().Timezone {
		t.Fatal(s.Timezone)
	}
}

func TestSaysSoWhenNothingChanges(t *testing.T) {
	d := setup(t)
	out := conntest.Call(t, tools(t, d, nil, ""), "update_settings", `{"timezone":"`+settings.Defaults().Timezone+`"}`)
	if !out.B("ok") || out.JSON("changed") != "{}" {
		t.Fatal(out)
	}
	if hasSettingsEvent(t, d) {
		t.Fatal("event logged")
	}
}

func TestGivesOtherAgentsTheReadToolOnly(t *testing.T) {
	d := testutil.DB(t)
	a, err := agents.Create(d, agents.Input{Slug: "kerja", Name: testutil.Ptr("Kerja"), Persona: testutil.Ptr("Asisten kerja.")}, 1)
	testutil.Must(t, err)
	if keys := tools(t, d, nil, a.ID).Keys(); strings.Join(keys, ",") != "get_settings" {
		t.Fatal(keys)
	}
}

func TestListsOnlyTheConnectorsTheAgentCanCall(t *testing.T) {
	d := testutil.DB(t)
	a, err := agents.Create(d, agents.Input{Slug: "kerja", Name: testutil.Ptr("Kerja"), Persona: testutil.Ptr("Asisten kerja.")}, 1)
	testutil.Must(t, err)
	for _, id := range []string{"mcp-mine", "mcp-theirs"} {
		_, err := connectors.Upsert(d, connectors.UpsertInput{ID: id, Manifest: mcpconn.Manifest.Row()}, KEY, 1)
		testutil.Must(t, err)
	}
	other := "agt_other"
	for id, owner := range map[string]*string{"mcp-mine": &a.ID, "mcp-theirs": &other} {
		_, _, err := connectors.SetOwner(d, id, owner, 1)
		testutil.Must(t, err)
	}
	ids := strings.Join(conntest.Call(t, tools(t, d, nil, a.ID), "get_settings", `{}`).Strs("connectors", "id"), ",")
	if !strings.Contains(ids, "mcp-mine") || strings.Contains(ids, "mcp-theirs") {
		t.Fatal(ids)
	}
}
