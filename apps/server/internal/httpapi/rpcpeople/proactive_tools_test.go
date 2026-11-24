package rpcpeople

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/tools"
)

func TestAddCalendarToolsOffersMCPCalendarTools(t *testing.T) {
	v := &proactiveView{Connectors: map[string][]proactiveConnector{
		"calendar": {{ID: "gcal", Label: "Google Calendar", Status: "on"}},
		"whatsapp": {{ID: "mcp-hub", Label: "MCP Hub", Status: "on"}, {ID: "wa-hub", Label: "wa-hub", Status: "on"}},
	}}
	addCalendarTools(v, []tools.CatalogTool{
		{ConnectorID: "mcp-hub", ConnectorLabel: "MCP Hub", ConnectorType: "mcp", Tool: "calendar__list_events"},
		{ConnectorID: "mcp-hub", ConnectorLabel: "MCP Hub", ConnectorType: "mcp", Tool: "calendar__list_events"},
		// Palui's own log events, through the hub: not a calendar.
		{ConnectorID: "mcp-hub", ConnectorLabel: "MCP Hub", ConnectorType: "mcp", Tool: "palui-agent__list_events"},
		{ConnectorID: "wa-hub", ConnectorLabel: "wa-hub", ConnectorType: "mcp", Tool: "list_chats"},
		// Another agent's connector isn't in the view's MCP list.
		{ConnectorID: "mcp-other", ConnectorLabel: "Other", ConnectorType: "mcp", Tool: "gcal_list_events"},
		{ConnectorID: "gcal", ConnectorType: "gcal", Tool: "list_events"},
	})
	got := v.Connectors["calendar"]
	if len(got) != 2 || got[1] != (proactiveConnector{ID: "mcp-hub", Label: "MCP Hub", Status: "on", Tool: "calendar__list_events"}) {
		t.Fatalf("%+v", got)
	}
}
