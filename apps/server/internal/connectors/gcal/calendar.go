package gcal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

// Event is CalendarEvent: Start/End are RFC 3339 for timed events,
// YYYY-MM-DD for all-day ones.
type Event struct {
	ID         string
	CalendarID string
	Title      string
	Start      string
	End        string
	AllDay     bool
	Location   string
	Status     string
	Link       string
}

// JSON is the CalendarEvent object.
func (e Event) JSON() *jsonx.Object {
	o := obj("id", e.ID, "calendarId", e.CalendarID, "title", e.Title, "start", e.Start, "end", e.End, "allDay", e.AllDay)
	if e.Location != "" {
		o.Set("location", e.Location)
	}
	if e.Status != "" {
		o.Set("status", e.Status)
	}
	if e.Link != "" {
		o.Set("link", e.Link)
	}
	return o
}

type rawEvent struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"`
	Summary  string  `json:"summary"`
	Location string  `json:"location"`
	HTMLLink string  `json:"htmlLink"`
	Start    *evTime `json:"start"`
	End      *evTime `json:"end"`
}

type evTime struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
}

// ListEventsInput is listEvents' input.
type ListEventsInput struct {
	AccessToken string
	CalendarID  string
	TimeMin     string
	TimeMax     string
	// 0 is 50.
	MaxResults int
}

// ListEvents is listEvents: cancelled events dropped, all-day ones marked.
func ListEvents(ctx context.Context, in ListEventsInput, hc *http.Client) ([]Event, error) {
	max := in.MaxResults
	if max == 0 {
		max = 50
	}
	params := formEncode([][2]string{{"timeMin", in.TimeMin}, {"timeMax", in.TimeMax}, {"singleEvents", "true"}, {"orderBy", "startTime"}, {"maxResults", strconv.Itoa(max)}})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/calendars/%s/events?%s", CalendarAPI, url.PathEscape(in.CalendarID), params), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", "Bearer "+in.AccessToken)
	req.Header.Set("accept", "application/json")
	res, err := client(hc).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var body struct {
			Error *struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &body)
		msg := http.StatusText(res.StatusCode)
		code := fmt.Sprintf("http_%d", res.StatusCode)
		if body.Error != nil {
			if body.Error.Message != "" {
				msg = body.Error.Message
			}
			if body.Error.Status != "" {
				code = body.Error.Status
			}
		}
		if res.StatusCode == 404 {
			return nil, &GoogleError{404, "not_found", fmt.Sprintf("Calendar %s wasn't found", in.CalendarID)}
		}
		return nil, &GoogleError{res.StatusCode, code, fmt.Sprintf("Google Calendar answered %d: %s", res.StatusCode, msg)}
	}
	var body struct {
		Items []rawEvent `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	out := []Event{}
	for _, e := range body.Items {
		if e.Status == "cancelled" {
			continue
		}
		title := strx.Trim(e.Summary)
		if title == "" {
			title = "(no title)"
		}
		pick := func(t *evTime) string {
			if t == nil {
				return ""
			}
			if t.DateTime != "" {
				return t.DateTime
			}
			return t.Date
		}
		out = append(out, Event{ID: e.ID, CalendarID: in.CalendarID, Title: title, Start: pick(e.Start), End: pick(e.End), AllDay: e.Start == nil || e.Start.DateTime == "", Location: e.Location, Status: e.Status, Link: e.HTMLLink})
	}
	return out, nil
}

// Tokens caches the access token until a minute before it expires.
type TokenCache struct {
	ClientID, ClientSecret, RefreshToken string
	Client                               *http.Client
	mu                                   sync.Mutex
	token                                string
	expiresAt                            int64
}

// Get returns a fresh access token.
func (t *TokenCache) Get(ctx context.Context, now int64) (string, error) {
	t.mu.Lock()
	if t.token != "" && t.expiresAt-60_000 > now {
		tok := t.token
		t.mu.Unlock()
		return tok, nil
	}
	t.mu.Unlock()
	tok, exp, err := RefreshAccessToken(ctx, t.ClientID, t.ClientSecret, t.RefreshToken, t.Client, now)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.token, t.expiresAt = tok, exp
	t.mu.Unlock()
	return tok, nil
}

// Clear drops the cached token.
func (t *TokenCache) Clear() {
	t.mu.Lock()
	t.token, t.expiresAt = "", 0
	t.mu.Unlock()
}

// MaxRangeDays caps a range.
const MaxRangeDays = 31

var (
	dateOnlyRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	isoRe      = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2})(?::(\d{2})(?::(\d{2})(?:[.,](\d{1,9}))?)?)?)?(Z|[+-]\d{2}(?::?\d{2})?)?$`)
)

// point is a luxon DateTime: an instant shown in a zone.
type point struct {
	ms  int64
	loc *time.Location
}

// fromISO is DateTime.fromISO(s, { zone }): an offset is kept as the
// instant, a wall time is read in the zone. ok false when invalid.
func fromISO(s string, loc *time.Location) (int64, bool) {
	m := isoRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n := func(x string) int { v, _ := strconv.Atoi(x); return v }
	p := zone.Parts{Year: n(m[1]), Month: n(m[2]), Day: n(m[3]), Hour: n(m[4]), Minute: n(m[5]), Second: n(m[6])}
	if m[7] != "" {
		p.Millisecond = n((m[7] + "000")[:3])
	}
	if p.Month < 1 || p.Month > 12 || p.Day < 1 || p.Day > time.Date(p.Year, time.Month(p.Month)+1, 0, 0, 0, 0, 0, time.UTC).Day() || p.Hour > 23 || p.Minute > 59 || p.Second > 59 {
		return 0, false
	}
	if off := m[8]; off != "" {
		ts := zone.LocalTS(p)
		if off == "Z" {
			return ts, true
		}
		sign := 1
		if off[0] == '-' {
			sign = -1
		}
		digits := strings.ReplaceAll(off[1:], ":", "")
		h, mi := n(digits[:2]), 0
		if len(digits) >= 4 {
			mi = n(digits[2:4])
		}
		return ts - int64(sign*(h*60+mi))*60_000, true
	}
	return zone.FromParts(p, loc), true
}

func parsePoint(raw string, loc *time.Location) (int64, bool, bool) {
	s := strx.Trim(raw)
	if dateOnlyRe.MatchString(s) {
		ms, ok := fromISO(s, loc)
		return ms, true, ok
	}
	ms, ok := fromISO(s, loc)
	return ms, false, ok
}

var keywords = map[string][2]int{
	"today": {0, 1}, "hari_ini": {0, 1},
	"tomorrow": {1, 2}, "besok": {1, 2},
	"week": {0, 7}, "minggu_ini": {0, 7},
}

var spaceRunRe = regexp.MustCompile(`\s+`)

// Range is resolveRange's answer, as instants.
type Range struct {
	Min, Max int64
	loc      *time.Location
	TZ       string
}

// ResolveRange is resolveRange: from/to are ISO dates or datetimes, or today /
// tomorrow / week (hari ini / besok / minggu ini). Missing from is today; a
// date without to covers that whole day; a date-only to is inclusive.
func ResolveRange(from, to *string, tz string, now int64) (Range, error) {
	loc := zone.MustLoad(tz)
	today := zone.StartOfDay(now, loc)
	f := "today"
	if from != nil {
		f = *from
	}
	key := spaceRunRe.ReplaceAllString(strings.ToLower(strx.Trim(f)), "_")
	var start, end int64
	if kw, ok := keywords[key]; ok {
		start, end = zone.PlusDays(today, loc, kw[0]), zone.PlusDays(today, loc, kw[1])
	} else {
		ms, _, ok := parsePoint(f, loc)
		if !ok {
			return Range{}, fmt.Errorf("Can't read the date \"%s\". Use YYYY-MM-DD, an ISO time, today, tomorrow or week.", f)
		}
		start, end = ms, zone.PlusDays(ms, loc, 1)
	}
	if to != nil && strx.Trim(*to) != "" {
		ms, dateOnly, ok := parsePoint(*to, loc)
		if !ok {
			return Range{}, fmt.Errorf("Can't read the date \"%s\".", *to)
		}
		end = ms
		if dateOnly {
			end = zone.PlusDays(ms, loc, 1)
		}
	}
	if end <= start {
		return Range{}, fmt.Errorf("The end of the range must be after the start")
	}
	if diffDays(start, end, loc) > MaxRangeDays {
		end = zone.PlusDays(start, loc, MaxRangeDays)
	}
	return Range{Min: start, Max: end, loc: loc, TZ: tz}, nil
}

// diffDays is luxon's diff in days: calendar days plus the remaining fraction.
func diffDays(a, b int64, loc *time.Location) float64 {
	days := 0
	cur := a
	for {
		next := zone.PlusDays(a, loc, days+1)
		if next > b {
			break
		}
		days++
		cur = next
	}
	return float64(days) + float64(b-cur)/float64(zone.PlusDays(cur, loc, 1)-cur)
}

// ISO is luxon's toISO(), with suppressMilliseconds when asked.
func ISO(ms int64, tz string, suppressMs bool) string {
	s := zone.ISO(ms, tz, zone.MustLoad(tz))
	if suppressMs && ms%1000 == 0 {
		s = strings.Replace(s, ".000", "", 1)
	}
	return s
}

// UTCISO is toUTC().toISO().
func UTCISO(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

func fmtDay(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("Mon 2 Jan")
}

func fmtClock(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("15:04")
}

// PresentEvent is presentEvent: one event as the model sees it, in tz.
func PresentEvent(e Event, tz string) *jsonx.Object {
	loc := zone.MustLoad(tz)
	if e.AllDay {
		start, _ := fromISO(e.Start, loc)
		endMs, _ := fromISO(e.End, loc)
		last := zone.PlusDays(endMs, loc, -1)
		days := fmtDay(start, loc)
		if last > start {
			days = fmtDay(start, loc) + " to " + fmtDay(last, loc)
		}
		o := obj("title", e.Title, "when", days+" (all day)", "start", e.Start, "end", e.End, "allDay", true)
		if e.Location != "" {
			o.Set("location", e.Location)
		}
		o.Set("calendar", e.CalendarID)
		return o
	}
	s, _ := fromISO(e.Start, time.UTC)
	en, _ := fromISO(e.End, time.UTC)
	ps, pe := zone.PartsOf(s, loc), zone.PartsOf(en, loc)
	same := ps.Year == pe.Year && ps.Month == pe.Month && ps.Day == pe.Day
	when := fmtDay(s, loc) + " " + fmtClock(s, loc) + " to " + fmtDay(en, loc) + " " + fmtClock(en, loc)
	if same {
		when = fmtDay(s, loc) + " " + fmtClock(s, loc) + "-" + fmtClock(en, loc)
	}
	o := obj("title", e.Title, "when", when, "start", ISO(s, tz, true), "end", ISO(en, tz, true), "allDay", false)
	if e.Location != "" {
		o.Set("location", e.Location)
	}
	o.Set("calendar", e.CalendarID)
	return o
}

// SortEvents: all-day events sort at the start of their day in tz.
func SortEvents(events []Event, tz string) []Event {
	loc := zone.MustLoad(tz)
	key := func(e Event) int64 {
		if e.AllDay {
			ms, _ := fromISO(e.Start, loc)
			return ms
		}
		ms, _ := fromISO(e.Start, time.UTC)
		return ms
	}
	out := append([]Event(nil), events...)
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}
