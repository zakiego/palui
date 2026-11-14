package reminders_test

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

const DAY = 24 * tu.HOUR

func outboxRows(t *testing.T, q db.Q) []db.Outbox {
	t.Helper()
	rows, err := q.Query("SELECT " + db.OutboxCols + " FROM outbox ORDER BY rowid")
	out, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(t, err)
	return out
}

func getRem(t *testing.T, q db.Q, id string) db.Reminder {
	t.Helper()
	r, ok, err := reminders.Get(q, id)
	tu.Must(t, err)
	if !ok {
		t.Fatalf("no reminder %s", id)
	}
	return r
}

func create(t *testing.T, q db.Q, in reminders.CreateInput, now int64) db.Reminder {
	t.Helper()
	r, err := reminders.Create(q, in, now)
	tu.Must(t, err)
	return r
}

func newSched(q db.Q, clock func() int64, timers reminders.Timers, o reminders.SchedulerOptions) *reminders.Scheduler {
	o.DB, o.Log, o.Clock, o.Timers = q, logx.Silent, clock, timers
	return reminders.NewScheduler(o)
}

func fixed(ms int64) func() int64 { return func() int64 { return ms } }

func TestFiringPlainReminders(t *testing.T) {
	t.Run("enqueues the text with actions, marks a one-off done and notifies the worker", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "bayar listrik", LocalStart: "2026-11-10T10:05", TZ: WITA}, NOW)
		clock := tu.NewFakeClock(NOW)
		var notified atomic.Int32
		off := outbox.OnEnqueued(func() { notified.Add(1) })
		defer off()
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if n := len(outboxRows(t, d)); n != 0 {
			t.Fatal(n)
		}
		clock.Advance(5*tu.MIN, nil)
		rows := outboxRows(t, d)
		if len(rows) != 1 {
			t.Fatal(len(rows))
		}
		row := rows[0]
		if row.Source != "reminder:"+r.ID || row.Channel != "telegram" || *row.ExpiresAt != NOW+5*tu.MIN+DAY || row.Payload.Text != "⏰ bayar listrik" {
			t.Fatalf("%+v", row)
		}
		if !slices.Equal(row.Payload.Actions, reminders.ReminderButtons(r.ID)) || row.Payload.Actions[2] != (db.OutboxAction{ID: "rem:snooze:" + r.ID + ":1h", Label: "1j"}) {
			t.Fatalf("%+v", row.Payload.Actions)
		}
		after := getRem(t, d, r.ID)
		if after.Status != "done" || *after.LastOutboxID != row.ID || *after.LastFiredAt != NOW+5*tu.MIN {
			t.Fatalf("%+v", after)
		}
		if notified.Load() == 0 {
			t.Fatal("worker not notified")
		}
		s.Stop()
	})
	t.Run("moves a series to its next occurrence", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "minum obat", LocalStart: "2026-11-10T10:01", TZ: WITA, RRule: "FREQ=DAILY"}, NOW)
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		clock.Advance(tu.MIN, nil)
		after := getRem(t, d, r.ID)
		if after.Status != "pending" || reminders.UTCToLocal(after.FireAt, WITA) != "2026-11-11T10:01:00" || after.ScheduledFor != after.FireAt {
			t.Fatalf("%+v", after)
		}
		s.Stop()
	})
	t.Run("uses the chat's channel", func(t *testing.T) {
		d, _, otherID := tu.Setup(t)
		create(t, d, reminders.CreateInput{ChatID: otherID, Text: "x", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW)
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if outboxRows(t, d)[0].Channel != "whatsapp" {
			t.Fatal("channel")
		}
		s.Stop()
	})
	t.Run("cancels a reminder whose chat is gone", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW)
		_, err := d.Exec("DELETE FROM chats WHERE id = ?", chatID)
		tu.Must(t, err)
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if getRem(t, d, r.ID).Status != "cancelled" || len(outboxRows(t, d)) != 0 {
			t.Fatal("not cancelled")
		}
		var kind string
		tu.Must(t, d.QueryRow("SELECT kind FROM events WHERE kind = 'reminder.orphan'").Scan(&kind))
		s.Stop()
	})
}

func TestRestartWithOverdueReminders(t *testing.T) {
	t.Run("fires an overdue one-off once with the telat note", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "bayar listrik", LocalStart: "2026-11-10T09:00", TZ: WITA}, NOW-2*tu.HOUR)
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		clock.Advance(5*tu.MIN, nil)
		rows := outboxRows(t, d)
		if len(rows) != 1 || rows[0].Payload.Text != "⏰ bayar listrik (telat, harusnya 09.00)" || getRem(t, d, r.ID).Status != "done" {
			t.Fatalf("%+v", rows)
		}
		s.Stop()
	})
	t.Run("fires a series that missed several runs once, then resumes on schedule", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		// Created Saturday for daily 08:00; the process was down until Tuesday 10:00.
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "jalan pagi", LocalStart: "2026-11-07T08:00", TZ: WITA, RRule: "FREQ=DAILY"}, l2u(t, "2026-11-07T07:00", WITA))
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		clock.Advance(10*tu.MIN, nil)
		rows := outboxRows(t, d)
		// The note names the latest missed run (today 08:00), not Saturday's.
		if len(rows) != 1 || rows[0].Payload.Text != "⏰ jalan pagi (telat, harusnya 08.00)" {
			t.Fatalf("%+v", rows)
		}
		if l := reminders.UTCToLocal(getRem(t, d, r.ID).FireAt, WITA); l != "2026-11-11T08:00:00" {
			t.Fatal(l)
		}
		s.Stop()
	})
	t.Run("shows the date when the missed run was on another day", func(t *testing.T) {
		fireAt := l2u(t, "2026-11-08T21:00", WITA)
		if m := reminders.ReminderMessage(db.Reminder{Text: "x", FireAt: fireAt, TZ: WITA, LocalStart: "2026-11-08T21:00:00"}, NOW); m != "⏰ x (telat, harusnya Minggu 8 Nov 21.00)" {
			t.Fatal(m)
		}
	})
	t.Run("doesn't add the note within two minutes", func(t *testing.T) {
		if m := reminders.ReminderMessage(db.Reminder{Text: "x", FireAt: NOW - 2*tu.MIN, TZ: WITA}, NOW); m != "⏰ x" {
			t.Fatal(m)
		}
	})
	t.Run("recovers rows a crash left in firing", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW)
		_, err := d.Exec("UPDATE reminders SET status = 'firing' WHERE id = ?", r.ID)
		tu.Must(t, err)
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if len(outboxRows(t, d)) != 1 {
			t.Fatal("not recovered")
		}
		s.Stop()
	})
}

func TestClaimIdempotency(t *testing.T) {
	d, chatID, _ := tu.Setup(t)
	for i := 0; i < 5; i++ {
		create(t, d, reminders.CreateInput{ChatID: chatID, Text: "r", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW)
	}
	a := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{})
	b := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{})
	var wg sync.WaitGroup
	var total atomic.Int32
	for _, s := range []*reminders.Scheduler{a, b, a} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			total.Add(int32(s.Sweep().Reminders))
		}()
	}
	wg.Wait()
	if total.Load() != 5 || len(outboxRows(t, d)) != 5 {
		t.Fatalf("claimed %d, rows %d", total.Load(), len(outboxRows(t, d)))
	}
	a.Sweep()
	if len(outboxRows(t, d)) != 5 {
		t.Fatal("fired twice")
	}
}

func TestTimerArming(t *testing.T) {
	t.Run("caps the delay at 60 s for a reminder 25 days out and doesn't spin", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		create(t, d, reminders.CreateInput{ChatID: chatID, Text: "jauh", LocalStart: "2026-12-05T10:00", TZ: WITA}, NOW)
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if clock.LastArmed() != reminders.MaxTimerMs {
			t.Fatal(clock.LastArmed())
		}
		ran := clock.Advance(10*tu.MIN, nil)
		// 10 timer ticks plus 20 safety sweeps, nothing like a 1 ms loop.
		if ran > 31 {
			t.Fatal(ran)
		}
		for _, ms := range clock.Armed() {
			if ms != reminders.MaxTimerMs {
				t.Fatal(clock.Armed())
			}
		}
		if len(outboxRows(t, d)) != 0 {
			t.Fatal("fired")
		}
		s.Stop()
	})
	t.Run("arms for the exact delay when the next fire is closer than 60 s", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		create(t, d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-10T10:00:20", TZ: WITA}, NOW)
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if clock.LastArmed() != 20_000 {
			t.Fatal(clock.LastArmed())
		}
		s.Stop()
	})
	t.Run("re-arms when a reminder is created or snoozed (poke)", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{})
		tu.Must(t, s.Start())
		if clock.LastArmed() != reminders.MaxTimerMs {
			t.Fatal(clock.LastArmed())
		}
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-10T10:00:05", TZ: WITA}, NOW)
		s.Idle()
		if clock.LastArmed() != 5000 {
			t.Fatal(clock.LastArmed())
		}
		_, err := reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Until: NOW + 3000}, NOW)
		tu.Must(t, err)
		s.Idle()
		if clock.LastArmed() != 3000 {
			t.Fatal(clock.LastArmed())
		}
		s.Stop()
		if clock.Pending() != 0 {
			t.Fatal(clock.Pending())
		}
	})
	t.Run("retries after 1 s when something due is left pending", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		clock := tu.NewFakeClock(NOW)
		s := newSched(d, clock.Now, clock, reminders.SchedulerOptions{Extensions: []reminders.Extension{stuckExt{NOW - 1}}})
		create(t, d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-11T10:00", TZ: WITA}, NOW)
		tu.Must(t, s.Start())
		if clock.LastArmed() != 1000 {
			t.Fatal(clock.LastArmed())
		}
		s.Stop()
	})
}

// stuckExt always reports something overdue and never claims it.
type stuckExt struct{ due int64 }

func (e stuckExt) Sweep(int64) (int, error)      { return 0, errors.New("broken") }
func (e stuckExt) NextDue() (int64, bool, error) { return e.due, true, nil }

func TestSmartReminders(t *testing.T) {
	t.Run("calls OnSmartReminder instead of enqueueing text", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "cek cuaca", LocalStart: "2026-11-10T10:00", TZ: WITA, SmartPrompt: tu.Ptr("Cek cuaca Banjarbaru")}, NOW)
		var mu sync.Mutex
		var got []db.Reminder
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{OnSmartReminder: func(r db.Reminder) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, r)
			return nil
		}})
		tu.Must(t, s.Start())
		s.Idle()
		if len(got) != 1 || got[0].ID != r.ID || *got[0].SmartPrompt != "Cek cuaca Banjarbaru" {
			t.Fatalf("%+v", got)
		}
		if len(outboxRows(t, d)) != 0 || getRem(t, d, r.ID).Status != "done" {
			t.Fatal("state")
		}
		s.Stop()
	})
	t.Run("falls back to the plain text when the smart run fails", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "cek cuaca", LocalStart: "2026-11-10T10:00", TZ: WITA, SmartPrompt: tu.Ptr("x")}, NOW)
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{OnSmartReminder: func(db.Reminder) error { return errors.New("model off") }})
		tu.Must(t, s.Start())
		s.Idle()
		if outboxRows(t, d)[0].Payload.Text != "⏰ cek cuaca" || getRem(t, d, r.ID).Status != "done" {
			t.Fatal("no fallback")
		}
		s.Stop()
	})
	t.Run("a panicking smart run falls back too", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		create(t, d, reminders.CreateInput{ChatID: chatID, Text: "cek", LocalStart: "2026-11-10T10:00", TZ: WITA, SmartPrompt: tu.Ptr("x")}, NOW)
		s := newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{OnSmartReminder: func(db.Reminder) error { panic("boom") }})
		tu.Must(t, s.Start())
		s.Idle()
		if len(outboxRows(t, d)) != 1 {
			t.Fatal("no fallback")
		}
		s.Stop()
	})
}

func TestRealTimersFire(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	timers := reminders.RealTimers{}
	h := timers.SetTimeout(wg.Done, 1)
	wg.Wait()
	timers.ClearTimeout(h)
	stopped := timers.SetTimeout(func() { t.Error("cleared timer fired") }, 60_000)
	timers.ClearTimeout(stopped)
	i := timers.SetInterval(func() {}, 60_000)
	timers.ClearInterval(i)
	timers.ClearInterval(i)
}
