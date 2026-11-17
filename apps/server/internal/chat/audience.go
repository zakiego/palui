package chat

// Who can read a chat (PLAN 19.4), which decides the tools a turn gets.

import (
	"context"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// GroupLookupTimeout bounds the one channel request a turn may wait on.
const GroupLookupTimeout = 3 * time.Second

// channelOf is the running channel a chat goes out on, nil for the web chat
// or when it isn't running.
func channelOf(c *EngineCtx, chat db.Chat) connectors.ChannelHandle {
	if chat.Channel == db.ChannelWeb {
		return nil
	}
	if chat.ConnectorID != "" {
		if e, ok := c.Host.Get(chat.ConnectorID); ok && e.Handle != nil {
			return e.Handle.Channel
		}
		return nil
	}
	return c.Host.ChannelFor(chat.Channel)
}

// Audience is who can read this chat (PLAN 19.4): owner, members or outside.
// Cheapest check first. Every doubt is outside: a default agent, an agent
// with no members, every Telegram group, and a WhatsApp group whose
// participant list can't be read in time. A database error is returned; a
// channel error never is.
func Audience(c *EngineCtx, chat db.Chat, agent db.Agent) (string, error) {
	q := c.DB
	shared, err := agents.IsSharedChat(q, chat)
	if err != nil {
		return tools.AudienceOutside, err
	}
	if !shared {
		return tools.AudienceOwner, nil
	}
	if agent.IsDefault {
		return tools.AudienceOutside, nil
	}
	has, err := members.HasAny(q, agent.ID)
	if err != nil || !has {
		return tools.AudienceOutside, err
	}
	if chat.Kind == "dm" {
		ok, err := members.IsMember(q, agent.ID, members.Sender{Channel: chat.Channel, ExternalID: chat.ExternalID})
		if err != nil || !ok {
			return tools.AudienceOutside, err
		}
		return tools.AudienceMembers, nil
	}
	if chat.Kind != "group" || chat.Channel != db.ChannelWhatsApp {
		return tools.AudienceOutside, nil
	}
	outside := func(reason string) {
		if c.Log != nil {
			c.Log.Info("group counted as outside", logx.Fields{"chat_id": chat.ID, "reason": reason})
		}
	}
	lister, ok := channelOf(c, chat).(connectors.GroupLister)
	if !ok {
		outside("channel can't list participants")
		return tools.AudienceOutside, nil
	}
	timeout := GroupLookupTimeout
	if c.GroupLookupTimeout > 0 {
		timeout = c.GroupLookupTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	list, err := lister.GroupParticipants(ctx, chat.ExternalID)
	if err != nil {
		outside(err.Error())
		return tools.AudienceOutside, nil
	}
	if ctx.Err() != nil {
		outside(ctx.Err().Error())
		return tools.AudienceOutside, nil
	}
	if len(list) == 0 {
		outside("empty participant list")
		return tools.AudienceOutside, nil
	}
	for _, p := range list {
		if p.ExternalID == "" && p.AltID == "" {
			outside("participant without an id")
			return tools.AudienceOutside, nil
		}
		isOwner, err := owner.IsOwner(q, owner.Sender{Channel: chat.Channel, ExternalID: p.ExternalID, AltID: p.AltID})
		if err != nil {
			return tools.AudienceOutside, err
		}
		if isOwner {
			continue
		}
		isMember, err := members.IsMember(q, agent.ID, members.Sender{Channel: chat.Channel, ExternalID: p.ExternalID, AltID: p.AltID})
		if err != nil {
			return tools.AudienceOutside, err
		}
		if !isMember {
			outside("someone in the group is not a member")
			return tools.AudienceOutside, nil
		}
	}
	return tools.AudienceMembers, nil
}

// HistoryHasOutsider reports whether the session holds a message from
// someone who is neither the owner nor a member of this agent, such as a
// stranger who has since left the group. Their text would reach a members
// turn as if a member wrote it (in the history window or folded into the
// summary), so the turn runs as outside until the session starts over. It
// reads every sender of the session, not only the loaded window, plus the
// loaded history for messages of this batch. Hook messages are left to the
// taint of their own turn.
func HistoryHasOutsider(q db.Q, chat db.Chat, agentID, sessionID string, history []db.Message) (bool, error) {
	var ids []string
	if sessionID != "" {
		rows, err := q.Query("SELECT DISTINCT sender_id FROM messages WHERE session_id = ? AND role = 'user' AND from_owner = 0 AND sender_id IS NOT NULL AND sender_id != ''", sessionID)
		if err != nil {
			return true, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return true, err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return true, err
		}
		if err := rows.Err(); err != nil {
			return true, err
		}
	}
	for _, m := range history {
		if m.Role == "user" && !m.FromOwner && m.SenderID != nil && *m.SenderID != "" {
			ids = append(ids, *m.SenderID)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || strings.HasPrefix(id, "hook:") {
			continue
		}
		seen[id] = true
		isOwner, err := owner.IsOwner(q, owner.Sender{Channel: chat.Channel, ExternalID: id})
		if err != nil {
			return true, err
		}
		if isOwner {
			continue
		}
		isMember, err := members.IsMember(q, agentID, members.Sender{Channel: chat.Channel, ExternalID: id})
		if err != nil || !isMember {
			return true, err
		}
	}
	return false, nil
}
