package rpcsched

import (
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/reminders.ts and the writes of
// server/reminders-bridge.ts: the Reminders page. Every write pokes the
// runtime so the scheduler re-arms at once.

const reminderGoneText = "That reminder doesn't exist anymore."

func init() {
	httpapi.Register(httpapi.Handler{Name: "getReminders", Method: http.MethodGet, Guard: httpapi.User, Fn: getReminders})
	httpapi.Register(httpapi.Handler{
		Name: "parseReminder", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("text", zod.String().Trim().Min(1).Max(500)), zod.F("tz", zod.String().Max(64).Optional())),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				Text string `json:"text"`
				TZ   string `json:"tz"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			// A POST that spends model tokens: same-origin check, like every write.
			rt, err := c.Runtime()
			if err != nil {
				return nil, err
			}
			return rt.ParseReminder(c.Ctx, in.Text, in.TZ)
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "createReminderFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(
			zod.F("chatId", zod.String().Min(1).Max(100)),
			zod.F("text", zod.String().Trim().Min(1).Max(500)),
			zod.F("fireAtIso", zod.String().Min(10).Max(40)),
			zod.F("tz", ianaZone()),
			zod.F("rrule", zod.Nullable(zod.String().Max(300)).Optional()),
		),
		Fn: createReminder,
	})
	httpapi.Register(httpapi.Handler{
		Name: "cancelReminderFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: idInput(),
		Fn: func(c *httpapi.Call) (any, error) {
			q, id := c.DB(), inputID(c)
			if _, ok, err := reminders.Get(q, id); err != nil {
				return nil, err
			} else if !ok {
				return nil, httpapi.Fail(reminderGoneText)
			}
			if _, err := reminders.Cancel(q, id, c.Now()); err != nil {
				return nil, friendly(err)
			}
			poke(c)
			return obj("ok", true), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "snoozeReminderFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("preset", zod.Enum("10m", "1h", "tomorrow"))),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				ID     string `json:"id"`
				Preset string `json:"preset"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			q := c.DB()
			if _, ok, err := reminders.Get(q, in.ID); err != nil {
				return nil, err
			} else if !ok {
				return nil, httpapi.Fail(reminderGoneText)
			}
			row, err := reminders.SnoozeReminder(q, in.ID, reminders.Snooze{Preset: in.Preset}, c.Now())
			if err != nil {
				return nil, friendly(err)
			}
			poke(c)
			return obj("fireAt", row.FireAt), nil
		},
	})
}

func createReminder(c *httpapi.Call) (any, error) {
	var in struct {
		ChatID    string  `json:"chatId"`
		Text      string  `json:"text"`
		FireAtIso string  `json:"fireAtIso"`
		TZ        string  `json:"tz"`
		RRule     *string `json:"rrule"`
	}
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	ms, ok := fromISOSetZone(in.FireAtIso)
	loc := luxonZone(in.TZ)
	if !ok || loc == nil {
		return nil, httpapi.Fail("That time couldn't be read. Try rephrasing it.")
	}
	rr := ""
	if in.RRule != nil {
		rr = *in.RRule
	}
	row, err := reminders.Create(c.DB(), reminders.CreateInput{ChatID: in.ChatID, Text: in.Text, LocalStart: wallTime(ms, loc), TZ: in.TZ, RRule: rr}, c.Now())
	if err != nil {
		return nil, friendly(err)
	}
	poke(c)
	return obj("id", row.ID), nil
}

type reminderChat struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Channel string `json:"channel"`
}

type reminderView struct {
	ID          string        `json:"id"`
	Text        string        `json:"text"`
	RRule       *string       `json:"rrule"`
	TZ          string        `json:"tz"`
	FireAt      int64         `json:"fireAt"`
	Status      string        `json:"status"`
	LastFiredAt *int64        `json:"lastFiredAt"`
	Smart       bool          `json:"smart"`
	Chat        *reminderChat `json:"chat"`
}

type reminderTotals struct {
	Upcoming  int64 `json:"upcoming"`
	Recurring int64 `json:"recurring"`
	Done      int64 `json:"done"`
}

const (
	activeOneOff    = "status IN ('pending', 'firing') AND rrule IS NULL"
	activeRecurring = "status IN ('pending', 'firing') AND rrule IS NOT NULL"
	finished        = "status IN ('done', 'cancelled')"
)

func getReminders(c *httpapi.Call) (any, error) {
	q := c.DB()
	titles := map[string]*reminderChat{}
	rows, err := q.Query("SELECT id, title, channel, external_id FROM chats")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ch reminderChat
		var title *string
		var ext string
		if err := rows.Scan(&ch.ID, &title, &ch.Channel, &ext); err != nil {
			rows.Close()
			return nil, err
		}
		ch.Title = titleOr(title, ext)
		titles[ch.ID] = &ch
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	list := func(where, order string, limit int) ([]reminderView, error) {
		rows, err := q.Query("SELECT "+db.ReminderCols+" FROM reminders WHERE "+where+" ORDER BY "+order+" LIMIT ?", limit)
		all, err := db.All(rows, err, db.ScanReminder)
		if err != nil {
			return nil, err
		}
		out := []reminderView{}
		for _, r := range all {
			out = append(out, reminderView{ID: r.ID, Text: r.Text, RRule: r.RRule, TZ: r.TZ, FireAt: r.FireAt, Status: r.Status, LastFiredAt: r.LastFiredAt, Smart: r.SmartPrompt != nil && *r.SmartPrompt != "", Chat: titles[r.ChatID]})
		}
		return out, nil
	}
	upcoming, err := list(activeOneOff, "fire_at ASC", 200)
	if err != nil {
		return nil, err
	}
	recurring, err := list(activeRecurring, "fire_at ASC", 200)
	if err != nil {
		return nil, err
	}
	done, err := list(finished, "updated_at DESC", 100)
	if err != nil {
		return nil, err
	}
	// The lists above are capped; these are how many rows there really are.
	var totals reminderTotals
	for _, t := range []struct {
		where string
		n     *int64
	}{{activeOneOff, &totals.Upcoming}, {activeRecurring, &totals.Recurring}, {finished, &totals.Done}} {
		if err := q.QueryRow("SELECT count(*) FROM reminders WHERE " + t.where).Scan(t.n); err != nil {
			return nil, err
		}
	}
	options, err := chatOptions(q)
	if err != nil {
		return nil, err
	}
	// Default target: the owner's Telegram DM, then WhatsApp, then the web chat.
	var defaultChatID *string
	for _, ch := range []string{"telegram", "whatsapp"} {
		o, ok, err := owner.GetOwner(q, ch)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		chat, ok, err := chats.Find(q, ch, o.ExternalID, "")
		if err != nil {
			return nil, err
		}
		if ok {
			defaultChatID = &chat.ID
			break
		}
	}
	if defaultChatID == nil {
		for i := range options {
			if options[i].Channel == "web" {
				defaultChatID = &options[i].ID
				break
			}
		}
	}
	if defaultChatID == nil && len(options) > 0 {
		defaultChatID = &options[0].ID
	}
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	return obj("upcoming", upcoming, "recurring", recurring, "done", done, "totals", totals, "chats", options, "defaultChatId", defaultChatID, "timezone", tz), nil
}
