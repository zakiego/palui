package proactive_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	"github.com/zakiego/palui/apps/server/internal/settings"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

// NOW is Tuesday 2026-11-10 10:00 WITA.
var NOW = tu.NOW

const (
	HOUR = tu.HOUR
	MIN  = int64(60_000)
)

// fakeTools is a ToolHost with a calendar read tool whose result tests set,
// a write tool, and an unclassified one.
type fakeTools struct {
	mu     sync.Mutex
	events string
	calls  int
	err    error
}

func (f *fakeTools) set(events string) {
	f.mu.Lock()
	f.events = events
	f.mu.Unlock()
}

func (f *fakeTools) Tools(_ context.Context, _, _ string, _ int64) ([]proactive.Tool, error) {
	if f.err != nil {
		return nil, f.err
	}
	read := proactive.Tool{
		Info:        proactive.ToolInfo{Name: "gcal__get_calendar", ConnectorID: "gcal", ConnectorLabel: "Google Calendar", Tool: "get_calendar", Class: "read", Untrusted: true},
		Description: "Events in a range",
		Schema:      json.RawMessage(`{"type":"object","properties":{"from":{"type":"string"}}}`),
		Execute: func(_ context.Context, _ json.RawMessage) (any, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls++
			return f.events, nil
		},
	}
	noop := func(context.Context, json.RawMessage) (any, error) { return "ok", nil }
	write := proactive.Tool{Info: proactive.ToolInfo{Name: "notes__add_note", ConnectorID: "notes", Tool: "add_note", Class: "write"}, Execute: noop}
	unsorted := proactive.Tool{Info: proactive.ToolInfo{Name: "hub__list_things", ConnectorID: "hub", Tool: "list_things", Class: "destructive", SuggestedClass: "read"}, Execute: noop}
	return []proactive.Tool{read, write, unsorted}, nil
}

type host struct{ m models.LanguageModel }

func (h host) ConnectorModel(id, model, role string) (models.LanguageModel, bool, error) {
	return h.m, true, nil
}

type world struct {
	t     *testing.T
	d     *db.DB
	now   int64
	tools *fakeTools
	model *models.ScriptedModel
	e     *proactive.Engine
	agent db.Agent
	chat  db.Chat
}

const prompt = "Check @gcal__get_calendar for the next 36 hours and tell me when to leave."

func newWorld(t *testing.T, mutate func(*proactive.Config)) *world {
	t.Helper()
	d := tu.DB(t)
	w := &world{t: t, d: d, now: NOW, tools: &fakeTools{events: `[{"id":"e1","title":"Klinik","start":"2026-11-10T14:00:00+08:00","location":"Jl. A. Yani km 4"}]`}, model: models.NewScripted().WithFallback(models.Say(`{"items":[]}`))}
	chat, err := chats.EnsureWebChat(d, NOW)
	tu.Must(t, err)
	w.chat = chat
	a, err := agents.GetDefault(d, NOW)
	tu.Must(t, err)
	w.agent = a
	tu.Must(t, settings.Set(d, "modelRoles", settings.ModelRoles{Fast: []settings.ModelChainLink{{ConnectorID: "m", Model: "x"}}}, NOW))
	cfg := proactive.Defaults()
	cfg.Prompt = prompt
	if mutate != nil {
		mutate(&cfg)
	}
	on := true
	_, _, err = proactive.Save(d, proactive.SaveInput{AgentID: a.ID, Enabled: &on, ChatID: &chat.ID, Config: &cfg, Tools: w.tools}, NOW)
	tu.Must(t, err)
	w.e = proactive.New(proactive.Options{DB: d, Clock: func() int64 { return w.now }, Tools: w.tools, Models: host{w.model}, Serial: true})
	return w
}

func (w *world) sweep() {
	w.t.Helper()
	_, err := w.e.Sweep(w.now)
	tu.Must(w.t, err)
	w.e.Idle()
}

func (w *world) at(t int64) *world { w.now = t; return w }

func (w *world) nudge(key string) db.Nudge {
	w.t.Helper()
	n, err := db.ScanNudge(w.d.QueryRow("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND key = ?", w.agent.ID, key))
	if err != nil {
		w.t.Fatalf("nudge %s: %v", key, err)
	}
	return n
}

func (w *world) outbox() []db.Outbox {
	w.t.Helper()
	rows, err := w.d.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY created_at, rowid")
	list, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(w.t, err)
	return list
}

func (w *world) ticks() []db.ProactiveTick {
	w.t.Helper()
	list, err := proactive.Ticks(w.d, w.agent.ID, 0, 100)
	tu.Must(w.t, err)
	return list
}

func (w *world) row() db.AgentProactive {
	w.t.Helper()
	r, _, _, err := proactive.Get(w.d, w.agent.ID)
	tu.Must(w.t, err)
	return r
}

// answer queues a run: one calendar call, then the JSON answer.
func (w *world) answer(out string) {
	w.model.Push(models.CallTool("gcal__get_calendar", map[string]any{"from": "now"}, ""), models.Say(out))
}

func TestMentionsParseNamespacedToolsOnly(t *testing.T) {
	got := proactive.Mentions("Check @gcal__get_calendar, then @mcp-j__list_chats. Mail me at a@b__c or @gcal__get_calendar again. @nounderscore @x__")
	if strings.Join(got, ",") != "gcal__get_calendar,mcp-j__list_chats" {
		t.Fatalf("%v", got)
	}
}

func TestCheckExplainsEveryMentionThatDoesntResolve(t *testing.T) {
	list, _ := (&fakeTools{}).Tools(context.Background(), "", "", 0)
	infos := proactive.Infos(list)
	tu.Must(t, proactive.Check([]string{"gcal__get_calendar"}, infos))
	err := proactive.Check([]string{"gcal__get_calendar", "gone__x", "notes__add_note", "hub__list_things"}, infos)
	var ce *proactive.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"@gone__x " + proactive.ReasonNotFound, "@notes__add_note " + proactive.ReasonNotRead, "@hub__list_things " + proactive.ReasonUnsorted} {
		if !strings.Contains(ce.Msg, want) {
			t.Errorf("missing %q in %q", want, ce.Msg)
		}
	}
	if err := proactive.Check(nil, infos); err == nil || !strings.Contains(err.Error(), "Mention at least one tool") {
		t.Fatalf("%v", err)
	}
	st := proactive.MentionStatus([]string{"gcal__get_calendar", "gone__x"}, infos)
	if st[0].Reason != "" || st[1].Reason != proactive.ReasonNotFound {
		t.Fatalf("%+v", st)
	}
}

func TestConfigBoundsPatchAndUpgrade(t *testing.T) {
	c := proactive.Defaults()
	tu.Must(t, c.Validate())
	next, err := proactive.MergePatch(c, []byte(`{"quietHours":{"start":"23:00"},"prompt":"  hi @a__b \r\n"}`))
	tu.Must(t, err)
	if next.QuietHours.Start != "23:00" || next.QuietHours.End != "07:00" || next.Prompt != "hi @a__b" {
		t.Fatalf("%+v", next)
	}
	// The same patch sent as a JSON string, the way models often send it.
	str, err := proactive.MergePatch(c, []byte(`"{\"quietHours\":{\"start\":\"23:00\"},\"prompt\":\"  hi @a__b \\r\\n\"}"`))
	tu.Must(t, err)
	if str != next {
		t.Fatalf("string patch: %+v", str)
	}
	for _, notObject := range []string{`"hello"`, `"[1]"`, `[1]`, `3`} {
		_, err := proactive.MergePatch(c, []byte(notObject))
		if err == nil || !strings.Contains(err.Error(), "must be a JSON object") {
			t.Errorf("%s: %v", notObject, err)
		}
	}
	long := strings.Repeat("x", proactive.MaxPromptChars+1)
	many := ""
	for i := 0; i < proactive.MaxMentions+1; i++ {
		many += " @c__t" + string(rune('a'+i))
	}
	for _, bad := range []string{`{"checkEveryMin":5}`, `{"quietHours":{"start":"25:00"}}`, `{"modelRole":"vision"}`, `{"calendar":{}}`, `{"prompt":"` + long + `"}`, `{"prompt":"` + many + `"}`} {
		if _, err := proactive.MergePatch(c, []byte(bad)); err == nil {
			t.Errorf("%.60s should be refused", bad)
		}
	}
	q := proactive.QuietHours{Start: "22:00", End: "07:00"}
	if !q.InQuiet(23*60) || !q.InQuiet(6*60+59) || q.InQuiet(7*60) || q.InQuiet(12*60) {
		t.Fatal("quiet hours wrap midnight")
	}
	// Schema 1 keeps its rhythm and gets a starter prompt from its sources.
	v1 := `{"schema":1,"checkEveryMin":45,"maxPerHour":2,"quietHours":{"start":"23:00","end":"06:00","urgentPasses":false},"morning":{"on":false,"at":"08:00"},"evening":{"on":true,"at":"21:00"},"calendar":{"connectorId":"gcal","departLeadMin":40,"onlineLeadMin":5,"notifyChanges":true},"whatsapp":{"connectorId":"wa","account":"personal","waitHours":3,"escalateHours":6,"maxAgeDays":3,"skipGroups":true},"contactsAuto":true,"contacts":[{"subject":"wa:chat:7","label":"Istri"}],"modelRole":"default"}`
	up, err := proactive.ParseConfig([]byte(v1))
	tu.Must(t, err)
	tu.Must(t, up.Validate())
	if up.Schema != proactive.ConfigSchema || up.CheckEveryMin != 45 || up.MaxPerHour != 2 || up.QuietHours.Start != "23:00" || up.Morning.On || up.Evening.At != "21:00" || up.ModelRole != "default" {
		t.Fatalf("%+v", up)
	}
	for _, want := range []string{"40 minutes before", "5 minutes before", "more than 3 hours", "6 hours after", "Istri", "Skip groups"} {
		if !strings.Contains(up.Prompt, want) {
			t.Errorf("starter misses %q:\n%s", want, up.Prompt)
		}
	}
	if len(proactive.Mentions(up.Prompt)) != 0 {
		t.Fatal("the starter leaves the mentions to the owner")
	}
}

func TestOutputIsReadLenientlyAndBounded(t *testing.T) {
	in := "Here you go:\n```json\n" + `{"items":[
		{"key":"cal:e1:leave","text":"Berangkat ke klinik","sendAt":"2026-11-10T13:30:00+08:00","urgent":true},
		{"key":"wa:7:123","subject":"wa:7","text":"Istri nunggu"},
		{"key":"bad key!","text":"x"},
		{"key":"cal:e2:leave","text":"x","sendAt":"2026-11-13T10:00:00+08:00"},
		{"key":"cal:e3:leave","text":"x","sendAt":"2026-11-10T09:00:00+08:00"},
		{"key":"cal:e4:leave","text":"x","sendAt":"2026-11-10T09:55:00+08:00"},
		{"key":"empty","text":"  "}
	],"closed":[{"key":"wa:9:1","why":"replied"}],"remember":[{"text":"Pak Budi janji kirim besok","days":3}],"forget":["pmem_1","nope"]}` + "\n```"
	out, err := proactive.ParseOutput(in, NOW)
	tu.Must(t, err)
	if len(out.Items) != 3 {
		t.Fatalf("items %+v skipped %v", out.Items, out.Skipped)
	}
	if out.Items[0].SendAt != time.Date(2026, 11, 10, 5, 30, 0, 0, time.UTC).UnixMilli() || !out.Items[0].Urgent || out.Items[0].Subject != "cal:e1" {
		t.Fatalf("%+v", out.Items[0])
	}
	if out.Items[1].Subject != "wa:7" || out.Items[2].Key != "cal:e4:leave" || out.Items[2].SendAt != 0 {
		t.Fatalf("%+v", out.Items)
	}
	if len(out.Skipped) != 4 || len(out.Closed) != 1 || len(out.Remember) != 1 || len(out.Forget) != 1 {
		t.Fatalf("%+v", out)
	}
	if _, err := proactive.ParseOutput("nothing to say", NOW); err == nil {
		t.Fatal("no JSON")
	}
}

func TestTheRunWritesTheMessageForWhatGoesOutNow(t *testing.T) {
	w := newWorld(t, nil)
	msg := "Hai Zaki 👋, ini yang perlu kamu cek:\n\n1. 💬 Bu Diana belum dibalas.\n2. 📧 Tagihan listrik jatuh tempo besok."
	w.answer(`{"items":[{"key":"wa:1:a","text":"Bu Diana belum dibalas."},{"key":"mail:z:9","text":"Tagihan listrik jatuh tempo besok."},{"key":"cal:e1:leave","text":"Berangkat ke klinik.","sendAt":"2026-11-10T13:30:00+08:00"}],"message":` + strconv.Quote(msg) + `}`)
	w.sweep()
	out := w.outbox()
	if len(out) != 1 || out[0].Payload.Text != msg || len(out[0].Payload.Actions) != 0 {
		t.Fatalf("%+v", out)
	}
	if w.nudge("wa:1:a").Status != proactive.StatusSent || w.nudge("mail:z:9").Status != proactive.StatusSent || w.nudge("cal:e1:leave").Status != proactive.StatusScheduled {
		t.Fatal("the message carries the items sent now; the timed one waits")
	}
	// No wording: the texts go out one after another.
	w.answer(`{"items":[{"key":"wa:2:b","text":"Mama Isti nanya video."},{"key":"wa:3:c","text":"Lim nanya helm."}]}`)
	w.at(NOW + 2*HOUR).sweep()
	if out := w.outbox(); len(out) != 2 || out[1].Payload.Text != "Mama Isti nanya video.\n\nLim nanya helm." {
		t.Fatalf("%+v", out)
	}
}

func TestRunSendsNowSchedulesTimedAndRemembers(t *testing.T) {
	w := newWorld(t, nil)
	w.answer(`{"items":[{"key":"cal:e1:leave","text":"30 menit lagi berangkat ke klinik.","sendAt":"2026-11-10T13:30:00+08:00"},{"key":"cal:e1:today","text":"Hari ini jam 14.00 ke klinik."}],"remember":[{"text":"Klinik minta datang 15 menit lebih awal","days":2}]}`)
	w.sweep()
	out := w.outbox()
	if len(out) != 1 || out[0].Payload.Text != "Hari ini jam 14.00 ke klinik." || len(out[0].Payload.Actions) != 0 || out[0].Source != proactive.OutboxSourcePrefix+w.agent.ID {
		t.Fatalf("%+v", out)
	}
	if n := w.nudge("cal:e1:today"); n.Status != proactive.StatusSent {
		t.Fatalf("%+v", n)
	}
	timed := w.nudge("cal:e1:leave")
	if timed.Status != proactive.StatusScheduled || *timed.WakeAt != time.Date(2026, 11, 10, 5, 30, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("%+v", timed)
	}
	notes, err := proactive.Notes(w.d, w.agent.ID, w.now)
	tu.Must(t, err)
	if len(notes) != 1 || notes[0].Source != proactive.NoteSourceModel || *notes[0].ExpiresAt != NOW+2*24*HOUR {
		t.Fatalf("%+v", notes)
	}
	// Only the mentioned tool was offered.
	if offered := w.model.Offered[0]; len(offered) != 1 || offered[0] != "gcal__get_calendar" {
		t.Fatalf("%v", offered)
	}
	// The timed item fires at its minute, from its stored text.
	w.at(*timed.WakeAt).sweep()
	if out := w.outbox(); len(out) != 2 || out[1].Payload.Text != "30 menit lagi berangkat ke klinik." {
		t.Fatalf("%+v", out)
	}
}

func TestAKeyIsNeverSentTwiceButAScheduledOneMoves(t *testing.T) {
	w := newWorld(t, nil)
	w.answer(`{"items":[{"key":"wa:7:1","text":"Istri nunggu balasan."},{"key":"cal:e1:leave","text":"Berangkat.","sendAt":"2026-11-10T13:30:00+08:00"}]}`)
	w.sweep()
	w.tools.set(`[{"id":"e1","title":"Klinik","start":"2026-11-10T15:00:00+08:00"}]`)
	w.answer(`{"items":[{"key":"wa:7:1","text":"Istri masih nunggu."},{"key":"cal:e1:leave","text":"Berangkat, jamnya mundur.","sendAt":"2026-11-10T14:30:00+08:00"}]}`)
	w.at(NOW + 30*MIN).sweep()
	if out := w.outbox(); len(out) != 1 {
		t.Fatalf("sent twice: %+v", out)
	}
	n := w.nudge("cal:e1:leave")
	if n.Status != proactive.StatusScheduled || *n.WakeAt != time.Date(2026, 11, 10, 6, 30, 0, 0, time.UTC).UnixMilli() || !strings.Contains(string(n.Snapshot), "mundur") {
		t.Fatalf("%+v %s", n, n.Snapshot)
	}
}

func TestReplaySkipsTheModelWhenNothingChanged(t *testing.T) {
	w := newWorld(t, nil)
	w.answer(`{"items":[]}`)
	w.sweep()
	calls := len(w.model.Calls)
	w.at(NOW + 30*MIN).sweep()
	if len(w.model.Calls) != calls {
		t.Fatalf("model ran on an unchanged tick: %d -> %d", calls, len(w.model.Calls))
	}
	ticks := w.ticks()
	var l proactive.TickLog
	tu.Must(t, ticks[0].Sources.Decode(&l))
	if l.Skipped != proactive.SkipUnchanged || l.Full || ticks[0].Error != nil {
		t.Fatalf("%+v %v", l, ticks[0].Error)
	}
	// A changed result runs the model again.
	w.tools.set(`[{"id":"e9","title":"Rapat baru","start":"2026-11-10T16:00:00+08:00"}]`)
	w.answer(`{"items":[{"key":"cal:e9:new","text":"Ada rapat baru jam 16.00."}]}`)
	w.at(NOW + 60*MIN).sweep()
	if out := w.outbox(); len(out) != 1 || !strings.Contains(out[0].Payload.Text, "rapat baru") {
		t.Fatalf("%+v", out)
	}
	// And so does a run 2 hours after the last full one, unchanged or not.
	w.answer(`{"items":[]}`)
	before := len(w.model.Calls)
	w.at(NOW + 60*MIN + proactive.FullRunEveryMs).sweep()
	if len(w.model.Calls) == before {
		t.Fatal("a full run is due every 2 hours")
	}
}

func TestAMissingMentionFailsTheRunAndThreeInARowPause(t *testing.T) {
	w := newWorld(t, nil)
	_, err := w.d.Exec("UPDATE agent_proactive SET config = json_set(config, '$.prompt', 'Read @gone__list_events') WHERE agent_id = ?", w.agent.ID)
	tu.Must(t, err)
	for i := 0; i < 3; i++ {
		w.at(NOW + int64(i)*30*MIN).sweep()
	}
	ticks := w.ticks()
	if len(ticks) != 3 || ticks[0].Error == nil || !strings.Contains(*ticks[0].Error, "@gone__list_events "+proactive.ReasonNotFound) {
		t.Fatalf("%+v", ticks)
	}
	if r := w.row(); r.PausedReason == nil || !strings.Contains(*r.PausedReason, "3 runs in a row") {
		t.Fatalf("%+v", r)
	}
	out := w.outbox()
	if len(out) != 2 || !strings.Contains(out[0].Payload.Text, "gagal jalan") || !strings.Contains(out[1].Payload.Text, "matiin") {
		t.Fatalf("one notice a day, then the pause: %+v", out)
	}
	if len(w.model.Calls) != 0 {
		t.Fatal("no model call without the tools")
	}
}

func TestQuietHoursHoldRunsAndMessages(t *testing.T) {
	w := newWorld(t, nil)
	w.answer(`{"items":[{"key":"cal:e1:leave","text":"Berangkat.","sendAt":"2026-11-10T22:30:00+08:00"},{"key":"cal:e2:moved","text":"Rapat besok pagi pindah.","sendAt":"2026-11-10T23:00:00+08:00","urgent":true}]}`)
	w.sweep()
	// 22:30 WITA: quiet. The plain timed item is held, the urgent one passes.
	w.at(time.Date(2026, 11, 10, 14, 30, 0, 0, time.UTC).UnixMilli()).sweep()
	if n := w.nudge("cal:e1:leave"); n.Status != proactive.StatusPending {
		t.Fatalf("%+v", n)
	}
	r := w.row()
	if *r.NextTickAt != time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("next tick should wait for 07:00 WITA: %d", *r.NextTickAt)
	}
	w.at(time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC).UnixMilli()).sweep()
	if out := w.outbox(); len(out) != 1 || out[0].Payload.Text != "Rapat besok pagi pindah." {
		t.Fatalf("%+v", out)
	}
	// 07:00: the morning summary carries what was held.
	w.answer(`{"items":[],"summary":"Pagi! Semalam ada pengingat berangkat yang ketahan."}`)
	w.answer(`{"items":[]}`)
	w.at(time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC).UnixMilli()).sweep()
	out := w.outbox()
	var summary *db.Outbox
	for i := range out {
		if strings.Contains(out[i].Payload.Text, "Pagi!") {
			summary = &out[i]
		}
	}
	if summary == nil {
		t.Fatalf("%+v", out)
	}
	if n := w.nudge("cal:e1:leave"); n.Status != proactive.StatusSent || *n.OutboxID != summary.ID {
		t.Fatalf("held item goes out with the summary: %+v", n)
	}
}

func TestMutedSubjectsAndClosedKeys(t *testing.T) {
	w := newWorld(t, nil)
	tu.Must(t, proactive.Mute(w.d, w.agent.ID, "wa:46", "Grup promo", NOW))
	w.answer(`{"items":[{"key":"wa:46:1","text":"Promo."},{"key":"wa:7:1","text":"Istri nunggu.","sendAt":"2026-11-10T12:00:00+08:00"}]}`)
	w.sweep()
	var n int
	tu.Must(t, w.d.QueryRow("SELECT count(*) FROM nudges WHERE key = 'wa:46:1'").Scan(&n))
	if n != 0 {
		t.Fatal("a muted subject gets no nudge")
	}
	w.tools.set("changed")
	w.answer(`{"items":[],"closed":[{"key":"wa:7:1","why":"udah dibales"}]}`)
	w.at(NOW + 30*MIN).sweep()
	if got := w.nudge("wa:7:1"); got.Status != proactive.StatusDropped || *got.Reason != "model: udah dibales" {
		t.Fatalf("%+v", got)
	}
}

func TestNotesKeepOwnerNotesAndPushOutTheModels(t *testing.T) {
	d := tu.DB(t)
	a, err := agents.GetDefault(d, NOW)
	tu.Must(t, err)
	long := strings.Repeat("x", proactive.MaxNoteChars-1)
	for i := 0; i < proactive.MaxNotesChars/proactive.MaxNoteChars-1; i++ {
		_, err := proactive.AddNote(d, a.ID, long+string(rune('a'+i)), proactive.NoteSourceOwner, 0, NOW+int64(i))
		tu.Must(t, err)
	}
	m, err := proactive.AddNote(d, a.ID, strings.Repeat("m", 200), proactive.NoteSourceModel, 99, NOW+100)
	tu.Must(t, err)
	if *m.ExpiresAt != NOW+100+int64(proactive.NoteDaysMax)*24*HOUR {
		t.Fatalf("model notes live at most 30 days: %+v", m)
	}
	// An owner note that doesn't fit pushes the model's note out first...
	_, err = proactive.AddNote(d, a.ID, long+"y", proactive.NoteSourceOwner, 0, NOW+200)
	tu.Must(t, err)
	list, err := proactive.Notes(d, a.ID, NOW+300)
	tu.Must(t, err)
	for _, n := range list {
		if n.Source == proactive.NoteSourceModel {
			t.Fatal("the model's note should be gone")
		}
	}
	// ...and is refused when only owner notes are left.
	if _, err := proactive.AddNote(d, a.ID, long+"z", proactive.NoteSourceOwner, 0, NOW+400); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("%v", err)
	}
	// The model can't delete an owner note.
	ok, err := proactive.ForgetNote(d, a.ID, list[0].ID, true)
	tu.Must(t, err)
	if ok {
		t.Fatal("onlyModel")
	}
	// Keep turns a model note into the owner's, with no expiry.
	tu.Must(t, func() error { _, err := d.Exec("DELETE FROM proactive_notes"); return err }())
	mn, err := proactive.AddNote(d, a.ID, "short", proactive.NoteSourceModel, 1, NOW)
	tu.Must(t, err)
	ok, err = proactive.EditNote(d, a.ID, mn.ID, nil, true, NOW+1)
	tu.Must(t, err)
	kept, err := proactive.Notes(d, a.ID, NOW+10*24*HOUR)
	tu.Must(t, err)
	if !ok || len(kept) != 1 || kept[0].Source != proactive.NoteSourceOwner || kept[0].ExpiresAt != nil {
		t.Fatalf("%+v", kept)
	}
}

func TestAReplyThatIsntAnActionBecomesAnOwnerNote(t *testing.T) {
	w := newWorld(t, nil)
	w.answer(`{"items":[{"key":"wa:46:1","text":"Grup bank nanya OTP."}]}`)
	w.sweep()
	out := w.outbox()
	_, err := w.d.Exec("UPDATE outbox SET channel_msg_id = 'm1' WHERE id = ?", out[0].ID)
	tu.Must(t, err)
	reply, ok, err := proactive.Actions{}.FromText(w.d, "yang dari bank nggak usah", "m1", w.chat.ID, NOW+MIN)
	tu.Must(t, err)
	if ok || reply != "" {
		t.Fatal("the turn still answers the reply")
	}
	notes, err := proactive.Notes(w.d, w.agent.ID, NOW+MIN)
	tu.Must(t, err)
	if len(notes) != 1 || notes[0].Source != proactive.NoteSourceOwner || !strings.Contains(notes[0].Text, "yang dari bank nggak usah") || !strings.Contains(notes[0].Text, "Grup bank") {
		t.Fatalf("%+v", notes)
	}
	if r := w.row(); *r.NextTickAt != NOW+MIN {
		t.Fatalf("the next run comes now: %d", *r.NextTickAt)
	}
	// An action is still an action.
	reply, ok, err = proactive.Actions{}.FromText(w.d, "done", "m1", w.chat.ID, NOW+2*MIN)
	tu.Must(t, err)
	if !ok || !strings.Contains(reply, "selesai") {
		t.Fatalf("%q %v", reply, ok)
	}
}

func TestSaveRefusesTurningOnWithoutResolvableMentions(t *testing.T) {
	d := tu.DB(t)
	chat, err := chats.EnsureWebChat(d, NOW)
	tu.Must(t, err)
	a, err := agents.GetDefault(d, NOW)
	tu.Must(t, err)
	on := true
	cfg := proactive.Defaults()
	cfg.Prompt = "Check @notes__add_note"
	_, _, err = proactive.Save(d, proactive.SaveInput{AgentID: a.ID, Enabled: &on, ChatID: &chat.ID, Config: &cfg, Tools: &fakeTools{}}, NOW)
	if err == nil || !strings.Contains(err.Error(), proactive.ReasonNotRead) {
		t.Fatalf("%v", err)
	}
	// Off, any prompt can be saved as a draft.
	off := false
	_, _, err = proactive.Save(d, proactive.SaveInput{AgentID: a.ID, Enabled: &off, ChatID: &chat.ID, Config: &cfg, Tools: &fakeTools{}}, NOW)
	tu.Must(t, err)
}

// dbTools reads the database the way the live host does (connectors, agent
// permissions) before it answers.
type dbTools struct {
	fakeTools
	d *db.DB
}

func (f *dbTools) Tools(ctx context.Context, agentID, chatID string, now int64) ([]proactive.Tool, error) {
	var n int
	if err := f.d.QueryRow("SELECT count(*) FROM connectors").Scan(&n); err != nil {
		return nil, err
	}
	return f.fakeTools.Tools(ctx, agentID, chatID, now)
}

func TestSaveListsToolsOutsideItsTransaction(t *testing.T) {
	d := tu.DB(t)
	chat, err := chats.EnsureWebChat(d, NOW)
	tu.Must(t, err)
	a, err := agents.GetDefault(d, NOW)
	tu.Must(t, err)
	on := true
	cfg := proactive.Defaults()
	cfg.Prompt = prompt
	done := make(chan error, 1)
	go func() {
		_, _, err := proactive.Save(d, proactive.SaveInput{AgentID: a.ID, Enabled: &on, ChatID: &chat.ID, Config: &cfg, Tools: &dbTools{d: d}}, NOW)
		done <- err
	}()
	select {
	case err := <-done:
		tu.Must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Save waited on the database it holds: the live tool host reads it")
	}
}

func TestAnEmptyAnswerFailsTheRunInsteadOfSayingNothing(t *testing.T) {
	w := newWorld(t, nil)
	var maxOut int
	cutOff := models.Step{Fn: func(_ context.Context, opts models.CallOptions) ([]models.Event, error) {
		maxOut = *opts.MaxOutputTokens
		return []models.Event{{Type: models.EventFinish, FinishReason: models.FinishReason{Unified: "length"}, Usage: models.MockUsage(10)}}, nil
	}}
	w.model.Push(models.CallTool("gcal__get_calendar", map[string]any{"from": "now"}, ""), cutOff)
	res, err := w.e.TestRun(context.Background(), w.agent.ID, nil, NOW)
	tu.Must(t, err)
	if !strings.Contains(res.Error, "ran out of output tokens") || len(res.Items) != 0 {
		t.Fatalf("%+v", res)
	}
	if maxOut != proactive.MaxRunOutputTokens {
		t.Fatalf("a run's model call gets %d output tokens", maxOut)
	}
	// A live run with no answer counts as a failure, not a quiet run.
	w.model.Push(models.CallTool("gcal__get_calendar", map[string]any{"from": "now"}, ""), models.Say("  "))
	w.sweep()
	if tl := w.ticks(); len(tl) == 0 || tl[0].Error == nil || !strings.Contains(*tl[0].Error, "no answer") {
		t.Fatalf("the empty answer wasn't recorded as a failure: %+v", tl)
	}
}

func TestTestRunWritesNothing(t *testing.T) {
	w := newWorld(t, nil)
	w.answer(`{"items":[{"key":"cal:e1:leave","text":"Berangkat.","sendAt":"2026-11-10T13:30:00+08:00"}],"remember":[{"text":"x"}]}`)
	res, err := w.e.TestRun(context.Background(), w.agent.ID, nil, NOW)
	tu.Must(t, err)
	if len(res.Items) != 1 || res.Items[0].SendAt == nil || len(res.Calls) != 1 || len(res.Remember) != 1 || res.Tokens == 0 {
		t.Fatalf("%+v", res)
	}
	var n int
	tu.Must(t, w.d.QueryRow("SELECT (SELECT count(*) FROM nudges) + (SELECT count(*) FROM proactive_notes) + (SELECT count(*) FROM outbox)").Scan(&n))
	if n != 0 {
		t.Fatalf("a test run wrote %d rows", n)
	}
	bad := "Read @gone__x"
	if _, err := w.e.TestRun(context.Background(), w.agent.ID, &bad, NOW); err == nil || !strings.Contains(err.Error(), "@gone__x") {
		t.Fatalf("%v", err)
	}
}
