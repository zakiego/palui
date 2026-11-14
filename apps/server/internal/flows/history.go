package flows

// Run history and the views the agent tools, the recap tool and the dashboard
// share (history.ts). Views are ordered jsonx objects in the TS key order, so
// M6 and M8 can return them as they are.

import (
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
)

// RunQuery is RunQuery. Zero values mean "no filter"; Limit 0 is 500.
type RunQuery struct {
	ChatID       string
	FlowID       string
	Since        *int64
	Until        *int64
	Status       []string
	TriggerID    string
	Limit        int
	TopLevelOnly bool
}

// ListRuns is listRuns: newest first, dry runs left out.
func ListRuns(q db.Q, rq RunQuery) ([]db.FlowRun, error) {
	conds := []string{"dry_run = 0"}
	var args []any
	if rq.ChatID != "" {
		conds, args = append(conds, "chat_id = ?"), append(args, rq.ChatID)
	}
	if rq.FlowID != "" {
		conds, args = append(conds, "flow_id = ?"), append(args, rq.FlowID)
	}
	if rq.Since != nil {
		conds, args = append(conds, "started_at >= ?"), append(args, *rq.Since)
	}
	if len(rq.Status) > 0 {
		conds, args = append(conds, "status IN ("+placeholders(len(rq.Status))+")"), append(args, anyArgs(rq.Status)...)
	}
	if rq.TriggerID != "" {
		conds, args = append(conds, "trigger_id = ?"), append(args, rq.TriggerID)
	}
	if rq.TopLevelOnly {
		conds = append(conds, "parent_run_id IS NULL")
	}
	limit := rq.Limit
	if limit == 0 {
		limit = 500
	}
	rows, err := q.Query("SELECT "+db.FlowRunCols+" FROM flow_runs WHERE "+strings.Join(conds, " AND ")+fmt.Sprintf(" ORDER BY started_at DESC, id DESC LIMIT %d", limit), args...)
	list, err := db.All(rows, err, db.ScanFlowRun)
	if err != nil || rq.Until == nil {
		return list, err
	}
	var out []db.FlowRun
	for _, r := range list {
		if r.StartedAt <= *rq.Until {
			out = append(out, r)
		}
	}
	return out, nil
}

func isCheckinOutput(v any) bool {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return false
	}
	_, a := o.Get("answers")
	_, m := o.Get("missed")
	return a && m
}

// DecidingCheckin is decidingCheckin: the check-in output that decides a run
// (the latest answer wins, else the last one reached).
func DecidingCheckin(steps *jsonx.Object) (string, *jsonx.Object) {
	var all []string
	for _, k := range steps.Keys() {
		v, _ := steps.Get(k)
		if isCheckinOutput(v) {
			all = append(all, k)
		}
	}
	best := ""
	var bestAt float64
	for _, k := range all {
		v, _ := steps.Get(k)
		o := v.(*jsonx.Object)
		ans, _ := o.Get("answer")
		if jsonx.Truthy(objGet(o, "missed")) || !jsonx.Truthy(ans) {
			continue
		}
		at, _ := objNum(o, "answeredAt")
		if best == "" || at >= bestAt {
			best, bestAt = k, at
		}
	}
	if best == "" {
		if len(all) == 0 {
			return "", nil
		}
		best = all[len(all)-1]
	}
	v, _ := steps.Get(best)
	return best, v.(*jsonx.Object)
}

// jsString is String(v) for a JSON value.
func jsString(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case undefT:
		return "undefined"
	case *jsonx.Object:
		return "[object Object]"
	}
	return templateString(v, true)
}

func triggerLabel(run *db.FlowRun) string {
	v, ok := run.Trigger.Get("label")
	if !ok || v == nil {
		return ""
	}
	return jsString(v)
}

// CheckinInfo is checkinOf's answer.
type CheckinInfo struct {
	StepID string
	Answer *string
	Missed bool
	Open   bool
	Label  string
}

func (c *CheckinInfo) value() any {
	if c == nil {
		return nil
	}
	return obj("stepId", c.StepID, "answer", strOrNull(c.Answer), "missed", c.Missed, "open", c.Open, "label", c.Label)
}

// CheckinOf is checkinOf: a run's check-in answer, from its output (or its
// wait state while open).
func CheckinOf(run *db.FlowRun) *CheckinInfo {
	vars := DecodeVars(run.Vars)
	label := triggerLabel(run)
	if run.Status == "waiting" && run.Wait != nil && gstr(run.Wait, "kind") == "checkin" && gstr(run.Wait, "stepId") != "" {
		var ans *string
		if list := garr(run.Wait, "answers"); len(list) > 0 {
			if s, ok := objGet(list[len(list)-1], "answer").(string); ok {
				ans = &s
			}
		}
		return &CheckinInfo{StepID: gstr(run.Wait, "stepId"), Answer: ans, Open: true, Label: label}
	}
	if id, hit := DecidingCheckin(vars.Steps); hit != nil {
		var ans *string
		if s, ok := objGet(hit, "answer").(string); ok {
			ans = &s
		}
		return &CheckinInfo{StepID: id, Answer: ans, Missed: jsonx.Truthy(objGet(hit, "missed")), Label: label}
	}
	return nil
}

// RunSummary is RunSummary; the maps keep insertion order.
type RunSummary struct {
	Total        int
	ByStatus     *jsonx.Map[float64]
	BySkipReason *jsonx.Map[float64]
	ByAnswer     *jsonx.Map[float64]
	Missed       int
	Open         int
	MissedLabels []string
}

func inc(m *jsonx.Map[float64], k string) {
	v, _ := m.Get(k)
	m.Set(k, v+1)
}

// SummarizeRuns is summarizeRuns.
func SummarizeRuns(rows []db.FlowRun) RunSummary {
	s := RunSummary{ByStatus: jsonx.NewMap[float64](), BySkipReason: jsonx.NewMap[float64](), ByAnswer: jsonx.NewMap[float64](), MissedLabels: []string{}}
	for i := range rows {
		r := &rows[i]
		s.Total++
		inc(s.ByStatus, r.Status)
		if r.SkipReason != nil && *r.SkipReason != "" {
			inc(s.BySkipReason, *r.SkipReason)
		}
		c := CheckinOf(r)
		if c == nil {
			continue
		}
		switch {
		case c.Open:
			s.Open++
		case c.Missed:
			s.Missed++
			if c.Label != "" {
				s.MissedLabels = append(s.MissedLabels, c.Label)
			}
		case c.Answer != nil && *c.Answer != "":
			inc(s.ByAnswer, *c.Answer)
		}
	}
	return s
}

func uniqueStrings(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// RecapText is recapText.
func RecapText(name string, def *Definition, rows []db.FlowRun, days float64) string {
	s := SummarizeRuns(rows)
	span := jsonx.NumberString(days) + " hari terakhir"
	if days == 1 {
		span = "24 jam terakhir"
	} else if days == 7 {
		span = "7 hari terakhir"
	}
	lines := []string{fmt.Sprintf("📊 Rekap %s, %s:", name, span)}
	var checkin *Step
	for i := range def.Steps {
		if def.Steps[i].Kind == "checkin" {
			checkin = &def.Steps[i]
			break
		}
	}
	skipped, _ := s.ByStatus.Get("skipped")
	if checkin != nil {
		for _, a := range checkin.Answers {
			if n, _ := s.ByAnswer.Get(a.ID); n != 0 {
				lines = append(lines, fmt.Sprintf("- %s: %s", a.Label, jsonx.NumberString(n)))
			}
		}
		counted := float64(s.Total) - skipped - float64(s.Open)
		closed := 0.0
		for _, k := range s.ByAnswer.Keys() {
			v, _ := s.ByAnswer.Get(k)
			closed += v
		}
		missed := fmt.Sprintf("- Terlewat: %d", s.Missed)
		if len(s.MissedLabels) > 0 {
			rev := make([]string, len(s.MissedLabels))
			for i, l := range s.MissedLabels {
				rev[len(rev)-1-i] = l
			}
			u := uniqueStrings(rev)
			if len(u) > 8 {
				u = u[:8]
			}
			missed += " (" + strings.Join(u, ", ") + ")"
		}
		lines = append(lines, missed)
		if s.Open > 0 {
			lines = append(lines, fmt.Sprintf("- Masih nunggu jawaban: %d", s.Open))
		}
		lines = append(lines, fmt.Sprintf("Selesai %s/%s.", jsonx.NumberString(closed), jsonx.NumberString(counted)))
	} else {
		done, _ := s.ByStatus.Get("done")
		lines = append(lines, "- Terkirim: "+jsonx.NumberString(done))
		if failed, _ := s.ByStatus.Get("failed"); failed != 0 {
			lines = append(lines, "- Gagal: "+jsonx.NumberString(failed))
		}
	}
	if skipped != 0 {
		lines = append(lines, "- Dilewati (Palui lagi mati): "+jsonx.NumberString(skipped))
	}
	return strings.Join(lines, "\n")
}

// ApprovalState is approvalState: approved, waiting or not_needed.
func ApprovalState(q db.Q, flow *db.Flow) (string, error) {
	var v *db.FlowVersion
	if flow.CurrentVersionID != nil {
		var err error
		if v, err = GetVersion(q, *flow.CurrentVersionID); err != nil {
			return "", err
		}
	}
	if v == nil || len(v.WriteCalls) == 0 {
		return "not_needed", nil
	}
	if WritesApproved(v) {
		return "approved", nil
	}
	return "waiting", nil
}

func whenOrNull(ms *int64, tz string, now int64) any {
	if ms == nil {
		return nil
	}
	return reminders.FormatWhen(*ms, tz, now)
}

// FlowSummaryView is flowSummaryView.
func FlowSummaryView(q db.Q, flow *db.Flow, now int64) (*jsonx.Object, error) {
	version, def, err := currentDef(q, flow)
	if err != nil {
		return nil, err
	}
	rows, err := TriggersOf(q, flow.ID)
	if err != nil {
		return nil, err
	}
	last, err := ListRuns(q, RunQuery{FlowID: flow.ID, Limit: 1, TopLevelOnly: true})
	if err != nil {
		return nil, err
	}
	approval, err := ApprovalState(q, flow)
	if err != nil {
		return nil, err
	}
	var paused any
	if !flow.Enabled && flow.PausedUntil != nil {
		paused = reminders.FormatWhen(*flow.PausedUntil, flow.TZ, now)
	}
	triggers := []any{}
	for i := range def.Triggers {
		t := &def.Triggers[i]
		var row *db.FlowTrigger
		for j := range rows {
			if rows[j].TriggerID == t.ID {
				row = &rows[j]
				break
			}
		}
		var next, lastErr any
		if flow.Enabled && row != nil && row.NextFireAt != nil {
			next = reminders.FormatWhen(*row.NextFireAt, flow.TZ, now)
		}
		if row != nil {
			lastErr = strOrNull(row.LastError)
		}
		triggers = append(triggers, obj("id", t.ID, "kind", t.Kind, "summary", DescribeTrigger(t), "next", next, "lastError", lastErr))
	}
	triggers = append(triggers, obj("id", "manual", "kind", "manual", "summary", "run_flow", "next", nil, "lastError", nil))
	var lastRun any
	if len(last) > 0 {
		l := last[0]
		lastRun = obj("id", l.ID, "status", l.Status, "skipReason", strOrNull(l.SkipReason), "at", reminders.FormatWhen(l.StartedAt, flow.TZ, now), "error", strOrNull(l.Error))
	}
	return obj("id", flow.ID, "name", flow.Name, "description", strOrNull(def.Description), "active", flow.Enabled, "pausedUntil", paused, "version", float64(version.N), "approval", approval, "triggers", triggers, "lastRun", lastRun, "lastError", strOrNull(flow.LastError)), nil
}

// FlowDetailView is flowDetailView; versionN 0 shows the current version.
// Nil when that version doesn't exist.
func FlowDetailView(q db.Q, flow *db.Flow, now int64, versionN int64) (*jsonx.Object, error) {
	all, err := ListVersions(q, flow.ID)
	if err != nil {
		return nil, err
	}
	var version *db.FlowVersion
	for i := range all {
		if (versionN != 0 && all[i].N == versionN) || (versionN == 0 && flow.CurrentVersionID != nil && all[i].ID == *flow.CurrentVersionID) {
			version = &all[i]
			break
		}
	}
	if version == nil {
		return nil, nil
	}
	out, err := FlowSummaryView(q, flow, now)
	if err != nil {
		return nil, err
	}
	calls := []any{}
	for _, w := range version.WriteCalls {
		calls = append(calls, DescribeWriteCall(w))
	}
	versions := []any{}
	for _, v := range all {
		current := flow.CurrentVersionID != nil && v.ID == *flow.CurrentVersionID
		versions = append(versions, obj("n", float64(v.N), "createdBy", v.CreatedBy, "note", strOrNull(v.Note), "approved", v.ApprovedAt != nil || len(v.WriteCalls) == 0, "at", reminders.FormatWhen(v.CreatedAt, flow.TZ, now), "current", current))
	}
	trigs, err := TriggersOf(q, flow.ID)
	if err != nil {
		return nil, err
	}
	state := []any{}
	for _, r := range trigs {
		var cursor, poll any = nil, undef
		if r.Cursor != nil {
			cursor = obj("at", float64(r.Cursor.At), "key", r.Cursor.Key)
		}
		if r.Kind == "poll" {
			poll = r.PollState
		}
		fails, _ := objNum(r.SourceState, "failures")
		var next any
		if r.NextFireAt != nil {
			next = float64(*r.NextFireAt)
		}
		state = append(state, obj("trigger", r.TriggerID, "kind", r.Kind, "cursor", cursor, "nextFireAt", next, "lastError", strOrNull(r.LastError), "sourceFailures", fails, "poll", poll))
	}
	out.Set("shownVersion", float64(version.N))
	out.Set("definition", version.Definition)
	out.Set("writeCalls", calls)
	out.Set("versions", versions)
	out.Set("triggerState", state)
	out.Set("state", cloneObj(flow.State.Values))
	return out, nil
}

// RunView is runView.
func RunView(q db.Q, run *db.FlowRun, tz string, now int64, withSteps bool) (*jsonx.Object, error) {
	trig := run.TriggerKind
	if run.TriggerID != nil {
		trig = *run.TriggerID
	}
	label, ok := run.Trigger.Get("label")
	if !ok {
		label = nil
	}
	var sched, ended any
	if run.ScheduledAt != nil && *run.ScheduledAt != 0 {
		sched = reminders.FormatWhen(*run.ScheduledAt, tz, now)
	}
	if run.EndedAt != nil && *run.EndedAt != 0 {
		ended = reminders.FormatWhen(*run.EndedAt, tz, now)
	}
	out := obj("id", run.ID, "status", run.Status, "skipReason", strOrNull(run.SkipReason), "trigger", trig, "at", reminders.FormatWhen(run.StartedAt, tz, now), "scheduledAt", sched, "label", label, "endedAt", ended, "error", strOrNull(run.Error))
	if run.ParentRunID != nil && *run.ParentRunID != "" {
		var item any
		if run.ItemIndex != nil {
			item = float64(*run.ItemIndex)
		}
		out.Set("parent", *run.ParentRunID)
		out.Set("item", item)
	}
	if run.Status == "waiting" && run.Wait != nil {
		var wake any
		if run.WakeAt != nil && *run.WakeAt != 0 {
			wake = reminders.FormatWhen(*run.WakeAt, tz, now)
		}
		kind, _ := run.Wait.Get("kind")
		out.Set("waiting", kind)
		out.Set("wakeAt", wake)
	}
	out.Set("checkin", CheckinOf(run).value())
	if !withSteps {
		return out, nil
	}
	rows, err := q.Query("SELECT id, status, item_index FROM flow_runs WHERE parent_run_id = ? ORDER BY rowid", run.ID)
	children, err := db.All(rows, err, func(s db.Scanner) (any, error) {
		var id, status string
		var item *int64
		if err := s.Scan(&id, &status, &item); err != nil {
			return nil, err
		}
		var it any
		if item != nil {
			it = float64(*item)
		}
		return obj("id", id, "status", status, "item", it), nil
	})
	if err != nil {
		return nil, err
	}
	steps, err := StepRunsOf(q, run.ID)
	if err != nil {
		return nil, err
	}
	list := []any{}
	for _, s := range steps {
		var ms any
		if s.Ms != nil {
			ms = float64(*s.Ms)
		}
		list = append(list, obj("seq", float64(s.Seq), "step", s.StepID, "kind", s.Kind, "status", s.Status, "branch", strOrNull(s.Branch), "input", s.Input, "output", s.Output, "error", strOrNull(s.Error), "tainted", s.Tainted, "ms", ms, "attempt", float64(s.Attempt)))
	}
	out.Set("triggerPayload", run.Trigger)
	out.Set("taint", stringsValue(run.Taint))
	out.Set("steps", list)
	if len(children) > 0 {
		out.Set("children", children)
	}
	return out, nil
}

// StepRowsCount is stepRowsCount.
func StepRowsCount(q db.Q, runID string) (int, error) {
	var n int
	err := q.QueryRow("SELECT count(*) FROM flow_step_runs WHERE run_id = ?", runID).Scan(&n)
	return n, err
}

// FlowRecap is flowRecap (flows__recap).
func FlowRecap(q db.Q, flow *db.Flow, days float64, triggerID string, now int64) (*jsonx.Object, error) {
	_, def, err := currentDef(q, flow)
	if err != nil {
		return nil, err
	}
	since := now - int64(days*24*3600_000)
	rows, err := ListRuns(q, RunQuery{FlowID: flow.ID, Since: &since, TriggerID: triggerID, Limit: 5000})
	if err != nil {
		return nil, err
	}
	s := SummarizeRuns(rows)
	labels := uniqueStrings(s.MissedLabels)
	if len(labels) > 20 {
		labels = labels[:20]
	}
	return obj("flow", flow.Name, "days", days, "total", float64(s.Total), "byAnswer", s.ByAnswer, "byStatus", s.ByStatus, "missed", float64(s.Missed), "open", float64(s.Open), "missedLabels", stringsValue(labels), "text", RecapText(flow.Name, def, rows, days)), nil
}

// Retention (RFC 3.5).
const (
	StepIODays = 30
	RunDays    = 180
)

// PruneResult is pruneFlows' counts.
type PruneResult struct {
	IO   int64 `json:"io"`
	Runs int64 `json:"runs"`
	Dry  int64 `json:"dry"`
}

// PruneFlows is pruneFlows: step inputs and outputs go after 30 days,
// finished runs after 180, dry runs after 30.
func PruneFlows(q db.Q, now int64) (PruneResult, error) {
	day := int64(24 * 3600_000)
	var out PruneResult
	io, err := db.Changes(q.Exec("UPDATE flow_step_runs SET input = NULL, output = NULL WHERE started_at < ? AND output IS NOT NULL", now-StepIODays*day))
	if err != nil {
		return out, err
	}
	out.IO = io
	old := func(cutoff int64, dry bool) ([]string, error) {
		rows, err := q.Query("SELECT id FROM flow_runs WHERE dry_run = ? AND started_at < ? AND status IN ('done', 'failed', 'skipped', 'cancelled') LIMIT 5000", db.Bool(dry), cutoff)
		return db.All(rows, err, func(s db.Scanner) (string, error) {
			var id string
			return id, s.Scan(&id)
		})
	}
	for _, pass := range []struct {
		cutoff int64
		dry    bool
	}{{now - RunDays*day, false}, {now - StepIODays*day, true}} {
		ids, err := old(pass.cutoff, pass.dry)
		if err != nil {
			return out, err
		}
		for i := 0; i < len(ids); i += 500 {
			chunk := ids[i:min(len(ids), i+500)]
			if _, err := q.Exec("DELETE FROM flow_step_runs WHERE run_id IN ("+placeholders(len(chunk))+")", anyArgs(chunk)...); err != nil {
				return out, err
			}
			n, err := db.Changes(q.Exec("DELETE FROM flow_runs WHERE id IN ("+placeholders(len(chunk))+")", anyArgs(chunk)...))
			if err != nil {
				return out, err
			}
			if pass.dry {
				out.Dry += n
			} else {
				out.Runs += n
			}
		}
	}
	return out, nil
}
