package flows_test

// dryrun.test.ts

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

func draft(t *testing.T, def string) flows.Validated {
	t.Helper()
	r := flows.ValidateDefinition(js(def), flows.ValidateContext{ResolveTool: flows.ResolverFrom(ExampleTools), HookExists: func(string) bool { return true }})
	eq(t, r.Problems, []flows.Problem{}, "problems")
	return r
}

func dryRun(t *testing.T, h *harness, in flows.DryRunInput) *flows.DryRunResult {
	t.Helper()
	if in.ChatID == "" {
		in.ChatID = h.chatID
	}
	if in.TZ == "" {
		in.TZ = WITA
	}
	if in.Now == 0 {
		in.Now = at("2026-11-10T10:00")
	}
	res, err := flows.DryRunFlow(h.engine, in)
	tu.Must(t, err)
	return res
}

func dryLog(steps []flows.DryStep, f func(s flows.DryStep) string) []string {
	out := []string{}
	for _, s := range steps {
		out = append(out, f(s))
	}
	return out
}

func stepStatus(s flows.DryStep) string { return s.Step + ":" + s.Status }

func countRows(t *testing.T, q db.Q, sql string) int {
	t.Helper()
	var n int
	tu.Must(t, q.QueryRow(sql).Scan(&n))
	return n
}

func TestDryRuns(t *testing.T) {
	t.Run("9a: calls read tools for real, stubs the message, writes nothing else", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-jadwal-sholat__get_prayer_times": prayerResult, "reminders__list_reminders": returns(`{"reminders":[]}`)})})
		v := draft(t, BRIEFING)
		res := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, WriteCalls: v.WriteCalls})
		eq(t, res.Status, "done", "status")
		eq(t, dryLog(res.Steps, stepStatus), []string{"prayer:ok", "weather:ok", "reminders:ok", "today:ok", "send:stubbed"}, "steps")
		contains(t, field(res.Steps[len(res.Steps)-1].Output, "text").(string), "Subuh 04:39", "text")
		var tools []string
		for _, c := range h.calls.all() {
			tools = append(tools, c.Tool)
		}
		eq(t, tools, []string{"get_prayer_times", "fetch_page", "list_reminders"}, "calls")
		eq(t, countRows(t, h.db, "SELECT count(*) FROM outbox"), 0, "outbox")
		rows, err := h.db.Query("SELECT " + db.FlowRunCols + " FROM flow_runs")
		runs, err := db.All(rows, err, db.ScanFlowRun)
		tu.Must(t, err)
		if len(runs) != 1 || !runs[0].DryRun || runs[0].FlowID != "draft_"+h.chatID {
			t.Errorf("runs %+v", runs)
		}
		// The scheduler never picks a dry run up.
		h.run(at("2026-11-10T10:05"))
		eq(t, countRows(t, h.db, "SELECT count(*) FROM flow_runs WHERE dry_run = 0"), 0, "real runs")
	})
	t.Run("9d: stubs the prompt with an override and walks the chosen branch", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": returns(`{"total":35,"missed":3,"byAnswer":{},"text":"rekap"}`)})})
		v := draft(t, RECAP)
		res := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, Stubs: jobj(`{"cheer":"Tetap semangat!"}`)})
		eq(t, dryLog(res.Steps, stepStatus), []string{"recap:ok", "any_missed:ok", "cheer:stubbed", "send_cheer:stubbed"}, "steps")
		eq(t, field(res.Steps[1].Input, "explain"), "steps.recap.data.missed = 3", "explain")
		eq(t, field(res.Steps[len(res.Steps)-1].Output, "text"), "Tetap semangat!", "text")
		eq(t, h.promptCount(), 0, "prompts")
	})
	t.Run("a saved flow dry-runs its current version with a sample schedule payload", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": returns(`{"total":0,"missed":0,"byAnswer":{},"text":""}`)})})
		flow := h.create(RECAP).Flow
		res := dryRun(t, h, flows.DryRunInput{Flow: flow})
		eq(t, res.Status, "done", "status")
		eq(t, dryLog(res.Steps, func(s flows.DryStep) string { return s.Step }), []string{"recap", "any_missed", "clean"}, "steps")
	})
	t.Run("stubs write tools with the rendered args", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		v := draft(t, `{"name":"Catat","steps":[{"id":"note","kind":"tool","tool":"notes__save_note","args":{"text":"Tanggal {{now.date}}"}}]}`)
		res := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, WriteCalls: v.WriteCalls})
		eq(t, res.Steps[0].Status, "stubbed", "status")
		eq(t, field(res.Steps[0].Input, "args"), js(`{"text":"Tanggal 2026-11-10"}`), "args")
		eq(t, field(res.Steps[0].Input, "class"), "write", "class")
		eq(t, len(h.calls.all()), 0, "calls")
	})
	t.Run("9b: a fake webhook payload walks the merge branch; the wait is skipped and the read is real", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-github__get_commit_status": returns(`{"state":"failure"}`)})})
		v := draft(t, PR_MERGED)
		payload := jobj(`{"payload":{"action":"closed","pull_request":{"merged":true,"number":7,"title":"x","merge_commit_sha":"s1","html_url":"https://github.com/zakiego/palui/pull/7"}}}`)
		res := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, Trigger: payload})
		eq(t, dryLog(res.Steps, stepStatus), []string{"is_merge:ok", "settle:skipped", "ci:ok", "green:ok", "not_ok:stubbed"}, "steps")
		eq(t, field(res.Steps[len(res.Steps)-1].Output, "text"), "⚠️ PR #7 merged tapi CI failure. Cek: https://github.com/zakiego/palui/pull/7", "text")
		closed := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, Trigger: jobj(`{"payload":{"action":"opened"}}`)})
		eq(t, dryLog(closed.Steps, func(s flows.DryStep) string { return s.Step }), []string{"is_merge"}, "not a merge")
	})
	t.Run("9e: the check-in is stubbed with the chosen answer, and the recap trigger can be tried alone", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"flows__recap": returns(`{"total":0,"text":""}`)})})
		v := draft(t, SHOLAT)
		ask := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, Trigger: jobj(`{"label":"Subuh","time":"04.39"}`), Stubs: jobj(`{"ask":"not_yet"}`)})
		s := ask.Steps[0]
		if s.Step != "ask" || s.Status != "stubbed" || field(s.Output, "answer") != "not_yet" || field(s.Output, "asked") != "Udah sholat Subuh (04.39)?" {
			t.Errorf("ask %+v %s", s, jsonx.Stringify(s.Output))
		}
		recap := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, TriggerID: "recap"})
		eq(t, dryLog(recap.Steps, func(s flows.DryStep) string {
			b := ""
			if s.Branch != nil {
				b = *s.Branch
			}
			return s.Step + ":" + b
		}), []string{"recap_read:", "recap_any:else"}, "recap")
	})
}
