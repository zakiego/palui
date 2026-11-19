// Package chats ports packages/core/src/chats.ts. A chat is one conversation on
// one channel; its current session holds the summary and the messages in context.
package chats

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const WebChatExternalID = "web"

func Get(q db.Q, id string) (db.Chat, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE id = ?", id), db.ScanChat)
}

// Find looks a chat up by channel, connector ("" is the channel's main one)
// and external id.
func Find(q db.Q, channel, externalID, connectorID string) (db.Chat, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE channel = ? AND connector_id = ? AND external_id = ?", channel, connectorID, externalID), db.ScanChat)
}

// SetReplyMode is for groups: "mention" or "all".
func SetReplyMode(q db.Q, chatID, mode string) (db.Chat, bool, error) {
	return db.One(q.WQueryRow("UPDATE chats SET reply_mode = ? WHERE id = ? RETURNING "+db.ChatCols, mode, chatID), db.ScanChat)
}

// AckPatch sets the reaction while a reply is written. Nil fields stay.
type AckPatch struct {
	On    *bool
	Emoji *string
}

// SetAck turns the reaction on or off and picks its emoji. Callers check the
// emoji with ack.ParseEmoji.
func SetAck(q db.Q, chatID string, patch AckPatch) (db.Chat, bool, error) {
	var sets []string
	var args []any
	if patch.On != nil {
		sets = append(sets, "ack = ?")
		args = append(args, db.Bool(*patch.On))
	}
	if patch.Emoji != nil {
		sets = append(sets, "ack_emoji = ?")
		args = append(args, *patch.Emoji)
	}
	if len(sets) == 0 {
		return Get(q, chatID)
	}
	args = append(args, chatID)
	return db.One(q.WQueryRow("UPDATE chats SET "+strings.Join(sets, ", ")+" WHERE id = ? RETURNING "+db.ChatCols, args...), db.ScanChat)
}

// SaveMediaPatch sets the chat's auto-save. Nil fields stay.
type SaveMediaPatch struct {
	On *bool
	// Retention in days; callers check it with attachments.ValidAutoDays.
	Days *int64
}

// SetSaveMedia turns auto-saving this chat's photos and documents on or off
// and sets how long they stay. Files already saved are kept either way.
func SetSaveMedia(q db.Q, chatID string, patch SaveMediaPatch) (db.Chat, bool, error) {
	var sets []string
	var args []any
	if patch.On != nil {
		sets = append(sets, "save_media = ?")
		args = append(args, db.Bool(*patch.On))
	}
	if patch.Days != nil {
		sets = append(sets, "save_media_days = ?")
		args = append(args, *patch.Days)
	}
	if len(sets) == 0 {
		return Get(q, chatID)
	}
	args = append(args, chatID)
	return db.One(q.WQueryRow("UPDATE chats SET "+strings.Join(sets, ", ")+" WHERE id = ? RETURNING "+db.ChatCols, args...), db.ScanChat)
}

// EnsureInput: Allowed and AgentID only apply to a new chat.
type EnsureInput struct {
	Channel    string
	ExternalID string
	Title      string
	// "dm" (default) or "group".
	Kind        string
	Allowed     *bool
	ConnectorID string
	AgentID     *string
}

// Ensure finds or creates the chat and makes sure it has an open session.
func Ensure(q db.Q, in EnsureInput, now int64) (db.Chat, error) {
	var out db.Chat
	err := q.Tx(func(tx db.Q) error {
		chat, ok, err := db.One(tx.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE channel = ? AND connector_id = ? AND external_id = ?", in.Channel, in.ConnectorID, in.ExternalID), db.ScanChat)
		if err != nil {
			return err
		}
		if !ok {
			kind := in.Kind
			if kind == "" {
				kind = "dm"
			}
			allowed := true
			if in.Allowed != nil {
				allowed = *in.Allowed
			}
			chat, err = db.ScanChat(tx.WQueryRow("INSERT INTO chats (id, channel, connector_id, external_id, title, kind, allowed, agent_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+db.ChatCols,
				secret.NewID("chat"), in.Channel, in.ConnectorID, in.ExternalID, in.Title, kind, db.Bool(allowed), in.AgentID, now))
			if err != nil {
				return err
			}
		} else if in.Title != "" && in.Title != chat.Title {
			chat, err = db.ScanChat(tx.WQueryRow("UPDATE chats SET title = ? WHERE id = ? RETURNING "+db.ChatCols, in.Title, chat.ID))
			if err != nil {
				return err
			}
		}
		if chat.SessionID == nil || *chat.SessionID == "" {
			s, err := db.ScanSession(tx.WQueryRow("INSERT INTO sessions (id, chat_id, created_at) VALUES (?, ?, ?) RETURNING "+db.SessionCols, secret.NewID("ses"), chat.ID, now))
			if err != nil {
				return err
			}
			chat, err = db.ScanChat(tx.WQueryRow("UPDATE chats SET session_id = ? WHERE id = ? RETURNING "+db.ChatCols, s.ID, chat.ID))
			if err != nil {
				return err
			}
		}
		out = chat
		return nil
	})
	return out, err
}

// EnsureWebChat is the dashboard's "Palui (web)" chat.
func EnsureWebChat(q db.Q, now int64) (db.Chat, error) {
	return Ensure(q, EnsureInput{Channel: db.ChannelWeb, ExternalID: WebChatExternalID, Title: "Palui (web)"}, now)
}

func GetSession(q db.Q, id string) (db.Session, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.SessionCols+" FROM sessions WHERE id = ?", id), db.ScanSession)
}

// Rotated is what RotateSession closed and opened.
type Rotated struct {
	Closed *db.Session
	Opened db.Session
}

// RotateSession is `/new`: close the current session and open a fresh one.
func RotateSession(q db.Q, chatID string, now int64) (Rotated, error) {
	var out Rotated
	err := q.Tx(func(tx db.Q) error {
		chat, ok, err := db.One(tx.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE id = ?", chatID), db.ScanChat)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("No chat %s", chatID)
		}
		if chat.SessionID != nil && *chat.SessionID != "" {
			closed, ok, err := db.One(tx.WQueryRow("UPDATE sessions SET closed_at = ? WHERE id = ? RETURNING "+db.SessionCols, now, *chat.SessionID), db.ScanSession)
			if err != nil {
				return err
			}
			if ok {
				out.Closed = &closed
			}
		}
		opened, err := db.ScanSession(tx.WQueryRow("INSERT INTO sessions (id, chat_id, created_at) VALUES (?, ?, ?) RETURNING "+db.SessionCols, secret.NewID("ses"), chatID, now))
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE chats SET session_id = ? WHERE id = ?", opened.ID, chatID); err != nil {
			return err
		}
		out.Opened = opened
		return nil
	})
	return out, err
}

// --- Cross-chat lookup (the chat history skill) ------------------------------------

// Cond is an extra SQL filter on chats, like chatsServedBy() for an agent's tools.
type Cond struct {
	SQL  string
	Args []any
}

type Search struct {
	// Matched against the title, the external id and the chat id (case-insensitive substring).
	Query   string
	Channel string
	Kind    string
	Limit   int
	Scope   *Cond
}

type Hit struct {
	db.Chat
	MessageCount int64 `json:"messageCount"`
}

const SearchMax = 20

func likeArg(q string) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range q {
		if r == '\\' || r == '%' || r == '_' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

// SearchChats returns chats matching the search, most recent conversation
// first, with how many owner and Palui messages each holds.
func SearchChats(q db.Q, s Search) ([]Hit, error) {
	var where []string
	var args []any
	if text := strx.Trim(s.Query); text != "" {
		p := likeArg(text)
		where = append(where, `(title like ? escape '\' or external_id like ? escape '\' or id like ? escape '\')`)
		args = append(args, p, p, p)
	}
	if s.Channel != "" {
		where = append(where, "channel = ?")
		args = append(args, s.Channel)
	}
	if s.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, s.Kind)
	}
	if s.Scope != nil {
		where = append(where, "("+s.Scope.SQL+")")
		args = append(args, s.Scope.Args...)
	}
	limit := SearchMax
	if s.Limit != 0 {
		limit = clamp(s.Limit, 1, SearchMax)
	}
	query := "SELECT " + db.ChatCols + `, (select count(*) from "messages" m where m."chat_id" = "chats"."id" and m."role" in ('user', 'assistant')) FROM chats`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY last_message_at is null, last_message_at desc, created_at desc LIMIT ?"
	args = append(args, limit)
	rows, err := q.Query(query, args...)
	return db.All(rows, err, func(s db.Scanner) (Hit, error) {
		var h Hit
		c := &h.Chat
		err := s.Scan(append(db.ChatDest(c), &h.MessageCount)...)
		return h, err
	})
}

type MessageQuery struct {
	Limit int
	// Only messages strictly before this instant (UTC ms).
	Before *int64
	// Only messages at or after this instant (UTC ms).
	Since *int64
	// Case-insensitive substring of the message text.
	Query string
	// Query also matches the text or media label of the message a row
	// quotes (a reply to a contact card names the contact).
	Quotes bool
	// Only messages older than this one, in read order (created_at, then id),
	// for paging back without skipping messages that share a millisecond.
	BeforeMessage *db.Message
}

const (
	ReadDefault = 30
	ReadMax     = 100
)

// ReadMessages returns the newest owner and Palui messages of a chat that
// match, oldest first.
func ReadMessages(q db.Q, chatID string, mq MessageQuery) ([]db.Message, error) {
	where := []string{"chat_id = ?", "role in (?, ?)"}
	args := []any{chatID, "user", "assistant"}
	if mq.Before != nil {
		where = append(where, "created_at < ?")
		args = append(args, *mq.Before)
	}
	if mq.Since != nil {
		where = append(where, "created_at >= ?")
		args = append(args, *mq.Since)
	}
	if m := mq.BeforeMessage; m != nil {
		where = append(where, "(created_at < ? or (created_at = ? and id < ?))")
		args = append(args, m.CreatedAt, m.CreatedAt, m.ID)
	}
	if text := strx.Trim(mq.Query); text != "" {
		if mq.Quotes {
			where = append(where, `(text like ? escape '\' or (json_valid(content) and (json_extract(content, '$.quoted.text') like ? escape '\' or json_extract(content, '$.quoted.media') like ? escape '\')))`)
			args = append(args, likeArg(text), likeArg(text), likeArg(text))
		} else {
			where = append(where, `text like ? escape '\'`)
			args = append(args, likeArg(text))
		}
	}
	limit := ReadDefault
	if mq.Limit != 0 {
		limit = clamp(mq.Limit, 1, ReadMax)
	}
	args = append(args, limit)
	rows, err := q.Query("SELECT "+db.MessageCols+" FROM messages WHERE "+strings.Join(where, " AND ")+" ORDER BY created_at desc, id desc LIMIT ?", args...)
	out, err := db.All(rows, err, db.ScanMessage)
	if err != nil {
		return nil, err
	}
	slices.Reverse(out)
	return out, nil
}

// Resolve finds a chat by its id, or by an exact external id (WhatsApp JID,
// Telegram chat id). Several chats can share an external id across channels;
// pass the channel then.
func Resolve(q db.Q, ref, channel string) ([]db.Chat, error) {
	byID, ok, err := Get(q, ref)
	if err != nil {
		return nil, err
	}
	if ok && (channel == "" || byID.Channel == channel) {
		return []db.Chat{byID}, nil
	}
	if channel != "" {
		rows, err := q.Query("SELECT "+db.ChatCols+" FROM chats WHERE external_id = ? AND channel = ?", ref, channel)
		return db.All(rows, err, db.ScanChat)
	}
	rows, err := q.Query("SELECT "+db.ChatCols+" FROM chats WHERE external_id = ?", ref)
	return db.All(rows, err, db.ScanChat)
}

// GetMessage is one message of a chat by id.
func GetMessage(q db.Q, chatID, id string) (db.Message, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.MessageCols+" FROM messages WHERE id = ? AND chat_id = ?", id, chatID), db.ScanMessage)
}
