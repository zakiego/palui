package proactive

// The output contract (RFC 5): what a run's model answers, read leniently
// (a bad item is skipped, not the whole answer) and bounded so a poisoned
// tool result can't flood the ledger.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	// MaxItemChars caps one item's text (UTF-16 units).
	MaxItemChars = 300
	// MaxItems, MaxClosed, MaxRemember and MaxForget cap one answer.
	MaxItems    = 20
	MaxClosed   = 50
	MaxRemember = 10
	MaxForget   = 20
	// SendAtAheadMs is how far ahead an item may be timed.
	SendAtAheadMs = 48 * 3600_000
	// SendAtLateMs: an item timed this far in the past is sent now; older is dropped.
	SendAtLateMs = 10 * 60_000
)

// OutputSchema is the run's answer, for the repair call's ResponseFormat.
const OutputSchema = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"subject":{"type":"string"},"text":{"type":"string"},"sendAt":{"type":"string"},"urgent":{"type":"boolean"}},"required":["key","text"],"additionalProperties":false}},"closed":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"why":{"type":"string"}},"required":["key"],"additionalProperties":false}},"remember":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"days":{"type":"integer"}},"required":["text"],"additionalProperties":false}},"forget":{"type":"array","items":{"type":"string"}},"summary":{"type":"string"}},"required":["items"],"additionalProperties":false}`

// Item is one thing to tell (RFC 5).
type Item struct {
	Key     string
	Subject string
	Text    string
	// Unix ms; 0 sends it now.
	SendAt int64
	Urgent bool
}

// Closed is a key the model says is resolved.
type Closed struct {
	Key string
	Why string
}

// Remember is a note to keep.
type Remember struct {
	Text string
	Days int
}

// Output is a run's parsed answer. Skipped lists what was left out and why,
// for the tick log.
type Output struct {
	Items    []Item
	Closed   []Closed
	Remember []Remember
	Forget   []string
	Summary  string
	Skipped  []string
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,200}$`)

// SubjectOf is a key's default subject: the key up to its second colon
// ("wa:280:1764052320" is "wa:280").
func SubjectOf(key string) string {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 3 {
		return key
	}
	return parts[0] + ":" + parts[1]
}

// ErrNoJSON is the refusal for an answer with no JSON object in it.
var ErrNoJSON = errors.New("the model's answer had no JSON object")

// jsonObjectIn finds the JSON object in a model's text: the whole text, a
// fenced block, or the outermost braces.
func jsonObjectIn(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "```"); i >= 0 {
		rest := t[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			t = strings.TrimSpace(rest[:j])
		}
	}
	start, end := strings.Index(t, "{"), strings.LastIndex(t, "}")
	if start < 0 || end <= start {
		return "", false
	}
	return t[start : end+1], true
}

type rawOutput struct {
	Items []struct {
		Key     string `json:"key"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
		SendAt  string `json:"sendAt"`
		Urgent  bool   `json:"urgent"`
	} `json:"items"`
	Closed []struct {
		Key string `json:"key"`
		Why string `json:"why"`
	} `json:"closed"`
	Remember []struct {
		Text string `json:"text"`
		Days int    `json:"days"`
	} `json:"remember"`
	Forget  []string `json:"forget"`
	Summary string   `json:"summary"`
}

// ParseOutput reads a model's answer at now.
func ParseOutput(text string, now int64) (Output, error) {
	obj, ok := jsonObjectIn(text)
	if !ok {
		return Output{}, ErrNoJSON
	}
	var raw rawOutput
	if err := json.Unmarshal([]byte(obj), &raw); err != nil {
		return Output{}, errors.New("the model's answer wasn't valid JSON")
	}
	var out Output
	seen := map[string]bool{}
	for _, it := range raw.Items {
		key := strings.TrimSpace(it.Key)
		if !keyRe.MatchString(key) {
			out.Skipped = append(out.Skipped, "item with a bad key: "+clip(Field(key), 60))
			continue
		}
		if seen[key] {
			continue
		}
		text := itemText(it.Text)
		if text == "" {
			out.Skipped = append(out.Skipped, key+": empty text")
			continue
		}
		subject := strings.TrimSpace(it.Subject)
		if !keyRe.MatchString(subject) {
			subject = SubjectOf(key)
		}
		item := Item{Key: key, Subject: subject, Text: text, Urgent: it.Urgent}
		if s := strings.TrimSpace(it.SendAt); s != "" {
			at, err := time.Parse(time.RFC3339, s)
			switch {
			case err != nil:
				out.Skipped = append(out.Skipped, key+": sendAt isn't a time like 2026-11-25T08:30:00+08:00")
				continue
			case at.UnixMilli() > now+SendAtAheadMs:
				out.Skipped = append(out.Skipped, key+": sendAt is more than 48 hours ahead")
				continue
			case at.UnixMilli() > now:
				item.SendAt = at.UnixMilli()
			case at.UnixMilli() < now-SendAtLateMs:
				out.Skipped = append(out.Skipped, key+": sendAt already passed")
				continue
			}
		}
		if len(out.Items) >= MaxItems {
			out.Skipped = append(out.Skipped, key+": over the limit of items per run")
			continue
		}
		seen[key] = true
		out.Items = append(out.Items, item)
	}
	for _, c := range raw.Closed {
		key := strings.TrimSpace(c.Key)
		if keyRe.MatchString(key) && len(out.Closed) < MaxClosed {
			out.Closed = append(out.Closed, Closed{Key: key, Why: clip(Field(c.Why), 120)})
		}
	}
	for _, r := range raw.Remember {
		if t := cleanNote(r.Text); t != "" && len(out.Remember) < MaxRemember {
			out.Remember = append(out.Remember, Remember{Text: t, Days: r.Days})
		}
	}
	for _, id := range raw.Forget {
		if id = strings.TrimSpace(id); strings.HasPrefix(id, "pmem_") && len(out.Forget) < MaxForget {
			out.Forget = append(out.Forget, id)
		}
	}
	out.Summary = MessageText(raw.Summary)
	return out, nil
}

// itemText is one item's text: plain, line breaks kept, capped.
func itemText(s string) string {
	s = MessageText(s)
	if strx.Len16(s) > MaxItemChars {
		s = strx.Slice16(s, MaxItemChars-1) + "…"
	}
	return s
}
