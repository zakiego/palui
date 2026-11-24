package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// Snapshot is what a source showed about one thing, kept on the nudge to
// render it and to notice a change (RFC 4.2). Fields from other people
// (titles, locations, names, previews) are untrusted text.
type Snapshot struct {
	// Calendar.
	EventID  string `json:"eventId,omitempty"`
	Title    string `json:"title,omitempty"`
	Start    int64  `json:"start,omitempty"`
	End      int64  `json:"end,omitempty"`
	AllDay   bool   `json:"allDay,omitempty"`
	Location string `json:"location,omitempty"`
	MeetLink string `json:"meetLink,omitempty"`
	// The start and location before a change (cal_changed).
	PrevStart    int64  `json:"prevStart,omitempty"`
	PrevLocation string `json:"prevLocation,omitempty"`
	// WhatsApp.
	ChatID        string `json:"chatId,omitempty"`
	Name          string `json:"name,omitempty"`
	AwaitingSince int64  `json:"awaitingSince,omitempty"`
	Count         int64  `json:"count,omitempty"`
	Preview       string `json:"preview,omitempty"`
	Escalation    bool   `json:"escalation,omitempty"`
	// Summaries: the date they cover ("2026-11-24").
	Date string `json:"date,omitempty"`
}

// Candidate is something a source found this tick (RFC 3).
type Candidate struct {
	Key     string
	Kind    string
	Subject string
	// Timed nudges: when to send. Zero for now nudges.
	WakeAt int64
	// Calendar: the event start, after which a timed nudge is stale.
	Snapshot Snapshot
}

// Event is one calendar event, from the gcal events action or an MCP tool.
type Event struct {
	ID       string
	Title    string
	Start    int64
	End      int64
	AllDay   bool
	Location string
	MeetLink string
}

// WaitingChat is one wa-hub chat waiting for a reply.
type WaitingChat struct {
	ID            string
	Name          string
	Group         bool
	AwaitingSince int64
	Count         int64
	Preview       string
}

// Sources is how the engine reads the outside world. Tests fake it.
type Sources interface {
	Events(ctx context.Context, cal CalendarSource, agentID string, from, to int64, tz string) ([]Event, error)
	Waiting(ctx context.Context, wa WhatsAppSource, agentID string, since int64) ([]WaitingChat, error)
}

// LiveSources reads through the supervisor's running connectors.
type LiveSources struct {
	Running  func() []connectors.RunningEntry
	CallTool tools.ToolCaller
}

func (s LiveSources) entry(id string) (*connectors.RunningEntry, error) {
	if s.Running != nil {
		for _, e := range s.Running() {
			if e.ID == id {
				e := e
				return &e, nil
			}
		}
	}
	return nil, fmt.Errorf("Connector %s isn't running. Turn it on in Connectors.", id)
}

// Events calls the gcal connector's events action, or the MCP tool cal.Tool.
func (s LiveSources) Events(ctx context.Context, cal CalendarSource, agentID string, from, to int64, tz string) ([]Event, error) {
	if cal.Tool != "" {
		return s.mcpEvents(ctx, cal, agentID, from, to, tz)
	}
	e, err := s.entry(cal.ConnectorID)
	if err != nil {
		return nil, err
	}
	if e.Handle == nil || e.Handle.Action == nil {
		return nil, fmt.Errorf("Connector %s can't list events", cal.ConnectorID)
	}
	in := jsonx.NewMap[any]()
	in.Set("from", from)
	in.Set("to", to)
	out, err := e.Handle.Action(ctx, "events", in)
	if err != nil {
		return nil, err
	}
	return ParseEvents(out, tz)
}

// ParseEvents reads the events action's {events: [...]}.
func ParseEvents(out *jsonx.Object, tz string) ([]Event, error) {
	b, err := jsonx.Marshal(out)
	if err != nil {
		return nil, err
	}
	var body struct {
		Events []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Start    string `json:"start"`
			End      string `json:"end"`
			AllDay   bool   `json:"allDay"`
			Location string `json:"location"`
			MeetLink string `json:"meetLink"`
			Declined bool   `json:"declined"`
		} `json:"events"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, err
	}
	loc := loadTZ(tz)
	var list []Event
	for _, e := range body.Events {
		if e.Declined || e.ID == "" {
			continue
		}
		start, ok1 := parseWhen(e.Start, loc)
		end, ok2 := parseWhen(e.End, loc)
		if !ok1 {
			continue
		}
		if !ok2 {
			end = start
		}
		list = append(list, Event{ID: e.ID, Title: e.Title, Start: start, End: end, AllDay: e.AllDay, Location: e.Location, MeetLink: e.MeetLink})
	}
	return list, nil
}

// mcpEvents calls a Google Calendar style list_events tool over MCP. The tool
// caller refuses it until the owner classifies it read.
func (s LiveSources) mcpEvents(ctx context.Context, cal CalendarSource, agentID string, from, to int64, tz string) ([]Event, error) {
	if s.CallTool == nil {
		return nil, errors.New("No tool caller is wired")
	}
	loc := loadTZ(tz)
	args := jsonx.NewMap[any]()
	args.Set("timeMin", time.UnixMilli(from).In(loc).Format(time.RFC3339))
	args.Set("timeMax", time.UnixMilli(to).In(loc).Format(time.RFC3339))
	args.Set("timeZone", loc.String())
	out, err := s.CallTool(ctx, cal.ConnectorID, cal.Tool, args, &tools.CallScope{AgentID: agentID})
	if err != nil {
		return nil, err
	}
	return ParseMCPEvents(out, tz)
}

// ErrNoEvents is the refusal for a calendar tool whose result has no events
// list, so a wrong tool isn't read as an empty calendar.
var ErrNoEvents = errors.New("The calendar tool didn't return an events list. Pick a Google Calendar list_events tool.")

// mcpEventTime is a Google Calendar start or end: dateTime, or date for an
// all-day event.
type mcpEventTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

// ParseMCPEvents reads a list_events MCP result in the Google Calendar API
// shape: {events: [...]} as structured content or as JSON text. Cancelled
// events and events the owner declined are left out.
func ParseMCPEvents(out any, tz string) ([]Event, error) {
	b, err := jsonx.Marshal(out)
	if err != nil {
		return nil, err
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	if res.IsError {
		msg := "The calendar tool refused the call"
		for _, c := range res.Content {
			if c.Type == "text" && c.Text != "" {
				msg = c.Text
				break
			}
		}
		return nil, errors.New(clip(msg, 300))
	}
	var body struct {
		Events *[]struct {
			ID          string       `json:"id"`
			Summary     string       `json:"summary"`
			Start       mcpEventTime `json:"start"`
			End         mcpEventTime `json:"end"`
			Status      string       `json:"status"`
			Location    string       `json:"location"`
			Description string       `json:"description"`
			MeetLink    string       `json:"meetLink"`
			HangoutLink string       `json:"hangoutLink"`
			Attendees   []struct {
				Self           bool   `json:"self"`
				Response       string `json:"response"`
				ResponseStatus string `json:"responseStatus"`
			} `json:"attendees"`
		} `json:"events"`
	}
	parsed := false
	if len(res.Structured) > 0 && string(res.Structured) != "null" {
		parsed = json.Unmarshal(res.Structured, &body) == nil && body.Events != nil
	}
	for i := 0; !parsed && i < len(res.Content); i++ {
		if c := res.Content[i]; c.Type == "text" {
			parsed = json.Unmarshal([]byte(c.Text), &body) == nil && body.Events != nil
		}
	}
	if !parsed {
		return nil, ErrNoEvents
	}
	loc := loadTZ(tz)
	list := []Event{}
	for _, e := range *body.Events {
		if e.ID == "" || e.Status == "cancelled" {
			continue
		}
		declined := false
		for _, a := range e.Attendees {
			declined = declined || (a.Self && (a.Response == "declined" || a.ResponseStatus == "declined"))
		}
		if declined {
			continue
		}
		allDay := e.Start.DateTime == "" && e.Start.Date != ""
		start, ok := parseWhen(e.Start.DateTime+e.Start.Date, loc)
		if !ok {
			continue
		}
		end, ok := parseWhen(e.End.DateTime+e.End.Date, loc)
		if !ok {
			end = start
		}
		link := e.MeetLink
		if link == "" {
			link = e.HangoutLink
		}
		if link == "" {
			link = joinLinkIn(e.Description)
		}
		list = append(list, Event{ID: e.ID, Title: e.Summary, Start: start, End: end, AllDay: allDay, Location: e.Location, MeetLink: link})
	}
	return list, nil
}

// joinLinkRe finds a video call link in an event description (Teams and Zoom
// invites put it there instead of in a conference field).
var joinLinkRe = regexp.MustCompile(`https://(?:meet\.google\.com|(?:[a-z0-9-]+\.)?zoom\.us|teams\.microsoft\.com|teams\.live\.com)/[^\s<>"')\]]+`)

func joinLinkIn(text string) string {
	link := joinLinkRe.FindString(text)
	if len(link) > 1000 {
		return ""
	}
	return link
}

func loadTZ(tz string) *time.Location {
	if l, err := time.LoadLocation(tz); err == nil && tz != "" {
		return l
	}
	return time.UTC
}

func parseWhen(s string, loc *time.Location) (int64, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli(), true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t.UnixMilli(), true
	}
	return 0, false
}

// Waiting calls wa-hub's list_chats or wa.Tool (tab unanswered, since) and reads its
// structured content. A wa-hub without it is refused: parsing its text would
// break on a wording change (RFC decision 1).
func (s LiveSources) Waiting(ctx context.Context, wa WhatsAppSource, agentID string, since int64) ([]WaitingChat, error) {
	if s.CallTool == nil {
		return nil, errors.New("No tool caller is wired")
	}
	args := jsonx.NewMap[any]()
	args.Set("account", wa.Account)
	args.Set("tab", "unanswered")
	args.Set("limit", 100)
	args.Set("since", since)
	tool := wa.Tool
	if tool == "" {
		tool = "list_chats"
	}
	out, err := s.CallTool(ctx, wa.ConnectorID, tool, args, &tools.CallScope{AgentID: agentID})
	if err != nil {
		return nil, err
	}
	return ParseWaiting(out)
}

// ErrNoStructured is the refusal for a wa-hub too old to send JSON.
var ErrNoStructured = errors.New("wa-hub didn't send structured chats. Update wa-hub to a version whose list_chats returns structuredContent.")

// ParseWaiting reads a list_chats MCP result.
func ParseWaiting(out any) ([]WaitingChat, error) {
	b, err := jsonx.Marshal(out)
	if err != nil {
		return nil, err
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured *struct {
			Chats []struct {
				ID            json.Number `json:"id"`
				Kind          string      `json:"kind"`
				Name          string      `json:"name"`
				AwaitingSince int64       `json:"awaitingSince"`
				Unanswered    int64       `json:"unansweredCount"`
				Preview       string      `json:"preview"`
			} `json:"chats"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	if res.IsError {
		msg := "wa-hub refused the call"
		for _, c := range res.Content {
			if c.Type == "text" && c.Text != "" {
				msg = c.Text
				break
			}
		}
		return nil, errors.New(clip(msg, 300))
	}
	if res.Structured == nil {
		return nil, ErrNoStructured
	}
	list := []WaitingChat{}
	for _, c := range res.Structured.Chats {
		id := c.ID.String()
		if id == "" || c.AwaitingSince == 0 {
			continue
		}
		list = append(list, WaitingChat{ID: id, Name: c.Name, Group: c.Kind == "group", AwaitingSince: c.AwaitingSince, Count: c.Unanswered, Preview: c.Preview})
	}
	return list, nil
}

var urlRe = regexp.MustCompile(`(?i)^https?://\S+$`)

// videoLabelRe is a location that only names a video call service.
var videoLabelRe = regexp.MustCompile(`(?i)^(microsoft teams( meeting)?|teams meeting|zoom( meeting)?|google meet|online|virtual)$`)

// isPhysical: a location someone travels to (not empty, not a link, not a
// video call label like "Microsoft Teams Meeting").
func isPhysical(loc string) bool {
	l := strings.TrimSpace(loc)
	if l == "" || urlRe.MatchString(l) || videoLabelRe.MatchString(l) {
		return false
	}
	if u, err := url.Parse(l); err == nil && u.Scheme != "" && u.Host != "" {
		return false
	}
	return true
}

// onlineLink is an event's join link: the conference link, or a location that
// is a URL.
func onlineLink(e Event) string {
	if e.MeetLink != "" {
		return e.MeetLink
	}
	if l := strings.TrimSpace(e.Location); urlRe.MatchString(l) {
		return l
	}
	return ""
}

// CalendarCandidates turns events into timed nudges (RFC 5.1) plus one
// cal_event tracking row each.
func CalendarCandidates(events []Event, cal CalendarSource) []Candidate {
	var out []Candidate
	for _, e := range events {
		snap := Snapshot{EventID: e.ID, Title: e.Title, Start: e.Start, End: e.End, AllDay: e.AllDay, Location: e.Location, MeetLink: onlineLink(e)}
		out = append(out, Candidate{Key: "cal:" + e.ID, Kind: KindCalEvent, Subject: "cal:event:" + e.ID, Snapshot: snap})
		if e.AllDay {
			continue
		}
		if isPhysical(e.Location) {
			out = append(out, Candidate{Key: "cal:" + e.ID + ":depart", Kind: KindCalDepart, Subject: "cal:event:" + e.ID, WakeAt: e.Start - int64(cal.DepartLeadMin)*60_000, Snapshot: snap})
		} else if snap.MeetLink != "" {
			out = append(out, Candidate{Key: "cal:" + e.ID + ":start", Kind: KindCalStart, Subject: "cal:event:" + e.ID, WakeAt: e.Start - int64(cal.OnlineLeadMin)*60_000, Snapshot: snap})
		}
	}
	return out
}

// WhatsAppCandidates applies the fixed rules (RFC 5.2): groups out when
// asked, waits shorter than WaitHours out. One wait is one nudge: the key is
// the moment the wait began, so a second message in the same wait isn't a new
// nudge, and a new wait after a reply is.
func WhatsAppCandidates(chats []WaitingChat, wa WhatsAppSource, now int64) []Candidate {
	var out []Candidate
	minWait := int64(wa.WaitHours) * 3600_000
	oldest := now - int64(wa.MaxAgeDays)*24*3600_000
	for _, c := range chats {
		if wa.SkipGroups && c.Group {
			continue
		}
		if c.AwaitingSince < oldest || now-c.AwaitingSince < minWait {
			continue
		}
		out = append(out, Candidate{
			Key:     "wa:" + c.ID + ":" + strconv.FormatInt(c.AwaitingSince, 10),
			Kind:    KindWAWaiting,
			Subject: "wa:chat:" + c.ID,
			Snapshot: Snapshot{
				ChatID: c.ID, Name: c.Name, AwaitingSince: c.AwaitingSince, Count: c.Count, Preview: clip(c.Preview, 200),
			},
		})
	}
	return out
}

// ProfileQuery is what the proactive engine asks memory about the owner.
const ProfileQuery = "people the owner cares about and who matters to them: family, close friends, work and clients"

// MemoryProfile reads an agent's memory profile through the first running
// memory connector ("" when there is none or it fails).
func MemoryProfile(running func() []connectors.RunningEntry) func(ctx context.Context, a db.Agent, chatID string) string {
	return func(ctx context.Context, a db.Agent, chatID string) string {
		if running == nil {
			return ""
		}
		for _, e := range running() {
			if e.Handle == nil || e.Handle.Memory == nil || e.Handle.Memory.Recall == nil {
				continue
			}
			text, ok, err := e.Handle.Memory.Recall(ctx, ProfileQuery, connectors.RecallOptions{TimeoutMs: 5000, Container: agents.Container(a, chatID)})
			if err != nil || !ok {
				return ""
			}
			return text
		}
		return ""
	}
}
