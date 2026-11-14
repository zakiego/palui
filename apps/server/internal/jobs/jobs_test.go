package jobs_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

var (
	NOW  = tu.NOW
	WITA = tu.WITA
	MIN  = tu.MIN
	HOUR = tu.HOUR
)

func local(ms int64) string { return reminders.UTCToLocal(ms, WITA)[:16] }

func l2u(t *testing.T, s string) int64 {
	t.Helper()
	ms, err := reminders.LocalToUTC(s, WITA)
	tu.Must(t, err)
	return ms
}

func mj(t *testing.T) func(db.Job, error) db.Job {
	return func(j db.Job, err error) db.Job {
		t.Helper()
		tu.Must(t, err)
		return j
	}
}

func getJob(t *testing.T, q db.Q, id string) db.Job {
	t.Helper()
	j, ok, err := jobs.Get(q, id)
	tu.Must(t, err)
	if !ok {
		t.Fatalf("no job %s", id)
	}
	return j
}

func isJobErr(err error) bool {
	var je *jobs.JobError
	return errors.As(err, &je)
}

func TestJobService(t *testing.T) {
	t.Run("computes next_run_at in wall-clock time", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.DailyBriefing(chatID, WITA, NOW), NOW))
		if local(*j.NextRunAt) != "2026-11-11T07:00" {
			t.Fatal(local(*j.NextRunAt))
		}
		if s, _ := jobs.DescribeSchedule(j, NOW); s != "Tiap hari, 07.00 WITA" {
			t.Fatal(s)
		}
		if s, _ := jobs.DescribeNextRun(j, NOW); s != "Besok, Rabu 11 Nov, 07.00 WITA" {
			t.Fatal(s)
		}
	})
	t.Run("runs the heartbeat every 30 minutes from 08:00 to 22:00 inclusive", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW), NOW))
		if local(*j.NextRunAt) != "2026-11-10T10:30" {
			t.Fatal(local(*j.NextRunAt))
		}
		rr := j.RRule
		s := reminders.Schedule{RRule: &rr, TZ: WITA, LocalStart: j.LocalStart}
		occ, err := reminders.OccurrencesAfter(s, l2u(t, "2026-11-11T00:00"), 40)
		tu.Must(t, err)
		var day []string
		for _, o := range occ {
			if strings.HasPrefix(local(o), "2026-11-11") {
				day = append(day, local(o)[11:])
			}
		}
		if len(day) != 29 || day[0] != "08:00" || day[28] != "22:00" || strings.Contains(strings.Join(day, ","), "22:30") || strings.Contains(strings.Join(day, ","), "07:30") {
			t.Fatal(day)
		}
		// After 22:00 the next run is 08:00 the next morning.
		next, err := reminders.OccurrencesAfter(s, l2u(t, "2026-11-11T22:00"), 1)
		tu.Must(t, err)
		if local(next[0]) != "2026-11-12T08:00" {
			t.Fatal(local(next[0]))
		}
		if s, _ := jobs.DescribeSchedule(j, NOW); s != "Tiap hari, tiap 30 menit 08.00 sampai 22.00 WITA" {
			t.Fatal(s)
		}
		if !strings.Contains(jobs.HeartbeatRRule, "BYSETPOS=1,2,") {
			t.Fatal(jobs.HeartbeatRRule)
		}
	})
	t.Run("pauses, resumes from now, edits and deletes", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.CreateInput{Name: "Cek", RRule: "FREQ=WEEKLY;BYDAY=MO", TZ: WITA, LocalStart: "2026-11-16T09:00", Prompt: "cek", ChatID: chatID}, NOW))
		if local(*j.NextRunAt) != "2026-11-16T09:00" {
			t.Fatal(local(*j.NextRunAt))
		}
		if p := mj(t)(jobs.SetEnabled(d, j.ID, false, NOW)); p.NextRunAt != nil {
			t.Fatal("paused has a next run")
		}
		// Resumed after Monday: the missed run is skipped.
		if r := mj(t)(jobs.SetEnabled(d, j.ID, true, l2u(t, "2026-11-17T09:00"))); local(*r.NextRunAt) != "2026-11-23T09:00" {
			t.Fatal(local(*r.NextRunAt))
		}
		if e := mj(t)(jobs.Update(d, j.ID, jobs.Patch{RRule: tu.Ptr("FREQ=DAILY"), LocalStart: tu.Ptr("2026-11-10T18:00")}, NOW)); local(*e.NextRunAt) != "2026-11-10T18:00" {
			t.Fatal(local(*e.NextRunAt))
		}
		if _, err := jobs.Update(d, j.ID, jobs.Patch{RRule: tu.Ptr("FREQ=NEVER")}, NOW); !isJobErr(err) {
			t.Fatal(err)
		}
		if _, err := jobs.Update(d, j.ID, jobs.Patch{Prompt: tu.Ptr(" ")}, NOW); !isJobErr(err) {
			t.Fatal(err)
		}
		scope := []string{"notes"}
		if u := mj(t)(jobs.Update(d, j.ID, jobs.Patch{SetToolScope: true, ToolScope: &scope}, NOW)); u.ToolScope == nil || (*u.ToolScope)[0] != "notes" {
			t.Fatal(u.ToolScope)
		}
		ok, err := jobs.Delete(d, j.ID)
		tu.Must(t, err)
		if !ok {
			t.Fatal("not deleted")
		}
		if _, found, _ := jobs.Get(d, j.ID); found {
			t.Fatal("still there")
		}
	})
	t.Run("lists by next run with paused jobs last", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		a := mj(t)(jobs.Create(d, jobs.DailyBriefing(chatID, WITA, NOW), NOW))
		b := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW), NOW))
		in := jobs.DailyBriefing(chatID, WITA, NOW)
		in.Name, in.Enabled = "Paused", tu.Ptr(false)
		c := mj(t)(jobs.Create(d, in, NOW))
		ids := func(o jobs.ListOptions) []string {
			js, err := jobs.List(d, o)
			tu.Must(t, err)
			var out []string
			for _, j := range js {
				out = append(out, j.ID)
			}
			return out
		}
		if got := ids(jobs.ListOptions{}); !reflect.DeepEqual(got, []string{b.ID, a.ID, c.ID}) {
			t.Fatal(got)
		}
		if got := ids(jobs.ListOptions{Enabled: tu.Ptr(true)}); !reflect.DeepEqual(got, []string{b.ID, a.ID}) {
			t.Fatal(got)
		}
	})
	t.Run("rejects bad input", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		for _, in := range []jobs.CreateInput{
			{Name: "x", RRule: "FREQ=DAILY", TZ: "Bad/Zone", Prompt: "p", ChatID: chatID},
			{Name: "x", RRule: "FREQ=DAILY", TZ: WITA, Prompt: "p", ChatID: "nope"},
			{Name: "x", RRule: "FREQ=DAILY", TZ: WITA, Prompt: "p", ChatID: chatID, DailyTokenCap: tu.Ptr(int64(-1))},
			{Name: " ", RRule: "FREQ=DAILY", TZ: WITA, Prompt: "p", ChatID: chatID},
			{Name: "x", RRule: "FREQ=DAILY", TZ: WITA, Prompt: strings.Repeat("p", 8001), ChatID: chatID},
		} {
			if _, err := jobs.Create(d, in, NOW); !isJobErr(err) {
				t.Fatalf("%+v: %v", in.Name, err)
			}
		}
	})
}

type fakeTurn struct {
	mu    sync.Mutex
	calls []jobs.TurnRequest
}

func (f *fakeTurn) run(d db.Q, text string, tokens, cached int64) jobs.RunTurn {
	return func(_ context.Context, r jobs.TurnRequest) (jobs.TurnResponse, error) {
		f.mu.Lock()
		f.calls = append(f.calls, r)
		f.mu.Unlock()
		id := secret.NewID("run")
		_, err := d.Exec("INSERT INTO runs (id, chat_id, job_id, trigger, tokens_in, tokens_out, tokens_cached, status, started_at) VALUES (?, ?, ?, 'schedule', ?, 0, ?, 'ok', ?)", id, r.ChatID, r.JobID, tokens, cached, NOW)
		return jobs.TurnResponse{RunID: id, Text: text, Status: "ok"}, err
	}
}

func (f *fakeTurn) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func runner(d db.Q, rt jobs.RunTurn, clock func() int64) *jobs.Runner {
	return jobs.NewRunner(jobs.RunnerOptions{DB: d, Log: logx.Silent, RunTurn: rt, Clock: clock})
}

func fixed(ms int64) func() int64 { return func() int64 { return ms } }

func usage(t *testing.T, d db.Q, key string) int64 {
	v, err := budget.GetUsage(d, key)
	tu.Must(t, err)
	return v
}

func TestJobRunner(t *testing.T) {
	t.Run("passes the engine options through and records the result", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW), NOW))
		f := &fakeTurn{}
		res, err := runner(d, f.run(d, jobs.SilentToken, 1200, 0), fixed(NOW)).RunNow(j.ID)
		tu.Must(t, err)
		if !res.OK || res.Detail != "Nothing to report." {
			t.Fatalf("%+v", res)
		}
		if !reflect.DeepEqual(f.calls[0], jobs.TurnRequest{ChatID: chatID, Trigger: "schedule", Prompt: j.Prompt, JobID: j.ID, ToolScope: nil, ModelRole: "fast", SilentToken: "NOTHING"}) {
			t.Fatalf("%+v", f.calls[0])
		}
		after := getJob(t, d, j.ID)
		if after.Status != "idle" || *after.LastStatus != "ok" || *after.LastResult != "NOTHING (nothing sent)" || *after.LastRunAt != NOW || *after.NextRunAt != *j.NextRunAt {
			t.Fatalf("%+v", after)
		}
		// Run now leaves the schedule alone, and the runner never writes the outbox.
		var n int
		tu.Must(t, d.QueryRow("SELECT count(*) FROM outbox").Scan(&n))
		if n != 0 || usage(t, d, jobs.UsageKey(j, NOW)) != 1200 || jobs.UsageKey(j, NOW) != "job:"+j.ID+":2026-11-10" {
			t.Fatal("usage or outbox")
		}
	})
	t.Run("counts a run's cache reads at a tenth against the job's daily cap", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		in := jobs.Heartbeat(chatID, WITA, NOW)
		in.DailyTokenCap = tu.Ptr(int64(5000))
		j := mj(t)(jobs.Create(d, in, NOW))
		// 20,000 tokens in, 18,000 of them read from the cache: 2,000 + 1,800.
		f := &fakeTurn{}
		r := runner(d, f.run(d, jobs.SilentToken, 20_000, 18_000), fixed(NOW))
		_, err := r.RunNow(j.ID)
		tu.Must(t, err)
		if usage(t, d, jobs.UsageKey(j, NOW)) != 3800 {
			t.Fatal(usage(t, d, jobs.UsageKey(j, NOW)))
		}
		// Still under the 5,000 cap, where the raw 20,000 would have stopped the job for the day.
		if res, _ := r.RunNow(j.ID); !res.OK || f.n() != 2 || usage(t, d, jobs.UsageKey(j, NOW)) != 7600 {
			t.Fatalf("%+v", res)
		}
		if res, _ := r.RunNow(j.ID); res.OK || *getJob(t, d, j.ID).LastStatus != "cap" || f.n() != 2 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("skips with status cap once the daily cap is used up, and resets the next day", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		in := jobs.DailyBriefing(chatID, WITA, NOW)
		in.DailyTokenCap = tu.Ptr(int64(5000))
		j := mj(t)(jobs.Create(d, in, NOW))
		_, err := budget.AddUsage(d, jobs.UsageKey(j, NOW), 5000, NOW)
		tu.Must(t, err)
		f := &fakeTurn{}
		now := NOW
		r := runner(d, f.run(d, "Pagi!", 1000, 0), func() int64 { return now })
		res, _ := r.RunNow(j.ID)
		if res.OK || f.n() != 0 || res.Detail != "Daily token cap reached (5000/5000), skipped." {
			t.Fatalf("%+v", res)
		}
		if g := getJob(t, d, j.ID); *g.LastStatus != "cap" || g.Status != "idle" {
			t.Fatalf("%+v", g)
		}
		now = l2u(t, "2026-11-11T07:00")
		if res, _ := r.RunNow(j.ID); !res.OK || *getJob(t, d, j.ID).LastResult != "Pagi!" {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("truncates long results and records errors", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.DailyBriefing(chatID, WITA, NOW), NOW))
		f := &fakeTurn{}
		_, err := runner(d, f.run(d, strings.Repeat("x", 2000), 1000, 0), fixed(NOW)).RunNow(j.ID)
		tu.Must(t, err)
		if r := *getJob(t, d, j.ID).LastResult; len([]rune(r)) != 500 || !strings.HasSuffix(r, "…") {
			t.Fatal(len(r))
		}
		failing := runner(d, func(context.Context, jobs.TurnRequest) (jobs.TurnResponse, error) {
			return jobs.TurnResponse{}, errors.New("model off")
		}, fixed(NOW))
		res, _ := failing.RunNow(j.ID)
		if res.OK || res.Detail != "model off" {
			t.Fatalf("%+v", res)
		}
		if g := getJob(t, d, j.ID); *g.LastStatus != "error" || *g.LastResult != "model off" || g.Status != "idle" {
			t.Fatalf("%+v", g)
		}
	})
	t.Run("reports a non-ok status and an unknown job", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.DailyBriefing(chatID, WITA, NOW), NOW))
		r := runner(d, func(context.Context, jobs.TurnRequest) (jobs.TurnResponse, error) {
			return jobs.TurnResponse{RunID: "run_x", Text: "", Status: "budget"}, nil
		}, fixed(NOW))
		if res, _ := r.RunNow(j.ID); res.OK || res.Detail != "Finished with status budget." {
			t.Fatalf("%+v", res)
		}
		if res, _ := r.RunNow("job_missing"); res.OK || res.Detail != "No job job_missing." {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("refuses Run now while the job is running", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.DailyBriefing(chatID, WITA, NOW), NOW))
		started, release := make(chan struct{}), make(chan struct{})
		r := runner(d, func(context.Context, jobs.TurnRequest) (jobs.TurnResponse, error) {
			close(started)
			<-release
			return jobs.TurnResponse{RunID: "run_x", Text: "ok", Status: "ok"}, nil
		}, fixed(NOW))
		done := make(chan jobs.NowResult)
		go func() {
			res, _ := r.RunNow(j.ID)
			done <- res
		}()
		<-started
		if res, _ := r.RunNow(j.ID); !reflect.DeepEqual(res, jobs.NowResult{OK: false, Detail: "This job is already running."}) {
			t.Fatalf("%+v", res)
		}
		close(release)
		if res := <-done; !res.OK {
			t.Fatalf("%+v", res)
		}
	})
}

func newSched(d db.Q, clock func() int64, timers reminders.Timers, onJobDue func(db.Job) error) *reminders.Scheduler {
	return reminders.NewScheduler(reminders.SchedulerOptions{DB: d, Log: logx.Silent, Clock: clock, Timers: timers, OnJobDue: onJobDue})
}

func TestSchedulerWithJobs(t *testing.T) {
	t.Run("claims a due job once, advances next_run_at and hands it to the runner", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW), NOW))
		clock := tu.NewFakeClock(NOW)
		f := &fakeTurn{}
		r := runner(d, f.run(d, jobs.SilentToken, 1000, 0), clock.Now)
		var mu sync.Mutex
		due := 0
		s := newSched(d, clock.Now, clock, func(job db.Job) error {
			mu.Lock()
			due++
			mu.Unlock()
			r.Run(job)
			return nil
		})
		tu.Must(t, s.Start())
		if due != 0 {
			t.Fatal("ran early")
		}
		clock.Advance(30*MIN, s.Idle)
		after := getJob(t, d, j.ID)
		if due != 1 || f.n() != 1 || local(*after.NextRunAt) != "2026-11-10T11:00" || after.Status != "idle" {
			t.Fatalf("due %d calls %d %+v", due, f.n(), after)
		}
		s.Stop()
	})
	t.Run("runs a job that missed several runs during downtime once", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW-5*HOUR), NOW-5*HOUR))
		var mu sync.Mutex
		due := 0
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), func(db.Job) error {
			mu.Lock()
			due++
			mu.Unlock()
			return nil
		})
		tu.Must(t, s.Start())
		s.Idle()
		s.Sweep()
		if due != 1 || local(*getJob(t, d, j.ID).NextRunAt) != "2026-11-10T10:30" {
			t.Fatal(due)
		}
		s.Stop()
	})
	t.Run("doesn't claim paused jobs", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		in := jobs.Heartbeat(chatID, WITA, NOW-HOUR)
		in.Enabled = tu.Ptr(false)
		mj(t)(jobs.Create(d, in, NOW-HOUR))
		called := false
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), func(db.Job) error { called = true; return nil })
		tu.Must(t, s.Start())
		s.Idle()
		if called {
			t.Fatal("claimed a paused job")
		}
		s.Stop()
	})
	t.Run("records an error from the runner and sets the job idle", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW-HOUR), NOW-HOUR))
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), func(db.Job) error { return errors.New(strings.Repeat("e", 600)) })
		tu.Must(t, s.Start())
		s.Idle()
		g := getJob(t, d, j.ID)
		if g.Status != "idle" || *g.LastStatus != "error" || len(*g.LastResult) != 500 {
			t.Fatalf("%+v", g)
		}
		s.Stop()
	})
	t.Run("sets a due job idle when no runner is wired", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		j := mj(t)(jobs.Create(d, jobs.Heartbeat(chatID, WITA, NOW-HOUR), NOW-HOUR))
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), nil)
		tu.Must(t, s.Start())
		if g := getJob(t, d, j.ID); g.Status != "idle" || local(*g.NextRunAt) != "2026-11-10T10:30" {
			t.Fatalf("%+v", g)
		}
		s.Stop()
	})
}
