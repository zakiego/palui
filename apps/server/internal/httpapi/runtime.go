package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/deps"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// Runtime is the old control API (apps/runtime/src/control.ts) as in-process
// calls: what dashboard endpoints used to send to the runtime over HTTP. M8b's
// handlers call these. Each failure that had a status code is an *Error with
// the same status and message.
type Runtime struct {
	DB        *db.DB
	Engine    *chat.Engine
	Sup       *connectors.Supervisor
	Built     *deps.Built
	Bus       *chat.Bus
	Log       *logx.Logger
	StartedAt int64
	Clock     func() int64
}

func (r *Runtime) now() int64 {
	if r.Clock == nil {
		return time.Now().UnixMilli()
	}
	return r.Clock()
}

func (r *Runtime) log() *logx.Logger {
	if r.Log == nil {
		return logx.Silent
	}
	return r.Log
}

// RuntimeStatus is control.ts RuntimeStatus.
type RuntimeStatus struct {
	OK          bool                    `json:"ok"`
	StartedAt   int64                   `json:"startedAt"`
	Schema      string                  `json:"schema"`
	Connectors  []connectors.LiveStatus `json:"connectors"`
	TurnsPaused TurnsPaused             `json:"turnsPaused"`
	ActiveTurns []string                `json:"activeTurns"`
}

type TurnsPaused struct {
	Until  *int64  `json:"until"`
	Reason *string `json:"reason"`
}

// Status is GET /control/status.
func (r *Runtime) Status() (RuntimeStatus, error) {
	st, err := db.GetSchemaState(r.DB)
	if err != nil {
		return RuntimeStatus{}, err
	}
	schema := st.Version
	if !st.OK {
		schema = "not ok: " + st.Reason
	}
	paused := TurnsPaused{}
	if p, err := budget.ActivePause(r.DB, r.now()); err != nil {
		return RuntimeStatus{}, err
	} else if p != nil {
		paused = TurnsPaused{Until: &p.Until, Reason: &p.Reason}
	}
	active := r.Engine.ActiveTurns()
	if active == nil {
		active = []string{}
	}
	live := r.Sup.Status()
	if live == nil {
		live = []connectors.LiveStatus{}
	}
	return RuntimeStatus{OK: true, StartedAt: r.StartedAt, Schema: schema, Connectors: live, TurnsPaused: paused, ActiveTurns: active}, nil
}

// Reconcile is POST /control/reconcile: reconcile connectors, sweep the
// scheduler, and handle pending inbox rows (hook events), all in the background.
func (r *Runtime) Reconcile() {
	r.Sup.Poke()
	if r.Built != nil && r.Built.Scheduler != nil {
		r.Built.Scheduler.Poke()
	}
	Go(r.log(), "replay pending", func() {
		if _, err := r.Engine.ReplayPending(); err != nil {
			r.log().Error("replaying pending messages failed", logx.Fields{"error": err})
		}
	})
}

// Test is POST /control/test/:id.
func (r *Runtime) Test(id string) connectors.TestResult { return r.Sup.Test(id) }

// Action is POST /control/action/:id: a *connectors.StatusError keeps its status.
func (r *Runtime) Action(ctx context.Context, id, name string, input *jsonx.Object) (*jsonx.Object, error) {
	if name == "" {
		return nil, &Error{Status: 400, Message: "name is required"}
	}
	if input == nil {
		input = jsonx.NewMap[any]()
	}
	out, err := r.Sup.Action(ctx, id, name, input)
	if err != nil {
		var se *connectors.StatusError
		if errors.As(err, &se) {
			return nil, &Error{Status: se.Status, Message: se.Msg}
		}
		return nil, &Error{Status: 500, Message: err.Error()}
	}
	return out, nil
}

// Remove is POST /control/remove/:id: unlink, stop, delete, then move the
// connector's queued outbox rows to whatever channel can still send them.
func (r *Runtime) Remove(ctx context.Context, id string) connectors.RemoveResult {
	return r.Sup.Remove(ctx, id, func(ch string) {
		if _, _, err := outbox.RerouteChannelRows(r.DB, ch, r.now(), func(c string) outbox.Channel { return r.Sup.ChannelFor(c) }); err != nil {
			r.log().Error("rerouting outbox rows failed", logx.Fields{"channel": ch, "error": err})
		}
	})
}

// WebSent is the web chat send result.
type WebSent struct {
	OK        bool   `json:"ok"`
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
}

// WebSend is POST /control/web/send: the owner's dashboard chat message.
func (r *Runtime) WebSend(text string) (WebSent, error) {
	if strings.TrimSpace(text) == "" {
		return WebSent{}, &Error{Status: 400, Message: "text is required"}
	}
	res, err := r.Engine.Inbound(connectors.InboundMessage{Channel: "web", ConnectorID: "web", ChatExternalID: chats.WebChatExternalID, ChatKind: "dm", ChatTitle: "Palui (web)", SenderExternalID: "owner", Text: text, At: r.now()})
	if err != nil {
		return WebSent{}, err
	}
	if res == nil {
		return WebSent{}, &Error{Status: 500, Message: "The message was dropped"}
	}
	return WebSent{OK: true, ChatID: res.ChatID, MessageID: res.MessageID}, nil
}

// DecideApproval is POST /control/approvals/:id.
func (r *Runtime) DecideApproval(id string, approved bool) bool {
	return r.Engine.DecideApproval(id, approved, "dashboard")
}

// RunJob is POST /control/jobs/:id/run.
func (r *Runtime) RunJob(id string) (any, error) {
	if r.Built == nil || r.Built.Runner == nil {
		return nil, &Error{Status: 501, Message: "Jobs aren't wired into this runtime build yet"}
	}
	return r.Built.Runner.RunNow(id)
}

// StopChat is POST /control/stop/:chatId.
func (r *Runtime) StopChat(chatID string) bool { return r.Engine.StopChat(chatID) }

// SwitchAgent is POST /control/chats/:id/agent ("" = the default agent).
func (r *Runtime) SwitchAgent(chatID, agentID string) error {
	if _, err := r.Engine.SwitchAgent(chatID, agentID); err != nil {
		var ae *agents.Error
		if errors.As(err, &ae) {
			return &Error{Status: 400, Message: err.Error()}
		}
		return &Error{Status: 404, Message: err.Error()}
	}
	return nil
}

// AgentTools is POST /control/agents/:id/tools: the tools an agent is
// offered in the owner's own chat (DM-only tools count).
func (r *Runtime) AgentTools(ctx context.Context, agentID string) ([]tools.CatalogTool, error) {
	agent, ok, err := agents.Get(r.DB, agentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &Error{Status: 404, Message: "No such agent"}
	}
	web, err := chats.EnsureWebChat(r.DB, r.now())
	if err != nil {
		return nil, err
	}
	tz, err := settings.Timezone(r.DB)
	if err != nil {
		return nil, err
	}
	return tools.Catalog(ctx, r.Sup.Running(), tools.CatalogOptions{AgentID: agent.ID, ChatID: web.ID, Channel: "web", TZ: tz, Now: r.now(), Log: r.log()}), nil
}

// ParseReminder is POST /control/reminders/parse ("" tz = the settings timezone).
func (r *Runtime) ParseReminder(ctx context.Context, text, tz string) (reminders.ParsedReminder, error) {
	parse := r.Engine.Ctx.Deps.ParseReminder
	if parse == nil {
		return reminders.ParsedReminder{}, &Error{Status: 501, Message: "Reminder parsing isn't wired into this runtime build yet"}
	}
	if strings.TrimSpace(text) == "" {
		return reminders.ParsedReminder{}, &Error{Status: 400, Message: "text is required"}
	}
	chain, err := models.ResolveChain(r.DB, r.Sup, "fast")
	if err != nil {
		return reminders.ParsedReminder{}, err
	}
	if len(chain) == 0 {
		return reminders.ParsedReminder{}, &Error{Status: 503, Message: chat.ModelOffText}
	}
	if tz == "" {
		if tz, err = settings.Timezone(r.DB); err != nil {
			return reminders.ParsedReminder{}, err
		}
	}
	model, err := models.NewFailoverModel(chain, nil)
	if err != nil {
		return reminders.ParsedReminder{}, err
	}
	return parse(ctx, text, tz, r.now(), model)
}

// Error is an error with an HTTP status, for handlers and the runtime calls.
// Fields carries per-field problems a form can point at.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  *jsonx.Map[string]
}

func (e *Error) Error() string { return e.Message }

// statusOf maps an error to its HTTP status: *Error's own, 500 otherwise.
func statusOf(err error) int {
	var e *Error
	if errors.As(err, &e) && e.Status != 0 {
		return e.Status
	}
	return http.StatusInternalServerError
}
