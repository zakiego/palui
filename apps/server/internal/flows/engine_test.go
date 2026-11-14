package flows_test

// engine.test.ts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

var (
	prayerResult  = returns(`{"content":[{"type":"text","text":"{\"times\":{\"fajr\":\"04:39\",\"dhuhr\":\"12:01\",\"asr\":\"15:26\",\"maghrib\":\"18:12\",\"isha\":\"19:24\"}}"}]}`)
	weatherResult = returns(`{"text":"{\"current_condition\":[{\"temp_C\":\"27\",\"weatherDesc\":[{\"value\":\"Hujan ringan\"}]}]}"}`)
	remindersRes  = returns(`{"reminders":[{"text":"Bayar listrik","date":"2026-11-11"},{"text":"Beli obat","date":"2026-11-12"}]}`)
)

func briefingHarness(t *testing.T, over map[string]runFn) *harness {
	m := map[string]runFn{"mcp-jadwal-sholat__get_prayer_times": prayerResult, "webfetch__fetch_page": weatherResult, "reminders__list_reminders": remindersRes}
	for k, v := range over {
		m[k] = v
	}
	return newHarness(t, harnessOptions{connectors: exampleConnectors(m)})
}

func triggers(t *testing.T, q db.Q, flowID string) []db.FlowTrigger {
	t.Helper()
	rows, err := flows.TriggersOf(q, flowID)
	tu.Must(t, err)
	return rows
}

func i64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestEngineScheduleBriefing(t *testing.T) {
	t.Run("runs the morning briefing once per occurrence with the flow's chat in the tool context", func(t *testing.T) {
		h := briefingHarness(t, nil)
		flow := h.create(BRIEFING).Flow
		eq(t, i64(triggers(t, h.db, flow.ID)[0].NextFireAt), at("2026-11-11T06:00"), "next fire")
		h.run(at("2026-11-11T06:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		if run.Status != "done" || run.RunKey != "2026-11-11:06:00" || run.TriggerKind != "schedule" {
			t.Fatalf("run %s %s %s (%v)", run.Status, run.RunKey, run.TriggerKind, deref(run.Error))
		}
		eq(t, stepsJoin(stepLog(t, h.db, run.ID), func(l logEntry) string { return l.Step + ":" + l.Status }), "prayer:ok,weather:ok,reminders:ok,today:ok,send:ok", "steps")
		eq(t, sentTexts(t, h.db), []sentRow{{
			Source:  "flow:" + run.ID,
			Text:    "Selamat pagi! Rabu 11 Nov\n\nSholat: Subuh 04:39, Zuhur 12:01, Asar 15:26, Magrib 18:12, Isya 19:24\nCuaca: 27°C, Hujan ringan\n\nPengingat hari ini (1):\n- Bayar listrik\n- Beli obat",
			Actions: []string{},
		}}, "sent")
		eq(t, run.Taint, []string{"steps.prayer", "steps.weather"}, "taint")
		for _, c := range h.calls.all() {
			if c.Turn.ChatID != h.chatID || c.Turn.FlowID != flow.ID || c.Turn.Trigger != "flow" {
				t.Errorf("turn %+v", c.Turn)
			}
		}
		rows, err := h.db.Query("SELECT tool FROM tool_calls ORDER BY rowid")
		toolNames, err := db.All(rows, err, func(s db.Scanner) (string, error) { var x string; return x, s.Scan(&x) })
		tu.Must(t, err)
		eq(t, toolNames, []string{"mcp-jadwal-sholat__get_prayer_times", "webfetch__fetch_page", "reminders__list_reminders"}, "tool calls")
		// The same sweep again, and a restart in between: still one run, one message.
		tu.Must(t, h.engine.Recover(at("2026-11-11T06:00")))
		h.run(at("2026-11-11T06:00"))
		eq(t, len(runsOf(t, h.db, flow.ID)), 1, "runs")
		eq(t, len(sentTexts(t, h.db)), 1, "sent")
		eq(t, i64(triggers(t, h.db, flow.ID)[0].NextFireAt), at("2026-11-12T06:00"), "next fire")
	})
	t.Run("a failing source with onError continue still sends the briefing", func(t *testing.T) {
		h := briefingHarness(t, map[string]runFn{"mcp-jadwal-sholat__get_prayer_times": fails("ECONNRESET")})
		flow := h.create(BRIEFING).Flow
		h.run(at("2026-11-11T06:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		if run.Status != "waiting" || i64(run.WakeAt) != at("2026-11-11T06:00")+30_000 {
			t.Fatalf("run %s wake %d", run.Status, i64(run.WakeAt))
		}
		h.run(at("2026-11-11T06:00") + 30_000)
		h.run(at("2026-11-11T06:00") + 30_000 + 120_000)
		run = runsOf(t, h.db, flow.ID)[0]
		eq(t, run.Status, "done", "status")
		var prayer []string
		log := stepLog(t, h.db, run.ID)
		for _, l := range log {
			if l.Step == "prayer" {
				prayer = append(prayer, l.Status)
			}
		}
		eq(t, prayer, []string{"error", "error", "error"}, "prayer attempts")
		if last := log[len(log)-1]; last.Step != "send" || last.Status != "ok" {
			t.Errorf("last %+v", last)
		}
		contains(t, sentTexts(t, h.db)[0].Text, "Sholat: Subuh -, Zuhur -", "text")
	})
	t.Run("skips an occurrence more than lateGraceMin late after downtime", func(t *testing.T) {
		h := briefingHarness(t, nil)
		flow := h.create(BRIEFING).Flow
		h.run(at("2026-11-11T08:30"))
		runs := runsOf(t, h.db, flow.ID)
		if len(runs) != 1 || runs[0].Status != "skipped" || deref(runs[0].SkipReason) != "late" || runs[0].RunKey != "2026-11-11:06:00" {
			t.Fatalf("runs %+v", runs)
		}
		eq(t, len(sentTexts(t, h.db)), 0, "sent")
		eq(t, i64(triggers(t, h.db, flow.ID)[0].NextFireAt), at("2026-11-12T06:00"), "next fire")
	})
	t.Run("adds the late note when a run is a few minutes late", func(t *testing.T) {
		h := briefingHarness(t, nil)
		flow := h.create(BRIEFING).Flow
		h.run(at("2026-11-11T06:10"))
		eq(t, runsOf(t, h.db, flow.ID)[0].Status, "done", "status")
		match(t, sentTexts(t, h.db)[0].Text, `\(telat, harusnya 06\.00\)$`)
	})
}

const branchy = `{
  "name": "Cabang",
  "steps": [
    { "id": "pick", "kind": "if", "when": "trigger.args.mode == \"a\"", "then": "a", "else": "b" },
    { "id": "a", "kind": "set", "values": { "n": "flow.n + 1", "label": "{{trigger.args.mode | upper}}" }, "scope": "flow" },
    { "id": "say_a", "kind": "message", "text": "A ke-{{flow.n}} ({{flow.label}})", "next": null },
    { "id": "b", "kind": "message", "text": "B" }
  ]
}`

func TestEngineManualRuns(t *testing.T) {
	t.Run("runs a manual start through the scheduler, with branches and flow variables", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(branchy).Flow
		for _, mode := range []string{"a", "a", "b"} {
			r := h.start(flow.ID, "manual", fmt.Sprintf("manual:%s:%d", mode, h.now()), fmt.Sprintf(`{"args":{"mode":%q}}`, mode))
			eq(t, r.Run.Status, "queued", "status")
			h.run(h.now())
			h.setNow(h.now() + 1000)
		}
		eq(t, texts(t, h.db), []string{"A ke-1 (A)", "A ke-2 (A)", "B"}, "texts")
		f, _ := flows.GetFlow(h.db, flow.ID)
		eq(t, f.State, db.FlowState{Values: jobj(`{"n":2,"label":"A"}`), Tainted: []string{}}, "state")
		runs := runsOf(t, h.db, flow.ID)
		eq(t, stepLog(t, h.db, runs[len(runs)-1].ID), []logEntry{{"pick", "ok", "else", "<nil>"}, {"b", "ok", "<nil>", "<nil>"}}, "log")
	})
	t.Run("caps messages per run and fails the run with a sentence", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		var steps []string
		for i := 0; i < 11; i++ {
			steps = append(steps, fmt.Sprintf(`{"id":"m%d","kind":"message","text":"%d"}`, i, i))
		}
		flow := h.create(`{"name":"Spam","steps":[` + strings.Join(steps, ",") + `]}`).Flow
		h.start(flow.ID, "manual", "manual:1", "")
		h.run(h.now())
		n := 0
		for _, s := range sentTexts(t, h.db) {
			if strings.HasPrefix(s.Source, "flow:") {
				n++
			}
		}
		eq(t, n, 10, "flow messages")
		r := runsOf(t, h.db, flow.ID)[0]
		if r.Status != "failed" || deref(r.Error) != "Step m10: More than 10 messages in one run." {
			t.Errorf("run %s %s", r.Status, deref(r.Error))
		}
	})
	t.Run("skips runs past the per-flow hourly limit", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Sering","limits":{"runsPerHour":2},"steps":[{"id":"m","kind":"message","text":"x"}]}`).Flow
		var got []string
		for i := 1; i <= 3; i++ {
			r := h.start(flow.ID, "manual", fmt.Sprintf("manual:%d", i), "")
			if r.Skipped != "" {
				got = append(got, r.Skipped)
			} else {
				got = append(got, r.Run.Status)
			}
		}
		eq(t, got, []string{"queued", "queued", "rate_limited"}, "starts")
	})
	t.Run("dedupes a run key", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Sekali","steps":[{"id":"m","kind":"message","text":"x"}]}`).Flow
		if h.start(flow.ID, "webhook", "d1", "").Run == nil {
			t.Fatal("first start nil")
		}
		r := h.start(flow.ID, "webhook", "d1", "")
		if r.Run != nil || r.Skipped != "duplicate" {
			t.Errorf("second %+v", r)
		}
	})
	t.Run("pauses a flow after five failed runs in a row and says so once", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"reminders__list_reminders": fails("boom")})})
		flow := h.create(`{"name":"Rusak","steps":[{"id":"t","kind":"tool","tool":"reminders__list_reminders","retry":{"max":0,"backoffSec":5}}]}`).Flow
		for i := 0; i < 5; i++ {
			h.start(flow.ID, "manual", fmt.Sprintf("m%d", i), "")
			h.run(h.now())
			h.setNow(h.now() + MIN)
		}
		f, _ := flows.GetFlow(h.db, flow.ID)
		if f.Enabled || f.ConsecutiveFailures != 5 {
			t.Errorf("flow enabled %v failures %d", f.Enabled, f.ConsecutiveFailures)
		}
		notices := texts(t, h.db)
		eq(t, len(notices), 2, "notices")
		match(t, notices[0], `^⚠️ Flow "Rusak" gagal: Step t: boom`)
		match(t, notices[len(notices)-1], `^⏸️ Flow "Rusak" aku jeda: 5 kali gagal`)
	})
}

func TestEngineWriteApproval(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	saved := h.create(`{"name":"Catat","steps":[{"id":"hi","kind":"message","text":"Mulai"},{"id":"note","kind":"tool","tool":"notes__save_note","args":{"text":"{{now.date}}"}},{"id":"done","kind":"message","text":"Beres"}]}`)
	if !saved.NeedsApproval {
		t.Fatal("no approval needed")
	}
	eq(t, sentTexts(t, h.db)[0].Actions, []string{"flowver:approve:" + saved.Version.ID, "flowver:deny:" + saved.Version.ID}, "prompt actions")
	h.start(saved.Flow.ID, "manual", "m1", "")
	h.run(h.now())
	run := runsOf(t, h.db, saved.Flow.ID)[0]
	if run.Status != "awaiting_approval" || deref(run.SkipReason) != "not_approved" || deref(run.Cursor) != "note" {
		t.Fatalf("run %s %s %s", run.Status, deref(run.SkipReason), deref(run.Cursor))
	}
	eq(t, len(h.calls.of("save_note")), 0, "calls")
	r, err := flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
	tu.Must(t, err)
	if !r.OK {
		t.Fatal(r.Reply)
	}
	h.run(h.now())
	eq(t, runsOf(t, h.db, saved.Flow.ID)[0].Status, "done", "status")
	eq(t, len(h.calls.of("save_note")), 1, "calls")
	eq(t, texts(t, h.db)[1:], []string{"Mulai", "Beres"}, "texts")
}

func insertStepRow(t *testing.T, q db.Q, id, runID, stepID, kind string, turnRunID *string, now int64) {
	t.Helper()
	_, err := q.Exec("INSERT INTO flow_step_runs (id, run_id, seq, step_id, kind, status, turn_run_id, started_at) VALUES (?, ?, 1, ?, ?, 'running', ?, ?)", id, runID, stepID, kind, turnRunID, now)
	tu.Must(t, err)
}

func TestEngineRestartRecovery(t *testing.T) {
	t.Run("runs a read tool again when a crash cut it off mid-call", func(t *testing.T) {
		h := briefingHarness(t, nil)
		flow := h.create(BRIEFING).Flow
		run := h.start(flow.ID, "manual", "m1", "").Run
		_, err := h.db.Exec("UPDATE flow_runs SET status = 'running', lease_until = ? WHERE id = ?", h.now()+60_000, run.ID)
		tu.Must(t, err)
		insertStepRow(t, h.db, "fls_x", run.ID, "prayer", "tool", nil, h.now())
		tu.Must(t, h.engine.Recover(h.now()))
		eq(t, getRun(t, h.db, run.ID).Status, "queued", "recovered")
		h.run(h.now())
		eq(t, getRun(t, h.db, run.ID).Status, "done", "done")
		eq(t, stepLog(t, h.db, run.ID)[:2], []logEntry{{"prayer", "error", "<nil>", "interrupted by a restart; running it again"}, {"prayer", "ok", "<nil>", "<nil>"}}, "log")
	})
	t.Run("never repeats a write tool cut off mid-call; it tells the owner and takes onError", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		saved := h.create(`{"name":"Tulis","steps":[{"id":"note","kind":"tool","tool":"notes__save_note","args":{"text":"x"}}]}`)
		_, err := flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		run := h.start(saved.Flow.ID, "manual", "m1", "").Run
		_, err = h.db.Exec("UPDATE flow_runs SET status = 'running' WHERE id = ?", run.ID)
		tu.Must(t, err)
		insertStepRow(t, h.db, "fls_y", run.ID, "note", "tool", nil, h.now())
		tu.Must(t, h.engine.Recover(h.now()))
		h.run(h.now())
		eq(t, len(h.calls.of("save_note")), 0, "calls")
		r := getRun(t, h.db, run.ID)
		if r.Status != "failed" || deref(r.Error) != "Step note: unknown outcome after restart" {
			t.Errorf("run %s %s", r.Status, deref(r.Error))
		}
		found := false
		for _, s := range texts(t, h.db) {
			if strings.Contains(s, "kepotong restart") {
				found = true
			}
		}
		if !found {
			t.Error("no restart notice")
		}
	})
	t.Run("takes a finished prompt turn's output instead of running it again", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": returns(`{"total":10,"missed":2,"byAnswer":{},"text":"rekap"}`)})})
		flow := h.create(RECAP).Flow
		r, err := h.engine.StartRun(flows.StartInput{FlowID: flow.ID, TriggerKind: "manual", RunKey: "m1", Payload: jsonx.NewMap[any](), Now: h.now(), Start: "cheer"})
		tu.Must(t, err)
		_, err = h.db.Exec("UPDATE flow_runs SET status = 'running', vars = ? WHERE id = ?", `{"steps":{"recap":{"ok":true,"data":{"total":10}}},"vars":{}}`, r.Run.ID)
		tu.Must(t, err)
		turn := "run_done"
		insertStepRow(t, h.db, "fls_z", r.Run.ID, "cheer", "prompt", &turn, h.now())
		_, err = h.db.Exec("INSERT INTO runs (id, chat_id, trigger, status, result_preview, started_at) VALUES ('run_done', ?, 'flow', 'ok', 'Mantap minggu ini!', ?)", h.chatID, h.now())
		tu.Must(t, err)
		tu.Must(t, h.engine.Recover(h.now()))
		h.run(h.now())
		eq(t, h.promptCount(), 0, "prompts")
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "Mantap minggu ini!", "text")
	})
}

func recapTool(missed int) runFn {
	return returns(fmt.Sprintf(`{"total":35,"missed":%d,"byAnswer":{"done":%d},"text":"📊 Rekap: terlewat %d"}`, missed, 35-missed, missed))
}

func TestEnginePromptSteps(t *testing.T) {
	t.Run("puts data in blocks, never in the instruction, and sends the model's text", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": recapTool(2)})})
		flow := h.create(RECAP).Flow
		h.run(at("2026-11-15T20:00"))
		run := runsOf(t, h.db, flow.ID)[0]
		eq(t, run.Status, "done", "status")
		eq(t, h.promptCount(), 1, "prompts")
		p := h.prompts[0]
		if p.ChatID != h.chatID || p.ModelRole != "fast" || len(p.Tools) != 0 || p.Send || p.Tainted {
			t.Errorf("prompt %+v", p)
		}
		contains(t, p.Prompt, "<data name=\"recap\">\n{\"total\":35,\"missed\":2", "data block")
		if strings.Index(p.Prompt, "Tulis pesan pendek") > strings.Index(p.Prompt, "<data") {
			t.Error("data before the instruction")
		}
		eq(t, texts(t, h.db), []string{"Semangat!"}, "texts")
		eq(t, stepsJoin(stepLog(t, h.db, run.ID), func(l logEntry) string { return l.Step }), "recap,any_missed,cheer,send_cheer", "steps")
	})
	t.Run("falls back to the plain recap when the model fails", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": recapTool(1)}), prompt: func(*harness, flows.PromptRequest) (flows.PromptResponse, error) {
			return flows.PromptResponse{Status: "error"}, nil
		}})
		flow := h.create(RECAP).Flow
		h.run(at("2026-11-15T20:00"))
		eq(t, texts(t, h.db), []string{"📊 Rekap: terlewat 1"}, "texts")
		for _, l := range stepLog(t, h.db, runsOf(t, h.db, flow.ID)[0].ID) {
			if l.Step == "cheer" && (l.Status != "error" || l.Branch != "onError:plain") {
				t.Errorf("cheer %+v", l)
			}
		}
	})
	t.Run("takes the clean branch with no model call when nothing was missed", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": recapTool(0)})})
		h.create(RECAP)
		h.run(at("2026-11-15T20:00"))
		eq(t, h.promptCount(), 0, "prompts")
		eq(t, texts(t, h.db)[0], "Minggu ini 35 sholat, semuanya kecatat sudah. Mantap, pertahankan! 🤲", "text")
	})
	t.Run("respects the flow's daily token cap", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": recapTool(2)})})
		def := strings.Replace(RECAP, `"name": "Rekap sholat",`, `"name": "Rekap sholat", "limits": {"dailyTokens": 1000},`, 1)
		flow := h.create(def).Flow
		_, err := budget.AddUsage(h.db, "flow:"+flow.ID+":"+budget.DayKey(at("2026-11-15T20:00"), WITA), 1000, h.now())
		tu.Must(t, err)
		h.run(at("2026-11-15T20:00"))
		r := runsOf(t, h.db, flow.ID)[0]
		if r.Status != "skipped" || deref(r.SkipReason) != "budget" {
			t.Errorf("run %s %s", r.Status, deref(r.SkipReason))
		}
		eq(t, h.promptCount(), 0, "prompts")
	})
	t.Run("counts a prompt step's cache reads at a tenth against the flow's daily cap", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": recapTool(2)}), prompt: func(h *harness, r flows.PromptRequest) (flows.PromptResponse, error) {
			_, err := h.db.Exec("INSERT INTO runs (id, chat_id, trigger, status, tokens_in, tokens_out, tokens_cached, started_at) VALUES (?, ?, 'flow', 'ok', 20000, 100, 18000, ?)", r.TurnRunID, h.chatID, h.now())
			if err != nil {
				return flows.PromptResponse{}, err
			}
			return flows.PromptResponse{Status: "ok", Text: "Semangat!", RunID: r.TurnRunID}, nil
		}})
		flow := h.create(RECAP).Flow
		h.run(at("2026-11-15T20:00"))
		eq(t, h.promptCount(), 1, "prompts")
		used, err := budget.GetUsage(h.db, "flow:"+flow.ID+":"+budget.DayKey(at("2026-11-15T20:00"), WITA))
		tu.Must(t, err)
		eq(t, used, int64(2_000+1_800+100), "usage")
	})
}

func TestNowIsATuesdayMorning(t *testing.T) {
	eq(t, NOW, at("2026-11-10T10:00"), "NOW")
}

func TestRetention(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	flow := h.create(`{"name":"Lama","steps":[{"id":"m","kind":"message","text":"x"}]}`).Flow
	h.start(flow.ID, "manual", "m1", "")
	h.run(h.now())
	day := int64(24 * 3600_000)
	r, err := flows.PruneFlows(h.db, h.now()+31*day)
	tu.Must(t, err)
	eq(t, r, flows.PruneResult{IO: 1}, "first prune")
	rows, err := flows.StepRunsOf(h.db, runsOf(t, h.db, flow.ID)[0].ID)
	tu.Must(t, err)
	if rows[0].Status != "ok" || rows[0].Output != nil {
		t.Errorf("step row %+v", rows[0])
	}
	r, err = flows.PruneFlows(h.db, h.now()+181*day)
	tu.Must(t, err)
	eq(t, r.Runs, int64(1), "runs pruned")
	eq(t, len(runsOf(t, h.db, flow.ID)), 0, "runs left")
}

var _ = connectors.TurnInfo{}
