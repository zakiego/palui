package rpcpeople

import (
	"sort"
	"strings"

	"github.com/rivo/uniseg"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// The read-side queries of apps/app/src/server/queries.ts these pages use.

// clip is lib/format.ts clip: at most max UTF-16 units, never cutting a
// grapheme (Intl.Segmenter).
func clip(text string, max int) string {
	if strx.Len16(text) <= max {
		return text
	}
	var out strings.Builder
	n := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		seg := g.Str()
		w := strx.Len16(seg)
		if n+w > max {
			break
		}
		out.WriteString(seg)
		n += w
	}
	return out.String()
}

func titleOf(title, externalID string) string {
	if title != "" {
		return title
	}
	return externalID
}

// chatTitles: a chat's display name, its title or the channel id.
func chatTitles(q db.Q) (map[string]string, error) {
	rows, err := q.Query(`select "id", "title", "external_id" from "chats"`)
	type row struct{ id, title, ext string }
	all, err := db.All(rows, err, func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.id, &r.title, &r.ext)
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(all))
	for _, r := range all {
		out[r.id] = titleOf(r.title, r.ext)
	}
	return out, nil
}

type chatItem struct {
	ID          string  `json:"id"`
	Channel     string  `json:"channel"`
	Title       string  `json:"title"`
	Kind        string  `json:"kind"`
	Allowed     bool    `json:"allowed"`
	ReplyMode   string  `json:"replyMode"`
	Ack         bool    `json:"ack"`
	AckEmoji    string  `json:"ackEmoji"`
	LastAt      int64   `json:"lastAt"`
	Preview     string  `json:"preview"`
	PreviewRole *string `json:"previewRole"`
}

// chatList is every chat, or the ones where picks (an agent's), newest first.
func chatList(q db.Q, where string, args ...any) ([]chatItem, error) {
	query := "select " + db.ChatCols + ` from "chats"`
	if where != "" {
		query += " where " + where
	}
	rows, err := q.Query(query+` order by coalesce("chats"."last_message_at", "chats"."created_at") desc`, args...)
	list, err := db.All(rows, err, db.ScanChat)
	if err != nil {
		return nil, err
	}
	out := make([]chatItem, 0, len(list))
	for _, c := range list {
		type last struct {
			text, role string
			at         int64
		}
		l, ok, err := db.One(q.QueryRow(`select "text", "role", "created_at" from "messages" where ("messages"."chat_id" = ? and "messages"."role" in (?, ?)) order by "messages"."created_at" desc limit ?`, c.ID, "user", "assistant", 1), func(s db.Scanner) (last, error) {
			var r last
			return r, s.Scan(&r.text, &r.role, &r.at)
		})
		if err != nil {
			return nil, err
		}
		item := chatItem{ID: c.ID, Channel: c.Channel, Title: titleOf(c.Title, c.ExternalID), Kind: c.Kind, Allowed: c.Allowed, ReplyMode: c.ReplyMode, Ack: c.Ack, AckEmoji: c.AckEmoji}
		switch {
		case ok:
			item.LastAt = l.at
			item.Preview = clip(l.text, 140)
			role := l.role
			item.PreviewRole = &role
		case c.LastMessageAt != nil:
			item.LastAt = *c.LastMessageAt
		default:
			item.LastAt = c.CreatedAt
		}
		out = append(out, item)
	}
	// Sorted by the time the list shows, so it never reads out of order when
	// lastMessageAt lags the newest message.
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastAt > out[j].LastAt })
	return out, nil
}

type toolCallView struct {
	ID          string  `json:"id"`
	Tool        string  `json:"tool"`
	ConnectorID *string `json:"connectorId"`
	Class       string  `json:"class"`
	Status      string  `json:"status"`
	Ms          *int64  `json:"ms"`
	Args        string  `json:"args"`
	Result      *string `json:"result"`
}

// argsText is JSON.stringify(args, null, 2).slice(0, 2000), "" for null.
func argsText(raw db.RawJSON) string {
	if len(raw) == 0 {
		return ""
	}
	v, err := jsonx.Parse(raw)
	if err != nil || v == nil {
		return ""
	}
	return strx.Slice16(zod.Indent(v), 2000)
}

type transcriptChat struct {
	ID          string  `json:"id"`
	Channel     string  `json:"channel"`
	Title       string  `json:"title"`
	Kind        string  `json:"kind"`
	AgentID     *string `json:"agentId"`
	Allowed     bool    `json:"allowed"`
	ReplyMode   string  `json:"replyMode"`
	Ack         bool    `json:"ack"`
	AckEmoji    string  `json:"ackEmoji"`
	ConnectorID string  `json:"connectorId"`
	// Auto-save: on or off, retention in days (0 keeps), and what it holds now.
	SaveMedia     bool  `json:"saveMedia"`
	SaveMediaDays int64 `json:"saveMediaDays"`
	AutoSaved     int64 `json:"autoSaved"`
	AutoSavedSize int64 `json:"autoSavedSize"`
}

type transcriptMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Source    *string        `json:"source"`
	Sender    *string        `json:"sender"`
	Text      string         `json:"text"`
	Status    string         `json:"status"`
	CreatedAt int64          `json:"createdAt"`
	Tools     []toolCallView `json:"tools"`
}

type transcriptApproval struct {
	ID          string  `json:"id"`
	Tool        string  `json:"tool"`
	Class       string  `json:"class"`
	ArgsPreview *string `json:"argsPreview"`
	ExpiresAt   int64   `json:"expiresAt"`
}

type transcriptView struct {
	Chat      transcriptChat       `json:"chat"`
	Messages  []transcriptMessage  `json:"messages"`
	Approvals []transcriptApproval `json:"approvals"`
}

const transcriptLimit = 150

// transcript is the chat's current session (or all of it when it has none),
// at most 150 messages, oldest first. nil when the chat doesn't exist.
func transcript(q db.Q, chatID string) (*transcriptView, error) {
	chat, ok, err := db.One(q.QueryRow("select "+db.ChatCols+` from "chats" where "chats"."id" = ?`, chatID), db.ScanChat)
	if err != nil || !ok {
		return nil, err
	}
	where := `"messages"."chat_id" = ?`
	args := []any{chatID}
	if chat.SessionID != nil && *chat.SessionID != "" {
		where = `("messages"."chat_id" = ? and "messages"."session_id" = ?)`
		args = append(args, *chat.SessionID)
	}
	args = append(args, "user", "assistant", transcriptLimit)
	rows, err := q.Query("select "+db.MessageCols+` from "messages" where (`+where+` and "messages"."role" in (?, ?)) order by "messages"."created_at" desc limit ?`, args...)
	msgs, err := db.All(rows, err, db.ScanMessage)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	var runIDs []any
	seen := map[string]bool{}
	for _, m := range msgs {
		if m.RunID != nil && *m.RunID != "" && !seen[*m.RunID] {
			seen[*m.RunID] = true
			runIDs = append(runIDs, *m.RunID)
		}
	}
	byRun := map[string][]toolCallView{}
	if len(runIDs) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(runIDs)), ", ")
		rows, err := q.Query("select "+db.ToolCallCols+` from "tool_calls" where "tool_calls"."run_id" in (`+marks+`) order by "tool_calls"."created_at" asc`, runIDs...)
		calls, err := db.All(rows, err, db.ScanToolCall)
		if err != nil {
			return nil, err
		}
		for _, c := range calls {
			byRun[c.RunID] = append(byRun[c.RunID], toolCallView{ID: c.ID, Tool: c.Tool, ConnectorID: c.ConnectorID, Class: c.Class, Status: c.Status, Ms: c.Ms, Args: argsText(c.Args), Result: c.ResultPreview})
		}
	}
	shown := map[string]bool{}
	items := make([]transcriptMessage, 0, len(msgs))
	for _, m := range msgs {
		tools := []toolCallView{}
		if m.Role == "assistant" && m.RunID != nil && *m.RunID != "" && !shown[*m.RunID] {
			shown[*m.RunID] = true
			if list := byRun[*m.RunID]; list != nil {
				tools = list
			}
		}
		// Inbound webhook events are stored as user messages; the UI shows them as events.
		var source *string
		if m.SenderID != nil && strings.HasPrefix(*m.SenderID, "hook:") {
			s := "hook"
			source = &s
		}
		// Someone other than the owner in a shared chat.
		var sender *string
		if m.Role == "user" && !m.FromOwner {
			s := "?"
			if m.SenderName != nil {
				s = *m.SenderName
			} else if m.SenderID != nil {
				s = *m.SenderID
			}
			sender = &s
		}
		items = append(items, transcriptMessage{ID: m.ID, Role: m.Role, Source: source, Sender: sender, Text: m.Text, Status: m.Status, CreatedAt: m.CreatedAt, Tools: tools})
	}
	rows, err = q.Query("select "+db.ApprovalCols+` from "pending_approvals" where ("pending_approvals"."chat_id" = ? and "pending_approvals"."status" = ?) order by "pending_approvals"."created_at" asc`, chatID, "pending")
	pending, err := db.All(rows, err, db.ScanApproval)
	if err != nil {
		return nil, err
	}
	approvals := make([]transcriptApproval, 0, len(pending))
	for _, a := range pending {
		approvals = append(approvals, transcriptApproval{ID: a.ID, Tool: a.Tool, Class: a.ToolClass, ArgsPreview: a.ArgsPreview, ExpiresAt: a.ExpiresAt})
	}
	autoSaved, autoSize, err := attachments.AutoCount(q, chat.ID)
	if err != nil {
		return nil, err
	}
	return &transcriptView{
		Chat: transcriptChat{ID: chat.ID, Channel: chat.Channel, Title: titleOf(chat.Title, chat.ExternalID), Kind: chat.Kind, AgentID: chat.AgentID, Allowed: chat.Allowed, ReplyMode: chat.ReplyMode, Ack: chat.Ack, AckEmoji: chat.AckEmoji, ConnectorID: chat.ConnectorID,
			SaveMedia: chat.SaveMedia, SaveMediaDays: chat.SaveMediaDays, AutoSaved: autoSaved, AutoSavedSize: autoSize},
		Messages:  items,
		Approvals: approvals,
	}, nil
}
