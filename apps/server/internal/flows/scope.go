package flows

// What templates and expressions see during a run (scope.ts, RFC flows 4.2).

import (
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// PreAnswer is an answer the owner gave before the run reached its check-in.
type PreAnswer struct {
	StepID string
	Answer string
	Label  string
	Text   *string
	At     int64
}

func (p PreAnswer) value() *jsonx.Object {
	var text any = undef
	if p.Text != nil {
		text = *p.Text
	}
	return obj("stepId", p.StepID, "answer", p.Answer, "label", p.Label, "text", text, "at", float64(p.At))
}

func decodePreAnswer(v any) *PreAnswer {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil
	}
	at, _ := objNum(o, "at")
	return &PreAnswer{StepID: gstr(o, "stepId"), Answer: gstr(o, "answer"), Label: gstr(o, "label"), Text: gostr(o, "text"), At: int64(at)}
}

// RunVars is a run's vars column: step outputs, run variables, the each item.
type RunVars struct {
	Steps     *jsonx.Object
	Vars      *jsonx.Object
	HasItem   bool
	Item      any
	Index     any
	PreAnswer *PreAnswer
}

// DecodeVars reads flow_runs.vars.
func DecodeVars(o *jsonx.Object) RunVars {
	v := RunVars{Steps: jsonx.NewMap[any](), Vars: jsonx.NewMap[any]()}
	if o == nil {
		return v
	}
	if s := gobj(o, "steps"); s != nil {
		v.Steps = s
	}
	if s := gobj(o, "vars"); s != nil {
		v.Vars = s
	}
	if item, ok := o.Get("item"); ok {
		v.HasItem, v.Item = true, item
		if idx, ok := o.Get("index"); ok {
			v.Index = idx
		}
	}
	if p, ok := o.Get("preAnswer"); ok {
		v.PreAnswer = decodePreAnswer(p)
	}
	return v
}

// Value writes the vars in the TS key order.
func (v RunVars) Value() *jsonx.Object {
	o := obj("steps", cloneObj(v.Steps), "vars", cloneObj(v.Vars))
	if v.HasItem {
		o.Set("item", v.Item)
		o.Set("index", v.Index)
	}
	if v.PreAnswer != nil {
		o.Set("preAnswer", v.PreAnswer.value())
	}
	return o
}

func (v RunVars) clone() RunVars {
	out := v
	out.Steps, out.Vars = cloneObj(v.Steps), cloneObj(v.Vars)
	return out
}

// ScopeInput is ScopeInput.
type ScopeInput struct {
	Run  *db.FlowRun
	Flow FlowView
	Vars RunVars
	Now  int64
}

// FlowView is the part of a flow a run reads (a saved flow or a dry-run draft).
type FlowView struct {
	ID      string
	Name    string
	TZ      string
	ChatID  string
	State   db.FlowState
	AgentID *string
	Enabled bool
}

func viewOf(f *db.Flow) FlowView {
	return FlowView{ID: f.ID, Name: f.Name, TZ: f.TZ, ChatID: f.ChatID, State: f.State, AgentID: f.AgentID, Enabled: f.Enabled}
}

// ScopeOf is scopeOf.
func ScopeOf(c ScopeInput) Scope {
	followUps := 0.0
	if c.Run.Wait != nil {
		if n, ok := objNum(c.Run.Wait, "followUps"); ok {
			followUps = n
		}
	}
	flow := cloneObj(c.Flow.State.Values)
	flow.Set("name", c.Flow.Name)
	flow.Set("id", c.Flow.ID)
	var item, index any
	if c.Vars.HasItem {
		item, index = c.Vars.Item, c.Vars.Index
	}
	trigger := c.Run.Trigger
	if trigger == nil {
		trigger = jsonx.NewMap[any]()
	}
	return Scope{
		"trigger": trigger,
		"steps":   c.Vars.Steps,
		"vars":    c.Vars.Vars,
		"flow":    flow,
		"item":    item,
		"index":   index,
		"now":     NowScope(c.Now, c.Flow.TZ),
		"run":     obj("id", c.Run.ID, "attempt", 1.0, "followUps", followUps),
	}
}
