// Package reminders is the reminders connector (connectors/src/reminders):
// reminder tools for the model (PLAN 6). Every tool works on the turn's chat
// only; results carry the Indonesian echo so the model can repeat the time.
package reminders

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

var Manifest = connectors.Manifest{
	ID:        "reminders",
	Kind:      "skill",
	Name:      "Reminders",
	Icon:      "alarm-clock",
	Summary:   "Set, list, move and snooze reminders from chat.",
	Builtin:   true,
	Setup:     []connectors.SetupStep{},
	PauseNote: "Pausing hides the reminder tools. Reminders already set still fire.",
	Docs: &connectors.Docs{
		Commands: []string{"ingetin <apa> <kapan>", "remind me <when> to <what>"},
		Notes: []string{
			"Times are wall-clock times in the reminder's own zone (default from Settings, Asia/Makassar). A daily 08:00 reminder stays at 08:00 across DST changes.",
			"Repeats use RRULE. A monthly reminder on the 31st skips months that don't have one.",
			"If Palui was down when a reminder was due, it fires once on start with \"(telat, harusnya 09.00)\".",
		},
	},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

// LocalDateTimeRe is the shared YYYY-MM-DDTHH:mm(:ss) pattern of the tools.
var LocalDateTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$`)

// LocalDateTimeSource is its JS source.
const LocalDateTimeSource = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$`

// LocalDateTime is z.string().regex(..., "Use YYYY-MM-DDTHH:mm with no offset").
func LocalDateTime() *zod.StringS {
	return zod.String().Regex(LocalDateTimeRe, LocalDateTimeSource, "Use YYYY-MM-DDTHH:mm with no offset")
}

var (
	localDateTime = LocalDateTime().Describe("Wall-clock date and time in the reminder's zone, ISO 8601 without offset (YYYY-MM-DDTHH:mm). For a repeating reminder, the first occurrence.")
	rrule         = zod.Nullable(zod.String()).Optional().Describe(`RFC 5545 RRULE body without DTSTART for repeating reminders: "FREQ=DAILY", "FREQ=WEEKLY;BYDAY=MO", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "FREQ=MONTHLY;BYMONTHDAY=1", "FREQ=MONTHLY;BYDAY=-1FR" (last Friday). Omit or null for a one-off.`)
	tzField       = zod.String().Optional().Describe("IANA time zone, e.g. Asia/Makassar. Omit to use the owner's zone.")
	idField       = zod.String().Describe("Reminder id from list_reminders.")

	setInput = zod.Object(
		zod.F("text", zod.String().Min(1).Max(500).Describe("What to remind about, short, in the user's language.")),
		zod.F("localDateTime", localDateTime),
		zod.F("rrule", rrule),
		zod.F("tz", tzField),
	)
	listInput = zod.Object(
		zod.F("filter", zod.Default(zod.Enum("upcoming", "recurring", "done"), "upcoming")),
		zod.F("limit", zod.Default(zod.Int().Min(1).Max(50), float64(20))),
	)
	updateInput = zod.Object(
		zod.F("id", idField),
		zod.F("text", zod.String().Min(1).Max(500).Optional()),
		zod.F("localDateTime", localDateTime.Optional()),
		zod.F("rrule", rrule),
		zod.F("tz", tzField),
	)
	cancelInput = zod.Object(zod.F("id", idField))
	snoozeInput = zod.Object(
		zod.F("id", idField),
		zod.F("preset", zod.Enum("10m", "1h", "tomorrow").Optional()),
		zod.F("localDateTime", localDateTime.Optional().Describe("Snooze until this wall-clock time instead of a preset.")),
	)
)

func fail(err error) (any, error) {
	var re *reminders.ReminderError
	var te *reminders.TimeError
	if errors.As(err, &re) {
		return kit.Fail(re.Msg), nil
	}
	if errors.As(err, &te) {
		return kit.Fail(te.Msg), nil
	}
	return nil, err
}

func view(r db.Reminder, now int64) (any, error) {
	echo, err := reminders.ReminderEcho(r, now)
	if err != nil {
		return nil, err
	}
	var next any
	if r.Status == "pending" || r.Status == "firing" {
		next = reminders.ReminderNext(r, now)
	}
	return kit.O(
		"id", r.ID, "text", r.Text, "status", r.Status,
		"recurring", r.RRule != nil && *r.RRule != "",
		"rrule", kit.StrPtr(r.RRule), "tz", r.TZ,
		"when", echo, "next", next,
		"smart", r.SmartPrompt != nil && *r.SmartPrompt != "",
	), nil
}

// ZoneFor is zoneFor: the given zone, else the turn's when valid, else the
// owner's setting.
func ZoneFor(q db.Q, turn connectors.TurnInfo, tz string) (string, error) {
	if tz != "" {
		return tz, nil
	}
	if turn.TZ != "" && zone.Valid(turn.TZ) {
		return turn.TZ, nil
	}
	return settings.Timezone(q)
}

// Tools is reminderTools(db, turn, clock).
func Tools(d *db.DB, turn connectors.TurnInfo, clock func() int64) *connectors.ToolSet {
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	chatID := turn.ChatID
	own := func(id string) (db.Reminder, any, error) {
		r, ok, err := reminders.Get(d, id)
		if err != nil {
			return r, nil, err
		}
		if !ok || r.ChatID != chatID {
			return r, kit.Fail(fmt.Sprintf("No reminder %s in this chat. Call list_reminders to get the ids.", id)), nil
		}
		return r, nil, nil
	}
	set := connectors.NewToolSet()
	set.Set("set_reminder", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Set a reminder",
		Tool: kit.Tool("Create a reminder in this chat. Work out the local date and time yourself from the current time in the system prompt (a date with no time means 09:00). Repeat the returned `echo` to the user so a wrong time shows right away.", setInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				tz, err := ZoneFor(d, turn, in.S("tz"))
				if err != nil {
					return nil, err
				}
				r, err := reminders.Create(d, reminders.CreateInput{ChatID: chatID, Text: in.S("text"), LocalStart: in.S("localDateTime"), TZ: tz, RRule: in.S("rrule")}, now)
				if err != nil {
					return fail(err)
				}
				echo, err := reminders.ReminderEcho(r, now)
				if err != nil {
					return fail(err)
				}
				first := ""
				if r.RRule != nil && *r.RRule != "" {
					first = fmt.Sprintf(" Pertama: %s.", reminders.ReminderNext(r, now))
				}
				v, err := view(r, now)
				if err != nil {
					return fail(err)
				}
				return kit.O("ok", true, "id", r.ID, "echo", echo, "message", fmt.Sprintf("Oke, aku ingetin \"%s\": %s.%s", r.Text, echo, first), "reminder", v), nil
			}),
	})
	set.Set("list_reminders", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "List reminders",
		Tool: kit.Tool("List this chat's reminders. upcoming = pending by next fire time, recurring = repeating ones, done = finished or cancelled (latest first).", listInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				filter := in.S("filter")
				limit, _ := in.Int("limit")
				rows, err := reminders.List(d, reminders.ListOptions{Filter: reminders.Filter(filter), ChatID: chatID, Limit: int(limit)})
				if err != nil {
					return nil, err
				}
				list := make([]any, 0, len(rows))
				for _, r := range rows {
					v, err := view(r, now)
					if err != nil {
						return nil, err
					}
					list = append(list, v)
				}
				msg := "Nggak ada pengingat aktif."
				if filter == "done" {
					msg = "Belum ada pengingat yang selesai."
				}
				return kit.O("ok", true, "count", len(rows), "reminders", list, "message", kit.If(len(rows) == 0, msg)), nil
			}),
	})
	set.Set("update_reminder", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Change a reminder",
		Tool: kit.Tool("Change a reminder's text, time, repeat rule or zone. Changing the time reschedules it. Pass rrule null to make it a one-off.", updateInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				id := in.S("id")
				if _, refusal, err := own(id); err != nil || refusal != nil {
					return refusal, err
				}
				p := reminders.Patch{}
				if s, ok := in.Str("text"); ok {
					p.Text = &s
				}
				if s, ok := in.Str("localDateTime"); ok {
					p.LocalStart = &s
				}
				if in.Has("rrule") {
					s := in.S("rrule")
					p.RRule = &s
				}
				if s, ok := in.Str("tz"); ok {
					p.TZ = &s
				}
				r, err := reminders.Update(d, id, p, now)
				if err != nil {
					return fail(err)
				}
				echo, err := reminders.ReminderEcho(r, now)
				if err != nil {
					return fail(err)
				}
				v, err := view(r, now)
				if err != nil {
					return fail(err)
				}
				return kit.O("ok", true, "echo", echo, "message", fmt.Sprintf("Sip, pengingat \"%s\" sekarang %s.", r.Text, echo), "reminder", v), nil
			}),
	})
	set.Set("cancel_reminder", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Cancel a reminder",
		Tool: kit.Tool("Cancel a reminder (a repeating one stops for good). It stays in the done list.", cancelInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				id := in.S("id")
				r, refusal, err := own(id)
				if err != nil || refusal != nil {
					return refusal, err
				}
				if r.Status == "cancelled" {
					return kit.O("ok", true, "message", fmt.Sprintf("Pengingat \"%s\" memang udah dibatalin.", r.Text)), nil
				}
				r, err = reminders.Cancel(d, id, now)
				if err != nil {
					return nil, err
				}
				v, err := view(r, now)
				if err != nil {
					return nil, err
				}
				return kit.O("ok", true, "message", fmt.Sprintf("Oke, pengingat \"%s\" dibatalin.", r.Text), "reminder", v), nil
			}),
	})
	set.Set("snooze_reminder", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Snooze a reminder",
		Tool: kit.Tool("Snooze a reminder: preset 10m, 1h or tomorrow (tomorrow 09:00 in the reminder's zone), or an exact localDateTime. A repeating reminder keeps its rule afterwards.", snoozeInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				id := in.S("id")
				r, refusal, err := own(id)
				if err != nil || refusal != nil {
					return refusal, err
				}
				preset, until := in.S("preset"), in.S("localDateTime")
				if preset == "" && until == "" {
					return kit.Fail("Give a preset (10m, 1h, tomorrow) or a localDateTime."), nil
				}
				sn := reminders.Snooze{Preset: preset}
				if until != "" {
					at, err := reminders.LocalToUTC(until, r.TZ)
					if err != nil {
						return fail(err)
					}
					sn = reminders.Snooze{Until: at}
				}
				r, err = reminders.SnoozeReminder(d, id, sn, now)
				if err != nil {
					return fail(err)
				}
				when := strings.TrimPrefix(reminders.ReminderNext(r, now), "Hari ini, ")
				v, err := view(r, now)
				if err != nil {
					return fail(err)
				}
				return kit.O("ok", true, "message", fmt.Sprintf("Oke, ditunda sampai %s.", when), "reminder", v), nil
			}),
	})
	return set
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		rows, err := reminders.List(c.DB, reminders.ListOptions{Filter: "upcoming", Limit: 500})
		if err != nil {
			return connectors.TestResult{}, err
		}
		if n := len(rows); n > 0 {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d active reminder%s", n, kit.Plural(n))}, nil
		}
		return connectors.TestResult{OK: true, Detail: "Ready, no active reminders"}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				return Tools(c.DB, turn, nil), nil
			},
		}, nil
	},
}
