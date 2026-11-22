package outbound

// outbound/service.ts: contacts, limits and the kill switch, the signature,
// sending (now, held for the undo window, or scheduled), cancelling, notices
// to the owner and the hook the outbox worker calls when a send fails.

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// OutboundError carries a message meant for the owner or the model.
type OutboundError struct{ Msg string }

func (e *OutboundError) Error() string { return e.Msg }

func obErr(format string, a ...any) error { return &OutboundError{fmt.Sprintf(format, a...)} }

const (
	MaxOutboundText = 2000
	MaxContactName  = 60
	// A "now" send that couldn't go out within this is dropped (a stale "otw" is worse than none).
	OutboundTTLMs = int64(60 * 60_000)
	// A scheduled send this late (downtime) is skipped and the owner is told.
	OutboundLateSkipMs = int64(30 * 60_000)
	DayMs              = int64(24 * 3600_000)
)

func SourceOf(outboundID string) string       { return "outbound:" + outboundID }
func NoticeSourceOf(outboundID string) string { return "outbound-notice:" + outboundID }
func CancelAction(outboundID string) string   { return "ob:cancel:" + outboundID }

func Settings(q db.Q) (settings.Outbound, error) { return settings.GetOutbound(q) }

// --- Contacts ------------------------------------------------------------------------

// ContactLabel is "Budi (+62 822-0000-1001)" or "Budi (Telegram)".
func ContactLabel(name, channel, address string) string {
	if channel == "whatsapp" {
		return name + " (" + FormatPhone(address) + ")"
	}
	return name + " (Telegram)"
}

func label(c db.Contact) string { return ContactLabel(c.Name, c.Channel, c.Address) }

func GetContact(q db.Q, id string) (db.Contact, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ContactCols+" FROM contacts WHERE id = ?", id), db.ScanContact)
}

// ListContacts is an agent's contacts (PLAN 19). agentID nil lists every
// agent's (the dashboard).
func ListContacts(q db.Q, agentID *string) ([]db.Contact, error) {
	where, args := "removed_at IS NULL", []any{}
	if agentID != nil {
		where += " AND agent_id = ?"
		args = append(args, *agentID)
	}
	rows, err := q.Query("SELECT "+db.ContactCols+" FROM contacts WHERE "+where+" ORDER BY name ASC", args...)
	return db.All(rows, err, db.ScanContact)
}

func sameName(a, b string) bool {
	return strings.ToLower(strx.Trim(a)) == strings.ToLower(strx.Trim(b))
}

// FindContact is one of the agent's saved contacts by id, name
// (case-insensitive) or WhatsApp number.
func FindContact(q db.Q, agentID, ref string) (db.Contact, bool, error) {
	r := strx.Trim(ref)
	if r == "" {
		return db.Contact{}, false, nil
	}
	all, err := ListContacts(q, &agentID)
	if err != nil {
		return db.Contact{}, false, err
	}
	for _, c := range all {
		if c.ID == r {
			return c, true, nil
		}
	}
	for _, c := range all {
		if sameName(c.Name, r) {
			return c, true, nil
		}
	}
	if phone := NormalizePhone(r); phone != "" {
		for _, c := range all {
			if c.Channel == "whatsapp" && c.Address == phone {
				return c, true, nil
			}
		}
	}
	return db.Contact{}, false, nil
}

func cleanName(name string) (string, error) {
	n := strx.Trim(strx.CollapseSpace(name))
	if n == "" {
		return "", obErr("A contact needs a name.")
	}
	if strx.Len16(n) > MaxContactName {
		return "", obErr("Keep the name under %d characters.", MaxContactName)
	}
	return n, nil
}

func policyWords(p string) string {
	if p == "allow" {
		return "always allow"
	}
	return "always ask"
}

type AddContactInput struct {
	AgentID string
	Name    string
	Phone   string
	ChatID  string
	// ask (default) or allow.
	Policy string
	Note   *string
}

func trimOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strx.Trim(*s)
	if t == "" {
		return nil
	}
	return &t
}

// AddContact adds a contact from a WhatsApp number, or from an existing
// Telegram/WhatsApp DM.
func AddContact(q db.Q, in AddContactInput, now int64) (db.Contact, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return db.Contact{}, err
	}
	agent := in.AgentID
	active, err := ListContacts(q, &agent)
	if err != nil {
		return db.Contact{}, err
	}
	for _, c := range active {
		if sameName(c.Name, name) {
			return db.Contact{}, obErr("There's already a contact named %s.", name)
		}
	}
	var channel, address string
	var chat db.Chat
	switch {
	case in.Phone != "":
		digits := NormalizePhone(in.Phone)
		if digits == "" {
			return db.Contact{}, obErr("%s isn't a phone number. Use 08xx or +<country code>.", in.Phone)
		}
		channel, address = "whatsapp", digits
		// A new number gets a chat row Palui won't answer (allowed=false); an existing chat keeps its settings.
		found, ok, err := chats.Find(q, "whatsapp", WaJID(digits), "")
		if err != nil {
			return db.Contact{}, err
		}
		if ok {
			chat = found
		} else {
			no := false
			if chat, err = chats.Ensure(q, chats.EnsureInput{Channel: "whatsapp", ExternalID: WaJID(digits), Title: name, Allowed: &no}, now); err != nil {
				return db.Contact{}, err
			}
		}
	case in.ChatID != "":
		found, ok, err := chats.Get(q, in.ChatID)
		if err != nil {
			return db.Contact{}, err
		}
		if !ok {
			return db.Contact{}, obErr("No chat %s.", in.ChatID)
		}
		if found.Channel == "web" {
			return db.Contact{}, obErr("The web chat can't be a contact.")
		}
		if found.Kind != "dm" {
			return db.Contact{}, obErr("Only direct chats can be contacts, not groups.")
		}
		chat, channel = found, found.Channel
		if channel == "whatsapp" {
			address = nonDigit.ReplaceAllString(strings.SplitN(found.ExternalID, "@", 2)[0], "")
		} else {
			address = found.ExternalID
		}
	default:
		return db.Contact{}, obErr("Give a phone number or a chat.")
	}
	for _, c := range active {
		if c.ChatID == chat.ID || (c.Channel == channel && c.Address == address) {
			return db.Contact{}, obErr("That's already saved as %s.", c.Name)
		}
	}
	policy := in.Policy
	if policy == "" {
		policy = "ask"
	}
	row, err := db.ScanContact(q.WQueryRow("INSERT INTO contacts (id, agent_id, name, channel, address, chat_id, policy, note, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.ContactCols,
		secret.NewID("con"), in.AgentID, name, channel, address, chat.ID, policy, trimOrNil(in.Note), now, now))
	if err != nil {
		return row, err
	}
	_, err = events.Log(q, events.Input{Kind: "outbound.contact", Message: fmt.Sprintf("Contact %s added (%s)", label(row), policyWords(row.Policy)), Data: obj("contactId", row.ID)}, now)
	return row, err
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// ContactPatch: nil leaves a field alone; Note pointing at "" clears it.
type ContactPatch struct {
	Name   *string
	Policy *string
	Note   *string
}

func UpdateContact(q db.Q, id string, p ContactPatch, now int64) (db.Contact, error) {
	c, ok, err := GetContact(q, id)
	if err != nil {
		return c, err
	}
	if !ok || c.RemovedAt != nil {
		return c, obErr("No contact %s.", id)
	}
	cols := []string{"updated_at = ?"}
	args := []any{now}
	if p.Name != nil {
		name, err := cleanName(*p.Name)
		if err != nil {
			return c, err
		}
		others, err := ListContacts(q, c.AgentID)
		if err != nil {
			return c, err
		}
		for _, o := range others {
			if o.ID != id && sameName(o.Name, name) {
				return c, obErr("There's already a contact named %s.", name)
			}
		}
		cols, args = append(cols, "name = ?"), append(args, name)
	}
	if p.Policy != nil {
		cols, args = append(cols, "policy = ?"), append(args, *p.Policy)
	}
	if p.Note != nil {
		cols, args = append(cols, "note = ?"), append(args, trimOrNil(p.Note))
	}
	row, err := db.ScanContact(q.WQueryRow("UPDATE contacts SET "+strings.Join(cols, ", ")+" WHERE id = ? RETURNING "+db.ContactCols, append(args, id)...))
	if err != nil {
		return row, err
	}
	if p.Policy != nil && *p.Policy != c.Policy {
		_, err = events.Log(q, events.Input{Kind: "outbound.policy", Level: "warn", Message: fmt.Sprintf("%s is now %s", row.Name, policyWords(row.Policy)), Data: obj("contactId", id)}, now)
	}
	return row, err
}

// RemoveContact stops everything still waiting for the contact.
func RemoveContact(q db.Q, id string, now int64) (int64, error) {
	c, ok, err := GetContact(q, id)
	if err != nil {
		return 0, err
	}
	if !ok || c.RemovedAt != nil {
		return 0, obErr("No contact %s.", id)
	}
	var cancelled int64
	err = q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("UPDATE contacts SET removed_at = ?, updated_at = ? WHERE id = ?", now, now, id); err != nil {
			return err
		}
		rows, err := tx.WQuery("SELECT id FROM outbound_messages WHERE contact_id = ? ORDER BY rowid", id)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var x string
			if err := rows.Scan(&x); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, x)
		}
		rows.Close()
		for _, oid := range ids {
			n, err := cancelOutboundRow(tx, oid, now)
			if err != nil {
				return err
			}
			cancelled += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if _, err := events.Log(q, events.Input{Kind: "outbound.contact", Message: fmt.Sprintf("Contact %s removed; %d waiting message(s) cancelled", label(c), cancelled), Data: obj("contactId", id)}, now); err != nil {
		return cancelled, err
	}
	reminders.NotifyScheduleChanged()
	return cancelled, nil
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// MentionsContact: whether the owner's own words name this contact (its whole
// name or its number).
func MentionsContact(name, channel, address, text string) bool {
	if text == "" {
		return false
	}
	hay := strings.ToLower(text)
	n := strings.ToLower(name)
	if at := strings.Index(hay, n); at != -1 {
		before, after := ' ', ' '
		if at > 0 {
			before, _ = utf8.DecodeLastRuneInString(hay[:at])
		}
		if at+len(n) < len(hay) {
			after, _ = utf8.DecodeRuneInString(hay[at+len(n):])
		}
		if !isWord(before) && !isWord(after) {
			return true
		}
	}
	if channel == "whatsapp" {
		for _, p := range PhonesIn(text) {
			if p == address {
				return true
			}
		}
	}
	return false
}

// --- Limits and the kill switch --------------------------------------------------------

func sentSince(q db.Q, since int64, chatID string) (int64, error) {
	where := "source LIKE 'outbound:%' AND created_at > ? AND status != 'cancelled'"
	args := []any{since}
	if chatID != "" {
		where += " AND chat_id = ?"
		args = append(args, chatID)
	}
	var n int64
	err := q.QueryRow("SELECT count(*) FROM outbox WHERE "+where, args...).Scan(&n)
	return n, err
}

// UsageCounts is usage(): sends in the last 24 hours, in total and to one
// contact (nil without one).
type UsageCounts struct {
	Total   int64  `json:"total"`
	Contact *int64 `json:"contact"`
}

func Usage(q db.Q, now int64, contact *db.Contact) (UsageCounts, error) {
	total, err := sentSince(q, now-DayMs, "")
	if err != nil {
		return UsageCounts{}, err
	}
	u := UsageCounts{Total: total}
	if contact != nil {
		n, err := sentSince(q, now-DayMs, contact.ChatID)
		if err != nil {
			return u, err
		}
		u.Contact = &n
	}
	return u, nil
}

const killSwitchText = "Sending messages to contacts is turned off (kill switch on the Outbound page)."

// AssertCanSend returns an OutboundError when a send must not happen now.
func AssertCanSend(q db.Q, given db.Contact, now int64) error {
	s, err := Settings(q)
	if err != nil {
		return err
	}
	if !s.Enabled {
		return obErr(killSwitchText)
	}
	// Read it again: the caller's copy may be older than a removal.
	contact := given
	if c, ok, err := GetContact(q, given.ID); err != nil {
		return err
	} else if ok {
		contact = c
	}
	if contact.RemovedAt != nil {
		return obErr("%s was removed from contacts.", contact.Name)
	}
	u, err := Usage(q, now, &contact)
	if err != nil {
		return err
	}
	if *u.Contact >= s.PerContactPerDay {
		return obErr("Limit reached: %d messages to %s in the last 24 hours (max %d).", *u.Contact, contact.Name, s.PerContactPerDay)
	}
	if u.Total >= s.PerDay {
		return obErr("Limit reached: %d messages to contacts in the last 24 hours (max %d).", u.Total, s.PerDay)
	}
	return nil
}

// --- Signature -----------------------------------------------------------------------

var markup = regexp.MustCompile(`[_*~]`)

// looseText: case, whitespace and the WhatsApp markup (_ * ~) don't count, so
// a signature the agent typed by hand still matches.
func looseText(s string) string {
	return strings.ToLower(strx.Trim(strx.CollapseSpace(markup.ReplaceAllString(s, ""))))
}

// KnownSignatures is every signature text Palui knows: the current one, the
// owner's earlier ones and the earlier defaults.
func KnownSignatures(s settings.Outbound) []string {
	all := append([]string{s.Signature}, s.PastSignatures...)
	all = append(all, settings.DefaultOutboundSignature)
	all = append(all, settings.LegacyOutboundSignatures...)
	var out []string
	for _, x := range all {
		if strx.Trim(x) != "" {
			out = append(out, x)
		}
	}
	return out
}

// EndsWithSignature: the text already ends with one of these signatures, or
// with a signature's last line (the agent may have left out the dash line).
func EndsWithSignature(text string, signatures []string) bool {
	body := looseText(text)
	for _, sig := range signatures {
		full := looseText(sig)
		if full != "" && strings.HasSuffix(body, full) {
			return true
		}
		lines := strings.Split(strx.Trim(sig), "\n")
		last := looseText(lines[len(lines)-1])
		if strx.Len16(last) >= 20 && strings.HasSuffix(body, last) {
			return true
		}
	}
	return false
}

// WithSignature is the body with the signature after one blank line, unless
// it's off, opted out (noSignature), or already there.
func WithSignature(text string, s settings.Outbound, noSignature bool) string {
	t := strx.Trim(text)
	sig := strx.Trim(s.Signature)
	if !s.SignatureOn || sig == "" || noSignature {
		return t
	}
	if EndsWithSignature(t, KnownSignatures(s)) {
		return t
	}
	return t + "\n\n" + sig
}

// FinalText is the exact text a contact gets.
func FinalText(q db.Q, text string, noSignature bool) (string, error) {
	t := strx.Trim(text)
	if t == "" {
		return "", obErr("The message is empty.")
	}
	s, err := Settings(q)
	if err != nil {
		return "", err
	}
	full := WithSignature(t, s, noSignature)
	if n := strx.Len16(full); n > MaxOutboundText {
		return "", obErr("Keep the message under %d characters, signature included (this one is %d).", MaxOutboundText, n)
	}
	return full, nil
}

// SettingsPatch is what the Outbound page saves (no enabled, no pastSignatures).
type SettingsPatch struct {
	PerContactPerDay int64  `json:"perContactPerDay"`
	PerDay           int64  `json:"perDay"`
	HoldSeconds      int64  `json:"holdSeconds"`
	SignatureOn      bool   `json:"signatureOn"`
	Signature        string `json:"signature"`
}

// SaveSettings saves the limits and the signature. An edited signature text
// is remembered, so messages already carrying it aren't signed twice.
func SaveSettings(q db.Q, p SettingsPatch, now int64) (settings.Outbound, error) {
	cur, err := Settings(q)
	if err != nil {
		return cur, err
	}
	signature := strx.Trim(strx.NormalizeNewlines(p.Signature))
	if p.SignatureOn && signature == "" {
		return cur, obErr("Write the signature, or turn it off.")
	}
	past := cur.PastSignatures
	curSig := strx.Trim(cur.Signature)
	if curSig != "" && curSig != signature {
		next := []string{curSig}
		for _, x := range past {
			if x != curSig && x != signature {
				next = append(next, x)
			}
		}
		if len(next) > 10 {
			next = next[:10]
		}
		past = next
	}
	if past == nil {
		past = []string{}
	}
	next := settings.Outbound{Enabled: cur.Enabled, PerContactPerDay: p.PerContactPerDay, PerDay: p.PerDay, HoldSeconds: p.HoldSeconds, SignatureOn: p.SignatureOn, Signature: signature, PastSignatures: past}
	if err := settings.Set(q, "outbound", next, now); err != nil {
		return cur, err
	}
	if cur.SignatureOn != next.SignatureOn || curSig != signature {
		msg := "Signature for messages to contacts turned off"
		if next.SignatureOn {
			msg = "Signature for messages to contacts changed"
		}
		if _, err := events.Log(q, events.Input{Kind: "outbound.signature", Message: msg}, now); err != nil {
			return next, err
		}
	}
	return next, nil
}

// SetEnabled is the kill switch. Off cancels every send still waiting in the outbox.
func SetEnabled(q db.Q, enabled bool, now int64) (int64, error) {
	cur, err := Settings(q)
	if err != nil {
		return 0, err
	}
	cur.Enabled = enabled
	if err := settings.Set(q, "outbound", cur, now); err != nil {
		return 0, err
	}
	var cancelled int64
	if !enabled {
		if cancelled, err = db.Changes(q.Exec("UPDATE outbox SET status = 'cancelled', error = 'outbound turned off', claimed_at = NULL WHERE source LIKE 'outbound:%' AND status = 'pending'")); err != nil {
			return 0, err
		}
	}
	in := events.Input{Kind: "outbound.switch", Level: "info", Message: "Messages to contacts turned on"}
	if !enabled {
		in.Level, in.Message = "warn", fmt.Sprintf("Messages to contacts turned off; %d waiting message(s) cancelled", cancelled)
	}
	if _, err := events.Log(q, in, now); err != nil {
		return cancelled, err
	}
	reminders.NotifyScheduleChanged()
	return cancelled, nil
}

// --- Sending -------------------------------------------------------------------------

type SendMeta struct {
	// chat or flow.
	Origin    string
	OriginRef *string
	// owner, policy or flow_version.
	ApprovedBy string
	// Where notices about this send go.
	OwnerChatID *string
	// true: no signature on this one (the owner asked for that; TS signature: false).
	NoSignature bool
}

func enqueueTo(q db.Q, contact db.Contact, outboundID, text string, now, notBefore int64) (string, error) {
	chat, ok, err := chats.Get(q, contact.ChatID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", obErr("The chat for %s is gone; add the contact again.", contact.Name)
	}
	expires := notBefore + OutboundTTLMs
	row, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: text, Format: "plain"}, Source: SourceOf(outboundID), NotBefore: &notBefore, ExpiresAt: &expires}, now)
	return row.ID, err
}

func strOf[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

type SendResult struct {
	Row      db.OutboundMessage `json:"row"`
	OutboxID string             `json:"outboxId"`
	SendAt   int64              `json:"sendAt"`
}

// SendNow sends now. holdMs (the undo window) delays the outbox row; 0 sends at once.
func SendNow(q db.Q, contact db.Contact, text string, meta SendMeta, holdMs int64, now int64) (SendResult, error) {
	if err := AssertCanSend(q, contact, now); err != nil {
		return SendResult{}, err
	}
	full, err := FinalText(q, text, meta.NoSignature)
	if err != nil {
		return SendResult{}, err
	}
	sendAt := now + max(0, holdMs)
	tz, err := settings.Timezone(q)
	if err != nil {
		return SendResult{}, err
	}
	var res SendResult
	err = q.Tx(func(tx db.Q) error {
		id := secret.NewID("obm")
		outboxID, err := enqueueTo(tx, contact, id, full, now, sendAt)
		if err != nil {
			return err
		}
		local := time.UnixMilli(sendAt).UTC().Format("2006-01-02T15:04")
		row, err := db.ScanOutboundMessage(tx.WQueryRow("INSERT INTO outbound_messages (id, agent_id, contact_id, text, origin, origin_ref, approved_by, owner_chat_id, rrule, tz, local_start, fire_at, status, last_outbox_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, 'done', ?, ?, ?) RETURNING "+db.OutboundMessageCols,
			id, contact.AgentID, contact.ID, full, meta.Origin, meta.OriginRef, meta.ApprovedBy, meta.OwnerChatID, tz, local, sendAt, outboxID, now, now))
		res = SendResult{Row: row, OutboxID: outboxID, SendAt: sendAt}
		return err
	})
	if err != nil {
		return SendResult{}, err
	}
	outbox.Notify()
	return res, nil
}

type ScheduleInput struct {
	LocalStart string
	TZ         string
	// "" is a one-off.
	RRule string
}

// ScheduleSend sends at a wall-clock time, or on a repeat. Guards run again at
// each fire.
func ScheduleSend(q db.Q, contact db.Contact, text string, when ScheduleInput, meta SendMeta, now int64) (db.OutboundMessage, error) {
	s, err := Settings(q)
	if err != nil {
		return db.OutboundMessage{}, err
	}
	if !s.Enabled {
		return db.OutboundMessage{}, obErr(killSwitchText)
	}
	current := contact
	if c, ok, err := GetContact(q, contact.ID); err != nil {
		return db.OutboundMessage{}, err
	} else if ok {
		current = c
	}
	if current.RemovedAt != nil {
		return db.OutboundMessage{}, obErr("%s was removed from contacts.", contact.Name)
	}
	// Signed now, so the stored text is the one the owner approved; the
	// scheduler sends it as is at every fire.
	full, err := FinalText(q, text, meta.NoSignature)
	if err != nil {
		return db.OutboundMessage{}, err
	}
	first, err := func() (reminders.FirstFire, error) {
		if err := reminders.AssertZone(when.TZ); err != nil {
			return reminders.FirstFire{}, err
		}
		local, err := reminders.NormalizeLocal(when.LocalStart)
		if err != nil {
			return reminders.FirstFire{}, err
		}
		var rr *string
		if when.RRule != "" {
			n, err := reminders.NormalizeRrule(when.RRule)
			if err != nil {
				return reminders.FirstFire{}, err
			}
			rr = &n
		}
		first, err := reminders.ResolveFirstFire(reminders.Schedule{RRule: rr, TZ: when.TZ, LocalStart: local}, now)
		if err != nil {
			return first, err
		}
		// A one-off time already past is refused instead of rolling to
		// tomorrow: "jam 8" said at 21:00 is a mistake worth asking about.
		if first.Rolled {
			return first, obErr("%s today has already passed. Pick a time in the future.", hhmm(local))
		}
		return first, nil
	}()
	if err != nil {
		var te *reminders.TimeError
		if errors.As(err, &te) {
			return db.OutboundMessage{}, &OutboundError{te.Msg}
		}
		return db.OutboundMessage{}, err
	}
	fs := first.Schedule
	row, err := db.ScanOutboundMessage(q.WQueryRow("INSERT INTO outbound_messages (id, agent_id, contact_id, text, origin, origin_ref, approved_by, owner_chat_id, rrule, tz, local_start, fire_at, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'scheduled', ?, ?) RETURNING "+db.OutboundMessageCols,
		secret.NewID("obm"), contact.AgentID, contact.ID, full, meta.Origin, meta.OriginRef, meta.ApprovedBy, meta.OwnerChatID, fs.RRule, fs.TZ, fs.LocalStart, first.FireAt, now, now))
	if err != nil {
		return row, err
	}
	reminders.NotifyScheduleChanged()
	return row, nil
}

// hhmm is localStart.slice(11, 16).
func hhmm(local string) string {
	if len(local) < 16 {
		if len(local) > 11 {
			return local[11:]
		}
		return ""
	}
	return local[11:16]
}

func GetOutbound(q db.Q, id string) (db.OutboundMessage, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.OutboundMessageCols+" FROM outbound_messages WHERE id = ?", id), db.ScanOutboundMessage)
}

// cancelOutboundRow cancels a scheduled request and its sends still waiting
// in the outbox. It returns how many things it stopped.
func cancelOutboundRow(q db.Q, id string, now int64) (int64, error) {
	n, err := db.Changes(q.Exec("UPDATE outbound_messages SET status = 'cancelled', updated_at = ? WHERE id = ? AND status IN ('scheduled', 'firing')", now, id))
	if err != nil {
		return 0, err
	}
	m, err := db.Changes(q.Exec("UPDATE outbox SET status = 'cancelled', error = 'cancelled by the owner', claimed_at = NULL WHERE source = ? AND status = 'pending'", SourceOf(id)))
	return n + m, err
}

type CancelResult struct {
	OK      bool                `json:"ok"`
	Message string              `json:"message"`
	Row     *db.OutboundMessage `json:"row,omitempty"`
}

func outboxStatus(q db.Q, id *string) (string, error) {
	if id == nil {
		return "", nil
	}
	var s string
	err := q.QueryRow("SELECT status FROM outbox WHERE id = ?", *id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return s, err
}

// Cancel is cancelOutbound: "batal" on a send stops it if it hasn't gone out,
// and says what happened.
func Cancel(q db.Q, id string, now int64) (CancelResult, error) {
	row, ok, err := GetOutbound(q, id)
	if err != nil {
		return CancelResult{}, err
	}
	if !ok {
		return CancelResult{OK: false, Message: fmt.Sprintf("Nggak ada pesan %s.", id)}, nil
	}
	who := "kontak"
	if c, ok, err := GetContact(q, row.ContactID); err != nil {
		return CancelResult{}, err
	} else if ok {
		who = c.Name
	}
	n, err := cancelOutboundRow(q, id, now)
	if err != nil {
		return CancelResult{}, err
	}
	reminders.NotifyScheduleChanged()
	if n > 0 {
		if _, err := events.Log(q, events.Input{Kind: "outbound.cancel", Message: fmt.Sprintf("Message to %s cancelled", who), Data: obj("outboundId", id)}, now); err != nil {
			return CancelResult{}, err
		}
		after, _, err := GetOutbound(q, id)
		if err != nil {
			return CancelResult{}, err
		}
		return CancelResult{OK: true, Message: fmt.Sprintf("Oke, pesan ke %s dibatalin.", who), Row: &after}, nil
	}
	st, err := outboxStatus(q, row.LastOutboxID)
	if err != nil {
		return CancelResult{}, err
	}
	if st == "sent" || st == "sending" {
		return CancelResult{OK: false, Message: fmt.Sprintf("Telat, pesan ke %s udah kekirim.", who), Row: &row}, nil
	}
	return CancelResult{OK: false, Message: fmt.Sprintf("Pesan ke %s udah nggak aktif.", who), Row: &row}, nil
}

// NewestHeld is the newest send still in its undo window asked from this
// chat (a bare "batal"); ok false when there is none.
func NewestHeld(q db.Q, ownerChatID string, now int64) (db.OutboundMessage, bool, error) {
	cols := "o." + strings.ReplaceAll(db.OutboundMessageCols, ", ", ", o.")
	return db.One(q.QueryRow("SELECT "+cols+" FROM outbound_messages o INNER JOIN outbox b ON b.id = o.last_outbox_id WHERE o.owner_chat_id = ? AND b.status = 'pending' AND b.next_attempt_at > ? ORDER BY o.created_at DESC LIMIT 1", ownerChatID, now), db.ScanOutboundMessage)
}

// HeldFromNotice is the send a notice was about, from the notice's channel
// message id (a reply quoting it); "" when none.
func HeldFromNotice(q db.Q, chatID, channelMsgID string) (string, error) {
	var src string
	err := q.QueryRow("SELECT source FROM outbox WHERE chat_id = ? AND channel_msg_id = ? AND source LIKE 'outbound-notice:%' LIMIT 1", chatID, channelMsgID).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(src, "outbound-notice:"), nil
}

// --- Notices to the owner --------------------------------------------------------------

// NoticeToOwner enqueues a plain notice to the owner's chat (nothing when the
// chat is unknown). Actions ride along when given.
func NoticeToOwner(q db.Q, chatID *string, text, source string, actions []db.OutboxAction, now int64) error {
	if chatID == nil || *chatID == "" {
		return nil
	}
	chat, ok, err := chats.Get(q, *chatID)
	if err != nil || !ok {
		return err
	}
	if source == "" {
		source = "system"
	}
	if _, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: text, Format: "plain", Actions: actions}, Source: source}, now); err != nil {
		return err
	}
	outbox.Notify()
	return nil
}

// HoldNotice is the "kekirim 15 detik lagi" notice with a cancel button.
func HoldNotice(q db.Q, row db.OutboundMessage, contact db.Contact, sendAt, now int64) error {
	secs := max(0, int64(math.Floor(float64(sendAt-now)/1000+0.5)))
	if secs > 0 {
		text := fmt.Sprintf("Pesan ke %s kekirim %d detik lagi:\n\n%s", label(contact), secs, row.Text)
		return NoticeToOwner(q, row.OwnerChatID, text, NoticeSourceOf(row.ID), []db.OutboxAction{{ID: CancelAction(row.ID), Label: "Batalin"}}, now)
	}
	return NoticeToOwner(q, row.OwnerChatID, fmt.Sprintf("Pesan ke %s dikirim:\n\n%s", label(contact), row.Text), NoticeSourceOf(row.ID), nil, now)
}

// OutboundFailed is the hook the outbox worker calls when it gives up on a
// row (outbox.WorkerOptions.OutboundFailed).
func OutboundFailed(q db.Q, r db.Outbox, now int64) error {
	if !strings.HasPrefix(r.Source, "outbound:") {
		return nil
	}
	row, ok, err := GetOutbound(q, strings.TrimPrefix(r.Source, "outbound:"))
	if err != nil || !ok {
		return err
	}
	who := "kontak"
	if c, ok, err := GetContact(q, row.ContactID); err != nil {
		return err
	} else if ok {
		who = c.Name
	}
	detail := ""
	if r.Error != nil && *r.Error != "" {
		detail = " (" + strx.Slice16(*r.Error, 120) + ")"
	}
	return NoticeToOwner(q, row.OwnerChatID, fmt.Sprintf("Pesan ke %s gagal terkirim%s.", who, detail), "system", nil, now)
}

// --- Listing -------------------------------------------------------------------------

type View struct {
	ID        string  `json:"id"`
	Contact   string  `json:"contact"`
	Text      string  `json:"text"`
	Status    string  `json:"status"`
	Recurring bool    `json:"recurring"`
	RRule     *string `json:"rrule"`
	Next      *string `json:"next"`
	Delivery  *string `json:"delivery"`
}

func ViewOutbound(q db.Q, r db.OutboundMessage, now int64) (View, error) {
	v := View{ID: r.ID, Contact: r.ContactID, Text: r.Text, Status: r.Status, Recurring: r.RRule != nil && *r.RRule != "", RRule: r.RRule}
	if c, ok, err := GetContact(q, r.ContactID); err != nil {
		return v, err
	} else if ok {
		v.Contact = label(c)
	}
	st, err := outboxStatus(q, r.LastOutboxID)
	if err != nil {
		return v, err
	}
	if st != "" {
		v.Delivery = &st
	}
	if r.Status == "scheduled" {
		n := reminders.FormatWhen(r.FireAt, r.TZ, now)
		v.Next = &n
	}
	return v, nil
}

// List is listOutbound: "scheduled" (scheduled or firing, by next fire) or
// "recent" (by last change). limit 0 means 20.
func List(q db.Q, agentID, filter string, limit int) ([]db.OutboundMessage, error) {
	if limit == 0 {
		limit = 20
	}
	var rows *sql.Rows
	var err error
	if filter == "scheduled" {
		rows, err = q.Query("SELECT "+db.OutboundMessageCols+" FROM outbound_messages WHERE agent_id = ? AND status IN ('scheduled', 'firing') ORDER BY fire_at ASC LIMIT ?", agentID, limit)
	} else {
		rows, err = q.Query("SELECT "+db.OutboundMessageCols+" FROM outbound_messages WHERE agent_id = ? ORDER BY updated_at DESC LIMIT ?", agentID, limit)
	}
	return db.All(rows, err, db.ScanOutboundMessage)
}

// Router is what the chat router needs (chat.Outbound): held sends and "batal".
type Router struct{}

func (Router) OwnerChatID(q db.Q, id string) (string, bool, error) {
	row, ok, err := GetOutbound(q, id)
	if err != nil || !ok {
		return "", ok, err
	}
	return strOf(row.OwnerChatID), true, nil
}

func (Router) Cancel(q db.Q, id string, now int64) (string, error) {
	r, err := Cancel(q, id, now)
	return r.Message, err
}

func (Router) HeldFromNotice(q db.Q, chatID, channelMsgID string) (string, error) {
	return HeldFromNotice(q, chatID, channelMsgID)
}

func (Router) NewestHeld(q db.Q, chatID string, now int64) (string, error) {
	row, ok, err := NewestHeld(q, chatID, now)
	if err != nil || !ok {
		return "", err
	}
	return row.ID, nil
}
