package flows

// Executing a run (engine.ts execute, apply, finish, afterFailure): each step
// returns a transition that is applied in one transaction.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

func strxSlice16(s string, n int) string { return strx.Slice16(s, n) }

func splitDots(p string) []string {
	var out []string
	for _, s := range strings.Split(p, ".") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// opt is a field a transition may leave out (TS undefined).
type field[T any] struct {
	Set bool
	V   T
}

func some[T any](v T) field[T] { return field[T]{true, v} }

// stepRow is Transition.row.
type stepRow struct {
	Seq        *int64
	Status     string
	Input      field[any]
	Output     field[any]
	Tainted    field[bool]
	Branch     field[*string]
	Error      field[*string]
	Attempt    int64
	ToolCallID field[*string]
	TurnRunID  field[*string]
	Ms         field[*int64]
}

type outMsg struct {
	chatID    string
	payload   db.OutboxPayload
	expiresAt *int64
	extra     bool
	contactID string
	noSig     bool
}

type childItem struct {
	item  any
	index int64
	key   *string
}

type children struct {
	body    string
	items   []childItem
	tainted bool
}

type flowEvent struct {
	name string
	data EventData
}

// then is Transition.then: goto, wait, fail or end.
type then struct {
	t          string
	to         *string
	wait       *jsonx.Object
	wakeAt     *int64
	status     string
	skipReason *string
	err        *string
}

type transition struct {
	row          stepRow
	output       field[any]
	setVars      *jsonx.Object
	setFlow      *jsonx.Object
	flowTaint    []string
	hasFlowTaint bool
	taint        []string
	messages     []outMsg
	children     *children
	events       []flowEvent
	then         then
}

func gotoStep(to *string) then { return then{t: "goto", to: to} }

// versionView is the part of a version a run reads.
type versionView struct {
	ID         string
	WriteCalls []WriteCall
	ApprovedAt *int64
}

type runCtx struct {
	run     *db.FlowRun
	flow    FlowView
	version versionView
	def     *Definition
	vars    RunVars
	now     int64
	dry     bool
	stubs   *jsonx.Object
}

// ExecOptions is ExecOptions: a dry run passes the definition, flow and
// version it runs (a draft has no rows).
type ExecOptions struct {
	Dry     bool
	Stubs   *jsonx.Object
	Def     *Definition
	Flow    *FlowView
	Version *versionView
}

func (c *runCtx) taintCheck() TaintCheck {
	taint := c.run.Taint
	return func(k string) bool { return KeyTainted(taint, k) }
}

func (c *runCtx) env() EvalEnv {
	return EvalEnv{Scope: ScopeOf(ScopeInput{Run: c.run, Flow: c.flow, Vars: c.vars, Now: c.now}), TZ: c.flow.TZ, Now: c.now}
}

// stepPanic carries an error out of a step; runStep turns it into onError.
type stepPanic struct{ err error }

func must[T any](v T, err error) T {
	if err != nil {
		panic(stepPanic{err})
	}
	return v
}

func checkErr(err error) {
	if err != nil {
		panic(stepPanic{err})
	}
}

// onError is onError: "fail", "continue" or an error branch.
func (c *runCtx) onError(step *Step, msg string, row stepRow, tainted bool) transition {
	how := "fail"
	if step.OnError != nil {
		how = *step.OnError
	}
	output := obj("ok", false, "error", msg)
	row.Status = "error"
	row.Error = some(&msg)
	row.Output = some[any](output)
	row.Tainted = some(row.Tainted.V || tainted)
	var taint []string
	if tainted {
		taint = []string{"steps." + step.ID}
	}
	tr := transition{row: row, output: some[any](output), taint: taint}
	switch how {
	case "fail":
		e := "Step " + step.ID + ": " + msg
		tr.then = then{t: "fail", err: &e}
	case "continue":
		tr.row.Branch = some(ptr("continue"))
		tr.then = gotoStep(DefaultNext(c.def, step.ID))
	default:
		tr.row.Branch = some(ptr("onError:" + how))
		tr.then = gotoStep(&how)
	}
	return tr
}

// decorate adds the late note to the first message of a late schedule run,
// and the stale-times note when the trigger used a fallback.
func (c *runCtx) decorate(text string) string {
	out := text
	at := c.run.ScheduledAt
	if c.run.TriggerKind == "schedule" && at != nil && *at != 0 && c.run.MessagesSent == 0 {
		if c.now-*at > LateNoteMs {
			out += fmt.Sprintf(" (telat, harusnya %s)", reminders.FormatClock(*at, c.flow.TZ))
		}
		if objGet(c.run.Trigger, "fallback") == true {
			out += "\n(Jadwal pakai data terakhir yang berhasil diambil, sumbernya lagi error.)"
		}
	}
	return out
}

func (e *Engine) contextOf(q db.Q, run *db.FlowRun, o ExecOptions, now int64) (*runCtx, error) {
	if o.Def != nil && o.Flow != nil && o.Version != nil {
		return &runCtx{run: run, flow: *o.Flow, version: *o.Version, def: o.Def, vars: DecodeVars(run.Vars), now: now, dry: o.Dry, stubs: o.Stubs}, nil
	}
	flow, err := GetFlow(q, run.FlowID)
	if err != nil {
		return nil, err
	}
	v, err := GetVersion(q, run.VersionID)
	if err != nil {
		return nil, err
	}
	if flow == nil || v == nil {
		return nil, nil
	}
	def, err := DefinitionOf(v)
	if err != nil {
		return nil, err
	}
	return &runCtx{run: run, flow: viewOf(flow), version: versionView{ID: v.ID, WriteCalls: v.WriteCalls, ApprovedAt: v.ApprovedAt}, def: def, vars: DecodeVars(run.Vars), now: now, dry: o.Dry, stubs: o.Stubs}, nil
}

func nextSeq(q db.Q, runID string) (int64, error) {
	var v *int64
	if err := q.QueryRow("SELECT max(seq) FROM flow_step_runs WHERE run_id = ?", runID).Scan(&v); err != nil {
		return 0, err
	}
	return deref(v, 0) + 1, nil
}

func (e *Engine) turnFor(q db.Q, runID, flowID, chatID, tz string, agentID *string, now int64) (connectors.TurnInfo, error) {
	chat, ok, err := chats.Get(q, chatID)
	if err != nil {
		return connectors.TurnInfo{}, err
	}
	channel := ""
	if ok {
		channel = chat.Channel
	}
	agent, memory := e.flowAgent(q, flowID, chatID, agentID, now)
	return connectors.TurnInfo{RunID: runID, ChatID: chatID, Channel: channel, Trigger: "flow", TZ: tz, Now: now, FlowID: flowID, AgentID: agent, Memory: memory}, nil
}

func (e *Engine) emit(q db.Q, name string, run *db.FlowRun, data EventData, now int64) error {
	_, err := EmitFlowEvent(q, name, run, data, now)
	return err
}

// apply applies one step's transition. It returns the run as it stands after,
// and whether execution goes on.
func (e *Engine) apply(c *runCtx, step *Step, tr transition, startedAt int64) (*db.FlowRun, bool, error) {
	q := e.o.DB
	now := e.clock()
	out := c.run
	err := q.Tx(func(tx db.Q) error {
		fresh, err := GetRun(tx, c.run.ID)
		if err != nil {
			return err
		}
		if fresh == nil || fresh.Status != "running" {
			if fresh != nil {
				out = fresh
			}
			return nil
		}
		vars := RunVars{Steps: cloneObj(c.vars.Steps), Vars: cloneObj(c.vars.Vars)}
		if tr.setVars != nil {
			vars.Vars.Assign(tr.setVars)
		}
		if c.vars.HasItem {
			vars.HasItem, vars.Item, vars.Index = true, c.vars.Item, c.vars.Index
		}
		// An early answer lasts until the first check-in step: it takes it or drops it.
		pre := DecodeVars(fresh.Vars).PreAnswer
		if pre == nil {
			pre = c.vars.PreAnswer
		}
		if pre != nil && step.Kind != "checkin" {
			vars.PreAnswer = pre
		}
		if tr.output.Set {
			vars.Steps.Set(step.ID, tr.output.V)
			if jsonBytes(vars.Value()) > Limits.VarsBytes {
				vars.Steps.Set(step.ID, obj("$truncated", true, "preview", strx.Slice16(CompactJSON(tr.output.V), 2000)))
			}
		}
		messagesSent := fresh.MessagesSent
		var outboxIDs []string
		for _, m := range tr.messages {
			if c.dry {
				continue
			}
			if m.contactID != "" {
				contact, ok, err := outbound.GetContact(tx, m.contactID)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				ref, owner := c.run.ID, c.flow.ChatID
				sent, err := outbound.SendNow(tx, contact, m.payload.Text, outbound.SendMeta{Origin: "flow", OriginRef: &ref, ApprovedBy: "flow_version", OwnerChatID: &owner, NoSignature: m.noSig}, 0, now)
				if err != nil {
					return err
				}
				outboxIDs = append(outboxIDs, sent.OutboxID)
				if !m.extra {
					messagesSent++
				}
				continue
			}
			id, err := e.send(tx, m.chatID, m.payload, RunSource(c.run.ID), now, m.expiresAt)
			if err != nil {
				return err
			}
			if id != "" {
				outboxIDs = append(outboxIDs, id)
			}
			if !m.extra {
				messagesSent++
			}
		}
		output := tr.row.Output
		if len(outboxIDs) > 0 && output.Set {
			if o, ok := output.V.(*jsonx.Object); ok {
				if _, has := o.Get("text"); has {
					n := o.Clone()
					n.Set("outboxId", outboxIDs[0])
					output = some[any](n)
				}
			}
		}
		if len(outboxIDs) > 0 && tr.output.Set {
			if _, ok := tr.output.V.(*jsonx.Object); ok {
				cur, _ := vars.Steps.Get(step.ID)
				n := cloneObj(asObject(cur))
				n.Set("outboxId", outboxIDs[0])
				vars.Steps.Set(step.ID, n)
			}
		}
		seq := tr.row.Seq
		if seq != nil {
			sets := []string{"status = ?"}
			args := []any{tr.row.Status}
			if tr.row.Input.Set {
				sets, args = append(sets, "input = ?"), append(args, db.JSONValue(CapIO(tr.row.Input.V, 0)))
			}
			if output.Set {
				sets, args = append(sets, "output = ?"), append(args, db.JSONValue(CapIO(output.V, 0)))
			}
			if tr.row.Tainted.Set {
				sets, args = append(sets, "tainted = ?"), append(args, db.Bool(tr.row.Tainted.V))
			}
			if tr.row.Branch.Set {
				sets, args = append(sets, "branch = ?"), append(args, tr.row.Branch.V)
			}
			if tr.row.Error.Set {
				sets, args = append(sets, "error = ?"), append(args, tr.row.Error.V)
			}
			if tr.row.ToolCallID.Set {
				sets, args = append(sets, "tool_call_id = ?"), append(args, tr.row.ToolCallID.V)
			}
			if tr.row.TurnRunID.Set {
				sets, args = append(sets, "turn_run_id = ?"), append(args, tr.row.TurnRunID.V)
			}
			if tr.row.Status != "waiting" {
				var ms *int64
				if tr.row.Ms.Set && tr.row.Ms.V != nil {
					ms = tr.row.Ms.V
				} else {
					var started int64
					if err := tx.QueryRow("SELECT started_at FROM flow_step_runs WHERE run_id = ? AND seq = ?", c.run.ID, *seq).Scan(&started); err == nil {
						ms = ptr(now - started)
					}
				}
				sets, args = append(sets, "ms = ?"), append(args, ms)
			}
			if _, err := tx.Exec("UPDATE flow_step_runs SET "+strings.Join(sets, ", ")+" WHERE run_id = ? AND seq = ?", append(args, c.run.ID, *seq)...); err != nil {
				return err
			}
		} else {
			n, err := nextSeq(tx, c.run.ID)
			if err != nil {
				return err
			}
			seq = &n
			var ms *int64
			if tr.row.Ms.Set && tr.row.Ms.V != nil {
				ms = tr.row.Ms.V
			} else if tr.row.Status != "waiting" {
				ms = ptr(now - startedAt)
			}
			attempt := tr.row.Attempt
			if attempt == 0 {
				attempt = 1
			}
			var in, outv any = nil, nil
			if tr.row.Input.Set {
				in = CapIO(tr.row.Input.V, 0)
			}
			if output.Set {
				outv = CapIO(output.V, 0)
			}
			if _, err := tx.Exec("INSERT INTO flow_step_runs (id, run_id, seq, step_id, kind, started_at, status, input, output, tainted, branch, error, attempt, tool_call_id, turn_run_id, ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				newStepID(), c.run.ID, n, step.ID, step.Kind, startedAt, tr.row.Status, db.JSONValue(in), db.JSONValue(outv), db.Bool(tr.row.Tainted.V), tr.row.Branch.V, tr.row.Error.V, attempt, tr.row.ToolCallID.V, tr.row.TurnRunID.V, ms); err != nil {
				return err
			}
		}
		// A wait remembers its step row, so the answer or the wake updates it.
		if tr.then.t == "wait" {
			if _, has := tr.then.wait.Get("seq"); !has {
				tr.then.wait.Set("seq", float64(*seq))
			}
		}
		if (tr.setFlow != nil || tr.hasFlowTaint) && !c.dry {
			f, err := GetFlow(tx, c.flow.ID)
			if err != nil {
				return err
			}
			if f != nil {
				values := cloneObj(f.State.Values)
				if tr.setFlow != nil {
					values.Assign(tr.setFlow)
				}
				tainted := append([]string{}, f.State.Tainted...)
				for _, k := range tr.flowTaint {
					if !containsStr(tainted, k) {
						tainted = append(tainted, k)
					}
				}
				state := db.FlowState{Values: values, Tainted: tainted}
				if _, err := tx.Exec("UPDATE flows SET state = ?, updated_at = ? WHERE id = ?", db.JSON[db.FlowState]{V: state}, now, f.ID); err != nil {
					return err
				}
				c.flow.State = state
			}
		} else if tr.setFlow != nil && c.dry {
			values := cloneObj(c.flow.State.Values)
			values.Assign(tr.setFlow)
			c.flow.State = db.FlowState{Values: values, Tainted: c.flow.State.Tainted}
		}
		taint := fresh.Taint
		if len(tr.taint) > 0 {
			taint = AddTaint(fresh.Taint, tr.taint...)
		}
		next := *fresh
		next.Vars, next.Taint, next.MessagesSent, next.UpdatedAt = vars.Value(), taint, messagesSent, now
		if tr.row.Status != "waiting" {
			next.StepsDone++
		}
		th := tr.then
		switch th.t {
		case "goto":
			next.Cursor, next.Wait = th.to, nil
		case "wait":
			status := th.status
			if status == "" {
				status = "waiting"
			}
			next.Status, next.Wait, next.WakeAt, next.LeaseUntil, next.SkipReason = status, th.wait, th.wakeAt, nil, th.skipReason
		case "fail":
			msg := strx.Slice16(*th.err, 300)
			next.Status, next.Cursor, next.Wait, next.WakeAt, next.LeaseUntil, next.Error, next.EndedAt = "failed", nil, nil, nil, nil, &msg, &now
		default:
			next.Status, next.SkipReason, next.Cursor, next.Wait, next.WakeAt, next.LeaseUntil, next.Error, next.EndedAt = th.status, th.skipReason, nil, nil, nil, nil, th.err, &now
		}
		var wait db.NullJSON[any]
		if next.Wait != nil {
			wait = db.SomeJSON[any](next.Wait)
		}
		if _, err := tx.Exec("UPDATE flow_runs SET vars = ?, taint = ?, messages_sent = ?, steps_done = ?, updated_at = ?, cursor = ?, wait = ?, status = ?, wake_at = ?, lease_until = ?, skip_reason = ?, error = ?, ended_at = ? WHERE id = ?",
			db.JSON[any]{V: next.Vars}, db.JSON[[]string]{V: next.Taint}, next.MessagesSent, next.StepsDone, now, next.Cursor, wait, next.Status, next.WakeAt, next.LeaseUntil, next.SkipReason, next.Error, next.EndedAt, c.run.ID); err != nil {
			return err
		}
		out = &next
		if th.t == "fail" && !c.dry {
			if err := e.afterFailure(tx, out, now); err != nil {
				return err
			}
			msg := strx.Slice16(*th.err, 300)
			if err := e.emit(tx, "run.failed", out, EventData{Step: &step.ID, Error: &msg}, now); err != nil {
				return err
			}
		}
		if tr.children != nil && !c.dry {
			flowRow, err := GetFlow(tx, c.flow.ID)
			if err != nil {
				return err
			}
			version, err := GetVersion(tx, c.run.VersionID)
			if err != nil {
				return err
			}
			started, deduped := 0, 0
			if flowRow != nil && version != nil {
				for _, it := range tr.children.items {
					key := fmt.Sprintf("each:%s:%s:%d", c.run.ID, step.ID, it.index)
					if it.key != nil {
						key = "each:" + step.ID + ":" + *it.key
					}
					taint := out.Taint
					if tr.children.tainted {
						taint = AddTaint(out.Taint, "item")
					}
					cv := RunVars{Steps: cloneObj(vars.Steps), Vars: cloneObj(vars.Vars), HasItem: true, Item: it.item, Index: float64(it.index)}
					idx := it.index
					parent := c.run.ID
					child, err := InsertFlowRun(tx, InsertRunInput{
						StartInput:  StartInput{FlowID: flowRow.ID, TriggerID: c.run.TriggerID, TriggerKind: c.run.TriggerKind, RunKey: key, Payload: c.run.Trigger, Taint: taint, ScheduledAt: c.run.ScheduledAt, Now: now, Start: tr.children.body},
						Flow:        flowRow,
						Version:     version,
						ParentRunID: &parent,
						ItemIndex:   &idx,
						Vars:        &cv,
					})
					if err != nil {
						return err
					}
					if child != nil {
						started++
					} else {
						deduped++
					}
				}
			}
			cur, _ := vars.Steps.Get(step.ID)
			counted := cloneObj(asObject(cur))
			counted.Set("started", float64(started))
			counted.Set("deduped", float64(deduped))
			vars.Steps.Set(step.ID, counted)
			next.Vars = vars.Value()
			if _, err := tx.Exec("UPDATE flow_runs SET vars = ? WHERE id = ?", db.JSON[any]{V: next.Vars}, c.run.ID); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE flow_step_runs SET output = ? WHERE run_id = ? AND seq = ?", db.JSONValue(CapIO(counted, 0)), c.run.ID, *seq); err != nil {
				return err
			}
			out = &next
			if started > 0 {
				reminders.NotifyScheduleChanged()
			}
		}
		for _, ev := range tr.events {
			if c.dry {
				continue
			}
			if err := e.emit(tx, ev.name, out, ev.data, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	c.run = out
	c.vars = DecodeVars(out.Vars)
	e.flushOutbox()
	return out, out.Status == "running" && tr.then.t == "goto", nil
}

func (e *Engine) finish(c *runCtx) (*db.FlowRun, error) {
	q := e.o.DB
	now := e.clock()
	out := c.run
	err := q.Tx(func(tx db.Q) error {
		r, ok, err := db.One(tx.WQueryRow("UPDATE flow_runs SET status = 'done', cursor = NULL, wait = NULL, wake_at = NULL, lease_until = NULL, ended_at = ?, updated_at = ? WHERE id = ? AND status = 'running' RETURNING "+db.FlowRunCols, now, now, c.run.ID), db.ScanFlowRun)
		if err != nil || !ok {
			return err
		}
		out = &r
		if c.dry {
			return nil
		}
		if r.ParentRunID == nil {
			if _, err := tx.Exec("UPDATE flows SET consecutive_failures = 0 WHERE id = ?", c.flow.ID); err != nil {
				return err
			}
		}
		return e.emit(tx, "run.done", out, EventData{}, now)
	})
	if err != nil {
		return nil, err
	}
	c.run = out
	return out, nil
}

// afterFailure: one failure notice per flow per day, and an auto-pause after
// five failed runs in a row.
func (e *Engine) afterFailure(q db.Q, run *db.FlowRun, now int64) error {
	f, err := GetFlow(q, run.FlowID)
	if err != nil || f == nil {
		return err
	}
	failures := f.ConsecutiveFailures + 1
	if _, err := q.Exec("UPDATE flows SET consecutive_failures = ?, last_error = ?, updated_at = ? WHERE id = ?", failures, run.Error, now, f.ID); err != nil {
		return err
	}
	why := "unknown error"
	short := "error"
	if run.Error != nil {
		why, short = *run.Error, *run.Error
	}
	if err := logEvent(q, events.Input{Level: "warn", Kind: "flow.run_failed", Message: fmt.Sprintf(`Flow "%s" run failed: %s`, f.Name, why), Data: obj("flowId", f.ID, "runId", run.ID)}, now); err != nil {
		return err
	}
	today := LocalDate(now, f.TZ)
	if failures >= Limits.AutoPauseAfter && f.Enabled {
		if _, err := q.Exec("UPDATE flows SET enabled = 0, paused_until = NULL, updated_at = ? WHERE id = ?", now, f.ID); err != nil {
			return err
		}
		if _, err := q.Exec("UPDATE flow_triggers SET next_fire_at = NULL, next_occ = NULL WHERE flow_id = ?", f.ID); err != nil {
			return err
		}
		if err := logEvent(q, events.Input{Level: "warn", Kind: "flow.auto_paused", Message: fmt.Sprintf(`Flow "%s" was paused after %d failed runs in a row.`, f.Name, failures), Data: obj("flowId", f.ID)}, now); err != nil {
			return err
		}
		_, err := e.send(q, f.ChatID, db.OutboxPayload{Text: fmt.Sprintf(`⏸️ Flow "%s" aku jeda: %d kali gagal berturut-turut. Terakhir: %s. Cek pakai explain_flow, terus jalankan lagi kalau udah beres.`, f.Name, failures, short), Format: "plain"}, "flow-notice:"+f.ID, now, nil)
		return err
	}
	if deref(f.NoticeFor, "") != today {
		if _, err := q.Exec("UPDATE flows SET notice_for = ? WHERE id = ?", today, f.ID); err != nil {
			return err
		}
		_, err := e.send(q, f.ChatID, db.OutboxPayload{Text: fmt.Sprintf(`⚠️ Flow "%s" gagal: %s`, f.Name, short), Format: "plain"}, "flow-notice:"+f.ID, now, nil)
		return err
	}
	return nil
}

func (e *Engine) runStep(c *runCtx, step *Step) (tr transition) {
	if r, ok := e.recoverStep(c, step); ok {
		return r
	}
	defer func() {
		if r := recover(); r != nil {
			var err error
			if sp, ok := r.(stepPanic); ok {
				err = sp.err
			} else {
				err = fmt.Errorf("%v", r)
			}
			e.log.Error("flow step crashed", logx.Fields{"run_id": c.run.ID, "step": step.ID, "err": err})
			tr = c.onError(step, errorText(err), stepRow{Status: "error"}, false)
		}
	}()
	switch step.Kind {
	case "if":
		return e.stepIf(c, step)
	case "set":
		return e.stepSet(c, step)
	case "message":
		return e.stepMessage(c, step)
	case "tool":
		return e.stepTool(c, step)
	case "prompt":
		return e.stepPrompt(c, step)
	case "checkin":
		return e.stepCheckin(c, step)
	case "wait":
		return e.stepWait(c, step)
	case "each":
		return e.stepEach(c, step)
	}
	return c.onError(step, fmt.Sprintf("Step kind %s isn't supported.", step.Kind), stepRow{Status: "error"}, false)
}

// Execute is execute: a claimed run's steps until it ends, waits or fails.
func (e *Engine) Execute(runID string, o ExecOptions) (*db.FlowRun, error) {
	q := e.o.DB
	run, err := GetRun(q, runID)
	if err != nil || run == nil || run.Status != "running" {
		return run, err
	}
	c, err := e.contextOf(q, run, o, e.clock())
	if err != nil {
		return nil, err
	}
	if c == nil {
		if _, err := q.Exec("UPDATE flow_runs SET status = 'failed', error = ?, ended_at = ?, lease_until = NULL WHERE id = ?", "Its flow or version is gone.", e.clock(), runID); err != nil {
			return nil, err
		}
		return GetRun(q, runID)
	}
	for guard := int64(0); guard < Limits.StepExecutionsPerRun+10; guard++ {
		c.now = e.clock()
		if c.run.Cursor == nil || *c.run.Cursor == "" {
			return e.finish(c)
		}
		if c.run.StepsDone >= Limits.StepExecutionsPerRun {
			msg := fmt.Sprintf("The run passed %d step executions.", Limits.StepExecutionsPerRun)
			_, _, err := e.apply(c, &c.def.Steps[0], transition{row: stepRow{Status: "error", Error: some(ptr("step limit"))}, then: then{t: "fail", err: &msg}}, c.now)
			return c.run, err
		}
		step := c.def.Step(*c.run.Cursor)
		if step == nil {
			if _, err := q.Exec("UPDATE flow_runs SET status = 'failed', error = ?, ended_at = ?, lease_until = NULL WHERE id = ?", fmt.Sprintf("Step %s isn't in the definition.", *c.run.Cursor), c.now, runID); err != nil {
				return nil, err
			}
			return GetRun(q, runID)
		}
		startedAt := e.clock()
		tr := e.runStep(c, step)
		r, goOn, err := e.apply(c, step, tr, startedAt)
		if err != nil {
			return nil, err
		}
		if !goOn {
			return r, nil
		}
	}
	return c.run, nil
}

var errNoRunner = errors.New("no runner")
