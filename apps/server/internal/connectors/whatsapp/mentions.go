package whatsapp

// Outgoing @mentions (docs/adr/0002-whatsapp-mentions.md). WhatsApp writes a
// mention as "@<user part of the JID>" in the text plus that JID in
// ContextInfo.MentionedJID; the app then shows the person's name and notifies
// them. Agents write the same "@<id>" token, and Send turns a token into a
// real mention only when it is the id of someone in the group right now.
// Anything else stays plain text, so no JID is ever made up.

import (
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/connectors"
)

// MaxMentions caps the people one outbox message may notify. Tokens past it
// stay plain text.
const MaxMentions = 10

// mentionToken: "@" and 5 to 20 digits, not inside a word or an email
// address, and not followed by more digits or a word character.
var mentionToken = regexp.MustCompile(`(^|[^\w@.])@(\d{5,20})\b`)

// UserPart is the part of a JID before "@" (and before a device suffix).
func UserPart(jid string) string {
	bare := BareJID(jid)
	if at := strings.Index(bare, "@"); at != -1 {
		return bare[:at]
	}
	return bare
}

// MentionTokens are the ids written as "@<digits>" in text, in order, once each.
func MentionTokens(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionToken.FindAllStringSubmatch(text, -1) {
		if id := m[2]; !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ResolveMentions maps the "@<digits>" tokens in text to the JIDs of group
// participants whose phone JID or LID has that user part. The JID keeps the
// form the token was written in (a LID token gives the LID, a phone token the
// phone JID), so the text and MentionedJID line up. Unknown tokens are left
// out. At most max JIDs, in order of first appearance.
func ResolveMentions(text string, people []connectors.Participant, max int) []string {
	byUser := map[string]string{}
	for _, p := range people {
		for _, id := range []string{p.ExternalID, p.AltID} {
			if id == "" || !(IsLID(id) || IsPhoneJID(id)) {
				continue
			}
			byUser[UserPart(id)] = BareJID(id)
		}
	}
	var out []string
	for _, tok := range MentionTokens(text) {
		if len(out) >= max {
			break
		}
		if jid, ok := byUser[tok]; ok {
			out = append(out, jid)
		}
	}
	return out
}

// MentionsIn is the subset of jids whose token appears in text, in the order
// of jids: the mentions one chunk of a split message carries.
func MentionsIn(text string, jids []string) []string {
	if len(jids) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, tok := range MentionTokens(text) {
		have[tok] = true
	}
	var out []string
	for _, j := range jids {
		if have[UserPart(j)] {
			out = append(out, j)
		}
	}
	return out
}
