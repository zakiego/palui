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
	// The agents it serves; empty is every agent (only tool connectors have any).
	AgentIDs   []string `json:"agentIds"`
	Secrets    *string  `json:"secrets"`
	ConfigHash string   `json:"configHash"`
	ErrorSince *int64   `json:"errorSince"`
	AlertedAt  *int64   `json:"alertedAt"`
	SortOrder  int64    `json:"sortOrder"`
	CreatedAt  int64    `json:"createdAt"`
	UpdatedAt  int64    `json:"updatedAt"`
}

// agent_ids falls back to agent_id for a row only a build before 0023 wrote
// (fail closed: that agent, not every agent).
const ConnectorCols = "id, type, kind, label, enabled, status, status_detail, trust, config, coalesce(agent_ids, CASE WHEN agent_id != '' THEN json_array(agent_id) END), secrets, config_hash, error_since, alerted_at, sort_order, created_at, updated_at"

func ScanConnector(s Scanner) (Connector, error) {
	var c Connector
	var cfg JSON[*jsonx.Object]
	var agentIDs NullJSON[[]string]
	err := s.Scan(&c.ID, &c.Type, &c.Kind, &c.Label, &c.Enabled, &c.Status, &c.StatusDetail, &c.Trust, &cfg, &agentIDs, &c.Secrets, &c.ConfigHash, &c.ErrorSince, &c.AlertedAt, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt)
	c.Config = cfg.V
	if c.Config == nil {
		c.Config = jsonx.NewMap[any]()
	}
	c.AgentIDs = agentIDs.V
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
	// The owner lets this member add and remove chat rules (0020).
	CanSetRules bool `json:"canSetRules"`
}

const AgentMemberCols = "agent_id, member_id, created_at, can_set_rules"

func ScanAgentMember(s Scanner) (AgentMember, error) {
	var r AgentMember
	err := s.Scan(&r.AgentID, &r.MemberID, &r.CreatedAt, &r.CanSetRules)
	return r, err
}

// ChatRule is a short rule for one shared chat, added from that chat by the
// owner or a member the owner allowed (0020). MemberID nil means the owner.
type ChatRule struct {
	ID         string  `json:"id"`
	AgentID    string  `json:"agentId"`
	ChatID     string  `json:"chatId"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	MemberID   *string `json:"memberId"`
	AuthorName string  `json:"authorName"`
	CreatedAt  int64   `json:"createdAt"`
}

const ChatRuleCols = "id, agent_id, chat_id, title, body, member_id, author_name, created_at"

func ScanChatRule(s Scanner) (ChatRule, error) {
	var r ChatRule
	err := s.Scan(&r.ID, &r.AgentID, &r.ChatID, &r.Title, &r.Body, &r.MemberID, &r.AuthorName, &r.CreatedAt)
	return r, err
}

// ChatRuleChange is one add or remove of a chat rule: the rule as it was,
// who wrote it (MemberID, AuthorName), who changed it (ByMemberID, ByName;
// nil member means the owner) and when the owner undid it.
type ChatRuleChange struct {
	ID         string  `json:"id"`
	AgentID    string  `json:"agentId"`
	ChatID     string  `json:"chatId"`
	RuleID     string  `json:"ruleId"`
	Action     string  `json:"action"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	MemberID   *string `json:"memberId"`
	AuthorName string  `json:"authorName"`
	ByMemberID *string `json:"byMemberId"`
	ByName     string  `json:"byName"`
	RunID      *string `json:"runId"`
	CreatedAt  int64   `json:"createdAt"`
	UndoneAt   *int64  `json:"undoneAt"`
}

const ChatRuleChangeCols = "id, agent_id, chat_id, rule_id, action, title, body, member_id, author_name, by_member_id, by_name, run_id, created_at, undone_at"

func ScanChatRuleChange(s Scanner) (ChatRuleChange, error) {
	var r ChatRuleChange
	err := s.Scan(&r.ID, &r.AgentID, &r.ChatID, &r.RuleID, &r.Action, &r.Title, &r.Body, &r.MemberID, &r.AuthorName, &r.ByMemberID, &r.ByName, &r.RunID, &r.CreatedAt, &r.UndoneAt)
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
	// Keep every photo and document sent here in saved_attachments (off by default).
	SaveMedia bool `json:"saveMedia"`
	// Days an auto-saved file stays; 0 keeps it until someone deletes it.
	SaveMediaDays int64 `json:"saveMediaDays"`
}

const ChatCols = "id, channel, external_id, title, kind, session_id, connector_id, allowed, agent_id, reply_mode, ack, ack_emoji, last_message_at, created_at, save_media, save_media_days"

// ChatDest is ScanChat's scan targets, for queries that select more columns after ChatCols.
func ChatDest(c *Chat) []any {
	return []any{&c.ID, &c.Channel, &c.ExternalID, &c.Title, &c.Kind, &c.SessionID, &c.ConnectorID, &c.Allowed, &c.AgentID, &c.ReplyMode, &c.Ack, &c.AckEmoji, &c.LastMessageAt, &c.CreatedAt, &c.SaveMedia, &c.SaveMediaDays}
}

func ScanChat(s Scanner) (Chat, error) {
	var c Chat
	err := s.Scan(ChatDest(&c)...)
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
	// The setup of the session's last message turn (chat.SetupSnapshot),
	// internal: never sent to the dashboard.
	SetupSnapshot *string `json:"-"`
}

const SessionCols = "id, chat_id, summary, summarized_until, saved_to_recall_at, loaded_tools, created_at, closed_at, setup_snapshot"

func ScanSession(s Scanner) (Session, error) {
	var r Session
	err := s.Scan(&r.ID, &r.ChatID, &r.Summary, &r.SummarizedUntil, &r.SavedToRecallAt, &r.LoadedTools, &r.CreatedAt, &r.ClosedAt, &r.SetupSnapshot)
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
	AskChatID   *string       `json:"askChatId"` // old rows only: nothing writes it since PLAN 19.9 step 5
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
	// Why it was asked: a tools.Reason* code, nil on old rows.
	Reason *string `json:"reason,omitempty"`
}

const ApprovalCols = "id, run_id, chat_id, ask_chat_id, approval_id, tool_call_id, tool, tool_class, args_preview, messages, context, prompt_msg_id, expires_at, status, decided_at, created_at, reason"

func ScanApproval(s Scanner) (Approval, error) {
	var a Approval
	var ctx JSON[*jsonx.Object]
	err := s.Scan(&a.ID, &a.RunID, &a.ChatID, &a.AskChatID, &a.ApprovalID, &a.ToolCallID, &a.Tool, &a.ToolClass, &a.ArgsPreview, &a.Messages, &ctx, &a.PromptMsgID, &a.ExpiresAt, &a.Status, &a.DecidedAt, &a.CreatedAt, &a.Reason)
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
	// "tool" (save_attachment) or "auto" (the chat's auto-save).
	Origin string `json:"origin"`
	// Who sent the file and when, when known.
	SenderID   *string `json:"senderId"`
	SenderName *string `json:"senderName"`
	SentAt     *int64  `json:"sentAt"`
}

const AttachmentMetaCols = "id, chat_id, message_id, run_id, title, description, kind, mime_type, filename, size, sha256, created_at, origin, sender_id, sender_name, sent_at"

// AttachmentMetaDest is ScanAttachmentMeta's scan targets, for queries that
// select more columns after AttachmentMetaCols.
func AttachmentMetaDest(a *AttachmentMeta) []any {
	return []any{&a.ID, &a.ChatID, &a.MessageID, &a.RunID, &a.Title, &a.Description, &a.Kind, &a.MimeType, &a.Filename, &a.Size, &a.Sha256, &a.CreatedAt, &a.Origin, &a.SenderID, &a.SenderName, &a.SentAt}
}

func ScanAttachmentMeta(s Scanner) (AttachmentMeta, error) {
	var a AttachmentMeta
	err := s.Scan(AttachmentMetaDest(&a)...)
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
	// A photo, file, voice note, video or sticker; Text is then its caption.
	// Rows written before outbound media have none.
	Media *OutboxMedia `json:"media,omitempty"`
}

// Outbound media kinds (OutboxMedia.Kind): what the channel sends it as.
const (
	MediaImage    = "image"
	MediaVideo    = "video"
	MediaVoice    = "voice"
	MediaAudio    = "audio"
	MediaDocument = "document"
	MediaSticker  = "sticker"
)

// OutboxMedia is outbox.payload.media: what to send and where its bytes are.
type OutboxMedia struct {
	// outbox_media.id
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Normalized, without parameters.
	MimeType string `json:"mimeType"`
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size"`
	// Filled by the outbox worker just before the send, never stored.
	Bytes []byte `json:"-"`
}

// MediaBlob is an outbox_media row without `data`.
type MediaBlob struct {
	ID        string  `json:"id"`
	ChatID    string  `json:"chatId"`
	Kind      string  `json:"kind"`
	MimeType  string  `json:"mimeType"`
	Filename  *string `json:"filename"`
	Size      int64   `json:"size"`
	Sha256    string  `json:"sha256"`
	Source    string  `json:"source"`
	SourceRef *string `json:"sourceRef"`
	CreatedAt int64   `json:"createdAt"`
}

const MediaBlobCols = "id, chat_id, kind, mime_type, filename, size, sha256, source, source_ref, created_at"

func ScanMediaBlob(s Scanner) (MediaBlob, error) {
	var m MediaBlob
	err := s.Scan(&m.ID, &m.ChatID, &m.Kind, &m.MimeType, &m.Filename, &m.Size, &m.Sha256, &m.Source, &m.SourceRef, &m.CreatedAt)
	return m, err
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
// Connectors holds one entry per connector id, and Classes one per
// "<connectorId>:<class>". A higher level locks the ones under it: connector,
// then class, then tool, then AllowAll as the fallback. Both stay out of the
// JSON while empty.
type ToolPermissions struct {
	AllowAll   bool               `json:"allowAll"`
	Tools      *jsonx.Map[string] `json:"tools"`
	Connectors *jsonx.Map[string] `json:"connectors,omitempty"`
	Classes    *jsonx.Map[string] `json:"classes,omitempty"`
	// Owner turns ask before writes after the conversation read untrusted
	// content (the taint rules). Off: they follow the policy and class only.
	AskAfterUntrusted bool `json:"askAfterUntrusted,omitempty"`
}

// ModelLink is one step of a model chain: a model connector, a model id and
// an optional reasoning effort.
type ModelLink struct {
	ConnectorID string `json:"connectorId"`
	Model       string `json:"model"`
	Effort      string `json:"effort,omitempty"`
}

// AgentModels is agents.model_roles: the chains one agent uses instead of the
// global modelRoles setting. A role left empty uses the global chain.
type AgentModels struct {
	Default []ModelLink `json:"default,omitempty"`
	Fast    []ModelLink `json:"fast,omitempty"`
	Vision  []ModelLink `json:"vision,omitempty"`
}

// Role returns the chain for "default", "fast" or "vision".
func (m AgentModels) Role(role string) []ModelLink {
	switch role {
	case "fast":
		return m.Fast
	case "vision":
		return m.Vision
	}
	return m.Default
}

// Empty says whether every role uses the global chain.
func (m AgentModels) Empty() bool { return len(m.Default)+len(m.Fast)+len(m.Vision) == 0 }

// Agent: the table still has owner_powers (PLAN 19.9 step 5). Nothing reads
// or writes it, and a later release drops the column with a table rebuild.
type Agent struct {
	ID                   string                    `json:"id"`
	Slug                 string                    `json:"slug"`
	Name                 string                    `json:"name"`
	Persona              string                    `json:"persona"`
	IsDefault            bool                      `json:"isDefault"`
	RecallContainer      *string                   `json:"recallContainer"`
	RecallReadContainers NullJSON[[]string]        `json:"recallReadContainers"`
	RecallPerChat        bool                      `json:"recallPerChat"`
	GuestTools           string                    `json:"guestTools"`
	DailyTokenCap        int64                     `json:"dailyTokenCap"`
	ToolPermissions      NullJSON[ToolPermissions] `json:"toolPermissions"`
	TelegramConnectorID  *string                   `json:"telegramConnectorId"`
	AvatarImage          *string                   `json:"avatarImage"`
	AvatarIcon           *string                   `json:"avatarIcon"`
	AvatarColor          *string                   `json:"avatarColor"`
	CreatedAt            int64                     `json:"createdAt"`
	UpdatedAt            int64                     `json:"updatedAt"`
	ModelRoles           NullJSON[AgentModels]     `json:"modelRoles"`
	TranscribeVoice      bool                      `json:"transcribeVoice"`
}

const AgentCols = "id, slug, name, persona, is_default, recall_container, recall_read_containers, recall_per_chat, guest_tools, daily_token_cap, tool_permissions, telegram_connector_id, avatar_image, avatar_icon, avatar_color, created_at, updated_at, model_roles, transcribe_voice"

func ScanAgent(s Scanner) (Agent, error) {
	var a Agent
	err := s.Scan(&a.ID, &a.Slug, &a.Name, &a.Persona, &a.IsDefault, &a.RecallContainer, &a.RecallReadContainers, &a.RecallPerChat, &a.GuestTools, &a.DailyTokenCap, &a.ToolPermissions, &a.TelegramConnectorID, &a.AvatarImage, &a.AvatarIcon, &a.AvatarColor, &a.CreatedAt, &a.UpdatedAt, &a.ModelRoles, &a.TranscribeVoice)
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

// Watch is a watches row (PLAN 6e). Status is active, checking, done,
// expired, cancelled or failed.
type Watch struct {
	ID          string  `json:"id"`
	ChatID      string  `json:"chatId"`
	AgentID     *string `json:"agentId"`
	CreatedBy   string  `json:"createdBy"`
	Topic       string  `json:"topic"`
	Question    string  `json:"question"`
	DoneWhen    string  `json:"doneWhen"`
	EveryMin    int64   `json:"everyMin"`
	ExpiresAt   int64   `json:"expiresAt"`
	Status      string  `json:"status"`
	NextCheckAt *int64  `json:"nextCheckAt"`
	LeaseUntil  *int64  `json:"leaseUntil"`
	Checks      int64   `json:"checks"`
	Fails       int64   `json:"fails"`
	LastCheckAt *int64  `json:"lastCheckAt"`
	LastRunID   *string `json:"lastRunId"`
	LastError   *string `json:"lastError"`
	Notes       string  `json:"notes"`
	LastSummary string  `json:"lastSummary"`
	TokensUsed  int64   `json:"tokensUsed"`
	TokenCap    int64   `json:"tokenCap"`
	Result      *string `json:"result"`
	OutboxID    *string `json:"outboxId"`
	EndedAt     *int64  `json:"endedAt"`
	CreatedAt   int64   `json:"createdAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

const WatchCols = "id, chat_id, agent_id, created_by, topic, question, done_when, every_min, expires_at, status, next_check_at, lease_until, checks, fails, last_check_at, last_run_id, last_error, notes, last_summary, tokens_used, token_cap, result, outbox_id, ended_at, created_at, updated_at"

func ScanWatch(s Scanner) (Watch, error) {
	var w Watch
	err := s.Scan(&w.ID, &w.ChatID, &w.AgentID, &w.CreatedBy, &w.Topic, &w.Question, &w.DoneWhen, &w.EveryMin, &w.ExpiresAt, &w.Status, &w.NextCheckAt, &w.LeaseUntil, &w.Checks, &w.Fails, &w.LastCheckAt, &w.LastRunID, &w.LastError, &w.Notes, &w.LastSummary, &w.TokensUsed, &w.TokenCap, &w.Result, &w.OutboxID, &w.EndedAt, &w.CreatedAt, &w.UpdatedAt)
	return w, err
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

// AgentProactive is an agent_proactive row (docs/rfc/proactive.md 4.1): one
// per agent, off until the owner turns it on. Config is the versioned JSON
// the proactive package validates.
type AgentProactive struct {
	AgentID        string  `json:"agentId"`
	Enabled        bool    `json:"enabled"`
	ChatID         *string `json:"chatId"`
	Config         RawJSON `json:"config"`
	NextTickAt     *int64  `json:"nextTickAt"`
	LastTickAt     *int64  `json:"lastTickAt"`
	TickLeaseUntil *int64  `json:"tickLeaseUntil"`
	PausedReason   *string `json:"pausedReason"`
	CreatedAt      int64   `json:"createdAt"`
	UpdatedAt      int64   `json:"updatedAt"`
}

const AgentProactiveCols = "agent_id, enabled, chat_id, config, next_tick_at, last_tick_at, tick_lease_until, paused_reason, created_at, updated_at"

func ScanAgentProactive(s Scanner) (AgentProactive, error) {
	var r AgentProactive
	err := s.Scan(&r.AgentID, &r.Enabled, &r.ChatID, &r.Config, &r.NextTickAt, &r.LastTickAt, &r.TickLeaseUntil, &r.PausedReason, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// Nudge is a nudges row (docs/rfc/proactive.md 4.2): one thing a proactive
// agent found, with its status, so it's told once.
type Nudge struct {
	ID          string  `json:"id"`
	AgentID     string  `json:"agentId"`
	Key         string  `json:"key"`
	Kind        string  `json:"kind"`
	Subject     string  `json:"subject"`
	Status      string  `json:"status"`
	WakeAt      *int64  `json:"wakeAt"`
	SentAt      *int64  `json:"sentAt"`
	EscalatedAt *int64  `json:"escalatedAt"`
	Snapshot    RawJSON `json:"snapshot"`
	Reason      *string `json:"reason"`
	OutboxID    *string `json:"outboxId"`
	CreatedAt   int64   `json:"createdAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

const NudgeCols = "id, agent_id, key, kind, subject, status, wake_at, sent_at, escalated_at, snapshot, reason, outbox_id, created_at, updated_at"

func ScanNudge(s Scanner) (Nudge, error) {
	var r Nudge
	err := s.Scan(&r.ID, &r.AgentID, &r.Key, &r.Kind, &r.Subject, &r.Status, &r.WakeAt, &r.SentAt, &r.EscalatedAt, &r.Snapshot, &r.Reason, &r.OutboxID, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// ProactiveMute is a proactive_mutes row: a subject never nudged again.
type ProactiveMute struct {
	AgentID   string `json:"agentId"`
	Subject   string `json:"subject"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"createdAt"`
}

const ProactiveMuteCols = "agent_id, subject, label, created_at"

func ScanProactiveMute(s Scanner) (ProactiveMute, error) {
	var r ProactiveMute
	err := s.Scan(&r.AgentID, &r.Subject, &r.Label, &r.CreatedAt)
	return r, err
}

// ProactiveNote is a proactive_notes row: one memory note an agent's
// proactive runs carry between runs. Source is owner or model; owner notes
// never expire.
type ProactiveNote struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	Text      string `json:"text"`
	Source    string `json:"source"`
	ExpiresAt *int64 `json:"expiresAt"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

const ProactiveNoteCols = "id, agent_id, text, source, expires_at, created_at, updated_at"

func ScanProactiveNote(s Scanner) (ProactiveNote, error) {
	var r ProactiveNote
	err := s.Scan(&r.ID, &r.AgentID, &r.Text, &r.Source, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// WaInboxChat is a wa_inbox_chats row: one chat of a read-only WhatsApp
// inbox. WaitingSince is nil when the owner replied last.
type WaInboxChat struct {
	ConnectorID  string `json:"connectorId"`
	ChatJID      string `json:"chatJid"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	LastInAt     *int64 `json:"lastInAt"`
	LastOutAt    *int64 `json:"lastOutAt"`
	WaitingSince *int64 `json:"waitingSince"`
	Unanswered   int64  `json:"unanswered"`
	Preview      string `json:"preview"`
	UpdatedAt    int64  `json:"updatedAt"`
}

const WaInboxChatCols = "connector_id, chat_jid, kind, name, last_in_at, last_out_at, waiting_since, unanswered, preview, updated_at"

func ScanWaInboxChat(s Scanner) (WaInboxChat, error) {
	var r WaInboxChat
	err := s.Scan(&r.ConnectorID, &r.ChatJID, &r.Kind, &r.Name, &r.LastInAt, &r.LastOutAt, &r.WaitingSince, &r.Unanswered, &r.Preview, &r.UpdatedAt)
	return r, err
}

// ProactiveTick is a proactive_ticks row: one collection pass.
type ProactiveTick struct {
	ID         string  `json:"id"`
	AgentID    string  `json:"agentId"`
	StartedAt  int64   `json:"startedAt"`
	FinishedAt *int64  `json:"finishedAt"`
	Sources    RawJSON `json:"sources"`
	Found      int64   `json:"found"`
	Sent       int64   `json:"sent"`
	Tokens     int64   `json:"tokens"`
	Error      *string `json:"error"`
}

const ProactiveTickCols = "id, agent_id, started_at, finished_at, sources, found, sent, tokens, error"

func ScanProactiveTick(s Scanner) (ProactiveTick, error) {
	var r ProactiveTick
	err := s.Scan(&r.ID, &r.AgentID, &r.StartedAt, &r.FinishedAt, &r.Sources, &r.Found, &r.Sent, &r.Tokens, &r.Error)
	return r, err
}
