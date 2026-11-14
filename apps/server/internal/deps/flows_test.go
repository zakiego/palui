package deps_test

// The flow boundaries M5 wired (deps.ts buildDeps): the flow engine as the
// first scheduler extension, flowActions in the router, the flow commands in
// /help, the open check-ins in a turn's system prompt, prompt steps as flow
// turns, and a decided flr_ approval waking its run.

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/deps"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

func flowWorld(t *testing.T, steps ...models.Step) *world {
	d := tu.DB(t)
	chattest.OwnTelegram(t, d)
	chattest.ModelChain(t, d, "cliproxy")
	w := &world{m: models.NewScripted(steps...), tg: conntest.NewFakeChannel("telegram")}
	clock := chattest.NewClock(chattest.Start)
	w.timer = tu.NewFakeClock(chattest.Start)
	w.b = deps.Build(deps.Options{DB: d, Log: logx.Silent, Clock: clock.Now, Timers: w.timer, FlowsSerial: true,
		Running: func() []connectors.RunningEntry { return w.h.Sup.Running() },
		RunTurn: func(o chat.TurnOptions) (chat.TurnResult, error) { return w.h.Engine.RunTurn(o) }})
	notes := conntest.Tools("save_note", connectors.PaluiTool{Class: "write", FlowApproval: "every_run", Tool: conntest.FnTool("save", func(*jsonx.Object) (any, error) { return map[string]any{"ok": true}, nil })})
	w.h = chattest.New(t, chattest.Options{DB: d, Clock: clock, Deps: w.b.Deps,
		Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", w.m), "telegram": conntest.ChannelRuntime("telegram", w.tg), "notes": conntest.ToolsRuntime("notes", func() *connectors.ToolSet { return notes }, "tools", nil)},
		Rows:     []chattest.Row{{"cliproxy", "model"}, {"telegram", "channel"}, {"notes", "tools"}}})
	return w
}

func (w *world) sweep(t *testing.T) {
	t.Helper()
	w.b.Scheduler.Sweep()
	w.b.Scheduler.Idle()
	w.h.Engine.Idle()
}

func TestFlowsWired(t *testing.T) {
	t.Run("a chat trigger starts a run the scheduler's flow extension executes, and /help lists it", func(t *testing.T) {
		w := flowWorld(t, models.Say("halo"))
		w.send(t, "hai", nil)
		chatID := w.chatID(t)
		_, err := flows.CreateFlow(w.h.DB, flows.SaveInput{ChatID: chatID, CreatedBy: "test", Definition: jobj(t, `{"name":"Beres","triggers":[{"id":"c","kind":"chat","command":"/beres","start":"ok"}],"steps":[{"id":"ok","kind":"message","text":"Dicatat: {{trigger.args}}"}]}`)}, w.h.Clock.Now())
		tu.Must(t, err)
		w.send(t, "/beres listrik", nil)
		w.sweep(t)
		got := texts(t, w.h.DB)
		if got[len(got)-1] != "Dicatat: listrik" {
			t.Fatalf("texts %q", got)
		}
		w.send(t, "/help", nil)
		got = texts(t, w.h.DB)
		if help := got[len(got)-1]; !strings.Contains(help, "Dari flow di chat ini:\n• /beres: flow \"Beres\"") {
			t.Fatalf("help %q", help)
		}
	})
	t.Run("a turn sees the open check-ins, and a check-in button is answered without the model", func(t *testing.T) {
		w := flowWorld(t, models.Say("halo"), models.Say("oke"))
		w.send(t, "hai", nil)
		chatID := w.chatID(t)
		saved, err := flows.CreateFlow(w.h.DB, flows.SaveInput{ChatID: chatID, CreatedBy: "test", Definition: jobj(t, `{"name":"Obat","steps":[{"id":"ask","kind":"checkin","text":"Udah minum obat?","deadline":{"kind":"after","minutes":60}}]}`)}, w.h.Clock.Now())
		tu.Must(t, err)
		r, err := w.b.Flows.StartRun(flows.StartInput{FlowID: saved.Flow.ID, TriggerKind: "manual", RunKey: "m1", Payload: jsonx.NewMap[any](), Now: w.h.Clock.Now()})
		tu.Must(t, err)
		w.sweep(t)
		w.send(t, "gimana?", nil)
		if p := models.PromptText(w.m.Prompts[len(w.m.Prompts)-1]); !strings.Contains(p, "flows__answer_checkin") || !strings.Contains(p, r.Run.ID) {
			t.Fatalf("prompt has no check-ins:\n%s", p)
		}
		n := len(w.m.Prompts)
		w.send(t, "", func(m *connectors.InboundMessage) {
			m.Callback = &connectors.InboundCallback{Data: "fl:" + r.Run.ID + ":done"}
		})
		got := texts(t, w.h.DB)
		if got[len(got)-1] != "Sip, dicatat: Sudah." || len(w.m.Prompts) != n {
			t.Fatalf("texts %q, prompts %d -> %d", got, n, len(w.m.Prompts))
		}
	})
	t.Run("a prompt step runs as an unattended flow turn", func(t *testing.T) {
		w := flowWorld(t, models.Say("halo"), models.Say("Semangat!"))
		w.send(t, "hai", nil)
		chatID := w.chatID(t)
		saved, err := flows.CreateFlow(w.h.DB, flows.SaveInput{ChatID: chatID, CreatedBy: "test", Definition: jobj(t, `{"name":"Cheer","steps":[{"id":"p","kind":"prompt","prompt":"Kasih semangat."},{"id":"m","kind":"message","text":"{{steps.p.text}}"}]}`)}, w.h.Clock.Now())
		tu.Must(t, err)
		_, err = w.b.Flows.StartRun(flows.StartInput{FlowID: saved.Flow.ID, TriggerKind: "manual", RunKey: "m1", Payload: jsonx.NewMap[any](), Now: w.h.Clock.Now()})
		tu.Must(t, err)
		w.sweep(t)
		var trigger string
		tu.Must(t, w.h.DB.QueryRow("SELECT trigger FROM runs ORDER BY rowid DESC LIMIT 1").Scan(&trigger))
		got := texts(t, w.h.DB)
		if trigger != "flow" || got[len(got)-1] != "Semangat!" {
			t.Fatalf("trigger %s texts %q", trigger, got)
		}
	})
	t.Run("a decided flr_ approval wakes the flow run", func(t *testing.T) {
		w := flowWorld(t, models.Say("halo"))
		w.send(t, "hai", nil)
		chatID := w.chatID(t)
		saved, err := flows.CreateFlow(w.h.DB, flows.SaveInput{ChatID: chatID, CreatedBy: "test", Definition: jobj(t, `{"name":"Catat","steps":[{"id":"n","kind":"tool","tool":"notes__save_note","args":{"text":"x"}},{"id":"m","kind":"message","text":"tersimpan"}]}`)}, w.h.Clock.Now())
		tu.Must(t, err)
		_, err = flows.DecideVersion(w.h.DB, saved.Version.ID, true, "test", w.h.Clock.Now())
		tu.Must(t, err)
		r, err := w.b.Flows.StartRun(flows.StartInput{FlowID: saved.Flow.ID, TriggerKind: "manual", RunKey: "m1", Payload: jsonx.NewMap[any](), Now: w.h.Clock.Now()})
		tu.Must(t, err)
		w.sweep(t)
		pending, err := approvals.ForRun(w.h.DB, r.Run.ID)
		tu.Must(t, err)
		if len(pending) != 1 {
			t.Fatalf("approvals %d", len(pending))
		}
		w.send(t, "ya", nil)
		w.sweep(t)
		run, err := flows.GetRun(w.h.DB, r.Run.ID)
		tu.Must(t, err)
		got := texts(t, w.h.DB)
		if run.Status != "done" || got[len(got)-1] != "tersimpan" {
			t.Fatalf("run %s texts %q", run.Status, got)
		}
	})
}

func jobj(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	o, err := jsonx.ParseObject([]byte(s))
	tu.Must(t, err)
	return o
}
