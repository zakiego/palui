package tools

// The tools an agent can be offered right now, for the agent page (PLAN 19.7),
// from tools/catalog.ts: the collection a turn in the owner's own chat runs,
// with each tool's guestSafe flag saying whether guest turns get it too.

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
	ConnectorType string  `json:"connectorType"`
	Class         string  `json:"class"`
	Title         *string `json:"title"`
	// Its results come from outside, so they taint the turn.
	Untrusted bool `json:"untrusted"`
	GuestSafe bool `json:"guestSafe"`
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
		out = append(out, CatalogTool{Name: m.Name, Tool: m.Tool, ConnectorID: m.ConnectorID, ConnectorLabel: label, ConnectorType: typ, Class: m.Class, Title: title, Untrusted: m.Untrusted, GuestSafe: m.GuestSafe})
	}
	return out
}
