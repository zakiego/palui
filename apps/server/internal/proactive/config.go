// Package proactive lets an agent tell the owner things nobody asked for
// (docs/rfc/proactive.md): on a timer it collects from the calendar and the
// owner's WhatsApp, keeps a ledger of what it already said (nudges), and sends
// what's new through the outbox, within quiet hours and a rate limit.
package proactive

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// ConfigSchema is the config format version.
const ConfigSchema = 1

// Config is agent_proactive.config (RFC 4.4).
type Config struct {
	Schema        int        `json:"schema"`
	CheckEveryMin int        `json:"checkEveryMin"`
	MaxPerHour    int        `json:"maxPerHour"`
	QuietHours    QuietHours `json:"quietHours"`
	Morning       Summary    `json:"morning"`
	Evening       Summary    `json:"evening"`
	// nil turns the source off.
	Calendar *CalendarSource `json:"calendar"`
	WhatsApp *WhatsAppSource `json:"whatsapp"`
	// ContactsAuto gives the model the agent's memory profile to judge who
	// matters. Contacts are always told, whatever the model thinks.
	ContactsAuto bool      `json:"contactsAuto"`
	Contacts     []Contact `json:"contacts"`
	// "fast" or "default".
	ModelRole string `json:"modelRole"`
}

// QuietHours: nothing is sent from Start to End (local "HH:MM"), except
// urgent calendar nudges when UrgentPasses.
type QuietHours struct {
	Start        string `json:"start"`
	End          string `json:"end"`
	UrgentPasses bool   `json:"urgentPasses"`
}

// Summary is the morning or evening message.
type Summary struct {
	On bool   `json:"on"`
	At string `json:"at"`
}

// CalendarSource reads a gcal connector, or with Tool an MCP connector's
// Google Calendar style list_events tool (timeMin, timeMax, timeZone).
type CalendarSource struct {
	ConnectorID   string `json:"connectorId"`
	Tool          string `json:"tool,omitempty"`
	DepartLeadMin int    `json:"departLeadMin"`
	OnlineLeadMin int    `json:"onlineLeadMin"`
	NotifyChanges bool   `json:"notifyChanges"`
}

// WhatsAppSource reads wa-hub's list_chats over an MCP connector.
type WhatsAppSource struct {
	ConnectorID   string `json:"connectorId"`
	Account       string `json:"account"`
	WaitHours     int    `json:"waitHours"`
	EscalateHours int    `json:"escalateHours"`
	MaxAgeDays    int    `json:"maxAgeDays"`
	SkipGroups    bool   `json:"skipGroups"`
}

// Contact is someone whose waiting chat is always told.
type Contact struct {
	// "wa:chat:<id>".
	Subject string `json:"subject"`
	Label   string `json:"label"`
}

// Defaults is a new agent's config: every source off until one is picked.
func Defaults() Config {
	return Config{
		Schema:        ConfigSchema,
		CheckEveryMin: 30,
		MaxPerHour:    4,
		QuietHours:    QuietHours{Start: "22:00", End: "07:00", UrgentPasses: true},
		Morning:       Summary{On: true, At: "07:00"},
		Evening:       Summary{On: true, At: "20:00"},
		ContactsAuto:  true,
		Contacts:      []Contact{},
		ModelRole:     "fast",
	}
}

// DefaultCalendar and DefaultWhatsApp are a source's values when it's added.
func DefaultCalendar(connectorID string) *CalendarSource {
	return &CalendarSource{ConnectorID: connectorID, DepartLeadMin: 30, OnlineLeadMin: 10, NotifyChanges: true}
}

func DefaultWhatsApp(connectorID string) *WhatsAppSource {
	return &WhatsAppSource{ConnectorID: connectorID, Account: "personal", WaitHours: 2, EscalateHours: 4, MaxAgeDays: 3, SkipGroups: true}
}

// ConfigError is a refusal the dashboard and the model can show as is.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &ConfigError{fmt.Sprintf(format, a...)} }

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
var subjectRe = regexp.MustCompile(`^wa:chat:[A-Za-z0-9_-]{1,64}$`)
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func between(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return bad("%s must be between %d and %d", name, lo, hi)
	}
	return nil
}

// Validate checks bounds and formats. Connectors are checked by the service,
// which has the database.
func (c Config) Validate() error {
	if c.Schema != ConfigSchema {
		return bad("Unknown config schema %d", c.Schema)
	}
	checks := []error{
		between("Check every", c.CheckEveryMin, 15, 120),
		between("Max messages per hour", c.MaxPerHour, 1, 10),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	for name, v := range map[string]string{"Quiet hours start": c.QuietHours.Start, "Quiet hours end": c.QuietHours.End, "Morning summary time": c.Morning.At, "Evening summary time": c.Evening.At} {
		if !hhmm.MatchString(v) {
			return bad("%s must be a time like 07:00", name)
		}
	}
	if c.ModelRole != "fast" && c.ModelRole != "default" {
		return bad("Model role must be fast or default")
	}
	if cal := c.Calendar; cal != nil {
		if strings.TrimSpace(cal.ConnectorID) == "" {
			return bad("Pick a calendar connector")
		}
		if cal.Tool != "" && !toolNameRe.MatchString(cal.Tool) {
			return bad("Calendar tool must be an MCP tool name")
		}
		if err := between("Leave reminder", cal.DepartLeadMin, 0, 180); err != nil {
			return err
		}
		if err := between("Online meeting reminder", cal.OnlineLeadMin, 0, 60); err != nil {
			return err
		}
	}
	if wa := c.WhatsApp; wa != nil {
		if strings.TrimSpace(wa.ConnectorID) == "" {
			return bad("Pick a WhatsApp connector")
		}
		if strings.TrimSpace(wa.Account) == "" || len(wa.Account) > 32 {
			return bad("WhatsApp account must be 1 to 32 characters")
		}
		if err := between("Unanswered after", wa.WaitHours, 1, 24); err != nil {
			return err
		}
		if err := between("Remind again after", wa.EscalateHours, 0, 24); err != nil {
			return err
		}
		if err := between("Ignore waits older than", wa.MaxAgeDays, 1, 14); err != nil {
			return err
		}
	}
	if len(c.Contacts) > 100 {
		return bad("At most 100 contacts")
	}
	seen := map[string]bool{}
	for _, ct := range c.Contacts {
		if !subjectRe.MatchString(ct.Subject) {
			return bad("Contact %q isn't a WhatsApp chat", ct.Subject)
		}
		if seen[ct.Subject] {
			return bad("Contact %q is listed twice", ct.Label)
		}
		seen[ct.Subject] = true
		if l := strings.TrimSpace(ct.Label); l == "" || len([]rune(l)) > 80 {
			return bad("Contact names must be 1 to 80 characters")
		}
	}
	return nil
}

// ParseConfig reads a stored config, filling what an older row lacks from the
// defaults so a newer field never reads as zero.
func ParseConfig(raw []byte) (Config, error) {
	c := Defaults()
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, err
	}
	if c.Contacts == nil {
		c.Contacts = []Contact{}
	}
	return c, nil
}

// MarshalConfig writes the config with stable key order.
func MarshalConfig(c Config) ([]byte, error) { return jsonx.Marshal(c) }

// MergePatch applies an RFC 7396 merge patch (from the dashboard or the
// update_proactive tool) to the config and validates the result.
func MergePatch(c Config, patch []byte) (Config, error) {
	base, err := json.Marshal(c)
	if err != nil {
		return Config{}, err
	}
	var doc, p any
	if err := json.Unmarshal(base, &doc); err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal(patch, &p); err != nil {
		return Config{}, bad("The change isn't valid JSON")
	}
	merged, err := json.Marshal(mergeValue(doc, p))
	if err != nil {
		return Config{}, err
	}
	out := Defaults()
	dec := json.NewDecoder(strings.NewReader(string(merged)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return Config{}, bad("The change doesn't fit the config: %s", err.Error())
	}
	if out.Contacts == nil {
		out.Contacts = []Contact{}
	}
	if err := out.Validate(); err != nil {
		return Config{}, err
	}
	return out, nil
}

func mergeValue(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	for k, v := range p {
		if v == nil {
			delete(t, k)
			continue
		}
		t[k] = mergeValue(t[k], v)
	}
	return t
}

// minutesOf reads "HH:MM" as minutes after midnight.
func minutesOf(s string) int {
	var h, m int
	_, _ = fmt.Sscanf(s, "%d:%d", &h, &m)
	return h*60 + m
}

// InQuiet reports whether a local minute of the day falls in quiet hours
// (a window may wrap midnight).
func (q QuietHours) InQuiet(minute int) bool {
	s, e := minutesOf(q.Start), minutesOf(q.End)
	if s == e {
		return false
	}
	if s < e {
		return minute >= s && minute < e
	}
	return minute >= s || minute < e
}
