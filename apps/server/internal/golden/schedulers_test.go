package golden_test

// Goldens from testdata/gen/schedulers.ts (M4): the reminder parser's prompt
// and request, finishParse, parse errors, echoes, action replies, job presets,
// outbound texts and rows, and a scheduler trace across the New York DST change.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	gWITA = "Asia/Makassar"
	gNY   = "America/New_York"
	gMIN  = int64(60_000)
	gHOUR = int64(3600_000)
)

type schedGolden struct {
	Now     int64 `json:"now"`
	Prompts []struct {
		TZ     string `json:"tz"`
		Now    int64  `json:"now"`
		Prompt string `json:"prompt"`
	} `json:"prompts"`
	Wire []struct {
		Provider string          `json:"provider"`
		Model    string          `json:"model"`
		Mode     string          `json:"mode"`
		Phrase   string          `json:"phrase"`
		Reply    json.RawMessage `json:"reply"`
		Requests []struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    json.RawMessage   `json:"body"`
		} `json:"requests"`
		Result json.RawMessage `json:"result"`
	} `json:"wire"`
	Finish []struct {
		M        reminders.ModelReminder `json:"m"`
		Fallback string                  `json:"fallback"`
		TZ       string                  `json:"tz"`
		Now      int64                   `json:"now"`
		Result   json.RawMessage         `json:"result"`
	} `json:"finish"`
	ParseErrors []struct {
		Name   string          `json:"name"`
		Text   string          `json:"text"`
		TZ     string          `json:"tz"`
		Calls  int             `json:"calls"`
		Result json.RawMessage `json:"result"`
	} `json:"parseErrors"`
	Echoes struct {
		Reminders []struct {
			LocalStart       string  `json:"localStart"`
			TZ               string  `json:"tz"`
			RRule            string  `json:"rrule"`
			FireAt           int64   `json:"fireAt"`
			LocalStartStored string  `json:"localStartStored"`
			RRuleStored      *string `json:"rruleStored"`
			Echo             string  `json:"echo"`
			Next             string  `json:"next"`
		} `json:"reminders"`
		Messages []struct {
			R    db.Reminder `json:"r"`
			Now  int64       `json:"now"`
			Text string      `json:"text"`
		} `json:"messages"`
		Buttons []db.OutboxAction `json:"buttons"`
	} `json:"echoes"`
	Actions struct {
		Parsed []struct {
			Text   string                `json:"text"`
			Action *reminders.TextAction `json:"action"`
		} `json:"parsed"`
		Replies json.RawMessage `json:"replies"`
		Rows    json.RawMessage `json:"rows"`
	} `json:"actions"`
	Presets struct {
		HeartbeatRRule string          `json:"heartbeatRrule"`
		Jobs           json.RawMessage `json:"jobs"`
	} `json:"presets"`
	Outbound json.RawMessage `json:"outbound"`
	Trace    json.RawMessage `json:"trace"`
}

func loadSchedGolden(t *testing.T) schedGolden {
	t.Helper()
	b, err := os.ReadFile("../../testdata/schedulers.json")
	tu.Must(t, err)
	var g schedGolden
	tu.Must(t, json.Unmarshal(b, &g))
	return g
}

func canon(t *testing.T, raw []byte) string {
	t.Helper()
	v, err := jsonx.Parse(raw)
	tu.Must(t, err)
	return jsonx.Canonical(v)
}

// sameStored compares normalized Go output with the golden after parsing the
// stored JSON columns, so payload, settings value and event data compare by
// content (Go writes struct key order).
func sameStored(t *testing.T, name string, got any, want json.RawMessage) {
	t.Helper()
	norm := tu.NormalizeGolden(got)
	g, err := jsonx.Parse([]byte(norm))
	tu.Must(t, err)
	w, err := jsonx.Parse(want)
	tu.Must(t, err)
	gs := jsonx.Canonical(tu.ParseJSONColumns(g, "payload", "value", "data"))
	ws := jsonx.Canonical(tu.ParseJSONColumns(w, "payload", "value", "data"))
	if gs != ws {
		t.Errorf("%s differs from the TS golden; %s", name, tu.DiffAt(gs, ws))
	}
}

func TestGoldenParsePrompts(t *testing.T) {
	g := loadSchedGolden(t)
	for _, c := range g.Prompts {
		if got := reminders.BuildSystemPrompt(c.TZ, c.Now); got != c.Prompt {
			t.Errorf("%s %d: %s", c.TZ, c.Now, tu.DiffAt(got, c.Prompt))
		}
	}
	if len(g.Prompts) < 6 {
		t.Fatal(len(g.Prompts))
	}
}

// recModel records the call options and answers from a script.
type recModel struct {
	provider string
	reply    *models.ScriptedModel
	calls    []models.CallOptions
}

func (m *recModel) Provider() string { return m.provider }
func (m *recModel) ModelID() string  { return "m" }
func (m *recModel) Stream(ctx context.Context, o models.CallOptions) (models.Stream, error) {
	m.calls = append(m.calls, o)
	return m.reply.Stream(ctx, o)
}

func TestGoldenParseWire(t *testing.T) {
	g := loadSchedGolden(t)
	for _, c := range g.Wire {
		name := c.Provider + " " + c.Model + " " + c.Phrase
		step := models.Say(string(c.Reply))
		if c.Mode == "tool" {
			var in any
			tu.Must(t, json.Unmarshal(c.Reply, &in))
			step = models.CallTool(models.JSONToolName, in, "toolu_1")
		}
		rec := &recModel{provider: "anthropic.messages", reply: models.NewScripted(step)}
		res := reminders.ParseReminder(context.Background(), c.Phrase, gWITA, g.Now, rec, reminders.ParseOptions{})
		if got, want := jsonx.Canonical(mustParse(t, jsonx.Stringify(res))), canon(t, c.Result); got != want {
			t.Errorf("%s: result\n got %s\nwant %s", name, got, want)
		}
		if len(rec.calls) != len(c.Requests) {
			t.Fatalf("%s: %d calls", name, len(rec.calls))
		}
		var url string
		var headers map[string]string
		var body *jsonx.Object
		if c.Provider == "anthropic" {
			r, err := models.NewAnthropic(models.AnthropicConfig{BaseURL: "http://gateway.test:8317/v1", APIKey: "test-key"}, c.Model).BuildRequest(rec.calls[0])
			tu.Must(t, err)
			url, headers, body = r.URL, r.Headers, r.Body
		} else {
			r, err := models.NewOpenAI(models.OpenAIConfig{BaseURL: "http://gateway.test:8317/v1", APIKey: "test-key", Name: "cliproxy"}, c.Model).BuildRequest(rec.calls[0])
			tu.Must(t, err)
			url, headers, body = r.URL, r.Headers, r.Body
		}
		want := c.Requests[0]
		if url != want.URL {
			t.Errorf("%s: url %s", name, url)
		}
		for _, h := range []string{"anthropic-beta", "anthropic-version", "x-api-key", "authorization"} {
			if headers[h] != want.Headers[h] {
				t.Errorf("%s: header %s = %q, want %q", name, h, headers[h], want.Headers[h])
			}
		}
		// Go streams; TS generateText did not. The stream flag and the
		// streaming-only eager_input_streaming on the JSON tool are the only
		// differences allowed.
		body.Delete("stream")
		if tools, ok := body.Get("tools"); ok {
			for _, tl := range tools.([]any) {
				tl.(*jsonx.Object).Delete("eager_input_streaming")
			}
		}
		if got, w := jsonx.Canonical(mustParse(t, jsonx.Stringify(body))), canon(t, want.Body); got != w {
			t.Errorf("%s: body\n%s", name, tu.DiffAt(got, w))
		}
	}
	if len(g.Wire) != 9 {
		t.Fatal(len(g.Wire))
	}
}

func TestGoldenFinishParse(t *testing.T) {
	g := loadSchedGolden(t)
	for i, c := range g.Finish {
		res, err := reminders.FinishParse(c.M, c.Fallback, c.TZ, c.Now)
		tu.Must(t, err)
		if got, want := jsonx.Canonical(mustParse(t, jsonx.Stringify(res))), canon(t, c.Result); got != want {
			t.Errorf("%d %+v:\n got %s\nwant %s", i, c.M, got, want)
		}
	}
	if len(g.Finish) < 77 {
		t.Fatal(len(g.Finish))
	}
}

func TestGoldenParseErrors(t *testing.T) {
	g := loadSchedGolden(t)
	steps := map[string]models.Step{
		"not json":    models.Say("not json at all"),
		"wrong shape": models.Say(`{"text":"x"}`),
		"empty reply": {Events: []models.Event{{Type: models.EventFinish, FinishReason: models.FinishReason{Unified: "length"}}}},
		"http 400":    models.Fail(&models.APICallError{Message: "prompt is too long", StatusCode: 400}),
		"empty":       models.Say("{}"),
		"bad zone":    models.Say("{}"),
	}
	zero := 0
	for _, c := range g.ParseErrors {
		m := models.NewScripted(steps[c.Name])
		res := reminders.ParseReminder(context.Background(), c.Text, c.TZ, g.Now, m, reminders.ParseOptions{MaxRetries: &zero})
		if got, want := jsonx.Canonical(mustParse(t, jsonx.Stringify(res))), canon(t, c.Result); got != want {
			t.Errorf("%s:\n got %s\nwant %s", c.Name, got, want)
		}
		if len(m.Calls) != c.Calls {
			t.Errorf("%s: %d calls, want %d", c.Name, len(m.Calls), c.Calls)
		}
	}
}

func TestGoldenEchoes(t *testing.T) {
	g := loadSchedGolden(t)
	d := tu.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, g.Now)
	tu.Must(t, err)
	for _, c := range g.Echoes.Reminders {
		r, err := reminders.Create(d, reminders.CreateInput{ChatID: chat.ID, Text: "x", LocalStart: c.LocalStart, TZ: c.TZ, RRule: c.RRule}, g.Now)
		tu.Must(t, err)
		e, err := reminders.ReminderEcho(r, g.Now)
		tu.Must(t, err)
		if r.FireAt != c.FireAt || r.LocalStart != c.LocalStartStored || jsonx.Stringify(r.RRule) != jsonx.Stringify(c.RRuleStored) || e != c.Echo || reminders.ReminderNext(r, g.Now) != c.Next {
			t.Errorf("%+v: got %d %s %v %q %q", c, r.FireAt, r.LocalStart, r.RRule, e, reminders.ReminderNext(r, g.Now))
		}
	}
	for _, m := range g.Echoes.Messages {
		if got := reminders.ReminderMessage(m.R, m.Now); got != m.Text {
			t.Errorf("%+v: %q want %q", m.R, got, m.Text)
		}
	}
	if jsonx.Stringify(reminders.ReminderButtons("rem_x")) != jsonx.Stringify(g.Echoes.Buttons) {
		t.Error("buttons")
	}
}

func TestGoldenActions(t *testing.T) {
	g := loadSchedGolden(t)
	for _, p := range g.Actions.Parsed {
		if got := reminders.ParseActionText(p.Text); jsonx.Stringify(got) != jsonx.Stringify(p.Action) {
			t.Errorf("%q: %s want %s", p.Text, jsonx.Stringify(got), jsonx.Stringify(p.Action))
		}
	}
	now := g.Now
	d := tu.DB(t)
	chat, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, now)
	other, _ := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "628123@s.whatsapp.net"}, now)
	mk := func(text, local, tz, rr string) db.Reminder {
		r, err := reminders.Create(d, reminders.CreateInput{ChatID: chat.ID, Text: text, LocalStart: local, TZ: tz, RRule: rr}, now)
		tu.Must(t, err)
		return r
	}
	a := mk("a", "2026-11-10T10:00", gWITA, "")
	s := mk("s", "2026-11-16T08:00", gWITA, "FREQ=WEEKLY;BYDAY=MO")
	ny := mk("ny", "2026-11-10T22:00", gNY, "")
	reminders.NewScheduler(reminders.SchedulerOptions{DB: d, Clock: func() int64 { return now }, Timers: tu.NewFakeClock(now)}).Sweep()
	_, err := d.Exec("UPDATE outbox SET status = 'sent', channel_msg_id = 'M1' WHERE source = ?", "reminder:"+a.ID)
	tu.Must(t, err)
	acts := reminders.Actions{}
	cb := func(data, chatID string, at int64) func() (string, bool, error) {
		return func() (string, bool, error) { return acts.FromCallback(d, data, chatID, at) }
	}
	tx := func(text, quote string) func() (string, bool, error) {
		return func() (string, bool, error) { return acts.FromText(d, text, quote, chat.ID, now) }
	}
	steps := []struct {
		name string
		fn   func() (string, bool, error)
	}{
		{"cb snooze 10m", cb("rem:snooze:"+a.ID+":10m", chat.ID, now)},
		{"cb snooze 1h", cb("rem:snooze:"+a.ID+":1h", chat.ID, now)},
		{"cb snooze tomorrow", cb("rem:snooze:"+a.ID+":tomorrow", chat.ID, now)},
		{"cb done", cb("rem:done:"+a.ID, chat.ID, now)},
		{"cb done series", cb("rem:done:"+s.ID, chat.ID, now)},
		{"cb snooze series 1h", cb("rem:snooze:"+s.ID+":1h", chat.ID, now)},
		{"cb done snoozed series", cb("rem:done:"+s.ID, chat.ID, now+gMIN)},
		{"cb snooze ny tomorrow", cb("rem:snooze:"+ny.ID+":tomorrow", chat.ID, now)},
		{"cb other chat", cb("rem:done:"+s.ID, other.ID, now)},
		{"cb bad preset", cb("rem:snooze:"+s.ID+":2d", chat.ID, now)},
		{"cb snooze without preset", cb("rem:snooze:"+s.ID, chat.ID, now)},
		{"cb missing", cb("rem:done:rem_missing", chat.ID, now)},
		{"cb other prefix", cb("approve:x", chat.ID, now)},
		{"text tunda besok", tx("tunda besok", "M1")},
		{"text done", tx("udah ya", "M1")},
		{"text no quote", tx("done", "")},
		{"text unknown quote", tx("done", "M404")},
		{"text other", tx("makasih", "M1")},
	}
	var replies []any
	for _, st := range steps {
		reply, ok, err := st.fn()
		tu.Must(t, err)
		var r any
		if ok {
			r = reply
		}
		o := jsonx.NewMap[any]()
		o.Set("name", st.name)
		o.Set("reply", r)
		replies = append(replies, o)
	}
	_, err = reminders.MarkDone(d, ny.ID, now)
	tu.Must(t, err)
	_, err = reminders.SnoozeReminder(d, ny.ID, reminders.Snooze{Until: now + 5*gMIN}, now)
	tu.Must(t, err)
	sameStored(t, "action replies", replies, g.Actions.Replies)
	sameStored(t, "reminder rows", tu.TableRows(t, d, "reminders", ""), g.Actions.Rows)
}

func TestGoldenPresets(t *testing.T) {
	g := loadSchedGolden(t)
	if jobs.HeartbeatRRule != g.Presets.HeartbeatRRule {
		t.Fatal(jobs.HeartbeatRRule)
	}
	d := tu.DB(t)
	chat, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, g.Now)
	var out []any
	for _, tz := range []string{gWITA, gNY, "Asia/Tokyo"} {
		for _, make := range []func(string, string, int64) jobs.CreateInput{jobs.DailyBriefing, jobs.Heartbeat} {
			in := make(chat.ID, tz, g.Now)
			j, err := jobs.Create(d, in, g.Now)
			tu.Must(t, err)
			sched, err := jobs.DescribeSchedule(j, g.Now)
			tu.Must(t, err)
			next, _ := jobs.DescribeNextRun(j, g.Now)
			o := jsonx.NewMap[any]()
			o.Set("tz", tz)
			o.Set("input", mustParse(t, jsonx.Stringify(in)))
			o.Set("nextRunAt", *j.NextRunAt)
			o.Set("schedule", sched)
			o.Set("next", next)
			out = append(out, o)
		}
	}
	sameStored(t, "presets", out, g.Presets.Jobs)
}

func TestGoldenOutbound(t *testing.T) {
	g := loadSchedGolden(t)
	now := g.Now
	d := tu.DB(t)
	owner, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, now)
	oid := owner.ID
	meta := outbound.SendMeta{Origin: "chat", ApprovedBy: "owner", OwnerChatID: &oid}
	must := func(err error) { t.Helper(); tu.Must(t, err) }
	zaki, err := outbound.AddContact(d, outbound.AddContactInput{AgentID: "agt_test", Name: "Zaki", Phone: "082200001001"}, now)
	must(err)
	dm, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "777", Title: "Budi"}, now)
	budi, err := outbound.AddContact(d, outbound.AddContactInput{AgentID: "agt_test", Name: "Budi", ChatID: dm.ID}, now)
	must(err)
	labels := []any{outbound.ContactLabel(zaki.Name, zaki.Channel, zaki.Address), outbound.ContactLabel(budi.Name, budi.Channel, budi.Address), outbound.ContactLabel("X", "whatsapp", "14155550100")}
	var phones []any
	for _, p := range []string{"082200001001", "0822 0000-1001", "+62 822 0000 1001", "+62 0822 0000 1001", "82200001001", "+1 415 555 0100", "0065 9123 4567", "Zaki", "0812", "14155550100", "62 812"} {
		var n any
		if v := outbound.NormalizePhone(p); v != "" {
			n = v
		}
		phones = append(phones, obj("raw", p, "normalized", n))
	}
	var formatted []any
	for _, dg := range []string{"6282200001001", "14155550100", "621234567"} {
		formatted = append(formatted, obj("digits", dg, "formatted", outbound.FormatPhone(dg)))
	}
	found := outbound.PhonesIn("chat Zaki (082200001001) atau +62 811-1111-1111, bukan 12345 ya")
	held, err := outbound.SendNow(d, zaki, "Halo, ini asisten Zaki. Dia lagi otw.", meta, 15_000, now)
	must(err)
	must(outbound.HoldNotice(d, held.Row, zaki, held.SendAt, now))
	instant, err := outbound.SendNow(d, budi, "otw", meta, 0, now)
	must(err)
	must(outbound.HoldNotice(d, instant.Row, budi, instant.SendAt, now))
	cancelled, _ := outbound.Cancel(d, held.Row.ID, now+1000)
	again, _ := outbound.Cancel(d, held.Row.ID, now+2000)
	_, err = d.Exec("UPDATE outbox SET status = 'sent' WHERE id = ?", instant.OutboxID)
	must(err)
	late, _ := outbound.Cancel(d, instant.Row.ID, now+2000)
	missing, _ := outbound.Cancel(d, "obm_missing", now)
	failedBox, err := outbox.Enqueue(d, outbox.EnqueueInput{ChatID: zaki.ChatID, Channel: "whatsapp", Payload: db.OutboxPayload{Text: "x"}, Source: "outbound:" + held.Row.ID}, now)
	must(err)
	failedBox.Error = tu.Ptr(strings.Repeat("x", 200))
	must(outbound.OutboundFailed(d, failedBox, now))
	failedBox.Error = nil
	must(outbound.OutboundFailed(d, failedBox, now))
	def := settings.Defaults().Outbound
	o2 := def
	o2.PerContactPerDay = 2
	must(settings.Set(d, "outbound", o2, now))
	ext := outbound.NewScheduler(d, logx.Silent)
	over, err := outbound.ScheduleSend(d, zaki, "malam", outbound.ScheduleInput{LocalStart: "2026-11-10T20:00", TZ: gWITA}, meta, now)
	must(err)
	_, err = ext.Sweep(over.FireAt)
	must(err)
	must(settings.Set(d, "outbound", def, now))
	lateSend, err := outbound.ScheduleSend(d, budi, "late", outbound.ScheduleInput{LocalStart: "2026-11-11T20:00", TZ: gWITA}, meta, now)
	must(err)
	_, err = ext.Sweep(lateSend.FireAt + 31*gMIN)
	must(err)
	gone, err := outbound.ScheduleSend(d, budi, "gone", outbound.ScheduleInput{LocalStart: "2026-11-12T20:00", TZ: gWITA, RRule: "FREQ=DAILY"}, meta, now)
	must(err)
	_, err = d.Exec("DELETE FROM chats WHERE id = ?", dm.ID)
	must(err)
	_, err = ext.Sweep(gone.FireAt)
	must(err)
	var texts []any
	for _, tx := range []string{"otw", "otw\n\n" + def.Signature, "otw\n_dikirim otomatis oleh asisten ai zaki. balasan ke nomor ini tidak dibaca, silakan hubungi zaki langsung_ ", "Balasan ke nomor ini tidak dibaca?", "  spaced  \n\n"} {
		f, err := outbound.FinalText(d, tx, false)
		must(err)
		u, err := outbound.FinalText(d, tx, true)
		must(err)
		texts = append(texts, obj("text", tx, "final", f, "unsigned", u))
	}
	var errs []any
	for _, tx := range []string{"", strings.Repeat("a", 1990)} {
		if _, err := outbound.FinalText(d, tx, false); err != nil {
			errs = append(errs, err.Error())
		}
	}
	patch := outbound.SettingsPatch{PerContactPerDay: def.PerContactPerDay, PerDay: def.PerDay, HoldSeconds: def.HoldSeconds, SignatureOn: def.SignatureOn, Signature: "\u2014\n_Asisten Zaki_"}
	_, err = outbound.SaveSettings(d, patch, now)
	must(err)
	patch.Signature = "(dikirim asisten Zaki)\r\n"
	_, err = outbound.SaveSettings(d, patch, now+1)
	must(err)
	got := obj(
		"labels", labels, "phones", phones, "formatted", formatted, "found", anys(found), "texts", texts, "errors", errs,
		"cancel", obj("cancelled", cancelled.Message, "again", again.Message, "late", late.Message, "missing", missing.Message),
		"outbox", tu.TableRows(t, d, "outbox", ""), "outboundMessages", tu.TableRows(t, d, "outbound_messages", ""),
		"events", tu.Without(tu.TableRows(t, d, "events", ""), "id"), "settings", tu.TableRows(t, d, "settings", "WHERE key = 'outbound'"),
	)
	sameStored(t, "outbound", got, g.Outbound)
}

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Parse([]byte(s))
	tu.Must(t, err)
	return v
}

func anys(ss []string) []any {
	out := []any{}
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func TestGoldenSchedulerTrace(t *testing.T) {
	g := loadSchedGolden(t)
	d := tu.DB(t)
	// Saturday 2026-10-31 12:00 in New York (EDT). DST ends Sunday 02:00.
	start := int64(1793462400000)
	must := func(err error) { t.Helper(); tu.Must(t, err) }
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, start)
	must(err)
	must(settings.Set(d, "timezone", gNY, start))
	for _, in := range []reminders.CreateInput{
		{Text: "minum obat", LocalStart: "2026-10-31T01:30", RRule: "FREQ=DAILY"},
		{Text: "sekali", LocalStart: "2026-11-01T01:30"},
		{Text: "jalan pagi", LocalStart: "2026-11-01T08:00", RRule: "FREQ=DAILY"},
		{Text: "cek cuaca", LocalStart: "2026-11-01T07:00", SmartPrompt: tu.Ptr("Cek cuaca New York")},
		{Text: "gagal pintar", LocalStart: "2026-11-01T09:15", SmartPrompt: tu.Ptr("fail")},
		{Text: "tiap 6 jam", LocalStart: "2026-10-31T18:00", RRule: "FREQ=HOURLY;INTERVAL=6"},
	} {
		in.ChatID, in.TZ = chat.ID, gNY
		_, err := reminders.Create(d, in, start)
		must(err)
	}
	_, err = jobs.Create(d, jobs.CreateInput{Name: "Pagi", RRule: "FREQ=DAILY", TZ: gNY, LocalStart: "2026-10-31T06:00", Prompt: "briefing", ChatID: chat.ID, DailyTokenCap: tu.Ptr(int64(150))}, start)
	must(err)
	_, err = jobs.Create(d, jobs.CreateInput{Name: "Malam", RRule: "FREQ=DAILY;BYHOUR=1,23;BYMINUTE=30", TZ: gNY, LocalStart: "2026-10-31T01:30", Prompt: "cek", ChatID: chat.ID, ModelRole: "fast"}, start)
	must(err)
	zaki, err := outbound.AddContact(d, outbound.AddContactInput{AgentID: "agt_test", Name: "Zaki", Phone: "082200001001"}, start)
	must(err)
	oid := chat.ID
	_, err = outbound.ScheduleSend(d, zaki, "kabar pagi", outbound.ScheduleInput{LocalStart: "2026-11-01T09:00", TZ: gNY, RRule: "FREQ=DAILY"}, outbound.SendMeta{Origin: "chat", ApprovedBy: "owner", OwnerChatID: &oid}, start)
	must(err)

	clock := tu.NewFakeClock(start)
	var mu sync.Mutex
	smartCalls, turns := []any{}, []any{}
	runner := jobs.NewRunner(jobs.RunnerOptions{DB: d, Clock: clock.Now, RunTurn: func(_ context.Context, r jobs.TurnRequest) (jobs.TurnResponse, error) {
		mu.Lock()
		o := mustParse(t, jsonx.Stringify(r)).(*jsonx.Object)
		o.Set("at", clock.Now())
		turns = append(turns, o)
		mu.Unlock()
		id := secret.NewID("run")
		_, err := d.Exec("INSERT INTO runs (id, chat_id, job_id, trigger, tokens_in, tokens_out, tokens_cached, status, started_at) VALUES (?, ?, ?, 'schedule', 100, 20, 0, 'ok', ?)", id, r.ChatID, r.JobID, clock.Now())
		text := "ok " + r.Prompt
		if r.Prompt == "cek" {
			text = "NOTHING"
		}
		return jobs.TurnResponse{RunID: id, Text: text, Status: "ok"}, err
	}})
	s := reminders.NewScheduler(reminders.SchedulerOptions{
		DB: d, Clock: clock.Now, Timers: clock,
		OnSmartReminder: func(r db.Reminder) error {
			mu.Lock()
			smartCalls = append(smartCalls, obj("id", r.ID, "at", clock.Now(), "prompt", *r.SmartPrompt))
			mu.Unlock()
			if *r.SmartPrompt == "fail" {
				return os.ErrInvalid
			}
			return nil
		},
		OnJobDue:   func(j db.Job) error { runner.Run(j); return nil },
		Extensions: []reminders.Extension{outbound.NewScheduler(d, logx.Silent)},
	})
	steps := []any{}
	snap := func(label string, ran int) {
		var armed any
		if a := clock.LastArmed(); a >= 0 {
			armed = a
		}
		steps = append(steps, obj("label", label, "now", clock.Now(), "ran", ran, "armed", armed,
			"reminders", tu.TableRows(t, d, "reminders", ""), "jobs", tu.TableRows(t, d, "jobs", ""), "outbound", tu.TableRows(t, d, "outbound_messages", ""),
			"outbox", tu.TableRows(t, d, "outbox", ""), "usage", tu.TableRows(t, d, "usage", ""), "events", tu.Without(tu.TableRows(t, d, "events", ""), "id")))
	}
	must(s.Start())
	s.Idle()
	snap("start", 0)
	for _, p := range []struct {
		label string
		ms    int64
	}{{"+6h", 6 * gHOUR}, {"+4h", 4 * gHOUR}, {"+3h", 3 * gHOUR}, {"+50m", 50 * gMIN}, {"+1h", gHOUR}, {"+2h", 2 * gHOUR}, {"+6h", 6 * gHOUR}, {"+12h", 12 * gHOUR}, {"+24h", 24 * gHOUR}} {
		ran := clock.Advance(p.ms, s.Idle)
		snap(p.label, ran)
	}
	s.Stop()
	sameStored(t, "trace", obj("start", start, "steps", steps, "smartCalls", smartCalls, "turns", turns, "pending", clock.Pending()), g.Trace)
}
