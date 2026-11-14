package reminders_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

var acts = reminders.Actions{}

func oneOff(t *testing.T, q db.Q, chatID string) db.Reminder {
	return create(t, q, reminders.CreateInput{ChatID: chatID, Text: "bayar listrik", LocalStart: "2026-11-10T10:00", TZ: WITA}, NOW)
}

func cb(t *testing.T, q db.Q, data, chatID string) (string, bool) {
	t.Helper()
	r, ok, err := acts.FromCallback(q, data, chatID, NOW)
	tu.Must(t, err)
	return r, ok
}

func txt(t *testing.T, q db.Q, text, quote, chatID string) (string, bool) {
	t.Helper()
	r, ok, err := acts.FromText(q, text, quote, chatID, NOW)
	tu.Must(t, err)
	return r, ok
}

func TestFromCallback(t *testing.T) {
	t.Run("marks done", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := oneOff(t, d, chatID)
		if got, ok := cb(t, d, "rem:done:"+r.ID, chatID); !ok || got != "Sip, ditandai selesai." {
			t.Fatal(got)
		}
		if getRem(t, d, r.ID).Status != "done" {
			t.Fatal("status")
		}
	})
	t.Run("snoozes with each preset", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := oneOff(t, d, chatID)
		if got, _ := cb(t, d, "rem:snooze:"+r.ID+":10m", chatID); got != "Oke, ditunda sampai 10.10 WITA." {
			t.Fatal(got)
		}
		if getRem(t, d, r.ID).FireAt != NOW+10*tu.MIN {
			t.Fatal("fireAt")
		}
		if got, _ := cb(t, d, "rem:snooze:"+r.ID+":1h", chatID); got != "Oke, ditunda sampai 11.00 WITA." {
			t.Fatal(got)
		}
		if got, _ := cb(t, d, "rem:snooze:"+r.ID+":tomorrow", chatID); got != "Oke, ditunda sampai Besok, Rabu 11 Nov, 09.00 WITA." {
			t.Fatal(got)
		}
		if l := reminders.UTCToLocal(getRem(t, d, r.ID).FireAt, WITA); l != "2026-11-11T09:00:00" {
			t.Fatal(l)
		}
	})
	t.Run("says when the next run is after Done on a series", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		r := create(t, d, reminders.CreateInput{ChatID: chatID, Text: "x", LocalStart: "2026-11-16T08:00", TZ: WITA, RRule: "FREQ=WEEKLY;BYDAY=MO"}, NOW)
		if got, _ := cb(t, d, "rem:done:"+r.ID, chatID); got != "Sip, ditandai selesai. Berikutnya Senin 16 Nov, 08.00 WITA." {
			t.Fatal(got)
		}
	})
	t.Run("ignores other callbacks and refuses another chat's reminder", func(t *testing.T) {
		d, chatID, otherID := tu.Setup(t)
		r := oneOff(t, d, chatID)
		if _, ok := cb(t, d, "approve:abc", chatID); ok {
			t.Fatal("took approve")
		}
		if _, ok := cb(t, d, "rem:snooze:"+r.ID+":2d", chatID); ok {
			t.Fatal("took 2d")
		}
		if _, ok := cb(t, d, "rem:snooze:"+r.ID, chatID); ok {
			t.Fatal("took snooze without preset")
		}
		if got, _ := cb(t, d, "rem:done:"+r.ID, otherID); got != "Pengingat itu nggak ketemu di chat ini." {
			t.Fatal(got)
		}
		if getRem(t, d, r.ID).Status != "pending" {
			t.Fatal("status")
		}
		_, err := reminders.Cancel(d, r.ID, NOW)
		tu.Must(t, err)
		if got, _ := cb(t, d, "rem:done:"+r.ID, chatID); got != "Pengingat itu udah dibatalin." {
			t.Fatal(got)
		}
	})
}

// fired is a reminder the scheduler sent and the worker acked with WAMSG1.
func fired(t *testing.T, other bool) (*db.DB, string, db.Reminder) {
	d, chatID, otherID := tu.Setup(t)
	if other {
		chatID = otherID
	}
	r := oneOff(t, d, chatID)
	newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{}).Sweep()
	_, err := d.Exec("UPDATE outbox SET status = 'sent', channel_msg_id = 'WAMSG1' WHERE source = ?", "reminder:"+r.ID)
	tu.Must(t, err)
	return d, chatID, r
}

func TestFromText(t *testing.T) {
	t.Run("handles done words when the reply quotes the reminder", func(t *testing.T) {
		for _, word := range []string{"done", "Selesai", "udah", "udah ya", "beres!"} {
			d, chatID, r := fired(t, true)
			if got, _ := txt(t, d, word, "WAMSG1", chatID); got != "Sip, ditandai selesai." {
				t.Fatalf("%s: %s", word, got)
			}
			if getRem(t, d, r.ID).Status != "done" {
				t.Fatal(word)
			}
		}
	})
	t.Run("handles snooze phrasings", func(t *testing.T) {
		cases := []struct {
			text  string
			delta int64
		}{{"tunda 10m", 10 * tu.MIN}, {"tunda", 10 * tu.MIN}, {"tunda 1j", tu.HOUR}, {"tunda 1 jam", tu.HOUR}, {"snooze 1h", tu.HOUR}, {"Snooze 10 min", 10 * tu.MIN}}
		for _, c := range cases {
			d, chatID, r := fired(t, true)
			if got, _ := txt(t, d, c.text, "WAMSG1", chatID); !strings.HasPrefix(got, "Oke, ditunda sampai") {
				t.Fatalf("%s: %s", c.text, got)
			}
			if getRem(t, d, r.ID).FireAt != NOW+c.delta {
				t.Fatal(c.text)
			}
		}
		d, chatID, r := fired(t, true)
		if got, _ := txt(t, d, "tunda besok", "WAMSG1", chatID); got != "Oke, ditunda sampai Besok, Rabu 11 Nov, 09.00 WITA." {
			t.Fatal(got)
		}
		if l := reminders.UTCToLocal(getRem(t, d, r.ID).FireAt, WITA); l != "2026-11-11T09:00:00" {
			t.Fatal(l)
		}
	})
	t.Run("returns nothing without a quote, for unknown quotes and for other text", func(t *testing.T) {
		d, chatID, _ := fired(t, true)
		for _, c := range [][2]string{{"done", ""}, {"done", "OTHER"}, {"makasih, nanti aku bayar", "WAMSG1"}, {"tunda 3 hari", "WAMSG1"}} {
			if _, ok := txt(t, d, c[0], c[1], chatID); ok {
				t.Fatal(c)
			}
		}
	})
	t.Run("ignores quotes of non-reminder messages", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		row, err := outbox.Enqueue(d, outbox.EnqueueInput{ChatID: chatID, Channel: "telegram", Payload: db.OutboxPayload{Text: "hi"}, Source: "reply:run_1"}, NOW)
		tu.Must(t, err)
		_, err = d.Exec("UPDATE outbox SET channel_msg_id = 'M9' WHERE id = ?", row.ID)
		tu.Must(t, err)
		if _, ok := txt(t, d, "done", "M9", chatID); ok {
			t.Fatal("took it")
		}
	})
	t.Run("refuses a reminder from another chat", func(t *testing.T) {
		d, _, r := fired(t, true)
		before := getRem(t, d, r.ID)
		if got, _ := txt(t, d, "tunda 1j", "WAMSG1", "chat_elsewhere"); got != "Pengingat itu nggak ketemu di chat ini." {
			t.Fatal(got)
		}
		if !reflect.DeepEqual(getRem(t, d, r.ID), before) {
			t.Fatal("changed")
		}
	})
}

func TestActionsOnFallbackDelivered(t *testing.T) {
	t.Run("accepts Done and Snooze from the chat the outbox row was moved to", func(t *testing.T) {
		d, chatID, otherID := tu.Setup(t)
		r := oneOff(t, d, chatID)
		newSched(d, fixed(NOW), tu.NewFakeClock(NOW), reminders.SchedulerOptions{}).Sweep()
		// The outbox worker moved the row to the fallback channel's chat and sent it there.
		_, err := d.Exec("UPDATE outbox SET status = 'sent', chat_id = ?, fallback_from = 'telegram', channel_msg_id = 'WAFB1' WHERE source = ?", otherID, "reminder:"+r.ID)
		tu.Must(t, err)
		if got, _ := txt(t, d, "tunda 1j", "WAFB1", otherID); !strings.HasPrefix(got, "Oke, ditunda sampai") {
			t.Fatal(got)
		}
		if got, _ := cb(t, d, "rem:done:"+r.ID, otherID); got != "Sip, ditandai selesai." {
			t.Fatal(got)
		}
		if getRem(t, d, r.ID).Status != "done" {
			t.Fatal("status")
		}
	})
	t.Run("still refuses a chat that never got the reminder", func(t *testing.T) {
		d, chatID, otherID := tu.Setup(t)
		r := oneOff(t, d, chatID)
		if got, _ := cb(t, d, "rem:done:"+r.ID, otherID); got != "Pengingat itu nggak ketemu di chat ini." {
			t.Fatal(got)
		}
	})
}

func TestParseActionText(t *testing.T) {
	want := map[string]*reminders.TextAction{
		"tunda 1 jam lagi":               {Kind: "snooze", Preset: "1h"},
		"snooze tomorrow":                {Kind: "snooze", Preset: "tomorrow"},
		"tunda sampai besok":             {Kind: "snooze", Preset: "tomorrow"},
		"udah bayar listrik kemarin sih": nil,
		"":                               nil,
	}
	for in, w := range want {
		if got := reminders.ParseActionText(in); !reflect.DeepEqual(got, w) {
			t.Fatalf("%q: %+v", in, got)
		}
	}
}
