package tools_test

// Ports of tools/collect.test.ts, search.test.ts and catalog.test.ts, the
// goldens from testdata/gen/tools.ts, and tests for the server-side caller.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/tools"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var turn = connectors.TurnInfo{RunID: "run_1", ChatID: "chat_1", Channel: "telegram", Trigger: "message", TZ: "Asia/Makassar", Now: 1}

type def struct {
	name, description, class, title string
	schema                          string
	untrusted                       bool
}

func entry(id, kind, label, trust string, defs []def) connectors.RunningEntry {
	return connectors.RunningEntry{ID: id, Row: db.Connector{ID: id, Kind: kind, Label: label, Trust: trust, Type: id}, Handle: &connectors.RunningConnector{
		Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
			set := connectors.NewToolSet()
			for _, d := range defs {
				schema := d.schema
				if schema == "" {
					schema = `{"type":"object","properties":{"q":{"type":"string"}}}`
				}
				set.Set(d.name, connectors.PaluiTool{Class: d.class, Title: d.title, Untrusted: d.untrusted, Tool: connectors.Tool{Description: d.description, InputSchema: json.RawMessage(schema), Execute: func(context.Context, json.RawMessage, connectors.ToolCallInfo) (any, error) { return "ok", nil }}})
			}
			return set, nil
		},
	}}
}

func simple(id, kind string, names ...string) connectors.RunningEntry {
	var defs []def
	for _, n := range names {
		defs = append(defs, def{name: n, description: id + " " + n, class: "read"})
	}
	return entry(id, kind, id, "own", defs)
}

func running() []connectors.RunningEntry {
	return []connectors.RunningEntry{simple("notes", "tools", "add", "list", "remove"), simple("recall", "memory", "search"), simple("skills", "skill", "read"), simple("web", "tools", "fetch", "search")}
}

func opts(maxTools int, sticky []string) tools.CollectOptions {
	return tools.CollectOptions{Turn: turn, MaxTools: maxTools, Sticky: sticky, OnUntrustedResult: func(*tools.ToolMeta) {}}
}

// offered is the names in the order the model is sent them.
func offered(c *tools.Collected) string {
	active := c.Active()
	var out []string
	for _, n := range c.Names() {
		for _, a := range active {
			if a == n {
				out = append(out, n)
			}
		}
	}
	return strings.Join(out, ",")
}

func TestCollectOrder(t *testing.T) {
	ctx := context.Background()
	first := offered(tools.Collect(ctx, running(), opts(64, nil)))
	if first != "skills__read,recall__search,notes__add,notes__list,notes__remove,web__fetch,web__search" {
		t.Fatal(first)
	}
	for i := 0; i < 3; i++ {
		if offered(tools.Collect(ctx, running(), opts(64, nil))) != first {
			t.Fatal("order changed")
		}
	}
	c := tools.Collect(ctx, running(), opts(4, nil))
	if offered(c) != "skills__read,recall__search,notes__add,palui__search_tools" || strings.Join(c.Deferred, ",") != "notes__list,notes__remove,web__fetch,web__search" {
		t.Fatal(offered(c), c.Deferred)
	}
	again := tools.Collect(ctx, running(), opts(4, nil))
	if offered(again) != offered(c) || strings.Join(again.Names(), ",") != strings.Join(c.Names(), ",") {
		t.Fatal("search order changed")
	}
	a := tools.Collect(ctx, running(), opts(4, []string{"web__search", "notes__remove"}))
	b := tools.Collect(ctx, running(), opts(4, []string{"notes__remove", "web__search"}))
	if offered(a) != "skills__read,notes__remove,web__search,palui__search_tools" || offered(b) != offered(a) {
		t.Fatal(offered(a), offered(b))
	}
}

func TestCollectFilters(t *testing.T) {
	ctx := context.Background()
	mixed := entry("alpha", "tools", "Alpha", "external", []def{{name: "read", class: "read"}, {name: "wipe", class: "destructive"}, {name: "odd", class: "weird"}, {name: "note", class: "write"}})
	guest := entry("g", "tools", "G", "own", nil)
	guest.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
		return conntest.Tools("safe", connectors.PaluiTool{Class: "read", GuestSafe: true, Tool: conntest.FnTool("s", nil)}, "unsafe", connectors.PaluiTool{Class: "read", Tool: conntest.FnTool("u", nil)}), nil
	}
	broken := entry("broken", "tools", "B", "own", nil)
	broken.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) { panic("boom") }
	owned := entry("crm", "tools", "CRM", "own", []def{{name: "deals", class: "read"}})
	agent := "agt_sales"
	owned.Row.AgentID = &agent
	all := []connectors.RunningEntry{mixed, guest, broken, owned}
	names := func(o tools.CollectOptions) string { return strings.Join(tools.Collect(ctx, all, o).Names(), ",") }
	o := opts(64, nil)
	if got := names(o); got != "alpha__read,alpha__wipe,alpha__odd,alpha__note,g__safe,g__unsafe" {
		t.Fatal(got)
	}
	c := tools.Collect(ctx, all, o)
	if c.Meta["alpha__odd"].Class != "destructive" || !c.Meta["alpha__read"].Untrusted || c.Meta["g__safe"].Untrusted || strings.Join(c.ConnectorIDs, ",") != "alpha,g" {
		t.Fatal(c.Meta["alpha__odd"], c.ConnectorIDs)
	}
	o.ExcludeDestructive = true
	if got := names(o); got != "alpha__read,alpha__note,g__safe,g__unsafe" {
		t.Fatal(got)
	}
	o = opts(64, nil)
	o.Scope = []string{"g"}
	o.Deny = []string{"g__unsafe"}
	if got := names(o); got != "g__safe" {
		t.Fatal(got)
	}
	o = opts(64, nil)
	o.Only = []string{"alpha__note"}
	if got := names(o); got != "alpha__note" {
		t.Fatal(got)
	}
	o.Only = []string{"*"}
	o.Turn.AgentID = "agt_sales"
	if got := names(o); !strings.HasSuffix(got, "crm__deals") {
		t.Fatal(got)
	}

	// Audience: an outside turn gets chat and no-data tools, never destructive.
	fn := conntest.FnTool("x", nil)
	scoped := entry("sc", "tools", "Scoped", "own", nil)
	scoped.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
		return conntest.Tools(
			"none", connectors.PaluiTool{Class: "read", Data: connectors.DataNone, Tool: fn},
			"chat", connectors.PaluiTool{Class: "write", Data: connectors.DataChat, Tool: fn},
			"agent", connectors.PaluiTool{Class: "read", Data: connectors.DataAgent, Tool: fn},
			"plain", connectors.PaluiTool{Class: "read", Tool: fn},
			"wipe", connectors.PaluiTool{Class: "destructive", Data: connectors.DataChat, Tool: fn},
		), nil
	}
	ownedChat := entry("crmx", "tools", "CRMX", "own", nil)
	ownedChat.Row.AgentID = &agent
	ownedChat.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
		return conntest.Tools("plain", connectors.PaluiTool{Class: "read", Tool: fn}), nil
	}
	aud := []connectors.RunningEntry{mixed, scoped, ownedChat}
	o = opts(64, nil)
	o.Turn.AgentID = agent
	o.Audience = tools.AudienceOutside
	c = tools.Collect(ctx, aud, o)
	if got := strings.Join(c.Names(), ","); got != "sc__none,sc__chat" || strings.Join(c.ConnectorIDs, ",") != "sc" {
		t.Fatal(got, c.ConnectorIDs)
	}
	o.Audience = tools.AudienceOwner
	if got := strings.Join(tools.Collect(ctx, aud, o).Names(), ","); got != "alpha__read,alpha__wipe,alpha__odd,alpha__note,sc__none,sc__chat,sc__agent,sc__plain,sc__wipe,crmx__plain" {
		t.Fatal(got)
	}
	// A members turn also gets the agent's own data, still never destructive
	// or shared.
	o.Audience = tools.AudienceMembers
	if got := strings.Join(tools.Collect(ctx, aud, o).Names(), ","); got != "sc__none,sc__chat,sc__agent,crmx__plain" {
		t.Fatal(got)
	}
	// An audience nobody knows gets nothing.
	o.Audience = "strangers"
	if c := tools.Collect(ctx, aud, o); len(c.Names()) != 0 || len(c.ConnectorIDs) != 0 {
		t.Fatal(c.Names())
	}
}

func TestInAudienceTable(t *testing.T) {
	scopes := []string{connectors.DataNone, connectors.DataChat, connectors.DataAgent, connectors.DataShared, "weird", ""}
	classes := []string{"read", "write", "destructive"}
	for _, aud := range []string{"", tools.AudienceOwner, tools.AudienceMembers, tools.AudienceOutside, "strangers"} {
		for _, scope := range scopes {
			for _, class := range classes {
				var want bool
				switch aud {
				case "", tools.AudienceOwner:
					want = true
				case tools.AudienceMembers:
					want = (scope == connectors.DataNone || scope == connectors.DataChat || scope == connectors.DataAgent) && class != "destructive"
				case tools.AudienceOutside:
					want = (scope == connectors.DataNone || scope == connectors.DataChat) && class != "destructive"
				}
				if got := tools.InAudience(aud, scope, class); got != want {
					t.Fatalf("%q %q %q: %v", aud, scope, class, got)
				}
			}
		}
	}
	if !tools.InAudience(tools.AudienceMembers, connectors.DataAgent, "read") || tools.InAudience(tools.AudienceMembers, connectors.DataShared, "read") {
		t.Fatal("members")
	}
	for aud, want := range map[string]bool{"": false, tools.AudienceOwner: false, tools.AudienceMembers: true, tools.AudienceOutside: true, "strangers": true} {
		if tools.NoApprovals(aud) != want {
			t.Fatal(aud)
		}
	}
}

func TestMembersTurnGetsAgentDataOnly(t *testing.T) {
	ctx := context.Background()
	fn := conntest.FnTool("x", nil)
	e := entry("mix", "tools", "Mix", "own", nil)
	e.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
		return conntest.Tools(
			"a", connectors.PaluiTool{Class: "read", Data: connectors.DataNone, Tool: connectors.Tool{Description: "no data", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"b", connectors.PaluiTool{Class: "write", Data: connectors.DataChat, Tool: connectors.Tool{Description: "chat thing", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"c", connectors.PaluiTool{Class: "read", Data: connectors.DataAgent, Tool: connectors.Tool{Description: "agent ledger", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"d", connectors.PaluiTool{Class: "destructive", Data: connectors.DataAgent, Tool: connectors.Tool{Description: "agent wipe", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"secret", connectors.PaluiTool{Class: "read", Data: connectors.DataShared, Tool: connectors.Tool{Description: "private owner balance", InputSchema: fn.InputSchema, Execute: fn.Execute}},
		), nil
	}
	o := opts(2, nil)
	o.Audience = tools.AudienceMembers
	c := tools.Collect(ctx, []connectors.RunningEntry{e}, o)
	if got := strings.Join(c.Names(), ","); got != "mix__a,mix__b,mix__c,palui__search_tools" {
		t.Fatal(got)
	}
	for _, n := range []string{"mix__d", "mix__secret"} {
		if c.Meta[n] != nil || strings.Contains(strings.Join(c.Active(), ","), n) || strings.Contains(strings.Join(c.Deferred, ","), n) {
			t.Fatal(n, c.Active(), c.Deferred)
		}
	}
	search, _ := c.Tools.Get(tools.SearchTool)
	for _, in := range []string{`{"names":["mix__secret","mix__d"]}`, `{"names":["secret"]}`, `{"query":"private owner balance"}`, `{"query":"agent wipe"}`} {
		out, err := search.Execute(ctx, json.RawMessage(in), connectors.ToolCallInfo{})
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range out.(tools.SearchResult).Loaded {
			if l.Name == "mix__secret" || l.Name == "mix__d" {
				t.Fatal(in, l.Name)
			}
		}
	}
}

func TestSearchNeverLoadsToolsOutsideTheAudience(t *testing.T) {
	ctx := context.Background()
	fn := conntest.FnTool("x", nil)
	e := entry("mix", "tools", "Mix", "own", nil)
	e.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
		return conntest.Tools(
			"a", connectors.PaluiTool{Class: "read", Data: connectors.DataChat, Tool: connectors.Tool{Description: "chat one", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"b", connectors.PaluiTool{Class: "read", Data: connectors.DataChat, Tool: connectors.Tool{Description: "chat two", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"c", connectors.PaluiTool{Class: "read", Data: connectors.DataNone, Tool: connectors.Tool{Description: "chat three", InputSchema: fn.InputSchema, Execute: fn.Execute}},
			"secret", connectors.PaluiTool{Class: "read", Data: connectors.DataAgent, Tool: connectors.Tool{Description: "private ledger balance", InputSchema: fn.InputSchema, Execute: fn.Execute}},
		), nil
	}
	o := opts(2, []string{"mix__secret"})
	o.Audience = tools.AudienceOutside
	c := tools.Collect(ctx, []connectors.RunningEntry{e}, o)
	if c.Meta["mix__secret"] != nil || strings.Contains(strings.Join(c.Active(), ","), "secret") || strings.Contains(strings.Join(c.Deferred, ","), "secret") {
		t.Fatal(c.Active(), c.Deferred)
	}
	if len(c.Deferred) == 0 {
		t.Fatal("tools should defer", c.Active())
	}
	before := strings.Join(c.Active(), ",")
	search, _ := c.Tools.Get(tools.SearchTool)
	for _, in := range []string{`{"names":["mix__secret"]}`, `{"names":["secret"]}`, `{"query":"private ledger balance"}`} {
		out, err := search.Execute(ctx, json.RawMessage(in), connectors.ToolCallInfo{})
		if err != nil {
			t.Fatal(err)
		}
		if r := out.(tools.SearchResult); len(r.Loaded) != 0 {
			t.Fatal(in, r.Loaded)
		}
	}
	if strings.Join(c.Active(), ",") != before {
		t.Fatal(c.Active())
	}
}

func TestOutsideTurnRefusesAToolThatAsksOnItsOwn(t *testing.T) {
	ctx := context.Background()
	ran := 0
	e := entry("share", "tools", "Share", "own", nil)
	e.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
		return conntest.Tools("go", connectors.PaluiTool{Class: "write", Data: connectors.DataChat, NeedsApproval: func(json.RawMessage) bool { return true }, Tool: conntest.FnTool("x", func(*jsonx.Object) (any, error) { ran++; return "done", nil })}), nil
	}
	var started []string
	run := func(audience string) (any, error) {
		o := opts(64, nil)
		o.Audience = audience
		o.OnToolStart = func(id string) { started = append(started, id) }
		c := tools.Collect(ctx, []connectors.RunningEntry{e}, o)
		tl, ok := c.Tools.Get("share__go")
		if !ok {
			t.Fatal("missing", audience)
		}
		return tl.Execute(ctx, json.RawMessage(`{}`), connectors.ToolCallInfo{ToolCallID: "c-" + audience})
	}
	if _, err := run(tools.AudienceOutside); err == nil || err.Error() != tools.OutsideApprovalError || ran != 0 {
		t.Fatal(err, ran)
	}
	if _, err := run(tools.AudienceMembers); err == nil || err.Error() != tools.OutsideApprovalError || ran != 0 {
		t.Fatal(err, ran)
	}
	if out, err := run(""); err != nil || out != "done" || ran != 1 {
		t.Fatal(out, err, ran)
	}
	if strings.Join(started, ",") != "c-outside,c-members,c-" {
		t.Fatal(started)
	}
}

func TestCollectResolvesDataScope(t *testing.T) {
	ctx := context.Background()
	scoped := func(id string, agentID *string) connectors.RunningEntry {
		e := entry(id, "tools", id, "own", nil)
		e.Row.AgentID = agentID
		e.Handle.Tools = func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
			fn := conntest.FnTool("x", nil)
			return conntest.Tools("plain", connectors.PaluiTool{Class: "read", Tool: fn}, "chat", connectors.PaluiTool{Class: "read", Data: connectors.DataChat, Tool: fn}, "none", connectors.PaluiTool{Class: "read", Data: connectors.DataNone, Tool: fn}, "shared", connectors.PaluiTool{Class: "read", Data: connectors.DataShared, Tool: fn}), nil
		}
		return e
	}
	agent := "agt_sales"
	o := opts(64, nil)
	o.Turn.AgentID = agent
	c := tools.Collect(ctx, []connectors.RunningEntry{scoped("glob", nil), scoped("own", &agent)}, o)
	want := map[string]string{
		"glob__plain": connectors.DataShared, "glob__chat": connectors.DataChat, "glob__none": connectors.DataNone, "glob__shared": connectors.DataShared,
		"own__plain": connectors.DataAgent, "own__chat": connectors.DataChat, "own__none": connectors.DataNone, "own__shared": connectors.DataShared,
	}
	for n, scope := range want {
		m := c.Meta[n]
		if m == nil || m.Scope != scope {
			t.Fatal(n, m)
		}
	}
	// The declared value stays as it was for the catalog.
	if c.Meta["own__plain"].Data != "" || c.Meta["glob__chat"].Data != connectors.DataChat {
		t.Fatal(c.Meta["own__plain"], c.Meta["glob__chat"])
	}
}

// An MCP server the owner marked as holding no private data reaches outside and
// members turns with its non-destructive tools; its destructive ones never do.
func TestCollectMarkedMCPReachesSharedTurns(t *testing.T) {
	ctx := context.Background()
	e := entry("mcp-prayer", "tools", "Prayer times", "external", []def{{name: "times", class: "read"}, {name: "wipe", class: "destructive"}})
	e.Row.Type = "mcp"
	e.Row.Config = jsonx.NewMap[any]()
	e.Row.Config.Set(connectors.NoPrivateDataKey, true)
	for _, aud := range []string{tools.AudienceOutside, tools.AudienceMembers} {
		o := opts(64, nil)
		o.Audience = aud
		c := tools.Collect(ctx, []connectors.RunningEntry{e}, o)
		if m := c.Meta["mcp-prayer__times"]; m == nil || m.Scope != connectors.DataNone {
			t.Fatal(aud, m)
		}
		if c.Meta["mcp-prayer__wipe"] != nil {
			t.Fatal(aud, "destructive tool offered")
		}
	}
	// Unmarked, the same global server stays with the owner.
	e.Row.Config = jsonx.NewMap[any]()
	o := opts(64, nil)
	o.Audience = tools.AudienceOutside
	if c := tools.Collect(ctx, []connectors.RunningEntry{e}, o); c.Meta["mcp-prayer__times"] != nil {
		t.Fatal("unmarked server reached an outside turn")
	}
}

func TestCollectWrapsUntrustedAndTimes(t *testing.T) {
	ctx := context.Background()
	var tainted, started []string
	o := opts(64, nil)
	o.OnUntrustedResult = func(m *tools.ToolMeta) { tainted = append(tainted, m.Name) }
	o.OnToolStart = func(id string) { started = append(started, id) }
	c := tools.Collect(ctx, []connectors.RunningEntry{entry("web", "tools", "Web", "own", []def{{name: "fetch", class: "read", untrusted: true}, {name: "local", class: "read"}})}, o)
	for _, n := range []string{"web__fetch", "web__local"} {
		tl, _ := c.Tools.Get(n)
		if _, err := tl.Execute(ctx, json.RawMessage(`{}`), connectors.ToolCallInfo{ToolCallID: "c-" + n}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(tainted, ",") != "web__fetch" || strings.Join(started, ",") != "c-web__fetch,c-web__local" {
		t.Fatal(tainted, started)
	}
}

func TestSearch(t *testing.T) {
	doc := func(cid, label, tool, desc string) tools.SearchDoc {
		return tools.SearchDoc{Name: cid + "__" + tool, ConnectorID: cid, ConnectorLabel: label, Tool: tool, Description: desc}
	}
	docs := []tools.SearchDoc{
		doc("reminders", "Reminders", "set_reminder", "Buat pengingat pada waktu tertentu"),
		doc("mcp-jadwal-sholat", "jadwal-sholat", "search_cities", "Search Indonesian cities by name"),
		doc("mcp-jadwal-sholat", "jadwal-sholat", "get_prayer_times", "Get the prayer times for a city and a date"),
		doc("mcp-host", "host", "disk_free", "Free disk space on the host"),
	}
	if strings.Join(tools.Tokens("get_prayer_times"), ",") != "get,prayer,times" || strings.Join(tools.Tokens("Jadwal-Sholat"), ",") != "jadwal,sholat" {
		t.Fatal("tokens")
	}
	hits := func(q string, limit int) []string {
		var out []string
		for _, h := range tools.SearchTools(docs, q, limit) {
			out = append(out, h.Doc.Tool)
		}
		return out
	}
	if strings.Join(hits("jadwal sholat", 5), ",") != "search_cities,get_prayer_times" || hits("prayer time", 5)[0] != "get_prayer_times" || hits("disk", 5)[0] != "disk_free" {
		t.Fatal(hits("jadwal sholat", 5))
	}
	if len(hits("", 5)) != 0 || len(hits("zzz", 5)) != 0 || len(hits("jadwal", 1)) != 1 {
		t.Fatal("empty")
	}
}

type toolsGolden struct {
	Names []struct {
		ID, Tool, Name string
	} `json:"names"`
	Collections []struct {
		MaxTools     int      `json:"maxTools"`
		Sticky       []string `json:"sticky"`
		Tools        []string `json:"tools"`
		Active       []string `json:"active"`
		Deferred     []string `json:"deferred"`
		ConnectorIDs []string `json:"connectorIds"`
		Meta         []struct {
			Name, ConnectorID, Tool, Class string
			Untrusted                      bool
			Title, Label                   *string
		} `json:"meta"`
	} `json:"collections"`
	Search struct {
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
		Results     []struct {
			Input  json.RawMessage `json:"input"`
			Output json.RawMessage `json:"output"`
			Active []string        `json:"active"`
			OnLoad [][]string      `json:"onLoad"`
		} `json:"results"`
	} `json:"search"`
	Tokens []struct {
		Input  string   `json:"input"`
		Tokens []string `json:"tokens"`
	} `json:"tokens"`
}

func goldenRunning() []connectors.RunningEntry {
	obj := `{"type":"object","properties":{"q":{"type":"string"}}}`
	return []connectors.RunningEntry{
		entry("notes", "tools", "Notes", "own", []def{{name: "add", description: "Add a note", class: "write", schema: obj}, {name: "list", description: "List notes", class: "read", title: "List notes", schema: obj}, {name: "remove", description: "Remove a note", class: "destructive", schema: obj}}),
		entry("recall", "memory", "Recall", "own", []def{{name: "search", description: "Search memory", class: "read", schema: obj}}),
		entry("skills", "skill", "Skills", "own", []def{{name: "read", description: "Read a skill", class: "read", schema: obj}}),
		entry("mcp-jadwal-sholat", "tools", "jadwal-sholat", "external", []def{{name: "search_cities", description: "Search Indonesian cities by name", class: "read", schema: obj}, {name: "get_prayer_times", description: "Get the prayer times for a city and a date", class: "read", schema: `{"type":"object","properties":{"city":{"type":"string"},"date":{"type":"string"}},"required":["city"]}`}}),
		entry("web", "tools", "Web", "own", []def{{name: "fetch", description: "Fetch a page", class: "read", untrusted: true, schema: obj}, {name: "search", description: "Search the web", class: "bogus", schema: obj}}),
	}
}

func canon(t *testing.T, raw []byte) string {
	t.Helper()
	v, err := jsonx.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return jsonx.Canonical(v)
}

func TestGoldenTools(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/tools.json")
	testutil.Must(t, err)
	var g toolsGolden
	testutil.Must(t, json.Unmarshal(raw, &g))
	for _, n := range g.Names {
		if got := tools.Namespaced(n.ID, n.Tool); got != n.Name {
			t.Errorf("Namespaced(%q, %q) = %q, want %q", n.ID, n.Tool, got, n.Name)
		}
	}
	for _, tc := range g.Tokens {
		if got := tools.Tokens(tc.Input); strings.Join(got, "|") != strings.Join(tc.Tokens, "|") {
			t.Errorf("Tokens(%q) = %q, want %q", tc.Input, got, tc.Tokens)
		}
	}
	ctx := context.Background()
	for _, c := range g.Collections {
		got := tools.Collect(ctx, goldenRunning(), opts(c.MaxTools, c.Sticky))
		if strings.Join(got.Names(), ",") != strings.Join(c.Tools, ",") || strings.Join(got.Active(), ",") != strings.Join(c.Active, ",") || strings.Join(got.Deferred, ",") != strings.Join(c.Deferred, ",") || strings.Join(got.ConnectorIDs, ",") != strings.Join(c.ConnectorIDs, ",") {
			t.Errorf("max %d sticky %v:\n got %v / %v / %v\nwant %v / %v / %v", c.MaxTools, c.Sticky, got.Names(), got.Active(), got.Deferred, c.Tools, c.Active, c.Deferred)
		}
		for _, m := range c.Meta {
			gm := got.Meta[m.Name]
			title := ""
			if m.Title != nil {
				title = *m.Title
			}
			label := ""
			if m.Label != nil {
				label = *m.Label
			}
			if gm == nil || gm.ConnectorID != m.ConnectorID || gm.Tool != m.Tool || gm.Class != m.Class || gm.Untrusted != m.Untrusted || gm.Title != title || gm.Label != label {
				t.Errorf("meta %s: %+v, want %+v", m.Name, gm, m)
			}
		}
	}
	if tools.SearchToolDescription != g.Search.Description || canon(t, []byte(tools.SearchToolSchema)) != canon(t, g.Search.Schema) {
		t.Fatal("search tool definition differs")
	}
	for _, r := range g.Search.Results {
		var loads [][]string
		o := opts(3, nil)
		o.OnLoad = func(n []string) { loads = append(loads, n) }
		c := tools.Collect(ctx, goldenRunning(), o)
		st, _ := c.Tools.Get(tools.SearchTool)
		out, err := st.Execute(ctx, r.Input, connectors.ToolCallInfo{ToolCallID: "t"})
		testutil.Must(t, err)
		b, _ := jsonx.Marshal(out)
		if canon(t, b) != canon(t, r.Output) {
			t.Errorf("search %s:\n got %s\nwant %s", r.Input, b, r.Output)
		}
		if strings.Join(c.Active(), ",") != strings.Join(r.Active, ",") || len(loads) != len(r.OnLoad) {
			t.Errorf("search %s: active %v, want %v; loads %v want %v", r.Input, c.Active(), r.Active, loads, r.OnLoad)
		}
	}
}

func catalogHarness(t *testing.T, registry map[string]*connectors.Runtime, rows map[string]string) (*db.DB, *connectors.Supervisor) {
	d := testutil.DB(t)
	for id, kind := range rows {
		testutil.SeedConnector(t, d, id, kind, testutil.SeedOptions{}, 1)
	}
	sup := connectors.NewSupervisor(connectors.SupervisorOptions{DB: d, Registry: registry, MasterKey: testutil.TestKey, Clock: func() int64 { return testutil.NOW }})
	sup.Reconcile()
	t.Cleanup(sup.Stop)
	return d, sup
}

func TestCatalog(t *testing.T) {
	ctx := context.Background()
	fn := func(desc string) connectors.Tool {
		return conntest.FnTool(desc, func(*jsonx.Object) (any, error) { return "ok", nil })
	}
	_, sup := catalogHarness(t, map[string]*connectors.Runtime{
		"web": conntest.ToolsRuntime("web", func() *connectors.ToolSet {
			return conntest.Tools("fetch", connectors.PaluiTool{Tool: fn("fetch"), Class: "read", Title: "Fetch a page", Untrusted: true, GuestSafe: true, Data: connectors.DataNone})
		}, "", nil),
		"rem": conntest.ToolsRuntime("rem", func() *connectors.ToolSet {
			return conntest.Tools("set", connectors.PaluiTool{Tool: fn("set"), Class: "write", Data: connectors.DataChat}, "wipe", connectors.PaluiTool{Tool: fn("wipe"), Class: "destructive", Data: connectors.DataChat})
		}, "", nil),
		"alpha": conntest.ToolsRuntime("alpha", func() *connectors.ToolSet {
			return conntest.Tools("wipe", connectors.PaluiTool{Tool: fn("wipe"), Class: "destructive"}, "note", connectors.PaluiTool{Tool: fn("note"), Class: "write"})
		}, "", nil),
	}, map[string]string{"web": "tools", "alpha": "tools", "rem": "tools"})
	list := tools.Catalog(ctx, sup.Running(), tools.CatalogOptions{AgentID: "agt_x", ChatID: "chat_x", Channel: "web", TZ: "Asia/Makassar", Now: testutil.NOW})
	by := map[string]tools.CatalogTool{}
	var names []string
	for _, c := range list {
		by[c.Name] = c
		names = append(names, c.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "alpha__note,alpha__wipe,rem__set,rem__wipe,web__fetch" {
		t.Fatal(names)
	}
	if f := by["web__fetch"]; f.Tool != "fetch" || f.ConnectorID != "web" || f.Class != "read" || *f.Title != "Fetch a page" || !f.Untrusted || !f.GuestSafe || f.ConnectorType != "web" {
		t.Fatalf("%+v", f)
	}
	if w := by["alpha__wipe"]; w.Class != "destructive" || w.GuestSafe || w.Title != nil {
		t.Fatalf("%+v", w)
	}
	if n := by["alpha__note"]; n.Class != "write" || n.GuestSafe {
		t.Fatalf("%+v", n)
	}
	// What an outside turn gets: a chat-scope write yes, a destructive one no.
	if r := by["rem__set"]; !r.GuestSafe || r.Data != connectors.DataChat {
		t.Fatalf("%+v", r)
	}
	if r := by["rem__wipe"]; r.GuestSafe {
		t.Fatalf("%+v", r)
	}
}

func TestCatalogAgentScopedConnector(t *testing.T) {
	ctx := context.Background()
	fn := conntest.FnTool("x", func(*jsonx.Object) (any, error) { return "[]", nil })
	d, sup := catalogHarness(t, map[string]*connectors.Runtime{
		"web": conntest.ToolsRuntime("web", func() *connectors.ToolSet {
			return conntest.Tools("fetch", connectors.PaluiTool{Tool: fn, Class: "read", Data: connectors.DataNone})
		}, "", nil),
		"crm": conntest.ToolsRuntime("crm", func() *connectors.ToolSet {
			return conntest.Tools("deals", connectors.PaluiTool{Tool: fn, Class: "read"}, "notes", connectors.PaluiTool{Tool: fn, Class: "read", Data: connectors.DataChat})
		}, "", nil),
	}, map[string]string{"web": "tools", "crm": "tools"})
	sales := "agt_sales"
	_, _, err := connectors.SetOwner(d, "crm", &sales, 2)
	testutil.Must(t, err)
	names := func(agent string) string {
		var out []string
		for _, c := range tools.Catalog(ctx, sup.Running(), tools.CatalogOptions{AgentID: agent, ChatID: "chat_x", Channel: "web", TZ: "Asia/Makassar", Now: testutil.NOW}) {
			out = append(out, c.Name+":"+c.Data)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	// The catalog sends the declared scope; "" follows the connector's owner.
	if names("agt_sales") != "crm__deals:,crm__notes:chat,web__fetch:none" || names("agt_other") != "web__fetch:none" {
		t.Fatal(names("agt_sales"), names("agt_other"))
	}
	// An agent-scoped read tool is for members, not for outsiders.
	for _, c := range tools.Catalog(ctx, sup.Running(), tools.CatalogOptions{AgentID: "agt_sales", ChatID: "chat_x", Channel: "web", TZ: "Asia/Makassar", Now: testutil.NOW}) {
		want := map[string][2]bool{"crm__deals": {false, true}, "crm__notes": {true, true}, "web__fetch": {true, true}}[c.Name]
		if c.GuestSafe != want[0] || c.MemberSafe != want[1] {
			t.Fatalf("%+v", c)
		}
	}
	_, _, err = connectors.SetOwner(d, "crm", nil, 3)
	testutil.Must(t, err)
	if names("agt_other") != "crm__deals:,crm__notes:chat,web__fetch:none" {
		t.Fatal(names("agt_other"))
	}
}

func TestConnectorToolCaller(t *testing.T) {
	ctx := context.Background()
	readTool := connectors.PaluiTool{Class: "read", Tool: conntest.FnTool("r", func(in *jsonx.Object) (any, error) { v, _ := in.Get("q"); return "got " + v.(string), nil })}
	writeTool := connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("w", func(*jsonx.Object) (any, error) { return "x", nil })}
	slow := connectors.PaluiTool{Class: "read", Tool: connectors.Tool{Execute: func(ctx context.Context, _ json.RawMessage, _ connectors.ToolCallInfo) (any, error) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		return "late", nil
	}}}
	var seenTurn connectors.TurnInfo
	viaTools := connectors.RunningEntry{ID: "src", Row: db.Connector{ID: "src"}, Handle: &connectors.RunningConnector{Tools: func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
		seenTurn = turn
		return conntest.Tools("read", readTool, "write", writeTool, "slow", slow), nil
	}}}
	viaCall := connectors.RunningEntry{ID: "mcp", Row: db.Connector{ID: "mcp"}, Handle: &connectors.RunningConnector{CallTool: func(_ context.Context, name string, args *jsonx.Object) (connectors.ToolCallResult, error) {
		if name == "drop" {
			return connectors.ToolCallResult{Class: "", Output: "dropped"}, nil
		}
		return connectors.ToolCallResult{Class: "read", Output: map[string]any{"name": name}}, nil
	}}}
	bare := connectors.RunningEntry{ID: "bare", Row: db.Connector{ID: "bare"}, Handle: &connectors.RunningConnector{}}
	call := tools.ConnectorToolCaller(func() []connectors.RunningEntry { return []connectors.RunningEntry{viaTools, viaCall, bare} }, tools.CallerOptions{TZ: func() string { return "Asia/Makassar" }, TimeoutMs: 50, Clock: func() int64 { return 7 }})
	args := jsonx.NewMap[any]()
	args.Set("q", "x")
	if out, err := call(ctx, "src", "read", args, &tools.CallScope{AgentID: "agt_a"}); err != nil || out != "got x" {
		t.Fatal(out, err)
	}
	if seenTurn.RunID != "source_7" || seenTurn.Trigger != "schedule" || seenTurn.TZ != "Asia/Makassar" || seenTurn.AgentID != "agt_a" {
		t.Fatalf("%+v", seenTurn)
	}
	errText := func(_ any, err error) string {
		var te *tools.ToolCallError
		if !errors.As(err, &te) {
			return "not a ToolCallError: " + err.Error()
		}
		return te.Msg
	}
	for _, c := range []struct{ got, want string }{
		{errText(call(ctx, "src", "write", nil, nil)), "src.write is classified write; only read tools can feed a flow source. Mark it read on the Connectors page if it only reads."},
		{errText(call(ctx, "src", "nope", nil, nil)), "src.nope doesn't exist or is turned off."},
		{errText(call(ctx, "src", "slow", nil, nil)), "src.slow took longer than 0 s."},
		{errText(call(ctx, "mcp", "drop", nil, nil)), "mcp.drop is classified destructive; only read tools can feed a flow source. Mark it read on the Connectors page if it only reads."},
		{errText(call(ctx, "off", "x", nil, nil)), "Connector off isn't running. Turn it on in Connectors."},
		{errText(call(ctx, "bare", "x", nil, nil)), "Connector bare has no tools."},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
	if out, err := call(ctx, "mcp", "deals", nil, nil); err != nil || jsonx.Stringify(out) != `{"name":"deals"}` {
		t.Fatal(out, err)
	}
}

func TestSearchToolZodGivesSearchToolSchema(t *testing.T) {
	if got := string(zod.JSONSchemaBytes(tools.SearchToolZod)); got != tools.SearchToolSchema {
		t.Fatalf("\n got  %s\n want %s", got, tools.SearchToolSchema)
	}
}
