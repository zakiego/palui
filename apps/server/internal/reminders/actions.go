package reminders

// Reminder actions the router handles before the model (reminders/actions.ts,
// PLAN 6, 8.2):
//   FromCallback  Telegram buttons: "rem:done:<id>", "rem:snooze:<id>:<10m|1h|tomorrow>"
//   FromText      WhatsApp replies that quote a reminder: "done", "tunda 1j"...
// ok true means the input was a reminder action and reply is what to send.
// Actions implements chat.ReminderActions.

import (
	"errors"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var callbackRe = regexp.MustCompile(`^rem:(done|snooze):([A-Za-z0-9_-]+)(?::(10m|1h|tomorrow))?$`)

var doneWords = map[string]bool{
	"done": true, "selesai": true, "udah": true, "udh": true, "sudah": true, "dah": true, "beres": true,
	"ok done": true, "oke done": true, "sip": true, "siap": true, "ok": true, "oke": true, "okay": true,
	"✅": true, "👍": true, "kelar": true,
}

var (
	preset10m      = regexp.MustCompile(`^(10 ?(m|mnt|menit|min|mins|minutes?))$`)
	preset1h       = regexp.MustCompile(`^(1 ?(j|jam|h|hr|hour)|sejam|satu jam|an hour|one hour)$`)
	presetTomorrow = regexp.MustCompile(`^(besok|bsk|besok pagi|tomorrow|tmr|tmrw)( (jam )?(09|9)([.:]00)?)?$`)
	trailingPunct  = regexp.MustCompile(`[!.,?]+$`)
	doneRe         = regexp.MustCompile(`^(udah|sudah|dah|done|selesai|beres|kelar)( (ya|yaa|kok|deh|dong|bro|kak|nih))?$`)
	snoozeRe       = regexp.MustCompile(`^(tunda|snooze|nanti|ingetin lagi|remind me again|later)(?: (?:in|dalam|lagi|sampai|ke))? ?(.*)$`)
	trailingLagi   = regexp.MustCompile(` lagi$`)
)

// parsePreset: text after "tunda"/"snooze" to a preset. Accepts "10m",
// "10 menit", "1j", "1 jam", "1h", "1 hour", "besok", "tomorrow".
func parsePreset(rest string) string {
	r := strx.Trim(strx.CollapseSpace(rest))
	switch {
	case r == "" || preset10m.MatchString(r):
		return "10m"
	case preset1h.MatchString(r):
		return "1h"
	case presetTomorrow.MatchString(r):
		return "tomorrow"
	}
	return ""
}

// TextAction is Kind "done", or "snooze" with Preset.
type TextAction struct {
	Kind   string `json:"kind"`
	Preset string `json:"preset,omitempty"`
}

// ParseActionText is parseActionText; nil when the text isn't an action.
func ParseActionText(text string) *TextAction {
	t := strx.Trim(strx.CollapseSpace(trailingPunct.ReplaceAllString(strings.ToLower(text), "")))
	if t == "" {
		return nil
	}
	if doneWords[t] || doneRe.MatchString(t) {
		return &TextAction{Kind: "done"}
	}
	if m := snoozeRe.FindStringSubmatch(t); m != nil {
		if p := parsePreset(trailingLagi.ReplaceAllString(m[2], "")); p != "" {
			return &TextAction{Kind: "snooze", Preset: p}
		}
	}
	return nil
}

func doneReply(r db.Reminder, now int64) string {
	if r.RRule == nil {
		return "Sip, ditandai selesai."
	}
	return "Sip, ditandai selesai. Berikutnya " + FormatWhen(r.FireAt, r.TZ, now) + "."
}

func snoozeReply(r db.Reminder, now int64) string {
	when := strings.TrimPrefix(FormatWhen(r.FireAt, r.TZ, now), "Hari ini, ")
	return "Oke, ditunda sampai " + when + "."
}

// deliveredTo: the owner may act from the reminder's own chat, or from the
// chat a copy of it was delivered to (the outbox fallback moves the row).
func deliveredTo(q db.Q, id, chatID string) (bool, error) {
	var x string
	err := q.QueryRow("SELECT id FROM outbox WHERE source = ? AND chat_id = ? LIMIT 1", "reminder:"+id, chatID).Scan(&x)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func apply(q db.Q, id string, action TextAction, chatID string, now int64) (string, error) {
	r, ok, err := Get(q, id)
	if err != nil {
		return "", err
	}
	if ok && r.ChatID != chatID {
		if ok, err = deliveredTo(q, id, chatID); err != nil {
			return "", err
		}
	}
	if !ok {
		return "Pengingat itu nggak ketemu di chat ini.", nil
	}
	if r.Status == "cancelled" {
		return "Pengingat itu udah dibatalin.", nil
	}
	var reply string
	if action.Kind == "done" {
		row, err := MarkDone(q, id, now)
		if err == nil {
			reply = doneReply(row, now)
		}
		err = failText(err, &reply)
		return reply, err
	}
	row, err := SnoozeReminder(q, id, Snooze{Preset: action.Preset}, now)
	if err == nil {
		reply = snoozeReply(row, now)
	}
	err = failText(err, &reply)
	return reply, err
}

func failText(err error, reply *string) error {
	var re *ReminderError
	if errors.As(err, &re) {
		*reply = "Gagal: " + re.Msg
		return nil
	}
	return err
}

// Actions is reminderActions.
type Actions struct{}

func (Actions) FromCallback(q db.Q, data, chatID string, now int64) (string, bool, error) {
	m := callbackRe.FindStringSubmatch(data)
	if m == nil {
		return "", false, nil
	}
	verb, id, preset := m[1], m[2], m[3]
	if verb == "snooze" && preset == "" {
		return "", false, nil
	}
	action := TextAction{Kind: "done"}
	if verb == "snooze" {
		action = TextAction{Kind: "snooze", Preset: preset}
	}
	reply, err := apply(q, id, action, chatID, now)
	return reply, err == nil, err
}

func (Actions) FromText(q db.Q, text, replyToChannelMsgID, chatID string, now int64) (string, bool, error) {
	if replyToChannelMsgID == "" {
		return "", false, nil
	}
	action := ParseActionText(text)
	if action == nil {
		return "", false, nil
	}
	rows, err := q.Query("SELECT source, chat_id FROM outbox WHERE channel_msg_id = ? ORDER BY rowid", replyToChannelMsgID)
	if err != nil {
		return "", false, err
	}
	type hitRow struct{ source, chatID string }
	var all []hitRow
	for rows.Next() {
		var h hitRow
		if err := rows.Scan(&h.source, &h.chatID); err != nil {
			rows.Close()
			return "", false, err
		}
		all = append(all, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	// channel_msg_id is unique per channel chat, not globally: prefer this chat's row.
	source := ""
	for _, h := range all {
		if h.chatID == chatID && strings.HasPrefix(h.source, "reminder:") {
			source = h.source
			break
		}
	}
	if source == "" {
		for _, h := range all {
			if strings.HasPrefix(h.source, "reminder:") {
				source = h.source
				break
			}
		}
	}
	if source == "" {
		return "", false, nil
	}
	reply, err := apply(q, strings.TrimPrefix(source, "reminder:"), *action, chatID, now)
	return reply, err == nil, err
}
