package agents

// Reading down (PLAN 19.4, 19.9 step 3): Palui, the owner's own agent, may read
// the chats and memory of every other agent. No other agent reads Palui or
// another agent.

import (
	"slices"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
)

// CanReadDown says whether a turn gets the read-down tools: the default agent,
// answering the owner (mode "" or owner) in the owner's own DM or the web
// chat, on the owner's message or one of the owner's scheduled jobs. Hooks,
// flows, reminders, shared chats, other agents and anything unknown get
// nothing (fail closed).
func CanReadDown(q db.Q, turn connectors.TurnInfo, now int64) (bool, error) {
	if turn.Mode != "" && turn.Mode != "owner" {
		return false, nil
	}
	if turn.FlowID != "" {
		return false, nil
	}
	origin := turn.Origin
	if origin == "" {
		origin = turn.Trigger
	}
	if origin != "message" && origin != "schedule" {
		return false, nil
	}
	if turn.Trigger != "message" && turn.Trigger != "approval" && turn.Trigger != "schedule" {
		return false, nil
	}
	if turn.ChatID == "" {
		return false, nil
	}
	chat, ok, err := chats.Get(q, turn.ChatID)
	if err != nil || !ok {
		return false, err
	}
	shared, err := IsSharedChat(q, chat)
	if err != nil || shared {
		return false, err
	}
	var agent db.Agent
	if turn.AgentID != "" {
		a, ok, err := Get(q, turn.AgentID)
		if err != nil || !ok {
			return false, err
		}
		agent = a
	} else if agent, err = ForChat(q, chat.AgentID, now); err != nil {
		return false, err
	}
	return agent.IsDefault, nil
}

// Others is every agent but the default one, oldest first: the agents Palui
// reads down into.
func Others(q db.Q) ([]db.Agent, error) {
	rows, err := q.Query("SELECT " + db.AgentCols + " FROM agents WHERE is_default = 0 ORDER BY created_at ASC, id ASC")
	return db.All(rows, err, db.ScanAgent)
}

// Other is one agent Palui may read down into, by slug or id. ok is false for
// the default agent and for an agent that doesn't exist.
func Other(q db.Q, ref string) (db.Agent, bool, error) {
	a, ok, err := GetBySlug(q, ref)
	if err != nil {
		return db.Agent{}, false, err
	}
	if !ok {
		if a, ok, err = Get(q, ref); err != nil {
			return db.Agent{}, false, err
		}
	}
	if !ok || a.IsDefault {
		return db.Agent{}, false, nil
	}
	return a, true, nil
}

// MemoryContainers are the recall containers that hold another agent's own
// memory: its container, and with recallPerChat the per-chat containers of the
// given chats. The owner's containers are never in it, whatever the agent's
// settings say. Empty for the default agent.
func MemoryContainers(q db.Q, a db.Agent, chatIDs []string) ([]string, error) {
	if a.IsDefault {
		return []string{}, nil
	}
	owned, err := OwnerContainers(q)
	if err != nil {
		return nil, err
	}
	out := []string{}
	add := func(c *string) {
		if c != nil && *c != "" && !slices.Contains(owned, *c) && !slices.Contains(out, *c) {
			out = append(out, *c)
		}
	}
	add(Container(a, ""))
	if a.RecallPerChat {
		for _, id := range chatIDs {
			add(Container(a, id))
		}
	}
	return out, nil
}
