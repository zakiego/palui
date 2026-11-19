package chat_test

// The HTTP API connector's tools in the real turn loop: its own approval rule,
// taint, agent permissions, unattended turns and shared chats.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/httpreq"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/safenet"
)

const httpKey = "sk_example_0123456789"

// httpNet is a fake network: every request answers 200 with a JSON body that
// echoes the key, and is counted with its headers.
type httpNet struct {
	mu   sync.Mutex
	seen []safenet.TransportRequest
}

func (n *httpNet) transport(req safenet.TransportRequest) (*safenet.TransportResponse, error) {
	n.mu.Lock()
	n.seen = append(n.seen, req)
	n.mu.Unlock()
	h := http.Header{}
	h.Set("content-type", "application/json")
	body := `{"plan":"free","echo":"` + httpKey + `"}`
	return &safenet.TransportResponse{Status: 200, Header: h, Body: strings.NewReader(body), Cancel: func() {}}, nil
}

func (n *httpNet) calls() []safenet.TransportRequest {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]safenet.TransportRequest(nil), n.seen...)
}

func httpRuntime(policy string, n *httpNet) *connectors.Runtime {
	cfg := httpreq.Config{Domains: []httpreq.Domain{{Host: "api.example.com"}}, Auth: "header", HeaderName: "xi-api-key", ValueTemplate: "{value}", Policy: policy}
	o := httpreq.Options{Transport: n.transport, Resolver: func(context.Context, string) ([]safenet.ResolvedAddress, error) {
		return []safenet.ResolvedAddress{{Address: "93.184.216.34", Family: 4}}, nil
	}}
	return toolsRT("http-example", func() *connectors.ToolSet { return httpreq.Tools(cfg, "Example", httpKey, o) })
}

var httpRows = []chattest.Row{{"http-example", "tools"}, {"alpha", "tools"}}

func httpWorld(t *testing.T, policy string, n *httpNet, notes *executedLog, steps ...models.Step) *world {
	t.Helper()
	return basic(t, steps, extra{registry: map[string]*connectors.Runtime{
		"http-example": httpRuntime(policy, n),
		"alpha": toolsRT("alpha", tools("note", connectors.PaluiTool{Class: "write", Tool: conntest.FnTool("note", func(in *jsonx.Object) (any, error) {
			notes.add(str(in, "text"))
			return "ok", nil
		})})),
	}, rows: httpRows})
}

func getQuota() models.Step {
	return models.CallTool("http-example__http_request", objOf("url", "https://api.example.com/v1/user/subscription"), "h1")
}

func postItem() models.Step {
	return models.CallTool("http-example__http_request_write", objOf("url", "https://api.example.com/v1/items", "method", "POST", "json", objOf("name", "a")), "w1")
}

func setToolPermission(t *testing.T, h *world, tool, value string) {
	t.Helper()
	perms := jsonx.NewMap[*string]()
	perms.Set(tool, sp(value))
	agentID := need(agents.GetDefault(h.DB, 1)).ID
	need(agents.SetToolPermissions(h.DB, agentID, agents.PermissionsPatch{Tools: perms}, 1))
}

func lastRunStatus(t *testing.T, h *world) string {
	runs := allRuns(t, h.DB)
	return runs[len(runs)-1].Status
}

func TestHTTPRequestTools(t *testing.T) {
	t.Run("ask: a read waits for the owner and the preview has no key", func(t *testing.T) {
		n := &httpNet{}
		h := httpWorld(t, "ask", n, &executedLog{}, getQuota(), models.Say("ok"))
		h.sendIdle("cek kuota")
		eq(t, lastRunStatus(t, h), "awaiting_approval")
		eq(t, len(n.calls()), 0)
		p := allApprovals(t, h.DB, "")[0]
		eq(t, p.Tool, "http-example__http_request")
		contains(t, *p.ArgsPreview, "GET https://api.example.com/v1/user/subscription")
		contains(t, *p.ArgsPreview, "Key: Example")
		notContains(t, *p.ArgsPreview, httpKey)
		prompt := strings.Join(outboxTexts(t, h.DB), "\n")
		contains(t, prompt, "Boleh aku jalanin Example: read? (cuma baca data, gak ngubah apa-apa)")
		notContains(t, prompt, "mengubah data")
	})

	t.Run("allow: a read runs with the key, then a plain write asks because the turn is tainted", func(t *testing.T) {
		n, notes := &httpNet{}, &executedLog{}
		h := httpWorld(t, "allow", n, notes, getQuota(), models.CallTool("alpha__note", objOf("text", "x"), "n1"))
		h.sendIdle("cek kuota lalu catat")
		calls := n.calls()
		eq(t, len(calls), 1)
		eq(t, calls[0].Headers["xi-api-key"], httpKey)
		eq(t, len(notes.get()), 0)
		eq(t, allApprovals(t, h.DB, "")[0].Tool, "alpha__note")
		eq(t, count(t, h.DB, "runs WHERE tainted = 1"), 1)
		// The stored call holds neither the key nor the echoed key.
		for _, c := range allToolCalls(t, h.DB) {
			if c.Tool != "http-example__http_request" {
				continue
			}
			notContains(t, string(c.Args), httpKey)
			if c.ResultPreview == nil {
				t.Fatal("no result preview")
			}
			notContains(t, *c.ResultPreview, httpKey)
			contains(t, *c.ResultPreview, "free")
		}
	})

	t.Run("allow: a write asks in a clean turn, and runs when the agent allows that tool", func(t *testing.T) {
		n := &httpNet{}
		h := httpWorld(t, "allow", n, &executedLog{}, postItem(), models.Say("ok"))
		h.sendIdle("tambah item")
		eq(t, lastRunStatus(t, h), "awaiting_approval")
		eq(t, len(n.calls()), 0)

		n = &httpNet{}
		h = httpWorld(t, "allow", n, &executedLog{}, postItem(), models.Say("ok"))
		setToolPermission(t, h, "http-example__http_request_write", "allow")
		h.sendIdle("tambah item")
		eq(t, count(t, h.DB, "pending_approvals"), 0)
		calls := n.calls()
		eq(t, len(calls), 1)
		eq(t, calls[0].Method, "POST")
		eq(t, *calls[0].Body, `{"name":"a"}`)
	})

	t.Run("ask: the tool's own rule wins over an agent's allow and allow-all", func(t *testing.T) {
		n := &httpNet{}
		h := httpWorld(t, "ask", n, &executedLog{}, getQuota(), models.Say("ok"))
		setToolPermission(t, h, "http-example__http_request", "allow")
		h.sendIdle("cek kuota")
		eq(t, lastRunStatus(t, h), "awaiting_approval")

		h = httpWorld(t, "ask", n, &executedLog{}, getQuota(), models.Say("ok"))
		allowAll(t, h.DB, "", nil)
		h.sendIdle("cek kuota")
		eq(t, lastRunStatus(t, h), "awaiting_approval")
		eq(t, len(n.calls()), 0)
	})

	t.Run("allow: a scheduled turn runs the read and asks for the write", func(t *testing.T) {
		n := &httpNet{}
		h := httpWorld(t, "allow", n, &executedLog{}, models.Say("hi"), getQuota(), postItem())
		h.sendIdle("init")
		c := firstChat(t, h.DB)
		r := need(h.Engine.RunTurn(chat.TurnOptions{ChatID: c.ID, Trigger: "schedule", Prompt: "cek kuota tiap pagi"}))
		eq(t, r.Status, "awaiting_approval")
		eq(t, len(n.calls()), 1)
		eq(t, n.calls()[0].Method, "GET")
		eq(t, allApprovals(t, h.DB, "")[0].Tool, "http-example__http_request_write")
	})

	t.Run("a members chat is never offered the tools, even when the connector serves only that agent", func(t *testing.T) {
		ran, n := &executedLog{}, &httpNet{}
		reg := memberRegistry(ran)
		reg["http-example"] = httpRuntime("allow", n)
		rows := append(append([]chattest.Row{}, memberRows...), chattest.Row{"http-example", "tools"})
		h := membersWorld(t, []models.Step{getQuota(), models.Say("gak bisa")}, reg, rows)
		need(connectorsSetOwner(h.DB, "http-example", h.chef.ID))
		h.sendIdle("cek kuota", memberDM)
		eq(t, len(n.calls()), 0)
		if len(h.m.Offered) == 0 {
			t.Fatal("the member's turn never reached the model")
		}
		for _, offered := range h.m.Offered {
			for _, name := range offered {
				if strings.HasPrefix(name, "http-example__") {
					t.Fatal(offered)
				}
			}
		}
	})
}
