package db

import (
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Row types for the tables, in the field order of packages/db/src/schema.ts so
// their JSON matches the TS rows. Each table has a column list (always named:
// the live column order differs from schema.ts) and a scan function that reads
// that list. M1 covers the tables its domains touch; later milestones add theirs.

// Enum-like values (schema.ts).
const (
	ChannelTelegram = "telegram"
	ChannelWhatsApp = "whatsapp"
	ChannelWeb      = "web"
)

var ConnectorKinds = []string{"channel", "model", "memory", "tools", "skill"}
var ConnectorStatuses = []string{"not_set_up", "connecting", "on", "paused", "error"}

// --- connectors ---------------------------------------------------------------

type Connector struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Kind         string        `json:"kind"`
	Label        string        `json:"label"`
	Enabled      bool          `json:"enabled"`
	Status       string        `json:"status"`
	StatusDetail *string       `json:"statusDetail"`
	Trust        string        `json:"trust"`
	Config       *jsonx.Object `json:"config"`
	AgentID      *string       `json:"agentId"`
	Secrets      *string       `json:"secrets"`
	ConfigHash   string        `json:"configHash"`
	ErrorSince   *int64        `json:"errorSince"`
	AlertedAt    *int64        `json:"alertedAt"`
	SortOrder    int64         `json:"sortOrder"`
	CreatedAt    int64         `json:"createdAt"`
	UpdatedAt    int64         `json:"updatedAt"`
}

const ConnectorCols = "id, type, kind, label, enabled, status, status_detail, trust, config, agent_id, secrets, config_hash, error_since, alerted_at, sort_order, created_at, updated_at"

func ScanConnector(s Scanner) (Connector, error) {
	var c Connector
	var cfg JSON[*jsonx.Object]
	err := s.Scan(&c.ID, &c.Type, &c.Kind, &c.Label, &c.Enabled, &c.Status, &c.StatusDetail, &c.Trust, &cfg, &c.AgentID, &c.Secrets, &c.ConfigHash, &c.ErrorSince, &c.AlertedAt, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt)
	c.Config = cfg.V
	if c.Config == nil {
		c.Config = jsonx.NewMap[any]()
	}
	return c, err
}

// --- identities, claims -----------------------------------------------------------

type Identity struct {
	ID          string  `json:"id"`
	Channel     string  `json:"channel"`
	ExternalID  string  `json:"externalId"`
	AltID       *string `json:"altId"`
	DisplayName *string `json:"displayName"`
	Role        string  `json:"role"`
	CreatedAt   int64   `json:"createdAt"`
}

const IdentityCols = "id, channel, external_id, alt_id, display_name, role, created_at"

func ScanIdentity(s Scanner) (Identity, error) {
	var r Identity
	err := s.Scan(&r.ID, &r.Channel, &r.ExternalID, &r.AltID, &r.DisplayName, &r.Role, &r.CreatedAt)
	return r, err
}

// --- members, member_accounts, agent_members (PLAN 19.4) ---------------------------

type Member struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

const MemberCols = "id, name, created_at, updated_at"

func ScanMember(s Scanner) (Member, error) {
	var r Member
	err := s.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

type MemberAccount struct {
	ID          string  `json:"id"`
	MemberID    string  `json:"memberId"`
	Channel     string  `json:"channel"`
	ExternalID  string  `json:"externalId"`
	AltID       *string `json:"altId"`
	DisplayName *string `json:"displayName"`
	CreatedAt   int64   `json:"createdAt"`
}

const MemberAccountCols = "id, member_id, channel, external_id, alt_id, display_name, created_at"

func ScanMemberAccount(s Scanner) (MemberAccount, error) {
	var r MemberAccount
	err := s.Scan(&r.ID, &r.MemberID, &r.Channel, &r.ExternalID, &r.AltID, &r.DisplayName, &r.CreatedAt)
	return r, err
}

type AgentMember struct {
	AgentID   string `json:"agentId"`
	MemberID  string `json:"memberId"`
	CreatedAt int64  `json:"createdAt"`
}

const AgentMemberCols = "agent_id, member_id, created_at"

func ScanAgentMember(s Scanner) (AgentMember, error) {
	var r AgentMember
	err := s.Scan(&r.AgentID, &r.MemberID, &r.CreatedAt)
	return r, err
}

// --- chats, sessions, messages ---------------------------------------------------

type Chat struct {
	ID            string  `json:"id"`
	Channel       string  `json:"channel"`
	ExternalID    string  `json:"externalId"`
	Title         string  `json:"title"`
	Kind          string  `json:"kind"`
	SessionID     *string `json:"sessionId"`
	ConnectorID   string  `json:"connectorId"`
	Allowed       bool    `json:"allowed"`
	AgentID       *string `json:"agentId"`
	ReplyMode     string  `json:"replyMode"`
	Ack           bool    `json:"ack"`
	AckEmoji      string  `json:"ackEmoji"`
	LastMessageAt *int64  `json:"lastMessageAt"`
	CreatedAt     int64   `json:"createdAt"`
}

const ChatCols = "id, channel, external_id, title, kind, session_id, connector_id, allowed, agent_id, reply_mode, ack, ack_emoji, last_message_at, created_at"

func ScanChat(s Scanner) (Chat, error) {
	var c Chat
	err := s.Scan(&c.ID, &c.Channel, &c.ExternalID, &c.Title, &c.Kind, &c.SessionID, &c.ConnectorID, &c.Allowed, &c.AgentID, &c.ReplyMode, &c.Ack, &c.AckEmoji, &c.LastMessageAt, &c.CreatedAt)
	return c, err
}

type Session struct {
	ID              string             `json:"id"`
	ChatID          string             `json:"chatId"`
	Summary         *string            `json:"summary"`
	SummarizedUntil *int64             `json:"summarizedUntil"`
	SavedToRecallAt *int64             `json:"savedToRecallAt"`
	LoadedTools     NullJSON[[]string] `json:"loadedTools"`
	CreatedAt       int64              `json:"createdAt"`
	ClosedAt        *int64             `json:"closedAt"`
}

const SessionCols = "id, chat_id, summary, summarized_until, saved_to_recall_at, loaded_tools, created_at, closed_at"

func ScanSession(s Scanner) (Session, error) {
	var r Session
	err := s.Scan(&r.ID, &r.ChatID, &r.Summary, &r.SummarizedUntil, &r.SavedToRecallAt, &r.LoadedTools, &r.CreatedAt, &r.ClosedAt)
	return r, err
}

type Message struct {
	ID                  string  `json:"id"`
	ChatID              string  `json:"chatId"`
	SessionID           *string `json:"sessionId"`
	Role                string  `json:"role"`
	Content             RawJSON `json:"content"`
	Text                string  `json:"text"`
	ChannelMsgID        *string `json:"channelMsgId"`
	ReplyToChannelMsgID *string `json:"replyToChannelMsgId"`
	SenderID            *string `json:"senderId"`
	SenderName          *string `json:"senderName"`
	FromOwner           bool    `json:"fromOwner"`
	Status              string  `json:"status"`
	IgnoreReason        *string `json:"ignoreReason"`
	RunID               *string `json:"runId"`
	CreatedAt           int64   `json:"createdAt"`
}

const MessageCols = "id, chat_id, session_id, role, content, text, channel_msg_id, reply_to_channel_msg_id, sender_id, sender_name, from_owner, status, ignore_reason, run_id, created_at"

func ScanMessage(s Scanner) (Message, error) {
	var m Message
	err := s.Scan(&m.ID, &m.ChatID, &m.SessionID, &m.Role, &m.Content, &m.Text, &m.ChannelMsgID, &m.ReplyToChannelMsgID, &m.SenderID, &m.SenderName, &m.FromOwner, &m.Status, &m.IgnoreReason, &m.RunID, &m.CreatedAt)
	return m, err
}

// --- tool calls, approvals ---------------------------------------------------------

type ToolCall struct {
	ID            string  `json:"id"`
	RunID         string  `json:"runId"`
	ConnectorID   *string `json:"connectorId"`
	Tool          string  `json:"tool"`
	Class         string  `json:"class"`
	Args          RawJSON `json:"args"`
	ResultPreview *string `json:"resultPreview"`
	Status        string  `json:"status"`
	Ms            *int64  `json:"ms"`
	CreatedAt     int64   `json:"createdAt"`
}

const ToolCallCols = "id, run_id, connector_id, tool, class, args, result_preview, status, ms, created_at"

func ScanToolCall(s Scanner) (ToolCall, error) {
	var t ToolCall
	err := s.Scan(&t.ID, &t.RunID, &t.ConnectorID, &t.Tool, &t.Class, &t.Args, &t.ResultPreview, &t.Status, &t.Ms, &t.CreatedAt)
	return t, err
}

type Approval struct {
	ID          string        `json:"id"`
	RunID       string        `json:"runId"`
	ChatID      string        `json:"chatId"`
	AskChatID   *string       `json:"askChatId"`
	ApprovalID  string        `json:"approvalId"`
	ToolCallID  *string       `json:"toolCallId"`
	Tool        string        `json:"tool"`
	ToolClass   string        `json:"toolClass"`
	ArgsPreview *string       `json:"argsPreview"`
	Messages    RawJSON       `json:"messages"`
	Context     *jsonx.Object `json:"context"`
	PromptMsgID *string       `json:"promptMsgId"`
	ExpiresAt   int64         `json:"expiresAt"`
	Status      string        `json:"status"`
	DecidedAt   *int64        `json:"decidedAt"`
	CreatedAt   int64         `json:"createdAt"`
}

const ApprovalCols = "id, run_id, chat_id, ask_chat_id, approval_id, tool_call_id, tool, tool_class, args_preview, messages, context, prompt_msg_id, expires_at, status, decided_at, created_at"

func ScanApproval(s Scanner) (Approval, error) {
	var a Approval
	var ctx JSON[*jsonx.Object]
	err := s.Scan(&a.ID, &a.RunID, &a.ChatID, &a.AskChatID, &a.ApprovalID, &a.ToolCallID, &a.Tool, &a.ToolClass, &a.ArgsPreview, &a.Messages, &ctx, &a.PromptMsgID, &a.ExpiresAt, &a.Status, &a.DecidedAt, &a.CreatedAt)
	a.Context = ctx.V
	if a.Context == nil {
		a.Context = jsonx.NewMap[any]()
	}
	return a, err
}

// --- saved attachments, shares ----------------------------------------------------

// AttachmentMeta is a saved_attachments row without `data`.
type AttachmentMeta struct {
	ID          string  `json:"id"`
	ChatID      string  `json:"chatId"`
	MessageID   *string `json:"messageId"`
	RunID       *string `json:"runId"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Kind        string  `json:"kind"`
	MimeType    string  `json:"mimeType"`
	Filename    *string `json:"filename"`
	Size        int64   `json:"size"`
	Sha256      string  `json:"sha256"`
	CreatedAt   int64   `json:"createdAt"`
}

const AttachmentMetaCols = "id, chat_id, message_id, run_id, title, description, kind, mime_type, filename, size, sha256, created_at"

func ScanAttachmentMeta(s Scanner) (AttachmentMeta, error) {
	var a AttachmentMeta
	err := s.Scan(&a.ID, &a.ChatID, &a.MessageID, &a.RunID, &a.Title, &a.Description, &a.Kind, &a.MimeType, &a.Filename, &a.Size, &a.Sha256, &a.CreatedAt)
	return a, err
}

type Share struct {
	ID           string `json:"id"`
	AttachmentID string `json:"attachmentId"`
	TokenHash    string `json:"tokenHash"`
	Visibility   string `json:"visibility"`
	CreatedBy    string `json:"createdBy"`
	ExpiresAt    int64  `json:"expiresAt"`
	RevokedAt    *int64 `json:"revokedAt"`
	OpenCount    int64  `json:"openCount"`
	LastOpenedAt *int64 `json:"lastOpenedAt"`
	CreatedAt    int64  `json:"createdAt"`
}

const ShareCols = "id, attachment_id, token_hash, visibility, created_by, expires_at, revoked_at, open_count, last_opened_at, created_at"

func ScanShare(s Scanner) (Share, error) {
	var r Share
	err := s.Scan(&r.ID, &r.AttachmentID, &r.TokenHash, &r.Visibility, &r.CreatedBy, &r.ExpiresAt, &r.RevokedAt, &r.OpenCount, &r.LastOpenedAt, &r.CreatedAt)
	return r, err
}

// --- outbox -------------------------------------------------------------------------

type OutboxAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// OutboxPayload is outbox.payload.
type OutboxPayload struct {
	Text                string         `json:"text"`
	Actions             []OutboxAction `json:"actions,omitempty"`
	ReplyToChannelMsgID string         `json:"replyToChannelMsgId,omitempty"`
	// "plain" or "markdown".
	Format string `json:"format,omitempty"`
}

type Outbox struct {
	ID            string        `json:"id"`
	ChatID        string        `json:"chatId"`
	Channel       string        `json:"channel"`
	ConnectorID   *string       `json:"connectorId"`
	Payload       OutboxPayload `json:"payload"`
	Status        string        `json:"status"`
	Attempts      int64         `json:"attempts"`
	NextAttemptAt int64         `json:"nextAttemptAt"`
	ClaimedAt     *int64        `json:"claimedAt"`
	ChannelMsgID  *string       `json:"channelMsgId"`
	Source        string        `json:"source"`
	Error         *string       `json:"error"`
	FallbackFrom  *string       `json:"fallbackFrom"`
	CreatedAt     int64         `json:"createdAt"`
	ExpiresAt     *int64        `json:"expiresAt"`
	SentAt        *int64        `json:"sentAt"`
}

const OutboxCols = "id, chat_id, channel, connector_id, payload, status, attempts, next_attempt_at, claimed_at, channel_msg_id, source, error, fallback_from, created_at, expires_at, sent_at"

func ScanOutbox(s Scanner) (Outbox, error) {
	var o Outbox
	var p JSON[OutboxPayload]
	err := s.Scan(&o.ID, &o.ChatID, &o.Channel, &o.ConnectorID, &p, &o.Status, &o.Attempts, &o.NextAttemptAt, &o.ClaimedAt, &o.ChannelMsgID, &o.Source, &o.Error, &o.FallbackFrom, &o.CreatedAt, &o.ExpiresAt, &o.SentAt)
	o.Payload = p.V
	return o, err
}

// --- events -------------------------------------------------------------------------

type Event struct {
	ID          int64         `json:"id"`
	At          int64         `json:"at"`
	Level       string        `json:"level"`
	ConnectorID *string       `json:"connectorId"`
	Kind        string        `json:"kind"`
	Message     string        `json:"message"`
	Data        *jsonx.Object `json:"data"`
}

const EventCols = "id, at, level, connector_id, kind, message, data"

func ScanEvent(s Scanner) (Event, error) {
	var e Event
	var data NullJSON[*jsonx.Object]
	err := s.Scan(&e.ID, &e.At, &e.Level, &e.ConnectorID, &e.Kind, &e.Message, &data)
	if data.Valid {
		e.Data = data.V
	}
	return e, err
}

// --- hooks --------------------------------------------------------------------------

type Hook struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	Mode      string `json:"mode"`
	ChatID    string `json:"chatId"`
	Enabled   bool   `json:"enabled"`
	LastAt    *int64 `json:"lastAt"`
	CreatedAt int64  `json:"createdAt"`
}

const HookCols = "id, slug, name, secret, mode, chat_id, enabled, last_at, created_at"

func ScanHook(s Scanner) (Hook, error) {
	var h Hook
	err := s.Scan(&h.ID, &h.Slug, &h.Name, &h.Secret, &h.Mode, &h.ChatID, &h.Enabled, &h.LastAt, &h.CreatedAt)
	return h, err
}

// --- custom_commands --------------------------------------------------------------

// CustomCommand is a custom_commands row. Name has no leading slash. AgentID
// nil means every agent; an agent's own command beats a global one.
type CustomCommand struct {
	ID           string  `json:"id"`
	AgentID      *string `json:"agentId"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Prompt       string  `json:"prompt"`
	AttachReport bool    `json:"attachReport"`
	Enabled      bool    `json:"enabled"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

const CustomCommandCols = "id, agent_id, name, description, prompt, attach_report, enabled, created_at, updated_at"

func ScanCustomCommand(s Scanner) (CustomCommand, error) {
	var r CustomCommand
	err := s.Scan(&r.ID, &r.AgentID, &r.Name, &r.Description, &r.Prompt, &r.AttachReport, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// --- instructions, preferences -----------------------------------------------------

type Instruction struct {
	ID        string  `json:"id"`
	AgentID   *string `json:"agentId"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	Enabled   bool    `json:"enabled"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

const InstructionCols = "id, agent_id, title, body, enabled, created_at, updated_at"

func ScanInstruction(s Scanner) (Instruction, error) {
	var r Instruction
	err := s.Scan(&r.ID, &r.AgentID, &r.Title, &r.Body, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

type Preference struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updatedAt"`
}

const PreferenceCols = "key, value, updated_at"

func ScanPreference(s Scanner) (Preference, error) {
	var r Preference
	err := s.Scan(&r.Key, &r.Value, &r.UpdatedAt)
	return r, err
}

// --- agents -------------------------------------------------------------------------

// ToolPermissions is agents.tool_permissions. Tools keeps the stored key order.
type ToolPermissions struct {
	AllowAll bool               `json:"allowAll"`
	Tools    *jsonx.Map[string] `json:"tools"`
}

type Agent struct {
	ID                   string                    `json:"id"`
	Slug                 string                    `json:"slug"`
	Name                 string                    `json:"name"`
	Persona              string                    `json:"persona"`
	IsDefault            bool                      `json:"isDefault"`
	RecallContainer      *string                   `json:"recallContainer"`
	RecallReadContainers NullJSON[[]string]        `json:"recallReadContainers"`
	RecallPerChat        bool                      `json:"recallPerChat"`
	OwnerPowers          string                    `json:"ownerPowers"`
	GuestTools           string                    `json:"guestTools"`
	DailyTokenCap        int64                     `json:"dailyTokenCap"`
	ToolPermissions      NullJSON[ToolPermissions] `json:"toolPermissions"`
	TelegramConnectorID  *string                   `json:"telegramConnectorId"`
	AvatarImage          *string                   `json:"avatarImage"`
	AvatarIcon           *string                   `json:"avatarIcon"`
	AvatarColor          *string                   `json:"avatarColor"`
	CreatedAt            int64                     `json:"createdAt"`
	UpdatedAt            int64                     `json:"updatedAt"`
}

const AgentCols = "id, slug, name, persona, is_default, recall_container, recall_read_containers, recall_per_chat, owner_powers, guest_tools, daily_token_cap, tool_permissions, telegram_connector_id, avatar_image, avatar_icon, avatar_color, created_at, updated_at"

func ScanAgent(s Scanner) (Agent, error) {
	var a Agent
	err := s.Scan(&a.ID, &a.Slug, &a.Name, &a.Persona, &a.IsDefault, &a.RecallContainer, &a.RecallReadContainers, &a.RecallPerChat, &a.OwnerPowers, &a.GuestTools, &a.DailyTokenCap, &a.ToolPermissions, &a.TelegramConnectorID, &a.AvatarImage, &a.AvatarIcon, &a.AvatarColor, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// --- runs, reports (M3) ---------------------------------------------------------------

// Run is a runs row. Trigger is message, schedule, hook, approval, reminder or
// flow; Status is running, ok, error, aborted, awaiting_approval or budget.
type Run struct {
	ID               string  `json:"id"`
	ChatID           *string `json:"chatId"`
	AgentID          *string `json:"agentId"`
	JobID            *string `json:"jobId"`
	Trigger          string  `json:"trigger"`
	Model            *string `json:"model"`
	ModelConnectorID *string `json:"modelConnectorId"`
	TokensIn         int64   `json:"tokensIn"`
	TokensOut        int64   `json:"tokensOut"`
	TokensCached     int64   `json:"tokensCached"`
	TokensCacheWrite int64   `json:"tokensCacheWrite"`
	Steps            int64   `json:"steps"`
	Tainted          bool    `json:"tainted"`
	Status           string  `json:"status"`
	Error            *string `json:"error"`
	ResultPreview    *string `json:"resultPreview"`
	StartedAt        int64   `json:"startedAt"`
	Ms               *int64  `json:"ms"`
}

const RunCols = "id, chat_id, agent_id, job_id, trigger, model, model_connector_id, tokens_in, tokens_out, tokens_cached, tokens_cache_write, steps, tainted, status, error, result_preview, started_at, ms"

func ScanRun(s Scanner) (Run, error) {
	var r Run
	err := s.Scan(&r.ID, &r.ChatID, &r.AgentID, &r.JobID, &r.Trigger, &r.Model, &r.ModelConnectorID, &r.TokensIn, &r.TokensOut, &r.TokensCached, &r.TokensCacheWrite, &r.Steps, &r.Tainted, &r.Status, &r.Error, &r.ResultPreview, &r.StartedAt, &r.Ms)
	return r, err
}

// Report is a reports row: a /report snapshot, Text exactly as sent.
type Report struct {
	ID        string  `json:"id"`
	ChatID    string  `json:"chatId"`
	SessionID *string `json:"sessionId"`
	Note      string  `json:"note"`
	Text      string  `json:"text"`
	CreatedAt int64   `json:"createdAt"`
}

const ReportCols = "id, chat_id, session_id, note, text, created_at"

func ScanReport(s Scanner) (Report, error) {
	var r Report
	err := s.Scan(&r.ID, &r.ChatID, &r.SessionID, &r.Note, &r.Text, &r.CreatedAt)
	return r, err
}

// --- reminders, jobs, contacts, outbound_messages (M4) ------------------------

// Reminder is a reminders row. Status is pending, firing, done or cancelled.
type Reminder struct {
	ID           string  `json:"id"`
	ChatID       string  `json:"chatId"`
	Text         string  `json:"text"`
	RRule        *string `json:"rrule"`
	TZ           string  `json:"tz"`
	LocalStart   string  `json:"localStart"`
	FireAt       int64   `json:"fireAt"`
	ScheduledFor int64   `json:"scheduledFor"`
	Status       string  `json:"status"`
	LastFiredAt  *int64  `json:"lastFiredAt"`
	SnoozedUntil *int64  `json:"snoozedUntil"`
	SmartPrompt  *string `json:"smartPrompt"`
	LastOutboxID *string `json:"lastOutboxId"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

const ReminderCols = "id, chat_id, text, rrule, tz, local_start, fire_at, scheduled_for, status, last_fired_at, snoozed_until, smart_prompt, last_outbox_id, created_at, updated_at"

func ScanReminder(s Scanner) (Reminder, error) {
	var r Reminder
	err := s.Scan(&r.ID, &r.ChatID, &r.Text, &r.RRule, &r.TZ, &r.LocalStart, &r.FireAt, &r.ScheduledFor, &r.Status, &r.LastFiredAt, &r.SnoozedUntil, &r.SmartPrompt, &r.LastOutboxID, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// Job is a jobs row. Kind is job, heartbeat or briefing; ModelRole default,
// fast or vision; Status idle or running. ToolScope nil means every enabled
// connector.
type Job struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	RRule         string    `json:"rrule"`
	TZ            string    `json:"tz"`
	LocalStart    string    `json:"localStart"`
	Prompt        string    `json:"prompt"`
	ChatID        string    `json:"chatId"`
	ModelRole     string    `json:"modelRole"`
	ToolScope     *[]string `json:"toolScope"`
	Enabled       bool      `json:"enabled"`
	NextRunAt     *int64    `json:"nextRunAt"`
	Status        string    `json:"status"`
	LastRunAt     *int64    `json:"lastRunAt"`
	LastStatus    *string   `json:"lastStatus"`
	LastResult    *string   `json:"lastResult"`
	DailyTokenCap int64     `json:"dailyTokenCap"`
	CreatedAt     int64     `json:"createdAt"`
	UpdatedAt     int64     `json:"updatedAt"`
}

const JobCols = "id, name, kind, rrule, tz, local_start, prompt, chat_id, model_role, tool_scope, enabled, next_run_at, status, last_run_at, last_status, last_result, daily_token_cap, created_at, updated_at"

func ScanJob(s Scanner) (Job, error) {
	var j Job
	var scope NullJSON[[]string]
	err := s.Scan(&j.ID, &j.Name, &j.Kind, &j.RRule, &j.TZ, &j.LocalStart, &j.Prompt, &j.ChatID, &j.ModelRole, &scope, &j.Enabled, &j.NextRunAt, &j.Status, &j.LastRunAt, &j.LastStatus, &j.LastResult, &j.DailyTokenCap, &j.CreatedAt, &j.UpdatedAt)
	if scope.Valid && scope.V != nil {
		v := scope.V
		j.ToolScope = &v
	}
	return j, err
}

// ToolScopeValue is the tool_scope column value for a scope (nil writes NULL).
func ToolScopeValue(scope *[]string) NullJSON[[]string] {
	if scope == nil {
		return NullJSON[[]string]{}
	}
	v := *scope
	if v == nil {
		v = []string{}
	}
	return SomeJSON(v)
}

// Contact is a contacts row. Channel is whatsapp or telegram; Policy ask or allow.
type Contact struct {
	ID        string  `json:"id"`
	AgentID   *string `json:"agentId"`
	Name      string  `json:"name"`
	Channel   string  `json:"channel"`
	Address   string  `json:"address"`
	ChatID    string  `json:"chatId"`
	Policy    string  `json:"policy"`
	Note      *string `json:"note"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	RemovedAt *int64  `json:"removedAt"`
}

const ContactCols = "id, agent_id, name, channel, address, chat_id, policy, note, created_at, updated_at, removed_at"

func ScanContact(s Scanner) (Contact, error) {
	var c Contact
	err := s.Scan(&c.ID, &c.AgentID, &c.Name, &c.Channel, &c.Address, &c.ChatID, &c.Policy, &c.Note, &c.CreatedAt, &c.UpdatedAt, &c.RemovedAt)
	return c, err
}

// OutboundMessage is an outbound_messages row. Origin is chat or flow;
// ApprovedBy owner, policy or flow_version; Status scheduled, firing, done or
// cancelled.
type OutboundMessage struct {
	ID           string  `json:"id"`
	AgentID      *string `json:"agentId"`
	ContactID    string  `json:"contactId"`
	Text         string  `json:"text"`
	Origin       string  `json:"origin"`
	OriginRef    *string `json:"originRef"`
	ApprovedBy   string  `json:"approvedBy"`
	OwnerChatID  *string `json:"ownerChatId"`
	RRule        *string `json:"rrule"`
	TZ           string  `json:"tz"`
	LocalStart   string  `json:"localStart"`
	FireAt       int64   `json:"fireAt"`
	Status       string  `json:"status"`
	LastOutboxID *string `json:"lastOutboxId"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

const OutboundMessageCols = "id, agent_id, contact_id, text, origin, origin_ref, approved_by, owner_chat_id, rrule, tz, local_start, fire_at, status, last_outbox_id, created_at, updated_at"

func ScanOutboundMessage(s Scanner) (OutboundMessage, error) {
	var o OutboundMessage
	err := s.Scan(&o.ID, &o.AgentID, &o.ContactID, &o.Text, &o.Origin, &o.OriginRef, &o.ApprovedBy, &o.OwnerChatID, &o.RRule, &o.TZ, &o.LocalStart, &o.FireAt, &o.Status, &o.LastOutboxID, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

// --- MCP access -----------------------------------------------------------------------

// OAuthClient is an oauth_clients row: an MCP client that registered itself
// (RFC 7591). RedirectURIs is a JSON array. AuthMethod is none,
// client_secret_post or client_secret_basic; SecretHash is set for the last two.
type OAuthClient struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	ClientURI    *string        `json:"clientUri"`
	RedirectURIs JSON[[]string] `json:"redirectUris"`
	AuthMethod   string         `json:"authMethod"`
	SecretHash   *string        `json:"-"`
	CreatedAt    int64          `json:"createdAt"`
}

const OAuthClientCols = "id, name, client_uri, redirect_uris, auth_method, secret_hash, created_at"

func ScanOAuthClient(s Scanner) (OAuthClient, error) {
	var r OAuthClient
	err := s.Scan(&r.ID, &r.Name, &r.ClientURI, &r.RedirectURIs, &r.AuthMethod, &r.SecretHash, &r.CreatedAt)
	return r, err
}

// OAuthRequest is an oauth_requests row: one authorization request waiting
// for the owner's consent. Once approved it carries the user and the hash of
// the one-time code the client exchanges.
type OAuthRequest struct {
	ID            string  `json:"id"`
	ClientID      string  `json:"clientId"`
	RedirectURI   string  `json:"redirectUri"`
	State         *string `json:"state"`
	Scope         *string `json:"scope"`
	CodeChallenge string  `json:"-"`
	UserID        *string `json:"userId"`
	CodeHash      *string `json:"-"`
	CreatedAt     int64   `json:"createdAt"`
	ExpiresAt     int64   `json:"expiresAt"`
}

const OAuthRequestCols = "id, client_id, redirect_uri, state, scope, code_challenge, user_id, code_hash, created_at, expires_at"

func ScanOAuthRequest(s Scanner) (OAuthRequest, error) {
	var r OAuthRequest
	err := s.Scan(&r.ID, &r.ClientID, &r.RedirectURI, &r.State, &r.Scope, &r.CodeChallenge, &r.UserID, &r.CodeHash, &r.CreatedAt, &r.ExpiresAt)
	return r, err
}

// MCPGrant is an mcp_grants row: one way into /mcp. Kind token is a personal
// token (no expiry, no refresh); kind oauth is an OAuth connection whose
// access and refresh tokens rotate in place. Only hashes are stored.
type MCPGrant struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	Name             string  `json:"name"`
	UserID           string  `json:"userId"`
	ClientID         *string `json:"clientId"`
	Scope            *string `json:"scope"`
	TokenHint        *string `json:"tokenHint"`
	AccessHash       string  `json:"-"`
	AccessExpiresAt  *int64  `json:"accessExpiresAt"`
	RefreshHash      *string `json:"-"`
	RefreshExpiresAt *int64  `json:"refreshExpiresAt"`
	PrevRefreshHash  *string `json:"-"`
	RotatedAt        *int64  `json:"rotatedAt"`
	CreatedAt        int64   `json:"createdAt"`
	LastUsedAt       *int64  `json:"lastUsedAt"`
}

const MCPGrantCols = "id, kind, name, user_id, client_id, scope, token_hint, access_hash, access_expires_at, refresh_hash, refresh_expires_at, prev_refresh_hash, rotated_at, created_at, last_used_at"

func ScanMCPGrant(s Scanner) (MCPGrant, error) {
	var r MCPGrant
	err := s.Scan(&r.ID, &r.Kind, &r.Name, &r.UserID, &r.ClientID, &r.Scope, &r.TokenHint, &r.AccessHash, &r.AccessExpiresAt, &r.RefreshHash, &r.RefreshExpiresAt, &r.PrevRefreshHash, &r.RotatedAt, &r.CreatedAt, &r.LastUsedAt)
	return r, err
}
