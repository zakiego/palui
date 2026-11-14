package db

import "github.com/zakiego/palui/apps/server/internal/jsonx"

// --- flows (M5) -------------------------------------------------------------------------
// The JSON columns keep their TS shapes. Free-form ones (trigger payloads, run
// vars, waits, step input and output, source and poll state) are ordered
// jsonx objects, so a row written by either stack reads back unchanged.

// FlowState is flows.state.
type FlowState struct {
	Values  *jsonx.Object `json:"values"`
	Tainted []string      `json:"tainted"`
}

type Flow struct {
	ID                  string    `json:"id"`
	ChatID              string    `json:"chatId"`
	Name                string    `json:"name"`
	TZ                  string    `json:"tz"`
	Enabled             bool      `json:"enabled"`
	PausedUntil         *int64    `json:"pausedUntil"`
	CurrentVersionID    *string   `json:"currentVersionId"`
	ApprovedVersionID   *string   `json:"approvedVersionId"`
	State               FlowState `json:"state"`
	ConsecutiveFailures int64     `json:"consecutiveFailures"`
	AgentID             *string   `json:"agentId"`
	LegacyRoutineID     *string   `json:"legacyRoutineId"`
	Shadow              bool      `json:"shadow"`
	LastError           *string   `json:"lastError"`
	NoticeFor           *string   `json:"noticeFor"`
	CreatedAt           int64     `json:"createdAt"`
	UpdatedAt           int64     `json:"updatedAt"`
}

const FlowCols = "id, chat_id, name, tz, enabled, paused_until, current_version_id, approved_version_id, state, consecutive_failures, agent_id, legacy_routine_id, shadow, last_error, notice_for, created_at, updated_at"

func ScanFlow(s Scanner) (Flow, error) {
	var f Flow
	var st JSON[FlowState]
	err := s.Scan(&f.ID, &f.ChatID, &f.Name, &f.TZ, &f.Enabled, &f.PausedUntil, &f.CurrentVersionID, &f.ApprovedVersionID, &st, &f.ConsecutiveFailures, &f.AgentID, &f.LegacyRoutineID, &f.Shadow, &f.LastError, &f.NoticeFor, &f.CreatedAt, &f.UpdatedAt)
	f.State = st.V
	if f.State.Values == nil {
		f.State.Values = jsonx.NewMap[any]()
	}
	if f.State.Tainted == nil {
		f.State.Tainted = []string{}
	}
	return f, err
}

// FlowWriteCall is one write call a version can make (validate.ts WriteCall):
// a tool call {stepId, kind: "tool", connectorId, connectorType?, tool, class,
// args} or a message {stepId, kind: "message", to, label?, signature?: false}.
// The field order writes both variants in their TS key order.
type FlowWriteCall struct {
	StepID        string        `json:"stepId"`
	Kind          string        `json:"kind"`
	ConnectorID   string        `json:"connectorId,omitempty"`
	ConnectorType string        `json:"connectorType,omitempty"`
	Tool          string        `json:"tool,omitempty"`
	Class         string        `json:"class,omitempty"`
	Args          *jsonx.Object `json:"args,omitempty"`
	To            string        `json:"to,omitempty"`
	Label         string        `json:"label,omitempty"`
	Signature     *bool         `json:"signature,omitempty"`
}

type FlowVersion struct {
	ID          string          `json:"id"`
	FlowID      string          `json:"flowId"`
	N           int64           `json:"n"`
	Definition  *jsonx.Object   `json:"definition"`
	Hash        string          `json:"hash"`
	WriteCalls  []FlowWriteCall `json:"writeCalls"`
	CreatedBy   string          `json:"createdBy"`
	Note        *string         `json:"note"`
	ApprovedAt  *int64          `json:"approvedAt"`
	ApprovedVia *string         `json:"approvedVia"`
	CreatedAt   int64           `json:"createdAt"`
}

const FlowVersionCols = "id, flow_id, n, definition, hash, write_calls, created_by, note, approved_at, approved_via, created_at"

func ScanFlowVersion(s Scanner) (FlowVersion, error) {
	var v FlowVersion
	var def JSON[*jsonx.Object]
	var wc JSON[[]FlowWriteCall]
	err := s.Scan(&v.ID, &v.FlowID, &v.N, &def, &v.Hash, &wc, &v.CreatedBy, &v.Note, &v.ApprovedAt, &v.ApprovedVia, &v.CreatedAt)
	v.Definition, v.WriteCalls = def.V, wc.V
	if v.WriteCalls == nil {
		v.WriteCalls = []FlowWriteCall{}
	}
	return v, err
}

// FlowOcc is flow_triggers.next_occ.
type FlowOcc struct {
	At       int64  `json:"at"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	Fallback bool   `json:"fallback,omitempty"`
}

// FlowCursor is flow_triggers.cursor (SourceCursor).
type FlowCursor struct {
	At  int64  `json:"at"`
	Key string `json:"key"`
}

type FlowTrigger struct {
	ID            string        `json:"id"`
	FlowID        string        `json:"flowId"`
	TriggerID     string        `json:"triggerId"`
	Kind          string        `json:"kind"`
	Match         *string       `json:"match"`
	Status        string        `json:"status"`
	NextFireAt    *int64        `json:"nextFireAt"`
	NextOcc       *FlowOcc      `json:"nextOcc"`
	Cursor        *FlowCursor   `json:"cursor"`
	SourceState   *jsonx.Object `json:"sourceState"`
	NextRefreshAt *int64        `json:"nextRefreshAt"`
	PollState     *jsonx.Object `json:"pollState"`
	LastError     *string       `json:"lastError"`
	UpdatedAt     int64         `json:"updatedAt"`
}

const FlowTriggerCols = "id, flow_id, trigger_id, kind, match, status, next_fire_at, next_occ, cursor, source_state, next_refresh_at, poll_state, last_error, updated_at"

func ScanFlowTrigger(s Scanner) (FlowTrigger, error) {
	var t FlowTrigger
	var occ NullJSON[FlowOcc]
	var cur NullJSON[FlowCursor]
	var ss, ps JSON[*jsonx.Object]
	err := s.Scan(&t.ID, &t.FlowID, &t.TriggerID, &t.Kind, &t.Match, &t.Status, &t.NextFireAt, &occ, &cur, &ss, &t.NextRefreshAt, &ps, &t.LastError, &t.UpdatedAt)
	if occ.Valid {
		t.NextOcc = &occ.V
	}
	if cur.Valid {
		t.Cursor = &cur.V
	}
	t.SourceState, t.PollState = ss.V, ps.V
	if t.SourceState == nil {
		t.SourceState = jsonx.NewMap[any]()
	}
	if t.PollState == nil {
		t.PollState = jsonx.NewMap[any]()
	}
	return t, err
}

type FlowRun struct {
	ID                 string        `json:"id"`
	FlowID             string        `json:"flowId"`
	VersionID          string        `json:"versionId"`
	ChatID             string        `json:"chatId"`
	TriggerID          *string       `json:"triggerId"`
	TriggerKind        string        `json:"triggerKind"`
	RunKey             string        `json:"runKey"`
	ParentRunID        *string       `json:"parentRunId"`
	ItemIndex          *int64        `json:"itemIndex"`
	Status             string        `json:"status"`
	SkipReason         *string       `json:"skipReason"`
	Trigger            *jsonx.Object `json:"trigger"`
	Vars               *jsonx.Object `json:"vars"`
	Taint              []string      `json:"taint"`
	Cursor             *string       `json:"cursor"`
	Wait               *jsonx.Object `json:"wait"`
	WakeAt             *int64        `json:"wakeAt"`
	LeaseUntil         *int64        `json:"leaseUntil"`
	StepsDone          int64         `json:"stepsDone"`
	MessagesSent       int64         `json:"messagesSent"`
	ScheduledAt        *int64        `json:"scheduledAt"`
	StartedAt          int64         `json:"startedAt"`
	EndedAt            *int64        `json:"endedAt"`
	Error              *string       `json:"error"`
	LegacyOccurrenceID *string       `json:"legacyOccurrenceId"`
	DryRun             bool          `json:"dryRun"`
	UpdatedAt          int64         `json:"updatedAt"`
}

const FlowRunCols = "id, flow_id, version_id, chat_id, trigger_id, trigger_kind, run_key, parent_run_id, item_index, status, skip_reason, trigger, vars, taint, cursor, wait, wake_at, lease_until, steps_done, messages_sent, scheduled_at, started_at, ended_at, error, legacy_occurrence_id, dry_run, updated_at"

func ScanFlowRun(s Scanner) (FlowRun, error) {
	var r FlowRun
	var trig, vars JSON[*jsonx.Object]
	var taint JSON[[]string]
	var wait NullJSON[*jsonx.Object]
	err := s.Scan(&r.ID, &r.FlowID, &r.VersionID, &r.ChatID, &r.TriggerID, &r.TriggerKind, &r.RunKey, &r.ParentRunID, &r.ItemIndex, &r.Status, &r.SkipReason, &trig, &vars, &taint, &r.Cursor, &wait, &r.WakeAt, &r.LeaseUntil, &r.StepsDone, &r.MessagesSent, &r.ScheduledAt, &r.StartedAt, &r.EndedAt, &r.Error, &r.LegacyOccurrenceID, &r.DryRun, &r.UpdatedAt)
	r.Trigger, r.Vars, r.Taint = trig.V, vars.V, taint.V
	if wait.Valid {
		r.Wait = wait.V
	}
	if r.Trigger == nil {
		r.Trigger = jsonx.NewMap[any]()
	}
	if r.Vars == nil {
		r.Vars = jsonx.NewMap[any]()
	}
	if r.Taint == nil {
		r.Taint = []string{}
	}
	return r, err
}

type FlowStepRun struct {
	ID         string  `json:"id"`
	RunID      string  `json:"runId"`
	Seq        int64   `json:"seq"`
	StepID     string  `json:"stepId"`
	Kind       string  `json:"kind"`
	Attempt    int64   `json:"attempt"`
	Status     string  `json:"status"`
	Input      any     `json:"input"`
	Output     any     `json:"output"`
	Tainted    bool    `json:"tainted"`
	Branch     *string `json:"branch"`
	ToolCallID *string `json:"toolCallId"`
	TurnRunID  *string `json:"turnRunId"`
	Error      *string `json:"error"`
	StartedAt  int64   `json:"startedAt"`
	Ms         *int64  `json:"ms"`
}

const FlowStepRunCols = "id, run_id, seq, step_id, kind, attempt, status, input, output, tainted, branch, tool_call_id, turn_run_id, error, started_at, ms"

func ScanFlowStepRun(s Scanner) (FlowStepRun, error) {
	var r FlowStepRun
	var in, out NullJSON[any]
	err := s.Scan(&r.ID, &r.RunID, &r.Seq, &r.StepID, &r.Kind, &r.Attempt, &r.Status, &in, &out, &r.Tainted, &r.Branch, &r.ToolCallID, &r.TurnRunID, &r.Error, &r.StartedAt, &r.Ms)
	r.Input, r.Output = in.V, out.V
	return r, err
}

// JSONValue is a value for a nullable JSON column: SQL NULL for nil.
func JSONValue(v any) NullJSON[any] {
	if v == nil {
		return NullJSON[any]{}
	}
	return SomeJSON(v)
}
