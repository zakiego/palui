package gcal

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// ConfigZod is the gcal manifest's config.
var ConfigZod = zod.Object(
	zod.F("calendarIds", zod.Default(zod.Array(zod.String().Trim().Min(1)).Min(1), []any{"primary"})),
	zod.F("email", zod.String().Optional()),
)

var SecretZod = []zod.Field{
	zod.F("clientId", zod.String().Trim().Regex(regexp.MustCompile(`\.apps\.googleusercontent\.com$`), `\.apps\.googleusercontent\.com$`, "Google client ids end with .apps.googleusercontent.com")),
	zod.F("clientSecret", zod.String().Trim().Min(10)),
	zod.F("refreshToken", zod.String().Min(10).Optional()),
}

var Manifest = connectors.Manifest{
	ID:      "gcal",
	Kind:    "tools",
	Name:    "Google Calendar",
	Icon:    "calendar",
	Summary: "Read your Google Calendar.",
	Setup: []connectors.SetupStep{
		{Field: "clientId", Label: "OAuth client ID", Placeholder: "1234-abc.apps.googleusercontent.com", Help: "Make a Web application client with this redirect URI, then paste its ID.", Command: "{appUrl}/api/oauth/google/callback"},
		{Field: "clientSecret", Label: "OAuth client secret", Secret: true},
		{Action: "oauth", Label: "Connect with Google"},
	},
	Trust:         "own",
	RemoveWarning: "This deletes the Google tokens. Revoke Palui at myaccount.google.com/permissions too.",
	Docs: &connectors.Docs{Notes: []string{
		"Tool: get_calendar (read). Times are shown in your timezone.",
		"Anyone can put text in your calendar by sending an invite, so calendar results taint the turn: any write after them asks you first.",
	}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

const notConnected = "Google sign-in isn't done yet. Press Connect with Google in the Calendar setup."

func explain(err error) string {
	var ge *GoogleError
	if errors.As(err, &ge) && ge.Code == "invalid_grant" {
		return "Google access was revoked or expired. Connect with Google again in the dashboard."
	}
	return err.Error()
}

// Config is the stored config.
type Config struct {
	CalendarIDs []string
	Email       string
}

// ConfigFrom reads ctx.config.
func ConfigFrom(o *jsonx.Object) Config {
	c := Config{}
	if o == nil {
		return c
	}
	if v, ok := o.Get("calendarIds"); ok {
		if list, ok := v.([]any); ok {
			for _, x := range list {
				if s, ok := x.(string); ok {
					c.CalendarIDs = append(c.CalendarIDs, s)
				}
			}
		}
	}
	if v, ok := o.Get("email"); ok {
		c.Email, _ = v.(string)
	}
	return c
}

var calendarInput = zod.Object(
	zod.F("from", zod.String().Optional().Describe(`Start: "today", "tomorrow", "week", or an ISO date/time`)),
	zod.F("to", zod.String().Optional().Describe("End (inclusive for dates). Optional")),
)

// CalendarTool is getCalendarTool.
func CalendarTool(tokens *TokenCache, cfg Config, turn connectors.TurnInfo, hc *http.Client, clock func() int64) connectors.Tool {
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	return kit.Tool("Read the owner's Google Calendar (read only). `from`/`to` are ISO dates (YYYY-MM-DD) or ISO times, or the words today, tomorrow or week. Without arguments it shows today. Times come back in the owner's timezone.", calendarInput,
		func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
			tz, now := turn.TZ, turn.Now
			var from, to *string
			if s, ok := in.Str("from"); ok {
				from = &s
			}
			if s, ok := in.Str("to"); ok {
				to = &s
			}
			r, err := ResolveRange(from, to, tz, now)
			if err != nil {
				return kit.O("error", err.Error()), nil
			}
			token, err := tokens.Get(ctx, clock())
			var all []Event
			if err == nil {
				for _, id := range cfg.CalendarIDs {
					list, lerr := ListEvents(ctx, ListEventsInput{AccessToken: token, CalendarID: id, TimeMin: UTCISO(r.Min), TimeMax: UTCISO(r.Max), MaxResults: 50}, hc)
					if lerr != nil {
						err = lerr
						break
					}
					all = append(all, list...)
				}
			}
			if err != nil {
				var ge *GoogleError
				if errors.As(err, &ge) && ge.Status == 401 {
					tokens.Clear()
				}
				return kit.O("error", explain(err)), nil
			}
			events := make([]any, 0, len(all))
			for _, e := range SortEvents(all, tz) {
				events = append(events, PresentEvent(e, tz))
			}
			return kit.O("range", kit.O("from", ISO(r.Min, tz, true), "to", ISO(r.Max, tz, true), "tz", tz), "count", len(events), "events", events), nil
		})
}

func tokensFor(c connectors.Context, hc *http.Client) *TokenCache {
	id, sec, ref := c.Secret("clientId"), c.Secret("clientSecret"), c.Secret("refreshToken")
	if id == "" || sec == "" || ref == "" {
		return nil
	}
	return &TokenCache{ClientID: id, ClientSecret: sec, RefreshToken: ref, Client: hc}
}

// Options are what tests inject.
type Options struct {
	Client *http.Client
	Clock  func() int64
}

// New is createGcalRuntime(fetch).
func New(opts Options) *connectors.Runtime {
	clock := opts.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			if c.Secret("clientId") == "" || c.Secret("clientSecret") == "" {
				return connectors.TestResult{OK: false, Detail: "Add the OAuth client ID and secret"}, nil
			}
			tokens := tokensFor(c, opts.Client)
			if tokens == nil {
				return connectors.TestResult{OK: false, Detail: notConnected}, nil
			}
			ctx := c.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			cfg := ConfigFrom(c.Config)
			token, err := tokens.Get(ctx, clock())
			if err != nil {
				return connectors.TestResult{OK: false, Detail: explain(err)}, nil
			}
			now := clock()
			calendarID := "primary"
			if len(cfg.CalendarIDs) > 0 {
				calendarID = cfg.CalendarIDs[0]
			}
			list, err := ListEvents(ctx, ListEventsInput{AccessToken: token, CalendarID: calendarID, TimeMin: UTCISO(now), TimeMax: UTCISO(now + 30*24*3600_000), MaxResults: 1}, opts.Client)
			if err != nil {
				return connectors.TestResult{OK: false, Detail: explain(err)}, nil
			}
			who := "Connected"
			if cfg.Email != "" {
				who = "Connected as " + cfg.Email
			}
			if len(list) == 0 {
				return connectors.TestResult{OK: true, Detail: who + ". No events in the next 30 days.", Data: kit.O("nextEvent", nil)}, nil
			}
			return connectors.TestResult{OK: true, Detail: who + ". Next event: " + list[0].Title, Data: kit.O("nextEvent", list[0].JSON())}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			tokens := tokensFor(c, opts.Client)
			if tokens == nil {
				return nil, errors.New(notConnected)
			}
			cfg := ConfigFrom(c.Config)
			return &connectors.RunningConnector{
				Stop: func(context.Context) error { return nil },
				Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
					set := connectors.NewToolSet()
					set.Set("get_calendar", connectors.PaluiTool{Tool: CalendarTool(tokens, cfg, turn, opts.Client, clock), Class: "read", Untrusted: true, Title: "Check the calendar"})
					return set, nil
				},
				Live: func() *jsonx.Object {
					var email any
					if cfg.Email != "" {
						email = cfg.Email
					}
					ids := make([]any, len(cfg.CalendarIDs))
					for i, x := range cfg.CalendarIDs {
						ids[i] = x
					}
					return kit.O("email", email, "calendars", ids)
				},
				Action: func(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error) {
					if name != "authUrl" {
						return nil, errors.New("Unknown action " + name)
					}
					clientID := c.Secret("clientId")
					if clientID == "" {
						return nil, errors.New("Save the OAuth client ID first")
					}
					cfgApp := config.Get()
					key, err := config.MasterKey(cfgApp)
					if err != nil {
						return nil, err
					}
					redirect := RedirectURIFor(cfgApp.AppURL)
					issued := IssueState(key, c.ID, clock())
					if c.PatchConfig != nil {
						c.PatchConfig(NonceConfig(issued))
					}
					return kit.O("url", BuildAuthURL(AuthURLInput{ClientID: clientID, RedirectURI: redirect, State: issued.State, LoginHint: cfg.Email}), "redirectUri", redirect), nil
				},
			}, nil
		},
	}
}

// Runtime is the gcal connector runtime.
var Runtime = New(Options{})
