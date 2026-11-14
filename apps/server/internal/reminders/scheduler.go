package reminders

// The scheduler (reminders/scheduler.ts, PLAN 6, 7). One timer armed for
// min(next due - now, 60 s) plus a 30 s safety sweep. Both paths run the same
// sweep, and the sweep only acts on rows it claimed with a single
// UPDATE ... RETURNING, so two sweeps (or two processes) never fire the same
// reminder twice:
//   reminders: pending -> firing, then done (one-off) or pending at the next fire
//   jobs:      idle -> running, next_run_at advanced, back to idle after the run
//
// A plain reminder never calls the model: it becomes an outbox row with Done
// and Snooze actions. A smart reminder (smart_prompt set) goes to
// OnSmartReminder. Extensions (flows M5, outbound) sweep on the same timer.
//
// Go differences from the single-threaded TS: ticks are serialized by a
// mutex; Poke runs the tick in its own goroutine (TS ran the sweep inside the
// writer's notifyScheduleChanged call), so a writer inside a transaction never
// deadlocks on the single write connection. Idle waits for pokes too.

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	MaxTimerMs          = int64(60_000)
	SweepIntervalMs     = int64(30_000)
	LateAfterMs         = int64(2 * 60_000)
	ReminderOutboxTTLMs = int64(24 * 3600_000)
	// When something is due but still pending right after a sweep (a fire
	// failed), wait this long before trying again instead of spinning.
	retryMs = int64(1000)
)

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Timers is SchedulerTimers. Handles are opaque.
type Timers interface {
	SetTimeout(fn func(), ms int64) any
	ClearTimeout(h any)
	SetInterval(fn func(), ms int64) any
	ClearInterval(h any)
}

// RealTimers runs callbacks on their own goroutines.
type RealTimers struct{}

type intervalHandle struct{ stop chan struct{} }

func (RealTimers) SetTimeout(fn func(), ms int64) any {
	return time.AfterFunc(time.Duration(ms)*time.Millisecond, fn)
}

func (RealTimers) ClearTimeout(h any) {
	if t, ok := h.(*time.Timer); ok {
		t.Stop()
	}
}

func (RealTimers) SetInterval(fn func(), ms int64) any {
	h := &intervalHandle{stop: make(chan struct{})}
	t := time.NewTicker(time.Duration(ms) * time.Millisecond)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fn()
			case <-h.stop:
				return
			}
		}
	}()
	return h
}

func (RealTimers) ClearInterval(h any) {
	if i, ok := h.(*intervalHandle); ok {
		select {
		case <-i.stop:
		default:
			close(i.stop)
		}
	}
}

// Extension is SchedulerExtension: more rows to sweep on the same timer
// (flows, outbound). Each claims its own rows. NextDue's ok false means
// nothing is due. An extension may also implement Recoverer and Idler.
type Extension interface {
	Sweep(now int64) (int, error)
	NextDue() (int64, bool, error)
}

// Recoverer resets rows a crash left mid-fire (SchedulerExtension.recover).
type Recoverer interface{ Recover(now int64) error }

// Idler waits for work the extension started (SchedulerExtension.idle).
type Idler interface{ Idle() }

type SchedulerOptions struct {
	DB  db.Q
	Log *logx.Logger
	// Unix ms; default time.Now.
	Clock  func() int64
	Timers Timers
	// Runs the reminder's smart prompt as a turn (blocking). When it returns
	// an error, the plain text is sent instead so the reminder isn't lost.
	OnSmartReminder func(r db.Reminder) error
	// Runs a claimed job (JobRunner.Run). The scheduler sets the job back to
	// idle once this returns.
	OnJobDue   func(job db.Job) error
	Extensions []Extension
}

// SweepResult is what one sweep claimed.
type SweepResult struct {
	Reminders int `json:"reminders"`
	Jobs      int `json:"jobs"`
}

type Scheduler struct {
	o   SchedulerOptions
	log *logx.Logger

	mu          sync.Mutex
	running     bool
	timer       any
	timerGen    int
	interval    any
	unsubscribe func()

	tickMu sync.Mutex

	flightMu sync.Mutex
	flight   int
	flightCv *sync.Cond
}

func NewScheduler(o SchedulerOptions) *Scheduler {
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	if o.Timers == nil {
		o.Timers = RealTimers{}
	}
	log := o.Log
	if log == nil {
		log = logx.Silent
	}
	s := &Scheduler{o: o, log: log}
	s.flightCv = sync.NewCond(&s.flightMu)
	return s
}

// track runs fn in a goroutine that Idle waits for.
func (s *Scheduler) track(fn func()) {
	s.flightMu.Lock()
	s.flight++
	s.flightMu.Unlock()
	go func() {
		defer func() {
			s.flightMu.Lock()
			s.flight--
			s.flightCv.Broadcast()
			s.flightMu.Unlock()
		}()
		fn()
	}()
}

// Idle returns once the smart reminders, job runs and pokes started so far
// have settled, then waits for the extensions.
func (s *Scheduler) Idle() {
	s.flightMu.Lock()
	for s.flight > 0 {
		s.flightCv.Wait()
	}
	s.flightMu.Unlock()
	for _, ext := range s.o.Extensions {
		if i, ok := ext.(Idler); ok {
			i.Idle()
		}
	}
}

// ReminderButtons is reminderActions: the Done and Snooze buttons.
func ReminderButtons(id string) []db.OutboxAction {
	return []db.OutboxAction{
		{ID: "rem:done:" + id, Label: "Done"},
		{ID: "rem:snooze:" + id + ":10m", Label: "10m"},
		{ID: "rem:snooze:" + id + ":1h", Label: "1j"},
		{ID: "rem:snooze:" + id + ":tomorrow", Label: "Besok 09.00"},
	}
}

// ReminderMessage is "⏰ Bayar listrik" plus "(telat, harusnya 09.00)" when the
// fire is more than two minutes late (downtime). The date is added when it was
// meant for another day. For a series that missed several runs, the note
// names the latest missed one.
func ReminderMessage(r db.Reminder, now int64) string {
	note := ""
	if now-r.FireAt > LateAfterMs {
		meant := r.FireAt
		if r.RRule != nil && *r.RRule != "" && (r.SnoozedUntil == nil || *r.SnoozedUntil != r.FireAt) {
			if m, err := LatestAtOrBefore(Schedule{RRule: r.RRule, TZ: r.TZ, LocalStart: r.LocalStart}, r.FireAt, now); err == nil {
				meant = m
			}
		}
		when := FormatClock(meant, r.TZ)
		if FormatDate(meant, r.TZ, now) != FormatDate(now, r.TZ, now) {
			when = FormatDate(meant, r.TZ, now) + " " + when
		}
		note = " (telat, harusnya " + when + ")"
	}
	return "⏰ " + r.Text + note
}

// advance moves a fired reminder on: one-off -> done; series -> next
// occurrence after now (several missed runs collapse into this fire).
func (s *Scheduler) advance(q db.Q, r db.Reminder, now int64, outboxID string) error {
	cols := "last_fired_at = ?, snoozed_until = NULL, updated_at = ?"
	args := []any{now, now}
	if outboxID != "" {
		cols += ", last_outbox_id = ?"
		args = append(args, outboxID)
	}
	if r.RRule == nil {
		cols += ", status = 'done'"
	} else {
		next, ok, err := NextOccurrence(scheduleOf(r), now)
		if err != nil {
			s.log.Error("reminder rule broke, stopping the series", logx.Fields{"reminder_id": r.ID, "err": err})
			ok = false
		}
		if !ok {
			cols += ", status = 'done'"
		} else {
			cols += ", status = 'pending', fire_at = ?, scheduled_for = ?"
			args = append(args, next, next)
		}
	}
	_, err := q.Exec("UPDATE reminders SET "+cols+" WHERE id = ? AND status = 'firing'", append(args, r.ID)...)
	return err
}

// firePlain enqueues the reminder text and advances, in one transaction.
// false when the chat is gone (the reminder is cancelled then).
func (s *Scheduler) firePlain(r db.Reminder, now int64) (bool, error) {
	q := s.o.DB
	chat, ok, err := chats.Get(q, r.ChatID)
	if err != nil {
		return false, err
	}
	if !ok {
		if _, err := q.Exec("UPDATE reminders SET status = 'cancelled', updated_at = ? WHERE id = ?", now, r.ID); err != nil {
			return false, err
		}
		_, err := events.Log(q, events.Input{Level: "warn", Kind: "reminder.orphan", Message: fmt.Sprintf("Reminder %s was cancelled: its chat %s no longer exists.", r.ID, r.ChatID)}, now)
		return false, err
	}
	payload := db.OutboxPayload{Text: ReminderMessage(r, now), Actions: ReminderButtons(r.ID)}
	expires := now + ReminderOutboxTTLMs
	return true, q.Tx(func(tx db.Q) error {
		row, err := outbox.Enqueue(tx, outbox.EnqueueInput{ChatID: r.ChatID, Channel: chat.Channel, Payload: payload, Source: "reminder:" + r.ID, ExpiresAt: &expires}, now)
		if err != nil {
			return err
		}
		return s.advance(tx, r, now, row.ID)
	})
}

func (s *Scheduler) fireSmart(r db.Reminder) {
	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("%v", p)
			}
		}()
		return s.o.OnSmartReminder(r)
	}()
	if err == nil {
		if err := s.advance(s.o.DB, r, s.o.Clock(), ""); err != nil {
			s.log.Error("smart reminder advance failed", logx.Fields{"reminder_id": r.ID, "err": err})
		}
		return
	}
	s.log.Warn("smart reminder failed, sending the plain text", logx.Fields{"reminder_id": r.ID, "err": err})
	sent, err := s.firePlain(r, s.o.Clock())
	if err != nil {
		s.log.Error("reminder fire failed", logx.Fields{"reminder_id": r.ID, "err": err})
		return
	}
	if sent {
		outbox.Notify()
	}
}

func (s *Scheduler) sweepReminders(now int64) (int, error) {
	q := s.o.DB
	rows, qerr := q.WQuery("UPDATE reminders SET status = 'firing', updated_at = ? WHERE status = 'pending' AND fire_at <= ? RETURNING "+db.ReminderCols, now, now)
	claimed, err := db.All(rows, qerr, db.ScanReminder)
	if err != nil {
		return 0, err
	}
	enqueued := 0
	for _, r := range claimed {
		if r.SmartPrompt != nil && *r.SmartPrompt != "" && s.o.OnSmartReminder != nil {
			r := r
			s.track(func() { s.fireSmart(r) })
			continue
		}
		sent, err := s.firePlain(r, now)
		if err != nil {
			// Leave it pending so the next sweep retries it (delivery is at least once).
			s.log.Error("reminder fire failed", logx.Fields{"reminder_id": r.ID, "err": err})
			_, _ = q.Exec("UPDATE reminders SET status = 'pending' WHERE id = ? AND status = 'firing'", r.ID)
			continue
		}
		if sent {
			enqueued++
		}
	}
	if enqueued > 0 {
		outbox.Notify()
	}
	return len(claimed), nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Scheduler) sweepJobs(now int64) (int, error) {
	q := s.o.DB
	rows, qerr := q.WQuery("UPDATE jobs SET status = 'running', updated_at = ? WHERE status = 'idle' AND enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ? RETURNING "+db.JobCols, now, now)
	claimed, err := db.All(rows, qerr, db.ScanJob)
	if err != nil {
		return 0, err
	}
	for _, job := range claimed {
		rr := job.RRule
		var next *int64
		n, ok, err := NextOccurrence(Schedule{RRule: &rr, TZ: job.TZ, LocalStart: job.LocalStart}, now)
		if err != nil {
			s.log.Error("job rule broke, it won't run again until edited", logx.Fields{"job_id": job.ID, "err": err})
		} else if ok {
			next = &n
		}
		if _, err := q.Exec("UPDATE jobs SET next_run_at = ? WHERE id = ?", next, job.ID); err != nil {
			return len(claimed), err
		}
		id := job.ID
		setIdle := func() {
			if _, err := q.Exec("UPDATE jobs SET status = 'idle', updated_at = ? WHERE id = ?", s.o.Clock(), id); err != nil {
				s.log.Error("job reset failed", logx.Fields{"job_id": id, "err": err})
			}
		}
		if s.o.OnJobDue == nil {
			s.log.Warn("job due but no job runner is wired", logx.Fields{"job_id": id})
			setIdle()
			continue
		}
		job.NextRunAt = next
		j := job
		s.track(func() {
			defer setIdle()
			err := func() (err error) {
				defer func() {
					if p := recover(); p != nil {
						err = fmt.Errorf("%v", p)
					}
				}()
				return s.o.OnJobDue(j)
			}()
			if err != nil {
				s.log.Error("job run failed", logx.Fields{"job_id": id, "err": err})
				if _, err := q.Exec("UPDATE jobs SET last_status = 'error', last_result = ?, last_run_at = ? WHERE id = ?", strx.Slice16(errText(err), 500), s.o.Clock(), id); err != nil {
					s.log.Error("job result write failed", logx.Fields{"job_id": id, "err": err})
				}
			}
		})
	}
	return len(claimed), nil
}

// Sweep is one sweep; what the timer and the safety interval both call.
func (s *Scheduler) Sweep() SweepResult {
	now := s.o.Clock()
	var res SweepResult
	var err error
	if res.Reminders, err = s.sweepReminders(now); err != nil {
		s.log.Error("reminder sweep failed", logx.Fields{"err": err})
	}
	if res.Jobs, err = s.sweepJobs(now); err != nil {
		s.log.Error("job sweep failed", logx.Fields{"err": err})
	}
	for _, ext := range s.o.Extensions {
		func() {
			defer func() {
				if p := recover(); p != nil {
					s.log.Error("scheduler extension sweep failed", logx.Fields{"err": fmt.Errorf("%v", p)})
				}
			}()
			if _, err := ext.Sweep(now); err != nil {
				s.log.Error("scheduler extension sweep failed", logx.Fields{"err": err})
			}
		}()
	}
	return res
}

func (s *Scheduler) nextDue() (int64, bool, error) {
	q := s.o.DB
	var best int64
	found := false
	consider := func(v sql.NullInt64) {
		if v.Valid && (!found || v.Int64 < best) {
			best, found = v.Int64, true
		}
	}
	var a, b sql.NullInt64
	if err := q.QueryRow("SELECT min(fire_at) FROM reminders WHERE status = 'pending'").Scan(&a); err != nil {
		return 0, false, err
	}
	if err := q.QueryRow("SELECT min(next_run_at) FROM jobs WHERE enabled = 1 AND status = 'idle' AND next_run_at IS NOT NULL").Scan(&b); err != nil {
		return 0, false, err
	}
	consider(a)
	consider(b)
	for _, ext := range s.o.Extensions {
		v, ok, err := ext.NextDue()
		if err != nil {
			return 0, false, err
		}
		consider(sql.NullInt64{Int64: v, Valid: ok})
	}
	return best, found, nil
}

func (s *Scheduler) arm() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	if s.timer != nil {
		s.o.Timers.ClearTimeout(s.timer)
		s.timer = nil
	}
	s.mu.Unlock()
	delay := MaxTimerMs
	due, ok, err := s.nextDue()
	if err != nil {
		s.log.Error("could not read the next due time", logx.Fields{"err": err})
	} else if ok {
		d := due - s.o.Clock()
		if d <= 0 {
			delay = retryMs
		} else {
			delay = min(d, MaxTimerMs)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.timerGen++
	gen := s.timerGen
	s.timer = s.o.Timers.SetTimeout(func() {
		s.mu.Lock()
		if s.timerGen == gen {
			s.timer = nil
		}
		s.mu.Unlock()
		s.tick()
	}, delay)
}

func (s *Scheduler) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Scheduler) tick() {
	if !s.isRunning() {
		return
	}
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	s.Sweep()
	s.arm()
}

// Start recovers rows a crash left mid-fire, sweeps once (overdue reminders
// fire now, with the "telat" note) and arms the timers. It returns after that
// sweep.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()
	// Rows a crash left mid-fire: fire them again (at least once beats never).
	q := s.o.DB
	now := s.o.Clock()
	if _, err := q.Exec("UPDATE reminders SET status = 'pending', updated_at = ? WHERE status = 'firing'", now); err != nil {
		return err
	}
	if _, err := q.Exec("UPDATE jobs SET status = 'idle', updated_at = ? WHERE status = 'running'", now); err != nil {
		return err
	}
	for _, ext := range s.o.Extensions {
		if r, ok := ext.(Recoverer); ok {
			if err := r.Recover(now); err != nil {
				return err
			}
		}
	}
	unsub := OnScheduleChanged(s.Poke)
	s.mu.Lock()
	s.unsubscribe = unsub
	s.interval = s.o.Timers.SetInterval(s.tick, SweepIntervalMs)
	s.mu.Unlock()
	s.tick()
	return nil
}

// Stop clears the timers. Smart reminders and job runs already started keep
// going (Idle waits for them).
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	if s.unsubscribe != nil {
		s.unsubscribe()
		s.unsubscribe = nil
	}
	if s.timer != nil {
		s.o.Timers.ClearTimeout(s.timer)
	}
	if s.interval != nil {
		s.o.Timers.ClearInterval(s.interval)
	}
	s.timer, s.interval = nil, nil
	s.timerGen++
}

// Poke sweeps now and re-arms (after a reminder or job changed). The tick
// runs in its own goroutine.
func (s *Scheduler) Poke() {
	if s.isRunning() {
		s.track(s.tick)
	}
}
