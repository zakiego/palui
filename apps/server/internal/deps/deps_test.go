package deps_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deps"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

var rem = db.Reminder{ChatID: "chat_1", Text: "bayar listrik", SmartPrompt: tu.Ptr("ingetin bayar listrik, cek tagihan")}

func turn(res chat.TurnResult) deps.RunTurn {
	return func(chat.TurnOptions) (chat.TurnResult, error) { return res, nil }
}

func TestRunSmartReminder(t *testing.T) {
	t.Run("resolves when the turn sent a reply or an approval prompt", func(t *testing.T) {
		tu.Must(t, deps.RunSmartReminder(turn(chat.TurnResult{RunID: "r", Text: "Jangan lupa bayar listrik", Status: "ok"}), rem))
		tu.Must(t, deps.RunSmartReminder(turn(chat.TurnResult{RunID: "r", Status: "awaiting_approval"}), rem))
	})
	t.Run("fails when nothing was sent, so the scheduler falls back to the plain text", func(t *testing.T) {
		for _, status := range []string{"draining", "no_model", "budget", "error", "aborted", "interrupted"} {
			if err := deps.RunSmartReminder(turn(chat.TurnResult{Status: status}), rem); err == nil || !strings.Contains(err.Error(), status) {
				t.Fatal(status, err)
			}
		}
		if err := deps.RunSmartReminder(turn(chat.TurnResult{RunID: "r", Text: "  ", Status: "ok"}), rem); err == nil || !strings.Contains(err.Error(), "status ok") {
			t.Fatal(err)
		}
		if err := deps.RunSmartReminder(func(chat.TurnOptions) (chat.TurnResult, error) { return chat.TurnResult{}, errors.New("x") }, rem); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("runs the smart prompt as a reminder turn", func(t *testing.T) {
		var seen chat.TurnOptions
		tu.Must(t, deps.RunSmartReminder(func(o chat.TurnOptions) (chat.TurnResult, error) {
			seen = o
			return chat.TurnResult{RunID: "r", Text: "x", Status: "ok"}, nil
		}, rem))
		if !reflect.DeepEqual(seen, chat.TurnOptions{ChatID: "chat_1", Trigger: "reminder", Prompt: *rem.SmartPrompt}) {
			t.Fatalf("%+v", seen)
		}
	})
}

func TestJobTurn(t *testing.T) {
	var seen chat.TurnOptions
	rt := deps.JobTurn(func(o chat.TurnOptions) (chat.TurnResult, error) {
		seen = o
		return chat.TurnResult{RunID: "run_1", Text: "ok", Status: "ok"}, nil
	})
	scope := []string{"notes"}
	res, err := rt(context.Background(), jobs.TurnRequest{ChatID: "c", Trigger: "schedule", Prompt: "p", JobID: "j", ToolScope: &scope, ModelRole: "fast", SilentToken: "NOTHING"})
	tu.Must(t, err)
	if res != (jobs.TurnResponse{RunID: "run_1", Text: "ok", Status: "ok"}) || !reflect.DeepEqual(seen, chat.TurnOptions{ChatID: "c", Trigger: "schedule", Prompt: "p", JobID: "j", ToolScope: &scope, ModelRole: "fast", SilentToken: "NOTHING"}) {
		t.Fatalf("%+v %+v", res, seen)
	}
}

type world struct {
	h     *chattest.Harness
	b     *deps.Built
	m     *models.ScriptedModel
	tg    *conntest.FakeChannel
	timer *tu.FakeClock
}

func newWorld(t *testing.T, steps ...models.Step) *world {
	d := tu.DB(t)
	chattest.OwnTelegram(t, d)
	chattest.ModelChain(t, d, "cliproxy")
	w := &world{m: models.NewScripted(steps...), tg: conntest.NewFakeChannel("telegram")}
	clock := chattest.NewClock(chattest.Start)
	w.timer = tu.NewFakeClock(chattest.Start)
	w.b = deps.Build(deps.Options{DB: d, Log: logx.Silent, Clock: clock.Now, Timers: w.timer,
		RunTurn: func(o chat.TurnOptions) (chat.TurnResult, error) { return w.h.Engine.RunTurn(o) }})
	w.h = chattest.New(t, chattest.Options{DB: d, Clock: clock, Deps: w.b.Deps,
		Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", w.m), "telegram": conntest.ChannelRuntime("telegram", w.tg)},
		Rows:     []chattest.Row{{"cliproxy", "model"}, {"telegram", "channel"}}})
	return w
}

func (w *world) send(t *testing.T, text string, f func(*connectors.InboundMessage)) {
	t.Helper()
	m := conntest.TgMessage(text)
	if f != nil {
		f(&m)
	}
	_, err := w.h.Engine.Inbound(m)
	tu.Must(t, err)
	w.h.Engine.Idle()
}

func (w *world) chatID(t *testing.T) string {
	var id string
	tu.Must(t, w.h.DB.QueryRow("SELECT id FROM chats WHERE channel = 'telegram' AND external_id = '42'").Scan(&id))
	return id
}

func texts(t *testing.T, q db.Q) []string {
	rows, err := q.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY rowid")
	all, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(t, err)
	var out []string
	for _, o := range all {
		out = append(out, o.Payload.Text)
	}
	return out
}

func TestWiredIntoTheEngine(t *testing.T) {
	t.Run("the router runs the reminder buttons and quoted replies", func(t *testing.T) {
		w := newWorld(t, models.Say("halo"))
		w.send(t, "hai", nil)
		now := w.h.Clock.Now()
		chatID := w.chatID(t)
		r, err := reminders.Create(w.h.DB, reminders.CreateInput{ChatID: chatID, Text: "bayar listrik", LocalStart: reminders.UTCToLocal(now, "Asia/Makassar"), TZ: "Asia/Makassar"}, now)
		tu.Must(t, err)
		w.send(t, "", func(m *connectors.InboundMessage) {
			m.Callback = &connectors.InboundCallback{Data: "rem:snooze:" + r.ID + ":1h"}
		})
		got, _, _ := reminders.Get(w.h.DB, r.ID)
		if got.FireAt != now+tu.HOUR {
			t.Fatal(got.FireAt)
		}
		w.send(t, "", func(m *connectors.InboundMessage) { m.Callback = &connectors.InboundCallback{Data: "rem:done:" + r.ID} })
		if got, _, _ := reminders.Get(w.h.DB, r.ID); got.Status != "done" {
			t.Fatal(got.Status)
		}
		all := texts(t, w.h.DB)
		if !strings.HasPrefix(all[len(all)-2], "Oke, ditunda sampai") || all[len(all)-1] != "Sip, ditandai selesai." {
			t.Fatal(all)
		}
	})
	t.Run("a bare batal cancels the held message to a contact", func(t *testing.T) {
		w := newWorld(t, models.Say("halo"))
		w.send(t, "hai", nil)
		now := w.h.Clock.Now()
		owner := w.chatID(t)
		c, err := outbound.AddContact(w.h.DB, outbound.AddContactInput{AgentID: "agt_x", Name: "Zaki", Phone: "082200001001"}, now)
		tu.Must(t, err)
		r, err := outbound.SendNow(w.h.DB, c, "otw", outbound.SendMeta{Origin: "chat", ApprovedBy: "owner", OwnerChatID: &owner}, 15_000, now)
		tu.Must(t, err)
		w.send(t, "batal", nil)
		all := texts(t, w.h.DB)
		if all[len(all)-1] != "Oke, pesan ke Zaki dibatalin." {
			t.Fatal(all)
		}
		if o, _, _ := outbound.GetOutbound(w.h.DB, r.Row.ID); o.Status != "done" {
			t.Fatal(o.Status)
		}
		var st string
		tu.Must(t, w.h.DB.QueryRow("SELECT status FROM outbox WHERE id = ?", r.OutboxID).Scan(&st))
		if st != "cancelled" {
			t.Fatal(st)
		}
	})
	t.Run("jobs and smart reminders run as turns", func(t *testing.T) {
		w := newWorld(t, models.Say("halo"), models.Say("Briefing: cerah."), models.Say("Bawa payung."))
		w.send(t, "hai", nil)
		now := w.h.Clock.Now()
		chatID := w.chatID(t)
		j, err := jobs.Create(w.h.DB, jobs.CreateInput{Name: "Pagi", RRule: "FREQ=DAILY", TZ: "Asia/Makassar", Prompt: "briefing", ChatID: chatID}, now)
		tu.Must(t, err)
		res, err := w.b.Deps.Jobs.RunNow(context.Background(), j.ID)
		tu.Must(t, err)
		if !res.OK || res.RunID == "" {
			t.Fatalf("%+v", res)
		}
		var trig, jobID string
		tu.Must(t, w.h.DB.QueryRow("SELECT trigger, job_id FROM runs WHERE id = ?", res.RunID).Scan(&trig, &jobID))
		if trig != "schedule" || jobID != j.ID {
			t.Fatal(trig, jobID)
		}
		after, _, _ := jobs.Get(w.h.DB, j.ID)
		if *after.LastResult != "Briefing: cerah." {
			t.Fatal(*after.LastResult)
		}
		r, err := reminders.Create(w.h.DB, reminders.CreateInput{ChatID: chatID, Text: "cek cuaca", LocalStart: reminders.UTCToLocal(now, "Asia/Makassar"), TZ: "Asia/Makassar", SmartPrompt: tu.Ptr("Cek cuaca")}, now)
		tu.Must(t, err)
		tu.Must(t, w.b.Deps.Scheduler.Start())
		w.b.Scheduler.Idle()
		w.h.Engine.Idle()
		if got, _, _ := reminders.Get(w.h.DB, r.ID); got.Status != "done" {
			t.Fatal(got.Status)
		}
		var n int
		tu.Must(t, w.h.DB.QueryRow("SELECT count(*) FROM runs WHERE trigger = 'reminder' AND status = 'ok'").Scan(&n))
		if n != 1 {
			t.Fatal(n)
		}
		all := texts(t, w.h.DB)
		if all[len(all)-1] != "Bawa payung." {
			t.Fatal(all)
		}
		w.b.Deps.Scheduler.Poke()
		tu.Must(t, w.b.Deps.Scheduler.Stop(context.Background()))
	})
	t.Run("parses through the model with the parser deps", func(t *testing.T) {
		w := newWorld(t, models.Say(`{"text":"bayar listrik","localDateTime":"2026-11-11T09:00","rrule":null}`))
		p, err := w.b.Deps.ParseReminder(context.Background(), "besok jam 9 bayar listrik", "Asia/Makassar", tu.NOW, w.m)
		tu.Must(t, err)
		if !p.OK || p.Echo != "Besok, Rabu 11 Nov, 09.00 WITA" {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("the outbound-failed hook notices the owner", func(t *testing.T) {
		w := newWorld(t, models.Say("halo"))
		w.send(t, "hai", nil)
		owner := w.chatID(t)
		now := w.h.Clock.Now()
		c, err := outbound.AddContact(w.h.DB, outbound.AddContactInput{AgentID: "agt_x", Name: "Zaki", Phone: "082200001001"}, now)
		tu.Must(t, err)
		r, err := outbound.SendNow(w.h.DB, c, "otw", outbound.SendMeta{Origin: "chat", ApprovedBy: "owner", OwnerChatID: &owner}, 0, now)
		tu.Must(t, err)
		deps.OutboundFailed(logx.Silent)(w.h.DB, db.Outbox{ID: r.OutboxID, Source: "outbound:" + r.Row.ID}, now)
		all := texts(t, w.h.DB)
		if all[len(all)-1] != "Pesan ke Zaki gagal terkirim." {
			t.Fatal(all)
		}
	})
}
