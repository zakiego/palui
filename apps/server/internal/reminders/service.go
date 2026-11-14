package reminders

// Reminder CRUD (reminders/service.ts, PLAN 6). Every write recomputes fire_at
// in wall-clock time (time.go) and calls NotifyScheduleChanged (signal.go) so a
// reminder due in a few seconds doesn't wait for the next sweep.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// ReminderError carries a message meant for the owner or the model.
type ReminderError struct{ Msg string }

func (e *ReminderError) Error() string { return e.Msg }

func remErr(format string, a ...any) error { return &ReminderError{fmt.Sprintf(format, a...)} }

const MaxReminderText = 500

// wrap turns a TimeError into a ReminderError with the same message.
func wrap(err error) error {
	var te *TimeError
	if errors.As(err, &te) {
		return &ReminderError{te.Msg}
	}
	return err
}

func cleanText(text string) (string, error) {
	t := strx.Trim(text)
	if t == "" {
		return "", remErr("The reminder text is empty.")
	}
	if strx.Len16(t) > MaxReminderText {
		return "", remErr("The reminder text is longer than %d characters.", MaxReminderText)
	}
	return t, nil
}

func scheduleOf(r db.Reminder) Schedule {
	return Schedule{RRule: r.RRule, TZ: r.TZ, LocalStart: r.LocalStart}
}

func nullIfEmpty(s *string) *string {
	if s == nil {
		return nil
	}
	t := strx.Trim(*s)
	if t == "" {
		return nil
	}
	return &t
}

// ReminderEcho is "Besok, Rabu 11 Nov, 09.00 WITA" for one-offs, "Tiap Senin,
// 08.00 WITA" for series.
func ReminderEcho(r db.Reminder, now int64) (string, error) {
	return FormatEcho(scheduleOf(r), r.FireAt, now)
}

// ReminderNext is the next fire as a date ("Senin 16 Nov, 08.00 WITA").
func ReminderNext(r db.Reminder, now int64) string { return FormatWhen(r.FireAt, r.TZ, now) }

// --- CRUD ----------------------------------------------------------------------------

type CreateInput struct {
	ChatID     string
	Text       string
	LocalStart string
	TZ         string
	// "" is a one-off.
	RRule string
	// nil or blank is a plain reminder.
	SmartPrompt *string
}

func Create(q db.Q, in CreateInput, now int64) (db.Reminder, error) {
	row, err := create(q, in, now)
	if err != nil {
		return db.Reminder{}, wrap(err)
	}
	NotifyScheduleChanged()
	return row, nil
}

func create(q db.Q, in CreateInput, now int64) (db.Reminder, error) {
	if _, ok, err := chats.Get(q, in.ChatID); err != nil {
		return db.Reminder{}, err
	} else if !ok {
		return db.Reminder{}, remErr("No chat %s.", in.ChatID)
	}
	if err := AssertZone(in.TZ); err != nil {
		return db.Reminder{}, err
	}
	// Same rules as the parser: a one-off earlier today rolls to tomorrow,
	// far-off dates are refused (ResolveFirstFire).
	var rr *string
	if in.RRule != "" {
		n, err := NormalizeRrule(in.RRule)
		if err != nil {
			return db.Reminder{}, err
		}
		rr = &n
	}
	local, err := NormalizeLocal(in.LocalStart)
	if err != nil {
		return db.Reminder{}, err
	}
	first, err := ResolveFirstFire(Schedule{RRule: rr, TZ: in.TZ, LocalStart: local}, now)
	if err != nil {
		return db.Reminder{}, err
	}
	text, err := cleanText(in.Text)
	if err != nil {
		return db.Reminder{}, err
	}
	s := first.Schedule
	return db.ScanReminder(q.WQueryRow("INSERT INTO reminders (id, chat_id, text, rrule, tz, local_start, fire_at, scheduled_for, status, smart_prompt, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?) RETURNING "+db.ReminderCols,
		secret.NewID("rem"), in.ChatID, text, s.RRule, s.TZ, s.LocalStart, first.FireAt, first.FireAt, nullIfEmpty(in.SmartPrompt), now, now))
}

func Get(q db.Q, id string) (db.Reminder, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ReminderCols+" FROM reminders WHERE id = ?", id), db.ScanReminder)
}

func mustGet(q db.Q, id string) (db.Reminder, error) {
	r, ok, err := Get(q, id)
	if err != nil {
		return r, err
	}
	if !ok {
		return r, remErr("No reminder %s.", id)
	}
	return r, nil
}

// Filter is upcoming, recurring or done.
type Filter string

const (
	FilterUpcoming  Filter = "upcoming"
	FilterRecurring Filter = "recurring"
	FilterDone      Filter = "done"
)

type ListOptions struct {
	Filter Filter
	ChatID string
	// 0 means 50; clamped to 1..500.
	Limit int
}

// List: upcoming is pending/firing by next fire, recurring the live series by
// next fire, done the finished and cancelled ones, most recently changed first.
func List(q db.Q, o ListOptions) ([]db.Reminder, error) {
	limit := o.Limit
	if limit == 0 {
		limit = 50
	}
	limit = min(max(limit, 1), 500)
	var where []string
	var args []any
	if o.ChatID != "" {
		where = append(where, "chat_id = ?")
		args = append(args, o.ChatID)
	}
	order := "fire_at ASC"
	if o.Filter == FilterDone {
		where = append(where, "status IN ('done', 'cancelled')")
		order = "updated_at DESC"
	} else {
		where = append(where, "status IN ('pending', 'firing')")
		if o.Filter == FilterRecurring {
			where = append(where, "rrule IS NOT NULL")
		}
	}
	args = append(args, limit)
	rows, qerr := q.Query("SELECT "+db.ReminderCols+" FROM reminders WHERE "+strings.Join(where, " AND ")+" ORDER BY "+order+" LIMIT ?", args...)
	return db.All(rows, qerr, db.ScanReminder)
}

// Next is nextReminders: the next n pending reminders. Overdue ones are about
// to fire and stay listed first.
func Next(q db.Q, n int, now int64) ([]db.Reminder, error) {
	_ = now
	rows, qerr := q.Query("SELECT "+db.ReminderCols+" FROM reminders WHERE status = 'pending' ORDER BY fire_at ASC LIMIT ?", n)
	return db.All(rows, qerr, db.ScanReminder)
}

// Patch: nil leaves a field alone. RRule and SmartPrompt pointing at "" clear
// it (TS null).
type Patch struct {
	Text        *string
	LocalStart  *string
	TZ          *string
	RRule       *string
	SmartPrompt *string
}

// Update reschedules on a time change: fire_at is recomputed from the new
// schedule and a finished one-off goes back to pending. Cancelled reminders
// can't be edited.
func Update(q db.Q, id string, p Patch, now int64) (db.Reminder, error) {
	row, err := update(q, id, p, now)
	if err != nil {
		return db.Reminder{}, wrap(err)
	}
	NotifyScheduleChanged()
	return row, nil
}

type setList struct {
	cols []string
	args []any
}

func (s *setList) add(col string, v any) {
	s.cols = append(s.cols, col+" = ?")
	s.args = append(s.args, v)
}

func update(q db.Q, id string, p Patch, now int64) (db.Reminder, error) {
	r, err := mustGet(q, id)
	if err != nil {
		return r, err
	}
	if r.Status == "cancelled" {
		return r, remErr("That reminder was cancelled. Create a new one instead.")
	}
	set := &setList{}
	set.add("updated_at", now)
	if p.Text != nil {
		t, err := cleanText(*p.Text)
		if err != nil {
			return r, err
		}
		set.add("text", t)
	}
	if p.SmartPrompt != nil {
		set.add("smart_prompt", nullIfEmpty(p.SmartPrompt))
	}
	if p.LocalStart != nil || p.TZ != nil || p.RRule != nil {
		tz := r.TZ
		if p.TZ != nil {
			tz = *p.TZ
		}
		if err := AssertZone(tz); err != nil {
			return r, err
		}
		local := r.LocalStart
		if p.LocalStart != nil {
			if local, err = NormalizeLocal(*p.LocalStart); err != nil {
				return r, err
			}
		}
		rr := r.RRule
		if p.RRule != nil {
			rr = nil
			if *p.RRule != "" {
				n, err := NormalizeRrule(*p.RRule)
				if err != nil {
					return r, err
				}
				rr = &n
			}
		}
		first, err := ResolveFirstFire(Schedule{RRule: rr, TZ: tz, LocalStart: local}, now)
		if err != nil {
			return r, err
		}
		s := first.Schedule
		set.add("rrule", s.RRule)
		set.add("tz", s.TZ)
		set.add("local_start", s.LocalStart)
		set.add("fire_at", first.FireAt)
		set.add("scheduled_for", first.FireAt)
		set.add("status", "pending")
		set.add("snoozed_until", nil)
	}
	return db.ScanReminder(q.WQueryRow("UPDATE reminders SET "+strings.Join(set.cols, ", ")+" WHERE id = ? RETURNING "+db.ReminderCols, append(set.args, id)...))
}

func Cancel(q db.Q, id string, now int64) (db.Reminder, error) {
	if _, err := mustGet(q, id); err != nil {
		return db.Reminder{}, err
	}
	row, err := db.ScanReminder(q.WQueryRow("UPDATE reminders SET status = 'cancelled', updated_at = ? WHERE id = ? RETURNING "+db.ReminderCols, now, id))
	if err != nil {
		return row, err
	}
	NotifyScheduleChanged()
	return row, nil
}

// Snooze is a SnoozePreset: "10m", "1h", "tomorrow", or Preset "" with Until.
type Snooze struct {
	Preset string
	Until  int64
}

// SnoozeUntil is snoozeUntil.
func SnoozeUntil(p Snooze, tz string, now int64) int64 {
	switch p.Preset {
	case "":
		return p.Until
	case "10m":
		return now + 10*60_000
	case "1h":
		return now + 3600_000
	}
	return TomorrowAt(now, tz, 9, 0)
}

// SnoozeReminder moves the next fire to the snooze time. A done one-off comes
// back as pending. A series keeps its rule: after the snoozed fire, the next
// occurrence follows the rule again.
func SnoozeReminder(q db.Q, id string, p Snooze, now int64) (db.Reminder, error) {
	r, err := mustGet(q, id)
	if err != nil {
		return r, err
	}
	if r.Status == "cancelled" {
		return r, remErr("That reminder was cancelled.")
	}
	until := SnoozeUntil(p, r.TZ, now)
	if until <= now {
		return r, remErr("The snooze time must be in the future.")
	}
	row, err := db.ScanReminder(q.WQueryRow("UPDATE reminders SET fire_at = ?, snoozed_until = ?, status = 'pending', updated_at = ? WHERE id = ? RETURNING "+db.ReminderCols, until, until, now, id))
	if err != nil {
		return row, err
	}
	NotifyScheduleChanged()
	return row, nil
}

// MarkDone: a one-off is finished. A series just acknowledges this occurrence
// and keeps going; if it was snoozed, the snooze is dropped and the rule's next
// occurrence comes back.
func MarkDone(q db.Q, id string, now int64) (db.Reminder, error) {
	r, err := mustGet(q, id)
	if err != nil {
		return r, err
	}
	if r.Status == "cancelled" {
		return r, remErr("That reminder was cancelled.")
	}
	var row db.Reminder
	switch {
	case r.RRule == nil:
		row, err = db.ScanReminder(q.WQueryRow("UPDATE reminders SET status = 'done', snoozed_until = NULL, updated_at = ? WHERE id = ? RETURNING "+db.ReminderCols, now, id))
	case r.SnoozedUntil != nil && r.FireAt == *r.SnoozedUntil:
		next, ok, nerr := NextOccurrence(scheduleOf(r), now)
		if nerr != nil {
			return r, wrap(nerr)
		}
		if !ok {
			row, err = db.ScanReminder(q.WQueryRow("UPDATE reminders SET status = 'done', snoozed_until = NULL, updated_at = ? WHERE id = ? RETURNING "+db.ReminderCols, now, id))
		} else {
			row, err = db.ScanReminder(q.WQueryRow("UPDATE reminders SET fire_at = ?, scheduled_for = ?, snoozed_until = NULL, status = 'pending', updated_at = ? WHERE id = ? RETURNING "+db.ReminderCols, next, next, now, id))
		}
	default:
		row, err = db.ScanReminder(q.WQueryRow("UPDATE reminders SET updated_at = ? WHERE id = ? RETURNING "+db.ReminderCols, now, id))
	}
	if err != nil {
		return row, err
	}
	NotifyScheduleChanged()
	return row, nil
}
