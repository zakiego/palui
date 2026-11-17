package flows

// The flow engine (engine.ts, RFC flows 7): the part of the scheduler sweep
// that fires flow triggers and runs flow runs. Every claim is a guarded
// UPDATE, so two sweeps (or a restart in the middle of one) never act twice:
//
//	prune    retention, once a day
//	resume   flows whose pause ended
//	refresh  schedule tool sources (async, lease + backoff)
//	fire     due schedule triggers: the run row (keyed by occurrence) is
//	         inserted in the same transaction as the trigger's cursor advance
//	leases   runs a dead process left running get queued again
//	lifetime runs older than 8 days end as cancelled
//	polls    poll triggers (async, lease + backoff)
//	wake     queued and waiting runs whose wake_at passed, claimed with a lease
//
// A step finishes in one transaction: its step row, its output into vars, any
// outbox row, any flow state write and the cursor move.
//
// Engine implements reminders.Extension, Recoverer and Idler.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

const (
	RunLeaseMs           = 2 * 60_000
	LateNoteMs           = 2 * 60_000
	OutboxTTLMs          = 24 * 3600_000
	SilentToken          = "NOTHING"
	RefreshLeaseMs       = 5 * 60_000
	NoticeAfterFailures  = 3
	promptLeaseTimeoutMs = 180_000
)

var (
	ReadRetryBackoffSec = []int64{30, 120}
	RefreshBackoffMs    = []int64{60_000, 5 * 60_000, 15 * 60_000, 30 * 60_000, 60 * 60_000}
)

// PromptRequest is PromptRequest: a flow prompt step's unattended turn.
type PromptRequest struct {
	ChatID   string
	AgentID  *string
	FlowID   string
	FlowName string
	StepID   string
	// Pre-assigned turn run id, stored on the step row before the call.
	TurnRunID string
	Prompt    string
	Tools     []string
	Send      bool
	// "default" or "fast".
	ModelRole string
	Tainted   bool
}

// PromptResponse is PromptResponse.
type PromptResponse struct {
	Status  string
	Text    string
	RunID   string
	Tainted bool
}

// EngineOptions is FlowEngineOptions.
type EngineOptions struct {
	DB  db.Q
	Log *logx.Logger
	// Unix ms; default time.Now.
	Clock func() int64
	// Tool steps and polls: resolve the class first, then call.
	Tools FlowToolCaller
	// Schedule tool sources: a read-only tool caller (tools.ConnectorToolCaller).
	ReadSource tools.ToolCaller
	// Whether a source's connector returns outside text (taints the run).
	SourceUntrusted func(connectorID string) bool
	// Prompt steps: an unattended turn with trigger "flow".
	RunPrompt func(ctx context.Context, r PromptRequest) (PromptResponse, error)
	// Serial runs the async work a sweep starts (refreshes, polls, runs) one
	// after another at the end of Sweep, in the order it was started, instead
	// of in goroutines. Tests and goldens use it for a deterministic order.
	Serial bool
}

// Engine is the flow engine.
type Engine struct {
	o          EngineOptions
	log        *logx.Logger
	mu         sync.Mutex
	executing  map[string]bool
	refreshing map[string]bool
	enqueued   int
	wg         sync.WaitGroup
	queue      []func()
	lastPrune  int64
}

// NewEngine is createFlowEngine.
func NewEngine(o EngineOptions) *Engine {
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	log := o.Log
	if log == nil {
		log = logx.Silent
	}
	return &Engine{o: o, log: log, executing: map[string]bool{}, refreshing: map[string]bool{}}
}

func (e *Engine) clock() int64 { return e.o.Clock() }

// spawn runs fn in the background (or queues it in serial mode).
func (e *Engine) spawn(fn func()) {
	e.wg.Add(1)
	run := func() {
		defer e.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				e.log.Error("flow task crashed", logx.Fields{"err": fmt.Sprint(r)})
			}
		}()
		fn()
	}
	if e.o.Serial {
		e.mu.Lock()
		e.queue = append(e.queue, run)
		e.mu.Unlock()
		return
	}
	go run()
}

func (e *Engine) drainQueue() {
	for {
		e.mu.Lock()
		if len(e.queue) == 0 {
			e.mu.Unlock()
			return
		}
		fn := e.queue[0]
		e.queue = e.queue[1:]
		e.mu.Unlock()
		fn()
	}
}

// Idle waits for every refresh, poll and run the sweeps started.
func (e *Engine) Idle() {
	e.drainQueue()
	e.wg.Wait()
}

func (e *Engine) setFlag(m map[string]bool, id string, on bool) {
	e.mu.Lock()
	if on {
		m[id] = true
	} else {
		delete(m, id)
	}
	e.mu.Unlock()
}

func (e *Engine) hasFlag(m map[string]bool, id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return m[id]
}

// --- helpers ------------------------------------------------------------------------------

func jsonBytes(v any) int { return len(CompactJSON(v)) }

// CapIO is capIo: a value for a step log column, as is when small, else a
// marked preview.
func CapIO(v any, maxBytes int) any {
	if isUndef(v) {
		return nil
	}
	if maxBytes == 0 {
		maxBytes = Limits.StepIOBytes
	}
	s := CompactJSON(v)
	if len(s) <= maxBytes {
		return v
	}
	return obj("$truncated", true, "preview", sliceUnits(s, maxBytes/2))
}

func sliceUnits(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strxSlice16(s, n)
}

func (e *Engine) send(q db.Q, chatID string, payload db.OutboxPayload, source string, now int64, expiresAt *int64) (string, error) {
	chat, ok, err := chats.Get(q, chatID)
	if err != nil || !ok {
		return "", err
	}
	exp := now + OutboxTTLMs
	if expiresAt != nil {
		exp = *expiresAt
	}
	exp = max(exp, now+60_000)
	row, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: payload, Source: source, ExpiresAt: &exp}, now)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	e.enqueued++
	e.mu.Unlock()
	return row.ID, nil
}

func (e *Engine) flushOutbox() {
	e.mu.Lock()
	n := e.enqueued
	e.enqueued = 0
	e.mu.Unlock()
	if n > 0 {
		outbox.Notify()
	}
}

// RunSource is runSource.
func RunSource(runID string) string { return "flow:" + runID }

// flowAgent: the flow's agent (nil means the default one) and its recall scope.
func (e *Engine) flowAgent(q db.Q, flowID, chatID string, agentID *string, now int64) (string, *connectors.MemoryScope) {
	var agent db.Agent
	found := false
	if agentID != nil {
		if a, ok, err := agents.Get(q, *agentID); err == nil && ok {
			agent, found = a, true
		}
	}
	if !found {
		a, err := agents.GetDefault(q, now)
		if err != nil {
			return "", nil
		}
		agent = a
	}
	s := agents.Scope(agent, chatID)
	read := s.Read
	if read == nil {
		read = []string{}
	}
	return agent.ID, &connectors.MemoryScope{Container: s.Container, Read: read, Locked: s.Locked}
}

// --- resume ------------------------------------------------------------------------------

func (e *Engine) resume(now int64) (int, error) {
	q := e.o.DB
	rows, err := q.Query("SELECT "+db.FlowCols+" FROM flows WHERE enabled = 0 AND paused_until IS NOT NULL AND paused_until <= ? ORDER BY rowid", now)
	due, err := db.All(rows, err, db.ScanFlow)
	if err != nil {
		return 0, err
	}
	for _, f := range due {
		if _, err := SetFlowPaused(q, f.ID, false, nil, now, ""); err != nil {
			e.log.Error("flow resume failed", logx.Fields{"flow_id": f.ID, "err": err})
			if _, err := q.Exec("UPDATE flows SET paused_until = NULL WHERE id = ?", f.ID); err != nil {
				return len(due), err
			}
			continue
		}
		if err := logEvent(q, events.Input{Kind: "flow.resumed", Message: fmt.Sprintf(`Flow "%s" resumed after its pause ended.`, f.Name), Data: obj("flowId", f.ID)}, now); err != nil {
			return len(due), err
		}
	}
	return len(due), nil
}

// --- refresh (schedule tool sources) -------------------------------------------------------

func (e *Engine) refresh(rowID string, now int64) error {
	q := e.o.DB
	row, err := getTrigger(q, rowID)
	if err != nil || row == nil {
		return err
	}
	flow, err := GetFlow(q, row.FlowID)
	if err != nil || flow == nil {
		return err
	}
	_, def, err := currentDef(q, flow)
	if err != nil {
		return err
	}
	spec := def.Trigger(row.TriggerID)
	if spec == nil || spec.Kind != "schedule" || spec.Source.Kind != "tool" {
		_, err := q.Exec("UPDATE flow_triggers SET next_refresh_at = NULL WHERE id = ?", rowID)
		return err
	}
	src := spec.Source
	agentID, memory := e.flowAgent(q, flow.ID, flow.ChatID, flow.AgentID, now)
	call := func(ctx context.Context, connectorID, tool string, args *jsonx.Object) (any, error) {
		if e.o.ReadSource == nil {
			return nil, fmt.Errorf("No tool caller is wired in this process.")
		}
		return e.o.ReadSource(ctx, connectorID, tool, args, &tools.CallScope{AgentID: agentID, Memory: memory})
	}
	res := RefreshToolSource(context.Background(), src, flow.TZ, row.SourceState, call, now)
	fresh, err := getTrigger(q, rowID)
	if err != nil {
		return err
	}
	freshFlow, err := GetFlow(q, flow.ID)
	if err != nil {
		return err
	}
	// Edited in the meantime: the edit asked for its own refresh.
	if fresh == nil || freshFlow == nil || deref(freshFlow.CurrentVersionID, "") != deref(flow.CurrentVersionID, "") {
		return nil
	}
	state := res.State
	var nextRefreshAt int64
	if res.Error == nil {
		nextRefreshAt = NextLocalTime(src.RefreshAt, flow.TZ, now)
	} else {
		f, ok := objNum(state, "failures")
		failures := int64(f)
		if !ok {
			failures = 1
		}
		nextRefreshAt = now + RefreshBackoffMs[min(int(failures-1), len(RefreshBackoffMs)-1)]
		e.log.Warn("flow source read failed", logx.Fields{"flow_id": flow.ID, "failures": failures, "error": *res.Error})
		cid := src.ConnectorID
		if err := logEvent(q, events.Input{Level: "warn", Kind: "flow.source_failed", ConnectorID: &cid, Message: fmt.Sprintf(`Flow "%s" couldn't read %s.%s (try %d): %s`, flow.Name, src.ConnectorID, src.Tool, failures, *res.Error), Data: obj("flowId", flow.ID)}, now); err != nil {
			return err
		}
		today := LocalDate(now, flow.TZ)
		if failures >= NoticeAfterFailures && objStr(state, "noticeFor") != today && freshFlow.Enabled {
			hasData := len(stateFetched(state)) > 0
			data := "Belum ada data, jadi belum bisa jalan."
			if hasData {
				data = "Sementara pakai jadwal dari data terakhir yang berhasil."
			}
			if _, err := e.send(q, freshFlow.ChatID, db.OutboxPayload{Text: fmt.Sprintf(`⚠️ Flow "%s" gagal ambil data dari %s.%s. %s Aku coba lagi otomatis.`, flow.Name, src.ConnectorID, src.Tool, data)}, "flow-notice:"+flow.ID, now, nil); err != nil {
				return err
			}
			state.Set("noticeFor", today)
		}
	}
	next := *fresh
	next.SourceState, next.LastError, next.NextRefreshAt, next.UpdatedAt = state, res.Error, &nextRefreshAt, now
	if fresh.Status == "idle" && freshFlow.Enabled {
		occ, err := NextScheduled(spec, flow.TZ, state, fresh.Cursor, now)
		if err != nil {
			return err
		}
		next.NextOcc, next.NextFireAt = occ, nil
		if occ != nil {
			next.NextFireAt = ptr(occ.At)
		}
	}
	if _, err := q.Exec("UPDATE flow_triggers SET source_state = ?, last_error = ?, next_refresh_at = ?, updated_at = ?, next_occ = ?, next_fire_at = ? WHERE id = ?",
		db.JSON[any]{V: state}, next.LastError, next.NextRefreshAt, now, occValue(next.NextOcc), next.NextFireAt, rowID); err != nil {
		return err
	}
	e.flushOutbox()
	reminders.NotifyScheduleChanged()
	return nil
}

func (e *Engine) refreshes(now int64) (int, error) {
	q := e.o.DB
	type due struct {
		id, flowID string
		at         int64
	}
	rows, err := q.Query("SELECT id, next_refresh_at, flow_id FROM flow_triggers WHERE kind = 'schedule' AND next_refresh_at IS NOT NULL AND next_refresh_at <= ? ORDER BY rowid", now)
	list, err := db.All(rows, err, func(s db.Scanner) (due, error) {
		var d due
		return d, s.Scan(&d.id, &d.at, &d.flowID)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		flow, err := GetFlow(q, d.flowID)
		if err != nil {
			return n, err
		}
		if flow == nil {
			continue
		}
		lease := now + 6*3600_000
		if flow.Enabled {
			lease = now + RefreshLeaseMs
		}
		changed, err := db.Changes(q.Exec("UPDATE flow_triggers SET next_refresh_at = ? WHERE id = ? AND next_refresh_at = ?", lease, d.id, d.at))
		if err != nil {
			return n, err
		}
		if changed == 0 || !flow.Enabled {
			continue
		}
		n++
		id := d.id
		e.setFlag(e.refreshing, id, true)
		e.spawn(func() {
			defer func() {
				e.setFlag(e.refreshing, id, false)
				reminders.NotifyScheduleChanged()
			}()
			if err := e.refresh(id, now); err != nil {
				e.log.Error("flow refresh crashed", logx.Fields{"trigger_id": id, "err": err})
			}
		})
	}
	return n, nil
}

// --- fire (schedule triggers) --------------------------------------------------------------

// ScheduleSourceTaint is the taint a schedule run starts with. A tool source
// in items mode only hands back timestamps: the label and key come from the
// definition, and at, time, date and nextAt are formatted by Palui from a
// parsed time, so no text from the tool reaches the payload. A list source
// can copy a label (and so the key) out of the tool's data, which keeps the
// whole trigger tainted when the connector is untrusted.
func ScheduleSourceTaint(src Source, untrusted bool) Taint {
	if src.Kind != "tool" || !untrusted {
		return []string{}
	}
	if src.List == nil || src.List.Label == nil {
		return []string{}
	}
	return []string{"trigger"}
}

func (e *Engine) schedulePayload(flow *db.Flow, t *Trigger, occ Occurrence, following *Occurrence, now int64) *jsonx.Object {
	var nextAt any
	if following != nil {
		nextAt = float64(following.At)
	}
	return obj("kind", "schedule", "id", t.ID, "at", float64(occ.At), "time", reminders.FormatClock(occ.At, flow.TZ), "date", reminders.FormatDate(occ.At, flow.TZ, now), "label", occ.Label, "key", occ.Key, "fallback", occ.Fallback, "nextAt", nextAt)
}

func startDeadline(def *Definition, start string, occ Occurrence, following *Occurrence) *int64 {
	s := def.Step(start)
	if s == nil || s.Kind != "checkin" {
		return nil
	}
	d := s.Deadline
	switch {
	case d.Kind == "next":
		if following != nil {
			return ptr(following.At + int64(deref(d.PlusMin, 0))*60_000)
		}
		return ptr(occ.At + 24*3600_000)
	case d.Kind == "after" && d.Minutes != nil:
		return ptr(occ.At + int64(*d.Minutes)*60_000)
	}
	// A computed deadline needs the run; lateGraceMin decides for those.
	return nil
}

func (e *Engine) idleTrigger(q db.Q, id string, now int64) error {
	_, err := q.Exec("UPDATE flow_triggers SET status = 'idle', next_fire_at = NULL, updated_at = ? WHERE id = ?", now, id)
	return err
}

func (e *Engine) fireOne(row db.FlowTrigger, now int64) error {
	q := e.o.DB
	flow, err := GetFlow(q, row.FlowID)
	if err != nil {
		return err
	}
	occ := row.NextOcc
	if flow == nil || occ == nil {
		return e.idleTrigger(q, row.ID, now)
	}
	version, def, err := currentDef(q, flow)
	if err != nil {
		return err
	}
	spec := def.Trigger(row.TriggerID)
	if spec == nil || spec.Kind != "schedule" {
		return e.idleTrigger(q, row.ID, now)
	}
	if _, ok, err := chats.Get(q, flow.ChatID); err != nil {
		return err
	} else if !ok {
		if err := e.idleTrigger(q, row.ID, now); err != nil {
			return err
		}
		if _, err := q.Exec("UPDATE flows SET enabled = 0, last_error = ?, updated_at = ? WHERE id = ?", "The chat no longer exists.", now, flow.ID); err != nil {
			return err
		}
		return logEvent(q, events.Input{Level: "warn", Kind: "flow.orphan", Message: fmt.Sprintf(`Flow "%s" was paused: its chat %s no longer exists.`, flow.Name, flow.ChatID)}, now)
	}
	cursor := Cursor{At: occ.At, Key: occ.Key}
	following, err := NextOccurrenceAfter(ScheduleInput(spec, flow.TZ, row.SourceState), cursor)
	if err != nil {
		return err
	}
	deadline := startDeadline(def, spec.Start, *occ, following)
	var late bool
	if deadline != nil {
		late = *deadline <= now
	} else {
		late = now-occ.At > int64(deref(spec.LateGraceMin, 60))*60_000
	}
	payload := e.schedulePayload(flow, spec, *occ, following, now)
	untrusted := e.o.SourceUntrusted != nil && e.o.SourceUntrusted(spec.Source.ConnectorID)
	sourceTaint := ScheduleSourceTaint(spec.Source, untrusted)
	return q.Tx(func(tx db.Q) error {
		reason := ""
		if late {
			reason = "late"
		} else {
			var err error
			if reason, err = RunLimitReason(tx, flow, def, now); err != nil {
				return err
			}
		}
		tid := spec.ID
		in := InsertRunInput{StartInput: StartInput{FlowID: flow.ID, TriggerID: &tid, TriggerKind: "schedule", RunKey: occ.Key, Payload: payload, Taint: sourceTaint, ScheduledAt: ptr(occ.At), Now: now}, Flow: flow, Version: version}
		if reason != "" {
			in.Status, in.SkipReason = "skipped", &reason
		}
		run, err := InsertFlowRun(tx, in)
		if err != nil {
			return err
		}
		if run == nil {
			e.log.Info("flow occurrence already handled, not running again", logx.Fields{"flow_id": flow.ID, "key": occ.Key})
		}
		next, err := NextScheduled(spec, flow.TZ, row.SourceState, &cursor, now)
		if err != nil {
			return err
		}
		var nextAt *int64
		if next != nil {
			nextAt = ptr(next.At)
		}
		_, err = tx.Exec("UPDATE flow_triggers SET status = 'idle', cursor = ?, next_fire_at = ?, next_occ = ?, updated_at = ? WHERE id = ? AND status = 'firing'", cursorValue(&cursor), nextAt, occValue(next), now, row.ID)
		return err
	})
}

func (e *Engine) fire(now int64) (int, error) {
	q := e.o.DB
	n := 0
	for round := 0; round < 200; round++ {
		e.mu.Lock()
		var skip []string
		for id := range e.refreshing {
			skip = append(skip, id)
		}
		e.mu.Unlock()
		sql := "UPDATE flow_triggers SET status = 'firing', updated_at = ? WHERE kind = 'schedule' AND status = 'idle' AND next_fire_at IS NOT NULL AND next_fire_at <= ?"
		args := []any{now, now}
		if len(skip) > 0 {
			sql += " AND id NOT IN (" + placeholders(len(skip)) + ")"
			args = append(args, anyArgs(skip)...)
		}
		rows, err := q.WQuery(sql+" RETURNING "+db.FlowTriggerCols, args...)
		claimed, err := db.All(rows, err, db.ScanFlowTrigger)
		if err != nil {
			return n, err
		}
		if len(claimed) == 0 {
			break
		}
		for _, row := range claimed {
			n++
			if err := e.fireOne(row, now); err != nil {
				e.log.Error("flow fire failed", logx.Fields{"trigger_id": row.ID, "err": err})
				cursor := Cursor{At: now}
				if row.NextOcc != nil {
					cursor = Cursor{At: row.NextOcc.At, Key: row.NextOcc.Key}
				}
				if _, err2 := q.Exec("UPDATE flow_triggers SET status = 'idle', cursor = ?, next_fire_at = NULL, next_occ = NULL, last_error = ?, updated_at = ? WHERE id = ?", cursorValue(&cursor), errorText(err), now, row.ID); err2 != nil {
					return n, err2
				}
			}
		}
	}
	return n, nil
}

// --- polls ---------------------------------------------------------------------------------

func (e *Engine) poll(rowID string, now int64) error {
	q := e.o.DB
	row, err := getTrigger(q, rowID)
	if err != nil || row == nil {
		return err
	}
	flow, err := GetFlow(q, row.FlowID)
	if err != nil || flow == nil {
		return err
	}
	_, def, err := currentDef(q, flow)
	if err != nil {
		return err
	}
	spec := def.Trigger(row.TriggerID)
	if spec == nil || spec.Kind != "poll" {
		return nil
	}
	prev := row.PollState
	turn, err := e.turnFor(q, "poll_"+row.ID, flow.ID, flow.ChatID, flow.TZ, flow.AgentID, now)
	if err != nil {
		return err
	}
	var value any
	untrusted := false
	callErr := func() error {
		if e.o.Tools == nil {
			return fmt.Errorf("No tool caller is wired in this process.")
		}
		info, err := e.o.Tools.Resolve(context.Background(), spec.Tool, deref(spec.ConnectorID, ""), turn)
		if err != nil {
			return err
		}
		if info == nil {
			return fmt.Errorf("Tool %s isn't available (connector off or tool removed).", spec.Tool)
		}
		if info.Class != "read" {
			return fmt.Errorf("%s is now %s; a poll only calls read tools.", info.Name, info.Class)
		}
		untrusted = info.Untrusted
		out, err := e.o.Tools.Call(context.Background(), *info, spec.Args, turn, 0)
		if err != nil {
			return err
		}
		data, err := StepData(out)
		if err != nil {
			return err
		}
		if spec.Select != "" {
			value = GetIn(data, splitPath(spec.Select))
		} else {
			value = data
		}
		return nil
	}()
	if callErr != nil {
		f, _ := objNum(prev, "failures")
		failures := int64(f) + 1
		msg := errorText(callErr)
		state := spread(prev)
		state.Set("lastCheckedAt", float64(now))
		state.Set("failures", float64(failures))
		state.Set("lastError", msg)
		e.log.Warn("flow poll failed", logx.Fields{"flow_id": flow.ID, "failures": failures, "error": msg})
		if err := logEvent(q, events.Input{Level: "warn", Kind: "flow.source_failed", Message: fmt.Sprintf(`Flow "%s" couldn't poll %s (try %d): %s`, flow.Name, spec.Tool, failures, msg), Data: obj("flowId", flow.ID)}, now); err != nil {
			return err
		}
		today := LocalDate(now, flow.TZ)
		if failures >= NoticeAfterFailures && objStr(prev, "noticeFor") != today && flow.Enabled {
			if _, err := e.send(q, flow.ChatID, db.OutboxPayload{Text: fmt.Sprintf(`⚠️ Flow "%s" gagal ngecek %s %d kali berturut-turut: %s. Aku coba lagi otomatis.`, flow.Name, spec.Tool, failures, msg), Format: "plain"}, "flow-notice:"+flow.ID, now, nil); err != nil {
				return err
			}
			state.Set("noticeFor", today)
		}
		backoff := RefreshBackoffMs[min(int(failures-1), len(RefreshBackoffMs)-1)]
		if _, err := q.Exec("UPDATE flow_triggers SET poll_state = ?, last_error = ?, next_fire_at = ?, status = 'idle', updated_at = ? WHERE id = ?", db.JSON[any]{V: state}, msg, now+max(backoff, 60_000), now, rowID); err != nil {
			return err
		}
		e.flushOutbox()
		return nil
	}
	state := spread(prev)
	state.Set("lastCheckedAt", float64(now))
	state.Set("failures", 0.0)
	state.Set("lastError", nil)
	preview := CompactJSON(value)
	sum := sha256.Sum256([]byte(preview))
	hash := hex.EncodeToString(sum[:])
	changed := hash != objStr(prev, "lastHash")
	fire := jsonx.Truthy(objGet(prev, "seeded")) && changed && (spec.Fire == "change" || Truthy(value))
	err = q.Tx(func(tx db.Q) error {
		if fire {
			var previous any
			if p := objStr(prev, "lastValuePreview"); p != "" {
				if v, err := jsonx.Parse([]byte(p)); err == nil {
					previous = v
				} else {
					previous = p
				}
			}
			taint := []string{}
			if untrusted {
				taint = []string{"trigger"}
			}
			tid := spec.ID
			if _, err := StartFlowRun(tx, StartInput{FlowID: flow.ID, TriggerID: &tid, TriggerKind: "poll", RunKey: fmt.Sprintf("poll:%s:%d:%s", spec.ID, now, hash[:16]), Payload: obj("kind", "poll", "value", value, "previous", previous, "checkedAt", float64(now)), Taint: taint, Now: now}); err != nil {
				return err
			}
		}
		next := spread(state)
		next.Set("lastHash", hash)
		if len(preview) <= 4000 {
			next.Set("lastValuePreview", preview)
		} else {
			next.Set("lastValuePreview", nil)
		}
		next.Set("seeded", true)
		_, err := tx.Exec("UPDATE flow_triggers SET poll_state = ?, last_error = NULL, next_fire_at = ?, status = 'idle', updated_at = ? WHERE id = ?", db.JSON[any]{V: next}, now+int64(spec.EveryMin)*60_000, now, rowID)
		return err
	})
	if err != nil {
		return err
	}
	e.flushOutbox()
	reminders.NotifyScheduleChanged()
	return nil
}

func splitPath(p string) []any {
	var out []any
	for _, s := range splitDots(p) {
		out = append(out, s)
	}
	return out
}

func (e *Engine) polls(now int64) (int, error) {
	q := e.o.DB
	type due struct {
		id, flowID string
		at         int64
	}
	rows, err := q.Query("SELECT id, next_fire_at, flow_id FROM flow_triggers WHERE kind = 'poll' AND status = 'idle' AND next_fire_at IS NOT NULL AND next_fire_at <= ? ORDER BY rowid", now)
	list, err := db.All(rows, err, func(s db.Scanner) (due, error) {
		var d due
		return d, s.Scan(&d.id, &d.at, &d.flowID)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		flow, err := GetFlow(q, d.flowID)
		if err != nil {
			return n, err
		}
		if flow == nil || !flow.Enabled {
			continue
		}
		// The lease: a crash mid-call leaves it to expire and poll again.
		changed, err := db.Changes(q.Exec("UPDATE flow_triggers SET status = 'firing', next_fire_at = ? WHERE id = ? AND status = 'idle' AND next_fire_at = ?", now+RefreshLeaseMs, d.id, d.at))
		if err != nil {
			return n, err
		}
		if changed == 0 {
			continue
		}
		n++
		id := d.id
		e.spawn(func() {
			defer func() {
				if _, err := q.Exec("UPDATE flow_triggers SET status = 'idle' WHERE id = ? AND status = 'firing'", id); err != nil {
					e.log.Error("flow poll release failed", logx.Fields{"trigger_id": id, "err": err})
				}
				reminders.NotifyScheduleChanged()
			}()
			if err := e.poll(id, now); err != nil {
				e.log.Error("flow poll crashed", logx.Fields{"trigger_id": id, "err": err})
			}
		})
	}
	return n, nil
}

// --- wake and execute ----------------------------------------------------------------------

func (e *Engine) wake(now int64) (int, error) {
	q := e.o.DB
	type due struct {
		id, flowID, status string
		lease              *int64
	}
	rows, err := q.Query("SELECT id, flow_id, lease_until, status FROM flow_runs WHERE status IN ('queued', 'waiting') AND wake_at IS NOT NULL AND wake_at <= ? AND dry_run = 0 ORDER BY wake_at ASC, rowid ASC LIMIT 100", now)
	list, err := db.All(rows, err, func(s db.Scanner) (due, error) {
		var d due
		return d, s.Scan(&d.id, &d.flowID, &d.lease, &d.status)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	runningBy := map[string]int{}
	for _, d := range list {
		if e.hasFlag(e.executing, d.id) {
			continue
		}
		if _, ok := runningBy[d.flowID]; !ok {
			var c int
			if err := q.QueryRow("SELECT count(*) FROM flow_runs WHERE flow_id = ? AND status = 'running'", d.flowID).Scan(&c); err != nil {
				return n, err
			}
			runningBy[d.flowID] = c
		}
		// Queued runs wait their turn; waiting ones (a check-in re-ask) always wake.
		if d.status == "queued" && runningBy[d.flowID] >= Limits.RunningPerFlow {
			continue
		}
		sql := "UPDATE flow_runs SET status = 'running', lease_until = ?, updated_at = ? WHERE id = ? AND status = ? AND "
		args := []any{now + RunLeaseMs, now, d.id, d.status}
		if d.lease == nil {
			sql += "lease_until IS NULL"
		} else {
			sql += "lease_until = ?"
			args = append(args, *d.lease)
		}
		claimed, err := db.Changes(q.Exec(sql, args...))
		if err != nil {
			return n, err
		}
		if claimed == 0 {
			continue
		}
		runningBy[d.flowID]++
		n++
		id := d.id
		e.setFlag(e.executing, id, true)
		e.spawn(func() {
			defer func() {
				e.setFlag(e.executing, id, false)
				reminders.NotifyScheduleChanged()
			}()
			if _, err := e.Execute(id, ExecOptions{}); err != nil {
				e.log.Error("flow run crashed", logx.Fields{"run_id": id, "err": err})
			}
		})
	}
	return n, nil
}

// expireLeases: runs left running past their lease by something other than
// this process.
func (e *Engine) expireLeases(now int64) (int, error) {
	q := e.o.DB
	rows, err := q.Query("SELECT id FROM flow_runs WHERE status = 'running' AND lease_until IS NOT NULL AND lease_until <= ? ORDER BY rowid", now-10*60_000)
	ids, err := db.All(rows, err, func(s db.Scanner) (string, error) {
		var id string
		return id, s.Scan(&id)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if e.hasFlag(e.executing, id) {
			continue
		}
		n++
		if _, err := q.Exec("UPDATE flow_runs SET status = 'queued', lease_until = NULL, wake_at = ?, updated_at = ? WHERE id = ? AND status = 'running'", now, now, id); err != nil {
			return n, err
		}
	}
	return n, nil
}

// expireRuns: runs older than their lifetime end as cancelled.
func (e *Engine) expireRuns(now int64) (int, error) {
	n, err := db.Changes(e.o.DB.Exec("UPDATE flow_runs SET status = 'cancelled', error = ?, ended_at = ?, wake_at = NULL, wait = NULL, updated_at = ? WHERE status IN ('queued', 'waiting', 'awaiting_approval') AND started_at <= ?",
		"The run waited more than 8 days.", now, now, now-Limits.RunLifetimeMs))
	return int(n), err
}

func (e *Engine) prune(now int64) (int, error) {
	e.mu.Lock()
	if now-e.lastPrune < 24*3600_000 {
		e.mu.Unlock()
		return 0, nil
	}
	e.lastPrune = now
	e.mu.Unlock()
	r, err := PruneFlows(e.o.DB, now)
	return int(r.IO + r.Runs + r.Dry), err
}

// --- the extension API ---------------------------------------------------------------------

// Recover is recover: rows a crash left mid-fire go back to idle, runs left
// running go back to queued.
func (e *Engine) Recover(now int64) error {
	q := e.o.DB
	if _, err := q.Exec("UPDATE flow_triggers SET status = 'idle', updated_at = ? WHERE status = 'firing'", now); err != nil {
		return err
	}
	_, err := q.Exec("UPDATE flow_runs SET status = 'queued', lease_until = NULL, wake_at = ?, updated_at = ? WHERE status = 'running' AND dry_run = 0", now, now)
	return err
}

// Sweep is sweep: the steps in order; one failing step is logged and the
// others still run. In serial mode the async work runs before it returns.
func (e *Engine) Sweep(now int64) (int, error) {
	setLastSweep(now)
	steps := []struct {
		name string
		fn   func(int64) (int, error)
	}{
		{"prune", e.prune}, {"resume", e.resume}, {"refresh", e.refreshes}, {"fire", e.fire},
		{"leases", e.expireLeases}, {"lifetime", e.expireRuns}, {"polls", e.polls}, {"wake", e.wake},
	}
	n := 0
	for _, s := range steps {
		c, err := func() (c int, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%v", r)
				}
			}()
			return s.fn(now)
		}()
		n += c
		if err != nil {
			e.log.Error(fmt.Sprintf("flow %s step failed", s.name), logx.Fields{"err": err})
		}
	}
	e.flushOutbox()
	if e.o.Serial {
		e.drainQueue()
	}
	return n, nil
}

// NextDue is nextDue: the soonest due time of any trigger, refresh, pause end
// or run wake.
func (e *Engine) NextDue() (int64, bool, error) {
	q := e.o.DB
	queries := []string{
		"SELECT min(next_fire_at) FROM flow_triggers WHERE status = 'idle' AND kind = 'schedule'",
		"SELECT min(next_refresh_at) FROM flow_triggers",
		"SELECT min(next_fire_at) FROM flow_triggers WHERE status = 'idle' AND kind = 'poll'",
		"SELECT min(paused_until) FROM flows WHERE enabled = 0",
		"SELECT min(wake_at) FROM flow_runs WHERE status IN ('queued', 'waiting') AND dry_run = 0",
	}
	var best int64
	found := false
	for _, sql := range queries {
		var v *int64
		if err := q.QueryRow(sql).Scan(&v); err != nil {
			return 0, false, err
		}
		if v != nil && (!found || *v < best) {
			best, found = *v, true
		}
	}
	return best, found, nil
}

// StartRun is startRun (run_flow, webhook, chat and event entry points).
func (e *Engine) StartRun(in StartInput) (StartResult, error) {
	return StartFlowRun(e.o.DB, in)
}

var _ reminders.Extension = (*Engine)(nil)
var _ reminders.Recoverer = (*Engine)(nil)
var _ reminders.Idler = (*Engine)(nil)

func newStepID() string { return secret.NewID("fls") }
