// Package watches is the watches connector (PLAN 6e): tools to set, list and
// cancel a watch from chat. Every tool works on the turn's chat only, and
// they are offered in message turns only, in any chat the agent answers,
// groups and other people's chats included.
package watches

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	remconn "github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/watches"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:        "watches",
	Kind:      "skill",
	Name:      "Watches",
	Icon:      "eye",
	Summary:   "Keep an eye on something and tell the chat once it happens.",
	Builtin:   true,
	Setup:     []connectors.SetupStep{},
	PauseNote: "Pausing hides the watch tools. Watches already set keep checking.",
	Docs: &connectors.Docs{
		Commands: []string{"kabarin kalau <sesuatu> udah keluar", "tell me when <something> happens"},
		Notes: []string{
			"A watch checks with the agent's read tools (web search, fetch page, any read MCP tool) every 15 to 1440 minutes, 30 by default.",
			"It can start later (\"mulai jam 16.00 besok\"): nothing is checked before its start time, and the first check runs right at it.",
			"It posts once, with the answer and its source, and stops. It ends after 7 days by default, 14 days at most, with a short note if nothing was found.",
			"Anyone the agent answers in a chat can set one there. A chat has at most 10 running, and each watch may spend up to 1,000,000 tokens.",
		},
	},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

var (
	createInput = zod.Object(
		zod.F("topic", zod.String().Min(1).Max(watches.MaxTopic).Describe("A short label for the watch, in the chat's language, e.g. \"Laba resmi Danantara 2025\".")),
		zod.F("question", zod.String().Min(1).Max(watches.MaxQuestion).Describe("What to look for, with the context a fresh check needs (it won't see this chat): names, period, where it's likely to appear.")),
		zod.F("doneWhen", zod.String().Min(1).Max(watches.MaxDoneWhen).Describe("What counts as found, strictly. Name what does NOT count, like claims already going around, so they don't trigger it. E.g. \"Danantara's audited 2025 net profit figure from its own release or BPK's audit opinion, reported by a major outlet. The Rp325,5 T and Rp326 T claims don't count.\"")),
		zod.F("everyMin", zod.Int().Min(watches.MinEveryMin).Max(watches.MaxEveryMin).Optional().Describe("Minutes between checks, default 30. Use more for slow things (daily news: 240).")),
		zod.F("startAt", remconn.LocalDateTime().Optional().Describe("Start checking only at this wall-clock time (YYYY-MM-DDTHH:mm, in the owner's zone), when the thing can't happen before it, like a closing price, a result announced at a set hour or a release at a known time. Nothing is checked before it and the first check runs right at it. Leave it out to start now. Must be before the end and within 14 days from now.")),
		zod.F("hours", zod.Int().Min(1).Max(watches.MaxHours).Optional().Describe("How long to keep watching, in hours from the start (startAt, or now). Default 168 (7 days). The end can be at most 336 hours (14 days) from now. Ignored when until is given.")),
		zod.F("until", remconn.LocalDateTime().Optional().Describe("Or: watch until this wall-clock time (YYYY-MM-DDTHH:mm, in the owner's zone).")),
	)
	listInput = zod.Object(
		zod.F("ended", zod.Default(zod.Bool(), false).Describe("List ended watches (found, expired, cancelled) instead of running ones.")),
	)
	cancelInput = zod.Object(zod.F("id", zod.String().Describe("Watch id from list_watches.")))
)

func fail(err error) (any, error) {
	var we *watches.WatchError
	var te *reminders.TimeError
	if errors.As(err, &we) {
		return kit.Fail(we.Msg), nil
	}
	if errors.As(err, &te) {
		return kit.Fail(te.Msg), nil
	}
	return nil, err
}

func view(w db.Watch, tz string, now int64) any {
	var next, starts any
	if w.NextCheckAt != nil && !watches.Ended(w.Status) {
		next = reminders.FormatWhen(*w.NextCheckAt, tz, now)
	}
	if w.StartsAt != nil {
		starts = reminders.FormatWhen(*w.StartsAt, tz, now)
	}
	return kit.O(
		"id", w.ID, "topic", w.Topic, "status", w.Status,
		"question", w.Question, "doneWhen", w.DoneWhen,
		"everyMin", w.EveryMin, "starts", starts, "ends", reminders.FormatWhen(w.ExpiresAt, tz, now),
		"nextCheck", next, "checks", w.Checks, "createdBy", w.CreatedBy,
		"lastSummary", kit.If(w.LastSummary != "", w.LastSummary), "result", kit.StrPtr(w.Result),
	)
}

// creator is who asked for the watch: the owner, or the sender of the newest
// message this turn answers.
func creator(q db.Q, turn connectors.TurnInfo) string {
	runID := turn.BatchRunID
	if runID == "" {
		runID = turn.RunID
	}
	var name *string
	var fromOwner bool
	err := q.QueryRow("SELECT sender_name, from_owner FROM messages WHERE chat_id = ? AND run_id = ? AND role = 'user' ORDER BY created_at DESC, id DESC LIMIT 1", turn.ChatID, runID).Scan(&name, &fromOwner)
	if err == nil && !fromOwner && name != nil && *name != "" {
		return *name
	}
	if turn.Mode == "" || turn.Mode == "owner" || fromOwner {
		return "owner"
	}
	return ""
}

// Offer: message turns (and their approval resumes) only, never a job, flow,
// hook or a watch check itself.
func Offer(turn connectors.TurnInfo) bool {
	return turn.Trigger == "message" || turn.Trigger == "approval"
}

// Tools is the watch tools for one turn's chat.
func Tools(d *db.DB, turn connectors.TurnInfo, clock func() int64) *connectors.ToolSet {
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	chatID := turn.ChatID
	set := connectors.NewToolSet()
	set.Set("watch_for", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Watch for something",
		Tool: kit.Tool("Keep checking for something that isn't out yet and tell this chat once it is: an official figure, an announcement, a result, a release, a price or status change. Use it when someone asks to be told when something happens, or offer it when you find the answer doesn't exist yet but will (\"belum keluar, mau aku kabarin pas udah keluar?\"). When it can't happen before a known time (a market close, an announcement at a set hour), set startAt so it doesn't spend checks before then. Each check runs on its own later with your read tools and no chat history, so put everything it needs in question and doneWhen. It posts once with the answer and source, then stops; if the time runs out first, it posts a short note. Repeat the returned message so people know when it ends.", createInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				tz, err := remconn.ZoneFor(d, turn, "")
				if err != nil {
					return nil, err
				}
				var starts int64
				if s := in.S("startAt"); s != "" {
					if starts, err = reminders.LocalToUTC(s, tz); err != nil {
						return fail(err)
					}
				}
				// hours count from the start.
				from := max(now, starts)
				expires := from + watches.DefaultHours*3600_000
				if until := in.S("until"); until != "" {
					if expires, err = reminders.LocalToUTC(until, tz); err != nil {
						return fail(err)
					}
				} else if h, ok := in.Int("hours"); ok {
					expires = from + h*3600_000
				} else if starts != 0 {
					// The default must still fit the 14-day cap from now.
					expires = min(expires, now+watches.MaxHours*3600_000)
				}
				every, _ := in.Int("everyMin")
				w, err := watches.Create(d, watches.CreateInput{
					ChatID: chatID, AgentID: turn.AgentID, CreatedBy: creator(d, turn),
					Topic: in.S("topic"), Question: in.S("question"), DoneWhen: in.S("doneWhen"),
					EveryMin: every, ExpiresAt: expires, StartsAt: starts,
				}, now)
				if err != nil {
					return fail(err)
				}
				msg := fmt.Sprintf("Oke, aku pantau \"%s\" tiap %s sampai %s. Begitu ketemu aku kabarin di sini.", w.Topic, everyText(w.EveryMin), reminders.FormatWhen(w.ExpiresAt, tz, now))
				if w.StartsAt != nil {
					msg = fmt.Sprintf("Oke, aku pantau \"%s\" mulai %s, tiap %s sampai %s. Sebelum itu nggak dicek sama sekali. Begitu ketemu aku kabarin di sini.", w.Topic, reminders.FormatWhen(*w.StartsAt, tz, now), everyText(w.EveryMin), reminders.FormatWhen(w.ExpiresAt, tz, now))
				}
				return kit.O("ok", true, "id", w.ID, "message", msg, "watch", view(w, tz, now)), nil
			}),
	})
	set.Set("list_watches", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "read",
		Title: "List watches",
		Tool: kit.Tool("List this chat's watches: running ones by next check, or ended ones (ended: true), latest first.", listInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				tz, err := remconn.ZoneFor(d, turn, "")
				if err != nil {
					return nil, err
				}
				rows, err := watches.List(d, chatID, in.True("ended"), 20)
				if err != nil {
					return nil, err
				}
				list := make([]any, 0, len(rows))
				for _, w := range rows {
					list = append(list, view(w, tz, now))
				}
				return kit.O("ok", true, "count", len(rows), "watches", list, "message", kit.If(len(rows) == 0, "Nggak ada pantauan.")), nil
			}),
	})
	set.Set("cancel_watch", connectors.PaluiTool{
		Data:  connectors.DataChat,
		Class: "write",
		Title: "Cancel a watch",
		Tool: kit.Tool("Stop a watch in this chat. It stays in the ended list.", cancelInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				id := in.S("id")
				w, ok, err := watches.Get(d, id)
				if err != nil {
					return nil, err
				}
				if !ok || w.ChatID != chatID {
					return kit.Fail(fmt.Sprintf("No watch %s in this chat. Call list_watches to get the ids.", id)), nil
				}
				if watches.Ended(w.Status) {
					return kit.O("ok", true, "message", fmt.Sprintf("Pantauan \"%s\" memang udah selesai (%s).", w.Topic, w.Status)), nil
				}
				if w, err = watches.Cancel(d, id, now); err != nil {
					return fail(err)
				}
				return kit.O("ok", true, "message", fmt.Sprintf("Oke, pantauan \"%s\" dihentikan.", w.Topic)), nil
			}),
	})
	return set
}

func everyText(min int64) string {
	if min%60 == 0 {
		return fmt.Sprintf("%d jam", min/60)
	}
	return fmt.Sprintf("%d menit", min)
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		var n int
		if err := c.DB.QueryRow("SELECT count(*) FROM watches WHERE status IN (?, ?)", watches.StatusActive, watches.StatusChecking).Scan(&n); err != nil {
			return connectors.TestResult{}, err
		}
		if n > 0 {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d running", n)}, nil
		}
		return connectors.TestResult{OK: true, Detail: "Ready, no running watches"}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				if !Offer(turn) {
					return connectors.NewToolSet(), nil
				}
				return Tools(c.DB, turn, nil), nil
			},
		}, nil
	},
}
