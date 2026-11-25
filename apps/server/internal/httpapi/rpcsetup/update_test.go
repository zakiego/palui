package rpcsetup_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

const kandangToken = "kandang-secret-token-123"

// updateConnectorFn renames a connector, changes its non-secret config and
// merges per-tool prefs into the saved ones, through the dashboard's
// SaveDraft, and never touches or returns secrets.
func TestUpdateConnectorEditsLabelConfigAndToolPrefs(t *testing.T) {
	rt := fakeRuntime(t, nil)
	d := testutil.DB(t)
	cfg, err := jsonx.ParseObject([]byte(`{"url":"https://kandang.example.com/mcp","authMode":"bearer","toolPrefs":{"ls":{"enabled":true,"class":"read","flowApproval":"every_run"},"rm":{"enabled":true,"class":"write"}}}`))
	testutil.Must(t, err)
	sec := jsonx.NewMap[string]()
	sec.Set("token", kandangToken)
	testutil.SeedConnector(t, d, "mcp-kandang", "tools", testutil.SeedOptions{Type: "mcp", Config: cfg, Secrets: sec}, 1)
	e, token := env(t, d, t.TempDir(), rt, 0, testutil.TestKey)

	status, body := e.Post(token, "updateConnectorFn", `{"id":"mcp-kandang","label":"  Kandang  ","config":{"url":"https://kandang2.example.com/mcp"},"toolPrefs":{"run":{"class":"read"},"ls":{"enabled":false},"rm":null},"secrets":{"token":"stolen-token-456"}}`)
	if status != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"label":"Kandang"`) {
		t.Fatal(status, body)
	}
	if strings.Contains(body, kandangToken) || strings.Contains(body, "stolen") {
		t.Fatalf("a secret in the response: %s", body)
	}
	r, _ := row(t, d, "mcp-kandang")
	if r.Label != "Kandang" {
		t.Fatal(r.Label)
	}
	if url, _ := r.Config.Get("url"); url != "https://kandang2.example.com/mcp" {
		t.Fatal(url)
	}
	if mode, _ := r.Config.Get("authMode"); mode != "bearer" {
		t.Fatal(mode)
	}
	prefs := mcpconn.PrefsFrom(r.Config)
	want := map[string]mcpconn.ToolPref{
		// Only enabled sent: the saved class and flow approval stay.
		"ls": {Enabled: false, Class: "read", FlowApproval: "every_run"},
		// Never saved: starts enabled, takes the class sent.
		"run": {Enabled: true, Class: "read"},
	}
	if len(prefs) != len(want) {
		t.Fatal(prefs)
	}
	for k, v := range want {
		if prefs[k] != v {
			t.Fatal(k, prefs[k])
		}
	}
	// The secret sent along was dropped; the saved one is untouched.
	secrets, err := connectors.Secrets(r, testutil.TestKey)
	testutil.Must(t, err)
	if got, _ := secrets.Get("token"); got != kandangToken {
		t.Fatal(got)
	}
	if kinds := eventKinds(t, d); len(kinds) != 1 || kinds[0] != "connector.update" {
		t.Fatal(kinds)
	}

	// A tool sent without a class never becomes safer than destructive.
	if status, body := e.Post(token, "updateConnectorFn", `{"id":"mcp-kandang","toolPrefs":{"new_tool":{"enabled":true}}}`); status != 200 {
		t.Fatal(status, body)
	}
	r, _ = row(t, d, "mcp-kandang")
	if p := mcpconn.PrefsFrom(r.Config)["new_tool"]; p != (mcpconn.ToolPref{Enabled: true, Class: "destructive"}) {
		t.Fatal(p)
	}
}

func TestUpdateConnectorRefusesWhatItCantChange(t *testing.T) {
	d := testutil.DB(t)
	cfg, err := jsonx.ParseObject([]byte(`{"url":"https://kandang.example.com/mcp","authMode":"bearer"}`))
	testutil.Must(t, err)
	testutil.SeedConnector(t, d, "mcp-kandang", "tools", testutil.SeedOptions{Type: "mcp", Config: cfg}, 1)
	testutil.SeedConnector(t, d, "gcal", "tools", testutil.SeedOptions{}, 1)
	e, token := env(t, d, t.TempDir(), nil, 0, testutil.TestKey)

	for _, c := range []struct{ in, want string }{
		{`{"id":"mcp-kandang","config":{"url":"ftp://nope"}}`, `Some fields need a fix. url:`},
		{`{"id":"mcp-kandang","config":{"noPrivateData":true}}`, `config.noPrivateData can't be changed here`},
		{`{"id":"mcp-kandang","config":{"addedFromChat":false}}`, `config.addedFromChat can't be changed here`},
		{`{"id":"mcp-kandang","config":{"toolPrefs":{}}}`, `config.toolPrefs can't be changed here`},
		{`{"id":"mcp-kandang","config":{"token":"x"}}`, `A MCP server connector has no config field \"token\".`},
		{`{"id":"mcp-kandang"}`, `Nothing to change`},
		{`{"id":"gcal","toolPrefs":{"x":{"class":"read"}}}`, `Only an MCP server has per-tool settings.`},
		{`{"id":"mcp-gone","label":"x"}`, `That connector doesn't exist anymore.`},
	} {
		status, body := e.Post(token, "updateConnectorFn", c.in)
		if status != 400 || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s", c.in, status, body)
		}
	}
	r, _ := row(t, d, "mcp-kandang")
	if url, _ := r.Config.Get("url"); url != "https://kandang.example.com/mcp" {
		t.Fatal(url)
	}
	if _, ok := r.Config.Get("noPrivateData"); ok {
		t.Fatal("noPrivateData was set")
	}
	if kinds := eventKinds(t, d); len(kinds) != 0 {
		t.Fatal(kinds)
	}
	if status, _ := e.Post("", "updateConnectorFn", `{"id":"mcp-kandang","label":"x"}`); status != 401 {
		t.Fatal(status)
	}
}

// The tool's input schema has no place for secrets.
func TestUpdateConnectorTakesNoSecrets(t *testing.T) {
	for _, h := range httpapi.Handlers() {
		if h.Name != "updateConnectorFn" {
			continue
		}
		var keys []string
		for _, f := range h.Input.(*zod.ObjectS).Fields() {
			keys = append(keys, f.Key)
		}
		if got := strings.Join(keys, " "); got != "id label config toolPrefs" {
			t.Fatal(got)
		}
		if schema := string(zod.JSONSchemaBytes(h.Input)); strings.Contains(schema, "secret") {
			t.Fatal(schema)
		}
		return
	}
	t.Fatal("updateConnectorFn isn't registered")
}
