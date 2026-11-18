package chat

// The inbox (inbox.ts, PLAN 8.1): every inbound message is written to
// `messages` with status `pending` before the channel handler returns. Media is
// turned into something the model can read later, at dispatch time, so a slow
// transcription never holds up the channel.

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// MaxInlineDocBytes is MAX_INLINE_DOC_BYTES.
const MaxInlineDocBytes = 64 * 1024

var (
	textMime = regexp.MustCompile(`(?i)^(text/|application/(json|xml|x-yaml|yaml|csv|x-ndjson|javascript|typescript|x-sh|sql))`)
	textExt  = regexp.MustCompile(`(?i)\.(txt|md|markdown|csv|tsv|json|ya?ml|xml|log|ini|toml|ts|js|py|sql|sh|html?)$`)
	spaceRun = regexp.MustCompile(`[` + strx.SpaceClass + `]+`)
)

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// WriteInbound is writeInbound. fromOwner comes from the sender check (PLAN
// 19.3); guests only get here in chats that exist, are allowed and have an agent.
func WriteInbound(q db.Q, msg connectors.InboundMessage, now int64, fromOwner bool) (db.Chat, db.Message, error) {
	// WhatsApp (PLAN 5.1, 19.2): a new group chat starts outside the
	// allowlist. The owner's DM is always in it.
	ownerWaDm := msg.Channel == "whatsapp" && msg.ChatKind == "dm" && fromOwner
	scope := ChatScope(msg.Channel, msg.ConnectorID)
	agentID, err := ScopeAgentID(q, scope)
	if err != nil {
		return db.Chat{}, db.Message{}, err
	}
	allowed := true
	if msg.Channel == "whatsapp" {
		allowed = ownerWaDm
	}
	chat, err := chats.Ensure(q, chats.EnsureInput{Channel: msg.Channel, ExternalID: msg.ChatExternalID, Title: msg.ChatTitle, Kind: msg.ChatKind, Allowed: &allowed, ConnectorID: scope, AgentID: agentID}, now)
	if err != nil {
		return db.Chat{}, db.Message{}, err
	}
	// A DM left out by the old utusan chat split opens again on its next message.
	if ownerWaDm && !chat.Allowed {
		if chat, err = db.ScanChat(q.WQueryRow("UPDATE chats SET allowed = 1 WHERE id = ? RETURNING "+db.ChatCols, chat.ID)); err != nil {
			return db.Chat{}, db.Message{}, err
		}
	}
	content := jsonx.NewMap[any]()
	content.Set("text", msg.Text)
	quoted, err := ResolveQuote(q, chat.ID, msg)
	if err != nil {
		return db.Chat{}, db.Message{}, err
	}
	if quoted != nil {
		content.Set("quoted", quoted)
	}
	if len(msg.Media) > 0 {
		media := make([]any, len(msg.Media))
		for i, m := range msg.Media {
			o := jsonx.NewMap[any]()
			o.Set("kind", m.Kind)
			o.Set("mimeType", m.MimeType)
			o.Set("data", m.Data)
			if m.Kind != "image" && m.Filename != "" {
				o.Set("filename", m.Filename)
			}
			media[i] = o
		}
		content.Set("media", media)
	}
	if msg.Callback != nil {
		content.Set("callback", msg.Callback)
	}
	raw, err := db.Raw(content)
	if err != nil {
		return db.Chat{}, db.Message{}, err
	}
	var senderName *string
	if msg.SenderName != "" {
		senderName = ptrOrNil(strx.Slice16(strx.Trim(spaceRun.ReplaceAllString(msg.SenderName, " ")), 80))
	}
	createdAt := msg.At
	if createdAt == 0 {
		createdAt = now
	}
	row, err := db.ScanMessage(q.WQueryRow("INSERT INTO messages (id, chat_id, session_id, role, content, text, channel_msg_id, reply_to_channel_msg_id, sender_id, sender_name, from_owner, status, created_at) VALUES (?, ?, ?, 'user', ?, ?, ?, ?, ?, ?, ?, 'pending', ?) RETURNING "+db.MessageCols,
		secret.NewID("msg"), chat.ID, chat.SessionID, raw, msg.Text, ptrOrNil(msg.ChannelMsgID), ptrOrNil(msg.ReplyToChannelMsgID), msg.SenderExternalID, senderName, db.Bool(fromOwner), createdAt))
	if err != nil {
		return db.Chat{}, db.Message{}, err
	}
	if _, err := q.Exec("UPDATE chats SET last_message_at = ? WHERE id = ?", now, chat.ID); err != nil {
		return db.Chat{}, db.Message{}, err
	}
	return chat, row, nil
}

var quoteFrom = map[string]string{"self": "assistant", "sender": "user", "other": "other", "unknown": "unknown"}

func trimEnd(s string) string { return strings.TrimRightFunc(s, strx.IsSpace) }

func capQuote(text string) string {
	t := strx.Trim(text)
	if strx.Len16(t) > QuoteMaxChars {
		return trimEnd(strx.Slice16(t, QuoteMaxChars)) + "... (truncated)"
	}
	return t
}

// ResolveQuote is resolveQuote: what the new message quotes. Our own copy wins
// (a reply Palui sent, from messages or the outbox, or an earlier owner
// message); otherwise what the channel sent.
func ResolveQuote(q db.Q, chatID string, msg connectors.InboundMessage) (*StoredQuote, error) {
	if id := msg.ReplyToChannelMsgID; id != "" {
		var role, text string
		var content db.RawJSON
		var senderID *string
		err := q.QueryRow("SELECT role, text, content, sender_id FROM messages WHERE chat_id = ? AND channel_msg_id = ? ORDER BY created_at DESC LIMIT 1", chatID, id).Scan(&role, &text, &content, &senderID)
		if err != nil && !isNoRows(err) {
			return nil, err
		}
		if err == nil && (role == "assistant" || role == "user") {
			c := ParseContent(content)
			t := text
			if t == "" {
				t = c.Text
			}
			t = capQuote(t)
			media := ""
			if c.hasImage() {
				media = "[image]"
			}
			if t != "" || media != "" {
				return &StoredQuote{From: storedQuoteFrom(role, senderID, msg), Text: t, Media: media}, nil
			}
		}
		var payload db.JSON[db.OutboxPayload]
		err = q.QueryRow("SELECT payload FROM outbox WHERE chat_id = ? AND channel_msg_id = ? ORDER BY created_at DESC LIMIT 1", chatID, id).Scan(&payload)
		if err != nil && !isNoRows(err) {
			return nil, err
		}
		if err == nil && payload.V.Text != "" {
			return &StoredQuote{From: "assistant", Text: capQuote(payload.V.Text)}, nil
		}
	}
	qt := msg.Quoted
	if qt == nil {
		return nil, nil
	}
	from, ok := quoteFrom[qt.From]
	if !ok {
		from = "unknown"
	}
	return &StoredQuote{From: from, Text: capQuote(qt.Text), Media: qt.Media}, nil
}

// storedQuoteFrom: who wrote a quoted row we stored. A "user" row is the
// sender's own only when they sent it; in a group it can be anyone's, and
// calling it theirs hid who the model was really reading
// (rpt_0mwm8tx7y3elwuzgdhl) and skipped the outsider taint. When the stored
// sender doesn't match either form of the new sender's id, the channel's own
// verdict decides (it maps LIDs and phone JIDs), and without one it's "other".
func storedQuoteFrom(role string, senderID *string, msg connectors.InboundMessage) string {
	if role != "user" || senderID == nil || *senderID == "" {
		return role
	}
	for _, id := range []string{msg.SenderExternalID, msg.SenderAltID} {
		if id != "" && id == *senderID {
			return "user"
		}
	}
	if msg.Quoted != nil && msg.Quoted.From == "sender" {
		return "user"
	}
	return "other"
}

func decodedSize(b64 string) int { return len(b64) * 3 / 4 }

func isTextDoc(mimeType, filename string) bool {
	return textMime.MatchString(mimeType) || (filename != "" && textExt.MatchString(filename))
}

// DecodeBase64 is Buffer.from(s, "base64"): lenient about padding, URL-safe
// characters and anything outside the alphabet.
func DecodeBase64(s string) []byte {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/':
			b.WriteRune(r)
		case r == '-':
			b.WriteByte('+')
		case r == '_':
			b.WriteByte('/')
		case r == '=':
			// Node stops at the first padding character.
			goto done
		}
	}
done:
	clean := b.String()
	if len(clean)%4 == 1 {
		clean = clean[:len(clean)-1]
	}
	out, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil {
		return nil
	}
	return out
}

func mediaEntry(pairs ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// PrepareMedia is prepareMedia: audio becomes "[voice] ..." text through the
// first running connector with Transcribe when the chat's agent has voice
// notes on (transcribe), small text documents are inlined. Images stay as
// base64 parts for the vision role. Updates the row.
func PrepareMedia(ctx context.Context, q db.Q, row db.Message, running []connectors.RunningEntry, transcribe bool, log *logx.Logger) (db.Message, error) {
	c := ParseContent(row.Content)
	needs := false
	for _, m := range c.Media {
		if m.Kind != "image" {
			needs = true
		}
	}
	if c.Prepared || !needs {
		return row, nil
	}
	var texts []string
	if row.Text != "" {
		texts = append(texts, row.Text)
	}
	var kept []any
	var transcriber *connectors.RunningEntry
	for i := range running {
		if running[i].Handle.Transcribe != nil {
			transcriber = &running[i]
			break
		}
	}
	for _, m := range c.Media {
		data := ""
		if m.Data != nil {
			data = *m.Data
		}
		if m.Kind == "image" {
			kept = append(kept, mediaEntry("kind", "image", "mimeType", m.MimeType, "data", data))
			continue
		}
		if m.Kind == "audio" {
			if !row.FromOwner {
				// Transcribing costs money per note, so only the owner's are.
				texts = append(texts, "[voice] (pesan suara dari tamu, gak ditranskrip)")
				kept = append(kept, mediaEntry("kind", "audio", "mimeType", m.MimeType, "note", "guest, not transcribed"))
				continue
			}
			if !transcribe {
				texts = append(texts, "[voice] (pesan suara, tapi transkripsi voice note lagi mati buat agent ini)")
				kept = append(kept, mediaEntry("kind", "audio", "mimeType", m.MimeType, "note", "transcription off for this agent"))
				continue
			}
			if transcriber == nil || data == "" {
				texts = append(texts, "[voice] (pesan suara, tapi gak ada skill transkripsi yang nyala)")
				kept = append(kept, mediaEntry("kind", "audio", "mimeType", m.MimeType, "note", "not transcribed"))
				continue
			}
			said, err := safeTranscribe(ctx, transcriber.Handle, connectors.InboundMedia{Kind: "audio", MimeType: m.MimeType, Data: data})
			if err != nil {
				log.Warn("transcription failed", logx.Fields{"connector_id": transcriber.ID, "error": err})
				texts = append(texts, "[voice] (pesan suara, transkripsinya gagal)")
				kept = append(kept, mediaEntry("kind", "audio", "mimeType", m.MimeType, "note", "transcription failed"))
				continue
			}
			texts = append(texts, "[voice] "+strx.Trim(said))
			kept = append(kept, mediaEntry("kind", "audio", "mimeType", m.MimeType, "note", "transcribed by "+transcriber.ID))
			continue
		}
		// Documents: small text files are inlined, the rest is named only. The
		// bytes stay on the row so the attachments tools can save the file.
		name := "dokumen"
		if m.Filename != nil {
			name = *m.Filename
		}
		filename := ""
		if m.Filename != nil {
			filename = *m.Filename
		}
		var entry *jsonx.Object
		if data != "" && isTextDoc(m.MimeType, filename) && decodedSize(data) <= MaxInlineDocBytes {
			texts = append(texts, fmt.Sprintf("[file: %s]\n%s", name, strx.DecodeUTF8(DecodeBase64(data))))
			entry = mediaEntry("kind", "document", "mimeType", m.MimeType, "filename", name, "note", "inlined")
		} else {
			texts = append(texts, fmt.Sprintf("[file: %s (%s), gak bisa dibaca langsung]", name, m.MimeType))
			entry = mediaEntry("kind", "document", "mimeType", m.MimeType, "filename", name, "note", "not readable")
		}
		if data != "" {
			entry.Set("data", data)
		}
		kept = append(kept, entry)
	}
	text := strings.Join(texts, "\n\n")
	content, err := jsonx.ParseObject(row.Content)
	if err != nil {
		content = jsonx.NewMap[any]()
	}
	content.Set("text", text)
	content.Set("media", kept)
	content.Set("prepared", true)
	raw, err := db.Raw(content)
	if err != nil {
		return row, err
	}
	return db.ScanMessage(q.WQueryRow("UPDATE messages SET content = ?, text = ? WHERE id = ? RETURNING "+db.MessageCols, raw, text, row.ID))
}

func safeTranscribe(ctx context.Context, h *connectors.RunningConnector, m connectors.InboundMedia) (s string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("transcribe panicked: %v", r)
		}
	}()
	return h.Transcribe(ctx, m)
}
