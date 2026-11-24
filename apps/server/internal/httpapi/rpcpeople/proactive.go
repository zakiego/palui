package rpcpeople

// The agent page's Proactive tab (docs/rfc/proactive.md 12): read and save
// an agent's proactive settings, unmute a subject, check now.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

type proactiveChat struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Channel string `json:"channel"`
}

type proactiveConnector struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

type proactiveNudge struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Subject     string  `json:"subject"`
	Status      string  `json:"status"`
	Label       string  `json:"label"`
	Reason      *string `json:"reason"`
	SentAt      *int64  `json:"sentAt"`
	EscalatedAt *int64  `json:"escalatedAt"`
	WakeAt      *int64  `json:"wakeAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

type proactiveView struct {
	AgentID      string                          `json:"agentId"`
	Enabled      bool                            `json:"enabled"`
	ChatID       *string                         `json:"chatId"`
	PausedReason *string                         `json:"pausedReason"`
	LastTickAt   *int64                          `json:"lastTickAt"`
	NextTickAt   *int64                          `json:"nextTickAt"`
	Config       proactive.Config                `json:"config"`
	Defaults     proactiveDefaults               `json:"defaults"`
	Chats        []proactiveChat                 `json:"chats"`
	Connectors   map[string][]proactiveConnector `json:"connectors"`
	Mutes        []db.ProactiveMute              `json:"mutes"`
	Activity     []proactiveNudge                `json:"activity"`
	TicksToday   int                             `json:"ticksToday"`
	SentToday    int                             `json:"sentToday"`
	TokensToday  int64                           `json:"tokensToday"`
	TokenCap     int64                           `json:"tokenCap"`
	LastErrors   map[string]string               `json:"lastErrors"`
}

type proactiveDefaults struct {
	Calendar proactive.CalendarSource `json:"calendar"`
	WhatsApp proactive.WhatsAppSource `json:"whatsapp"`
}

const proactiveAgentGone = "That agent doesn't exist anymore."

func init() {
	httpapi.Register(httpapi.Handler{
		Name: "getProactiveFn", Method: http.MethodGet, Guard: httpapi.User,
		Input: zod.Object(zod.F("agentId", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("agentId")
			return proactiveFor(c.DB(), id.(string), c.Now())
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
			save := proactive.SaveInput{AgentID: in.AgentID, Enabled: in.Enabled, ChatID: in.ChatID}
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
			return proactiveFor(q, in.AgentID, now)
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
			return proactiveFor(c.DB(), id.(string), c.Now())
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
			return proactiveFor(c.DB(), id.(string), c.Now())
		},
	})
}

func failConfig(err error) error {
	var ce *proactive.ConfigError
	if errors.As(err, &ce) {
		return httpapi.Fail("%s", ce.Msg)
	}
	return err
}

func proactiveFor(q db.Q, agentID string, now int64) (*proactiveView, error) {
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
		LastTickAt: row.LastTickAt, NextTickAt: row.NextTickAt, Config: cfg,
		Defaults:   proactiveDefaults{Calendar: *proactive.DefaultCalendar(""), WhatsApp: *proactive.DefaultWhatsApp("")},
		Connectors: map[string][]proactiveConnector{"calendar": {}, "whatsapp": {}},
		Chats:      []proactiveChat{}, Activity: []proactiveNudge{}, LastErrors: map[string]string{},
		TokenCap: agent.DailyTokenCap,
	}
	// Owner chats only (RFC 11): the web chat and the owner's DMs.
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
	// Connectors this agent may use, by source.
	crows, err := q.Query("SELECT id, type, label, status, agent_ids FROM connectors WHERE type IN ('gcal', 'mcp') ORDER BY sort_order, created_at")
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var id, typ, label, status string
		var ids sql.NullString
		if err := crows.Scan(&id, &typ, &label, &status, &ids); err != nil {
			crows.Close()
			return nil, err
		}
		if ids.Valid && ids.String != "" && ids.String != "[]" && !strings.Contains(ids.String, `"`+agentID+`"`) {
			continue
		}
		key := "whatsapp"
		if typ == "gcal" {
			key = "calendar"
		}
		v.Connectors[key] = append(v.Connectors[key], proactiveConnector{ID: id, Label: label, Status: status})
	}
	crows.Close()
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
		v.Activity = append(v.Activity, proactiveNudge{ID: n.ID, Kind: n.Kind, Subject: n.Subject, Status: n.Status, Label: proactive.Label(n), Reason: n.Reason, SentAt: n.SentAt, EscalatedAt: n.EscalatedAt, WakeAt: n.WakeAt, UpdatedAt: n.UpdatedAt})
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
	if len(ticks) > 0 {
		var res map[string]proactive.SourceResult
		if err := ticks[0].Sources.Decode(&res); err == nil {
			for name, r := range res {
				if !r.OK && r.Error != "" {
					v.LastErrors[name] = r.Error
				}
			}
		}
	}
	return v, nil
}
