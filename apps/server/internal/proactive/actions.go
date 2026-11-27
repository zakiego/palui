package proactive

// Owner actions on a proactive message (RFC 10), handled by the router
// before the model:
//   FromCallback  Telegram buttons: "ndg:done:<id>", "ndg:snooze:<id>:<1h|3h|tomorrow>", "ndg:mute:<id>"
//   FromText      WhatsApp replies quoting a proactive message: "done",
//                 "tunda 3j", "jangan ingetin"... Any other reply becomes an
//                 owner note in proactive memory (RFC 8) and the turn goes on
//                 as usual, so the agent still answers it.
// An action applies to every nudge the message carried. Actions implements
// chat.ReminderActions.

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var callbackRe = regexp.MustCompile(`^ndg:(done|snooze|mute):([A-Za-z0-9_-]+)(?::(1h|3h|tomorrow))?$`)

var (
	muteRe    = regexp.MustCompile(`^(jangan (di)?ingetin( lagi)?|ga(k|usah)? usah (di)?ingetin|nggak usah (di)?ingetin|stop|mute|skip|abaikan|biarin)$`)
	snooze3hR = regexp.MustCompile(`^(tunda|snooze|nanti|ingetin lagi)( (dalam|lagi))? ?(3 ?(j|jam|h|hr|hours?)|tiga jam)( lagi)?$`)
	punct     = regexp.MustCompile(`[!.,?]+$`)
)

// Action is a parsed owner action.
type Action struct {
	Kind   string // done, snooze, mute
	Preset string // 1h, 3h, tomorrow
}

// ParseText reads a WhatsApp reply; nil when it isn't an action.
func ParseText(text string) *Action {
	t := strx.Trim(strx.CollapseSpace(punct.ReplaceAllString(strings.ToLower(text), "")))
	if t == "" {
		return nil
	}
	if muteRe.MatchString(t) {
		return &Action{Kind: "mute"}
	}
	if snooze3hR.MatchString(t) {
		return &Action{Kind: "snooze", Preset: "3h"}
	}
	if a := reminders.ParseActionText(t); a != nil {
		if a.Kind == "done" {
			return &Action{Kind: "done"}
		}
		preset := a.Preset
		if preset == "10m" {
			preset = "1h" // a nudge's shortest snooze
		}
		return &Action{Kind: "snooze", Preset: preset}
	}
	return nil
}

// Actions is the router's proactive actions.
type Actions struct{}

func (Actions) FromCallback(q db.Q, data, chatID string, now int64) (string, bool, error) {
	m := callbackRe.FindStringSubmatch(data)
	if m == nil {
		return "", false, nil
	}
	a := Action{Kind: m[1], Preset: m[3]}
	if a.Kind == "snooze" && a.Preset == "" {
		return "", false, nil
	}
	n, ok, err := GetNudge(q, m[2])
	if err != nil {
		return "", false, err
	}
	if !ok || n.OutboxID == nil {
		return "Pesan itu udah nggak ada.", true, nil
	}
	reply, err := Apply(q, *n.OutboxID, chatID, a, now)
	return reply, err == nil, err
}

func (Actions) FromText(q db.Q, text, replyTo, chatID string, now int64) (string, bool, error) {
	if replyTo == "" {
		return "", false, nil
	}
	var outboxID string
	err := q.QueryRow("SELECT id FROM outbox WHERE channel_msg_id = ? AND source LIKE ? ORDER BY (chat_id = ?) DESC, rowid LIMIT 1", replyTo, OutboxSourcePrefix+"%", chatID).Scan(&outboxID)
	if noRows(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	a := ParseText(text)
	if a == nil {
		return "", false, rememberReply(q, outboxID, text, now)
	}
	reply, err := Apply(q, outboxID, chatID, *a, now)
	return reply, err == nil, err
}

// rememberReply keeps the owner's reply to a proactive message as a reply
// note, tied to what the message said, and looks again on the next sweep.
// The chat turn sees the same reply and saves anything lasting with
// remember_proactive, so the raw reply only bridges the next few runs: it
// expires after ReplyNoteDays and is the first owner-side note to go when
// memory is full.
func rememberReply(q db.Q, outboxID, text string, now int64) error {
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE outbox_id = ? ORDER BY created_at, id LIMIT 1", outboxID)
	list, err := db.All(rows, err, db.ScanNudge)
	if err != nil || len(list) == 0 {
		return err
	}
	n := list[0]
	body := strx.Trim(text)
	if body == "" {
		return nil
	}
	note := body
	if n.Kind == KindItem {
		note = "About \"" + clip(Label(n), 80) + "\": " + body
	}
	if _, err := AddNote(q, n.AgentID, note, NoteSourceReply, 0, now); err != nil {
		var ce *ConfigError
		if errors.As(err, &ce) {
			return nil // memory full: the turn still answers
		}
		return err
	}
	_, err = q.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ? AND enabled = 1 AND paused_reason IS NULL", now, n.AgentID)
	return err
}

// Apply runs an action on every nudge of one proactive message.
func Apply(q db.Q, outboxID, chatID string, a Action, now int64) (string, error) {
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE outbox_id = ? AND kind NOT IN (?, ?) ORDER BY created_at, id", outboxID, KindSumMorning, KindSumEvening)
	list, err := db.All(rows, err, db.ScanNudge)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "Pesan itu nggak bisa diubah lagi.", nil
	}
	var sentTo string
	if err := q.QueryRow("SELECT chat_id FROM outbox WHERE id = ?", outboxID).Scan(&sentTo); err != nil && !noRows(err) {
		return "", err
	}
	if sentTo != "" && sentTo != chatID {
		return "Pesan itu nggak ketemu di chat ini.", nil
	}
	var reply string
	err = q.Tx(func(tx db.Q) error {
		switch a.Kind {
		case "done":
			for _, n := range list {
				if err := setStatus(tx, n.ID, StatusDone, "owner: done", now); err != nil {
					return err
				}
			}
			reply = "Sip, ditandai selesai."
		case "snooze":
			tz, err := settings.Timezone(tx)
			if err != nil || tz == "" {
				tz = "UTC"
			}
			wake := snoozeUntil(a.Preset, tz, now)
			for _, n := range list {
				if _, err := tx.Exec("UPDATE nudges SET status = ?, wake_at = ?, reason = 'owner: snoozed', updated_at = ? WHERE id = ?", StatusSnoozed, wake, now, n.ID); err != nil {
					return err
				}
			}
			reply = "Oke, aku ingetin lagi " + when(wake, tz, now) + "."
		case "mute":
			var names []string
			for _, n := range list {
				if err := Mute(tx, n.AgentID, n.Subject, Label(n), now); err != nil {
					return err
				}
				names = append(names, Label(n))
			}
			reply = "Oke, " + strings.Join(uniq(names), ", ") + " nggak aku ingetin lagi."
		}
		return nil
	})
	return reply, err
}

// snoozeUntil: 1h, 3h, or tomorrow at 09:00 local.
func snoozeUntil(preset, tz string, now int64) int64 {
	switch preset {
	case "3h":
		return now + 3*3600_000
	case "tomorrow":
		loc := loadTZ(tz)
		t := time.UnixMilli(now).In(loc).AddDate(0, 0, 1)
		return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, loc).UnixMilli()
	}
	return now + 3600_000
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
