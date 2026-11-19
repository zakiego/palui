package whatsapp

// Text for the WhatsApp message types that carry no plain text and no media
// Palui downloads: contact cards, locations, polls, videos and events. Without
// it such a message reached nobody: deliver dropped it (rpt_0mwmwtby5l0zrpy3sgg,
// a contact card someone later asked the agent for).

import (
	"strconv"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

// VCard is the part of a vCard the agent needs.
type VCard struct {
	Name   string
	Phones []string
}

// ParseVCard reads the formatted name (FN, else N) and every phone number
// (TEL) from a vCard. Folded lines are joined. A TEL with no value falls back
// to its waid parameter, the number's WhatsApp id.
func ParseVCard(vcard string) VCard {
	var out VCard
	lines := unfoldVCard(vcard)
	nameFromN := ""
	seen := map[string]bool{}
	for _, line := range lines {
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		head, value := line[:colon], strx.Trim(line[colon+1:])
		params := strings.Split(head, ";")
		prop := strings.ToUpper(params[0])
		// Grouped properties: "item1.TEL;waid=...:+62 ...".
		if dot := strings.LastIndex(prop, "."); dot != -1 {
			prop = prop[dot+1:]
		}
		switch prop {
		case "FN":
			if out.Name == "" {
				out.Name = unescapeVCard(value)
			}
		case "N":
			if nameFromN == "" {
				nameFromN = nameOfN(value)
			}
		case "TEL":
			phone := value
			if phone == "" {
				for _, p := range params[1:] {
					if k, v, ok := strings.Cut(p, "="); ok && strings.EqualFold(k, "waid") && v != "" {
						phone = "+" + v
					}
				}
			}
			phone = strings.TrimPrefix(phone, "tel:")
			if phone != "" && !seen[phone] {
				seen[phone] = true
				out.Phones = append(out.Phones, phone)
			}
		}
	}
	if out.Name == "" {
		out.Name = nameFromN
	}
	return out
}

func unfoldVCard(vcard string) []string {
	raw := strings.Split(strings.ReplaceAll(vcard, "\r\n", "\n"), "\n")
	var lines []string
	for _, l := range raw {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	return lines
}

// nameOfN: "Family;Given;Middle;Prefix;Suffix" -> "Given Middle Family".
func nameOfN(value string) string {
	parts := strings.Split(value, ";")
	order := []int{3, 1, 2, 0, 4}
	var words []string
	for _, i := range order {
		if i < len(parts) {
			if w := strx.Trim(unescapeVCard(parts[i])); w != "" {
				words = append(words, w)
			}
		}
	}
	return strings.Join(words, " ")
}

func unescapeVCard(s string) string {
	return strings.NewReplacer(`\,`, ",", `\;`, ";", `\n`, " ", `\N`, " ", `\\`, `\`).Replace(s)
}

// contactEntry: "Name, +62 812-3456-7890" from a contact card. The name the
// sender's phone shows wins over the one inside the vCard.
func contactEntry(c *waE2E.ContactMessage) string {
	card := ParseVCard(c.GetVcard())
	name := strx.Trim(c.GetDisplayName())
	if name == "" {
		name = card.Name
	}
	if name == "" {
		name = "tanpa nama"
	}
	if len(card.Phones) == 0 {
		return name + ", tanpa nomor"
	}
	return name + ", " + strings.Join(card.Phones, ", ")
}

// ContactLabel: "[contact: Name, +62 ...]" for one card.
func ContactLabel(c *waE2E.ContactMessage) string {
	return "[contact: " + contactEntry(c) + "]"
}

// ContactsLabel: "[contacts: A, +62 ...; B, +62 ...]" for several cards.
func ContactsLabel(c *waE2E.ContactsArrayMessage) string {
	var entries []string
	for _, one := range c.GetContacts() {
		if one != nil {
			entries = append(entries, contactEntry(one))
		}
	}
	if len(entries) == 0 {
		if n := strx.Trim(c.GetDisplayName()); n != "" {
			return "[contacts: " + n + "]"
		}
		return "[contacts]"
	}
	return "[contacts: " + strings.Join(entries, "; ") + "]"
}

func coords(lat, lng float64) string {
	return strconv.FormatFloat(lat, 'f', 6, 64) + "," + strconv.FormatFloat(lng, 'f', 6, 64)
}

func locationLabel(c *waE2E.LocationMessage) string {
	var bits []string
	for _, s := range []string{c.GetName(), c.GetAddress()} {
		if s = strx.Trim(s); s != "" {
			bits = append(bits, s)
		}
	}
	if c.DegreesLatitude != nil || c.DegreesLongitude != nil {
		ll := coords(c.GetDegreesLatitude(), c.GetDegreesLongitude())
		bits = append(bits, ll, "https://maps.google.com/?q="+ll)
	}
	if len(bits) == 0 {
		return "[location]"
	}
	return "[location: " + strings.Join(bits, ", ") + "]"
}

func liveLocationLabel(c *waE2E.LiveLocationMessage) string {
	if c.DegreesLatitude == nil && c.DegreesLongitude == nil {
		return "[live location]"
	}
	ll := coords(c.GetDegreesLatitude(), c.GetDegreesLongitude())
	return "[live location: " + ll + ", https://maps.google.com/?q=" + ll + "]"
}

func pollLabel(c *waE2E.PollCreationMessage) string {
	var opts []string
	for _, o := range c.GetOptions() {
		if n := strx.Trim(o.GetOptionName()); n != "" {
			opts = append(opts, n)
		}
	}
	q := strx.Trim(c.GetName())
	switch {
	case q == "" && len(opts) == 0:
		return "[poll]"
	case len(opts) == 0:
		return "[poll: " + q + "]"
	}
	return "[poll: " + q + " | options: " + strings.Join(opts, " / ") + "]"
}

func pollOf(c *waE2E.Message) *waE2E.PollCreationMessage {
	for _, p := range []*waE2E.PollCreationMessage{c.GetPollCreationMessage(), c.GetPollCreationMessageV2(), c.GetPollCreationMessageV3(), c.GetPollCreationMessageV5()} {
		if p != nil {
			return p
		}
	}
	return nil
}

func videoLabel(v *waE2E.VideoMessage, note bool) string {
	switch {
	case note:
		return "[video note]"
	case v.GetGifPlayback():
		return "[GIF]"
	}
	return "[video]"
}

// ContentLabel is the text that stands in for a message Palui doesn't
// download: a contact card with its numbers, a location with its coordinates,
// a poll with its options, a video or an event. "" for text, photos, stickers,
// voice notes and documents (their text and media are handled elsewhere).
// A video's caption is the message text, so the label stays short.
func ContentLabel(c *waE2E.Message) string {
	switch {
	case c == nil:
		return ""
	case c.ContactMessage != nil:
		return ContactLabel(c.ContactMessage)
	case c.ContactsArrayMessage != nil:
		return ContactsLabel(c.ContactsArrayMessage)
	case c.LocationMessage != nil:
		return locationLabel(c.LocationMessage)
	case c.LiveLocationMessage != nil:
		return liveLocationLabel(c.LiveLocationMessage)
	case pollOf(c) != nil:
		return pollLabel(pollOf(c))
	case c.VideoMessage != nil:
		return videoLabel(c.VideoMessage, false)
	case c.PtvMessage != nil:
		return videoLabel(c.PtvMessage, true)
	case c.EventMessage != nil:
		if n := strx.Trim(c.EventMessage.GetName()); n != "" {
			return "[event: " + n + "]"
		}
		return "[event]"
	}
	return ""
}
