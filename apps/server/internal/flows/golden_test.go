package flows_test

// Goldens from testdata/gen/flows.ts (M5): validator results with zod's issue
// text, stored definitions and hashes, expressions, templates, taint, schedule
// occurrences across the New York DST changes, and run traces of scripted
// flows replayed step by step against the Go engine.
//
// Normalized in both stacks: ids (<prefix>#N in order of first appearance),
// events.id (dropped), hooks.secret and hooks.slug (random), the chat id in a
// dry run's draft flow id, the version hash
// of the hook trace (its definition holds the random hook id), and
// outbox.payload compared by content (Go writes {text, actions, format}).
// Every other column, stored JSON included, compares as exact text.

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

type flowsGolden struct {
	Now           int64           `json:"now"`
	ExprScope     json.RawMessage `json:"exprScope"`
	TemplateScope json.RawMessage `json:"templateScope"`
	TaintInputs   struct {
		Keys   []string        `json:"keys"`
		Values json.RawMessage `json:"values"`
		Args   json.RawMessage `json:"args"`
	} `json:"taintInputs"`
	Definitions []struct {
		Name       string          `json:"name"`
		Def        string          `json:"def"`
		OK         bool            `json:"ok"`
		Problems   []flows.Problem `json:"problems"`
		WriteCalls json.RawMessage `json:"writeCalls"`
		Stored     *string         `json:"stored"`
		Canonical  *string         `json:"canonical"`
		Hash       *string         `json:"hash"`
	} `json:"definitions"`
	Exprs []struct {
		Src    string          `json:"src"`
		TZ     string          `json:"tz"`
		Now    int64           `json:"now"`
		Result json.RawMessage `json:"result"`
		Error  *string         `json:"error"`
	} `json:"exprs"`
	Templates []struct {
		Src    string          `json:"src"`
		Mode   string          `json:"mode"`
		Taint  *[]string       `json:"taint"`
		Result json.RawMessage `json:"result"`
		Error  *string         `json:"error"`
	} `json:"templates"`
	Taint []struct {
		Taint  []string        `json:"taint"`
		Keys   map[string]bool `json:"keys"`
		Values []bool          `json:"values"`
		Args   []string        `json:"args"`
		Added  []string        `json:"added"`
	} `json:"taint"`
	Schedules []struct {
		Name        string          `json:"name"`
		Source      json.RawMessage `json:"source"`
		TZ          string          `json:"tz"`
		Days        *[]string       `json:"days"`
		Cursor      flows.Cursor    `json:"cursor"`
		N           int             `json:"n"`
		State       json.RawMessage `json:"state"`
		Occurrences json.RawMessage `json:"occurrences"`
		Error       *string         `json:"error"`
	} `json:"schedules"`
	Traces []json.RawMessage `json:"traces"`
}

var goldenFile = "../../testdata/flows.json"

func loadFlowsGolden(t *testing.T) flowsGolden {
	t.Helper()
	b, err := os.ReadFile(goldenFile)
	tu.Must(t, err)
	var g flowsGolden
	tu.Must(t, json.Unmarshal(b, &g))
	return g
}

func raw(t *testing.T, b []byte) any {
	t.Helper()
	v, err := jsonx.Parse(b)
	tu.Must(t, err)
	return v
}

func canon(v any) string { return jsonx.Canonical(v) }

// plainValue is the generator's plain(): undefined and non-finite numbers.
func plainValue(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = plainValue(x)
		}
		return out
	case *jsonx.Object:
		out := jsonx.NewMap[any]()
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			out.Set(k, plainValue(x))
		}
		return out
	}
	return v
}

func scopeOf(t *testing.T, b []byte) flows.Scope {
	o := raw(t, b).(*jsonx.Object)
	s := flows.Scope{}
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		s[k] = v
	}
	return s
}

func TestGoldenDefinitions(t *testing.T) {
	g := loadFlowsGolden(t)
	ctx := flows.ValidateContext{
		ResolveTool: flows.ResolverFrom(ExampleTools),
		HookExists:  func(id string) bool { return id == "hk_github_palui" },
		ChatID:      "chat_owner",
		ChatAllowed: func(id string) string {
			switch id {
			case "chat_owner", "chat_web":
				return "own"
			case "chat_family":
				return "allowed"
			}
			return ""
		},
		ResolveContact: func(ref string) *flows.ContactRef {
			if strings.ToLower(ref) == "zaki" || ref == "ctc_zaki" {
				return &flows.ContactRef{ID: "ctc_zaki", Name: "Zaki"}
			}
			return nil
		},
		FlowNames: []string{"Olahraga", "T"},
	}
	for _, c := range g.Definitions {
		t.Run(c.Name, func(t *testing.T) {
			r := flows.ValidateDefinition(raw(t, []byte(c.Def)), ctx)
			if r.OK != c.OK {
				t.Errorf("ok %v, want %v", r.OK, c.OK)
			}
			eq(t, r.Problems, c.Problems, "problems")
			if canon(r.WriteCalls) != canon(raw(t, c.WriteCalls)) {
				t.Errorf("write calls %s, want %s", jsonx.Stringify(r.WriteCalls), c.WriteCalls)
			}
			if c.Stored == nil {
				if r.Definition != nil {
					t.Errorf("parsed, want a refusal")
				}
				return
			}
			if r.Definition == nil {
				t.Fatalf("not parsed")
			}
			if got := jsonx.Stringify(r.Definition.Raw); got != *c.Stored {
				t.Errorf("stored definition differs; %s", tu.DiffAt(got, *c.Stored))
			}
			eq(t, flows.CanonicalJSON(r.Definition.Raw), *c.Canonical, "canonical")
			eq(t, flows.HashDefinition(r.Definition), *c.Hash, "hash")
		})
	}
}

func TestGoldenExprs(t *testing.T) {
	g := loadFlowsGolden(t)
	scope := scopeOf(t, g.ExprScope)
	for _, c := range g.Exprs {
		ast, err := flows.ParseExpr(c.Src)
		if c.Error != nil {
			if err == nil || err.Error() != *c.Error {
				t.Errorf("%q: error %v, want %q", strings.Repeat("", 0)+truncate(c.Src), err, *c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", truncate(c.Src), err)
			continue
		}
		r := flows.Evaluate(ast, flows.EvalEnv{Scope: scope, TZ: c.TZ, Now: c.Now})
		trace := []any{}
		for _, e := range r.Trace {
			trace = append(trace, obj("expr", e.Expr, "value", plainValue(e.Value)))
		}
		warnings := []any{}
		for _, w := range r.Warnings {
			warnings = append(warnings, w)
		}
		got := obj("value", plainValue(r.Value), "trace", trace, "warnings", warnings)
		if canon(got) != canon(raw(t, c.Result)) {
			t.Errorf("%q (%s):\n got %s\nwant %s", truncate(c.Src), c.TZ, canon(got), canon(raw(t, c.Result)))
		}
	}
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

func TestGoldenTemplates(t *testing.T) {
	g := loadFlowsGolden(t)
	scope := scopeOf(t, g.TemplateScope)
	env := flows.EvalEnv{Scope: scope, TZ: WITA, Now: g.Now}
	for _, c := range g.Templates {
		var tc flows.TaintCheck
		if c.Taint != nil {
			taint := *c.Taint
			tc = func(k string) bool { return flows.KeyTainted(taint, k) }
		}
		var out any
		var err error
		if c.Mode == "text" {
			out, err = flows.RenderText(c.Src, env, tc)
		} else {
			out, err = flows.RenderString(c.Src, env, c.Mode, tc)
		}
		if c.Error != nil {
			if err == nil || err.Error() != *c.Error {
				t.Errorf("%q: error %v, want %q", truncate(c.Src), err, *c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", truncate(c.Src), err)
			continue
		}
		if canon(plainValue(out)) != canon(raw(t, c.Result)) {
			t.Errorf("%q %s %v:\n got %s\nwant %s", truncate(c.Src), c.Mode, c.Taint, canon(out), canon(raw(t, c.Result)))
		}
	}
}

func TestGoldenTaint(t *testing.T) {
	g := loadFlowsGolden(t)
	values := raw(t, g.TaintInputs.Values).([]any)
	args := raw(t, g.TaintInputs.Args).(*jsonx.Object)
	for _, c := range g.Taint {
		for _, k := range g.TaintInputs.Keys {
			if flows.KeyTainted(c.Taint, k) != c.Keys[k] {
				t.Errorf("%v keyTainted(%s) != %v", c.Taint, k, c.Keys[k])
			}
		}
		var vals []bool
		for _, v := range values {
			vals = append(vals, flows.ValueTainted(c.Taint, v))
		}
		eq(t, vals, c.Values, fmt.Sprintf("%v values", c.Taint))
		eq(t, flows.TaintedArgs(c.Taint, args), c.Args, fmt.Sprintf("%v args", c.Taint))
		eq(t, flows.AddTaint(c.Taint, "trigger", "steps.z", "item"), c.Added, fmt.Sprintf("%v added", c.Taint))
	}
}

func TestGoldenSchedules(t *testing.T) {
	g := loadFlowsGolden(t)
	for _, c := range g.Schedules {
		t.Run(c.Name, func(t *testing.T) {
			def, problems := flows.ParseDefinition(js(fmt.Sprintf(`{"name":"S","triggers":[{"id":"t","kind":"schedule","start":"m","source":%s}],"steps":[{"id":"m","kind":"message","text":"x"}]}`, c.Source)))
			if def == nil {
				t.Fatalf("source: %v", problems)
			}
			var state *jsonx.Object
			if len(c.State) > 0 && string(c.State) != "null" {
				state = raw(t, c.State).(*jsonx.Object)
			}
			in := flows.ScheduleInput(&def.Triggers[0], c.TZ, state)
			if c.Days != nil {
				in.Days, in.HasDays = *c.Days, true
			}
			occ, err := flows.Upcoming(in, c.Cursor, c.N)
			if c.Error != nil {
				if err == nil || err.Error() != *c.Error {
					t.Errorf("error %v, want %q", err, *c.Error)
				}
				return
			}
			tu.Must(t, err)
			if canon(occ) != canon(raw(t, c.Occurrences)) {
				t.Errorf("occurrences\n got %s\nwant %s", canon(occ), canon(raw(t, c.Occurrences)))
			}
		})
	}
}

// --- traces ------------------------------------------------------------------------------

type traceSpec struct {
	Returns  json.RawMessage `json:"returns"`
	Fails    *string         `json:"fails"`
	Sequence []traceSpec     `json:"sequence"`
}

type traceDoc struct {
	Name   string `json:"name"`
	Tools  string `json:"tools"`
	Prompt *struct {
		Status  string  `json:"status"`
		Text    string  `json:"text"`
		Tokens  []int64 `json:"tokens"`
		Tainted bool    `json:"tainted"`
	} `json:"prompt"`
	ReadSource *string `json:"readSource"`
	Steps      []struct {
		Step   string          `json:"step"`
		Result json.RawMessage `json:"result"`
		Tables json.RawMessage `json:"tables"`
	} `json:"steps"`
}

func specFn(spec traceSpec) runFn {
	var mu sync.Mutex
	i := 0
	return func(*jsonx.Object, connectors.TurnInfo) (any, error) {
		mu.Lock()
		s := spec
		if spec.Sequence != nil {
			s = spec.Sequence[min(i, len(spec.Sequence)-1)]
			i++
		}
		mu.Unlock()
		if s.Fails != nil {
			return nil, fmt.Errorf("%s", *s.Fails)
		}
		if len(s.Returns) == 0 {
			return nil, nil
		}
		return jsonx.Parse(s.Returns)
	}
}

var tables = []string{"flows", "flow_versions", "flow_triggers", "flow_runs", "flow_step_runs", "outbox", "events", "pending_approvals", "tool_calls", "outbound_messages", "hooks"}

// A dry run's draft flow id embeds the chat id where the id pattern can't see it.
var draftID = regexp.MustCompile(`draft_chat_[0-9a-z]{19}`)

var refRe = regexp.MustCompile(`\{(chat|other|flow|version|run|approval|hook|contact)(?::(\d+))?(?::(\d+))?\}`)

type replayer struct {
	t        *testing.T
	h        *harness
	flowIDs  []string
	hookIDs  []string
	contacts []string
}

func (r *replayer) approvalIDs() []string {
	var out []string
	for _, a := range pendingApprovals(r.t, r.h.db) {
		out = append(out, a.ID)
	}
	return out
}

func (r *replayer) resolve(v any) any {
	switch x := v.(type) {
	case string:
		return refRe.ReplaceAllStringFunc(x, func(m string) string {
			p := refRe.FindStringSubmatch(m)
			i := 0
			fmt.Sscanf(p[2], "%d", &i)
			switch p[1] {
			case "chat":
				return r.h.chatID
			case "other":
				return r.h.otherChatID
			case "flow":
				return r.flowIDs[i]
			case "version":
				f, _ := flows.GetFlow(r.h.db, r.flowIDs[i])
				return *f.CurrentVersionID
			case "run":
				j := 0
				fmt.Sscanf(p[3], "%d", &j)
				return runsOf(r.t, r.h.db, r.flowIDs[i])[j].ID
			case "approval":
				return r.approvalIDs()[i]
			case "hook":
				return r.hookIDs[i]
			case "contact":
				return r.contacts[i]
			}
			return m
		})
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = r.resolve(e)
		}
		return out
	case *jsonx.Object:
		out := jsonx.NewMap[any]()
		for _, k := range x.Keys() {
			e, _ := x.Get(k)
			out.Set(k, r.resolve(e))
		}
		return out
	}
	return v
}

func writeCallsValue(list []flows.WriteCall) any {
	b, _ := jsonx.Marshal(list)
	v, _ := jsonx.Parse(b)
	return v
}

func saveValue(r *flows.SaveResult, version bool) *jsonx.Object {
	diff := []any{}
	for _, d := range r.Diff {
		diff = append(diff, d)
	}
	o := obj("needsApproval", r.NeedsApproval, "changed", r.Changed, "writeCalls", writeCallsValue(r.WriteCalls), "diff", diff)
	if version {
		o.Set("version", float64(r.Version.N))
	}
	return o
}

func numArg(o *jsonx.Object, k string) int {
	v, _ := o.Get(k)
	f, _ := v.(float64)
	return int(f)
}

func strArg(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func (r *replayer) step(st *jsonx.Object) any {
	h := r.h
	t := r.t
	kind := strArg(st, "do")
	time := func() int64 {
		if s := strArg(st, "at"); s != "" {
			return at(s)
		}
		return h.now()
	}
	if strArg(st, "at") != "" && kind != "sweep" {
		h.setNow(time())
	}
	errValue := func(err error) any { return obj("error", err.Error()) }
	switch kind {
	case "create":
		def, _ := st.Get("def")
		res, err := h.createErr(def)
		if err != nil {
			return errValue(err)
		}
		r.flowIDs = append(r.flowIDs, res.Flow.ID)
		return saveValue(res, false)
	case "update":
		def, _ := st.Get("def")
		patch, _ := st.Get("patch")
		res, err := flows.UpdateFlow(h.db, r.flowIDs[numArg(st, "flow")], flows.UpdateInput{Definition: def, Patch: patch, CreatedBy: "test", Validate: h.validateCtx()}, h.now())
		if err != nil {
			return errValue(err)
		}
		return saveValue(res, true)
	case "sweep":
		return float64(h.run(time()))
	case "start":
		payload, _ := st.Get("payload")
		p, _ := payload.(*jsonx.Object)
		if p == nil {
			p = jsonx.NewMap[any]()
		}
		var taint []string
		if tv, ok := st.Get("taint"); ok {
			for _, x := range tv.([]any) {
				taint = append(taint, x.(string))
			}
		}
		res, err := h.engine.StartRun(flows.StartInput{FlowID: r.flowIDs[numArg(st, "flow")], TriggerKind: strArg(st, "kind"), RunKey: strArg(st, "key"), Payload: p, Taint: taint, Now: h.now()})
		if err != nil {
			return errValue(err)
		}
		var skipped any
		if res.Skipped != "" {
			skipped = res.Skipped
		}
		return obj("run", res.Run != nil, "skipped", skipped)
	case "chat":
		chatID := strArg(st, "chat")
		if chatID == "" {
			chatID = h.chatID
		}
		owner, _ := st.Get("fromOwner")
		took, err := flows.Actions{}.FromChat(h.db, chatTrigger(strArg(st, "text"), chatID, strArg(st, "messageId"), owner == true), h.now())
		if err != nil {
			return errValue(err)
		}
		return took
	case "answer":
		res, err := flows.AnswerFlowCheckin(h.db, flows.AnswerInput{ChatID: h.chatID, RunID: strArg(st, "run"), Answer: strArg(st, "answer"), Text: strArg(st, "text")}, h.now())
		if err != nil {
			return errValue(err)
		}
		return obj("reply", res.Reply, "closed", res.Closed, "early", res.Early)
	case "callback", "text":
		var reply string
		var ok bool
		var err error
		if kind == "callback" {
			reply, ok, err = flows.Actions{}.FromCallback(h.db, strArg(st, "data"), h.chatID, h.now())
		} else {
			reply, ok, err = flows.Actions{}.FromText(h.db, strArg(st, "text"), strArg(st, "replyTo"), h.chatID, h.now())
		}
		if err != nil {
			return errValue(err)
		}
		if !ok {
			return nil
		}
		return reply
	case "decide":
		f, err := flows.GetFlow(h.db, r.flowIDs[numArg(st, "flow")])
		tu.Must(t, err)
		approved, _ := st.Get("approved")
		res, err := flows.DecideVersion(h.db, *f.CurrentVersionID, approved == true, "test", h.now())
		if err != nil {
			return errValue(err)
		}
		return res.Reply
	case "approve":
		id := r.approvalIDs()[numArg(st, "approval")]
		a, _, err := approvals.Get(h.db, id)
		tu.Must(t, err)
		approved, _ := st.Get("approved")
		ok, err := approvals.Decide(h.db, id, approved == true, h.now())
		tu.Must(t, err)
		tu.Must(t, flows.WakeFlowRun(h.db, a.RunID, h.now()))
		return ok
	case "expire":
		ids, err := approvals.ExpireDue(h.db, h.now())
		tu.Must(t, err)
		for _, id := range ids {
			tu.Must(t, flows.WakeFlowRun(h.db, id, h.now()))
		}
		return float64(len(ids))
	case "pause", "resume":
		var until *int64
		if s := strArg(st, "until"); s != "" {
			u := at(s)
			until = &u
		}
		_, err := flows.SetFlowPaused(h.db, r.flowIDs[numArg(st, "flow")], kind == "pause", until, h.now(), strArg(st, "reason"))
		if err != nil {
			return errValue(err)
		}
		return nil
	case "hook":
		c, err := hooks.Create(h.db, hooks.CreateInput{Name: strArg(st, "name"), Mode: strArg(st, "mode"), ChatID: h.chatID}, "0123456789abcdef0123456789abcdef", h.now())
		tu.Must(t, err)
		r.hookIDs = append(r.hookIDs, c.Hook.ID)
		return nil
	case "ingest":
		hook, ok, err := db.One(h.db.QueryRow("SELECT "+db.HookCols+" FROM hooks WHERE id = ?", r.hookIDs[numArg(st, "hook")]), db.ScanHook)
		tu.Must(t, err)
		if !ok {
			t.Fatal("no hook")
		}
		headers := map[string]string{}
		if hv, ok := st.Get("headers"); ok {
			ho := hv.(*jsonx.Object)
			for _, k := range ho.Keys() {
				v, _ := ho.Get(k)
				headers[k] = v.(string)
			}
		}
		res, err := flows.IngestFlowHook(h.db, hook, strArg(st, "body"), headers, h.now())
		if err != nil {
			return errValue(err)
		}
		return obj("started", float64(res.Started), "skipped", float64(res.Skipped), "listeners", float64(res.Listeners))
	case "contact":
		a, err := agents.GetDefault(h.db, h.now())
		tu.Must(t, err)
		c, err := outbound.AddContact(h.db, outbound.AddContactInput{AgentID: a.ID, Name: strArg(st, "name"), Phone: strArg(st, "phone")}, h.now())
		tu.Must(t, err)
		r.contacts = append(r.contacts, c.ID)
		return nil
	case "dry":
		in := flows.DryRunInput{ChatID: h.chatID, TZ: WITA, Now: h.now()}
		if _, ok := st.Get("flow"); ok {
			f, err := flows.GetFlow(h.db, r.flowIDs[numArg(st, "flow")])
			tu.Must(t, err)
			in.Flow = f
		}
		if d, ok := st.Get("def"); ok {
			in.Definition = flows.ValidateDefinition(d, flows.ValidateContext{ResolveTool: flows.ResolverFrom(ExampleTools)}).Definition
		}
		if v, ok := st.Get("trigger"); ok {
			in.Trigger = v.(*jsonx.Object)
		}
		if v, ok := st.Get("stubs"); ok {
			in.Stubs = v.(*jsonx.Object)
		}
		res, err := flows.DryRunFlow(h.engine, in)
		if err != nil {
			return errValue(err)
		}
		b, _ := jsonx.Marshal(res)
		v, _ := jsonx.Parse(b)
		return v
	case "crash":
		runID := strArg(st, "run")
		_, err := h.db.Exec("UPDATE flow_runs SET status = 'running', lease_until = ? WHERE id = ?", at(strArg(st, "lease")), runID)
		tu.Must(t, err)
		_, err = h.db.Exec("INSERT INTO flow_step_runs (id, run_id, seq, step_id, kind, status, started_at) VALUES ('fls_crashcrashcrashcrash', ?, 1, ?, ?, 'running', ?)", runID, strArg(st, "step"), strArg(st, "kind"), h.now())
		tu.Must(t, err)
		return nil
	}
	t.Fatalf("unknown step %s", kind)
	return nil
}

func snapshot(t *testing.T, q db.Q) *jsonx.Object {
	out := jsonx.NewMap[any]()
	for _, name := range tables {
		rows := tu.TableRows(t, q, name, "")
		if name == "events" {
			rows = tu.Without(rows, "id")
		}
		out.Set(name, rows)
	}
	return out
}

// looseTables drops random columns and parses outbox payloads, in place.
func loosen(v any) any {
	steps := objGet(v, "steps").([]any)
	for _, s := range steps {
		tb := objGet(s, "tables").(*jsonx.Object)
		if hs, ok := tb.Get("hooks"); ok {
			for _, h := range hs.([]any) {
				h.(*jsonx.Object).Delete("secret")
				h.(*jsonx.Object).Delete("slug")
			}
		}
		tu.ParseJSONColumns(tb, "payload")
	}
	return v
}

func dropHash(v any) {
	for _, s := range objGet(v, "steps").([]any) {
		for _, r := range objGet(objGet(s, "tables"), "flow_versions").([]any) {
			r.(*jsonx.Object).Delete("hash")
		}
	}
}

func objGet(v any, k string) any {
	o, _ := v.(*jsonx.Object)
	x, _ := o.Get(k)
	return x
}

func chatTrigger(text, chatID, msgID string, owner bool) chat.ChatTrigger {
	return chat.ChatTrigger{Text: text, ChatID: chatID, MessageID: msgID, FromOwner: owner}
}

func TestGoldenTraces(t *testing.T) {
	g := loadFlowsGolden(t)
	for _, rawTrace := range g.Traces {
		var doc traceDoc
		tu.Must(t, json.Unmarshal(rawTrace, &doc))
		t.Run(doc.Name, func(t *testing.T) {
			over := map[string]runFn{}
			var specs map[string]traceSpec
			tu.Must(t, json.Unmarshal([]byte(doc.Tools), &specs))
			for name, spec := range specs {
				over[name] = specFn(spec)
			}
			opts := harnessOptions{connectors: exampleConnectors(over)}
			if doc.ReadSource != nil && *doc.ReadSource == "prayer" {
				opts.readSource = (&fakePrayer{}).caller()
			}
			p := doc.Prompt
			opts.prompt = func(h *harness, r flows.PromptRequest) (flows.PromptResponse, error) {
				if p == nil {
					return flows.PromptResponse{Status: "ok", Text: "Semangat!", RunID: r.TurnRunID}, nil
				}
				if len(p.Tokens) == 3 {
					if _, err := h.db.Exec("INSERT INTO runs (id, chat_id, trigger, status, tokens_in, tokens_out, tokens_cached, started_at) VALUES (?, ?, 'flow', 'ok', ?, ?, ?, ?)", r.TurnRunID, h.chatID, p.Tokens[0], p.Tokens[1], p.Tokens[2], h.now()); err != nil {
						return flows.PromptResponse{}, err
					}
				}
				return flows.PromptResponse{Status: p.Status, Text: p.Text, RunID: r.TurnRunID, Tainted: p.Tainted}, nil
			}
			h := newHarness(t, opts)
			rp := &replayer{t: t, h: h}
			steps := []any{}
			for _, s := range doc.Steps {
				stepRaw := raw(t, []byte(s.Step)).(*jsonx.Object)
				result := rp.step(rp.resolve(stepRaw).(*jsonx.Object))
				steps = append(steps, obj("step", s.Step, "result", result, "tables", snapshot(t, h.db)))
			}
			tools := doc.Tools
			var prompt, readSource any
			if doc.Prompt != nil {
				prompt = objGet(raw(t, rawTrace), "prompt")
			}
			if doc.ReadSource != nil {
				readSource = *doc.ReadSource
			}
			got := raw(t, []byte(tu.NormalizeGolden(obj("name", doc.Name, "tools", tools, "prompt", prompt, "readSource", readSource, "steps", steps))))
			want := raw(t, rawTrace)
			loosen(got)
			loosen(want)
			if doc.Name == "hook trigger" {
				// The definition names the hook by its random id, so the hash differs.
				dropHash(got)
				dropHash(want)
			}
			gs, ws := objGet(got, "steps").([]any), objGet(want, "steps").([]any)
			for i := range ws {
				if i >= len(gs) {
					t.Fatalf("missing step %d", i)
				}
				gc, wc := draftID.ReplaceAllString(canon(gs[i]), "draft_chat#"), draftID.ReplaceAllString(canon(ws[i]), "draft_chat#")
				if gc != wc {
					t.Errorf("step %d (%s) differs; %s", i, jsonx.Stringify(objGet(ws[i], "step")), tu.DiffAt(gc, wc))
					return
				}
			}
		})
	}
}
