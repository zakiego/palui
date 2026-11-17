package flows_test

// The flow test harness: test-helpers.ts (the RFC 9 examples and their tool
// list), test-entries.ts (fake running connectors) and test-harness.ts (a
// flow engine on an in-memory database with a settable clock).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

const WITA = "Asia/Makassar"

var NOW = tu.NOW

const MIN = int64(60_000)

func at(local string) int64 {
	ms, err := reminders.LocalToUTC(local, WITA)
	if err != nil {
		panic(err)
	}
	return ms
}

// js parses a JSON literal into jsonx values (objects keep their key order).
func js(s string) any {
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		panic(fmt.Sprintf("bad JSON in test: %v\n%s", err, s))
	}
	return v
}

func jobj(s string) *jsonx.Object { return js(s).(*jsonx.Object) }

// --- test-helpers.ts ---------------------------------------------------------------------

func tool(name, connectorID, t, class string, untrusted bool, ctype string, approval string) flows.ToolInfo {
	return flows.ToolInfo{Name: name, ConnectorID: connectorID, Tool: t, Class: class, Untrusted: untrusted, ConnectorType: ctype, FlowApproval: approval}
}

var ExampleTools = []flows.ToolInfo{
	tool("mcp-jadwal-sholat__get_prayer_times", "mcp-jadwal-sholat", "get_prayer_times", "read", true, "mcp", ""),
	tool("webfetch__fetch_page", "webfetch", "fetch_page", "read", true, "webfetch", ""),
	tool("reminders__list_reminders", "reminders", "list_reminders", "read", false, "reminders", ""),
	tool("reminders__set_reminder", "reminders", "set_reminder", "write", false, "reminders", ""),
	tool("reminders__cancel_reminder", "reminders", "cancel_reminder", "destructive", false, "reminders", ""),
	tool("mcp-github__get_commit_status", "mcp-github", "get_commit_status", "read", true, "mcp", ""),
	tool("mcp-github__add_comment", "mcp-github", "add_comment", "write", true, "mcp", "every_run"),
	tool("mcp-keuangan__list_bills", "mcp-keuangan", "list_bills", "read", false, "mcp", ""),
	tool("mcp-keuangan__mark_paid", "mcp-keuangan", "mark_paid", "write", false, "mcp", ""),
	tool("notes__save_note", "notes", "save_note", "write", false, "notes", ""),
	tool("flows__recap", "flows", "recap", "read", false, "flows", ""),
}

// 9a. Morning briefing.
const BRIEFING = `{
  "name": "Briefing pagi",
  "triggers": [{ "id": "main", "kind": "schedule", "start": "prayer", "source": { "kind": "times", "times": [{ "time": "06:00", "label": "Briefing" }] } }],
  "steps": [
    { "id": "prayer", "kind": "tool", "tool": "mcp-jadwal-sholat__get_prayer_times", "args": { "city": "Banjarmasin", "date": "{{now.date}}" }, "onError": "continue" },
    { "id": "weather", "kind": "tool", "tool": "webfetch__fetch_page", "args": { "url": "https://wttr.in/Banjarmasin?format=j1" }, "parse": { "field": "text", "as": "json" }, "onError": "continue" },
    { "id": "reminders", "kind": "tool", "tool": "reminders__list_reminders", "args": { "filter": "upcoming", "limit": 20 } },
    { "id": "today", "kind": "set", "values": { "count": "count(steps.reminders.data.reminders, \"date\", now.date)" } },
    {
      "id": "send",
      "kind": "message",
      "text": "Selamat pagi! {{now.date | date}}\n\nSholat: Subuh {{steps.prayer.data.times.fajr | default(\"-\")}}, Zuhur {{steps.prayer.data.times.dhuhr | default(\"-\")}}, Asar {{steps.prayer.data.times.asr | default(\"-\")}}, Magrib {{steps.prayer.data.times.maghrib | default(\"-\")}}, Isya {{steps.prayer.data.times.isha | default(\"-\")}}\nCuaca: {{steps.weather.data.current_condition.0.temp_C | default(\"?\")}}°C, {{steps.weather.data.current_condition.0.weatherDesc.0.value | default(\"nggak kebaca\")}}\n\nPengingat hari ini ({{vars.count}}):\n{{steps.reminders.data.reminders | lines(\"text\")}}"
    }
  ]
}`

// 9b. GitHub PR merged, check CI, notify.
const PR_MERGED = `{
  "name": "PR merged",
  "triggers": [{ "id": "gh", "kind": "webhook", "hookId": "hk_github_palui", "start": "is_merge" }],
  "steps": [
    { "id": "is_merge", "kind": "if", "when": "trigger.payload.action == \"closed\" and trigger.payload.pull_request.merged == true", "then": "settle", "else": null },
    { "id": "settle", "kind": "wait", "minutes": 5 },
    {
      "id": "ci",
      "kind": "tool",
      "tool": "mcp-github__get_commit_status",
      "args": { "owner": "zakiego", "repo": "palui", "ref": "{{trigger.payload.pull_request.merge_commit_sha}}" },
      "retry": { "max": 2, "backoffSec": 60 },
      "onError": "ci_unknown"
    },
    { "id": "green", "kind": "if", "when": "steps.ci.data.state == \"success\"", "then": "ok", "else": "not_ok" },
    { "id": "ok", "kind": "message", "next": null, "text": "✅ PR #{{trigger.payload.pull_request.number}} \"{{trigger.payload.pull_request.title | truncate(80)}}\" merged, CI hijau." },
    { "id": "not_ok", "kind": "message", "next": null, "text": "⚠️ PR #{{trigger.payload.pull_request.number}} merged tapi CI {{steps.ci.data.state}}. Cek: {{trigger.payload.pull_request.html_url}}" },
    { "id": "ci_unknown", "kind": "message", "text": "PR #{{trigger.payload.pull_request.number}} merged, tapi status CI nggak kebaca: {{steps.ci.error | truncate(120)}}" }
  ]
}`

// 9c. Bill reminder.
const BILLS = `{
  "name": "Tagihan",
  "triggers": [{ "id": "daily", "kind": "schedule", "start": "bills", "source": { "kind": "times", "times": [{ "time": "09:00", "label": "Cek tagihan" }] } }],
  "steps": [
    { "id": "bills", "kind": "tool", "tool": "mcp-keuangan__list_bills", "args": { "status": "unpaid" } },
    { "id": "per_bill", "kind": "each", "items": "steps.bills.data.items", "where": "daysUntil(item.due) <= 3 and daysUntil(item.due) >= 0", "key": "{{item.id}}:{{item.due}}", "max": 20, "body": "ask", "next": null },
    {
      "id": "ask",
      "kind": "checkin",
      "text": "Tagihan {{item.name | truncate(40)}} Rp{{item.amount}} jatuh tempo {{item.due | date}}. Udah bayar?",
      "answers": [
        { "id": "paid", "label": "Udah", "words": ["udah", "sudah", "lunas"], "closes": true },
        { "id": "later", "label": "Belum", "words": ["belum", "nanti"], "closes": false }
      ],
      "followUp": { "everyMin": 240, "max": 3, "text": "🔁 {{item.name | truncate(40)}} belum dibayar ya? Jatuh tempo {{item.due | date}}." },
      "deadline": { "kind": "after", "minutes": 4320 },
      "missedText": "Tagihan {{item.name | truncate(40)}} lewat tanpa konfirmasi.",
      "on": { "paid": "mark" },
      "next": null
    },
    { "id": "mark", "kind": "set", "scope": "flow", "values": { "last_paid": "{{item.name}} {{now.date}}" } }
  ]
}`

// 9d. Weekly prayer recap with encouragement.
const RECAP = `{
  "name": "Rekap sholat",
  "triggers": [{ "id": "weekly", "kind": "schedule", "start": "recap", "source": { "kind": "rrule", "rrule": "FREQ=WEEKLY;BYDAY=SU", "start": "2026-11-15T20:00" } }],
  "steps": [
    { "id": "recap", "kind": "tool", "tool": "flows__recap", "args": { "flow": "Sholat 5 waktu", "days": 7 } },
    { "id": "any_missed", "kind": "if", "when": "steps.recap.data.missed > 0 or number(steps.recap.data.byAnswer.not_yet) > 0", "then": "cheer", "else": "clean" },
    {
      "id": "cheer",
      "kind": "prompt",
      "prompt": "Tulis pesan pendek (maks 4 kalimat), hangat dan nggak menggurui, buat Zaki tentang rekap sholatnya minggu ini. Sebut angkanya sekali, terus kasih semangat yang konkret buat minggu depan. Bahasa santai.",
      "input": { "recap": "{{steps.recap.data}}" },
      "tools": [],
      "model": "fast",
      "onError": "plain"
    },
    { "id": "send_cheer", "kind": "message", "text": "{{steps.cheer.text}}", "next": null },
    { "id": "plain", "kind": "message", "text": "{{steps.recap.data.text}}", "next": null },
    { "id": "clean", "kind": "message", "text": "Minggu ini {{steps.recap.data.total}} sholat, semuanya kecatat sudah. Mantap, pertahankan! 🤲" }
  ]
}`

const prayerSourceJSON = `{
  "kind": "tool",
  "connectorId": "mcp-jadwal-sholat",
  "tool": "get_prayer_times",
  "args": { "city": "Banjarmasin", "date": "{{date}}" },
  "refreshAt": "00:05",
  "items": [
    { "field": "times.fajr", "label": "Subuh", "offsetMin": 0 },
    { "field": "times.dhuhr", "label": "Zuhur", "offsetMin": 0 },
    { "field": "times.asr", "label": "Asar", "offsetMin": 0 },
    { "field": "times.maghrib", "label": "Magrib", "offsetMin": 0 },
    { "field": "times.isha", "label": "Isya", "offsetMin": 0 }
  ]
}`

// 9e. "Sholat 5 waktu" as a flow.
var SHOLAT = `{
  "name": "Sholat 5 waktu",
  "tz": "Asia/Makassar",
  "triggers": [
    { "id": "main", "kind": "schedule", "start": "ask", "lateGraceMin": 60, "source": ` + prayerSourceJSON + ` },
    { "id": "recap", "kind": "schedule", "start": "recap_read", "source": { "kind": "rrule", "rrule": "FREQ=WEEKLY;BYDAY=SU", "start": "2026-11-15T20:00" } }
  ],
  "steps": [
    {
      "id": "ask",
      "kind": "checkin",
      "text": "Udah sholat {{trigger.label}} ({{trigger.time}})?",
      "answers": [
        { "id": "done", "label": "Sudah", "words": ["udah", "udh", "dah", "done", "selesai", "beres"], "closes": true },
        { "id": "not_yet", "label": "Belum", "words": ["blm", "not yet", "nanti"], "closes": false }
      ],
      "followUp": { "everyMin": 15, "max": 4 },
      "deadline": { "kind": "next" },
      "next": null
    },
    { "id": "recap_read", "kind": "tool", "tool": "flows__recap", "args": { "flow": "self", "days": 7, "trigger": "main" } },
    { "id": "recap_any", "kind": "if", "when": "steps.recap_read.data.total > 0", "then": "recap_send", "else": null },
    { "id": "recap_send", "kind": "message", "text": "{{steps.recap_read.data.text}}" }
  ]
}`

var Examples = []struct{ Name, JSON string }{{"BRIEFING", BRIEFING}, {"PR_MERGED", PR_MERGED}, {"BILLS", BILLS}, {"RECAP", RECAP}, {"SHOLAT", SHOLAT}}

// prayerPayload: one day of times per call; times move a minute per day.
func prayerPayload(date string) *jsonx.Object {
	var day int
	fmt.Sscanf(date[8:10], "%d", &day)
	m := func(base int) string { return fmt.Sprintf("%02d", base+day%5) }
	return jobj(fmt.Sprintf(`{"date":%q,"location":{"slug":"banjarmasin","timezone":"Asia/Makassar"},"times":{"imsak":"04:%s","fajr":"04:%s","sunrise":"05:%s","dhuhr":"12:0%d","asr":"15:%s","maghrib":"18:%s","isha":"19:%s"}}`,
		date, m(24), m(34), m(50), day%5, m(25), m(11), m(23)))
}

// fakePrayer is fakePrayerTool: a source tool caller with a call log.
type fakePrayer struct {
	mu    sync.Mutex
	calls []struct {
		ConnectorID, Tool string
		Args              *jsonx.Object
	}
	fail bool
}

func (f *fakePrayer) caller() flows.ToolCaller {
	return func(_ context.Context, connectorID, toolName string, args *jsonx.Object, _ *tools.CallScope) (any, error) {
		f.mu.Lock()
		f.calls = append(f.calls, struct {
			ConnectorID, Tool string
			Args              *jsonx.Object
		}{connectorID, toolName, args})
		fail := f.fail
		f.mu.Unlock()
		if fail {
			return nil, fmt.Errorf("fetch failed: ECONNRESET")
		}
		date, _ := args.Get("date")
		return jobj(fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`, jsonx.Stringify(jsonx.Stringify(prayerPayload(date.(string)))))), nil
	}
}

func (f *fakePrayer) dates() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		d, _ := c.Args.Get("date")
		out = append(out, d.(string))
	}
	return out
}

// PRAYER_SOURCE: a tool source for the prayer server, as the agent would write it.
const PRAYER_SOURCE = `{
  "kind": "tool",
  "connectorId": "mcp-jadwal-sholat",
  "tool": "get_prayer_times",
  "args": { "city": "banjarmasin", "date": "{{date}}" },
  "refreshAt": "00:05",
  "items": [
    { "field": "times.fajr", "label": "Subuh" },
    { "field": "times.dhuhr", "label": "Dzuhur" },
    { "field": "times.asr", "label": "Ashar" },
    { "field": "times.maghrib", "label": "Maghrib" },
    { "field": "times.isha", "label": "Isya" }
  ]
}`

// --- test-entries.ts ---------------------------------------------------------------------

type runFn func(args *jsonx.Object, turn connectors.TurnInfo) (any, error)

type fakeTool struct {
	class     string
	untrusted bool
	approval  string
	validate  func(input json.RawMessage) error
	run       runFn
}

type fakeConnector struct {
	id, typ, trust string
	names          []string
	tools          map[string]*fakeTool
	agentID        *string
}

type callLog struct {
	mu    sync.Mutex
	calls []struct {
		ConnectorID, Tool string
		Args              *jsonx.Object
		Turn              connectors.TurnInfo
	}
}

func (l *callLog) add(cid, t string, args *jsonx.Object, turn connectors.TurnInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, struct {
		ConnectorID, Tool string
		Args              *jsonx.Object
		Turn              connectors.TurnInfo
	}{cid, t, args, turn})
}

func (l *callLog) of(t string) []*jsonx.Object {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*jsonx.Object
	for _, c := range l.calls {
		if t == "" || c.Tool == t {
			out = append(out, c.Args)
		}
	}
	return out
}

func (l *callLog) all() []struct {
	ConnectorID, Tool string
	Args              *jsonx.Object
	Turn              connectors.TurnInfo
} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append(l.calls[:0:0], l.calls...)
}

func fakeEntries(list []*fakeConnector, log *callLog) []connectors.RunningEntry {
	var out []connectors.RunningEntry
	for _, c := range list {
		c := c
		typ, trust := c.typ, c.trust
		if typ == "" {
			typ = c.id
		}
		if trust == "" {
			trust = "own"
		}
		out = append(out, connectors.RunningEntry{
			ID:  c.id,
			Row: db.Connector{ID: c.id, Type: typ, Kind: "tools", Label: c.id, Trust: trust, AgentID: c.agentID},
			Handle: &connectors.RunningConnector{Tools: func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				set := connectors.NewToolSet()
				for _, name := range c.names {
					name, t := name, c.tools[name]
					set.Set(name, connectors.PaluiTool{
						Class: t.class, Untrusted: t.untrusted, FlowApproval: t.approval,
						Tool: connectors.Tool{
							Description: name,
							Validate:    t.validate,
							Execute: func(_ context.Context, input json.RawMessage, _ connectors.ToolCallInfo) (any, error) {
								args, err := jsonx.ParseObject(input)
								if err != nil {
									return nil, err
								}
								log.add(c.id, name, args, turn)
								return t.run(args, turn)
							},
						},
					})
				}
				return set, nil
			}},
		})
	}
	return out
}

func okRun(*jsonx.Object, connectors.TurnInfo) (any, error) { return jobj(`{"ok":true}`), nil }

// exampleConnectors: connectors that answer like the examples' tools.
func exampleConnectors(over map[string]runFn) []*fakeConnector {
	var order []*fakeConnector
	by := map[string]*fakeConnector{}
	for _, t := range ExampleTools {
		c := by[t.ConnectorID]
		if c == nil {
			trust := "own"
			if t.Untrusted && t.ConnectorType == "mcp" {
				trust = "external"
			}
			c = &fakeConnector{id: t.ConnectorID, typ: t.ConnectorType, trust: trust, tools: map[string]*fakeTool{}}
			by[t.ConnectorID] = c
			order = append(order, c)
		}
		run := runFn(okRun)
		if r, ok := over[t.Name]; ok {
			run = r
		}
		c.names = append(c.names, t.Tool)
		c.tools[t.Tool] = &fakeTool{class: t.Class, untrusted: t.Untrusted, approval: t.FlowApproval, run: run}
	}
	return order
}

func returns(s string) runFn {
	return func(*jsonx.Object, connectors.TurnInfo) (any, error) { return js(s), nil }
}

func fails(msg string) runFn {
	return func(*jsonx.Object, connectors.TurnInfo) (any, error) { return nil, fmt.Errorf("%s", msg) }
}

// --- test-harness.ts ---------------------------------------------------------------------

type harnessOptions struct {
	connectors []*fakeConnector
	readSource flows.ToolCaller
	// sourceUntrusted marks a tool source's connector as external.
	sourceUntrusted func(connectorID string) bool
	prompt          func(h *harness, r flows.PromptRequest) (flows.PromptResponse, error)
	concurrent      bool
}

type harness struct {
	t           *testing.T
	db          *db.DB
	chatID      string
	otherChatID string
	mu          sync.Mutex
	clock       int64
	calls       *callLog
	promptsMu   sync.Mutex
	prompts     []flows.PromptRequest
	engine      *flows.Engine
	tools       *flows.ConnectorFlowCaller
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	d, chatID, otherID := tu.Setup(t)
	h := &harness{t: t, db: d, chatID: chatID, otherChatID: otherID, clock: NOW, calls: &callLog{}}
	conns := o.connectors
	if conns == nil {
		conns = exampleConnectors(nil)
	}
	entries := fakeEntries(conns, h.calls)
	h.tools = flows.NewConnectorFlowCaller(func() []connectors.RunningEntry { return entries })
	h.engine = flows.NewEngine(flows.EngineOptions{
		DB:              d,
		Clock:           h.now,
		Tools:           h.tools,
		ReadSource:      o.readSource,
		SourceUntrusted: o.sourceUntrusted,
		Serial:          !o.concurrent,
		RunPrompt: func(_ context.Context, r flows.PromptRequest) (flows.PromptResponse, error) {
			h.promptsMu.Lock()
			h.prompts = append(h.prompts, r)
			h.promptsMu.Unlock()
			if o.prompt != nil {
				return o.prompt(h, r)
			}
			return flows.PromptResponse{Status: "ok", Text: "Semangat!", RunID: r.TurnRunID}, nil
		},
	})
	return h
}

func (h *harness) now() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

func (h *harness) setNow(t int64) {
	h.mu.Lock()
	h.clock = t
	h.mu.Unlock()
}

func (h *harness) promptCount() int {
	h.promptsMu.Lock()
	defer h.promptsMu.Unlock()
	return len(h.prompts)
}

// run is the harness run(): a sweep, then one more for runs the first queued.
func (h *harness) run(when int64) int {
	h.t.Helper()
	h.setNow(when)
	n, err := h.engine.Sweep(when)
	tu.Must(h.t, err)
	h.engine.Idle()
	_, err = h.engine.Sweep(when)
	tu.Must(h.t, err)
	h.engine.Idle()
	return n
}

func (h *harness) validateCtx() *flows.ValidateContext {
	return &flows.ValidateContext{ResolveTool: flows.ResolverFrom(ExampleTools), HookExists: func(string) bool { return true }}
}

func (h *harness) createErr(def any) (*flows.SaveResult, error) {
	if s, ok := def.(string); ok {
		def = js(s)
	}
	return flows.CreateFlow(h.db, flows.SaveInput{ChatID: h.chatID, Definition: def, CreatedBy: "test", Validate: h.validateCtx()}, h.now())
}

func (h *harness) create(def any) *flows.SaveResult {
	h.t.Helper()
	r, err := h.createErr(def)
	tu.Must(h.t, err)
	return r
}

func (h *harness) start(flowID, kind, key, payload string, taint ...string) flows.StartResult {
	h.t.Helper()
	if payload == "" {
		payload = "{}"
	}
	r, err := h.engine.StartRun(flows.StartInput{FlowID: flowID, TriggerKind: kind, RunKey: key, Payload: jobj(payload), Taint: taint, Now: h.now()})
	tu.Must(h.t, err)
	return r
}

type sentRow struct {
	Source  string
	Text    string
	Actions []string
}

func sentTexts(t *testing.T, q db.Q) []sentRow {
	t.Helper()
	rows, err := q.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY created_at ASC, id ASC")
	list, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(t, err)
	out := []sentRow{}
	for _, r := range list {
		acts := []string{}
		for _, a := range r.Payload.Actions {
			acts = append(acts, a.ID)
		}
		out = append(out, sentRow{r.Source, r.Payload.Text, acts})
	}
	return out
}

func texts(t *testing.T, q db.Q) []string {
	var out []string
	for _, s := range sentTexts(t, q) {
		out = append(out, s.Text)
	}
	return out
}

func runsOf(t *testing.T, q db.Q, flowID string) []db.FlowRun {
	t.Helper()
	rows, err := q.Query("SELECT "+db.FlowRunCols+" FROM flow_runs WHERE flow_id = ? ORDER BY started_at ASC, id ASC", flowID)
	list, err := db.All(rows, err, db.ScanFlowRun)
	tu.Must(t, err)
	return list
}

func getRun(t *testing.T, q db.Q, id string) *db.FlowRun {
	t.Helper()
	r, err := flows.GetRun(q, id)
	tu.Must(t, err)
	return r
}

type logEntry struct {
	Step, Status string
	Branch       string
	Error        string
}

func stepLog(t *testing.T, q db.Q, runID string) []logEntry {
	t.Helper()
	rows, err := flows.StepRunsOf(q, runID)
	tu.Must(t, err)
	out := []logEntry{}
	for _, s := range rows {
		out = append(out, logEntry{s.StepID, s.Status, deref(s.Branch), deref(s.Error)})
	}
	return out
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func stepsJoin(log []logEntry, f func(l logEntry) string) string {
	var parts []string
	for _, l := range log {
		parts = append(parts, f(l))
	}
	return strings.Join(parts, ",")
}

// eq fails the test when got and want differ (compared as JSON).
func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	g, _ := jsonx.Marshal(got)
	w, _ := jsonx.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s:\n got: %s\nwant: %s", what, g, w)
	}
}

func contains(t *testing.T, s, sub, what string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s: %q doesn't contain %q", what, s, sub)
	}
}

// field reads a dotted path from a JSON object value.
func field(v any, path string) any {
	cur := v
	for _, p := range strings.Split(path, ".") {
		o, ok := cur.(*jsonx.Object)
		if !ok {
			return nil
		}
		cur, _ = o.Get(p)
	}
	return cur
}

func stepsOf(r *db.FlowRun) *jsonx.Object {
	return flows.DecodeVars(r.Vars).Steps
}
