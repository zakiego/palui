package flows_test

// entry.test.ts

import (
	"sort"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

const BERES = `{
  "name": "Beres",
  "triggers": [
    { "id": "cmd", "kind": "chat", "command": "/beres", "start": "ok" },
    { "id": "say", "kind": "chat", "phrases": ["udah bayar listrik"], "start": "ok" }
  ],
  "steps": [{ "id": "ok", "kind": "message", "text": "Dicatat: {{trigger.args | default(\"-\")}}" }]
}`

func TestChatTriggers(t *testing.T) {
	t.Run("matches the owner's command or phrase before the model and never reaches a turn", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(BERES).Flow
		c, _, err := chats.Get(h.db, h.chatID)
		tu.Must(t, err)
		route := func(text, msgID string, owner bool) chat.Route {
			r, err := chat.RouteMessage(chat.RouteInput{DB: h.db, Chat: c, Content: chat.StoredContent{Text: text}, Text: text, MessageID: msgID, FlowActions: flows.Actions{}, FromOwner: owner, Now: h.now()})
			tu.Must(t, err)
			return r
		}
		eq(t, route("/beres listrik november", "msg_1", true).Kind, "handled", "command")
		eq(t, route("Udah bayar listrik!", "msg_2", true).Kind, "handled", "phrase")
		// A guest saying it, or anything else, is just a message.
		eq(t, route("/beres", "msg_3", false).Kind, "turn", "guest")
		eq(t, route("beres?", "msg_4", true).Kind, "turn", "other")
		// Built-in commands win (they're refused at save anyway).
		eq(t, route("/status", "msg_5", true).Kind, "command", "builtin")
		h.run(h.now())
		var keys []string
		for _, r := range runsOf(t, h.db, flow.ID) {
			keys = append(keys, r.RunKey)
		}
		sort.Strings(keys)
		eq(t, keys, []string{"chat:msg_1", "chat:msg_2"}, "run keys")
		got := texts(t, h.db)
		sort.Strings(got)
		eq(t, got, []string{"Dicatat: -", "Dicatat: listrik november"}, "texts")
	})
	t.Run("lists a chat's flow commands for /help, phrases and other chats left out", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.create(BERES)
		got, err := flows.FlowCommands(h.db, h.chatID)
		tu.Must(t, err)
		eq(t, got, []chat.FlowCommand{{Command: "/beres", Flow: "Beres"}}, "commands")
		got, err = flows.FlowCommands(h.db, h.otherChatID)
		tu.Must(t, err)
		eq(t, got, []chat.FlowCommand{}, "other chat")
	})
	t.Run("only matches in the flow's own chat", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.create(BERES)
		took, err := flows.Actions{}.FromChat(h.db, chat.ChatTrigger{Text: "/beres", ChatID: h.otherChatID, MessageID: "m", FromOwner: true}, h.now())
		tu.Must(t, err)
		if took {
			t.Error("took it")
		}
	})
}

func TestWebhookTriggers(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	created, err := hooks.Create(h.db, hooks.CreateInput{Name: "Kosong", Mode: "flow", ChatID: h.chatID}, tu.TestKey, h.now())
	tu.Must(t, err)
	row, ok, err := db.One(h.db.QueryRow("SELECT "+db.HookCols+" FROM hooks"), db.ScanHook)
	tu.Must(t, err)
	if !ok {
		t.Fatal("no hook")
	}
	res, err := flows.IngestFlowHook(h.db, row, "{}", map[string]string{}, h.now())
	tu.Must(t, err)
	eq(t, res, flows.IngestResult{}, "ingest")
	eq(t, created.Hook.Mode, "flow", "mode")
	eq(t, flows.DeliveryKey("a", map[string]string{"x-palui-delivery": "abc"}), "hook:abc", "delivery id")
	eq(t, flows.DeliveryKey("a", map[string]string{}), flows.DeliveryKey("a", map[string]string{"x-palui-delivery": "bad id with spaces"}), "hash")
}
