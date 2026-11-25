// Package proactive lets an agent tell the owner things nobody asked for
// (docs/rfc/proactive-prompt.md). The owner writes one prompt that mentions
// the read tools it may use (@<connectorId>__<tool>); on a timer the engine
// runs it as a small tool loop, keeps a ledger of what it already said
// (nudges) and short notes between runs (memory), and sends what's new
// through the outbox, within quiet hours and a rate limit.
package proactive

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// ConfigSchema is the config format version. 1 was the fixed calendar and
// WhatsApp sources (docs/rfc/proactive.md); ParseConfig upgrades it.
const ConfigSchema = 2

// MaxPromptChars caps the prompt (UTF-16 units).
const MaxPromptChars = 4000

// MaxMentions caps the tools one prompt may mention.
const MaxMentions = 16

// Config is agent_proactive.config (RFC 9).
type Config struct {
	Schema int `json:"schema"`
	// The owner's instructions, with @<connectorId>__<tool> mentions.
	Prompt        string     `json:"prompt"`
	CheckEveryMin int        `json:"checkEveryMin"`
	QuietHours    QuietHours `json:"quietHours"`
	MaxPerHour    int        `json:"maxPerHour"`
	Morning       Summary    `json:"morning"`
	Evening       Summary    `json:"evening"`
	// "fast" or "default".
	ModelRole string `json:"modelRole"`
}

// QuietHours: no runs and nothing sent from Start to End (local "HH:MM"),
// except urgent timed items when UrgentPasses.
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

// Defaults is a new agent's config: no prompt yet.
func Defaults() Config {
	return Config{
		Schema:        ConfigSchema,
		Prompt:        "",
		CheckEveryMin: 30,
		QuietHours:    QuietHours{Start: "22:00", End: "07:00", UrgentPasses: true},
		MaxPerHour:    4,
		Morning:       Summary{On: true, At: "07:00"},
		Evening:       Summary{On: true, At: "20:00"},
		ModelRole:     "fast",
	}
}

// ConfigError is a refusal the dashboard and the model can show as is.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &ConfigError{fmt.Sprintf(format, a...)} }

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func between(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return bad("%s must be between %d and %d", name, lo, hi)
	}
	return nil
}

// Validate checks bounds and formats. Whether the mentions resolve is checked
// by Save and by every run, which can see the agent's tools.
func (c Config) Validate() error {
	if c.Schema != ConfigSchema {
		return bad("Unknown config schema %d", c.Schema)
	}
	if strx.Len16(c.Prompt) > MaxPromptChars {
		return bad("The prompt can be at most %d characters", MaxPromptChars)
	}
	if n := len(Mentions(c.Prompt)); n > MaxMentions {
		return bad("The prompt can mention at most %d tools, it mentions %d. Drop the ones a run doesn't need", MaxMentions, n)
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
	return nil
}

// configV1 is what schema 1 kept (docs/rfc/proactive.md 4.4), read only to
// carry the timing over and write a starter prompt.
type configV1 struct {
	Calendar *struct {
		DepartLeadMin int  `json:"departLeadMin"`
		OnlineLeadMin int  `json:"onlineLeadMin"`
		NotifyChanges bool `json:"notifyChanges"`
	} `json:"calendar"`
	WhatsApp *struct {
		WaitHours     int  `json:"waitHours"`
		EscalateHours int  `json:"escalateHours"`
		SkipGroups    bool `json:"skipGroups"`
	} `json:"whatsapp"`
	Contacts []struct {
		Label string `json:"label"`
	} `json:"contacts"`
}

// ParseConfig reads a stored config, filling what an older row lacks from the
// defaults so a newer field never reads as zero. A schema 1 config keeps its
// timing, quiet hours, rate, summaries and model, and gets a starter prompt
// built from its old sources (RFC 9).
func ParseConfig(raw []byte) (Config, error) {
	c := Defaults()
	if len(raw) == 0 {
		return c, nil
	}
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Config{}, err
	}
	if head.Schema < ConfigSchema {
		var old configV1
		if err := json.Unmarshal(raw, &old); err != nil {
			return Config{}, err
		}
		// The shared fields kept their names.
		var shared struct {
			CheckEveryMin int         `json:"checkEveryMin"`
			MaxPerHour    int         `json:"maxPerHour"`
			QuietHours    *QuietHours `json:"quietHours"`
			Morning       *Summary    `json:"morning"`
			Evening       *Summary    `json:"evening"`
			ModelRole     string      `json:"modelRole"`
		}
		if err := json.Unmarshal(raw, &shared); err != nil {
			return Config{}, err
		}
		if shared.CheckEveryMin != 0 {
			c.CheckEveryMin = shared.CheckEveryMin
		}
		if shared.MaxPerHour != 0 {
			c.MaxPerHour = shared.MaxPerHour
		}
		if shared.QuietHours != nil {
			c.QuietHours = *shared.QuietHours
		}
		if shared.Morning != nil {
			c.Morning = *shared.Morning
		}
		if shared.Evening != nil {
			c.Evening = *shared.Evening
		}
		if shared.ModelRole != "" {
			c.ModelRole = shared.ModelRole
		}
		c.Prompt = starterFromV1(old)
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// StarterPrompt is the prompt a new agent's editor offers: English, with the
// places for mentions left for the owner to fill (RFC 9a).
const StarterPrompt = `Check my calendar (mention its tool here) for the next 36 hours. If an event has a place to go to, tell me 30 minutes before I have to leave. If it's an online meeting, tell me 10 minutes before it starts, with the link. If an event moves, tell me right away.

Check my chats (mention the tool here). Tell me about direct messages waiting for my reply for more than 2 hours, only when they look like they need an answer. Skip groups, promos, receipts and one-time codes. If I still haven't replied 4 hours after you told me, remind me once more.`

func starterFromV1(old configV1) string {
	var b strings.Builder
	b.WriteString("Proactive used to read fixed sources. Mention the tools to read below, then turn it back on.\n\n")
	if cal := old.Calendar; cal != nil {
		fmt.Fprintf(&b, "Check my calendar (mention its tool here) for the next 36 hours. If an event has a place to go to, tell me %d minutes before I have to leave. If it's an online meeting, tell me %d minutes before it starts, with the link.", cal.DepartLeadMin, cal.OnlineLeadMin)
		if cal.NotifyChanges {
			b.WriteString(" If an event moves, tell me right away.")
		}
		b.WriteString("\n\n")
	}
	if wa := old.WhatsApp; wa != nil {
		fmt.Fprintf(&b, "Check my chats (mention the tool here). Tell me about direct messages waiting for my reply for more than %d hours, only when they look like they need an answer.", wa.WaitHours)
		if wa.SkipGroups {
			b.WriteString(" Skip groups.")
		}
		b.WriteString(" Skip promos, receipts and one-time codes.")
		if wa.EscalateHours > 0 {
			fmt.Fprintf(&b, " If I still haven't replied %d hours after you told me, remind me once more.", wa.EscalateHours)
		}
		var names []string
		for _, ct := range old.Contacts {
			if l := strings.TrimSpace(ct.Label); l != "" && len(names) < 20 {
				names = append(names, l)
			}
		}
		if len(names) > 0 {
			b.WriteString(" Always tell me about: " + strings.Join(names, ", ") + ".")
		}
		b.WriteString("\n\n")
	}
	if old.Calendar == nil && old.WhatsApp == nil {
		return StarterPrompt
	}
	out := strings.TrimSpace(b.String())
	if strx.Len16(out) > MaxPromptChars {
		out = strx.Slice16(out, MaxPromptChars)
	}
	return out
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
	// Models often send the object as a JSON string ("{\"prompt\": ...}").
	// A string can't be a config, so read the object inside it.
	if s, ok := p.(string); ok {
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			return Config{}, bad(`The change must be a JSON object, like {"prompt": "..."}`)
		}
	}
	if _, ok := p.(map[string]any); !ok {
		return Config{}, bad(`The change must be a JSON object, like {"prompt": "..."}`)
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
	out.Prompt = strings.TrimSpace(strings.ReplaceAll(out.Prompt, "\r\n", "\n"))
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
