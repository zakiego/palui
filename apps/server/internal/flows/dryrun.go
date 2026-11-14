package flows

// Dry runs (dryrun.ts, RFC flows 7.6, test_flow). A definition, saved or a
// draft, runs against a fake trigger payload: read tools are called for real,
// while write tools, messages, check-ins and prompts are stubbed and logged
// with what they would have sent. Waits are skipped and an each runs at most
// 3 children inline. Nothing is written except one flow_runs row with
// dry_run = 1 and its step log.

import (
	"errors"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

const DryEachLimit = 3

// DryRunInput is DryRunInput.
type DryRunInput struct {
	ChatID string
	// A saved flow, or a draft definition (validated by the caller).
	Flow       *db.Flow
	Definition *Definition
	WriteCalls []WriteCall
	TriggerID  string
	// The fake trigger payload; a sample is built from the trigger when nil.
	Trigger *jsonx.Object
	Stubs   *jsonx.Object
	Start   string
	TZ      string
	Now     int64
}

// DryStep is DryStep.
type DryStep struct {
	Step   string  `json:"step"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	Branch *string `json:"branch"`
	Input  any     `json:"input"`
	Output any     `json:"output"`
	Error  *string `json:"error"`
	Child  *int64  `json:"child,omitempty"`
}

// DryChild is one each child run inline.
type DryChild struct {
	Index  int64     `json:"index"`
	Status string    `json:"status"`
	Steps  []DryStep `json:"steps"`
}

// DryRunResult is DryRunResult.
type DryRunResult struct {
	RunID    string     `json:"runId"`
	Status   string     `json:"status"`
	Error    *string    `json:"error"`
	Steps    []DryStep  `json:"steps"`
	Vars     any        `json:"vars"`
	Children []DryChild `json:"children"`
	Note     string     `json:"note,omitempty"`
}

// SamplePayload is samplePayload: a trigger payload that looks like the real one.
func SamplePayload(q db.Q, def *Definition, flow *db.Flow, triggerID, tz string, now int64) *jsonx.Object {
	var t *Trigger
	if triggerID != "" {
		t = def.Trigger(triggerID)
	} else if len(def.Triggers) > 0 {
		t = &def.Triggers[0]
	}
	if t == nil || t.Kind == "manual" {
		return obj("kind", "manual", "args", jsonx.NewMap[any]())
	}
	switch t.Kind {
	case "schedule":
		var state *jsonx.Object
		if flow != nil {
			if rows, err := TriggersOf(q, flow.ID); err == nil {
				for _, r := range rows {
					if r.TriggerID == t.ID {
						state = r.SourceState
					}
				}
			}
		}
		var occ *Occurrence
		if list, err := Upcoming(ScheduleInput(t, tz, state), Cursor{At: now - 1}, 1); err == nil && len(list) > 0 {
			occ = &list[0]
		}
		at := now
		label, key := "Contoh", "dry"
		if occ != nil {
			at, label, key = occ.At, occ.Label, occ.Key
		}
		return obj("kind", "schedule", "id", t.ID, "at", float64(at), "time", reminders.FormatClock(at, tz), "date", reminders.FormatDate(at, tz, now), "label", label, "key", key, "fallback", false, "nextAt", nil)
	case "webhook":
		return obj("kind", "webhook", "payload", jsonx.NewMap[any](), "headers", jsonx.NewMap[any](), "receivedAt", float64(now))
	case "chat":
		text := ""
		if t.Command != nil {
			text = *t.Command
		} else if len(t.Phrases) > 0 {
			text = t.Phrases[0]
		}
		return obj("kind", "chat", "text", text, "args", "", "messageId", "dry")
	case "poll":
		return obj("kind", "poll", "value", nil, "previous", nil, "checkedAt", float64(now))
	case "event":
		fid := "draft"
		if flow != nil {
			fid = flow.ID
		}
		return obj("kind", "event", "event", t.Event, "flowId", fid, "runId", "dry", "step", nil, "answer", nil, "error", nil, "depth", 1.0)
	}
	return obj("kind", "manual", "args", jsonx.NewMap[any]())
}

func dryStepsOf(q db.Q, runID string, child *int64) ([]DryStep, error) {
	rows, err := StepRunsOf(q, runID)
	if err != nil {
		return nil, err
	}
	out := []DryStep{}
	for _, s := range rows {
		out = append(out, DryStep{Step: s.StepID, Kind: s.Kind, Status: s.Status, Branch: s.Branch, Input: s.Input, Output: s.Output, Error: s.Error, Child: child})
	}
	return out, nil
}

// DryRunFlow is dryRunFlow.
func DryRunFlow(e *Engine, in DryRunInput) (*DryRunResult, error) {
	q := e.o.DB
	var version *db.FlowVersion
	def := in.Definition
	if in.Flow != nil {
		v, d, err := currentDef(q, in.Flow)
		if err != nil {
			return nil, err
		}
		version = v
		if def == nil {
			def = d
		}
	}
	if def == nil {
		return nil, errors.New("Pass a saved flow or a definition.")
	}
	view := FlowView{ID: "draft_" + in.ChatID, Name: def.Name, TZ: in.TZ, ChatID: in.ChatID, State: db.FlowState{Values: jsonx.NewMap[any](), Tainted: []string{}}, Enabled: true}
	if in.Flow != nil {
		view.ID, view.TZ, view.State, view.AgentID = in.Flow.ID, in.Flow.TZ, in.Flow.State, in.Flow.AgentID
	}
	if def.TZ != nil {
		view.TZ = *def.TZ
	}
	vv := versionView{ID: "draft", WriteCalls: in.WriteCalls}
	if in.Definition == nil && version != nil {
		vv.ID = version.ID
	}
	if vv.WriteCalls == nil {
		if version != nil {
			vv.WriteCalls = version.WriteCalls
		} else {
			vv.WriteCalls = []WriteCall{}
		}
	}
	payload := in.Trigger
	if payload == nil {
		payload = SamplePayload(q, def, in.Flow, in.TriggerID, view.TZ, in.Now)
	}
	start := in.Start
	if start == "" {
		start = StartOf(def, in.TriggerID)
	}
	triggerKind := "manual"
	var trig *Trigger
	if in.TriggerID != "" {
		trig = def.Trigger(in.TriggerID)
	} else if len(def.Triggers) > 0 {
		trig = &def.Triggers[0]
	}
	if trig != nil {
		triggerKind = trig.Kind
	}
	var triggerID *string
	if in.TriggerID != "" {
		triggerID = &in.TriggerID
	} else if len(def.Triggers) > 0 {
		triggerID = &def.Triggers[0].ID
	}
	taint := []string{}
	if triggerKind == "webhook" {
		taint = []string{"trigger"}
	}
	var scheduledAt *int64
	if at, ok := objNum(payload, "at"); ok {
		scheduledAt = ptr(int64(at))
	}
	insert := func(vars RunVars, cursor string, parent *string, item *int64, t []string) (string, error) {
		id := secret.NewID("flr")
		_, err := q.Exec("INSERT INTO flow_runs (id, flow_id, version_id, chat_id, trigger_id, trigger_kind, run_key, parent_run_id, item_index, status, trigger, vars, taint, cursor, steps_done, messages_sent, scheduled_at, started_at, dry_run, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', ?, ?, ?, ?, 0, 0, ?, ?, 1, ?)",
			id, view.ID, vv.ID, in.ChatID, triggerID, triggerKind, "dry:"+id, parent, item, db.JSON[any]{V: payload}, db.JSON[any]{V: vars.Value()}, db.JSON[[]string]{V: t}, cursor, scheduledAt, in.Now, in.Now)
		return id, err
	}
	runID, err := insert(RunVars{Steps: jsonx.NewMap[any](), Vars: jsonx.NewMap[any]()}, start, nil, nil, taint)
	if err != nil {
		return nil, err
	}
	opts := ExecOptions{Dry: true, Stubs: in.Stubs, Def: def, Flow: &view, Version: &vv}
	run, err := e.Execute(runID, opts)
	if err != nil {
		return nil, err
	}
	steps, err := dryStepsOf(q, runID, nil)
	if err != nil {
		return nil, err
	}
	res := &DryRunResult{RunID: runID, Status: "failed", Steps: steps, Children: []DryChild{}}
	var runVars RunVars
	if run != nil {
		res.Status, res.Error, res.Vars = run.Status, run.Error, run.Vars
		runVars = DecodeVars(run.Vars)
	} else {
		runVars = RunVars{Steps: jsonx.NewMap[any](), Vars: jsonx.NewMap[any]()}
	}
	// An each in a dry run: the first 3 children run inline, in order.
	for _, s := range steps {
		if s.Kind != "each" {
			continue
		}
		items, _ := objGet(s.Output, "dryItems").([]any)
		if len(items) > DryEachLimit {
			items = items[:DryEachLimit]
		}
		body := def.Step(s.Step)
		if body == nil || body.Kind != "each" {
			continue
		}
		for _, it := range items {
			idxF, _ := objNum(it, "index")
			idx := int64(idxF)
			vars := runVars.clone()
			vars.HasItem, vars.Item, vars.Index = true, objGet(it, "item"), idxF
			t := taint
			if objGet(it, "taint") == true {
				t = append(append([]string{}, taint...), "item")
			}
			childID, err := insert(vars, body.Body, &runID, &idx, t)
			if err != nil {
				return nil, err
			}
			child, err := e.Execute(childID, opts)
			if err != nil {
				return nil, err
			}
			status := "failed"
			if child != nil {
				status = child.Status
			}
			cs, err := dryStepsOf(q, childID, &idx)
			if err != nil {
				return nil, err
			}
			res.Children = append(res.Children, DryChild{Index: idx, Status: status, Steps: cs})
		}
	}
	return res, nil
}
