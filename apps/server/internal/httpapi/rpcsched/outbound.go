package rpcsched

import (
	"net/http"
	"regexp"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// apps/app/src/lib/fns/outbound.ts: the Outbound page (kill switch and
// limits, contacts, scheduled messages and the send log).

// LogLimit is LOG_LIMIT: rows in the send log.
const LogLimit = 100

// MaxSignature is settings-schema.ts MAX_SIGNATURE.
const MaxSignature = 150

var crlf = regexp.MustCompile(`\r\n?`)

// outboundLimitsZod is outboundSettingsSchema.omit({ enabled: true }).
func outboundLimitsZod() *zod.ObjectS {
	return zod.Object(
		zod.F("perContactPerDay", zod.Int().Min(1).Max(50)),
		zod.F("perDay", zod.Int().Min(1).Max(200)),
		zod.F("holdSeconds", zod.Int().Min(0).Max(120)),
		zod.F("signatureOn", zod.Bool()),
		zod.F("signature", zod.TransformPipe(zod.String(), func(v any) any {
			s, _ := v.(string)
			return strx.Trim(crlf.ReplaceAllString(s, "\n"))
		}, zod.String().Max(MaxSignature))),
	)
}

func init() {
	httpapi.Register(httpapi.Handler{Name: "getOutbound", Method: http.MethodGet, Guard: httpapi.User, Fn: getOutbound})
	httpapi.Register(httpapi.Handler{
		Name: "setOutboundSwitch", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("enabled", zod.Bool())),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				Enabled bool `json:"enabled"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			n, err := outbound.SetEnabled(c.DB(), in.Enabled, c.Now())
			if err != nil {
				return nil, err
			}
			return obj("cancelled", n), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "saveOutboundLimits", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: outboundLimitsZod(),
		Fn: func(c *httpapi.Call) (any, error) {
			var in outbound.SettingsPatch
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			if _, err := outbound.SaveSettings(c.DB(), in, c.Now()); err != nil {
				return nil, friendly(err)
			}
			return obj("ok", true), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "addContactFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(
			zod.F("agentId", zod.String().Min(1).Max(100)),
			zod.F("name", zod.String().Trim().Min(1).Max(60)),
			zod.F("phone", zod.String().Trim().Max(30).Optional()),
			zod.F("chatId", zod.String().Max(100).Optional()),
			zod.F("policy", zod.Default(zod.Enum("ask", "allow"), "ask")),
			zod.F("note", zod.String().Trim().Max(200).Optional()),
		),
		Fn: addContact,
	})
	httpapi.Register(httpapi.Handler{
		Name: "setContactPolicy", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: zod.Object(zod.F("id", zod.String().Min(1).Max(100)), zod.F("policy", zod.Enum("ask", "allow"))),
		Fn: func(c *httpapi.Call) (any, error) {
			var in struct {
				ID     string `json:"id"`
				Policy string `json:"policy"`
			}
			if err := c.Decode(&in); err != nil {
				return nil, err
			}
			if _, err := outbound.UpdateContact(c.DB(), in.ID, outbound.ContactPatch{Policy: &in.Policy}, c.Now()); err != nil {
				return nil, friendly(err)
			}
			return obj("ok", true), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "removeContactFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: idInput(),
		Fn: func(c *httpapi.Call) (any, error) {
			n, err := outbound.RemoveContact(c.DB(), inputID(c), c.Now())
			if err != nil {
				return nil, friendly(err)
			}
			return obj("cancelled", n), nil
		},
	})
	httpapi.Register(httpapi.Handler{
		Name: "cancelOutboundFn", Method: http.MethodPost, Guard: httpapi.Writer,
		Input: idInput(),
		Fn: func(c *httpapi.Call) (any, error) {
			r, err := outbound.Cancel(c.DB(), inputID(c), c.Now())
			if err != nil {
				return nil, err
			}
			if !r.OK {
				return nil, httpapi.Fail("%s", r.Message)
			}
			return obj("ok", true), nil
		},
	})
}

// idInput is z.object({ id: z.string().min(1).max(100) }).
func idInput() *zod.ObjectS { return zod.Object(zod.F("id", zod.String().Min(1).Max(100))) }

func inputID(c *httpapi.Call) string {
	v, _ := c.Obj().Get("id")
	s, _ := v.(string)
	return s
}

func addContact(c *httpapi.Call) (any, error) {
	var in struct {
		AgentID string  `json:"agentId"`
		Name    string  `json:"name"`
		Phone   string  `json:"phone"`
		ChatID  string  `json:"chatId"`
		Policy  string  `json:"policy"`
		Note    *string `json:"note"`
	}
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	q := c.DB()
	if _, ok, err := agents.Get(q, in.AgentID); err != nil {
		return nil, err
	} else if !ok {
		return nil, httpapi.Fail("That agent doesn't exist anymore.")
	}
	row, err := outbound.AddContact(q, outbound.AddContactInput{AgentID: in.AgentID, Name: in.Name, Phone: in.Phone, ChatID: in.ChatID, Policy: in.Policy, Note: in.Note}, c.Now())
	if err != nil {
		return nil, friendly(err)
	}
	return obj("id", row.ID), nil
}

type outAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

type outContact struct {
	ID        string  `json:"id"`
	AgentID   string  `json:"agentId"`
	Name      string  `json:"name"`
	Channel   string  `json:"channel"`
	Address   string  `json:"address"`
	Policy    string  `json:"policy"`
	Note      *string `json:"note"`
	SentToday int64   `json:"sentToday"`
}

type outScheduled struct {
	ID      string  `json:"id"`
	AgentID string  `json:"agentId"`
	Contact string  `json:"contact"`
	Text    string  `json:"text"`
	FireAt  *int64  `json:"fireAt"`
	TZ      string  `json:"tz"`
	RRule   *string `json:"rrule"`
	Origin  string  `json:"origin"`
}

type outLog struct {
	ID         string  `json:"id"`
	AgentID    *string `json:"agentId"`
	Contact    string  `json:"contact"`
	Text       any     `json:"text"`
	Status     string  `json:"status"`
	Error      *string `json:"error"`
	Origin     *string `json:"origin"`
	ApprovedBy *string `json:"approvedBy"`
	CreatedAt  int64   `json:"createdAt"`
	SendAt     *int64  `json:"sendAt"`
	SentAt     *int64  `json:"sentAt"`
	OutboundID *string `json:"outboundId"`
}

type outDM struct {
	ID         string  `json:"id"`
	Title      *string `json:"title"`
	Channel    string  `json:"channel"`
	ExternalID string  `json:"externalId"`
}

type outboundPage struct {
	Agents         []outAgent        `json:"agents"`
	DefaultAgentID string            `json:"defaultAgentId"`
	Settings       settings.Outbound `json:"settings"`
	Usage          int64             `json:"usage"`
	Contacts       []outContact      `json:"contacts"`
	Scheduled      []outScheduled    `json:"scheduled"`
	Log            []outLog          `json:"log"`
	TelegramChats  []outDM           `json:"telegramChats"`
	Timezone       string            `json:"timezone"`
}

func getOutbound(c *httpapi.Call) (any, error) {
	q, now := c.DB(), c.Now()
	st, err := settings.GetOutbound(q)
	if err != nil {
		return nil, err
	}
	contactRows, err := outbound.ListContacts(q, nil)
	if err != nil {
		return nil, err
	}
	byID := map[string]db.Contact{}
	for _, r := range contactRows {
		byID[r.ID] = r
	}
	// Every agent has its own contacts (PLAN 19); null rows belong to the default agent.
	agentRows, err := agents.List(q, now)
	if err != nil {
		return nil, err
	}
	page := outboundPage{Settings: st, Agents: []outAgent{}, Contacts: []outContact{}, Scheduled: []outScheduled{}, Log: []outLog{}, TelegramChats: []outDM{}}
	for _, a := range agentRows {
		page.Agents = append(page.Agents, outAgent{ID: a.ID, Name: a.Name, IsDefault: a.IsDefault})
	}
	def, err := agents.GetDefault(q, now)
	if err != nil {
		return nil, err
	}
	page.DefaultAgentID = def.ID
	agentOf := func(id *string) string {
		if id == nil {
			return def.ID
		}
		return *id
	}
	for _, r := range contactRows {
		addr := r.Address
		if r.Channel == "whatsapp" {
			addr = outbound.FormatPhone(r.Address)
		}
		u, err := outbound.Usage(q, now, &r)
		if err != nil {
			return nil, err
		}
		var sent int64
		if u.Contact != nil {
			sent = *u.Contact
		}
		page.Contacts = append(page.Contacts, outContact{ID: r.ID, AgentID: agentOf(r.AgentID), Name: r.Name, Channel: r.Channel, Address: addr, Policy: r.Policy, Note: r.Note, SentToday: sent})
	}

	rows, err := q.Query("SELECT id, agent_id, contact_id, text, fire_at, tz, rrule, origin FROM outbound_messages WHERE status IN ('scheduled', 'firing') ORDER BY fire_at LIMIT 200")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s outScheduled
		var agentID *string
		var contactID string
		if err := rows.Scan(&s.ID, &agentID, &contactID, &s.Text, &s.FireAt, &s.TZ, &s.RRule, &s.Origin); err != nil {
			rows.Close()
			return nil, err
		}
		s.AgentID = agentOf(agentID)
		s.Contact = "Removed contact"
		if k, ok := byID[contactID]; ok {
			s.Contact = k.Name
		}
		page.Scheduled = append(page.Scheduled, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The log: every send is an outbox row with source "outbound:<id>".
	type sendRow struct {
		log       outLog
		payload   string
		mID       *string
		mAgent    *string
		mContact  *string
		mOrigin   *string
		mApproved *string
	}
	var sends []sendRow
	rows, err = q.Query(`SELECT o.id, o.payload, o.status, o.error, o.created_at, o.next_attempt_at, o.sent_at, m.id, m.agent_id, m.contact_id, m.origin, m.approved_by
		FROM outbox o LEFT JOIN outbound_messages m ON m.id = substr(o.source, ?)
		WHERE o.source LIKE 'outbound:%' ORDER BY o.created_at DESC LIMIT ?`, len("outbound:")+1, LogLimit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s sendRow
		if err := rows.Scan(&s.log.ID, &s.payload, &s.log.Status, &s.log.Error, &s.log.CreatedAt, &s.log.SendAt, &s.log.SentAt, &s.mID, &s.mAgent, &s.mContact, &s.mOrigin, &s.mApproved); err != nil {
			rows.Close()
			return nil, err
		}
		sends = append(sends, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Removed contacts keep their name in the log.
	nameOf := func(id string) (string, error) {
		if k, ok := byID[id]; ok {
			return k.Name, nil
		}
		k, ok, err := outbound.GetContact(q, id)
		if err != nil || !ok {
			return "Removed contact", err
		}
		return k.Name, nil
	}
	for _, s := range sends {
		l := s.log
		if p, err := jsonx.ParseObject([]byte(s.payload)); err == nil {
			l.Text, _ = p.Get("text")
		}
		l.Contact = "Unknown"
		if s.mID != nil {
			a := agentOf(s.mAgent)
			l.AgentID = &a
			if l.Contact, err = nameOf(*s.mContact); err != nil {
				return nil, err
			}
			l.Origin, l.ApprovedBy, l.OutboundID = s.mOrigin, s.mApproved, s.mID
		}
		page.Log = append(page.Log, l)
	}

	u, err := outbound.Usage(q, now, nil)
	if err != nil {
		return nil, err
	}
	page.Usage = u.Total

	var ownerTg *string
	if id, ok, err := owner.GetOwner(q, "telegram"); err != nil {
		return nil, err
	} else if ok {
		ownerTg = &id.ExternalID
	}
	rows, err = q.Query("SELECT id, title, channel, external_id FROM chats WHERE kind = 'dm' AND channel = 'telegram'")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d outDM
		if err := rows.Scan(&d.ID, &d.Title, &d.Channel, &d.ExternalID); err != nil {
			rows.Close()
			return nil, err
		}
		if ownerTg != nil && d.ExternalID == *ownerTg {
			continue
		}
		isContact := false
		for _, k := range contactRows {
			if k.ChatID == d.ID {
				isContact = true
				break
			}
		}
		if !isContact {
			page.TelegramChats = append(page.TelegramChats, d)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if page.Timezone, err = settings.Timezone(q); err != nil {
		return nil, err
	}
	return page, nil
}
