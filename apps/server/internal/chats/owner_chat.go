package chats

import (
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/owner"
)

// OwnerChat is the owner's DM on a channel: the chat they last wrote from, or
// one made from the owner identity (a Telegram user id is also their private
// chat id; a WhatsApp phone JID is a valid DM address). ok is false when the
// channel has no owner (or, for web, no chat yet).
func OwnerChat(q db.Q, channel string, now int64) (db.Chat, bool, error) {
	if channel == db.ChannelWeb {
		return db.One(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE channel = 'web' LIMIT 1"), db.ScanChat)
	}
	o, ok, err := owner.GetOwner(q, channel)
	if err != nil || !ok {
		return db.Chat{}, false, err
	}
	ids := []any{o.ExternalID}
	if o.AltID != nil && *o.AltID != "" {
		ids = append(ids, *o.AltID)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := append([]any{channel}, ids...)
	existing, found, err := db.One(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE channel = ? AND connector_id = '' AND external_id IN ("+marks+") AND kind = 'dm' ORDER BY last_message_at DESC LIMIT 1", args...), db.ScanChat)
	if err != nil || found {
		return existing, found, err
	}
	title := ""
	if o.DisplayName != nil {
		title = *o.DisplayName
	}
	c, err := Ensure(q, EnsureInput{Channel: channel, ExternalID: o.ExternalID, Kind: "dm", Title: title}, now)
	return c, err == nil, err
}
