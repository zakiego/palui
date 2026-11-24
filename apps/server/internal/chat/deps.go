package chat

import (
	"context"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/reminders"
)

// deps.ts: the parts of the engine built in later milestones are injected, so
// the engine compiles and runs without them. `palui serve` (M8) wires them.
// A nil field means that part isn't there: the router falls through to the
// next rule, the way the TS router does when a dep is undefined.

// ReminderActions is deps.ts's ReminderActions (M4 fills it from
// reminders/actions.ts). Each returns the reply and ok true when the input
// was its action.
type ReminderActions interface {
	// Telegram button data starting with "rem:".
	FromCallback(q db.Q, data, chatID string, now int64) (reply string, ok bool, err error)
	// WhatsApp-style text actions ("done", "tunda 1j") quoting a reminder.
	FromText(q db.Q, text, replyToChannelMsgID, chatID string, now int64) (reply string, ok bool, err error)
}

// ChatTrigger is the input of FlowActions.FromChat.
type ChatTrigger struct {
	Text      string
	ChatID    string
	MessageID string
	FromOwner bool
}

// FlowActions is deps.ts's FlowActions (M5): check-in answers ("fl:"),
// version approvals ("flowver:") and chat triggers.
type FlowActions interface {
	ReminderActions
	// FromChat starts a flow run when a chat trigger matches; true means the
	// flow took the message.
	FromChat(q db.Q, in ChatTrigger, now int64) (bool, error)
}

// ParsedReminder is control.ts's ParsedReminder (reminders.ParsedReminder,
// whose JSON keeps the TS keys).
type ParsedReminder = reminders.ParsedReminder

// Scheduler is deps.ts's scheduler (M4).
type Scheduler interface {
	Start() error
	Stop(ctx context.Context) error
	Poke()
}

// JobRunResult is what Jobs.RunNow returns.
type JobRunResult struct {
	OK     bool   `json:"ok"`
	RunID  string `json:"runId,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Jobs is deps.ts's jobs (M4).
type Jobs interface {
	RunNow(ctx context.Context, jobID string) (JobRunResult, error)
}

// Flows is what the engine needs from flows/service.ts and flows/checkin.ts
// (M5). Not a deps.ts field in TS (the TS engine imported those modules); the
// Go engine takes it as a dep to avoid an import cycle.
type Flows interface {
	// WakeRun is wakeFlowRun: a flow run's approval is decided, the flow
	// engine continues it.
	WakeRun(q db.Q, runID string, now int64) error
	// CheckinsPrompt is flowCheckinsPrompt ("" when there is nothing open).
	CheckinsPrompt(q db.Q, chatID string, now int64) (string, error)
	// ChatCommands lists the chat-trigger commands of the chat's enabled flows,
	// in listFlows order (helpText's flowCommands).
	ChatCommands(q db.Q, chatID string) ([]FlowCommand, error)
}

// FlowCommand is one of flowCommands' rows.
type FlowCommand struct {
	Command string
	Flow    string
}

// Outbound is what the router needs from outbound/service.ts (M4): messages to
// contacts held in their undo window.
type Outbound interface {
	// OwnerChatID is getOutbound(id).ownerChatId; ok false when there is no such row.
	OwnerChatID(q db.Q, id string) (ownerChatID string, ok bool, err error)
	// Cancel is cancelOutbound(id).message.
	Cancel(q db.Q, id string, now int64) (string, error)
	// HeldFromNotice is heldFromNotice: the held send a notice in this chat is about ("" if none).
	HeldFromNotice(q db.Q, chatID, channelMsgID string) (string, error)
	// NewestHeld is newestHeld(...)?.id ("" if none).
	NewestHeld(q db.Q, chatID string, now int64) (string, error)
}

// Deps is EngineDeps plus the Go-only boundaries above.
type Deps struct {
	ReminderActions ReminderActions
	FlowActions     FlowActions
	// ProactiveActions: buttons ("ndg:") and quoted replies on proactive
	// messages (docs/rfc/proactive.md 10).
	ProactiveActions ReminderActions
	ParseReminder    func(ctx context.Context, text, tz string, now int64, model models.LanguageModel) (ParsedReminder, error)
	Scheduler        Scheduler
	Jobs             Jobs
	Flows            Flows
	Outbound         Outbound
}
