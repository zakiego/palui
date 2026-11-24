package rpcpeople

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/tools"
)

func TestAddSourceToolsOffersMCPCalendarAndWhatsAppTools(t *testing.T) {
	v := &proactiveView{Connectors: map[string][]proactiveConnector{
		"calendar": {{ID: "gcal", Label: "Google Calendar", Status: "on"}},
		"whatsapp": {{ID: "mcp-hub", Label: "MCP Hub", Status: "on"}, {ID: "wa-hub", Label: "wa-hub", Status: "on"}, {ID: "todo", Label: "Todo", Status: "on"}},
	}}
	hub := func(tool string) tools.CatalogTool {
		return tools.CatalogTool{ConnectorID: "mcp-hub", ConnectorLabel: "MCP Hub", ConnectorType: "mcp", Tool: tool}
	}
	addSourceTools(v, []tools.CatalogTool{
		hub("calendar__list_events"),
		hub("calendar__list_events"),
		hub("wa-personal__list_chats"),
		// Palui's own log events and chats, through the hub: not sources.
		hub("palui-agent__list_events"),
		hub("palui-agent__list_chats"),
		{ConnectorID: "wa-hub", ConnectorLabel: "wa-hub", ConnectorType: "mcp", Tool: "list_chats"},
		{ConnectorID: "todo", ConnectorLabel: "Todo", ConnectorType: "mcp", Tool: "list_todos"},
		// Another agent's connector isn't in the view's MCP list.
		{ConnectorID: "mcp-other", ConnectorLabel: "Other", ConnectorType: "mcp", Tool: "gcal_list_events"},
		{ConnectorID: "gcal", ConnectorType: "gcal", Tool: "list_events"},
	})
	cal := v.Connectors["calendar"]
	if len(cal) != 2 || cal[1] != (proactiveConnector{ID: "mcp-hub", Label: "MCP Hub", Status: "on", Tool: "calendar__list_events"}) {
		t.Fatalf("calendar: %+v", cal)
	}
	wa := v.Connectors["whatsapp"]
	want := []proactiveConnector{{ID: "mcp-hub", Label: "MCP Hub", Status: "on", Tool: "wa-personal__list_chats"}, {ID: "wa-hub", Label: "wa-hub", Status: "on"}}
	if len(wa) != 2 || wa[0] != want[0] || wa[1] != want[1] {
		t.Fatalf("whatsapp: %+v", wa)
	}
}
