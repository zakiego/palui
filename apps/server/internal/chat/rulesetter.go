package chat

import (
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// RuleSetterFor decides whether a turn may add and remove the chat's rules,
// and under whose name. Only a fresh message turn in a members chat (every
// participant is the owner or a member, and no outsider wrote in the
// session) qualifies, and only when every message of the batch comes from
// the owner or from a member the owner let set rules. One message from
// anyone else and the turn gets no rule tools: their text could steer the
// change. The name is the newest message's sender. nil means no.
func RuleSetterFor(q db.Q, chat db.Chat, agent db.Agent, audience, origin string, resumed bool, history []db.Message, batch map[string]bool) (*connectors.RuleSetter, error) {
	if audience != tools.AudienceMembers || origin != "message" || resumed || agent.IsDefault {
		return nil, nil
	}
	var who *connectors.RuleSetter
	for _, h := range history {
		if !batch[h.ID] || h.Role != "user" {
			continue
		}
		if h.FromOwner {
			who = &connectors.RuleSetter{Name: "Owner"}
			continue
		}
		if h.SenderID == nil || *h.SenderID == "" {
			return nil, nil
		}
		m, ok, err := members.RuleSetter(q, agent.ID, members.Sender{Channel: chat.Channel, ExternalID: *h.SenderID})
		if err != nil || !ok {
			return nil, err
		}
		id := m.ID
		who = &connectors.RuleSetter{MemberID: &id, Name: m.Name}
	}
	return who, nil
}
