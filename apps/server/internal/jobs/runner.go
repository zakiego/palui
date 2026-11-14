package jobs

// Runs scheduled jobs as turns (jobs/runner.ts, PLAN 7). The scheduler claims
// a due job and hands it to Run; the dashboard's Run now goes through RunNow.
// The engine's RunTurn does the turn: no destructive tools, approval for write
// tools on schedule triggers, nothing sent when the reply is the silent token.
//
// The runner owns the per-job daily token cap: before a run it reads the
// counter job:<id>:<YYYY-MM-DD> (date in the job's zone) and skips with
// last_status "cap" when it's at or over the cap; after a run it adds the
// run's tokens (from the runs row) to that counter.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// TurnRequest is what RunTurn gets (a chat.TurnOptions subset).
type TurnRequest struct {
	ChatID  string `json:"chatId"`
	Trigger string `json:"trigger"`
	Prompt  string `json:"prompt"`
	JobID   string `json:"jobId"`
	// nil = every enabled connector.
	ToolScope   *[]string `json:"toolScope"`
	ModelRole   string    `json:"modelRole"`
	SilentToken string    `json:"silentToken"`
}

// TurnResponse is what the turn returned.
type TurnResponse struct {
	RunID  string
	Text   string
	Status string
}

// RunTurn runs one turn (engine.RunTurn, adapted in internal/deps).
type RunTurn func(ctx context.Context, r TurnRequest) (TurnResponse, error)

type RunResult struct {
	OK     bool   `json:"ok"`
	RunID  string `json:"runId,omitempty"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status"`
}

// NowResult is runNow's result.
type NowResult struct {
	OK     bool   `json:"ok"`
	RunID  string `json:"runId,omitempty"`
	Detail string `json:"detail,omitempty"`
}

const MaxResultChars = 500

func UsageKey(j db.Job, now int64) string {
	return "job:" + j.ID + ":" + reminders.UTCToLocal(now, j.TZ)[:10]
}

func truncate(s string) string {
	if strx.Len16(s) > MaxResultChars {
		return strx.Slice16(s, MaxResultChars-1) + "…"
	}
	return s
}

type RunnerOptions struct {
	DB      db.Q
	Log     *logx.Logger
	RunTurn RunTurn
	Clock   func() int64
	// Ctx is the context for turns (default context.Background()).
	Ctx context.Context
}

type Runner struct{ o RunnerOptions }

func NewRunner(o RunnerOptions) *Runner {
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	if o.Log == nil {
		o.Log = logx.Silent
	}
	if o.Ctx == nil {
		o.Ctx = context.Background()
	}
	return &Runner{o}
}

func (r *Runner) finish(jobID string, lastRunAt int64, status, result string) {
	if _, err := r.o.DB.Exec("UPDATE jobs SET last_run_at = ?, last_status = ?, last_result = ?, status = 'idle', updated_at = ? WHERE id = ?", lastRunAt, status, result, r.o.Clock(), jobID); err != nil {
		r.o.Log.Error("job finish failed", logx.Fields{"job_id": jobID, "err": err})
	}
}

// Run runs a job the scheduler already claimed (status running). It never fails.
func (r *Runner) Run(job db.Job) RunResult {
	q := r.o.DB
	startedAt := r.o.Clock()
	key := UsageKey(job, startedAt)
	used, err := budget.GetUsage(q, key)
	if err != nil {
		return r.failed(job, startedAt, err)
	}
	if job.DailyTokenCap > 0 && used >= job.DailyTokenCap {
		detail := fmt.Sprintf("Daily token cap reached (%d/%d), skipped.", used, job.DailyTokenCap)
		r.o.Log.Info("job skipped: daily token cap", logx.Fields{"job_id": job.ID, "used": used, "cap": job.DailyTokenCap})
		r.finish(job.ID, startedAt, "cap", detail)
		return RunResult{OK: false, Status: "cap", Detail: detail}
	}
	res, err := func() (res TurnResponse, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("%v", p)
			}
		}()
		return r.o.RunTurn(r.o.Ctx, TurnRequest{ChatID: job.ChatID, Trigger: "schedule", Prompt: job.Prompt, JobID: job.ID, ToolScope: job.ToolScope, ModelRole: job.ModelRole, SilentToken: SilentToken})
	}()
	if err != nil {
		return r.failed(job, startedAt, err)
	}
	var in, out int64
	var cached sql.NullInt64
	err = q.QueryRow("SELECT tokens_in, tokens_out, tokens_cached FROM runs WHERE id = ?", res.RunID).Scan(&in, &out, &cached)
	if err == nil {
		var c *int64
		if cached.Valid {
			c = &cached.Int64
		}
		if _, err := budget.AddUsage(q, key, budget.Tokens(budget.Usage{TokensIn: in, TokensOut: out, TokensCached: c}), r.o.Clock()); err != nil {
			return r.failed(job, startedAt, err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return r.failed(job, startedAt, err)
	}
	silent := strx.Trim(res.Text) == SilentToken
	result := res.Text
	if silent {
		result = SilentToken + " (nothing sent)"
	}
	r.finish(job.ID, startedAt, res.Status, truncate(result))
	rr := RunResult{OK: res.Status == "ok" || res.Status == "awaiting_approval", RunID: res.RunID, Status: res.Status}
	if silent {
		rr.Detail = "Nothing to report."
	}
	return rr
}

func (r *Runner) failed(job db.Job, startedAt int64, err error) RunResult {
	msg := err.Error()
	r.o.Log.Error("job run failed", logx.Fields{"job_id": job.ID, "err": err})
	r.finish(job.ID, startedAt, "error", truncate(msg))
	return RunResult{OK: false, Status: "error", Detail: msg}
}

// RunNow is the dashboard's Run now: it claims the job itself, runs it even
// when paused, and leaves next_run_at alone.
func (r *Runner) RunNow(jobID string) (NowResult, error) {
	q := r.o.DB
	if _, ok, err := Get(q, jobID); err != nil {
		return NowResult{}, err
	} else if !ok {
		return NowResult{OK: false, Detail: fmt.Sprintf("No job %s.", jobID)}, nil
	}
	claimed, ok, err := db.One(q.WQueryRow("UPDATE jobs SET status = 'running', updated_at = ? WHERE id = ? AND status = 'idle' RETURNING "+db.JobCols, r.o.Clock(), jobID), db.ScanJob)
	if err != nil {
		return NowResult{}, err
	}
	if !ok {
		return NowResult{OK: false, Detail: "This job is already running."}, nil
	}
	res := r.Run(claimed)
	detail := res.Detail
	if detail == "" && !res.OK {
		detail = fmt.Sprintf("Finished with status %s.", res.Status)
	}
	return NowResult{OK: res.OK, RunID: res.RunID, Detail: detail}, nil
}
