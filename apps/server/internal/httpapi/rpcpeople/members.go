package rpcpeople

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/members"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// Members of an agent (PLAN 19.4, 19.7). They have their own endpoints and
// aren't part of getAgentPage: the section saves at once, so it never belongs
// to the agent form's dirty state.

// People the add-member dialog offers at most, newest first.
const peopleLimit = 50

type memberAccountView struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	// What the owner typed or picked: the bare JID, LID or Telegram id.
	Address string `json:"address"`
	// For display: "+62 812-0000-0001" for a phone JID, else the address.
	Label string `json:"label"`
}

type memberAgent struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type memberView struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Accounts []memberAccountView `json:"accounts"`
	// Every agent the member is in, this one included.
	Agents []memberAgent `json:"agents"`
	// May add and remove chat rules for this agent (members of it only).
	CanSetRules bool `json:"canSetRules"`
}

type person struct {
	Channel string  `json:"channel"`
	Address string  `json:"address"`
	Label   string  `json:"label"`
	Name    *string `json:"name"`
	LastAt  int64   `json:"lastAt"`
}

type agentMembers struct {
	// In this agent.
	Members []memberView `json:"members"`
	// Members of other agents, to add with one click.
	Others []memberView `json:"others"`
	// Wrote in a chat, aren't the owner and aren't an account yet.
	People []person `json:"people"`
}

type savedMember struct {
	ID string `json:"id"`
}

type memberAccountInput struct {
	Channel string `json:"channel"`
	Address string `json:"address"`
}

type memberInput struct {
	AgentID  string               `json:"agentId"`
	ID       *string              `json:"id"`
	Name     string               `json:"name"`
	Accounts []memberAccountInput `json:"accounts"`
}

// rethrowMember: a members.Error's message is shown as is.
func rethrowMember(err error) error {
	var me *members.Error
	if errors.As(err, &me) {
		return httpapi.Fail("%s", me.Msg)
	}
	return err
}

// accountLabel formats a phone JID as a phone number; anything else is shown
// as it is.
func accountLabel(channel, address string) string {
	if channel == db.ChannelWhatsApp {
		if digits, ok := strings.CutSuffix(address, "@s.whatsapp.net"); ok {
			return outbound.FormatPhone(digits)
		}
	}
	return address
}

func toMemberView(v members.View, names map[string]memberAgent) memberView {
	out := memberView{ID: v.ID, Name: v.Name, Accounts: []memberAccountView{}, Agents: []memberAgent{}}
	for _, a := range v.Accounts {
		out.Accounts = append(out.Accounts, memberAccountView{ID: a.ID, Channel: a.Channel, Address: a.ExternalID, Label: accountLabel(a.Channel, a.ExternalID)})
	}
	for _, id := range v.AgentIDs {
		if a, ok := names[id]; ok {
			out.Agents = append(out.Agents, a)
		}
	}
	return out
}

func agentMembersOf(q db.Q, agentID string, now int64) (agentMembers, error) {
	all, err := members.All(q)
	if err != nil {
		return agentMembers{}, err
	}
	list, err := agents.List(q, now)
	if err != nil {
		return agentMembers{}, err
	}
	names := map[string]memberAgent{}
	for _, a := range list {
		names[a.ID] = memberAgent{ID: a.ID, Slug: a.Slug, Name: a.Name}
	}
	rulesOn, err := members.RulesOn(q, agentID)
	if err != nil {
		return agentMembers{}, err
	}
	out := agentMembers{Members: []memberView{}, Others: []memberView{}}
	taken := map[string]bool{}
	for _, m := range all {
		for _, a := range m.Accounts {
			taken[a.Channel+"\x00"+a.ExternalID] = true
			if a.AltID != nil {
				taken[a.Channel+"\x00"+*a.AltID] = true
			}
		}
		v := toMemberView(m, names)
		in := false
		for _, id := range m.AgentIDs {
			if id == agentID {
				in = true
			}
		}
		if in {
			v.CanSetRules = rulesOn[m.ID]
			out.Members = append(out.Members, v)
		} else {
			out.Others = append(out.Others, v)
		}
	}
	out.People, err = peopleWhoWrote(q, taken)
	return out, err
}

// peopleWhoWrote (D8): senders of every chat plus DM chats, which may have no
// stored message (a partner's first DM is dropped before setup). Leaves out
// the owner, hook senders and accounts in taken.
func peopleWhoWrote(q db.Q, taken map[string]bool) ([]person, error) {
	byKey := map[string]*person{}
	add := func(channel, address string, name *string, at int64) error {
		key := channel + "\x00" + address
		if address == "" || taken[key] {
			return nil
		}
		if name != nil && strings.TrimSpace(*name) == "" {
			name = nil
		}
		if p, ok := byKey[key]; ok {
			if at > p.LastAt {
				p.LastAt = at
			}
			if p.Name == nil {
				p.Name = name
			}
			return nil
		}
		isOwner, err := owner.IsOwner(q, owner.Sender{Channel: channel, ExternalID: address})
		if err != nil || isOwner {
			taken[key] = true
			return err
		}
		byKey[key] = &person{Channel: channel, Address: address, Label: accountLabel(channel, address), Name: name, LastAt: at}
		return nil
	}
	type row struct {
		channel, address string
		name             *string
		at               int64
	}
	scan := func(s db.Scanner) (row, error) {
		var r row
		return r, s.Scan(&r.channel, &r.address, &r.name, &r.at)
	}
	rows, err := q.Query(`SELECT c.channel, m.sender_id, max(m.sender_name), max(m.created_at) FROM messages m JOIN chats c ON c.id = m.chat_id
		WHERE m.role = 'user' AND m.from_owner = 0 AND m.sender_id IS NOT NULL AND m.sender_id NOT LIKE 'hook:%' AND c.channel IN ('whatsapp', 'telegram')
		GROUP BY c.channel, m.sender_id`)
	senders, err := db.All(rows, err, scan)
	if err != nil {
		return nil, err
	}
	rows, err = q.Query(`SELECT channel, external_id, title, coalesce(last_message_at, created_at) FROM chats WHERE kind = 'dm' AND channel IN ('whatsapp', 'telegram')`)
	dms, err := db.All(rows, err, scan)
	if err != nil {
		return nil, err
	}
	for _, r := range append(senders, dms...) {
		if err := add(r.channel, r.address, r.name, r.at); err != nil {
			return nil, err
		}
	}
	out := make([]person, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastAt != out[j].LastAt {
			return out[i].LastAt > out[j].LastAt
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		return out[i].Address < out[j].Address
	})
	if len(out) > peopleLimit {
		out = out[:peopleLimit]
	}
	return out, nil
}

func init() {
	httpapi.Register(httpapi.Handler{
		Name:   "getAgentMembersFn",
		Method: http.MethodGet,
		Guard:  httpapi.User,
		Input:  zod.Object(zod.F("id", agentID)),
		Fn: func(c *httpapi.Call) (any, error) {
			id, _ := c.Obj().Get("id")
			q := c.DB()
			if _, ok, err := agents.Get(q, id.(string)); err != nil {
				return nil, err
			} else if !ok {
				return nil, httpapi.Fail(agentGone)
			}
			return agentMembersOf(q, id.(string), c.Now())
		},
	})

	// Without id: a new member, linked to agentId. With id: rename and
	// replace accounts.
	httpapi.Register(httpapi.Handler{
		Name:   "saveMemberFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input: zod.Object(
			zod.F("agentId", agentID),
			zod.F("id", zod.Optional(zod.String().Min(1).Max(100))),
			zod.F("name", zod.String().Trim().Min(1).Max(members.MaxName)),
			zod.F("accounts", zod.Array(zod.Object(
				zod.F("channel", zod.Enum("whatsapp", "telegram")),
				zod.F("address", zod.String().Trim().Min(1).Max(64)),
			)).Min(1).Max(members.MaxAccounts)),
		),
		Fn: func(c *httpapi.Call) (any, error) {
			var in memberInput
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			accounts := make([]members.AccountInput, 0, len(in.Accounts))
			for _, a := range in.Accounts {
				accounts = append(accounts, members.AccountInput{Channel: a.Channel, Address: a.Address})
			}
			now := c.Now()
			var id string
			err := c.DB().Tx(func(tx db.Q) error {
				if in.ID != nil {
					v, err := members.Update(tx, *in.ID, in.Name, accounts, now)
					id = v.ID
					return err
				}
				v, err := members.Create(tx, in.Name, accounts, now)
				if err != nil {
					return err
				}
				id = v.ID
				return members.Link(tx, in.AgentID, v.ID, now)
			})
			if err != nil {
				return nil, rethrowMember(err)
			}
			return savedMember{ID: id}, nil
		},
	})

	// Add a member to an agent, or take them out (the last agent deletes them).
	httpapi.Register(httpapi.Handler{
		Name:   "setAgentMemberFn",
		Method: http.MethodPost,
		Guard:  httpapi.Writer,
		Input:  zod.Object(zod.F("agentId", agentID), zod.F("memberId", zod.String().Min(1).Max(100)), zod.F("on", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			o := c.Obj()
			agent, _ := o.Get("agentId")
			member, _ := o.Get("memberId")
			on, _ := o.Get("on")
			var err error
			if on.(bool) {
				err = members.Link(c.DB(), agent.(string), member.(string), c.Now())
			} else {
				_, err = members.Unlink(c.DB(), agent.(string), member.(string))
			}
			if err != nil {
				return nil, rethrowMember(err)
			}
			return okResult{OK: true}, nil
		},
	})
}
