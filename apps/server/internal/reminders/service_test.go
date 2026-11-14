package reminders_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

func mustRem(t *testing.T) func(db.Reminder, error) db.Reminder {
	return func(r db.Reminder, err error) db.Reminder {
		t.Helper()
		tu.Must(t, err)
		return r
	}
}

func local(t *testing.T, ms int64, tz string) string { return reminders.UTCToLocal(ms, tz) }

func isRemErr(err error) bool {
	var re *reminders.ReminderError
	return errors.As(err, &re)
}

func echo(t *testing.T, r db.Reminder) string {
	t.Helper()
	s, err := reminders.ReminderEcho(r, NOW)
	tu.Must(t, err)
	return s
}

func ids(rs []db.Reminder) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return strings.Join(out, ",")
}

func TestCreateReminder(t *testing.T) {
	t.Run("computes fire_at in the zone and echoes it", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "bayar listrik", LocalStart: "2026-11-11T09:00", TZ: WITA}, NOW))
		if reminders.UTCToISO(r.FireAt, "utc") != "2026-11-11T01:00:00.000Z" || r.ScheduledFor != r.FireAt || r.LocalStart != "2026-11-11T09:00:00" || r.Status != "pending" {
			t.Fatalf("%+v", r)
		}
		if e := echo(t, r); e != "Besok, Rabu 11 Nov, 09.00 WITA" {
			t.Fatal(e)
		}
	})
	t.Run("normalizes the rrule and starts a series at its first matching occurrence", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "kirim laporan", LocalStart: "2026-11-10T08:00", TZ: WITA, RRule: "BYDAY=MO;FREQ=WEEKLY"}, NOW))
		if *r.RRule != "FREQ=WEEKLY;BYDAY=MO" || local(t, r.FireAt, WITA) != "2026-11-16T08:00:00" || echo(t, r) != "Tiap Senin, 08.00 WITA" {
			t.Fatalf("%+v %s", r, echo(t, r))
		}
	})
	t.Run("rejects a one-off in the past, bad zones, bad rules and empty text", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		in := func(local, tz, rr, text, chat string) error {
			_, err := reminders.Create(d, reminders.CreateInput{ChatID: chat, Text: text, LocalStart: local, TZ: tz, RRule: rr}, NOW)
			return err
		}
		if err := in("2026-11-09T09:00", WITA, "", "x", chatID); err == nil || !strings.Contains(err.Error(), "already passed") {
			t.Fatal(err)
		}
		if err := in("2030-01-01T09:00", WITA, "", "x", chatID); err == nil || !strings.Contains(err.Error(), "2 years") {
			t.Fatal(err)
		}
		for _, err := range []error{
			in("2026-11-11T09:00", "Nowhere/City", "", "x", chatID),
			in("2026-11-11T09:00", WITA, "FREQ=NOPE", "x", chatID),
			in("2026-11-11T09:00", WITA, "", "  ", chatID),
			in("2026-11-11T09:00", WITA, "", "x", "chat_missing"),
			in("2026-11-11T09:00", WITA, "", strings.Repeat("x", 501), chatID),
		} {
			if !isRemErr(err) {
				t.Fatalf("want ReminderError, got %v", err)
			}
		}
	})
	t.Run("rolls a one-off earlier today to tomorrow, like the parser", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "cek email", LocalStart: "2026-11-10T09:00", TZ: WITA}, NOW))
		if r.LocalStart != "2026-11-11T09:00:00" || local(t, r.FireAt, WITA) != "2026-11-11T09:00:00" || echo(t, r) != "Besok, Rabu 11 Nov, 09.00 WITA" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("accepts a one-off due right now", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW))
		if r.FireAt != NOW {
			t.Fatal(r.FireAt)
		}
	})
}

func TestListAndNext(t *testing.T) {
	d, chatID, otherID := tu.Setup(t)
	mk := func(chat, text, local, rr string) db.Reminder {
		return mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chat, Text: text, LocalStart: local, TZ: WITA, RRule: rr}, NOW))
	}
	a := mk(chatID, "a", "2026-11-12T09:00", "")
	b := mk(chatID, "b", "2026-11-11T09:00", "FREQ=DAILY")
	c := mk(otherID, "c", "2026-11-10T11:00", "")
	dd := mk(chatID, "d", "2026-11-13T09:00", "")
	mustRem(t)(reminders.Cancel(d, dd.ID, NOW+1))
	list := func(o reminders.ListOptions) string {
		rs, err := reminders.List(d, o)
		tu.Must(t, err)
		return ids(rs)
	}
	want := func(got string, rs ...db.Reminder) {
		t.Helper()
		if got != ids(rs) {
			t.Fatalf("got %s want %s", got, ids(rs))
		}
	}
	want(list(reminders.ListOptions{Filter: "upcoming", ChatID: chatID}), b, a)
	want(list(reminders.ListOptions{Filter: "upcoming"}), c, b, a)
	want(list(reminders.ListOptions{Filter: "recurring"}), b)
	want(list(reminders.ListOptions{Filter: "done", ChatID: chatID}), dd)
	next, err := reminders.Next(d, 2, NOW)
	tu.Must(t, err)
	want(ids(next), c, b)
}

func TestUpdateReminder(t *testing.T) {
	t.Run("reschedules on a time change and revives a done one-off", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-10T11:00", TZ: WITA}, NOW))
		mustRem(t)(reminders.MarkDone(d, r.ID, NOW))
		u := mustRem(t)(reminders.Update(d, r.ID, reminders.Patch{LocalStart: tu.Ptr("2026-11-12T07:30"), Text: tu.Ptr("b")}, NOW))
		if u.Status != "pending" || u.Text != "b" || local(t, u.FireAt, WITA) != "2026-11-12T07:30:00" {
			t.Fatalf("%+v", u)
		}
		tt := mustRem(t)(reminders.Update(d, r.ID, reminders.Patch{TZ: tu.Ptr("Asia/Jakarta")}, NOW))
		if local(t, tt.FireAt, "Asia/Jakarta") != "2026-11-12T07:30:00" || tt.FireAt-u.FireAt != tu.HOUR {
			t.Fatalf("%+v", tt)
		}
	})
	t.Run("leaves the schedule alone on a text-only edit and refuses cancelled ones", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-11T09:00", TZ: WITA}, NOW))
		if u := mustRem(t)(reminders.Update(d, r.ID, reminders.Patch{Text: tu.Ptr("b")}, NOW)); u.FireAt != r.FireAt {
			t.Fatal(u)
		}
		mustRem(t)(reminders.Cancel(d, r.ID, NOW))
		if _, err := reminders.Update(d, r.ID, reminders.Patch{Text: tu.Ptr("c")}, NOW); !isRemErr(err) {
			t.Fatal(err)
		}
	})
	t.Run("clears a rule and a smart prompt", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-11T09:00", TZ: WITA, RRule: "FREQ=DAILY", SmartPrompt: tu.Ptr(" cek ")}, NOW))
		if *r.SmartPrompt != "cek" {
			t.Fatal(*r.SmartPrompt)
		}
		u := mustRem(t)(reminders.Update(d, r.ID, reminders.Patch{RRule: tu.Ptr(""), SmartPrompt: tu.Ptr("  ")}, NOW))
		if u.RRule != nil || u.SmartPrompt != nil {
			t.Fatalf("%+v", u)
		}
	})
}

func TestSnoozeReminder(t *testing.T) {
	t.Run("supports 10m, 1h and an explicit time", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW))
		if s := mustRem(t)(reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Preset: "10m"}, NOW)); s.FireAt != NOW+10*tu.MIN {
			t.Fatal(s.FireAt)
		}
		if s := mustRem(t)(reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Preset: "1h"}, NOW)); s.FireAt != NOW+tu.HOUR {
			t.Fatal(s.FireAt)
		}
		if s := mustRem(t)(reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Until: NOW + 5*tu.MIN}, NOW)); s.FireAt != NOW+5*tu.MIN {
			t.Fatal(s.FireAt)
		}
		if _, err := reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Until: NOW - 1}, NOW); !isRemErr(err) {
			t.Fatal(err)
		}
	})
	t.Run("snoozes to tomorrow 09:00 in the reminder's own zone", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-10T22:00", TZ: "America/New_York"}, NOW))
		// NOW is Monday 21:00 in New York, so "tomorrow" is Tuesday there.
		if s := mustRem(t)(reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Preset: "tomorrow"}, NOW)); local(t, s.FireAt, "America/New_York") != "2026-11-10T09:00:00" {
			t.Fatal(local(t, s.FireAt, "America/New_York"))
		}
		w := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "b", LocalStart: "2026-11-10T22:00", TZ: WITA}, NOW))
		if s := mustRem(t)(reminders.SnoozeReminder(d, w.ID, reminders.Snooze{Preset: "tomorrow"}, NOW)); local(t, s.FireAt, WITA) != "2026-11-11T09:00:00" {
			t.Fatal(local(t, s.FireAt, WITA))
		}
	})
	t.Run("revives a done one-off and keeps a series' rule", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-10T11:00", TZ: WITA}, NOW))
		mustRem(t)(reminders.MarkDone(d, r.ID, NOW))
		if s := mustRem(t)(reminders.SnoozeReminder(d, r.ID, reminders.Snooze{Preset: "10m"}, NOW)); s.Status != "pending" {
			t.Fatal(s.Status)
		}
		s := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "s", LocalStart: "2026-11-11T09:00", TZ: WITA, RRule: "FREQ=DAILY"}, NOW))
		sn := mustRem(t)(reminders.SnoozeReminder(d, s.ID, reminders.Snooze{Preset: "1h"}, NOW))
		if *sn.RRule != "FREQ=DAILY" || *sn.SnoozedUntil != NOW+tu.HOUR {
			t.Fatalf("%+v", sn)
		}
	})
}

func TestMarkDone(t *testing.T) {
	t.Run("finishes a one-off", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-11T09:00", TZ: WITA}, NOW))
		if m := mustRem(t)(reminders.MarkDone(d, r.ID, NOW)); m.Status != "done" {
			t.Fatal(m.Status)
		}
	})
	t.Run("only acknowledges a series, and drops a pending snooze", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		s := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "s", LocalStart: "2026-11-11T09:00", TZ: WITA, RRule: "FREQ=DAILY"}, NOW))
		acked := mustRem(t)(reminders.MarkDone(d, s.ID, NOW))
		if acked.Status != "pending" || acked.FireAt != s.FireAt {
			t.Fatalf("%+v", acked)
		}
		mustRem(t)(reminders.SnoozeReminder(d, s.ID, reminders.Snooze{Preset: "10m"}, NOW))
		back := mustRem(t)(reminders.MarkDone(d, s.ID, NOW+tu.MIN))
		if back.SnoozedUntil != nil || local(t, back.FireAt, WITA) != "2026-11-11T09:00:00" {
			t.Fatalf("%+v", back)
		}
		g, _, err := reminders.Get(d, s.ID)
		tu.Must(t, err)
		if g.Status != "pending" {
			t.Fatal(g.Status)
		}
	})
	t.Run("refuses a cancelled reminder and an unknown id", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := mustRem(t)(reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: "a", LocalStart: "2026-11-11T09:00", TZ: WITA}, NOW))
		mustRem(t)(reminders.Cancel(d, r.ID, NOW))
		if _, err := reminders.MarkDone(d, r.ID, NOW); !isRemErr(err) {
			t.Fatal(err)
		}
		if _, err := reminders.MarkDone(d, "rem_missing", NOW); !isRemErr(err) || err.Error() != "No reminder rem_missing." {
			t.Fatal(err)
		}
	})
}
