package rpcpeople_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// The handlers on the real in-process runtime (httpapi.Runtime on the chat
// test harness), and with no runtime at all: the wiring the golden's fake
// stands in for.

func realRuntimeEnv(t *testing.T) (*apitest.Env, string, *chattest.Harness) {
	h := chattest.New(t, chattest.Options{
		Registry: map[string]*connectors.Runtime{"web": web.New(chat.NewBus()), "reminders": reminders.Runtime},
		Rows:     []chattest.Row{{"web", "channel"}, {"reminders", "skill"}},
	})
	rt := &httpapi.Runtime{DB: h.DB, Engine: h.Engine, Sup: h.Sup, Bus: h.Bus, StartedAt: 7, Clock: h.Clock.Now}
	e := apitest.New(t, func(o *httpapi.Options) { o.DB = h.DB; o.Runtime = rt })
	e.Now.Store(h.Clock.Now())
	return e, e.SignIn(), h
}

func TestOnTheRealRuntime(t *testing.T) {
	e, token, h := realRuntimeEnv(t)
	if _, body := call(t, e, token, "stopTurn", `{"chatId":"chat_x"}`); body != `{"ok":false}` {
		t.Fatal(body)
	}
	if _, body := call(t, e, token, "decideApproval", `{"id":"apr_missing","approved":true}`); body != `{"ok":false}` {
		t.Fatal(body)
	}
	palui, err := agents.GetDefault(h.DB, h.Clock.Now())
	testutil.Must(t, err)
	tools := mustOK(t, e, token, "getAgentToolsFn", `{"id":"`+palui.ID+`"}`)
	if at(tools, "error") != nil || !strings.Contains(js(at(tools, "tools")), `"name":"reminders__set_reminder"`) {
		t.Fatal(js(tools))
	}
	// The runtime refuses a chat it doesn't know with its own message.
	if status, msg := errorOf(t, e, token, "setChatAgentFn", `{"chatId":"chat_missing","agentId":null}`); status != 404 || msg == "" {
		t.Fatal(status, msg)
	}
	sent := mustOK(t, e, token, "sendWebMessage", `{"text":" halo "}`)
	if at(sent, "ok") != true || !strings.HasPrefix(at(sent, "chatId").(string), "chat_") || at(sent, "messageId") == "" {
		t.Fatal(js(sent))
	}
	first, err := settings.Get[settings.FirstRun](h.DB, "firstRun")
	testutil.Must(t, err)
	if !first.SaidHi {
		t.Fatal("saidHi not set")
	}
	// A real switch through the runtime.
	other, err := agents.Create(h.DB, plainAgent("chef", "Chef"), h.Clock.Now())
	testutil.Must(t, err)
	if _, body := call(t, e, token, "setChatAgentFn", `{"chatId":"`+at(sent, "chatId").(string)+`","agentId":"`+other.ID+`"}`); body != `{"saved":true}` {
		t.Fatal(body)
	}
	row, _, err := chats.Get(h.DB, at(sent, "chatId").(string))
	testutil.Must(t, err)
	if row.AgentID == nil || *row.AgentID != other.ID {
		t.Fatalf("%+v", row)
	}
}

func TestWithTheRuntimeOff(t *testing.T) {
	e, token := memoryEnv(t)
	for name, input := range map[string]string{"sendWebMessage": `{"text":"halo"}`, "decideApproval": `{"id":"apr_1","approved":true}`, "stopTurn": `{"chatId":"chat_1"}`} {
		status, body := call(t, e, token, name, input)
		if status != 503 || !strings.Contains(body, `"error":"`+httpapi.RuntimeDown+`"`) || !strings.Contains(body, `"code":"RUNTIME_DOWN"`) {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	first, err := settings.Get[settings.FirstRun](e.DB, "firstRun")
	testutil.Must(t, err)
	if first.SaidHi {
		t.Fatal("saidHi set although nothing was sent")
	}
	palui, err := agents.GetDefault(e.DB, e.Now.Load())
	testutil.Must(t, err)
	tools := mustOK(t, e, token, "getAgentToolsFn", `{"id":"`+palui.ID+`"}`)
	if at(tools, "tools") != nil || at(tools, "error") != httpapi.RuntimeDown {
		t.Fatal(js(tools))
	}
	// The switch still happens, without the memory save, on a new session.
	chat, err := chats.Ensure(e.DB, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, e.Now.Load())
	testutil.Must(t, err)
	chef, err := agents.Create(e.DB, plainAgent("chef", "Chef"), e.Now.Load())
	testutil.Must(t, err)
	if _, body := call(t, e, token, "setChatAgentFn", `{"chatId":"`+chat.ID+`","agentId":"`+chef.ID+`"}`); body != `{"saved":false}` {
		t.Fatal(body)
	}
	row, _, err := chats.Get(e.DB, chat.ID)
	testutil.Must(t, err)
	if row.AgentID == nil || *row.AgentID != chef.ID || row.SessionID == nil || chat.SessionID == nil || *row.SessionID == *chat.SessionID {
		t.Fatalf("%+v", row)
	}
}
