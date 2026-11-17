package rpcpeople

import (
	"errors"
	"net/http"
	"slices"

	"github.com/zakiego/palui/apps/server/internal/ack"
	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deploynotice"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/agents.ts: the Agents pages (PLAN 19.7). The runtime
// reads agents and chats at the start of every turn, so writes go straight
// to the tables.

// Chats the agent page lists at once, newest first.
const agentChatsLimit = 100

const (
	agentGone = "That agent doesn't exist anymore."
	chatGone  = "That chat doesn't exist anymore."
)

// rethrowAgent: an AgentError's message is shown as is.
func rethrowAgent(err error) error {
	var ae *agents.Error
	if errors.As(err, &ae) {
		return httpapi.Fail("%s", ae.Msg)
	}
	return err
}

var (
	agentID   = zod.String().Min(1).Max(100)
	chatIDArg = zod.String().Min(1).Max(100)
)

func agentFields(extra ...zod.Field) *zod.ObjectS {
	fields := []zod.Field{
		zod.F("name", zod.String().Trim().Min(1).Max(agents.MaxAgentName)),
		zod.F("persona", zod.String().Trim().Min(1).Max(agents.MaxPersona)),
		zod.F("guestTools", zod.Enum("none", "safe")),
		zod.F("dailyTokenCap", zod.Int().Min(0).Max(100_000_000)),
		zod.F("recallContainer", zod.Nullable(zod.String().Trim().Max(64))),
		zod.F("recallReadContainers", zod.Array(zod.String().Trim().Max(64)).Max(agents.MaxReadContainers)),
		zod.F("recallPerChat", zod.Bool()),
		zod.F("telegramConnectorId", zod.Nullable(zod.String().Min(1).Max(64))),
		zod.F("avatarImage", zod.Nullable(zod.String().Max(agents.MaxAvatarImage))),
		zod.F("avatarIcon", zod.Nullable(zod.Enum(agents.AgentIcons...))),
		zod.F("avatarColor", zod.Nullable(zod.Enum(agents.AgentColors...))),
		// Left out keeps the agent's models; an empty role uses the global chain.
		zod.F("modelRoles", zod.Optional(zod.Object(roleChain("default"), roleChain("fast"), roleChain("vision")))),
	}
	return zod.Object(append(fields, extra...)...)
}

func roleChain(role string) zod.Field {
	return zod.F(role, zod.Array(settings.ModelLinkZod()).Max(6))
}

type agentInput struct {
	ID                   *string         `json:"id"`
	Slug                 *string         `json:"slug"`
	Name                 string          `json:"name"`
	Persona              string          `json:"persona"`
	GuestTools           string          `json:"guestTools"`
	DailyTokenCap        int64           `json:"dailyTokenCap"`
	RecallContainer      *string         `json:"recallContainer"`
	RecallReadContainers []string        `json:"recallReadContainers"`
	RecallPerChat        bool            `json:"recallPerChat"`
	TelegramConnectorID  *string         `json:"telegramConnectorId"`
	AvatarImage          *string         `json:"avatarImage"`
	AvatarIcon           *string         `json:"avatarIcon"`
	AvatarColor          *string         `json:"avatarColor"`
	ModelRoles           *db.AgentModels `json:"modelRoles"`
}

// nullable maps a JSON string or null onto agents.Input's "" = null.
func nullable(p *string) *string {
	if p == nil {
		return new(string)
	}
	return p
}

type agentPageChat struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Channel      string `json:"channel"`
	Kind         string `json:"kind"`
	LastAt       int64  `json:"lastAt"`
	Allowed      bool   `json:"allowed"`
	ReplyMode    string `json:"replyMode"`
	Ack          bool   `json:"ack"`
	AckEmoji     string `json:"ackEmoji"`
	DeployNotice bool   `json:"deployNotice"`
}

// agentModels is the agent's own chains next to the global ones it falls back to.
type agentModels struct {
	Own        roleChains            `json:"own"`
	Global     settings.ModelRoles   `json:"global"`
	Connectors []httpapi.ModelOption `json:"connectors"`
}

type roleChains struct {
	Default []db.ModelLink `json:"default"`
	Fast    []db.ModelLink `json:"fast"`
	Vision  []db.ModelLink `json:"vision"`
}

func orNone(l []db.ModelLink) []db.ModelLink {
	if l == nil {
		return []db.ModelLink{}
	}
	return l
}

type agentPage struct {
	Agent        agentViewT       `json:"agent"`
	Models       agentModels      `json:"models"`
	Bots         []telegramBot    `json:"bots"`
	Instructions []db.Instruction `json:"instructions"`
	Chats        []agentPageChat  `json:"chats"`
	ChatsTotal   int              `json:"chatsTotal"`
}

type savedAgent struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

type toolPermissionsResult struct {
	ToolPermissions db.ToolPermissions `json:"toolPermissions"`
}

type savedFlag struct {
	Saved bool `json:"saved"`
}

func chatExists(q db.Q, id string) (bool, error) {
	_, ok, err := chats.Get(q, id)
	return ok, err
}

func instructionsOrEmpty(list []db.Instruction) []db.Instruction {
	if list == nil {
		return []db.Instruction{}
	}
	return list
}

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getAgentsPage",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Fn: func(c *httpapi.Call) (any, error) {
			q, now := c.DB(), c.Now()
			rows, err := q.Query(`select "agent_id" from "chats"`)
			owners, err := db.All(rows, err, func(s db.Scanner) (*string, error) {
				var a *string
				return a, s.Scan(&a)
			})
			if err != nil {
				return nil, err
			}
			list, err := agents.List(q, now)
			if err != nil {
				return nil, err
			}
			views := make([]agentViewT, 0, len(list))
			for _, a := range list {
				n := 0
				for _, o := range owners {
					if o != nil && *o == a.ID {
						n++
					}
				}
				v, err := agentView(q, a, n, now)
				if err != nil {
					return nil, err
				}
				views = append(views, v)
			}
			bots, err := telegramBots(q)
			if err != nil {
				return nil, err
			}
			return struct {
				Agents []agentViewT  `json:"agents"`
				Bots   []telegramBot `json:"bots"`
			}{views, bots}, nil
		},
	})

	// One agent's page (/agents/<slug>): its settings, instructions and the
	// chats it answers in. agent is null when the slug doesn't exist.
	httpapi.Register(httpapi.Handler{
		Name:   "getAgentPage",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("slug", zod.String().Trim().Min(1).Max(64))),
		Fn: func(c *httpapi.Call) (any, error) {
			q, now := c.DB(), c.Now()
			slug, _ := c.Obj().Get("slug")
			row, ok, err := agents.GetBySlug(q, slug.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return struct {
					Agent *agentViewT `json:"agent"`
				}{}, nil
			}
			// Palui answers every chat that has no agent of its own.
			var own []chatItem
			if row.IsDefault {
				own, err = chatList(q, `"chats"."agent_id" is null`)
			} else {
				own, err = chatList(q, `"chats"."agent_id" = ?`, row.ID)
			}
			if err != nil {
				return nil, err
			}
			// A bot that belongs to another agent isn't offered.
			all, err := agents.List(q, now)
			if err != nil {
				return nil, err
			}
			taken := map[string]bool{}
			for _, a := range all {
				if a.ID != row.ID && a.TelegramConnectorID != nil && *a.TelegramConnectorID != "" {
					taken[*a.TelegramConnectorID] = true
				}
			}
			notice, err := settings.Get[[]string](q, "deployNoticeChats")
			if err != nil {
				return nil, err
			}
			view, err := agentView(q, row, len(own), now)
			if err != nil {
				return nil, err
			}
			bots, err := telegramBots(q)
			if err != nil {
				return nil, err
			}
			free := []telegramBot{}
			for _, b := range bots {
				if !taken[b.ID] {
					free = append(free, b)
				}
			}
			ins, err := notes.ListInstructions(q, notes.ListOptions{AgentID: row.ID}, now)
			if err != nil {
				return nil, err
			}
			list := []agentPageChat{}
			for i, ch := range own {
				if i >= agentChatsLimit {
					break
				}
				list = append(list, agentPageChat{ID: ch.ID, Title: ch.Title, Channel: ch.Channel, Kind: ch.Kind, LastAt: ch.LastAt, Allowed: ch.Allowed, ReplyMode: ch.ReplyMode, Ack: ch.Ack, AckEmoji: ch.AckEmoji, DeployNotice: slices.Contains(notice, ch.ID)})
			}
			global, err := settings.Get[settings.ModelRoles](q, "modelRoles")
			if err != nil {
				return nil, err
			}
			options, err := httpapi.ModelOptions(q)
			if err != nil {
				return nil, err
			}
			mine := row.ModelRoles.V
			models := agentModels{Own: roleChains{orNone(mine.Default), orNone(mine.Fast), orNone(mine.Vision)}, Global: global, Connectors: options}
			return agentPage{Agent: view, Models: models, Bots: free, Instructions: instructionsOrEmpty(ins), Chats: list, ChatsTotal: len(own)}, nil
		},
	})

	// The Tools and Memory tabs load on their own: both ask the runtime, which
	// can be slow or down, and the settings form shouldn't wait on it.
	httpapi.Register(httpapi.Handler{
		Name:   "getAgentToolsFn",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("id", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			row, ok, err := agents.Get(c.DB(), id.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(agentGone)
			}
			return agentTools(c, row, c.Now())
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "getAgentMemoryFn",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("id", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			row, ok, err := agents.Get(c.DB(), id.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(agentGone)
			}
			return agentMemory(c, row)
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "saveAgentFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  agentFields(zod.F("id", zod.Optional(zod.String().Min(1).Max(100))), zod.F("slug", zod.Optional(zod.String().Trim().Max(24)))),
		Fn: func(c *httpapi.Call) (any, error) {
			var in agentInput
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			list := in.RecallReadContainers
			fields := agents.Input{
				Name: &in.Name, Persona: &in.Persona, GuestTools: &in.GuestTools, DailyTokenCap: &in.DailyTokenCap,
				RecallContainer: nullable(in.RecallContainer), RecallReadContainers: &list, RecallPerChat: &in.RecallPerChat,
				TelegramConnectorID: nullable(in.TelegramConnectorID), AvatarImage: nullable(in.AvatarImage), AvatarIcon: nullable(in.AvatarIcon), AvatarColor: nullable(in.AvatarColor),
				ModelRoles: in.ModelRoles,
			}
			var row db.Agent
			var err error
			if in.ID != nil {
				row, err = agents.Update(c.DB(), *in.ID, fields, c.Now())
			} else {
				if in.Slug != nil {
					fields.Slug = *in.Slug
				}
				row, err = agents.Create(c.DB(), fields, c.Now())
			}
			if err != nil {
				return nil, rethrowAgent(err)
			}
			return savedAgent{ID: row.ID, Slug: row.Slug}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "deleteAgentFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			ok, err := agents.Delete(c.DB(), id.(string), c.Now())
			if err != nil {
				return nil, rethrowAgent(err)
			}
			if !ok {
				return nil, httpapi.Fail(agentGone)
			}
			return okResult{OK: true}, nil
		},
	})

	// Tool permissions save at once from the Tools tab.
	httpapi.Register(httpapi.Handler{
		Name:   "setAgentToolPermissionsFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input: zod.Object(
			zod.F("id", agentID),
			zod.F("allowAll", zod.Optional(zod.Bool())),
			zod.F("tools", zod.Optional(zod.Record(zod.Nullable(zod.Enum("allow", "ask", "deny"))).Keys(zod.String().Max(64)))),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("id")
			var patch agents.PermissionsPatch
			if v, ok := o.Get("allowAll"); ok {
				b := v.(bool)
				patch.AllowAll = &b
			}
			if v, ok := o.Get("tools"); ok {
				ts := jsonx.NewMap[*string]()
				m := v.(*jsonx.Object)
				for _, k := range m.Keys() {
					p, _ := m.Get(k)
					if s, ok := p.(string); ok {
						ts.Set(k, &s)
					} else {
						ts.Set(k, nil)
					}
				}
				patch.Tools = ts
			}
			row, err := agents.SetToolPermissions(c.DB(), id.(string), patch, c.Now())
			if err != nil {
				return nil, rethrowAgent(err)
			}
			return toolPermissionsResult{ToolPermissions: permissionsOf(row)}, nil
		},
	})

	// Switch a chat's agent. The runtime saves the session to the old agent's
	// memory first; when it's down, the switch still happens without that save.
	httpapi.Register(httpapi.Handler{
		Name:   "setChatAgentFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg), zod.F("agentId", zod.Nullable(zod.String().Min(1).Max(100)))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			chatID, _ := o.Get("chatId")
			raw, _ := o.Get("agentId")
			var agent *string
			if s, ok := raw.(string); ok {
				agent = &s
			}
			rt, err := runtimeOf(c)
			if err == nil {
				id := ""
				if agent != nil {
					id = *agent
				}
				err = rt.SwitchAgent(chatID.(string), id)
				if err == nil {
					return savedFlag{Saved: true}, nil
				}
			}
			if !isRuntimeDown(err) {
				return nil, rethrowRuntime(err)
			}
			q := c.DB()
			if _, _, err := agents.Assign(q, chatID.(string), agent, c.Now()); err != nil {
				return nil, rethrowAgent(err)
			}
			if _, err := chats.RotateSession(q, chatID.(string), c.Now()); err != nil {
				return nil, err
			}
			return savedFlag{Saved: false}, nil
		},
	})

	// Groups only: answer @mentions and replies, or every message.
	httpapi.Register(httpapi.Handler{
		Name:   "setChatReplyModeFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg), zod.F("mode", zod.Enum("mention", "all"))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			chatID, _ := o.Get("chatId")
			mode, _ := o.Get("mode")
			_, ok, err := chats.SetReplyMode(c.DB(), chatID.(string), mode.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(chatGone)
			}
			return okResult{OK: true}, nil
		},
	})

	// WhatsApp's reaction while a reply is being written: on or off, and which emoji.
	httpapi.Register(httpapi.Handler{
		Name:   "setChatAckFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg), zod.F("on", zod.Optional(zod.Bool())), zod.F("emoji", zod.Optional(zod.String().Max(32)))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			chatID, _ := o.Get("chatId")
			var patch chats.AckPatch
			if v, ok := o.Get("on"); ok {
				b := v.(bool)
				patch.On = &b
			}
			if v, ok := o.Get("emoji"); ok {
				emoji, ok := ack.ParseEmoji(v.(string))
				if !ok {
					return nil, httpapi.Fail("Pick a single emoji.")
				}
				patch.Emoji = &emoji
			}
			_, ok, err := chats.SetAck(c.DB(), chatID.(string), patch)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(chatGone)
			}
			return okResult{OK: true}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "setChatAllowedFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg), zod.F("allowed", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			chatID, _ := o.Get("chatId")
			allowed, _ := o.Get("allowed")
			n, err := db.Changes(c.DB().Exec(`update "chats" set "allowed" = ? where "chats"."id" = ?`, db.Bool(allowed.(bool)), chatID))
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, httpapi.Fail(chatGone)
			}
			return okResult{OK: true}, nil
		},
	})

	// Off by default: a chat that has it on gets one message when a new version starts running.
	httpapi.Register(httpapi.Handler{
		Name:   "setChatDeployNoticeFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg), zod.F("on", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			chatID, _ := o.Get("chatId")
			on, _ := o.Get("on")
			ok, err := chatExists(c.DB(), chatID.(string))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpapi.Fail(chatGone)
			}
			if err := deploynotice.Set(c.DB(), chatID.(string), on.(bool), c.Now()); err != nil {
				return nil, err
			}
			return okResult{OK: true}, nil
		},
	})
}
