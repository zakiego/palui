package proactive

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// MaxMessageChars caps a proactive message (RFC 8).
const MaxMessageChars = 700

// MaxFieldChars caps one source field in a template (RFC 7).
const MaxFieldChars = 200

var (
	ctlAll   = regexp.MustCompile(`[\x00-\x1f\x7f\x{2028}\x{2029}]`)
	ctlKeep  = regexp.MustCompile(`[\x00-\x09\x0b-\x1f\x7f\x{2028}\x{2029}]`)
	manyNL   = regexp.MustCompile(`\n{3,}`)
	manySp   = regexp.MustCompile(`[ \t]{2,}`)
	markdown = regexp.MustCompile("[*_`~]")
)

// Field cleans one untrusted value for a template: one line, plain, capped.
func Field(s string) string {
	s = ctlAll.ReplaceAllString(s, " ")
	s = markdown.ReplaceAllString(s, "")
	s = strings.TrimSpace(manySp.ReplaceAllString(s, " "))
	if strx.Len16(s) > MaxFieldChars {
		return strx.Slice16(s, MaxFieldChars-1) + "…"
	}
	return s
}

// MessageText cleans the model's wording: plain text, line breaks kept,
// capped at MaxMessageChars (it was written after reading untrusted data).
func MessageText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = ctlKeep.ReplaceAllString(s, " ")
	s = manyNL.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if strx.Len16(s) > MaxMessageChars {
		return strx.Slice16(s, MaxMessageChars-1) + "…"
	}
	return s
}

func clock(ms int64, tz string) string { return reminders.FormatClock(ms, tz) }

// when is "jam 14.30", "besok jam 09.00" or "Senin 30 Nov jam 09.00".
func when(ms int64, tz string, now int64) string {
	c := "jam " + clock(ms, tz)
	switch reminders.FormatDate(ms, tz, now) {
	case reminders.FormatDate(now, tz, now):
		return c
	case reminders.FormatDate(now+24*3600_000, tz, now):
		return "besok " + c
	}
	return reminders.FormatDate(ms, tz, now) + " " + c
}

func snapshotOf(n db.Nudge) Snapshot {
	var s Snapshot
	_ = n.Snapshot.Decode(&s)
	return s
}

// Line is a nudge as one plain line, used for timed nudges and whenever the
// model can't word a message (RFC 7, 8).
func Line(n db.Nudge, tz string, now int64) string {
	s := snapshotOf(n)
	title := Field(s.Title)
	if title == "" {
		title = "(tanpa judul)"
	}
	switch n.Kind {
	case KindCalDepart:
		return fmt.Sprintf("🚗 %s %s di %s. Waktunya siap-siap berangkat.", title, when(s.Start, tz, now), Field(s.Location))
	case KindCalStart:
		return fmt.Sprintf("📹 %s mulai %s: %s", title, when(s.Start, tz, now), Field(s.MeetLink))
	case KindCalChanged:
		line := fmt.Sprintf("📅 Jadwal berubah: %s sekarang %s", title, when(s.Start, tz, now))
		if loc := Field(s.Location); loc != "" {
			line += " di " + loc
		}
		if s.PrevStart != 0 && s.PrevStart != s.Start {
			line += " (tadinya " + when(s.PrevStart, tz, now) + ")"
		}
		return line + "."
	case KindWAWaiting:
		name := Field(s.Name)
		if name == "" {
			name = "Seseorang"
		}
		if s.Escalation {
			return fmt.Sprintf("💬 Masih belum kebales: %s, nunggu dari %s.", name, when(s.AwaitingSince, tz, now))
		}
		line := fmt.Sprintf("💬 %s nunggu balasan dari %s", name, when(s.AwaitingSince, tz, now))
		if p := Field(s.Preview); p != "" {
			line += ": “" + p + "”"
		}
		return line
	}
	return title
}

// Label is a short name for a nudge in replies and the mute list.
func Label(n db.Nudge) string {
	s := snapshotOf(n)
	switch {
	case s.Name != "":
		return Field(s.Name)
	case s.Title != "":
		return Field(s.Title)
	}
	return n.Subject
}

// Buttons is the owner's actions on a proactive message (RFC 10). A message
// with only calendar nudges gets mute alone: done or snooze on a "leave now"
// means nothing.
func Buttons(first db.Nudge, hasChat bool) []db.OutboxAction {
	id := first.ID
	if !hasChat {
		return []db.OutboxAction{{ID: "ndg:mute:" + id, Label: "Jangan ingetin"}}
	}
	return []db.OutboxAction{
		{ID: "ndg:done:" + id, Label: "Selesai"},
		{ID: "ndg:snooze:" + id + ":1h", Label: "1j"},
		{ID: "ndg:snooze:" + id + ":3h", Label: "3j"},
		{ID: "ndg:snooze:" + id + ":tomorrow", Label: "Besok"},
		{ID: "ndg:mute:" + id, Label: "Jangan ingetin"},
	}
}
