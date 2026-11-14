package rpcpeople

import (
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/chats.ts: the chat list, a transcript, the web chat
// input and approvals.

type chatAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

type chatAgentOption struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	IsDefault   bool    `json:"isDefault"`
	AvatarImage *string `json:"avatarImage"`
	AvatarIcon  *string `json:"avatarIcon"`
	AvatarColor *string `json:"avatarColor"`
}

type transcriptPage struct {
	*transcriptView
	Agent  chatAgent         `json:"agent"`
	Shared bool              `json:"shared"`
	Agents []chatAgentOption `json:"agents"`
}

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getChats",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Fn: func(c *httpapi.Call) (any, error) {
			web, err := chats.EnsureWebChat(c.DB(), c.Now())
			if err != nil {
				return nil, err
			}
			list, err := chatList(c.DB(), "")
			if err != nil {
				return nil, err
			}
			return struct {
				Chats     []chatItem `json:"chats"`
				WebChatID string     `json:"webChatId"`
			}{list, web.ID}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "getTranscript",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("chatId", chatIDArg)),
		Fn: func(c *httpapi.Call) (any, error) {
			q, now := c.DB(), c.Now()
			chatID, _ := c.Obj().Get("chatId")
			t, err := transcript(q, chatID.(string))
			if err != nil {
				return nil, err
			}
			row, ok, err := chats.Get(q, chatID.(string))
			if err != nil {
				return nil, err
			}
			if t == nil || !ok {
				return nil, httpapi.Fail(chatGone)
			}
			agent, err := agents.ForChat(q, row.AgentID, now)
			if err != nil {
				return nil, err
			}
			shared, err := agents.IsSharedChat(q, row)
			if err != nil {
				return nil, err
			}
			all, err := agents.List(q, now)
			if err != nil {
				return nil, err
			}
			options := make([]chatAgentOption, 0, len(all))
			for _, a := range all {
				options = append(options, chatAgentOption{ID: a.ID, Name: a.Name, IsDefault: a.IsDefault, AvatarImage: a.AvatarImage, AvatarIcon: a.AvatarIcon, AvatarColor: a.AvatarColor})
			}
			// Who answers here (PLAN 19.2), and the agents it can switch to.
			return transcriptPage{transcriptView: t, Agent: chatAgent{ID: agent.ID, Name: agent.Name, IsDefault: agent.IsDefault}, Shared: shared, Agents: options}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "sendWebMessage",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("text", zod.String().Trim().Min(1).Max(8000))),
		Fn: func(c *httpapi.Call) (any, error) {
			text, _ := c.Obj().Get("text")
			res, err := callRuntime(c, func(rt Runtime) (httpapi.WebSent, error) { return rt.WebSend(text.(string)) })
			if err != nil {
				return nil, err
			}
			first, err := settings.Get[settings.FirstRun](c.DB(), "firstRun")
			if err != nil {
				return nil, err
			}
			if !first.SaidHi {
				first.SaidHi = true
				if err := settings.Set(c.DB(), "firstRun", first, c.Now()); err != nil {
					return nil, err
				}
			}
			return res, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "decideApproval",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("approved", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			id, _ := o.Get("id")
			approved, _ := o.Get("approved")
			return callRuntime(c, func(rt Runtime) (okResult, error) {
				return okResult{OK: rt.DecideApproval(id.(string), approved.(bool))}, nil
			})
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "stopTurn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("chatId", chatIDArg)),
		Fn: func(c *httpapi.Call) (any, error) {
			chatID, _ := c.Obj().Get("chatId")
			return callRuntime(c, func(rt Runtime) (okResult, error) {
				return okResult{OK: rt.StopChat(chatID.(string))}, nil
			})
		},
	})
}
