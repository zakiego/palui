package whatsapp

// Pure helpers for reading WhatsApp messages and JIDs (whatsapp/messages.ts),
// on whatsmeow's waE2E protobufs instead of Baileys' WAProto. No socket, no IO.

import (
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/zakiego/palui/apps/server/internal/strx"
)

// BareJID strips a device suffix: "628123:12@s.whatsapp.net" -> "628123@s.whatsapp.net".
func BareJID(jid string) string {
	at := strings.Index(jid, "@")
	if at == -1 {
		return jid
	}
	local := jid[:at]
	if c := strings.Index(local, ":"); c != -1 {
		local = local[:c]
	}
	return local + jid[at:]
}

// JIDDigits is the digits of the user part.
func JIDDigits(jid string) string {
	local := strings.SplitN(BareJID(jid), "@", 2)[0]
	var b strings.Builder
	for _, r := range local {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func IsLID(jid string) bool      { return strings.HasSuffix(jid, "@lid") }
func IsPhoneJID(jid string) bool { return strings.HasSuffix(jid, "@s.whatsapp.net") }

// IsDirectChat: DMs only, no groups, status updates, broadcast lists or newsletters.
func IsDirectChat(jid string) bool {
	if jid == "status@broadcast" {
		return false
	}
	if strings.HasSuffix(jid, "@g.us") || strings.HasSuffix(jid, "@broadcast") || strings.HasSuffix(jid, "@newsletter") {
		return false
	}
	return IsPhoneJID(jid) || IsLID(jid)
}

// IsGroupChat: a group chat (PLAN 19.6). Groups are read so agents can answer mentions.
func IsGroupChat(jid string) bool { return strings.HasSuffix(jid, "@g.us") }

// contextInfos is Object.values(content).map(v => v?.contextInfo): the
// contextInfo of every set part, in field order.
func contextInfos(content *waE2E.Message) []*waE2E.ContextInfo {
	if content == nil {
		return nil
	}
	var out []*waE2E.ContextInfo
	m := content.ProtoReflect()
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() || !m.Has(fd) {
			continue
		}
		sub := m.Get(fd).Message()
		cfd := sub.Descriptor().Fields().ByName("contextInfo")
		if cfd == nil || cfd.Kind() != protoreflect.MessageKind || !sub.Has(cfd) {
			continue
		}
		if ci, ok := sub.Get(cfd).Message().Interface().(*waE2E.ContextInfo); ok {
			out = append(out, ci)
		}
	}
	return out
}

// IsAddressed: whether a group message is meant for us. It @mentions this
// account (by phone JID or LID), or it replies to one of our messages.
func IsAddressed(content *waE2E.Message, selfIDs ...string) bool {
	if content == nil {
		return false
	}
	self := map[string]bool{}
	for _, id := range selfIDs {
		if id != "" {
			self[BareJID(id)] = true
		}
	}
	for _, ctx := range contextInfos(content) {
		for _, j := range ctx.GetMentionedJID() {
			if self[BareJID(j)] {
				return true
			}
		}
		if p := ctx.GetParticipant(); p != "" && self[BareJID(p)] {
			return true
		}
	}
	return false
}

// Unwrap removes the envelopes WhatsApp puts around real content (disappearing
// messages, view once, documents with a caption, edits), at most five deep.
func Unwrap(content *waE2E.Message) *waE2E.Message {
	m := content
	for i := 0; i < 5 && m != nil; i++ {
		var inner *waE2E.Message
		switch {
		case m.GetEphemeralMessage().GetMessage() != nil:
			inner = m.GetEphemeralMessage().GetMessage()
		case m.GetViewOnceMessage().GetMessage() != nil:
			inner = m.GetViewOnceMessage().GetMessage()
		case m.GetViewOnceMessageV2().GetMessage() != nil:
			inner = m.GetViewOnceMessageV2().GetMessage()
		case m.GetViewOnceMessageV2Extension().GetMessage() != nil:
			inner = m.GetViewOnceMessageV2Extension().GetMessage()
		case m.GetDocumentWithCaptionMessage().GetMessage() != nil:
			inner = m.GetDocumentWithCaptionMessage().GetMessage()
		case m.GetEditedMessage().GetMessage() != nil:
			inner = m.GetEditedMessage().GetMessage()
		}
		if inner == nil {
			break
		}
		m = inner
	}
	return m
}

// TextOf: conversation, extended text, then the image, video and document captions.
func TextOf(c *waE2E.Message) string {
	switch {
	case c == nil:
		return ""
	case c.Conversation != nil:
		return *c.Conversation
	case c.ExtendedTextMessage != nil && c.ExtendedTextMessage.Text != nil:
		return *c.ExtendedTextMessage.Text
	case c.ImageMessage != nil && c.ImageMessage.Caption != nil:
		return *c.ImageMessage.Caption
	case c.VideoMessage != nil && c.VideoMessage.Caption != nil:
		return *c.VideoMessage.Caption
	case c.DocumentMessage != nil && c.DocumentMessage.Caption != nil:
		return *c.DocumentMessage.Caption
	}
	return ""
}

// MediaLabel is a short label for media in a quoted message: "[image]",
// "[document: a.pdf]". The quote only carries a thumbnail, so nothing is downloaded.
func MediaLabel(c *waE2E.Message) string {
	switch {
	case c == nil:
		return ""
	case c.ImageMessage != nil:
		return "[image]"
	case c.AudioMessage != nil:
		if c.AudioMessage.GetPTT() {
			return "[voice note]"
		}
		return "[audio]"
	case c.DocumentMessage != nil:
		if n := c.DocumentMessage.GetFileName(); n != "" {
			return "[document: " + n + "]"
		}
		return "[document]"
	case c.StickerMessage != nil:
		return "[sticker]"
	case c.AlbumMessage != nil:
		// The album message carries only counts; its photos come after it,
		// each as its own message pointing back here (AlbumOf).
		if n := c.AlbumMessage.GetExpectedImageCount() + c.AlbumMessage.GetExpectedVideoCount(); n > 0 {
			return fmt.Sprintf("[album of %d]", n)
		}
		return "[album]"
	}
	// Contacts keep their numbers and locations their coordinates, so a
	// reply to one still says what it was.
	return ContentLabel(c)
}

// AlbumOf is the id of the album a photo or video belongs to, "" when it's
// on its own. WhatsApp sends an album as one empty album message, then each
// item as a message whose messageAssociation points at the album's id. A
// reply to the album quotes that first, empty message (rpt_0mwsqf7y5vjh0reoc1a).
func AlbumOf(m *waE2E.Message) string {
	for i := 0; i < 5 && m != nil; i++ {
		a := m.GetMessageContextInfo().GetMessageAssociation()
		if a.GetAssociationType() == waE2E.MessageAssociation_MEDIA_ALBUM {
			if id := a.GetParentMessageKey().GetID(); id != "" {
				return id
			}
		}
		next := Unwrap(m)
		if next == m {
			break
		}
		m = next
	}
	return ""
}

// QuoteInfo is the message a message quotes.
type QuoteInfo struct {
	// The quoted message's id (stanzaId), "" when absent.
	ID string `json:"id,omitempty"`
	// Who wrote it (contextInfo.participant), when WhatsApp says.
	Participant string `json:"participant,omitempty"`
	Text        string `json:"text"`
	Media       string `json:"media,omitempty"`
	// The quoted message itself. It carries the media keys, so a quoted
	// photo or sticker can still be downloaded.
	Message *waE2E.Message `json:"-"`
}

// QuoteOf is the message this one quotes, from whichever part carries
// contextInfo. Only one level: a quote inside the quoted message is ignored.
// Text and media are empty when WhatsApp sent the id without the body.
func QuoteOf(content *waE2E.Message) *QuoteInfo {
	for _, ctx := range contextInfos(content) {
		if ctx.GetStanzaID() == "" && ctx.QuotedMessage == nil {
			continue
		}
		quoted := Unwrap(ctx.QuotedMessage)
		return &QuoteInfo{
			ID:          ctx.GetStanzaID(),
			Participant: ctx.GetParticipant(),
			Text:        strx.Trim(TextOf(quoted)),
			Media:       MediaLabel(quoted),
			Message:     quoted,
		}
	}
	return nil
}

// QuoteAuthor: who wrote a quoted message. This account (what Palui sends
// as), the sender of the new message, someone else, or unknown when WhatsApp
// left out the participant. In a DM there are only two people, so anyone who
// isn't us is the sender, even when their LID isn't mapped yet.
func QuoteAuthor(participant string, selfIDs, senderIDs []string, directChat bool) string {
	if participant == "" {
		return "unknown"
	}
	p := BareJID(participant)
	for _, id := range selfIDs {
		if id != "" && BareJID(id) == p {
			return "self"
		}
	}
	if directChat {
		return "sender"
	}
	for _, id := range senderIDs {
		if id != "" && BareJID(id) == p {
			return "sender"
		}
	}
	return "other"
}

// MediaInfo is what a message carries for the agent.
type MediaInfo struct {
	// image, audio or document.
	Kind     string
	MimeType string
	// fileLength, -1 when absent.
	Bytes       int64
	Filename    string
	DurationSec *float64
	// Sticker: an image sent as a sticker (WebP). Animated ones reach the
	// model as their first frame; Lottie ones can't be drawn at all.
	Sticker  bool
	Animated bool
	Lottie   bool
}

// MediaOf: image (stickers too), audio (voice notes too) or document; nil for
// anything else.
func MediaOf(c *waE2E.Message) *MediaInfo {
	if c == nil {
		return nil
	}
	length := func(p *uint64) int64 {
		if p == nil {
			return -1
		}
		return int64(*p)
	}
	mime := func(p *string, def string) string {
		if p == nil {
			return def
		}
		return *p
	}
	switch {
	case c.ImageMessage != nil:
		return &MediaInfo{Kind: "image", MimeType: mime(c.ImageMessage.Mimetype, "image/jpeg"), Bytes: length(c.ImageMessage.FileLength)}
	case c.AudioMessage != nil:
		info := &MediaInfo{Kind: "audio", MimeType: mime(c.AudioMessage.Mimetype, "audio/ogg"), Bytes: length(c.AudioMessage.FileLength)}
		if c.AudioMessage.Seconds != nil {
			d := float64(*c.AudioMessage.Seconds)
			info.DurationSec = &d
		}
		return info
	case c.DocumentMessage != nil:
		return &MediaInfo{Kind: "document", MimeType: mime(c.DocumentMessage.Mimetype, "application/octet-stream"), Bytes: length(c.DocumentMessage.FileLength), Filename: c.DocumentMessage.GetFileName()}
	case c.StickerMessage != nil:
		st := c.StickerMessage
		return &MediaInfo{Kind: "image", MimeType: mime(st.Mimetype, "image/webp"), Bytes: length(st.FileLength), Sticker: true, Animated: st.GetIsAnimated(), Lottie: st.GetIsLottie()}
	}
	return nil
}

// FormatPairingCode: "ABCDEFGH" -> "ABCD-EFGH".
func FormatPairingCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	c := strings.ToUpper(b.String())
	if len(c) == 8 {
		return c[:4] + "-" + c[4:]
	}
	return c
}

// PairingDigits is the number for a pairing code: country code included,
// leading 0 -> 62.
func PairingDigits(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if strings.HasPrefix(d, "0") {
		d = "62" + d[1:]
	}
	return d
}

// TimestampMs is the message time in ms, fallback when unknown.
func TimestampMs(seconds int64, fallback int64) int64 {
	if seconds > 0 {
		return seconds * 1000
	}
	return fallback
}
