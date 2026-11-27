package rpcpeople

// The agent page's Proactive tab (docs/rfc/proactive-prompt.md 12): read and
// save an agent's proactive prompt and settings, its memory notes, unmute a
// subject, check now, and a test run.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

type proactiveChat struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Channel string `json:"channel"`
}

type proactiveNudge struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Subject   string  `json:"subject"`
	Status    string  `json:"status"`
	Label     string  `json:"label"`
	Reason    *string `json:"reason"`
	SentAt    *int64  `json:"sentAt"`
	WakeAt    *int64  `json:"wakeAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

type proactiveLastRun struct {
	At      int64   `json:"at"`
	Full    bool    `json:"full"`
	Skipped string  `json:"skipped"`
	Calls   int     `json:"calls"`
	Error   *string `json:"error"`
}

type proactiveView struct {
	AgentID      string               `json:"agentId"`
	Enabled      bool                 `json:"enabled"`
	ChatID       *string              `json:"chatId"`
	PausedReason *string              `json:"pausedReason"`
	LastTickAt   *int64               `json:"lastTickAt"`
	NextTickAt   *int64               `json:"nextTickAt"`
	Config       proactive.Config     `json:"config"`
	Starter      string               `json:"starter"`
	Chats        []proactiveChat      `json:"chats"`
	Tools        []proactive.ToolInfo `json:"tools"`
	// Each mention in the saved prompt, with why it doesn't resolve ("" when it does).
	Mentions    []proactive.MentionProblem `json:"mentions"`
	Notes       []db.ProactiveNote         `json:"notes"`
	NotesChars  int                        `json:"notesChars"`
	Mutes       []db.ProactiveMute         `json:"mutes"`
	Activity    []proactiveNudge           `json:"activity"`
	TicksToday  int                        `json:"ticksToday"`
	SentToday   int                        `json:"sentToday"`
	TokensToday int64                      `json:"tokensToday"`
	TokenCap    int64                      `json:"tokenCap"`
	LastRun     *proactiveLastRun          `json:"lastRun"`
}

const proactiveAgentGone = "That agent doesn't exist anymore."

func init() {
	httpapi.Register(httpapi.Handler{
		Name: "getProactiveFn", Method: http.MethodGet, Guard: httpapi.User,
		Input: zod.Object(zod.F("agentId", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("agentId")
			return proactiveFor(c, id.(string))
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "saveProactiveFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(
			zod.F("agentId", agentID),
			zod.F("enabled", zod.Bool().Optional()),
			zod.F("chatId", zod.String().Max(100).Optional()),
			zod.F("config", zod.Unknown()),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				AgentID string          `json:"agentId"`
				Enabled *bool           `json:"enabled"`
				ChatID  *string         `json:"chatId"`
				Config  json.RawMessage `json:"config"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			q, now := c.DB(), c.Now()
			save := proactive.SaveInput{AgentID: in.AgentID, Enabled: in.Enabled, ChatID: in.ChatID, Tools: toolHost(c)}
			if len(in.Config) > 0 && string(in.Config) != "null" {
				_, cur, _, err := proactive.Get(q, in.AgentID)
				if err != nil {
					return nil, err
				}
				next, err := proactive.MergePatch(cur, in.Config)
				if err != nil {
					return nil, failConfig(err)
				}
				save.Config = &next
			}
			if _, _, err := proactive.Save(q, save, now); err != nil {
				return nil, failConfig(err)
			}
			if rt, err := c.Runtime(); err == nil {
				rt.Reconcile()
			}
			return proactiveFor(c, in.AgentID)
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "unmuteProactiveFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("agentId", agentID), zod.F("subject", zod.String().Min(1).Max(200))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("agentId")
			subject, _ := o.Get("subject")
			if _, err := proactive.Unmute(c.DB(), id.(string), subject.(string)); err != nil {
				return nil, err
			}
			return proactiveFor(c, id.(string))
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "checkProactiveNowFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("agentId", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("agentId")
			rt, err := c.Runtime()
			if err != nil {
				return nil, err
			}
			if rt.Built == nil || rt.Built.Proactive == nil {
				return nil, httpapi.Fail("Proactive isn't running.")
			}
			if err := rt.Built.Proactive.CheckNow(id.(string), c.Now()); err != nil {
				return nil, failConfig(err)
			}
			return proactiveFor(c, id.(string))
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "testProactiveFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("agentId", agentID), zod.F("prompt", zod.String().Max(proactive.MaxPromptChars*2).Optional())),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				AgentID string  `json:"agentId"`
				Prompt  *string `json:"prompt"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			rt, err := c.Runtime()
			if err != nil {
				return nil, err
			}
			if rt.Built == nil || rt.Built.Proactive == nil {
				return nil, httpapi.Fail("Proactive isn't running.")
			}
			res, err := rt.Built.Proactive.TestRun(c.Ctx, in.AgentID, in.Prompt, c.Now())
			if err != nil {
				return nil, failConfig(err)
			}
			return res, nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "addProactiveNoteFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(
			zod.F("agentId", agentID),
			zod.F("text", zod.String().Trim().Min(1).Max(proactive.MaxNoteChars*2)),
			zod.F("days", zod.Int().Min(1).Max(proactive.OwnerNoteDaysMax).Optional()),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				AgentID string `json:"agentId"`
				Text    string `json:"text"`
				Days    int    `json:"days"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			if err := mustAgent(c.DB(), in.AgentID); err != nil {
				return nil, err
			}
			if _, err := proactive.AddNote(c.DB(), in.AgentID, in.Text, proactive.NoteSourceOwner, in.Days, c.Now()); err != nil {
				return nil, failConfig(err)
			}
			return proactiveFor(c, in.AgentID)
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "editProactiveNoteFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(
			zod.F("agentId", agentID),
			zod.F("noteId", zod.String().Min(1).Max(100)),
			zod.F("text", zod.String().Trim().Min(1).Max(proactive.MaxNoteChars*2).Optional()),
			zod.F("keep", zod.Bool().Optional()),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				AgentID string  `json:"agentId"`
				NoteID  string  `json:"noteId"`
				Text    *string `json:"text"`
				Keep    *bool   `json:"keep"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			ok, err := proactive.EditNote(c.DB(), in.AgentID, in.NoteID, in.Text, in.Keep != nil && *in.Keep, c.Now())
			if err != nil {
				return nil, failConfig(err)
			}
			if !ok {
				return nil, httpapi.Fail("That note is gone.")
			}
			return proactiveFor(c, in.AgentID)
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "deleteProactiveNoteFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("agentId", agentID), zod.F("noteId", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("agentId")
			noteID, _ := o.Get("noteId")
			if _, err := proactive.ForgetNote(c.DB(), id.(string), noteID.(string), false); err != nil {
				return nil, err
			}
			return proactiveFor(c, id.(string))
		},
	})
}

func mustAgent(q db.Q, agentID string) error {
	_, ok, err := agents.Get(q, agentID)
	if err != nil {
		return err
	}
	if !ok {
		return httpapi.Fail(proactiveAgentGone)
	}
	return nil
}

func failConfig(err error) error {
	var ce *proactive.ConfigError
	if errors.As(err, &ce) {
		return httpapi.Fail("%s", ce.Msg)
	}
	return err
}

// catalogHost is the agent's tools as the runtime lists them, without a way
// to call them: enough to check mentions when no live host is wired (tests).
type catalogHost struct{ c *httpapi.Call }

func (h catalogHost) Tools(ctx context.Context, agentID, _ string, _ int64) ([]proactive.Tool, error) {
	rt, err := runtimeOf(h.c)
	if err != nil {
		return nil, err
	}
	list, err := rt.AgentTools(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]proactive.Tool, 0, len(list))
	for _, t := range list {
		out = append(out, proactive.Tool{Info: proactive.ToolInfo{Name: t.Name, ConnectorID: t.ConnectorID, ConnectorLabel: t.ConnectorLabel, Tool: t.Tool, Class: t.Class, SuggestedClass: t.SuggestedClass, Title: t.Title, Untrusted: t.Untrusted}})
	}
	return out, nil
}

// toolHost is the live host, or the runtime's catalog when none is wired.
func toolHost(c *httpapi.Call) proactive.ToolHost {
	if h := proactive.DefaultToolHost(); h != nil {
		return h
	}
	return catalogHost{c}
}

func proactiveFor(c *httpapi.Call, agentID string) (*proactiveView, error) {
	q, now := c.DB(), c.Now()
	agent, ok, err := agents.Get(q, agentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpapi.Fail(proactiveAgentGone)
	}
	row, cfg, _, err := proactive.Get(q, agentID)
	if err != nil {
		return nil, err
	}
	v := &proactiveView{
		AgentID: agentID, Enabled: row.Enabled, ChatID: row.ChatID, PausedReason: row.PausedReason,
		LastTickAt: row.LastTickAt, NextTickAt: row.NextTickAt, Config: cfg, Starter: proactive.StarterPrompt,
		Chats: []proactiveChat{}, Tools: []proactive.ToolInfo{}, Mentions: []proactive.MentionProblem{},
		Activity: []proactiveNudge{}, TokenCap: agent.DailyTokenCap,
	}
	// Owner chats only: the web chat and the owner's DMs.
	rows, err := q.Query("SELECT " + db.ChatCols + " FROM chats WHERE channel = 'web' OR kind = 'dm' ORDER BY (channel = 'web'), last_message_at DESC LIMIT 200")
	list, err := db.All(rows, err, db.ScanChat)
	if err != nil {
		return nil, err
	}
	for _, ch := range list {
		if shared, err := agents.IsSharedChat(q, ch); err != nil {
			return nil, err
		} else if shared {
			continue
		}
		title := ch.Title
		if title == "" {
			title = map[bool]string{true: "Web chat", false: ch.ExternalID}[ch.Channel == db.ChannelWeb]
		}
		v.Chats = append(v.Chats, proactiveChat{ID: ch.ID, Title: title, Channel: ch.Channel})
	}
	// The tools the prompt may mention, and how its mentions resolve. A host
	// that can't list them leaves the list empty rather than failing the tab.
	chatID := ""
	if row.ChatID != nil {
		chatID = *row.ChatID
	}
	if tools, err := toolHost(c).Tools(c.Ctx, agentID, chatID, now); err == nil {
		v.Tools = proactive.Infos(tools)
		if v.Tools == nil {
			v.Tools = []proactive.ToolInfo{}
		}
		v.Mentions = proactive.MentionStatus(proactive.Mentions(cfg.Prompt), v.Tools)
	}
	if v.Notes, err = proactive.Notes(q, agentID, now); err != nil {
		return nil, err
	}
	if v.Notes == nil {
		v.Notes = []db.ProactiveNote{}
	}
	for _, n := range v.Notes {
		v.NotesChars += strx.Len16(n.Text)
	}
	if v.Mutes, err = proactive.Mutes(q, agentID); err != nil {
		return nil, err
	}
	if v.Mutes == nil {
		v.Mutes = []db.ProactiveMute{}
	}
	tz, err := settings.Timezone(q)
	if err != nil || tz == "" {
		tz = "UTC"
	}
	dayStart := zone.StartOfDay(now, zone.MustLoad(tz))
	nudges, err := proactive.NudgesSince(q, agentID, dayStart)
	if err != nil {
		return nil, err
	}
	sentOutboxes := map[string]bool{}
	for _, n := range nudges {
		v.Activity = append(v.Activity, proactiveNudge{ID: n.ID, Kind: n.Kind, Subject: n.Subject, Status: n.Status, Label: proactive.Label(n), Reason: n.Reason, SentAt: n.SentAt, WakeAt: n.WakeAt, UpdatedAt: n.UpdatedAt})
		if n.SentAt != nil && *n.SentAt >= dayStart && n.OutboxID != nil {
			sentOutboxes[*n.OutboxID] = true
		}
	}
	if len(v.Activity) > 50 {
		v.Activity = v.Activity[:50]
	}
	v.SentToday = len(sentOutboxes)
	ticks, err := proactive.Ticks(q, agentID, dayStart, 500)
	if err != nil {
		return nil, err
	}
	v.TicksToday = len(ticks)
	for _, t := range ticks {
		v.TokensToday += t.Tokens
	}
	if last, err := proactive.Ticks(q, agentID, 0, 1); err == nil && len(last) == 1 {
		t := last[0]
		var l proactive.TickLog
		_ = t.Sources.Decode(&l)
		v.LastRun = &proactiveLastRun{At: t.StartedAt, Full: l.Full, Skipped: l.Skipped, Calls: len(l.Calls), Error: t.Error}
	}
	return v, nil
}
