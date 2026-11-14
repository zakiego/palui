package flows_test

// checkin.test.ts

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

const MEDICINE = `{
  "name": "Minum obat",
  "triggers": [{ "id": "main", "kind": "schedule", "start": "ask", "source": { "kind": "times", "times": [{ "time": "08:00", "label": "pagi" }, { "time": "20:00", "label": "malam" }] } }],
  "steps": [
    {
      "id": "ask",
      "kind": "checkin",
      "text": "💊 Obat {{trigger.label}} udah diminum?",
      "answers": [
        { "id": "taken", "label": "Udah minum", "words": ["udah", "sudah"], "closes": true, "reply": "Mantap, obat {{trigger.label}} tercatat 👍" },
        { "id": "skip", "label": "Skip", "closes": true },
        { "id": "later", "label": "Nanti", "closes": false }
      ],
      "followUp": { "everyMin": 15, "max": 2, "text": "Pengingat ke-{{run.followUps}}: obat {{trigger.label}} jangan lupa ya." },
      "deadline": { "kind": "after", "minutes": 120 },
      "missedText": "Obat {{trigger.label}} kelewat, dicatat ya.",
      "on": { "skip": "why" },
      "next": null
    },
    { "id": "why", "kind": "message", "text": "Oke, skip dulu." }
  ]
}`

func answer(t *testing.T, h *harness, runID, ans, text string, now int64) *flows.AnswerResult {
	t.Helper()
	r, err := flows.AnswerFlowCheckin(h.db, flows.AnswerInput{ChatID: h.chatID, RunID: runID, Answer: ans, Text: text}, now)
	tu.Must(t, err)
	return r
}

func waitDeadline(r db.FlowRun) int64 {
	v, _ := r.Wait.Get("deadlineAt")
	return int64(v.(float64))
}

func callback(t *testing.T, h *harness, data string, now int64) string {
	t.Helper()
	r, ok, err := flows.Actions{}.FromCallback(h.db, data, h.chatID, now)
	tu.Must(t, err)
	if !ok {
		t.Fatalf("callback %s not taken", data)
	}
	return r
}

func TestCheckinSteps(t *testing.T) {
	t.Run("asks with fl: buttons, re-asks, then logs a miss at the deadline", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-11T07:00"))
		flow := h.create(MEDICINE).Flow
		h.run(at("2026-11-11T08:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		if run.Status != "waiting" || i64(run.WakeAt) != at("2026-11-11T08:15") {
			t.Fatalf("run %s wake %d", run.Status, i64(run.WakeAt))
		}
		eq(t, sentTexts(t, h.db), []sentRow{{"flow:" + run.ID, "💊 Obat pagi udah diminum?", []string{"fl:" + run.ID + ":taken", "fl:" + run.ID + ":skip", "fl:" + run.ID + ":later"}}}, "ask")
		// A restart while it waits changes nothing.
		tu.Must(t, h.engine.Recover(at("2026-11-11T08:05")))
		h.run(at("2026-11-11T08:05"))
		eq(t, len(sentTexts(t, h.db)), 1, "sent")
		h.run(at("2026-11-11T08:15"))
		h.run(at("2026-11-11T08:30"))
		h.run(at("2026-11-11T08:45"))
		eq(t, texts(t, h.db), []string{"💊 Obat pagi udah diminum?", "Pengingat ke-1: obat pagi jangan lupa ya.", "Pengingat ke-2: obat pagi jangan lupa ya."}, "re-asks")
		eq(t, i64(getRun(t, h.db, run.ID).WakeAt), at("2026-11-11T10:00"), "wake")
		h.run(at("2026-11-11T10:00"))
		done := getRun(t, h.db, run.ID)
		eq(t, done.Status, "done", "status")
		ask, _ := stepsOf(done).Get("ask")
		if field(ask, "missed") != true || field(ask, "followUps") != 2.0 || field(ask, "answer") != nil {
			t.Errorf("ask %s", jsonx.Stringify(ask))
		}
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "Obat pagi kelewat, dicatat ya.", "missed text")
		eq(t, stepLog(t, h.db, run.ID), []logEntry{{"ask", "ok", "missed", "<nil>"}}, "log")
	})
	t.Run("takes a button answer, replies, and continues the run", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-11T07:00"))
		flow := h.create(MEDICINE).Flow
		h.run(at("2026-11-11T08:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		eq(t, callback(t, h, "fl:"+run.ID+":taken", at("2026-11-11T08:03")), "Mantap, obat pagi tercatat 👍", "reply")
		h.run(at("2026-11-11T08:03"))
		eq(t, getRun(t, h.db, run.ID).Status, "done", "status")
		eq(t, len(sentTexts(t, h.db)), 1, "sent")
		eq(t, callback(t, h, "fl:"+run.ID+":taken", at("2026-11-11T08:04")), "Oke, jawaban diganti: Udah minum.", "correction")
	})
	t.Run("branches on a closing answer, and a non-closing one re-asks from now", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-11T07:00"))
		flow := h.create(MEDICINE).Flow
		h.run(at("2026-11-11T08:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		later := answer(t, h, "", "nanti", "", at("2026-11-11T08:10"))
		eq(t, later.Reply, "Oke, dicatat: Nanti. Aku tanya lagi jam 08.25.", "reply")
		eq(t, i64(getRun(t, h.db, run.ID).WakeAt), at("2026-11-11T08:25"), "wake")
		answer(t, h, run.ID, "skip", "", at("2026-11-11T08:12"))
		h.run(at("2026-11-11T08:12"))
		eq(t, getRun(t, h.db, run.ID).Status, "done", "status")
		eq(t, stepsJoin(stepLog(t, h.db, run.ID), func(l logEntry) string { return l.Step + ":" + strings.Replace(l.Branch, "<nil>", "null", 1) }), "ask:on.skip,why:null", "log")
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "Oke, skip dulu.", "text")
	})
	t.Run("matches a quoted reply exactly and leaves anything else to the model", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-11T07:00"))
		flow := h.create(MEDICINE).Flow
		h.run(at("2026-11-11T08:00"))
		_, err := h.db.Exec("UPDATE outbox SET channel_msg_id = '777'")
		tu.Must(t, err)
		r, ok, err := flows.Actions{}.FromText(h.db, "udah tapi telat", "777", h.chatID, at("2026-11-11T08:05"))
		tu.Must(t, err)
		if ok {
			t.Errorf("took %q", r)
		}
		r, ok, err = flows.Actions{}.FromText(h.db, "Udah!", "777", h.chatID, at("2026-11-11T08:05"))
		tu.Must(t, err)
		if !ok || r != "Mantap, obat pagi tercatat 👍" {
			t.Errorf("reply %q %v", r, ok)
		}
		eq(t, runsOf(t, h.db, flow.ID)[0].Status, "queued", "status")
	})
	t.Run("lists open check-ins for the system prompt", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-11T07:00"))
		h.create(MEDICINE)
		h.run(at("2026-11-11T08:00"))
		open, err := flows.OpenFlowCheckins(h.db, h.chatID)
		tu.Must(t, err)
		p, err := flows.FlowCheckinsPrompt(h.db, h.chatID, at("2026-11-11T08:01"))
		tu.Must(t, err)
		contains(t, p, fmt.Sprintf(`- %s: flow "Minum obat", item "pagi", asked Rabu 11 Nov 08.00; answers: taken, skip, later (keeps asking)`, open[0].ID), "prompt")
	})
	t.Run("9e: deadline next is the next prayer time; the recap trigger reads the log", func(t *testing.T) {
		fp := &fakePrayer{}
		h := newHarness(t, harnessOptions{readSource: fp.caller(), connectors: exampleConnectors(map[string]runFn{"flows__recap": returns(`{"total":3,"text":"📊 Rekap"}`)})})
		flow := h.create(SHOLAT).Flow
		h.run(at("2026-11-10T10:00"))
		eq(t, fp.dates(), []string{"2026-11-10", "2026-11-11"}, "fetched dates")
		h.run(at("2026-11-10T12:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		if run.Status != "waiting" || run.RunKey != "2026-11-10:times.dhuhr" {
			t.Fatalf("run %s %s", run.Status, run.RunKey)
		}
		eq(t, texts(t, h.db)[0], "Udah sholat Zuhur (12.00)?", "ask")
		eq(t, waitDeadline(run), at("2026-11-10T15:25"), "deadline")
	})
	t.Run("a tool source is called with the flow agent's locked recall scope", func(t *testing.T) {
		fp := &fakePrayer{}
		var scopes []*tools.CallScope
		read := func(ctx context.Context, cid, tool string, args *jsonx.Object, scope *tools.CallScope) (any, error) {
			scopes = append(scopes, scope)
			return fp.caller()(ctx, cid, tool, args, scope)
		}
		h := newHarness(t, harnessOptions{readSource: read})
		h.create(SHOLAT)
		h.run(at("2026-11-10T10:00"))
		if len(scopes) == 0 {
			t.Fatal("no source calls")
		}
		a, err := agents.GetDefault(h.db, h.now())
		tu.Must(t, err)
		s := scopes[0]
		if s.AgentID != a.ID || s.Memory == nil || s.Memory.Container != nil || len(s.Memory.Read) != 0 || !s.Memory.Locked {
			t.Errorf("scope %+v memory %+v", s, s.Memory)
		}
	})
	t.Run("deadline next with plusMin stops 10 minutes before the next prayer", func(t *testing.T) {
		fp := &fakePrayer{}
		h := newHarness(t, harnessOptions{readSource: fp.caller()})
		def := strings.Replace(SHOLAT, `"deadline": { "kind": "next" }`, `"deadline": { "kind": "next", "plusMin": -10 }`, 1)
		flow := h.create(def).Flow
		h.run(at("2026-11-10T10:00"))
		h.run(at("2026-11-10T12:00"))
		eq(t, waitDeadline(runsOf(t, h.db, flow.ID)[0]), at("2026-11-10T15:15"), "deadline")
	})
}

func computedAsk(deadline string) string {
	return fmt.Sprintf(`{"id":"ask","kind":"checkin","text":"Udah sholat?","answers":[{"id":"done","label":"Sudah","closes":true}],"followUp":{"everyMin":30,"max":5},"deadline":%s,"missedText":"Kelewat.","onMissed":"last","next":null}`, deadline)
}

const lastStep = `{"id":"last","kind":"message","text":"Pengingat terakhir","next":null}`

func TestComputedCheckinDeadlines(t *testing.T) {
	t.Run("at: a template time with plusMin, and re-asks stop before it", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Sholat","tz":"Asia/Makassar","steps":[` + computedAsk(`{"kind":"at","at":"{{trigger.args.next}}","plusMin":-10}`) + `,` + lastStep + `]}`).Flow
		t0 := at("2026-11-10T18:16")
		h.setNow(t0)
		h.start(flow.ID, "manual", "a", `{"args":{"next":"2026-11-10T19:29"}}`)
		h.run(t0)
		eq(t, waitDeadline(runsOf(t, h.db, flow.ID)[0]), at("2026-11-10T19:19"), "deadline")
		h.run(at("2026-11-10T18:46"))
		h.run(at("2026-11-10T19:16"))
		eq(t, texts(t, h.db), []string{"Udah sholat?", "🔁 Udah sholat?", "🔁 Udah sholat?"}, "asks")
		eq(t, i64(runsOf(t, h.db, flow.ID)[0].WakeAt), at("2026-11-10T19:19"), "wake")
		h.run(at("2026-11-10T19:19"))
		eq(t, texts(t, h.db)[3:], []string{"Kelewat.", "Pengingat terakhir"}, "missed")
	})
	t.Run("at: a time already past skips the ask silently and goes onMissed", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Sholat","tz":"Asia/Makassar","steps":[` + computedAsk(`{"kind":"at","at":"{{trigger.args.next}}","plusMin":-10}`) + `,` + lastStep + `]}`).Flow
		t0 := at("2026-11-10T19:25")
		h.setNow(t0)
		h.start(flow.ID, "manual", "a", `{"args":{"next":"2026-11-10T19:29"}}`)
		h.run(t0)
		run := runsOf(t, h.db, flow.ID)[0]
		eq(t, run.Status, "done", "status")
		ask, _ := stepsOf(&run).Get("ask")
		if field(ask, "missed") != true || field(ask, "skipped") != true {
			t.Errorf("ask %s", jsonx.Stringify(ask))
		}
		eq(t, texts(t, h.db), []string{"Pengingat terakhir"}, "texts")
	})
	t.Run("after: minutes from a template", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Sholat","steps":[` + computedAsk(`{"kind":"after","minutes":"{{trigger.args.left}}"}`) + `,` + lastStep + `]}`).Flow
		t0 := h.now()
		h.start(flow.ID, "manual", "a", `{"args":{"left":63}}`)
		h.run(t0)
		eq(t, waitDeadline(runsOf(t, h.db, flow.ID)[0]), t0+63*MIN, "deadline")
	})
	t.Run("a value that isn't a time fails the step through onError", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Sholat","steps":[` + computedAsk(`{"kind":"at","at":"{{trigger.args.next}}"}`) + `,` + lastStep + `]}`).Flow
		h.start(flow.ID, "manual", "a", `{"args":{"next":"besok"}}`)
		h.run(h.now())
		run := runsOf(t, h.db, flow.ID)[0]
		eq(t, run.Status, "failed", "status")
		contains(t, deref(run.Error), "deadline.at", "error")
		eq(t, texts(t, h.db), []string{`⚠️ Flow "Sholat" gagal: Step ask: deadline.at ("besok") isn't a date or time.`}, "notice")
	})
}

func TestWaitSteps(t *testing.T) {
	payload := `{"payload":{"action":"closed","pull_request":{"merged":true,"number":46,"title":"feat: flows","merge_commit_sha":"abc123","html_url":"https://github.com/zakiego/palui/pull/46"}}}`
	t.Run("sleeps in the row across a restart, then checks CI and reports", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-github__get_commit_status": returns(`{"state":"success"}`)})})
		flow := h.create(PR_MERGED).Flow
		t0 := h.now()
		gh := "gh"
		_, err := h.engine.StartRun(flows.StartInput{FlowID: flow.ID, TriggerID: &gh, TriggerKind: "webhook", RunKey: "d-1", Payload: jobj(payload), Taint: []string{"trigger"}, Now: t0})
		tu.Must(t, err)
		h.run(t0)
		run := runsOf(t, h.db, flow.ID)[0]
		if run.Status != "waiting" || i64(run.WakeAt) != t0+5*MIN {
			t.Fatalf("run %s wake %d", run.Status, i64(run.WakeAt))
		}
		tu.Must(t, h.engine.Recover(t0+MIN))
		h.run(t0 + MIN)
		eq(t, runsOf(t, h.db, flow.ID)[0].Status, "waiting", "still waiting")
		h.run(t0 + 5*MIN)
		run = runsOf(t, h.db, flow.ID)[0]
		eq(t, run.Status, "done", "status")
		eq(t, h.calls.of("get_commit_status")[0], js(`{"owner":"zakiego","repo":"palui","ref":"abc123"}`), "args")
		eq(t, texts(t, h.db), []string{`✅ PR #46 "feat: flows" merged, CI hijau.`}, "texts")
		eq(t, stepsJoin(stepLog(t, h.db, run.ID), func(l logEntry) string { return l.Step + ":" + l.Status }), "is_merge:ok,settle:ok,ci:ok,green:ok,ok:ok", "log")
	})
	t.Run("an until template wakes at that time; a past one continues at once", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Tunggu","steps":[{"id":"w","kind":"wait","until":"{{trigger.args.at}}","plusMin":10},{"id":"m","kind":"message","text":"bangun"}]}`).Flow
		h.start(flow.ID, "manual", "a", `{"args":{"at":"2026-11-10T12:00"}}`)
		h.start(flow.ID, "manual", "b", `{"args":{"at":"2026-11-01"}}`)
		h.run(h.now())
		for _, r := range runsOf(t, h.db, flow.ID) {
			switch r.RunKey {
			case "a":
				if r.Status != "waiting" || i64(r.WakeAt) != at("2026-11-10T12:10") {
					t.Errorf("a: %s %d", r.Status, i64(r.WakeAt))
				}
			case "b":
				eq(t, r.Status, "done", "b")
			}
		}
	})
}

const komentar = `{"name":"Komentar","steps":[{"id":"note","kind":"tool","tool":"notes__save_note","args":{"text":"{{trigger.args.text}}"}},{"id":"done","kind":"message","text":"Tersimpan."}]}`

func approvedFlow(t *testing.T, h *harness, def string) *db.Flow {
	t.Helper()
	saved := h.create(def)
	_, err := flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
	tu.Must(t, err)
	return saved.Flow
}

func pendingApprovals(t *testing.T, q db.Q) []db.Approval {
	t.Helper()
	rows, err := q.Query("SELECT " + db.ApprovalCols + " FROM pending_approvals ORDER BY rowid")
	list, err := db.All(rows, err, db.ScanApproval)
	tu.Must(t, err)
	return list
}

func TestPerRunApproval(t *testing.T) {
	t.Run("runs approved writes with clean args without asking", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := approvedFlow(t, h, komentar)
		h.start(flow.ID, "manual", "m1", `{"args":{"text":"halo"}}`)
		h.run(h.now())
		eq(t, h.calls.of("save_note"), []any{js(`{"text":"halo"}`)}, "calls")
		eq(t, len(pendingApprovals(t, h.db)), 0, "approvals")
	})
	t.Run("asks when an argument is tainted, then runs exactly what was approved", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := approvedFlow(t, h, komentar)
		run := h.start(flow.ID, "webhook", "w1", `{"args":{"text":"dari web"}}`, "trigger").Run
		h.run(h.now())
		eq(t, getRun(t, h.db, run.ID).Status, "waiting", "status")
		ap := pendingApprovals(t, h.db)[0]
		if ap.RunID != run.ID || ap.Tool != "notes__save_note" || ap.ToolClass != "write" || deref(ap.ArgsPreview) != `{"text":"dari web"}` {
			t.Errorf("approval %+v", ap)
		}
		sent := sentTexts(t, h.db)
		last := sent[len(sent)-1]
		if last.Source != "approval:"+ap.ID {
			t.Errorf("source %s", last.Source)
		}
		eq(t, last.Actions, []string{"approve:" + ap.ID, "deny:" + ap.ID}, "actions")
		contains(t, last.Text, "argumen dari data luar (text)", "reason")
		eq(t, len(h.calls.all()), 0, "calls")
		_, err := approvals.Decide(h.db, ap.ID, true, h.now())
		tu.Must(t, err)
		tu.Must(t, flows.WakeFlowRun(h.db, run.ID, h.now()))
		h.run(h.now())
		eq(t, getRun(t, h.db, run.ID).Status, "done", "status")
		eq(t, h.calls.of(""), []any{js(`{"text":"dari web"}`)}, "calls")
		eq(t, stepsJoin(stepLog(t, h.db, run.ID), func(l logEntry) string { return l.Step + ":" + l.Status }), "note:ok,done:ok", "log")
	})
	t.Run("an expired or denied ask fails the step and takes onError", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		def := strings.Replace(komentar, `"args":{"text":"{{trigger.args.text}}"}}`, `"args":{"text":"{{trigger.args.text}}"},"onError":"continue"}`, 1)
		flow := approvedFlow(t, h, def)
		run := h.start(flow.ID, "webhook", "w1", `{"args":{"text":"x"}}`, "trigger").Run
		h.run(h.now())
		later := h.now() + 11*MIN
		ids, err := approvals.ExpireDue(h.db, later)
		tu.Must(t, err)
		for _, id := range ids {
			tu.Must(t, flows.WakeFlowRun(h.db, id, later))
		}
		h.run(later)
		eq(t, getRun(t, h.db, run.ID).Status, "done", "status")
		eq(t, stepLog(t, h.db, run.ID)[0], logEntry{"note", "error", "continue", "no answer from the owner within 10 minutes"}, "log")
		eq(t, len(h.calls.all()), 0, "calls")
	})
	t.Run("asks every run for a tool set to every_run", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := approvedFlow(t, h, `{"name":"Komen","steps":[{"id":"c","kind":"tool","tool":"mcp-github__add_comment","args":{"body":"ok"}}]}`)
		h.start(flow.ID, "manual", "m1", "")
		h.run(h.now())
		eq(t, pendingApprovals(t, h.db)[0].Tool, "mcp-github__add_comment", "tool")
		all := texts(t, h.db)
		contains(t, all[len(all)-1], "tool ini selalu minta izin", "reason")
	})
	t.Run("asks when a tool approved as read became write since", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		saved := h.create(`{"name":"Daftar","steps":[{"id":"r","kind":"tool","tool":"reminders__list_reminders"}]}`)
		if saved.NeedsApproval {
			t.Fatal("read needs approval")
		}
		conns := exampleConnectors(nil)
		for _, c := range conns {
			if c.id == "reminders" {
				c.tools["list_reminders"].class = "write"
			}
		}
		h2 := newHarness(t, harnessOptions{connectors: conns})
		saved2 := h2.create(`{"name":"Daftar","steps":[{"id":"r","kind":"tool","tool":"reminders__list_reminders"}]}`)
		h2.start(saved2.Flow.ID, "manual", "m1", "")
		h2.run(h2.now())
		all := texts(t, h2.db)
		contains(t, all[len(all)-1], "kelas tool berubah sejak disetujui", "reason")
		n := 0
		for _, a := range pendingApprovals(t, h2.db) {
			if a.Tool == "reminders__list_reminders" {
				n++
			}
		}
		eq(t, n, 1, "approvals")
	})
}

// The owner's full rule: ask when the prayer starts, remind at +10, +30, +70
// and +150 minutes, skip a reminder within 10 minutes of the last one, the
// last one always 10 minutes before the next prayer, and close 1 minute
// before the next prayer.
func sholatGrowing() string {
	answers := `[{"id":"done","label":"Sudah","words":["udah","sudah"],"closes":true,"reply":"Alhamdulillah 🤲"},{"id":"not_yet","label":"Belum","words":["belum"],"closes":false}]`
	ask := func(id, text, until, onMissed string) string {
		return fmt.Sprintf(`{"id":%q,"kind":"checkin","text":%q,"answers":%s,"followUp":{"everyMin":5,"max":0},"deadline":{"kind":"at","at":"{{%s}}"},"onMissed":%q,"next":null}`, id, text, answers, until, onMissed)
	}
	at := func(min int) string {
		return fmt.Sprintf("choose(trigger.at + %d * 60000 <= vars.last - 10 * 60000, trigger.at + %d * 60000, vars.last)", min, min)
	}
	return fmt.Sprintf(`{
  "name": "Sholat 5 waktu",
  "tz": "Asia/Makassar",
  "triggers": [{ "id": "main", "kind": "schedule", "start": "plan", "lateGraceMin": 60, "source": %s }],
  "steps": [
    { "id": "plan", "kind": "set", "values": { "last": "trigger.nextAt - 10 * 60000" } },
    { "id": "plan_times", "kind": "set", "values": { "r1": %q, "r2": %q, "r3": %q, "r4": %q } },
    %s, %s, %s, %s, %s,
    { "id": "final", "kind": "checkin", "text": "⏰ Terakhir: udah sholat {{trigger.label}}? 10 menit lagi masuk waktu berikutnya", "answers": %s, "followUp": { "everyMin": 5, "max": 0 }, "deadline": { "kind": "next", "plusMin": -1 }, "missedText": "{{trigger.label}} kelewat, dicatat ya", "next": null }
  ]
}`, prayerSourceJSON, at(10), at(30), at(70), at(150),
		ask("ask_0", "🕌 Waktu {{trigger.label}} masuk ({{trigger.time}}). Udah sholat?", "vars.r1", "ask_1"),
		ask("ask_1", "Udah sholat {{trigger.label}}?", "vars.r2", "ask_2"),
		ask("ask_2", "Udah sholat {{trigger.label}}?", "vars.r3", "ask_3"),
		ask("ask_3", "Udah sholat {{trigger.label}}?", "vars.r4", "ask_4"),
		ask("ask_4", "Udah sholat {{trigger.label}}?", "vars.last", "final"),
		answers)
}

func findRun(runs []db.FlowRun, suffix string) *db.FlowRun {
	for i := range runs {
		if strings.HasSuffix(runs[i].RunKey, suffix) {
			return &runs[i]
		}
	}
	return nil
}

func TestGrowingReminders(t *testing.T) {
	t.Run("Zuhur: every reminder fits, the last one at 15.15, closed at 15.24", func(t *testing.T) {
		fp := &fakePrayer{}
		h := newHarness(t, harnessOptions{readSource: fp.caller()})
		flow := h.create(sholatGrowing()).Flow
		h.run(at("2026-11-10T10:00"))
		for _, tm := range []string{"12:00", "12:10", "12:30", "13:10", "14:30", "15:15"} {
			h.run(at("2026-11-10T" + tm))
		}
		eq(t, texts(t, h.db), []string{
			"🕌 Waktu Zuhur masuk (12.00). Udah sholat?",
			"Udah sholat Zuhur?", "Udah sholat Zuhur?", "Udah sholat Zuhur?", "Udah sholat Zuhur?",
			"⏰ Terakhir: udah sholat Zuhur? 10 menit lagi masuk waktu berikutnya",
		}, "texts")
		eq(t, waitDeadline(*findRun(runsOf(t, h.db, flow.ID), "dhuhr")), at("2026-11-10T15:24"), "deadline")
		h.run(at("2026-11-10T15:24"))
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "Zuhur kelewat, dicatat ya", "missed")
	})
	t.Run("Magrib: reminders past 19.03 are skipped, the last one lands at 19.13", func(t *testing.T) {
		fp := &fakePrayer{}
		h := newHarness(t, harnessOptions{readSource: fp.caller()})
		h.create(sholatGrowing())
		h.run(at("2026-11-10T17:00"))
		for _, tm := range []string{"18:11", "18:21", "18:41", "19:13"} {
			h.run(at("2026-11-10T" + tm))
		}
		eq(t, texts(t, h.db), []string{
			"🕌 Waktu Magrib masuk (18.11). Udah sholat?",
			"Udah sholat Magrib?", "Udah sholat Magrib?",
			"⏰ Terakhir: udah sholat Magrib? 10 menit lagi masuk waktu berikutnya",
		}, "texts")
	})
	t.Run("Isya: the last reminder is 10 minutes before Subuh tomorrow", func(t *testing.T) {
		fp := &fakePrayer{}
		h := newHarness(t, harnessOptions{readSource: fp.caller()})
		flow := h.create(sholatGrowing()).Flow
		h.run(at("2026-11-10T19:00"))
		for _, tm := range []string{"2026-11-10T19:23", "2026-11-10T19:33", "2026-11-10T19:53", "2026-11-10T20:33", "2026-11-10T21:53", "2026-11-11T04:25"} {
			h.run(at(tm))
		}
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "⏰ Terakhir: udah sholat Isya? 10 menit lagi masuk waktu berikutnya", "last")
		n := 0
		for _, s := range all {
			if strings.Contains(s, "Isya") {
				n++
			}
		}
		eq(t, n, 6, "Isya texts")
		eq(t, waitDeadline(*findRun(runsOf(t, h.db, flow.ID), "isha")), at("2026-11-11T04:34"), "deadline")
	})
	t.Run("an answer stops the chain", func(t *testing.T) {
		fp := &fakePrayer{}
		h := newHarness(t, harnessOptions{readSource: fp.caller()})
		flow := h.create(sholatGrowing()).Flow
		h.run(at("2026-11-10T11:00"))
		h.run(at("2026-11-10T12:00"))
		run := findRun(runsOf(t, h.db, flow.ID), "dhuhr")
		answer(t, h, run.ID, "done", "", at("2026-11-10T12:05"))
		h.run(at("2026-11-10T15:20"))
		eq(t, texts(t, h.db), []string{"🕌 Waktu Zuhur masuk (12.00). Udah sholat?"}, "texts")
		eq(t, getRun(t, h.db, run.ID).Status, "done", "status")
	})
}

const PRAYER_CHAIN = `{
  "name": "Sholat",
  "tz": "Asia/Makassar",
  "triggers": [{ "id": "main", "kind": "schedule", "start": "info", "source": { "kind": "times", "times": [{ "time": "15:30", "label": "Ashar" }] } }],
  "steps": [
    { "id": "info", "kind": "message", "text": "🕌 Waktu {{trigger.label}} sudah masuk", "next": "wait1" },
    { "id": "wait1", "kind": "wait", "minutes": 10, "next": "ask1" },
    {
      "id": "ask1", "kind": "checkin", "text": "Udah sholat {{trigger.label}}?",
      "answers": [{ "id": "done", "label": "Sudah", "words": ["udah", "sudah"], "closes": true }, { "id": "not_yet", "label": "Belum", "words": ["belum"], "closes": false }],
      "followUp": { "everyMin": 5, "max": 0 }, "deadline": { "kind": "after", "minutes": 20 }, "on": { "done": "alhamdulillah" }, "onMissed": "ask2", "next": null
    },
    {
      "id": "ask2", "kind": "checkin", "text": "Pengingat: udah sholat {{trigger.label}}?",
      "answers": [{ "id": "done", "label": "Sudah", "words": ["udah", "sudah"], "closes": true }, { "id": "not_yet", "label": "Belum", "words": ["belum"], "closes": false }],
      "followUp": { "everyMin": 5, "max": 0 }, "deadline": { "kind": "after", "minutes": 30 }, "on": { "done": "alhamdulillah" }, "next": null
    },
    { "id": "alhamdulillah", "kind": "message", "text": "Alhamdulillah 🤲", "next": null }
  ]
}`

func TestEarlyCheckinAnswers(t *testing.T) {
	t.Run("keeps an answer given during the wait and skips the question", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		flow := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:30"))
		run := runsOf(t, h.db, flow.ID)[0]
		if run.Status != "waiting" || deref(run.Cursor) != "wait1" {
			t.Fatalf("run %s %s", run.Status, deref(run.Cursor))
		}
		res := answer(t, h, "", "udah", "Udah ashar", at("2026-11-12T15:33"))
		if !res.Closed || !res.Early || res.Reply != "Sip, dicatat duluan: Sudah (Ashar). Pertanyaannya nanti dilewati." || res.Run.ID != run.ID {
			t.Errorf("res %+v", res)
		}
		h.run(at("2026-11-12T15:40"))
		done := getRun(t, h.db, run.ID)
		eq(t, done.Status, "done", "status")
		eq(t, texts(t, h.db), []string{"🕌 Waktu Ashar sudah masuk", "Alhamdulillah 🤲"}, "texts")
		ask1, _ := stepsOf(done).Get("ask1")
		if field(ask1, "answer") != "done" || field(ask1, "label") != "Sudah" || field(ask1, "text") != "Udah ashar" || field(ask1, "answeredAt") != float64(at("2026-11-12T15:33")) || field(ask1, "missed") != false || field(ask1, "early") != true {
			t.Errorf("ask1 %s", jsonx.Stringify(ask1))
		}
		if _, has := done.Vars.Get("preAnswer"); has {
			t.Error("preAnswer kept")
		}
		eq(t, stepsJoin(stepLog(t, h.db, run.ID), func(l logEntry) string { return l.Step + ":" + l.Branch }), "info:<nil>,wait1:<nil>,ask1:on.done,alhamdulillah:<nil>", "log")
		s := flows.SummarizeRuns([]db.FlowRun{*done})
		if v, _ := s.ByAnswer.Get("done"); v != 1 || s.Missed != 0 {
			t.Errorf("summary %+v", s)
		}
	})
	t.Run("works with the run id too, and a newer early answer replaces the older one", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		flow := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:30"))
		run := runsOf(t, h.db, flow.ID)[0]
		answer(t, h, run.ID, "done", "", at("2026-11-12T15:33"))
		answer(t, h, run.ID, "Sudah", "Aku udah sholat ashar", at("2026-11-12T15:35"))
		pre, _ := getRun(t, h.db, run.ID).Vars.Get("preAnswer")
		eq(t, pre, js(fmt.Sprintf(`{"stepId":"ask1","answer":"done","label":"Sudah","text":"Aku udah sholat ashar","at":%d}`, at("2026-11-12T15:35"))), "preAnswer")
		h.run(at("2026-11-12T15:40"))
		ask1, _ := stepsOf(getRun(t, h.db, run.ID)).Get("ask1")
		eq(t, field(ask1, "answeredAt"), float64(at("2026-11-12T15:35")), "answeredAt")
	})
	t.Run("refuses a non-closing answer before the question and still asks", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		flow := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:30"))
		_, err := flows.AnswerFlowCheckin(h.db, flows.AnswerInput{ChatID: h.chatID, Answer: "belum"}, at("2026-11-12T15:33"))
		if err == nil || !strings.Contains(err.Error(), "only a closing answer can be recorded early") {
			t.Errorf("err %v", err)
		}
		h.run(at("2026-11-12T15:40"))
		eq(t, runsOf(t, h.db, flow.ID)[0].Status, "waiting", "status")
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "Udah sholat Ashar?", "ask")
	})
	t.Run("prefers an open check-in over a run that asks later, and lists both for the prompt", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		med := strings.Replace(MEDICINE, `"times": [{ "time": "08:00", "label": "pagi" }, { "time": "20:00", "label": "malam" }]`, `"times": [{ "time": "15:20", "label": "sore" }]`, 1)
		medicine := h.create(med).Flow
		sholat := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:20"))
		h.run(at("2026-11-12T15:30"))
		open := runsOf(t, h.db, medicine.ID)[0]
		waiting := runsOf(t, h.db, sholat.ID)[0]
		p, err := flows.FlowCheckinsPrompt(h.db, h.chatID, at("2026-11-12T15:31"))
		tu.Must(t, err)
		contains(t, p, fmt.Sprintf(`- %s: flow "Minum obat", item "sore", asked`, open.ID), "open")
		contains(t, p, fmt.Sprintf(`- %s: flow "Sholat", item "Ashar", not asked yet, next step Kamis 12 Nov 15.40; answers: done`, waiting.ID), "upcoming")
		eq(t, answer(t, h, "", "udah", "", at("2026-11-12T15:31")).Run.ID, open.ID, "answered run")
	})
	t.Run("a run past its check-ins only takes corrections", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		flow := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:30"))
		h.run(at("2026-11-12T15:40"))
		run := runsOf(t, h.db, flow.ID)[0]
		answer(t, h, "", "udah", "", at("2026-11-12T15:41"))
		h.run(at("2026-11-12T15:41"))
		eq(t, getRun(t, h.db, run.ID).Status, "done", "status")
		eq(t, answer(t, h, run.ID, "done", "", at("2026-11-12T15:50")).Reply, "Oke, jawaban diganti: Sudah.", "correction")
	})
}

func TestDecidingCheckin(t *testing.T) {
	t.Run("counts an answer on a later reminder as answered", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		flow := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:30"))
		h.run(at("2026-11-12T15:40"))
		h.run(at("2026-11-12T16:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		if w := flows.CheckinWaitOf(getRun(t, h.db, run.ID)); w == nil || w.StepID != "ask2" {
			t.Fatalf("wait %+v", w)
		}
		answer(t, h, "", "udah", "", at("2026-11-12T16:05"))
		h.run(at("2026-11-12T16:05"))
		done := getRun(t, h.db, run.ID)
		c := flows.CheckinOf(done)
		if c.StepID != "ask2" || c.Answer == nil || *c.Answer != "done" || c.Missed {
			t.Errorf("checkinOf %+v", c)
		}
		s := flows.SummarizeRuns([]db.FlowRun{*done})
		if v, _ := s.ByAnswer.Get("done"); v != 1 || s.Missed != 0 || len(s.MissedLabels) != 0 {
			t.Errorf("summary %+v", s)
		}
		// A correction goes to the same step the history reads.
		answer(t, h, run.ID, "done", "", at("2026-11-12T16:10"))
		steps := stepsOf(getRun(t, h.db, run.ID))
		ask1, _ := steps.Get("ask1")
		ask2, _ := steps.Get("ask2")
		if field(ask1, "missed") != true || len(field(ask1, "answers").([]any)) != 0 {
			t.Errorf("ask1 %s", jsonx.Stringify(ask1))
		}
		eq(t, len(field(ask2, "answers").([]any)), 2, "ask2 answers")
	})
	t.Run("a 'not yet' then misses everywhere is missed, and a late answer turns it answered", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-12T15:00"))
		flow := h.create(PRAYER_CHAIN).Flow
		h.run(at("2026-11-12T15:30"))
		h.run(at("2026-11-12T15:40"))
		answer(t, h, "", "belum", "", at("2026-11-12T15:45"))
		h.run(at("2026-11-12T16:00"))
		h.run(at("2026-11-12T16:30"))
		run := runsOf(t, h.db, flow.ID)[0]
		missed := getRun(t, h.db, run.ID)
		eq(t, missed.Status, "done", "status")
		s := flows.SummarizeRuns([]db.FlowRun{*missed})
		if s.Missed != 1 || len(s.MissedLabels) != 1 || s.MissedLabels[0] != "Ashar" || s.ByAnswer.Len() != 0 {
			t.Errorf("summary %+v", s)
		}
		res := answer(t, h, run.ID, "udah", "", at("2026-11-12T16:40"))
		eq(t, res.Reply, "Oke, jawaban diganti: Sudah.", "reply")
		s = flows.SummarizeRuns([]db.FlowRun{*getRun(t, h.db, run.ID)})
		if v, _ := s.ByAnswer.Get("done"); s.Missed != 0 || v != 1 {
			t.Errorf("summary after %+v", s)
		}
	})
	t.Run("reads the real Subuh run: a first missed step no longer hides the answer", func(t *testing.T) {
		steps := jobj(`{
      "ask0": {"answer": null, "answers": [], "followUps": 0, "missed": true},
      "g1": {"result": true},
      "ask1": {"answer": null, "answers": [], "followUps": 0, "missed": true},
      "g2": {"result": true},
      "ask2": {"answer": "done", "label": "Sudah", "answeredAt": 1794432349652, "answers": [{"answer": "done", "at": 1794432349652}], "followUps": 0, "missed": false},
      "alhamdulillah": {"text": "Alhamdulillah 🤲"}
    }`)
		id, _ := flows.DecidingCheckin(steps)
		eq(t, id, "ask2", "deciding")
		id, _ = flows.DecidingCheckin(jobj(`{"a":{"answer":null,"answers":[],"missed":true},"b":{"answer":"not_yet","answers":[{}],"missed":true}}`))
		eq(t, id, "b", "last reached")
		if _, hit := flows.DecidingCheckin(jobj(`{"x":{"text":"hi"}}`)); hit != nil {
			t.Error("found a check-in")
		}
	})
}

func TestSavedFilesInFlowSteps(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	flow := h.create(`{"name":"Kirim","steps":[{"id":"send","kind":"tool","tool":"mcp-keuangan__list_bills","args":{"file":"{{trigger.args.file}}"}}]}`).Flow
	run := h.start(flow.ID, "webhook", "w1", `{"args":{"file":"palui:attachment/att_ktp"}}`, "trigger").Run
	h.run(h.now())
	eq(t, len(h.calls.all()), 0, "calls")
	l := stepLog(t, h.db, run.ID)[0]
	if l.Step != "send" || l.Status != "error" || !strings.Contains(l.Error, "untrusted data") {
		t.Errorf("log %+v", l)
	}
	// The same arg from the flow's own trigger goes through.
	h.start(flow.ID, "manual", "m1", `{"args":{"file":"palui:attachment/att_ktp"}}`)
	h.run(h.now())
	eq(t, h.calls.of(""), []any{js(`{"file":"palui:attachment/att_ktp"}`)}, "calls")
}
