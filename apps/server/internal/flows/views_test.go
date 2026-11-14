package flows_test

// Go-only tests for code the TS suite exercised only through the connector:
// nextDue, the views, the recap, explain_flow, patches and pauses.

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestNextDue(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if _, ok, err := h.engine.NextDue(); err != nil || ok {
		t.Fatalf("empty: %v %v", ok, err)
	}
	h.create(BRIEFING)
	due, ok, err := h.engine.NextDue()
	tu.Must(t, err)
	if !ok || due != at("2026-11-11T06:00") {
		t.Fatalf("due %d %v", due, ok)
	}
	flow := h.create(`{"name":"Tunggu","steps":[{"id":"w","kind":"wait","minutes":30},{"id":"m","kind":"message","text":"x"}]}`).Flow
	h.start(flow.ID, "manual", "m1", "")
	h.run(h.now())
	due, _, _ = h.engine.NextDue()
	eq(t, due, h.now()+30*MIN, "wait wake")
}

func TestViewsAndRecap(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.setNow(at("2026-11-11T07:00"))
	flow := h.create(MEDICINE).Flow
	h.run(at("2026-11-11T08:00"))
	h.run(at("2026-11-11T10:00"))
	h.run(at("2026-11-11T20:00"))
	answer(t, h, "", "udah", "", at("2026-11-11T20:01"))
	h.run(at("2026-11-11T20:01"))

	sum, err := flows.FlowSummaryView(h.db, flow, at("2026-11-11T20:02"))
	tu.Must(t, err)
	for k, want := range map[string]any{"name": "Minum obat", "active": true, "version": 1.0, "approval": "not_needed"} {
		if v, _ := sum.Get(k); v != want {
			t.Errorf("summary.%s = %v", k, v)
		}
	}
	trig, _ := sum.Get("triggers")
	eq(t, len(trig.([]any)), 2, "triggers with manual")
	eq(t, field(trig.([]any)[0], "summary"), "Tiap hari: pagi 08.00, malam 20.00", "schedule summary")
	eq(t, field(trig.([]any)[0], "next"), "Besok, Kamis 12 Nov, 08.00 WITA", "next")

	detail, err := flows.FlowDetailView(h.db, flow, at("2026-11-11T20:02"), 0)
	tu.Must(t, err)
	if v, _ := detail.Get("shownVersion"); v != 1.0 {
		t.Errorf("shownVersion %v", v)
	}
	if none, err := flows.FlowDetailView(h.db, flow, h.now(), 9); err != nil || none != nil {
		t.Errorf("version 9: %v %v", none, err)
	}

	runs := runsOf(t, h.db, flow.ID)
	view, err := flows.RunView(h.db, &runs[0], WITA, at("2026-11-11T20:02"), true)
	tu.Must(t, err)
	eq(t, field(view, "checkin.missed"), true, "first run missed")
	steps, _ := view.Get("steps")
	eq(t, len(steps.([]any)), 1, "steps")

	recap, err := flows.FlowRecap(h.db, flow, 1, "", at("2026-11-11T20:02"))
	tu.Must(t, err)
	eq(t, field(recap, "text"), "📊 Rekap Minum obat, 24 jam terakhir:\n- Udah minum: 1\n- Terlewat: 1 (pagi)\nSelesai 1/2.", "recap text")
	eq(t, field(recap, "missed"), 1.0, "missed")
}

func TestDiagnose(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	flows.SetEngineClock(at("2026-11-10T09:00"), 0)
	flow := h.create(BRIEFING).Flow
	d, err := flows.DiagnoseFlow(h.db, flow, at("2026-11-11T06:00"), at("2026-11-11T09:00"))
	tu.Must(t, err)
	eq(t, d.Verdict, `No run of "Briefing pagi" near that time.`, "no run")
	contains(t, strings.Join(d.Facts, "\n"), "Trigger main occurrences around then: Hari ini, 06.00 WITA (Briefing).", "facts")

	h.run(at("2026-11-11T08:30"))
	// The run started at 08.30, so a question about 06.00 only sees it from 07.00 on (the window ends 2 h after).
	d, err = flows.DiagnoseFlow(h.db, flow, at("2026-11-11T06:00"), at("2026-11-11T09:00"))
	tu.Must(t, err)
	eq(t, d.Verdict, `No run of "Briefing pagi" near that time.`, "outside the window")
	d, err = flows.DiagnoseFlow(h.db, flow, at("2026-11-11T07:00"), at("2026-11-11T09:00"))
	tu.Must(t, err)
	eq(t, d.Verdict, "It ran at Hari ini, 06.00 WITA, but it was skipped as late: Palui was down or busy past the grace period at that time.", "late")

	paused, err := flows.SetFlowPaused(h.db, flow.ID, true, nil, h.now(), "rusak")
	tu.Must(t, err)
	d, err = flows.DiagnoseFlow(h.db, paused, at("2026-11-11T06:00"), at("2026-11-11T09:00"))
	tu.Must(t, err)
	eq(t, d.Verdict, `Flow "Briefing pagi" is paused (last error: rusak).`, "paused")
}

func TestPatchAndPause(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	saved := h.create(branchy)
	r, err := flows.UpdateFlow(h.db, saved.Flow.ID, flows.UpdateInput{Patch: jobj(`{"steps":{"b":{"text":"Bee"},"c":{"kind":"message","text":"new"},"say_a":null},"description":"Dua cabang"}`), CreatedBy: "test"}, h.now())
	tu.Must(t, err)
	eq(t, r.Diff, []string{"+ step c", "- step say_a", "~ step b"}, "diff")
	eq(t, r.Version.N, int64(2), "version")
	_, err = flows.UpdateFlow(h.db, saved.Flow.ID, flows.UpdateInput{Patch: jobj(`{"steps":{"pick":{"then":"gone"}}}`), CreatedBy: "test"}, h.now())
	if err == nil || !strings.Contains(err.Error(), `points then to "gone"`) {
		t.Errorf("dangling target: %v", err)
	}
	same, err := flows.UpdateFlow(h.db, saved.Flow.ID, flows.UpdateInput{Patch: jobj(`{"description":"Dua cabang"}`), CreatedBy: "test"}, h.now())
	tu.Must(t, err)
	if same.Changed {
		t.Error("an unchanged patch made a version")
	}
	if _, err := flows.SetFlowPaused(h.db, saved.Flow.ID, true, ptrInt(h.now()-1), h.now(), ""); err == nil {
		t.Error("a pause end in the past was accepted")
	}
	if got := flows.ApplyPatch(jobj(`{"a":1,"b":{"c":2}}`), jobj(`{"b":{"c":null,"d":3},"e":[1]}`)); jsonx.Stringify(got) != `{"a":1,"b":{"d":3},"e":[1]}` {
		t.Errorf("merge patch %s", jsonx.Stringify(got))
	}
	tu.Must(t, flows.DeleteFlow(h.db, saved.Flow.ID, h.now()))
	if f, _ := flows.GetFlow(h.db, saved.Flow.ID); f != nil {
		t.Error("flow kept")
	}
}

func ptrInt(v int64) *int64 { return &v }
