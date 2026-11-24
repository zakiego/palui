package proactive_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// hubEvents is what MCP Hub's calendar__list_events sends: the Google
// Calendar API shape as JSON text.
const hubEvents = `{
  "timeZone": "Asia/Makassar",
  "events": [
    {"id": "wisuda", "summary": "Wisuda Eva", "start": {"date": "2026-11-26"}, "end": {"date": "2026-11-27"}, "status": "confirmed", "location": "HBI (STKIP PGRI / UPK)"},
    {"id": "adam", "summary": "Adam x Zaki", "start": {"dateTime": "2026-11-26T13:30:00+08:00"}, "end": {"dateTime": "2026-11-26T14:00:00+08:00"}, "status": "confirmed", "meetLink": "https://meet.google.com/kej-tuxk-kao",
     "attendees": [{"email": "a@example.com", "response": "accepted"}, {"email": "me@example.com", "response": "accepted", "self": true}]},
    {"id": "teams", "summary": "Interview", "start": {"dateTime": "2026-11-26T18:30:00+08:00"}, "end": {"dateTime": "2026-11-26T19:15:00+08:00"}, "status": "confirmed", "location": "Microsoft Teams Meeting",
     "description": "Microsoft Teams meeting\nJoin: https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc%40thread.v2/0?context=x\nMeeting ID: 1"},
    {"id": "declined", "summary": "Skip me", "start": {"dateTime": "2026-11-26T09:00:00+08:00"}, "end": {"dateTime": "2026-11-26T10:00:00+08:00"}, "status": "confirmed",
     "attendees": [{"email": "me@example.com", "response": "declined", "self": true}]},
    {"id": "gone", "summary": "Cancelled", "start": {"dateTime": "2026-11-26T11:00:00+08:00"}, "end": {"dateTime": "2026-11-26T12:00:00+08:00"}, "status": "cancelled"},
    {"id": "cafe", "summary": "Kopi", "start": {"dateTime": "2026-11-26T16:00:00+08:00"}, "end": {"dateTime": "2026-11-26T17:00:00+08:00"}, "status": "confirmed", "location": "Kopi Janji Jiwa, Jl. A. Yani"}
  ]
}`

func textResult(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}

func TestParseMCPEventsReadsTheGoogleCalendarShape(t *testing.T) {
	list, err := proactive.ParseMCPEvents(textResult(hubEvents), "Asia/Makassar")
	tu.Must(t, err)
	ids := []string{}
	for _, e := range list {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "wisuda,adam,teams,cafe" {
		t.Fatalf("declined and cancelled should be left out: %v", ids)
	}
	if !list[0].AllDay || list[1].AllDay {
		t.Fatalf("all day: %+v", list[:2])
	}
	if list[1].Start != 1_795_671_000_000 || list[1].MeetLink != "https://meet.google.com/kej-tuxk-kao" {
		t.Fatalf("meet event: %+v", list[1])
	}
	if !strings.HasPrefix(list[2].MeetLink, "https://teams.microsoft.com/l/meetup-join/") || strings.ContainsAny(list[2].MeetLink, " \n") {
		t.Fatalf("teams link from the description: %q", list[2].MeetLink)
	}

	cands := proactive.CalendarCandidates(list, *proactive.DefaultCalendar("hub"))
	kinds := map[string]string{}
	for _, c := range cands {
		if c.Kind != proactive.KindCalEvent {
			kinds[c.Snapshot.EventID] = c.Kind
		}
	}
	if kinds["teams"] != proactive.KindCalStart {
		t.Fatalf("a Teams meeting is online, not a place to leave for: %v", kinds)
	}
	if kinds["cafe"] != proactive.KindCalDepart || kinds["adam"] != proactive.KindCalStart || kinds["wisuda"] != "" {
		t.Fatalf("%v", kinds)
	}
}

func TestParseMCPEventsRefusesWhatIsntACalendar(t *testing.T) {
	if _, err := proactive.ParseMCPEvents(textResult("12 log events"), "UTC"); !errors.Is(err, proactive.ErrNoEvents) {
		t.Fatalf("text: %v", err)
	}
	if _, err := proactive.ParseMCPEvents(textResult(`{"items": []}`), "UTC"); !errors.Is(err, proactive.ErrNoEvents) {
		t.Fatalf("other JSON: %v", err)
	}
	if _, err := proactive.ParseMCPEvents(map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Calendar not linked"}}}, "UTC"); err == nil || err.Error() != "Calendar not linked" {
		t.Fatalf("tool error: %v", err)
	}
	list, err := proactive.ParseMCPEvents(map[string]any{"content": []any{}, "structuredContent": map[string]any{"events": []any{}}}, "UTC")
	if err != nil || len(list) != 0 {
		t.Fatalf("structured empty calendar: %v %v", list, err)
	}
}

func TestLiveSourcesCallsTheMCPCalendarTool(t *testing.T) {
	var gotConn, gotTool string
	var gotArgs *jsonx.Object
	src := proactive.LiveSources{CallTool: func(ctx context.Context, connectorID, tool string, args *jsonx.Object, scope *tools.CallScope) (any, error) {
		gotConn, gotTool, gotArgs = connectorID, tool, args
		return textResult(hubEvents), nil
	}}
	cal := proactive.DefaultCalendar("mcp-hub")
	cal.Tool = "calendar__list_events"
	list, err := src.Events(context.Background(), *cal, "agt_1", 1_795_622_400_000, 1_795_708_800_000, "Asia/Makassar")
	tu.Must(t, err)
	if gotConn != "mcp-hub" || gotTool != "calendar__list_events" || len(list) != 4 {
		t.Fatalf("%s %s %d", gotConn, gotTool, len(list))
	}
	b, _ := jsonx.Marshal(gotArgs)
	if string(b) != `{"timeMin":"2026-11-26T00:00:00+08:00","timeMax":"2026-11-27T00:00:00+08:00","timeZone":"Asia/Makassar"}` {
		t.Fatalf("args: %s", b)
	}
}

func TestAnMCPCalendarNeedsAnMCPConnectorAndAToolName(t *testing.T) {
	d := tu.DB(t)
	web, err := chats.EnsureWebChat(d, NOW)
	tu.Must(t, err)
	a, err := agents.GetDefault(d, NOW)
	tu.Must(t, err)
	tu.SeedConnector(t, d, "gcal-1", "tools", tu.SeedOptions{Type: "gcal"}, NOW)
	tu.SeedConnector(t, d, "mcp-hub", "tools", tu.SeedOptions{Type: "mcp"}, NOW)
	on := true
	save := func(id, tool string) error {
		cfg := proactive.Defaults()
		cfg.Calendar = proactive.DefaultCalendar(id)
		cfg.Calendar.Tool = tool
		_, _, err := proactive.Save(d, proactive.SaveInput{AgentID: a.ID, Enabled: &on, ChatID: &web.ID, Config: &cfg}, NOW)
		return err
	}
	if err := save("mcp-hub", ""); err == nil || !strings.Contains(err.Error(), "isn't a calendar connector") {
		t.Fatalf("mcp without a tool: %v", err)
	}
	if err := save("gcal-1", "calendar__list_events"); err == nil || !strings.Contains(err.Error(), "isn't a calendar connector") {
		t.Fatalf("gcal with a tool: %v", err)
	}
	if err := save("mcp-hub", "calendar list"); err == nil || !strings.Contains(err.Error(), "tool name") {
		t.Fatalf("bad tool name: %v", err)
	}
	tu.Must(t, save("mcp-hub", "calendar__list_events"))
	tu.Must(t, save("gcal-1", ""))
}

func TestLiveSourcesCallsTheWhatsAppToolBehindAHub(t *testing.T) {
	var got []string
	src := proactive.LiveSources{CallTool: func(ctx context.Context, connectorID, tool string, args *jsonx.Object, scope *tools.CallScope) (any, error) {
		got = append(got, connectorID+"/"+tool)
		return map[string]any{"content": []any{}, "structuredContent": map[string]any{"chats": []any{}}}, nil
	}}
	wa := proactive.DefaultWhatsApp("wa-hub")
	_, err := src.Waiting(context.Background(), *wa, "agt_1", NOW)
	tu.Must(t, err)
	wa = proactive.DefaultWhatsApp("mcp-hub")
	wa.Tool = "wa-personal__list_chats"
	_, err = src.Waiting(context.Background(), *wa, "agt_1", NOW)
	tu.Must(t, err)
	if strings.Join(got, ",") != "wa-hub/list_chats,mcp-hub/wa-personal__list_chats" {
		t.Fatalf("%v", got)
	}
}
