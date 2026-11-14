package flows_test

// each-poll.test.ts

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

const billList = `[
  {"id":"pln","name":"Listrik","amount":350000,"due":"2026-11-12"},
  {"id":"net","name":"Internet","amount":400000,"due":"2026-11-25"},
  {"id":"bpjs","name":"BPJS","amount":150000,"due":"2026-11-11"}
]`

func bills(items string) runFn { return returns(`{"items":` + items + `}`) }

func topLevel(runs []db.FlowRun) []db.FlowRun {
	var out []db.FlowRun
	for _, r := range runs {
		if r.ParentRunID == nil {
			out = append(out, r)
		}
	}
	return out
}

func TestEachCrossRunKeys(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("asks once per bill due within 3 days, never twice (concurrent=%v)", concurrent), func(t *testing.T) {
			h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-keuangan__list_bills": bills(billList)}), concurrent: concurrent})
			flow := h.create(BILLS).Flow
			h.run(at("2026-11-11T09:00"))
			runs := runsOf(t, h.db, flow.ID)
			parent := topLevel(runs)[0]
			eq(t, parent.Status, "done", "parent")
			perBill, _ := stepsOf(&parent).Get("per_bill")
			eq(t, perBill, js(`{"count":2,"started":2,"deduped":0,"dropped":0}`), "per_bill")
			var keys []string
			var children []db.FlowRun
			for _, r := range runs {
				if r.ParentRunID != nil && *r.ParentRunID == parent.ID {
					children = append(children, r)
					keys = append(keys, r.RunKey)
					eq(t, r.Status, "waiting", "child status")
				}
			}
			sort.Strings(keys)
			eq(t, keys, []string{"each:per_bill:bpjs:2026-11-11", "each:per_bill:pln:2026-11-12"}, "keys")
			got := texts(t, h.db)
			sort.Strings(got)
			eq(t, got, []string{"Tagihan BPJS Rp150000 jatuh tempo Rabu 11 Nov. Udah bayar?", "Tagihan Listrik Rp350000 jatuh tempo Kamis 12 Nov. Udah bayar?"}, "asks")
			// The next day the same bills come back: nothing new is asked.
			h.run(at("2026-11-12T09:00"))
			tops := topLevel(runsOf(t, h.db, flow.ID))
			next := tops[len(tops)-1]
			perBill, _ = stepsOf(&next).Get("per_bill")
			eq(t, perBill, js(`{"count":1,"started":0,"deduped":1,"dropped":0}`), "next day")
			// Paying one runs its own branch and sets a flow variable.
			var pln db.FlowRun
			for _, c := range children {
				if strings.Contains(c.RunKey, "pln") {
					pln = c
				}
			}
			answer(t, h, pln.ID, "lunas", "", at("2026-11-12T09:30"))
			h.run(at("2026-11-12T09:30"))
			eq(t, getRun(t, h.db, pln.ID).Status, "done", "pln status")
			eq(t, stepsJoin(stepLog(t, h.db, pln.ID), func(l logEntry) string { return l.Step + ":" + strings.Replace(l.Branch, "<nil>", "null", 1) }), "ask:on.paid,mark:null", "pln log")
			f, _ := flows.GetFlow(h.db, flow.ID)
			eq(t, f.State.Values, js(`{"last_paid":"Listrik 2026-11-12"}`), "flow state")
		})
	}
	t.Run("drops items over max and says so", func(t *testing.T) {
		var many []string
		for i := 0; i < 5; i++ {
			many = append(many, fmt.Sprintf(`{"id":"b%d","name":"B%d","amount":1,"due":"2026-11-12"}`, i, i))
		}
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-keuangan__list_bills": bills("[" + strings.Join(many, ",") + "]")})})
		flow := h.create(strings.Replace(BILLS, `"max": 20`, `"max": 2`, 1)).Flow
		h.run(at("2026-11-11T09:00"))
		parent := topLevel(runsOf(t, h.db, flow.ID))[0]
		perBill, _ := stepsOf(&parent).Get("per_bill")
		eq(t, perBill, js(`{"count":5,"started":2,"deduped":0,"dropped":3}`), "per_bill")
		for _, l := range stepLog(t, h.db, parent.ID) {
			if l.Step == "per_bill" {
				eq(t, l.Error, "3 item(s) over max 2 were dropped.", "error")
			}
		}
	})
	t.Run("dry-runs the first 3 items inline with stubbed check-ins", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-keuangan__list_bills": bills(billList)})})
		v := flows.ValidateDefinition(js(BILLS), flows.ValidateContext{ResolveTool: flows.ResolverFrom(ExampleTools)})
		res := dryRun(t, h, flows.DryRunInput{Definition: v.Definition, Stubs: jobj(`{"ask":{"answer":"paid"}}`), Now: at("2026-11-11T09:00")})
		eq(t, dryLog(res.Steps, stepStatus), []string{"bills:ok", "per_bill:ok"}, "steps")
		var children [][]string
		for _, c := range res.Children {
			children = append(children, dryLog(c.Steps, func(s flows.DryStep) string {
				b := "null"
				if s.Branch != nil {
					b = *s.Branch
				}
				return s.Step + ":" + s.Status + ":" + b
			}))
		}
		eq(t, children, [][]string{{"ask:stubbed:on.paid", "mark:ok:null"}, {"ask:stubbed:on.paid", "mark:ok:null"}}, "children")
		eq(t, len(sentTexts(t, h.db)), 0, "sent")
	})
}

const POLL = `{
  "name": "CI",
  "triggers": [{ "id": "ci", "kind": "poll", "tool": "mcp-github__get_commit_status", "args": { "ref": "main" }, "everyMin": 5, "select": "state", "start": "say" }],
  "steps": [{ "id": "say", "kind": "message", "text": "CI: {{trigger.previous}} -> {{trigger.value}}" }]
}`

func TestPollTriggers(t *testing.T) {
	t.Run("seeds on the first poll, fires on a change, and stays quiet otherwise", func(t *testing.T) {
		var mu sync.Mutex
		state := "pending"
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-github__get_commit_status": func(*jsonx.Object, connectors.TurnInfo) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			return obj("state", state), nil
		}})})
		set := func(s string) { mu.Lock(); state = s; mu.Unlock() }
		flow := h.create(POLL).Flow
		t0 := h.now()
		h.run(t0)
		eq(t, len(runsOf(t, h.db, flow.ID)), 0, "runs")
		tr := triggers(t, h.db, flow.ID)[0]
		eq(t, i64(tr.NextFireAt), t0+5*MIN, "next fire")
		if v, _ := tr.PollState.Get("seeded"); v != true {
			t.Error("not seeded")
		}
		if v, _ := tr.PollState.Get("lastValuePreview"); v != `"pending"` {
			t.Errorf("preview %v", v)
		}
		h.run(t0 + 5*MIN)
		eq(t, len(runsOf(t, h.db, flow.ID)), 0, "runs")
		set("success")
		h.run(t0 + 10*MIN)
		run := runsOf(t, h.db, flow.ID)[0]
		if run.TriggerKind != "poll" || run.Status != "done" {
			t.Errorf("run %s %s", run.TriggerKind, run.Status)
		}
		eq(t, run.Taint, []string{"trigger"}, "taint")
		v, _ := run.Trigger.Get("value")
		p, _ := run.Trigger.Get("previous")
		if v != "success" || p != "pending" {
			t.Errorf("payload %s", jsonx.Stringify(run.Trigger))
		}
		eq(t, texts(t, h.db), []string{"CI: pending -> success"}, "texts")
		// Back and forth fires again.
		set("pending")
		h.run(t0 + 15*MIN)
		set("success")
		h.run(t0 + 20*MIN)
		eq(t, len(runsOf(t, h.db, flow.ID)), 3, "runs")
	})
	t.Run("backs off on failures and tells the owner after three", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"mcp-github__get_commit_status": fails("503")})})
		flow := h.create(POLL).Flow
		tm := h.now()
		for i := 0; i < 3; i++ {
			h.run(tm)
			tm = i64(triggers(t, h.db, flow.ID)[0].NextFireAt)
		}
		ps := triggers(t, h.db, flow.ID)[0].PollState
		f, _ := ps.Get("failures")
		le, _ := ps.Get("lastError")
		if f != 3.0 || le != "503" {
			t.Errorf("poll state %s", jsonx.Stringify(ps))
		}
		eq(t, texts(t, h.db), []string{`⚠️ Flow "CI" gagal ngecek mcp-github__get_commit_status 3 kali berturut-turut: 503. Aku coba lagi otomatis.`}, "notice")
	})
}

var _ = tu.NOW
