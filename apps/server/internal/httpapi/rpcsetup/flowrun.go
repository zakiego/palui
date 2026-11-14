package rpcsetup

// flowRunDetail of apps/app/src/server/flows.ts: one flow run's step log for
// the run page (getRun with a flr_ id).

import (
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// flowJSON is flows.ts json(): pretty JSON, capped at 6000 UTF-16 units so one
// huge value can't swamp the page.
func flowJSON(v any) string {
	s := pretty(v)
	if strx.Len16(s) > 6000 {
		return strx.Slice16(s, 5999) + "…"
	}
	return s
}

type flowStepView struct {
	Seq       int64   `json:"seq"`
	StepID    string  `json:"stepId"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	Branch    *string `json:"branch"`
	Error     *string `json:"error"`
	Ms        *int64  `json:"ms"`
	Tainted   bool    `json:"tainted"`
	Attempt   int64   `json:"attempt"`
	Input     *string `json:"input"`
	Output    *string `json:"output"`
	TurnRunID *string `json:"turnRunId"`
}

type flowChild struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Item   *int64 `json:"item"`
}

type flowRunView struct {
	ID          string         `json:"id"`
	FlowID      string         `json:"flowId"`
	FlowName    *string        `json:"flowName"`
	Status      string         `json:"status"`
	SkipReason  *string        `json:"skipReason"`
	Trigger     string         `json:"trigger"`
	TriggerKind string         `json:"triggerKind"`
	RunKey      string         `json:"runKey"`
	StartedAt   int64          `json:"startedAt"`
	EndedAt     *int64         `json:"endedAt"`
	ScheduledAt *int64         `json:"scheduledAt"`
	WakeAt      *int64         `json:"wakeAt"`
	Waiting     any            `json:"waiting"`
	Error       *string        `json:"error"`
	Tainted     bool           `json:"tainted"`
	Taint       []string       `json:"taint"`
	ParentRunID *string        `json:"parentRunId"`
	Payload     string         `json:"payload"`
	Steps       []flowStepView `json:"steps"`
	Children    []flowChild    `json:"children"`
}

func jsonOrNil(v any) *string {
	if v == nil {
		return nil
	}
	s := flowJSON(v)
	return &s
}

// flowRunDetail is flowRunDetail(db, id): nil for an unknown id or a dry run.
func flowRunDetail(q db.Q, id string) (*flowRunView, error) {
	r, err := flows.GetRun(q, id)
	if err != nil || r == nil || r.DryRun {
		return nil, err
	}
	f, err := flows.GetFlow(q, r.FlowID)
	if err != nil {
		return nil, err
	}
	v := &flowRunView{ID: r.ID, FlowID: r.FlowID, Status: r.Status, SkipReason: r.SkipReason, TriggerKind: r.TriggerKind, RunKey: r.RunKey, StartedAt: r.StartedAt, EndedAt: r.EndedAt, ScheduledAt: r.ScheduledAt, Error: r.Error, ParentRunID: r.ParentRunID, Taint: r.Taint, Steps: []flowStepView{}, Children: []flowChild{}}
	if f != nil {
		v.FlowName = &f.Name
	}
	v.Trigger = r.TriggerKind
	if r.TriggerID != nil {
		v.Trigger = *r.TriggerID
	}
	if r.Status == "waiting" {
		v.WakeAt = r.WakeAt
		if r.Wait != nil {
			v.Waiting, _ = r.Wait.Get("kind")
		}
	}
	if v.Taint == nil {
		v.Taint = []string{}
	}
	v.Tainted = len(v.Taint) > 0
	if r.Trigger == nil {
		v.Payload = flowJSON(nil)
	} else {
		v.Payload = flowJSON(r.Trigger)
	}
	res, qerr := q.Query("SELECT "+db.FlowStepRunCols+" FROM flow_step_runs WHERE run_id = ? ORDER BY seq", r.ID)
	steps, err := db.All(res, qerr, db.ScanFlowStepRun)
	if err != nil {
		return nil, err
	}
	for _, s := range steps {
		v.Steps = append(v.Steps, flowStepView{Seq: s.Seq, StepID: s.StepID, Kind: s.Kind, Status: s.Status, Branch: s.Branch, Error: s.Error, Ms: s.Ms, Tainted: s.Tainted, Attempt: s.Attempt, Input: jsonOrNil(s.Input), Output: jsonOrNil(s.Output), TurnRunID: s.TurnRunID})
	}
	res, qerr = q.Query("SELECT id, status, item_index FROM flow_runs WHERE parent_run_id = ?", r.ID)
	v.Children, err = db.All(res, qerr, func(s db.Scanner) (flowChild, error) {
		var c flowChild
		return c, s.Scan(&c.ID, &c.Status, &c.Item)
	})
	if err != nil {
		return nil, err
	}
	if v.Children == nil {
		v.Children = []flowChild{}
	}
	return v, nil
}
