// Package ics is the calendar feed connector: read any calendar from its
// secret iCalendar link (RFC 5545), the standard every calendar service can
// publish (Google, iCloud, Outlook, Fastmail, Nextcloud). Read only, one tool.
package ics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

func feedURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "webcal" || u.Scheme == "webcals")
}

func emailList(s string) bool {
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(e)
		if e != "" && (!strings.Contains(e, "@") || strings.ContainsAny(e, " \t<>")) {
			return false
		}
	}
	return true
}

// ConfigZod is the non-secret config: the owner's addresses, to mark the
// invites they declined.
var ConfigZod = zod.Object(
	zod.F("emails", zod.String().Trim().Max(500).Refine(emailList, "Use email addresses separated by commas").Optional()),
)

// SecretZod is the feed link: anyone with it can read the calendar.
var SecretZod = []zod.Field{zod.F("url", zod.String().Trim().Min(10).Max(2000).Refine(feedURL, "Use the calendar's https:// or webcal:// address"))}

var Manifest = connectors.Manifest{
	ID:       "ics",
	Kind:     "tools",
	Name:     "Calendar feed",
	Icon:     "calendar",
	Summary:  "Read any calendar from its secret iCal link.",
	Multiple: true,
	Trust:    "external",
	Setup: []connectors.SetupStep{
		{Field: "url", Label: "iCal address", Secret: true, Placeholder: "https://calendar.google.com/calendar/ical/…/basic.ics", Help: "The calendar's private iCal link. Google: Settings, your calendar, Secret address in iCal format. iCloud: share the calendar as public. Outlook: Publish a calendar, ICS. Fastmail and Nextcloud: the subscription link."},
		{Field: "emails", Label: "Your email addresses", Optional: true, Placeholder: "you@example.com", Help: "Separated by commas. Invites you declined are marked declined."},
	},
	PauseNote:     "While paused, agents can't read this calendar.",
	RemoveWarning: "This removes the link from Palui. The calendar isn't touched.",
	Docs: &connectors.Docs{
		Notes: []string{
			"Read only: one tool, get_events, lists the events in a time range, recurring ones expanded.",
			"The feed is read at most every 5 minutes. Most services refresh their iCal link every few minutes to a few hours.",
			"Event titles and descriptions come from whoever sent the invite, so they're treated as outside text.",
		},
	},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

const (
	// CacheFor is how long a fetched feed is reused.
	CacheFor = 5 * time.Minute
	// MaxFeedBytes caps a feed.
	MaxFeedBytes = 10 << 20
	// MaxEvents caps one answer.
	MaxEvents = 200
	// MaxDays caps the window.
	MaxDays = 31
)

// Options is the runtime's injectable plumbing.
type Options struct {
	Client *http.Client
	// Unix ms; default time.Now.
	Clock func() int64
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (o Options) now() time.Time {
	if o.Clock != nil {
		return time.UnixMilli(o.Clock())
	}
	return time.Now()
}

// Feed reads one feed, with a short cache.
type Feed struct {
	URL    string
	Emails []string
	opts   Options
	mu     sync.Mutex
	body   string
	at     time.Time
}

// NewFeed is a feed reader for a link.
func NewFeed(link string, emails []string, opts Options) *Feed {
	return &Feed{URL: httpURL(link), Emails: emails, opts: opts}
}

// httpURL turns webcal:// into https://.
func httpURL(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(strings.ToLower(s), "webcals://"):
		return "https://" + s[len("webcals://"):]
	case strings.HasPrefix(strings.ToLower(s), "webcal://"):
		return "https://" + s[len("webcal://"):]
	}
	return s
}

// redact hides the secret part of a link in errors: everything after the host.
func redact(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "the calendar link"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

func (f *Feed) fetch(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.opts.now()
	if f.body != "" && now.Sub(f.at) < CacheFor {
		return f.body, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return "", fmt.Errorf("Can't use %s: %v", redact(f.URL), err)
	}
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	req.Header.Set("User-Agent", "Palui calendar feed")
	res, err := f.opts.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("Couldn't reach %s: %v", redact(f.URL), unwrapURLError(err))
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%s answered %d. The link may have been reset: copy the secret iCal address again.", redact(f.URL), res.StatusCode)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("%s answered %d", redact(f.URL), res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxFeedBytes+1))
	if err != nil {
		return "", fmt.Errorf("Couldn't read %s: %v", redact(f.URL), err)
	}
	if len(b) > MaxFeedBytes {
		return "", fmt.Errorf("The calendar is over %d MB", MaxFeedBytes>>20)
	}
	f.body, f.at = string(b), now
	return f.body, nil
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Events reads the feed's occurrences in [from, to).
func (f *Feed) Events(ctx context.Context, from, to time.Time, tz *time.Location) ([]Event, error) {
	body, err := f.fetch(ctx)
	if err != nil {
		return nil, err
	}
	return Events(body, from, to, tz, append([]string(nil), f.Emails...))
}

var getInput = zod.Object(
	zod.F("from", zod.String().Trim().Max(40).Optional().Describe("Start of the range: an ISO date-time with offset (2026-11-25T08:00:00+08:00) or a date (2026-11-25). Default: now.")),
	zod.F("to", zod.String().Trim().Max(40).Optional().Describe("End of the range, like from. Default: from plus days.")),
	zod.F("days", zod.Int().Min(1).Max(MaxDays).Optional().Describe("Length of the range in days when to is left out. Default 2.")),
)

// parseWhen reads an ISO date-time or a date in tz.
func parseWhen(s string, tz *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, tz); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, tz); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func clip(s string, n int) string {
	if strx.Len16(s) <= n {
		return s
	}
	return strx.Slice16(s, n-1) + "…"
}

func loadTZ(name string) *time.Location {
	if l, err := time.LoadLocation(name); err == nil && name != "" {
		return l
	}
	return time.UTC
}

// Tools is the tool set for one turn.
func Tools(feed *Feed, turn connectors.TurnInfo, opts Options) *connectors.ToolSet {
	set := connectors.NewToolSet()
	set.Set("get_events", connectors.PaluiTool{
		Class:     "read",
		Untrusted: true,
		Title:     "List calendar events",
		Tool: kit.Tool("List the events of this calendar in a time range, recurring events expanded, sorted by start. Each event has an id (stable per occurrence), title, start and end in the owner's time zone (a date for all-day events), location, a video call link when there is one, and declined when the owner declined it. Titles and descriptions are written by whoever sent the invite.", getInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				tz := loadTZ(turn.TZ)
				now := opts.now()
				if turn.Now != 0 {
					now = time.UnixMilli(turn.Now)
				}
				from := now.In(tz)
				if s := in.S("from"); s != "" {
					t, ok := parseWhen(s, tz)
					if !ok {
						return kit.Fail("from isn't a date like 2026-11-25 or 2026-11-25T08:00:00+08:00"), nil
					}
					from = t.In(tz)
				}
				days := int64(2)
				if n, ok := in.Int("days"); ok {
					days = n
				}
				to := from.AddDate(0, 0, int(days))
				if s := in.S("to"); s != "" {
					t, ok := parseWhen(s, tz)
					if !ok {
						return kit.Fail("to isn't a date like 2026-11-26 or 2026-11-26T08:00:00+08:00"), nil
					}
					to = t.In(tz)
				}
				if !to.After(from) {
					return kit.Fail("to must be after from"), nil
				}
				if to.Sub(from) > MaxDays*24*time.Hour {
					to = from.AddDate(0, 0, MaxDays)
				}
				list, err := feed.Events(ctx, from, to, tz)
				if err != nil {
					return kit.Fail(err.Error()), nil
				}
				truncated := len(list) > MaxEvents
				if truncated {
					list = list[:MaxEvents]
				}
				events := make([]any, 0, len(list))
				for _, e := range list {
					start, end := e.Start.In(tz).Format(time.RFC3339), e.End.In(tz).Format(time.RFC3339)
					if e.AllDay {
						start, end = e.Start.Format("2006-01-02"), e.End.Format("2006-01-02")
					}
					o := kit.O("id", e.ID, "title", clip(e.Title, 300), "start", start, "end", end, "allDay", e.AllDay)
					if e.Location != "" {
						o.Set("location", clip(e.Location, 300))
					}
					if e.URL != "" {
						o.Set("link", clip(e.URL, 1000))
					}
					if e.Description != "" {
						o.Set("description", clip(e.Description, 500))
					}
					if e.Status != "" && e.Status != "confirmed" {
						o.Set("status", e.Status)
					}
					if e.Declined {
						o.Set("declined", true)
					}
					if e.Recurring {
						o.Set("recurring", true)
					}
					events = append(events, o)
				}
				return kit.O("range", kit.O("from", from.Format(time.RFC3339), "to", to.Format(time.RFC3339), "tz", tz.String()), "count", len(events), "truncated", truncated, "events", events), nil
			}),
	})
	return set
}

// config reads the link and the owner's addresses.
func config(c connectors.Context) (string, []string, error) {
	link := c.Secret("url")
	if !feedURL(link) {
		return "", nil, errors.New("Add the calendar's iCal address first.")
	}
	var emails []string
	if c.Config != nil {
		if v, ok := c.Config.Get("emails"); ok {
			if s, ok := v.(string); ok {
				for _, e := range strings.Split(s, ",") {
					if e = strings.TrimSpace(e); e != "" {
						emails = append(emails, e)
					}
				}
			}
		}
	}
	return link, emails, nil
}

// New is the runtime with injectable plumbing.
func New(opts Options) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			link, emails, err := config(c)
			if err != nil {
				return connectors.TestResult{OK: false, Detail: err.Error()}, nil
			}
			ctx := c.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			feed := NewFeed(link, emails, opts)
			now := opts.now()
			list, err := feed.Events(ctx, now, now.AddDate(0, 0, 7), time.UTC)
			if err != nil {
				return connectors.TestResult{OK: false, Detail: err.Error()}, nil
			}
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("Read the calendar: %d event%s in the next 7 days", len(list), kit.Plural(len(list)))}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			link, emails, err := config(c)
			if err != nil {
				return nil, err
			}
			feed := NewFeed(link, emails, opts)
			return &connectors.RunningConnector{
				Stop: func(context.Context) error { return nil },
				Tools: func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
					return Tools(feed, turn, opts), nil
				},
			}, nil
		},
	}
}

// Runtime is the calendar feed connector runtime.
var Runtime = New(Options{})
