// Package jobs ports packages/core/src/jobs: scheduled jobs (PLAN 7). A job is
// an RRULE, a zone, a prompt, a target chat, a tool scope and a model role.
// next_run_at is computed in wall-clock time with the reminders time code. The
// reminders scheduler claims due jobs; the Runner turns them into
// schedule-triggered turns.
package jobs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// JobError carries a message meant for the owner or the model.
type JobError struct{ Msg string }

func (e *JobError) Error() string { return e.Msg }

func jobErr(format string, a ...any) error { return &JobError{fmt.Sprintf(format, a...)} }

func wrap(err error) error {
	var te *reminders.TimeError
	if errors.As(err, &te) {
		return &JobError{te.Msg}
	}
	return err
}

// DefaultDailyTokenCap is the column default.
const DefaultDailyTokenCap = int64(50_000)

type CreateInput struct {
	Name string `json:"name"`
	// job (default), heartbeat or briefing.
	Kind  string `json:"kind,omitempty"`
	RRule string `json:"rrule"`
	TZ    string `json:"tz"`
	// Series anchor and time of day (ISO local, no offset). "" is today 09:00 in TZ.
	LocalStart string `json:"localStart,omitempty"`
	Prompt     string `json:"prompt"`
	ChatID     string `json:"chatId"`
	// default (default), fast or vision.
	ModelRole string `json:"modelRole,omitempty"`
	// Connector ids the job may use; nil = every enabled connector.
	ToolScope     *[]string `json:"toolScope"`
	Enabled       *bool     `json:"enabled,omitempty"`
	DailyTokenCap *int64    `json:"dailyTokenCap,omitempty"`
}

// Patch: nil leaves a field alone. SetToolScope with ToolScope nil clears the
// scope (TS null).
type Patch struct {
	Name          *string
	Kind          *string
	RRule         *string
	TZ            *string
	LocalStart    *string
	Prompt        *string
	ChatID        *string
	ModelRole     *string
	SetToolScope  bool
	ToolScope     *[]string
	Enabled       *bool
	DailyTokenCap *int64
}

func scheduleOf(j db.Job) reminders.Schedule {
	rr := j.RRule
	return reminders.Schedule{RRule: &rr, TZ: j.TZ, LocalStart: j.LocalStart}
}

func nextRun(s reminders.Schedule, now int64) (*int64, error) {
	n, ok, err := reminders.NextOccurrence(s, now)
	if err != nil || !ok {
		return nil, err
	}
	return &n, nil
}

// ComputeNextRun is the next run strictly after now, nil when the rule ended.
func ComputeNextRun(j db.Job, now int64) (*int64, error) {
	n, err := nextRun(scheduleOf(j), now)
	return n, wrap(err)
}

func checkText(name, prompt string) error {
	if strx.Trim(name) == "" {
		return jobErr("A job needs a name.")
	}
	if strx.Trim(prompt) == "" {
		return jobErr("A job needs a prompt.")
	}
	if strx.Len16(prompt) > 8000 {
		return jobErr("The job prompt is longer than 8000 characters.")
	}
	return nil
}

func checkCap(cap int64) error {
	if cap < 0 {
		return jobErr("The daily token cap must be a whole number, 0 or more.")
	}
	return nil
}

func Create(q db.Q, in CreateInput, now int64) (db.Job, error) {
	row, err := create(q, in, now)
	if err != nil {
		return db.Job{}, wrap(err)
	}
	reminders.NotifyScheduleChanged()
	return row, nil
}

func create(q db.Q, in CreateInput, now int64) (db.Job, error) {
	if err := checkText(in.Name, in.Prompt); err != nil {
		return db.Job{}, err
	}
	if _, ok, err := chats.Get(q, in.ChatID); err != nil {
		return db.Job{}, err
	} else if !ok {
		return db.Job{}, jobErr("No chat %s.", in.ChatID)
	}
	if err := reminders.AssertZone(in.TZ); err != nil {
		return db.Job{}, err
	}
	if in.DailyTokenCap != nil {
		if err := checkCap(*in.DailyTokenCap); err != nil {
			return db.Job{}, err
		}
	}
	rr, err := reminders.NormalizeRrule(in.RRule)
	if err != nil {
		return db.Job{}, err
	}
	local := reminders.UTCToLocal(now, in.TZ)[:10] + "T09:00:00"
	if in.LocalStart != "" {
		if local, err = reminders.NormalizeLocal(in.LocalStart); err != nil {
			return db.Job{}, err
		}
	}
	s := reminders.Schedule{RRule: &rr, TZ: in.TZ, LocalStart: local}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var next *int64
	if enabled {
		if next, err = nextRun(s, now); err != nil {
			return db.Job{}, err
		}
	}
	kind := in.Kind
	if kind == "" {
		kind = "job"
	}
	role := in.ModelRole
	if role == "" {
		role = "default"
	}
	cap := DefaultDailyTokenCap
	if in.DailyTokenCap != nil {
		cap = *in.DailyTokenCap
	}
	return db.ScanJob(q.WQueryRow("INSERT INTO jobs (id, name, kind, rrule, tz, local_start, prompt, chat_id, model_role, tool_scope, enabled, next_run_at, daily_token_cap, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.JobCols,
		secret.NewID("job"), strx.Trim(in.Name), kind, rr, in.TZ, local, strx.Trim(in.Prompt), in.ChatID, role, db.ToolScopeValue(in.ToolScope), db.Bool(enabled), next, cap, now, now))
}

func Get(q db.Q, id string) (db.Job, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.JobCols+" FROM jobs WHERE id = ?", id), db.ScanJob)
}

func mustGet(q db.Q, id string) (db.Job, error) {
	j, ok, err := Get(q, id)
	if err != nil {
		return j, err
	}
	if !ok {
		return j, jobErr("No job %s.", id)
	}
	return j, nil
}

type setList struct {
	cols []string
	args []any
}

func (s *setList) add(col string, v any) {
	s.cols = append(s.cols, col+" = ?")
	s.args = append(s.args, v)
}

func Update(q db.Q, id string, p Patch, now int64) (db.Job, error) {
	row, err := update(q, id, p, now)
	if err != nil {
		return db.Job{}, wrap(err)
	}
	reminders.NotifyScheduleChanged()
	return row, nil
}

func update(q db.Q, id string, p Patch, now int64) (db.Job, error) {
	j, err := mustGet(q, id)
	if err != nil {
		return j, err
	}
	set := &setList{}
	set.add("updated_at", now)
	if p.Name != nil || p.Prompt != nil {
		name, prompt := j.Name, j.Prompt
		if p.Name != nil {
			name = *p.Name
		}
		if p.Prompt != nil {
			prompt = *p.Prompt
		}
		if err := checkText(name, prompt); err != nil {
			return j, err
		}
	}
	if p.Name != nil {
		set.add("name", strx.Trim(*p.Name))
	}
	if p.Prompt != nil {
		set.add("prompt", strx.Trim(*p.Prompt))
	}
	if p.Kind != nil {
		set.add("kind", *p.Kind)
	}
	if p.ModelRole != nil {
		set.add("model_role", *p.ModelRole)
	}
	if p.SetToolScope {
		set.add("tool_scope", db.ToolScopeValue(p.ToolScope))
	}
	if p.DailyTokenCap != nil {
		if err := checkCap(*p.DailyTokenCap); err != nil {
			return j, err
		}
		set.add("daily_token_cap", *p.DailyTokenCap)
	}
	if p.ChatID != nil {
		if _, ok, err := chats.Get(q, *p.ChatID); err != nil {
			return j, err
		} else if !ok {
			return j, jobErr("No chat %s.", *p.ChatID)
		}
		set.add("chat_id", *p.ChatID)
	}
	enabled := j.Enabled
	if p.Enabled != nil {
		enabled = *p.Enabled
		set.add("enabled", db.Bool(enabled))
	}
	rr := j.RRule
	if p.RRule != nil {
		if rr, err = reminders.NormalizeRrule(*p.RRule); err != nil {
			return j, err
		}
	}
	tz := j.TZ
	if p.TZ != nil {
		tz = *p.TZ
	}
	local := j.LocalStart
	if p.LocalStart != nil {
		if local, err = reminders.NormalizeLocal(*p.LocalStart); err != nil {
			return j, err
		}
	}
	if err := reminders.AssertZone(tz); err != nil {
		return j, err
	}
	set.add("rrule", rr)
	set.add("tz", tz)
	set.add("local_start", local)
	var next *int64
	if enabled {
		if next, err = nextRun(reminders.Schedule{RRule: &rr, TZ: tz, LocalStart: local}, now); err != nil {
			return j, err
		}
	}
	set.add("next_run_at", next)
	return db.ScanJob(q.WQueryRow("UPDATE jobs SET "+strings.Join(set.cols, ", ")+" WHERE id = ? RETURNING "+db.JobCols, append(set.args, id)...))
}

// SetEnabled pauses or resumes. Resuming computes the next run from now: runs
// missed while paused are skipped, not caught up.
func SetEnabled(q db.Q, id string, enabled bool, now int64) (db.Job, error) {
	return Update(q, id, Patch{Enabled: &enabled}, now)
}

type ListOptions struct {
	ChatID  string
	Enabled *bool
	Kind    string
	// Chats, when set, keeps the jobs whose chat matches this SQL filter on
	// table `chats` (agents.ChatsServedBy). A job whose chat row is gone stays
	// only with OrphanChats.
	Chats       string
	ChatsArgs   []any
	OrphanChats bool
}

// List orders by next run then name, with paused jobs (next_run_at null) last.
func List(q db.Q, o ListOptions) ([]db.Job, error) {
	var where []string
	var args []any
	if o.ChatID != "" {
		where = append(where, "chat_id = ?")
		args = append(args, o.ChatID)
	}
	if o.Enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, db.Bool(*o.Enabled))
	}
	if o.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, o.Kind)
	}
	if o.Chats != "" {
		in := `chat_id IN (SELECT "chats"."id" FROM "chats" WHERE ` + o.Chats + `)`
		if o.OrphanChats {
			in = "(" + in + ` OR chat_id NOT IN (SELECT "id" FROM "chats"))`
		}
		where = append(where, in)
		args = append(args, o.ChatsArgs...)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	rows, qerr := q.Query("SELECT "+db.JobCols+" FROM jobs"+w+" ORDER BY next_run_at ASC, name ASC", args...)
	all, err := db.All(rows, qerr, db.ScanJob)
	if err != nil {
		return nil, err
	}
	out := make([]db.Job, 0, len(all))
	for _, j := range all {
		if j.NextRunAt != nil {
			out = append(out, j)
		}
	}
	for _, j := range all {
		if j.NextRunAt == nil {
			out = append(out, j)
		}
	}
	return out, nil
}

func Delete(q db.Q, id string) (bool, error) {
	n, err := db.Changes(q.Exec("DELETE FROM jobs WHERE id = ?", id))
	if err != nil {
		return false, err
	}
	if n > 0 {
		reminders.NotifyScheduleChanged()
	}
	return n > 0, nil
}

// DescribeSchedule is describeJobSchedule: "Tiap hari, 07.00 WITA".
func DescribeSchedule(j db.Job, now int64) (string, error) {
	at := now
	if j.NextRunAt != nil {
		at = *j.NextRunAt
	}
	s, err := reminders.FormatEcho(scheduleOf(j), at, now)
	return s, wrap(err)
}

// DescribeNextRun is "Besok, Rabu 11 Nov, 07.00 WITA"; ok false when paused or finished.
func DescribeNextRun(j db.Job, now int64) (string, bool) {
	if j.NextRunAt == nil {
		return "", false
	}
	return reminders.FormatWhen(*j.NextRunAt, j.TZ, now), true
}
