package rpcpeople

import (
	"net/http"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chatrules"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// Chat rules of an agent: what members (and the owner) set from shared
// chats, the history of every add and remove, the member switch that allows
// it, and undo. Like members, the section saves at once.

type chatRuleView struct {
	ID        string `json:"id"`
	ChatID    string `json:"chatId"`
	ChatTitle string `json:"chatTitle"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// "Owner" or the member's name.
	By        string `json:"by"`
	FromOwner bool   `json:"fromOwner"`
	CreatedAt int64  `json:"createdAt"`
}

type chatRuleChangeView struct {
	ID        string `json:"id"`
	ChatID    string `json:"chatId"`
	ChatTitle string `json:"chatTitle"`
	Action    string `json:"action"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// Who made the change: "Owner" or the member's name.
	By        string `json:"by"`
	FromOwner bool   `json:"fromOwner"`
	CreatedAt int64  `json:"createdAt"`
	UndoneAt  *int64 `json:"undoneAt"`
}

type chatRulesPage struct {
	Rules   []chatRuleView       `json:"rules"`
	Changes []chatRuleChangeView `json:"changes"`
}

func chatRulesOf(q db.Q, agentID string) (chatRulesPage, error) {
	rules, err := chatrules.ForAgent(q, agentID)
	if err != nil {
		return chatRulesPage{}, err
	}
	changes, err := chatrules.Changes(q, agentID)
	if err != nil {
		return chatRulesPage{}, err
	}
	titles, err := chatTitles(q)
	if err != nil {
		return chatRulesPage{}, err
	}
	out := chatRulesPage{Rules: []chatRuleView{}, Changes: []chatRuleChangeView{}}
	for _, r := range rules {
		out.Rules = append(out.Rules, chatRuleView{ID: r.ID, ChatID: r.ChatID, ChatTitle: titles[r.ChatID], Title: r.Title, Body: r.Body, By: chatrules.AuthorLabel(r), FromOwner: r.MemberID == nil, CreatedAt: r.CreatedAt})
	}
	for _, c := range changes {
		by := c.ByName
		if c.ByMemberID == nil {
			by = "Owner"
		}
		out.Changes = append(out.Changes, chatRuleChangeView{ID: c.ID, ChatID: c.ChatID, ChatTitle: titles[c.ChatID], Action: c.Action, Title: c.Title, Body: c.Body, By: by, FromOwner: c.ByMemberID == nil, CreatedAt: c.CreatedAt, UndoneAt: c.UndoneAt})
	}
	return out, nil
}

func rethrowRule(err error) error {
	if chatrules.IsError(err) {
		return httpapi.Fail("%s", err.Error())
	}
	return err
}

func checkAgent(q db.Q, id string) error {
	if _, ok, err := agents.Get(q, id); err != nil {
		return err
	} else if !ok {
		return httpapi.Fail(agentGone)
	}
	return nil
}

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getChatRulesFn",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("id", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			if err := checkAgent(c.DB(), id.(string)); err != nil {
				return nil, err
			}
			return chatRulesOf(c.DB(), id.(string))
		},
	})

	// The owner's opt-in: may this member add and remove chat rules?
	httpapi.Register(httpapi.Handler{
		Name:   "setMemberRulesFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("agentId", agentID), zod.F("memberId", zod.String().Min(1).Max(100)), zod.F("on", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			agent, _ := o.Get("agentId")
			member, _ := o.Get("memberId")
			on, _ := o.Get("on")
			if err := members.SetRules(c.DB(), agent.(string), member.(string), on.(bool)); err != nil {
				return nil, rethrowMember(err)
			}
			return okResult{OK: true}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "deleteChatRuleFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("agentId", agentID), zod.F("id", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			agent, _ := o.Get("agentId")
			id, _ := o.Get("id")
			if _, err := chatrules.Remove(c.DB(), agent.(string), "", id.(string), chatrules.Owner(), "", c.Now()); err != nil {
				return nil, rethrowRule(err)
			}
			return okResult{OK: true}, nil
		},
	})

	httpapi.Register(httpapi.Handler{
		Name:   "undoChatRuleChangeFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("agentId", agentID), zod.F("id", zod.String().Min(1).Max(100))),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			agent, _ := o.Get("agentId")
			id, _ := o.Get("id")
			if _, err := chatrules.Undo(c.DB(), agent.(string), id.(string), c.Now()); err != nil {
				return nil, rethrowRule(err)
			}
			return okResult{OK: true}, nil
		},
	})
}
