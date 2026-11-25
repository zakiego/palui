package rpcsetup_test

import (
	"testing"

	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// applySuggestedToolClassesFn saves the server's suggested class for every
// tool the owner hasn't classified, keeps the saved ones, and logs it once.
func TestApplySuggestedToolClassesFillsOnlyUnsavedTools(t *testing.T) {
	listTools, err := jsonx.ParseObject([]byte(`{"result":{"tools":[` +
		`{"name":"search","suggestedClass":"read","saved":true},` +
		`{"name":"add_note","suggestedClass":"write","saved":false},` +
		`{"name":"list_todos","suggestedClass":"read","saved":false},` +
		`{"name":"mystery","suggestedClass":"destructive","saved":false},` +
		`{"name":"get_notebook","suggestedClass":"destructive","hinted":false,"saved":false}]}}`))
	testutil.Must(t, err)
	actions := jsonx.NewMap[*jsonx.Object]()
	actions.Set("listTools", listTools)
	rt := fakeRuntime(t, []FakeConnector{{ID: "mcp-hub", Enabled: true, Actions: actions}})
	d := testutil.DB(t)
	cfg, err := jsonx.ParseObject([]byte(`{"url":"https://hub.example.com/mcp","authMode":"bearer","toolPrefs":{"search":{"enabled":false,"class":"destructive"}}}`))
	testutil.Must(t, err)
	testutil.SeedConnector(t, d, "mcp-hub", "tools", testutil.SeedOptions{Type: "mcp", Config: cfg}, 1)
	testutil.SeedConnector(t, d, "gcal", "tools", testutil.SeedOptions{}, 1)
	e, token := env(t, d, t.TempDir(), rt, 0, "")

	if status, body := e.Post(token, "applySuggestedToolClassesFn", `{"id":"mcp-hub"}`); status != 200 || body != `{"ok":true,"saved":3}` {
		t.Fatal(status, body)
	}
	r, _ := row(t, d, "mcp-hub")
	prefs := mcpconn.PrefsFrom(r.Config)
	want := map[string]mcpconn.ToolPref{
		"search":     {Enabled: false, Class: "destructive"},
		"add_note":   {Enabled: true, Class: "write"},
		"list_todos": {Enabled: true, Class: "read"},
		"mystery":    {Enabled: true, Class: "destructive"},
	}
	if len(prefs) != len(want) {
		t.Fatal(prefs)
	}
	for k, v := range want {
		if prefs[k] != v {
			t.Fatal(k, prefs[k])
		}
	}
	if url, _ := r.Config.Get("url"); url != "https://hub.example.com/mcp" {
		t.Fatal(url)
	}
	if kinds := eventKinds(t, d); len(kinds) != 1 || kinds[0] != "mcp.classes" {
		t.Fatal(kinds)
	}

	// Again: every tool has a pref now, so nothing changes or gets logged.
	if status, body := e.Post(token, "applySuggestedToolClassesFn", `{"id":"mcp-hub"}`); status != 200 || body != `{"ok":true,"saved":0}` {
		t.Fatal(status, body)
	}
	if kinds := eventKinds(t, d); len(kinds) != 1 {
		t.Fatal(kinds)
	}

	if status, body := e.Post(token, "applySuggestedToolClassesFn", `{"id":"gcal"}`); status != 400 || body != `{"error":"Only an MCP server suggests tool classes.","code":"BAD_REQUEST"}` {
		t.Fatal(status, body)
	}
	if status, body := e.Post(token, "applySuggestedToolClassesFn", `{"id":"mcp-gone"}`); status != 400 || body != `{"error":"That connector doesn't exist anymore.","code":"BAD_REQUEST"}` {
		t.Fatal(status, body)
	}
	if status, _ := e.Post("", "applySuggestedToolClassesFn", `{"id":"mcp-hub"}`); status != 401 {
		t.Fatal(status)
	}
}
