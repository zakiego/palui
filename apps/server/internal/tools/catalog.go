package tools

// The tools an agent can be offered right now, for the agent page (PLAN 19.7),
// from tools/catalog.ts: the collection a turn in the owner's own chat runs,
// with guestSafe saying whether a shared chat's outside turn gets it too and
// memberSafe whether a members turn does.

import (
	"context"
	"math"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

type CatalogTool struct {
	// Namespaced: <connectorId>__<tool>.
	Name           string `json:"name"`
	Tool           string `json:"tool"`
	ConnectorID    string `json:"connectorId"`
	ConnectorLabel string `json:"connectorLabel"`
	// The connector's type ("mcp" for an MCP server).
	ConnectorType string `json:"connectorType"`
	Class         string `json:"class"`
	// An MCP tool the owner hasn't classified yet: Class is destructive (fail
	// closed) and this is what the server's annotations suggest. Left out once
	// a class is saved, and for every other connector.
	SuggestedClass string  `json:"suggestedClass,omitempty"`
	Title          *string `json:"title"`
	// Its results come from outside, so they taint the turn.
	Untrusted bool `json:"untrusted"`
	// An outside turn (a shared chat, PLAN 19.4) gets it too.
	GuestSafe bool `json:"guestSafe"`
	// A members turn (PLAN 19.4) gets it too. Left out when false, so rows
	// written before members read the same.
	MemberSafe bool `json:"memberSafe,omitempty"`
	// Whose data it touches: chat, agent, shared or none. Left out, it follows
	// the connector: the agent's when one agent owns it, shared otherwise.
	Data string `json:"data,omitempty"`
}

type CatalogOptions struct {
	AgentID string
	ChatID  string
	Channel string
	TZ      string
	Now     int64
	Log     *logx.Logger
}

// Catalog is toolCatalog.
func Catalog(ctx context.Context, running []connectors.RunningEntry, o CatalogOptions) []CatalogTool {
	all := Collect(ctx, running, CollectOptions{
		Turn: connectors.TurnInfo{ChatID: o.ChatID, Channel: o.Channel, Trigger: "message", TZ: o.TZ, Now: o.Now, AgentID: o.AgentID},
		// Every tool lands in Meta, active or deferred, whatever this is.
		MaxTools:          math.MaxInt,
		Log:               o.Log,
		OnUntrustedResult: func(*ToolMeta) {},
	})
	// The same collection for an outside turn: what a shared chat gets.
	outsideTurn := connectors.TurnInfo{ChatID: o.ChatID, Channel: o.Channel, Trigger: "message", TZ: o.TZ, Now: o.Now, AgentID: o.AgentID, Mode: AudienceOutside}
	outside := Collect(ctx, running, CollectOptions{
		Turn:              outsideTurn,
		MaxTools:          math.MaxInt,
		Audience:          AudienceOutside,
		Log:               o.Log,
		OnUntrustedResult: func(*ToolMeta) {},
	})
	// And for a members turn.
	membersTurn := outsideTurn
	membersTurn.Mode = AudienceMembers
	members := Collect(ctx, running, CollectOptions{
		Turn:              membersTurn,
		MaxTools:          math.MaxInt,
		Audience:          AudienceMembers,
		Log:               o.Log,
		OnUntrustedResult: func(*ToolMeta) {},
	})
	rows := map[string]db.Connector{}
	for _, e := range running {
		rows[e.ID] = e.Row
	}
	out := []CatalogTool{}
	for _, name := range all.Names() {
		m := all.Meta[name]
		if m.Name == SearchTool {
			continue
		}
		row, ok := rows[m.ConnectorID]
		label := m.ConnectorID
		typ := ""
		if ok {
			if row.Label != "" {
				label = row.Label
			}
			typ = row.Type
		}
		var title *string
		if m.Title != "" {
			t := m.Title
			title = &t
		}
		out = append(out, CatalogTool{Name: m.Name, Tool: m.Tool, ConnectorID: m.ConnectorID, ConnectorLabel: label, ConnectorType: typ, Class: m.Class, SuggestedClass: m.Suggested, Title: title, Untrusted: m.Untrusted, GuestSafe: outside.Meta[name] != nil, MemberSafe: members.Meta[name] != nil, Data: m.Data})
	}
	return out
}
