// Package proactive is the proactive connector: the chat tools to read and
// change an agent's proactive settings (docs/rfc/proactive.md 10). The engine
// itself is internal/proactive and runs whether or not this connector is on.
package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:          "proactive",
	Kind:        "skill",
	Name:        "Proactive",
	Icon:        "radar",
	Summary:     "Tells you what matters without being asked.",
	Description: "An agent with proactive on checks your calendar and WhatsApp every 30 minutes and messages you first: when to leave, a meeting about to start, a changed event, a chat waiting for your reply, plus a morning and an evening summary. Set it up per agent on the agent's Proactive tab, or from chat with these tools.",
	Builtin:     true,
	Setup:       []connectors.SetupStep{},
	PauseNote:   "Pausing hides the chat tools. Proactive keeps running; turn it off on the agent's Proactive tab.",
	Docs: &connectors.Docs{
		Commands: []string{"nyalain proactive di chat ini", "jam tenang jadi 23:00", "jangan ingetin grup kantor lagi", "proactive hari ini ngabarin apa aja?"},
		Notes: []string{
			"Proactive is off for every agent until you turn it on. Its messages only go to your own DM or the web chat.",
			"WhatsApp needs a wa-hub MCP server whose list_chats returns structured content, classified read on the Connectors page.",
		},
	},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

var (
	getInput    = zod.Object()
	updateInput = zod.Object(
		zod.F("enabled", zod.Bool().Optional().Describe("Turn proactive on or off for this agent.")),
		zod.F("chat", zod.Enum("this").Optional().Describe(`"this" sends proactive messages to the current chat (it must be the owner's DM or the web chat).`)),
		// A JSON merge patch on the config; the tool description lists the keys.
		zod.F("patch", zod.Unknown()),
	)
	muteInput = zod.Object(
		zod.F("who", zod.String().Trim().Min(1).Max(120).Describe("A name or event title from a recent proactive message, e.g. \"grup kantor\" or \"Pak Budi\".")),
	)
	listInput = zod.Object(
		zod.F("status", zod.Enum("all", "sent", "pending", "dropped").Optional().Describe("Filter. Default all.")),
	)
)

// agentFor is the turn's agent ("" is the default agent).
func agentFor(q db.Q, turn connectors.TurnInfo) (db.Agent, error) {
	if turn.AgentID == "" {
		return agents.GetDefault(q, turn.Now)
	}
	a, ok, err := agents.Get(q, turn.AgentID)
	if err != nil {
		return db.Agent{}, err
	}
	if !ok {
		return db.Agent{}, errors.New("agent not found")
	}
	return a, nil
}

// Offer: the owner's own chats, in the owner's own turns (never jobs, hooks,
// flows or a shared chat).
func Offer(q db.Q, turn connectors.TurnInfo) (bool, error) {
	if turn.Trigger != "message" && turn.Trigger != "approval" {
		return false, nil
	}
	if turn.Mode != "" && turn.Mode != "owner" {
		return false, nil
	}
	c, ok, err := chats.Get(q, turn.ChatID)
	if err != nil || !ok {
		return false, err
	}
	shared, err := agents.IsSharedChat(q, c)
	return !shared, err
}

func refusal(err error) (any, error) {
	var ce *proactive.ConfigError
	if errors.As(err, &ce) {
		return kit.O("ok", false, "error", ce.Msg), nil
	}
	return nil, err
}

func view(q db.Q, a db.Agent) (any, error) {
	row, cfg, _, err := proactive.Get(q, a.ID)
	if err != nil {
		return nil, err
	}
	mutes, err := proactive.Mutes(q, a.ID)
	if err != nil {
		return nil, err
	}
	muted := make([]any, len(mutes))
	for i, m := range mutes {
		muted[i] = kit.O("subject", m.Subject, "label", m.Label)
	}
	rawCfg, err := proactive.MarshalConfig(cfg)
	if err != nil {
		return nil, err
	}
	cfgVal, err := jsonx.Parse(rawCfg)
	if err != nil {
		return nil, err
	}
	return kit.O("ok", true, "agent", a.Name, "enabled", row.Enabled, "chatId", kit.StrPtr(row.ChatID), "pausedReason", kit.StrPtr(row.PausedReason), "config", cfgVal, "muted", muted), nil
}

// Tools is the tool set for one turn.
func Tools(d *db.DB, clock func() int64, turn connectors.TurnInfo) *connectors.ToolSet {
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	set := connectors.NewToolSet()
	set.Set("get_proactive", connectors.PaluiTool{
		Data: connectors.DataAgent, Class: "read", Title: "Read proactive settings",
		Tool: kit.Tool("This agent's proactive settings: on or off, target chat, check interval, quiet hours, morning and evening summary, calendar and WhatsApp sources, important contacts and muted subjects.", getInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				a, err := agentFor(d, turn)
				if err != nil {
					return nil, err
				}
				return view(d, a)
			}),
	})
	set.Set("update_proactive", connectors.PaluiTool{
		Data: connectors.DataAgent, Class: "write", Title: "Change proactive settings",
		Tool: kit.Tool("Change this agent's proactive settings. enabled turns it on or off; chat \"this\" makes the current chat the target; patch is a JSON merge patch on the config (keys: checkEveryMin 15-120, maxPerHour 1-10, quietHours {start,end,urgentPasses}, morning {on,at}, evening {on,at}, calendar {connectorId,departLeadMin,onlineLeadMin,notifyChanges} or null, whatsapp {connectorId,account,waitHours,escalateHours,maxAgeDays,skipGroups} or null, contactsAuto, contacts [{subject,label}], modelRole fast|default). Times are HH:MM.", updateInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				a, err := agentFor(d, turn)
				if err != nil {
					return nil, err
				}
				save := proactive.SaveInput{AgentID: a.ID}
				if b, ok := in.Bool("enabled"); ok {
					save.Enabled = &b
				}
				if in.S("chat") == "this" {
					id := turn.ChatID
					save.ChatID = &id
				}
				if p, ok := in.Get("patch"); ok && p != nil {
					raw, err := jsonx.Marshal(p)
					if err != nil {
						return nil, err
					}
					_, cur, _, err := proactive.Get(d, a.ID)
					if err != nil {
						return nil, err
					}
					next, err := proactive.MergePatch(cur, raw)
					if err != nil {
						return refusal(err)
					}
					save.Config = &next
				}
				if _, _, err := proactive.Save(d, save, now); err != nil {
					return refusal(err)
				}
				return view(d, a)
			}),
		Preview: func(input json.RawMessage) string { return "Ubah setelan proactive: " + string(input) },
	})
	set.Set("mute_proactive", connectors.PaluiTool{
		Data: connectors.DataAgent, Class: "write", Title: "Stop a proactive reminder",
		Tool: kit.Tool("Stop proactive reminders about one chat or event, found by name among recent proactive messages.", muteInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				now := clock()
				a, err := agentFor(d, turn)
				if err != nil {
					return nil, err
				}
				who := strings.ToLower(in.S("who"))
				list, err := proactive.ListNudges(d, a.ID, 300)
				if err != nil {
					return nil, err
				}
				for _, n := range list {
					label := proactive.Label(n)
					if n.Subject == "summary" || !strings.Contains(strings.ToLower(label), who) {
						continue
					}
					if err := proactive.Mute(d, a.ID, n.Subject, label, now); err != nil {
						return nil, err
					}
					return kit.O("ok", true, "muted", label), nil
				}
				return kit.O("ok", false, "error", fmt.Sprintf("Nggak ketemu %q di pesan proactive terakhir.", in.S("who"))), nil
			}),
	})
	set.Set("unmute_proactive", connectors.PaluiTool{
		Data: connectors.DataAgent, Class: "write", Title: "Resume a proactive reminder",
		Tool: kit.Tool("Let proactive remind about a muted chat or event again, by its name in the muted list.", muteInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				a, err := agentFor(d, turn)
				if err != nil {
					return nil, err
				}
				who := strings.ToLower(in.S("who"))
				mutes, err := proactive.Mutes(d, a.ID)
				if err != nil {
					return nil, err
				}
				for _, m := range mutes {
					if strings.Contains(strings.ToLower(m.Label), who) {
						if _, err := proactive.Unmute(d, a.ID, m.Subject); err != nil {
							return nil, err
						}
						return kit.O("ok", true, "unmuted", m.Label), nil
					}
				}
				return kit.O("ok", false, "error", fmt.Sprintf("%q nggak ada di daftar yang di-mute.", in.S("who"))), nil
			}),
	})
	set.Set("list_nudges", connectors.PaluiTool{
		Data: connectors.DataAgent, Class: "read", Title: "List proactive messages",
		Tool: kit.Tool("What proactive found recently and what it did with each: sent, pending, dropped (with why), done, snoozed.", listInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				a, err := agentFor(d, turn)
				if err != nil {
					return nil, err
				}
				filter := in.S("status")
				list, err := proactive.ListNudges(d, a.ID, 60)
				if err != nil {
					return nil, err
				}
				out := []any{}
				for _, n := range list {
					if filter != "" && filter != "all" && n.Status != filter {
						continue
					}
					out = append(out, kit.O("kind", n.Kind, "status", n.Status, "label", proactive.Label(n), "reason", kit.StrPtr(n.Reason), "sentAt", n.SentAt, "updatedAt", n.UpdatedAt))
				}
				return kit.O("ok", true, "count", len(out), "nudges", out), nil
			}),
	})
	set.Set("check_proactive_now", connectors.PaluiTool{
		Data: connectors.DataAgent, Class: "write", Title: "Check now",
		Tool: kit.Tool("Run this agent's proactive check within a minute instead of waiting for the next one.", getInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				a, err := agentFor(d, turn)
				if err != nil {
					return nil, err
				}
				res, err := d.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ? AND enabled = 1", clock(), a.ID)
				if err != nil {
					return nil, err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return kit.O("ok", false, "error", "Proactive lagi mati buat agent ini."), nil
				}
				return kit.O("ok", true, "message", "Oke, dicek dalam semenit."), nil
			}),
	})
	return set
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		rows, err := proactive.Enabled(c.DB)
		if err != nil {
			return connectors.TestResult{}, err
		}
		if n := len(rows); n > 0 {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("On for %d agent%s", n, kit.Plural(n))}, nil
		}
		return connectors.TestResult{OK: true, Detail: "Ready, off for every agent"}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				ok, err := Offer(c.DB, turn)
				if err != nil || !ok {
					return connectors.NewToolSet(), err
				}
				return Tools(c.DB, nil, turn), nil
			},
		}, nil
	},
}
