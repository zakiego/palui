package flows

// explain_flow (diagnose.ts, RFC flows 10.1): "why didn't X fire?" walks a
// fixed checklist for a flow and a moment and returns the first thing that
// explains it, plus the facts behind it.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
)

// When this process started and last swept, for "was Palui down?".
var (
	clockMu     sync.Mutex
	startedAt   = time.Now().UnixMilli()
	lastSweepAt int64
)

func setLastSweep(now int64) {
	clockMu.Lock()
	lastSweepAt = now
	clockMu.Unlock()
}

// EngineClock is engineClock.
func EngineClock() (started, lastSweep int64) {
	clockMu.Lock()
	defer clockMu.Unlock()
	return startedAt, lastSweepAt
}

// SetEngineClock sets engineClock (tests).
func SetEngineClock(started, lastSweep int64) {
	clockMu.Lock()
	startedAt, lastSweepAt = started, lastSweep
	clockMu.Unlock()
}

// Branch is one branch a run took, with the if's explanation.
type Branch struct {
	Step    string  `json:"step"`
	Branch  *string `json:"branch"`
	Explain *string `json:"explain,omitempty"`
}

// DiagnosisRun is Diagnosis.run.
type DiagnosisRun struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	SkipReason *string  `json:"skipReason"`
	EndedAt    *string  `json:"endedAt"`
	LastStep   *string  `json:"lastStep"`
	Branches   []Branch `json:"branches"`
	Error      *string  `json:"error"`
}

// Diagnosis is Diagnosis.
type Diagnosis struct {
	Verdict string        `json:"verdict"`
	Facts   []string      `json:"facts"`
	Run     *DiagnosisRun `json:"run,omitempty"`
}

const (
	windowBeforeMs = 6 * 3600_000
	windowAfterMs  = 2 * 3600_000
)

func nearestRun(q db.Q, flow *db.Flow, at int64) (*db.FlowRun, error) {
	rows, err := q.Query("SELECT "+db.FlowRunCols+" FROM flow_runs WHERE flow_id = ? AND dry_run = 0 AND started_at >= ? AND started_at <= ? ORDER BY started_at DESC", flow.ID, at-windowBeforeMs-24*3600_000, at+windowAfterMs)
	list, err := db.All(rows, err, db.ScanFlowRun)
	if err != nil {
		return nil, err
	}
	t := func(r *db.FlowRun) int64 {
		if r.ScheduledAt != nil {
			return *r.ScheduledAt
		}
		return r.StartedAt
	}
	var near []db.FlowRun
	for i := range list {
		if x := t(&list[i]); x >= at-windowBeforeMs && x <= at+windowAfterMs {
			near = append(near, list[i])
		}
	}
	abs := func(x int64) int64 {
		if x < 0 {
			return -x
		}
		return x
	}
	sort.SliceStable(near, func(i, j int) bool { return abs(t(&near[i])-at) < abs(t(&near[j])-at) })
	if len(near) == 0 {
		return nil, nil
	}
	return &near[0], nil
}

var skipText = map[string]string{
	"late":         "it was skipped as late: Palui was down or busy past the grace period at that time",
	"duplicate":    "it was a duplicate of a run that already happened",
	"paused":       "the flow was paused when it came in",
	"rate_limited": "the flow hit its runs-per-hour limit",
	"budget":       "the flow hit its daily token cap",
	"not_approved": "it reached a write step of a version the owner hasn't approved",
	"chain_depth":  "it was an event chain deeper than 3 flows",
}

// DiagnoseFlow is diagnoseFlow.
func DiagnoseFlow(q db.Q, flow *db.Flow, at, now int64) (*Diagnosis, error) {
	facts := []string{}
	tz := flow.TZ
	when := func(ms int64) string { return reminders.FormatWhen(ms, tz, now) }
	if !flow.Enabled {
		var verdict string
		if flow.PausedUntil != nil && *flow.PausedUntil != 0 {
			verdict = fmt.Sprintf(`Flow "%s" is paused until %s.`, flow.Name, when(*flow.PausedUntil))
		} else {
			extra := ""
			if flow.LastError != nil && *flow.LastError != "" {
				extra = " (last error: " + *flow.LastError + ")"
			}
			verdict = fmt.Sprintf(`Flow "%s" is paused%s.`, flow.Name, extra)
		}
		if flow.ConsecutiveFailures >= 5 {
			facts = append(facts, fmt.Sprintf("It paused itself after %d failed runs in a row.", flow.ConsecutiveFailures))
		}
		return &Diagnosis{Verdict: verdict, Facts: facts}, nil
	}
	_, def, err := currentDef(q, flow)
	if err != nil {
		return nil, err
	}
	run, err := nearestRun(q, flow, at)
	if err != nil {
		return nil, err
	}
	if run != nil {
		steps, err := StepRunsOf(q, run.ID)
		if err != nil {
			return nil, err
		}
		branches := []Branch{}
		for _, s := range steps {
			if s.Branch == nil || *s.Branch == "" {
				continue
			}
			b := Branch{Step: s.StepID, Branch: s.Branch}
			if s.Kind == "if" {
				var ex string
				if v, ok := asObject(s.Input).Get("explain"); ok && v != nil {
					ex = jsString(v)
				} else {
					var trace []TraceEntry
					for _, x := range garr(asObject(s.Input), "values") {
						v, _ := asObject(x).Get("value")
						trace = append(trace, TraceEntry{Expr: objStr(x, "expr"), Value: v})
					}
					ex = ExplainTrace(trace)
				}
				b.Explain = &ex
			}
			branches = append(branches, b)
		}
		var last *db.FlowStepRun
		if len(steps) > 0 {
			last = &steps[len(steps)-1]
		}
		lastStep := "?"
		info := &DiagnosisRun{ID: run.ID, Status: run.Status, SkipReason: run.SkipReason, Branches: branches, Error: run.Error}
		if run.EndedAt != nil && *run.EndedAt != 0 {
			info.EndedAt = ptr(when(*run.EndedAt))
		}
		if last != nil {
			info.LastStep, lastStep = &last.StepID, last.StepID
		}
		startMs := run.StartedAt
		if run.ScheduledAt != nil {
			startMs = *run.ScheduledAt
		}
		started := when(startMs)
		var verdict string
		switch run.Status {
		case "skipped":
			reason := deref(run.SkipReason, "")
			if text, ok := skipText[reason]; ok {
				verdict = fmt.Sprintf("It ran at %s, but %s.", started, text)
			} else {
				verdict = fmt.Sprintf("It ran at %s, but was skipped (%s).", started, jsonxNullString(run.SkipReason))
			}
		case "failed":
			verdict = fmt.Sprintf("It ran at %s and failed at step %s: %s.", started, lastStep, deref(run.Error, "unknown error"))
		case "awaiting_approval":
			verdict = fmt.Sprintf("It ran at %s and is waiting for the owner to approve version changes (step %s).", started, lastStep)
		case "waiting":
			kind := "?"
			if run.Wait != nil {
				if k := gstr(run.Wait, "kind"); k != "" {
					kind = k
				}
			}
			until := ""
			if run.WakeAt != nil && *run.WakeAt != 0 {
				until = " until " + when(*run.WakeAt)
			}
			forWhat := ""
			if kind == "approval" {
				ap, ok, err := db.One(q.QueryRow("SELECT "+db.ApprovalCols+" FROM pending_approvals WHERE run_id = ? ORDER BY created_at DESC LIMIT 1", run.ID), db.ScanApproval)
				if err != nil {
					return nil, err
				}
				if ok {
					forWhat = ", for the owner to approve " + ap.Tool
				}
			}
			verdict = fmt.Sprintf("It ran at %s and is waiting (%s) at step %s%s%s.", started, kind, lastStep, until, forWhat)
		default:
			var stop *Branch
			for i := range branches {
				b := &branches[i]
				if b.Explain == nil || deref(b.Branch, "") != "else" {
					continue
				}
				if s := def.Step(b.Step); s != nil && s.Kind == "if" && s.Else.ID == nil {
					stop = b
					break
				}
			}
			if stop != nil {
				verdict = fmt.Sprintf("It ran at %s, but stopped at %s: %s (the condition was false).", started, stop.Step, *stop.Explain)
			} else {
				how := run.Status
				if how == "done" {
					how = "finished"
				}
				after := ""
				if last != nil {
					after = " after " + last.StepID
				}
				verdict = fmt.Sprintf("It ran at %s and %s%s.", started, how, after)
			}
		}
		if len(run.Taint) > 0 {
			facts = append(facts, fmt.Sprintf("Untrusted data in that run: %s.", strings.Join(run.Taint, ", ")))
		}
		return &Diagnosis{Verdict: verdict, Facts: facts, Run: info}, nil
	}

	// No run near that time: what the triggers say.
	rows, err := TriggersOf(q, flow.ID)
	if err != nil {
		return nil, err
	}
	for i := range def.Triggers {
		t := &def.Triggers[i]
		var row *db.FlowTrigger
		for j := range rows {
			if rows[j].TriggerID == t.ID {
				row = &rows[j]
				break
			}
		}
		switch {
		case t.Kind == "schedule" && row != nil:
			var near []string
			list, err := Upcoming(ScheduleInput(t, tz, row.SourceState), Cursor{At: at - 24*3600_000}, 40)
			if err != nil {
				facts = append(facts, fmt.Sprintf("Trigger %s: the schedule can't be computed: %s", t.ID, err.Error()))
			} else {
				for _, o := range list {
					d := o.At - at
					if d < 0 {
						d = -d
					}
					if d <= 12*3600_000 && len(near) < 6 {
						near = append(near, fmt.Sprintf("%s (%s)", when(o.At), o.Label))
					}
				}
			}
			if len(near) > 0 {
				facts = append(facts, fmt.Sprintf("Trigger %s occurrences around then: %s.", t.ID, strings.Join(near, ", ")))
			} else {
				facts = append(facts, fmt.Sprintf("Trigger %s has no occurrence within 12 hours of that time.", t.ID))
			}
			st := row.SourceState
			if f, _ := objNum(st, "failures"); f != 0 {
				le := objStr(st, "lastError")
				if le == "" {
					le = deref(row.LastError, "?")
				}
				facts = append(facts, fmt.Sprintf("Trigger %s: its source failed %s time(s) in a row; last error: %s.", t.ID, jsonx.NumberString(f), le))
			}
			if row.NextFireAt != nil && *row.NextFireAt != 0 {
				facts = append(facts, fmt.Sprintf("Trigger %s fires next at %s.", t.ID, when(*row.NextFireAt)))
			}
			if row.Cursor != nil && row.Cursor.Key == "" && row.Cursor.At > at {
				facts = append(facts, fmt.Sprintf("Trigger %s was (re)started after that time (an edit or a resume starts from then).", t.ID))
			}
		case t.Kind == "poll" && row != nil:
			p := row.PollState
			checked := "never"
			if v, ok := objNum(p, "lastCheckedAt"); ok && v != 0 {
				checked = when(int64(v))
			}
			le := ""
			if s := objStr(p, "lastError"); s != "" {
				le = ", last error: " + s
			}
			fails := ""
			if f, _ := objNum(p, "failures"); f != 0 {
				fails = fmt.Sprintf(" (%s failures)", jsonx.NumberString(f))
			}
			how := "is non-empty"
			if t.Fire == "change" {
				how = "changes"
			}
			facts = append(facts, fmt.Sprintf("Trigger %s (poll) last checked %s%s%s. It fires only when the value %s; the first poll only remembers the value.", t.ID, checked, le, fails, how))
		case t.Kind == "webhook":
			hook, ok, err := db.One(q.QueryRow("SELECT "+db.HookCols+" FROM hooks WHERE id = ?", t.HookID), db.ScanHook)
			if err != nil {
				return nil, err
			}
			if !ok {
				facts = append(facts, fmt.Sprintf("Trigger %s: hook %s no longer exists.", t.ID, t.HookID))
			} else {
				onOff := "off"
				if hook.Enabled {
					onOff = "on"
				}
				last := "never"
				if hook.LastAt != nil && *hook.LastAt != 0 {
					last = when(*hook.LastAt)
				}
				extra := ""
				if hook.Mode != "flow" {
					extra = " Its mode isn't flow, so it doesn't start this flow."
				}
				facts = append(facts, fmt.Sprintf(`Trigger %s: hook "%s" (%s, mode %s) last received something %s.%s`, t.ID, hook.Name, onOff, hook.Mode, last, extra))
			}
		case t.Kind == "chat":
			var n int
			if err := q.QueryRow("SELECT count(*) FROM messages WHERE chat_id = ? AND from_owner = 1 AND created_at >= ? AND created_at <= ?", flow.ChatID, at-windowBeforeMs, at+windowAfterMs).Scan(&n); err != nil {
				return nil, err
			}
			what := strings.Join(t.Phrases, " / ")
			if t.Command != nil {
				what = *t.Command
			}
			facts = append(facts, fmt.Sprintf("Trigger %s: %d owner message(s) in this chat around then; none matched %s exactly.", t.ID, n, what))
		case t.Kind == "event":
			from := fmt.Sprintf(`flow "%s"`, t.Flow)
			if t.Flow == "self" {
				from = "this flow"
			}
			facts = append(facts, fmt.Sprintf("Trigger %s: waits for %s from %s; no such event started a run then.", t.ID, t.Event, from))
		}
	}
	if len(def.Triggers) == 0 {
		facts = append(facts, "The flow has no triggers; it only runs with run_flow.")
	}
	started, swept := EngineClock()
	if at < started {
		var id int64
		err := q.QueryRow("SELECT id FROM events WHERE at >= ? AND at <= ? LIMIT 1", at-3600_000, at+3600_000).Scan(&id)
		if err != nil {
			facts = append(facts, fmt.Sprintf("Palui logged nothing within an hour of that time and restarted at %s: it was likely down.", when(started)))
		}
	}
	if swept != 0 {
		facts = append(facts, fmt.Sprintf("The scheduler last swept at %s.", when(swept)))
	}
	verdict := fmt.Sprintf(`No run of "%s" near that time.`, flow.Name)
	for _, f := range facts {
		if strings.Contains(f, "failed") || strings.Contains(f, "down") || strings.Contains(f, "no longer") || strings.Contains(f, "no occurrence") {
			verdict = f
			break
		}
	}
	return &Diagnosis{Verdict: verdict, Facts: facts}, nil
}

func jsonxNullString(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}
