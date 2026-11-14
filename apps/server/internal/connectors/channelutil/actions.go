package channelutil

// Channels without buttons (WhatsApp) show outbox actions as a reply hint
// instead: "Balas: done / tunda 1j / tunda besok". The words come from the
// action id the router understands (AGENTS.md "Outbox actions"), falling back
// to the label.

import (
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var snoozeWords = map[string]string{
	"10m":      "tunda 10m",
	"1h":       "tunda 1j",
	"tomorrow": "tunda besok",
}

// ActionReplyWord is the word the owner types to trigger this action. The
// router maps a reply back with the same function, so both sides agree on the
// vocabulary.
func ActionReplyWord(a db.OutboxAction) string {
	parts := strings.Split(a.ID, ":")
	at := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	head, second, fourth := at(0), at(1), at(3)
	switch {
	case head == "approve":
		return "ya"
	case head == "deny":
		return "batal"
	case head == "rem" && second == "done":
		return "done"
	case head == "rem" && second == "snooze" && fourth != "" && snoozeWords[fourth] != "":
		return snoozeWords[fourth]
	case head == "connector" && (second == "on" || second == "off") && at(2) != "":
		return "/connectors " + second + " " + strings.Join(parts[2:], ":")
	}
	return strx.Lower(strx.Trim(a.Label))
}

// ActionsHint is actionsHint: "" when there is nothing to offer.
func ActionsHint(actions []db.OutboxAction) string {
	var words []string
	seen := map[string]bool{}
	for _, a := range actions {
		w := ActionReplyWord(a)
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	if len(words) == 0 {
		return ""
	}
	return "Balas: " + strings.Join(words, " / ")
}

// MatchActionReply is matchActionReply: the action a free-text reply picks,
// when it matches one word exactly (case-insensitive, surrounding whitespace
// ignored).
func MatchActionReply(text string, actions []db.OutboxAction) (db.OutboxAction, bool) {
	t := strx.Lower(strx.Trim(text))
	if t == "" {
		return db.OutboxAction{}, false
	}
	for _, a := range actions {
		if strx.Lower(ActionReplyWord(a)) == t {
			return a, true
		}
	}
	return db.OutboxAction{}, false
}
