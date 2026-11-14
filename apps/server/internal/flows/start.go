package flows

// Starting a flow run (start.ts, RFC flows 7.1, 7.5): every entry point
// inserts its run here, keyed (flow, run_key) so a trigger fires at most once
// per key, with the hourly limits checked first. A skipped start is still a
// row, so "why didn't it run" always has an answer.

import (
	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

func approvalsMarkResumed(q db.Q, runID string, now int64) (bool, error) {
	return approvals.MarkResumed(q, runID, now)
}

// Limits are LIMITS (RFC 7.5).
var Limits = struct {
	StepExecutionsPerRun int64
	MessagesPerRun       int64
	RunsPerFlowPerHour   int
	RunsPerHour          int
	RunningPerFlow       int
	FlowDailyTokens      int64
	RunLifetimeMs        int64
	TriggerBytes         int
	VarsBytes            int
	StepIOBytes          int
	FlowStateBytes       int
	AutoPauseAfter       int64
	ChainDepth           int
}{
	StepExecutionsPerRun: 100,
	// A check-in chain (ask, four growing reminders, a last one) is six asks.
	MessagesPerRun:     10,
	RunsPerFlowPerHour: 30,
	RunsPerHour:        120,
	RunningPerFlow:     3,
	FlowDailyTokens:    50_000,
	RunLifetimeMs:      8 * 24 * 3600_000,
	TriggerBytes:       64 * 1024,
	VarsBytes:          256 * 1024,
	StepIOBytes:        8 * 1024,
	FlowStateBytes:     16 * 1024,
	AutoPauseAfter:     5,
	ChainDepth:         3,
}

// StartInput is StartInput.
type StartInput struct {
	FlowID      string
	TriggerID   *string
	TriggerKind string
	RunKey      string
	Payload     *jsonx.Object
	Taint       Taint
	ScheduledAt *int64
	Now         int64
	// The step to start at ("" = the trigger's start).
	Start string
}

// InsertRunInput is InsertRunInput.
type InsertRunInput struct {
	StartInput
	Flow        *db.Flow
	Version     *db.FlowVersion
	Status      string
	SkipReason  *string
	Error       *string
	ParentRunID *string
	ItemIndex   *int64
	Vars        *RunVars
	DryRun      bool
}

func hourCount(q db.Q, now int64, flowID string) (int, error) {
	sql := "SELECT count(*) FROM flow_runs WHERE started_at >= ? AND parent_run_id IS NULL AND dry_run = 0 AND status != 'skipped'"
	args := []any{now - 3600_000}
	if flowID != "" {
		sql += " AND flow_id = ?"
		args = append(args, flowID)
	}
	var n int
	err := q.QueryRow(sql, args...).Scan(&n)
	return n, err
}

// RunLimitReason is runLimitReason: why a new run can't start now, or "".
func RunLimitReason(q db.Q, flow *db.Flow, def *Definition, now int64) (string, error) {
	perFlow := Limits.RunsPerFlowPerHour
	if def.Limits != nil && def.Limits.RunsPerHour != nil {
		perFlow = min(*def.Limits.RunsPerHour, Limits.RunsPerFlowPerHour)
	}
	n, err := hourCount(q, now, flow.ID)
	if err != nil {
		return "", err
	}
	if n >= perFlow {
		return "rate_limited", nil
	}
	all, err := hourCount(q, now, "")
	if err != nil {
		return "", err
	}
	if all >= Limits.RunsPerHour {
		return "rate_limited", nil
	}
	return "", nil
}

// InsertFlowRun is insertFlowRun: nil on a duplicate key.
func InsertFlowRun(q db.Q, in InsertRunInput) (*db.FlowRun, error) {
	def, err := DefinitionOf(in.Version)
	if err != nil {
		return nil, err
	}
	payload := in.Payload
	if payload == nil {
		payload = jsonx.NewMap[any]()
	}
	if s := CompactJSON(payload); len(s) > Limits.TriggerBytes {
		payload = obj("$truncated", true, "preview", strx.Slice16(s, 4000))
	}
	status := in.Status
	if status == "" {
		status = "queued"
	}
	vars := RunVars{Steps: jsonx.NewMap[any](), Vars: jsonx.NewMap[any]()}
	if in.Vars != nil {
		vars = *in.Vars
	}
	taint := in.Taint
	if taint == nil {
		taint = []string{}
	}
	var cursor *string
	var wakeAt, endedAt *int64
	if status == "queued" {
		start := in.Start
		if start == "" {
			if in.TriggerID != nil {
				if t := def.Trigger(*in.TriggerID); t != nil {
					start = t.Start
				}
			}
			if start == "" {
				start = def.Steps[0].ID
			}
		}
		cursor, wakeAt = &start, ptr(in.Now)
	} else {
		endedAt = ptr(in.Now)
	}
	r, ok, err := db.One(q.WQueryRow("INSERT INTO flow_runs (id, flow_id, version_id, chat_id, trigger_id, trigger_kind, run_key, parent_run_id, item_index, status, skip_reason, trigger, vars, taint, cursor, wait, wake_at, lease_until, steps_done, messages_sent, scheduled_at, started_at, ended_at, error, legacy_occurrence_id, dry_run, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, NULL, 0, 0, ?, ?, ?, ?, NULL, ?, ?) ON CONFLICT DO NOTHING RETURNING "+db.FlowRunCols,
		secret.NewID("flr"), in.Flow.ID, in.Version.ID, in.Flow.ChatID, in.TriggerID, in.TriggerKind, in.RunKey, in.ParentRunID, in.ItemIndex, status, in.SkipReason, db.JSON[any]{V: payload}, db.JSON[any]{V: vars.Value()}, db.JSON[[]string]{V: taint}, cursor, wakeAt, in.ScheduledAt, in.Now, endedAt, in.Error, db.Bool(in.DryRun), in.Now), db.ScanFlowRun)
	if err != nil || !ok {
		return nil, err
	}
	return &r, nil
}

// StartResult is startFlowRun's answer: the run (nil on a duplicate key) and
// why it was skipped ("" when queued).
type StartResult struct {
	Run     *db.FlowRun
	Skipped string
}

// StartFlowRun is startFlowRun.
func StartFlowRun(q db.Q, in StartInput) (StartResult, error) {
	flow, err := GetFlow(q, in.FlowID)
	if err != nil || flow == nil {
		return StartResult{}, err
	}
	version, def, err := currentDef(q, flow)
	if err != nil {
		return StartResult{}, err
	}
	reason := ""
	if !flow.Enabled {
		reason = "paused"
	} else if reason, err = RunLimitReason(q, flow, def, in.Now); err != nil {
		return StartResult{}, err
	}
	ins := InsertRunInput{StartInput: in, Flow: flow, Version: version}
	if reason != "" {
		ins.Status, ins.SkipReason = "skipped", &reason
	}
	run, err := InsertFlowRun(q, ins)
	if err != nil {
		return StartResult{}, err
	}
	if run != nil && reason == "" {
		reminders.NotifyScheduleChanged()
	}
	res := StartResult{Run: run, Skipped: reason}
	if reason == "" && run == nil {
		res.Skipped = "duplicate"
	}
	return res, nil
}
