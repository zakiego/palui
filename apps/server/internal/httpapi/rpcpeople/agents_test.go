package rpcpeople_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/httpapi/rpcpeople"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// apps/app/src/server/agents.test.ts (agentMemory, agentTools), through the
// getAgentMemoryFn and getAgentToolsFn endpoints.

var agentsNow = time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC).UnixMilli()

const dayMs = 24 * 60 * 60 * 1000

func agentsEnv(t *testing.T) (*apitest.Env, string) {
	e, token := memoryEnv(t)
	e.Now.Store(agentsNow)
	return e, token
}

func withRecall(t *testing.T, e *apitest.Env, container string) {
	cfg := jsonx.NewMap[any]()
	cfg.Set("container", container)
	secrets := jsonx.NewMap[string]()
	secrets.Set("apiKey", "rcl_test")
	testutil.SeedConnector(t, e.DB, "recall", "memory", testutil.SeedOptions{Config: cfg, Secrets: secrets}, agentsNow)
}

func createAgent(t *testing.T, q db.Q, in agents.Input) db.Agent {
	t.Helper()
	a, err := agents.Create(q, in, agentsNow)
	testutil.Must(t, err)
	return a
}

func plainAgent(slug, name string) agents.Input {
	return agents.Input{Slug: slug, Name: testutil.Ptr(name), Persona: testutil.Ptr("p")}
}

func containersSpec(t *testing.T, list string) *jsonx.Object {
	o, err := jsonx.ParseObject([]byte(`{"action":{"containers":` + list + `}}`))
	testutil.Must(t, err)
	return o
}

func TestAgentMemorySaysThereIsNoMemoryConnector(t *testing.T) {
	e, token := agentsEnv(t)
	chef := createAgent(t, e.DB, plainAgent("chef", "Chef"))
	useFake(t, nil)
	_, body := call(t, e, token, "getAgentMemoryFn", `{"id":"`+chef.ID+`"}`)
	if body != `{"container":"agent-chef","perChat":false,"reads":[],"chatContainers":0,"connector":null,"stats":null,"error":null}` {
		t.Fatal(body)
	}
}

func TestAgentMemoryAddsUpEveryChatsContainerWhenPerChat(t *testing.T) {
	e, token := agentsEnv(t)
	withRecall(t, e, "main")
	in := plainAgent("shop", "Shop")
	in.RecallPerChat = testutil.Ptr(true)
	in.RecallReadContainers = &[]string{"kb-shop"}
	shop := createAgent(t, e.DB, in)
	a := agents.ChatContainer("agent-shop", "cht_a")
	b := agents.ChatContainer("agent-shop", "cht_b")
	n := func(v int64) string { return jsonx.NumberString(float64(v)) }
	list := `[{"container":"` + a + `","memories":3,"documents":1,"lastActivity":` + n(agentsNow-dayMs) + `},` +
		`{"container":"` + b + `","memories":4,"documents":2,"lastActivity":` + n(agentsNow) + `},` +
		`{"container":"agent-shopping","memories":50,"documents":5,"lastActivity":` + n(agentsNow) + `},` +
		`{"container":"kb-shop","memories":9,"documents":1,"lastActivity":` + n(agentsNow) + `}]`
	fake := useFake(t, containersSpec(t, list))
	r := mustOK(t, e, token, "getAgentMemoryFn", `{"id":"`+shop.ID+`"}`)
	if at(r, "container") != "agent-shop" || at(r, "perChat") != true || js(at(r, "reads")) != `["kb-shop"]` || at(r, "chatContainers") != 2.0 {
		t.Fatal(js(r))
	}
	if js(at(r, "stats")) != `{"container":"agent-shop","memories":7,"documents":3,"lastActivity":`+n(agentsNow)+`}` {
		t.Fatal(js(r))
	}
	if js(fake.calls) != `[["action","recall","listContainers"]]` {
		t.Fatal(js(fake.calls))
	}
}

func TestAgentMemoryReadsTheAgentsContainerAndPaluiUsesTheConnectors(t *testing.T) {
	e, token := agentsEnv(t)
	withRecall(t, e, "main")
	chef := createAgent(t, e.DB, plainAgent("chef", "Chef"))
	useFake(t, containersSpec(t, `[{"container":"agent-chef","memories":12,"documents":0,"lastActivity":`+jsonx.NumberString(float64(agentsNow))+`}]`))
	own := mustOK(t, e, token, "getAgentMemoryFn", `{"id":"`+chef.ID+`"}`)
	if at(own, "container") != "agent-chef" || at(own, "stats", "memories") != 12.0 || at(own, "stats", "lastActivity") != float64(agentsNow) || at(own, "error") != nil {
		t.Fatal(js(own))
	}
	if at(own, "connector", "id") != "recall" || at(own, "connector", "enabled") != true {
		t.Fatal(js(own))
	}
	// A container recall doesn't know yet is empty, not an error.
	palui, err := agents.GetDefault(e.DB, agentsNow)
	testutil.Must(t, err)
	def := mustOK(t, e, token, "getAgentMemoryFn", `{"id":"`+palui.ID+`"}`)
	if at(def, "container") != "main" || js(at(def, "stats")) != `{"container":"main","memories":0,"documents":0,"lastActivity":null}` {
		t.Fatal(js(def))
	}
}

func TestAgentMemoryReportsADownRuntimeWithoutFailing(t *testing.T) {
	e, token := agentsEnv(t)
	withRecall(t, e, "default")
	chef := createAgent(t, e.DB, plainAgent("chef", "Chef"))
	down, _ := jsonx.ParseObject([]byte(`{"down":true}`))
	useFake(t, down)
	r := mustOK(t, e, token, "getAgentMemoryFn", `{"id":"`+chef.ID+`"}`)
	if at(r, "stats") != nil || !strings.Contains(at(r, "error").(string), "isn't reachable") {
		t.Fatal(js(r))
	}
}

func TestAgentToolsCountsThisAgentsSuccessfulCallsFromTheLast7Days(t *testing.T) {
	e, token := agentsEnv(t)
	chef := createAgent(t, e.DB, plainAgent("chef", "Chef"))
	other := createAgent(t, e.DB, plainAgent("cs", "CS"))
	chat, err := chats.Ensure(e.DB, chats.EnsureInput{Channel: "web", ExternalID: "web"}, agentsNow)
	testutil.Must(t, err)
	run := func(id, agentID string) {
		_, err := e.DB.Exec("INSERT INTO runs (id, chat_id, agent_id, trigger, status, started_at) VALUES (?, ?, ?, 'message', 'ok', ?)", id, chat.ID, agentID, agentsNow)
		testutil.Must(t, err)
	}
	run("run_a", chef.ID)
	run("run_b", other.ID)
	toolCall := func(id, runID, status string, at int64) {
		_, err := e.DB.Exec("INSERT INTO tool_calls (id, run_id, connector_id, tool, class, status, created_at) VALUES (?, ?, 'web', 'web__fetch', 'read', ?, ?)", id, runID, status, at)
		testutil.Must(t, err)
	}
	toolCall("run_a:1", "run_a", "ok", agentsNow-dayMs)
	toolCall("run_a:2", "run_a", "ok", agentsNow-2*dayMs)
	toolCall("run_a:3", "run_a", "denied", agentsNow-dayMs)
	toolCall("run_a:4", "run_a", "ok", agentsNow-8*dayMs)
	toolCall("run_b:1", "run_b", "ok", agentsNow-dayMs)
	useFake(t, nil)
	_, body := call(t, e, token, "getAgentToolsFn", `{"id":"`+chef.ID+`"}`)
	if body != `{"tools":[],"error":null,"used":{"web__fetch":2},"scopes":{}}` {
		t.Fatal(body)
	}
}

func TestAgentToolsListsTheServersMarkedAsHoldingNoPrivateData(t *testing.T) {
	e, token := agentsEnv(t)
	chef := createAgent(t, e.DB, plainAgent("chef", "Chef"))
	cfg := jsonx.NewMap[any]()
	cfg.Set("url", "https://prayer.example.com/mcp")
	testutil.SeedConnector(t, e.DB, "mcp-prayer", "tools", testutil.SeedOptions{Type: "mcp", Config: cfg}, agentsNow)
	testutil.SeedConnector(t, e.DB, "mcp-notes", "tools", testutil.SeedOptions{Type: "mcp", Config: cfg.Clone()}, agentsNow)
	_, _, err := connectors.SetNoPrivateData(e.DB, "mcp-prayer", true, agentsNow)
	testutil.Must(t, err)
	useFake(t, nil)
	_, body := call(t, e, token, "getAgentToolsFn", `{"id":"`+chef.ID+`"}`)
	if body != `{"tools":[],"error":null,"used":{},"scopes":{"mcp-notes":null,"mcp-prayer":null},"noPrivateData":["mcp-prayer"]}` {
		t.Fatal(body)
	}
}

func TestAgentToolsStillShowsUsageWhenTheRuntimeIsDown(t *testing.T) {
	e, token := agentsEnv(t)
	chef := createAgent(t, e.DB, plainAgent("chef", "Chef"))
	down, _ := jsonx.ParseObject([]byte(`{"down":true}`))
	useFake(t, down)
	r := mustOK(t, e, token, "getAgentToolsFn", `{"id":"`+chef.ID+`"}`)
	if at(r, "tools") != nil || js(at(r, "used")) != `{}` || !strings.Contains(at(r, "error").(string), "isn't reachable") {
		t.Fatal(js(r))
	}
}

// apps/app/src/lib/format.test.ts clip, which the chat list's preview uses.
func TestClip(t *testing.T) {
	if got := rpcpeople.Clip("ok sip", 140); got != "ok sip" {
		t.Fatal(got)
	}
	text := "Wkwkwk " + strings.Repeat("\U0001F602", 80)
	cut := rpcpeople.Clip(text, 140)
	if strx.Len16(cut) > 140 || !strings.HasPrefix(text, cut) || strx.Len16(cut) != 139 {
		t.Fatalf("%d %q", strx.Len16(cut), cut)
	}
	if got := rpcpeople.Clip("ab\U0001F469\U0001F3FD‍\U0001F4BBcd", 4); got != "ab" {
		t.Fatal(got)
	}
}
