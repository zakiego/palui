package flows_test

// call.test.ts, and catalog.test.ts's third case (moved here from M2).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

var callTurn = connectors.TurnInfo{RunID: "flr_1", ChatID: "chat_owner", Channel: "telegram", Trigger: "flow", TZ: WITA, Now: 0, FlowID: "flw_1"}

// textOnly is z.object({ text: z.string() }) as a validator.
func textOnly(input json.RawMessage) error {
	o, err := jsonx.ParseObject(input)
	if err != nil {
		return err
	}
	v, ok := o.Get("text")
	if _, isStr := v.(string); !ok || !isStr {
		received := "undefined"
		if ok {
			received = fmt.Sprintf("%T", v)
		}
		return fmt.Errorf(`[ { "expected": "string", "code": "invalid_type", "path": [ "text" ], "message": "Invalid input: expected string, received %s" } ]`, received)
	}
	return nil
}

func TestConnectorFlowCaller(t *testing.T) {
	log := &callLog{}
	entries := fakeEntries([]*fakeConnector{
		{id: "reminders", names: []string{"list_reminders", "set_reminder", "cancel_reminder"}, tools: map[string]*fakeTool{
			"list_reminders": {class: "read", run: func(_ *jsonx.Object, turn connectors.TurnInfo) (any, error) { return obj("chat", turn.ChatID), nil }},
			"set_reminder": {class: "write", validate: textOnly, run: func(a *jsonx.Object, _ connectors.TurnInfo) (any, error) {
				v, _ := a.Get("text")
				return obj("ok", true, "text", v), nil
			}},
			"cancel_reminder": {class: "destructive", run: okRun},
		}},
		{id: "mcp-x", trust: "external", names: []string{"get"}, tools: map[string]*fakeTool{"get": {class: "read", run: returns(`{"content":[{"type":"text","text":"{\"a\":1}"}]}`)}}},
	}, log)
	caller := flows.NewConnectorFlowCaller(func() []connectors.RunningEntry { return entries })
	ctx := context.Background()
	resolve := func(tool, cid string) *flows.ToolInfo {
		info, err := caller.Resolve(ctx, tool, cid, callTurn)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}

	t.Run("resolves the class and trust before any call", func(t *testing.T) {
		info := resolve("reminders__cancel_reminder", "")
		if info.Class != "destructive" || info.ConnectorID != "reminders" || info.Tool != "cancel_reminder" {
			t.Errorf("info %+v", info)
		}
		if len(log.all()) != 0 {
			t.Error("a call happened")
		}
		x := resolve("get", "mcp-x")
		if !x.Untrusted || x.ConnectorType != "mcp-x" {
			t.Errorf("mcp-x %+v", x)
		}
		if resolve("nope__x", "") != nil {
			t.Error("nope resolved")
		}
	})
	t.Run("refuses a destructive tool without running it", func(t *testing.T) {
		info := resolve("reminders__cancel_reminder", "")
		_, err := caller.Call(ctx, *info, jsonx.NewMap[any](), callTurn, 0)
		if err == nil || !strings.Contains(err.Error(), "destructive") {
			t.Errorf("err %v", err)
		}
		if len(log.of("cancel_reminder")) != 0 {
			t.Error("ran it")
		}
	})
	t.Run("refuses when the class changed since it was resolved", func(t *testing.T) {
		info := *resolve("reminders__set_reminder", "")
		info.Class = "read"
		_, err := caller.Call(ctx, info, jobj(`{"text":"x"}`), callTurn, 0)
		if err == nil || !strings.Contains(err.Error(), "now classified write") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("passes the flow's chat and id in the tool context", func(t *testing.T) {
		info := resolve("reminders__list_reminders", "")
		out, err := caller.Call(ctx, *info, jsonx.NewMap[any](), callTurn, 0)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, out, js(`{"chat":"chat_owner"}`), "output")
		all := log.all()
		last := all[len(all)-1].Turn
		if last.ChatID != "chat_owner" || last.FlowID != "flw_1" || last.Trigger != "flow" {
			t.Errorf("turn %+v", last)
		}
	})
	t.Run("validates args against the input schema", func(t *testing.T) {
		info := resolve("reminders__set_reminder", "")
		_, err := caller.Call(ctx, *info, jobj(`{"text":5}`), callTurn, 0)
		if err == nil || !strings.Contains(err.Error(), "don't match the tool's input") {
			t.Errorf("err %v", err)
		}
		out, err := caller.Call(ctx, *info, jobj(`{"text":"beli obat"}`), callTurn, 0)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, out, js(`{"ok":true,"text":"beli obat"}`), "output")
	})
	t.Run("stepData reads MCP results and keeps plain text", func(t *testing.T) {
		v, _ := flows.StepData(js(`{"content":[{"type":"text","text":"{\"a\":1}"}]}`))
		eq(t, v, js(`{"a":1}`), "JSON text")
		v, _ = flows.StepData(js(`{"content":[{"type":"text","text":"hello"}]}`))
		eq(t, v, "hello", "plain text")
		v, _ = flows.StepData(js(`{"structuredContent":{"b":2},"content":[]}`))
		eq(t, v, js(`{"b":2}`), "structured")
		_, err := flows.StepData(js(`{"isError":true,"content":[{"type":"text","text":"rate limited"}]}`))
		if err == nil || !strings.Contains(err.Error(), "rate limited") {
			t.Errorf("err %v", err)
		}
	})
}

// catalog.test.ts: keeps flow tool steps to the tools the flow's agent may use.
func TestFlowCallerKeepsAgentTools(t *testing.T) {
	owner := "agt_sales"
	entries := fakeEntries([]*fakeConnector{{id: "crm", agentID: &owner, names: []string{"deals"}, tools: map[string]*fakeTool{"deals": {class: "read", run: returns(`[]`)}}}}, &callLog{})
	caller := flows.NewConnectorFlowCaller(func() []connectors.RunningEntry { return entries })
	turn := func(agent string) connectors.TurnInfo {
		return connectors.TurnInfo{RunID: "r", ChatID: "c", Trigger: "flow", TZ: "UTC", AgentID: agent}
	}
	list, _ := caller.List(context.Background(), turn("agt_sales"))
	if len(list) != 1 || list[0].Name != "crm__deals" {
		t.Errorf("sales: %v", list)
	}
	list, _ = caller.List(context.Background(), turn("agt_other"))
	if len(list) != 0 {
		t.Errorf("other: %v", list)
	}
	if info, _ := caller.Resolve(context.Background(), "crm__deals", "", turn("agt_other")); info != nil {
		t.Errorf("other resolved %v", info)
	}
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}
