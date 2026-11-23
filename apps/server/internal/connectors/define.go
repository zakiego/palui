package connectors

// The connector contract (PLAN 4.1), ported from connectors/define.ts. A
// connector type is a package in internal/connectors/<type> that exports a
// Manifest and a Runtime (Test, Start). The dashboard keeps its own TS
// manifests for the forms; a golden keeps the two in parity (M6).

import (
	"context"
	"encoding/json"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// --- Manifest --------------------------------------------------------------------------

// SetupStep is a wizard field, or (Action set) a wizard action the dashboard
// renders specially: claimOwner, pairWhatsApp, ownerPhone, pickContainer,
// listTools or oauth.
type SetupStep struct {
	Field string `json:"field,omitempty"`
	// Action steps carry Action, Label and Help only.
	Action string `json:"action,omitempty"`
	Label  string `json:"label"`
	Help   string `json:"help,omitempty"`
	// A command or value the help refers to; "{appUrl}" is the dashboard URL.
	Command     string `json:"command,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
	// Show an optional field before the first connect too.
	AskFirst bool `json:"askFirst,omitempty"`
	// Show the field only while another field has one of these values.
	ShowIf *ShowIf `json:"showIf,omitempty"`
}

type ShowIf struct {
	Field string   `json:"field"`
	In    []string `json:"in"`
}

// Preset is a ready-made instance of a multiple-instance connector.
type Preset struct {
	ID           string        `json:"id"`
	Label        string        `json:"label"`
	Config       *jsonx.Object `json:"config"`
	SecretFields []string      `json:"secretFields,omitempty"`
	Trust        string        `json:"trust,omitempty"`
}

type Docs struct {
	Commands []string `json:"commands,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

// Manifest is ConnectorManifest. The zod config schema becomes ParseConfig
// (validate and fill defaults, like manifest.config.parse); the secrets schema
// becomes SecretFields plus an optional ParseSecrets.
type Manifest struct {
	// Type id ("telegram", "mcp", "cliproxy").
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// lucide icon name.
	Icon        string      `json:"icon"`
	Summary     string      `json:"summary"`
	Description string      `json:"description,omitempty"`
	Setup       []SetupStep `json:"setup"`
	// Several instances allowed; instance ids become "<id>-<slug>".
	Multiple bool `json:"multiple,omitempty"`
	// Default trust for tool results ("own" or "external").
	Trust string `json:"trust,omitempty"`
	// Created on first boot without asking.
	Builtin        bool   `json:"builtin,omitempty"`
	PauseNote      string `json:"pauseNote,omitempty"`
	RemoveWarning  string `json:"removeWarning,omitempty"`
	UnlinkOnRemove bool   `json:"unlinkOnRemove,omitempty"`
	// Restart a running handle that reports error. nil: true for channels,
	// false otherwise.
	RestartOnError *bool    `json:"restartOnError,omitempty"`
	Presets        []Preset `json:"presets,omitempty"`
	Docs           *Docs    `json:"docs,omitempty"`

	// ConfigZod and SecretZod are the zod shapes behind ParseConfig and
	// ParseSecrets, field by field: saveDraft and the wizard check one field
	// at a time (configShape(m) and m.secrets[k] in TS). Nil: no fields.
	ConfigZod    *zod.ObjectS                                   `json:"-"`
	SecretZod    []zod.Field                                    `json:"-"`
	SecretFields []string                                       `json:"-"`
	ParseConfig  func(raw *jsonx.Object) (*jsonx.Object, error) `json:"-"`
	ParseSecrets func(raw *jsonx.Map[string]) error             `json:"-"`
}

// Row is the part the connectors store needs.
func (m Manifest) Row() RowManifest {
	return RowManifest{ID: m.ID, Kind: m.Kind, Name: m.Name, Trust: m.Trust}
}

// --- Runtime ---------------------------------------------------------------------------

// MemoryScope is what a memory connector's tools may touch this turn (PLAN
// 19.5). Nil Container means the connector's own.
type MemoryScope struct {
	Container *string  `json:"container"`
	Read      []string `json:"read"`
	Locked    bool     `json:"locked"`
}

// TurnInfo is what a running turn tells a connector when it asks for tools.
type TurnInfo struct {
	RunID  string
	ChatID string
	// "" when there is no channel (flows, sources).
	Channel string
	// message, schedule, hook, approval, reminder or flow.
	Trigger string
	TZ      string
	Now     int64
	JobID   string
	// The run whose owner messages this turn answers (differs after an approval resume).
	BatchRunID string
	AgentID    string
	FlowID     string
	// What started the turn, kept across approval resumes.
	Origin string
	// owner, members or outside: who can read the chat (PLAN 19.4).
	Mode string
	// Whether untrusted content reached the turn so far; read when the tool runs.
	Tainted func() bool
	// The owner's own messages in this batch, as typed.
	OwnerText *string
	Memory    *MemoryScope
	// Set when this turn may add and remove the chat's rules: a members chat
	// where everyone in the batch is the owner or a member the owner let set
	// rules. Who the newest message came from; MemberID nil is the owner.
	RuleSetter *RuleSetter
}

// RuleSetter is who a turn's chat rule changes are recorded under.
type RuleSetter struct {
	MemberID *string
	Name     string
}

// ToolCallInfo is what a tool's Execute gets besides its input.
type ToolCallInfo struct {
	ToolCallID string
	Messages   []models.Message
}

// Tool is an AI SDK tool: description, JSON schema input and execute.
type Tool struct {
	Description string
	InputSchema json.RawMessage
	// nil means the tool can't run here (a provider-executed tool).
	Execute func(ctx context.Context, input json.RawMessage, call ToolCallInfo) (any, error)
	Strict  *bool
	// Validate checks an input against the tool's own schema, like the AI
	// SDK's schema.validate on a zod schema (built-in tools set it; MCP tools,
	// whose JSON schema the AI SDK never validated, leave it nil). A flow tool
	// step calls it before Execute (flows/call.ts checkInput).
	Validate func(input json.RawMessage) error
	// Parse is the AI SDK's input validation for a tool call: the parsed
	// input (a jsonx value, zod's output with defaults filled and unknown keys
	// dropped) or the schema's error. The turn loop calls it on every model
	// call; a failure becomes the AI SDK's InvalidToolInputError. Nil for MCP
	// tools (the AI SDK never validated their JSON schema).
	Parse func(input any) (any, error)
	// ToModelOutput turns the tool's result into what the model sees (the AI
	// SDK tool's toModelOutput). Nil means the default: a string is text,
	// anything else JSON. MCP tools set it.
	ToModelOutput func(output any) models.ToolOutput
}

// PaluiTool is a tool plus its connector metadata (PaluiTool in define.ts).
// Class drives approvals; anything else is treated as destructive.
type PaluiTool struct {
	Tool  Tool
	Class string
	// Results taint the turn (web fetch, hooks, external MCP).
	Untrusted bool
	// Results hold what other people wrote for the owner's agent to read
	// (another agent's chats and memory). Implies Untrusted, and a write or
	// destructive call after it asks even when the owner set that tool to allow.
	OthersWrote bool
	Title       string
	// May run in a turn started by someone other than the owner.
	GuestSafe bool
	// Whose data the tool reads or writes: DataChat, DataAgent, DataShared or
	// DataNone. "" follows the connector, or is DataNone on an MCP server the
	// owner marked as holding no private data; ResolveData works out the scope
	// the server acts on.
	Data string
	// Flow write tools: "version" (default) or "every_run".
	FlowApproval string
	// Ask the owner for this call even where the class rules wouldn't.
	NeedsApproval func(input json.RawMessage) bool
	// The write only records what the owner just said in this chat (a
	// check-in answer). In the owner's own turn, untrusted results replayed
	// from earlier turns don't make it ask. Untrusted results in the same
	// turn, others' writing, jobs, flows and hooks still do.
	OwnerReply bool
	// The approval prompt's text for this call.
	Preview func(input json.RawMessage) string
}

// Data scopes for PaluiTool.Data, shown on the agent's Tools tab.
const (
	// Each chat sees only its own rows (reminders, attachments, flows).
	DataChat = "chat"
	// Only the agent that wrote it sees it (instructions, contacts, memory).
	DataAgent = "agent"
	// Every agent with the tool sees the same data (settings, the calendar).
	DataShared = "shared"
	// Keeps nothing (web fetch, or an MCP server the owner marked as holding
	// no private data).
	DataNone = "none"
)

// ToolSet is a connector's tools keyed by bare name, in JS object key order.
type ToolSet = jsonx.Map[PaluiTool]

func NewToolSet() *ToolSet { return jsonx.NewMap[PaluiTool]() }

type InboundMedia struct {
	// image, audio or document.
	Kind     string `json:"kind"`
	MimeType string `json:"mimeType"`
	// base64
	Data        string   `json:"data"`
	Filename    string   `json:"filename,omitempty"`
	DurationSec *float64 `json:"durationSec,omitempty"`
	// The bytes are here only for the chat's auto-save (Services.SavesMedia):
	// the model never sees them (too big for it, or group talk kept as context).
	SaveOnly bool `json:"-"`
	// A sticker, converted to an image. Auto-save skips it.
	Sticker bool `json:"-"`
	// The text the sender wrote with it, without Palui's labels and notes.
	Caption string `json:"-"`
}

// ContextPhotoMaxBytes caps a photo kept from group talk (ADR 0003).
// WhatsApp sends photos compressed, usually well under 1 MB.
const ContextPhotoMaxBytes = 5 * 1024 * 1024

// InboundQuote is the message an inbound one quotes, as the channel saw it.
type InboundQuote struct {
	// self, sender, other or unknown.
	From  string `json:"from"`
	Text  string `json:"text"`
	Media string `json:"media,omitempty"`
}

type InboundCallback struct {
	Data       string `json:"data"`
	CallbackID string `json:"callbackId,omitempty"`
}

type InboundMessage struct {
	Channel          string `json:"channel"`
	ConnectorID      string `json:"connectorId"`
	ChatExternalID   string `json:"chatExternalId"`
	ChatKind         string `json:"chatKind"`
	ChatTitle        string `json:"chatTitle,omitempty"`
	SenderExternalID string `json:"senderExternalId"`
	// WhatsApp: the other form of the sender (LID or phone JID).
	SenderAltID         string           `json:"senderAltId,omitempty"`
	SenderName          string           `json:"senderName,omitempty"`
	ChannelMsgID        string           `json:"channelMsgId,omitempty"`
	ReplyToChannelMsgID string           `json:"replyToChannelMsgId,omitempty"`
	Quoted              *InboundQuote    `json:"quoted,omitempty"`
	Text                string           `json:"text"`
	Media               []InboundMedia   `json:"media,omitempty"`
	Callback            *InboundCallback `json:"callback,omitempty"`
	// WhatsApp: the channel id of the album this photo belongs to (the album
	// message's own id), so a reply to the album finds its photos.
	AlbumID string `json:"albumId,omitempty"`
	// Groups: the message mentions this account or replies to it.
	Addressed bool  `json:"addressed,omitempty"`
	FromMe    bool  `json:"fromMe,omitempty"`
	At        int64 `json:"at"`
}

type SendResult = outbox.SendResult

// TypingOptions: Reaction is the emoji shown while thinking, "" for none,
// nil for the channel's default.
type TypingOptions struct {
	Reaction *string
}

// ChannelHandle is a running channel (outbox.Channel). Typing,
// AnswerCallback and GroupParticipants are optional (Typer, CallbackAnswerer,
// GroupLister).
type ChannelHandle = outbox.Channel

type Typer interface {
	Typing(ctx context.Context, chatExternalID string, on bool, opts TypingOptions) error
}

type CallbackAnswerer interface {
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

// Participant is one account in a group chat. AltID is its other form on
// WhatsApp (LID or phone JID), "" when unknown.
type Participant struct{ ExternalID, AltID string }

// GroupLister is a channel that can say who is in a group chat right now,
// without the channel's own account. Any error means the list is unknown,
// and the caller treats the chat as having outsiders.
type GroupLister interface {
	GroupParticipants(ctx context.Context, chatExternalID string) ([]Participant, error)
}

type RecallOptions struct {
	TimeoutMs int64
	// Overrides the connector's own container.
	Container *string
	// Who the facts are about ("the owner" by default).
	About string
}

type SaveInput struct {
	SourceID  string
	Content   string
	Title     string
	Container *string
}

// MemoryHandle is a memory connector's auto-recall and auto-save.
type MemoryHandle struct {
	// Profile text for the prompt; ok false when skipped.
	Recall     func(ctx context.Context, query string, opts RecallOptions) (profile string, ok bool, err error)
	Save       func(ctx context.Context, in SaveInput) error
	AutoRecall bool
	AutoSave   bool
}

// ToolCallResult is callTool's answer: the raw result and the tool's class.
type ToolCallResult struct {
	Class  string
	Output any
}

// RunningConnector is a started connector. Every field but Stop is optional.
type RunningConnector struct {
	Stop    func(ctx context.Context) error
	Channel ChannelHandle
	// Model connectors: a model for a model id and role, built on demand.
	Model func(modelID, role string) (models.LanguageModel, error)
	// Tool connectors: tools for this turn, keyed by bare name.
	Tools func(ctx context.Context, turn TurnInfo) (*ToolSet, error)
	// Call one tool outside a turn (flow sources).
	CallTool   func(ctx context.Context, name string, args *jsonx.Object) (ToolCallResult, error)
	Memory     *MemoryHandle
	Transcribe func(ctx context.Context, audio InboundMedia) (string, error)
	// Free-form live state for the dashboard.
	Live   func() *jsonx.Object
	Action func(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error)
}

// ClaimInput is a Telegram owner claim.
type ClaimInput struct {
	Channel     string
	Nonce       string
	ExternalID  string
	DisplayName string
}

// FlowToolCaller is flows/call.ts's FlowToolCaller (RFC flows 5.1): every
// running connector's tools with their class, for flow saves and dry runs, and
// the server-side call a flow tool step makes. flows.ConnectorFlowCaller
// implements it over the supervisor's running connectors.
type FlowToolCaller interface {
	// Every tool of every running connector the turn's agent may use.
	List(ctx context.Context, turn TurnInfo) ([]FlowToolInfo, error)
	// One tool, by namespaced name or connector id + bare name. Nil when missing.
	Resolve(ctx context.Context, tool, connectorID string, turn TurnInfo) (*FlowToolInfo, error)
	// Calls a resolved tool. The class is checked again first; timeoutMs 0 is
	// the 20 s default, capped at 60 s.
	Call(ctx context.Context, info FlowToolInfo, args *jsonx.Object, turn TurnInfo, timeoutMs int64) (any, error)
}

// FlowToolInfo is flows/validate.ts's ToolInfo: one tool as the running
// connectors describe it.
type FlowToolInfo struct {
	// Namespaced, as search_tools shows it.
	Name        string `json:"name"`
	ConnectorID string `json:"connectorId"`
	Tool        string `json:"tool"`
	Class       string `json:"class"`
	Untrusted   bool   `json:"untrusted"`
	// The connector's type ("" when unknown).
	ConnectorType string `json:"connectorType,omitempty"`
	// "version" (default) or "every_run".
	FlowApproval string          `json:"flowApproval,omitempty"`
	Description  string          `json:"-"`
	InputSchema  json.RawMessage `json:"-"`
	// The tool's own approval text (PaluiTool.Preview), nil when it has none.
	Preview func(json.RawMessage) string `json:"-"`
}

// Services the runtime gives every connector.
type Services struct {
	// Channels hand every inbound message here; it's in the inbox when this returns.
	Inbound func(ctx context.Context, msg InboundMessage) error
	// Telegram owner claim: true when the nonce was valid.
	ClaimOwner func(ctx context.Context, in ClaimInput) (bool, error)
	// Whether this sender is the owner (also learns the WhatsApp LID).
	IsOwner func(channel, externalID, altID string) bool
	// Whether the media of this message is worth downloading: true when it
	// will be answered (the owner, or a guest in a chat set up for guests).
	// Dropped messages and group talk kept as context never show their media.
	// Optional; without it only the owner's media is downloaded.
	WantsMedia func(msg InboundMessage) bool
	// Whether this message's chat auto-saves media (chats.save_media), so its
	// photos and documents are worth downloading even when WantsMedia says no.
	// Such media is marked SaveOnly unless the model wants it too. Optional.
	SavesMedia func(msg InboundMessage) bool
	// Whether a photo in group talk kept as context is worth downloading, so
	// a later reply to it can show it to the model (ADR 0003). The engine
	// keeps it only for a while (chat.ContextMediaKeepMs). Optional.
	KeepsMedia func(msg InboundMessage) bool
	// Reconcile now instead of on the next tick. Optional.
	Reconcile func()
	// Call a read-only tool of another running connector, server-side. Optional.
	CallTool func(ctx context.Context, connectorID, tool string, args *jsonx.Object) (any, error)
	// Optional.
	FlowTools FlowToolCaller
}

// Context is ConnectorContext.
type Context struct {
	ID      string
	Type    string
	Config  *jsonx.Object
	Secrets *jsonx.Map[string]
	Trust   string
	DB      *db.DB
	Log     *logx.Logger
	DataDir string
	// Canceled when the connector is paused, removed or restarted (the TS signal).
	Ctx      context.Context
	Services *Services
	// Report a status change: connecting, on, error or paused. nil detail clears it.
	SetStatus func(status string, detail *string)
	// level "" means info.
	Event func(kind, message, level string, data *jsonx.Object)
	// Persist non-secret config learned at runtime.
	PatchConfig func(patch *jsonx.Object)
	// Persist a secret learned at runtime; "" deletes the key. No restart.
	PatchSecrets func(patch *jsonx.Map[string])
}

// Secret reads one secret ("" when missing).
func (c Context) Secret(name string) string {
	if c.Secrets == nil {
		return ""
	}
	v, _ := c.Secrets.Get(name)
	return v
}

type TestResult struct {
	OK     bool          `json:"ok"`
	Detail string        `json:"detail"`
	Data   *jsonx.Object `json:"data,omitempty"`
}

// Runtime is ConnectorRuntime: Test and Start for one manifest.
type Runtime struct {
	Manifest Manifest
	Test     func(c Context) (TestResult, error)
	Start    func(c Context) (*RunningConnector, error)
}

// LiveStatus is ConnectorLive (control.ts): what the dashboard shows per row.
type LiveStatus struct {
	ID      string        `json:"id"`
	Running bool          `json:"running"`
	Status  string        `json:"status"`
	Detail  *string       `json:"detail"`
	Live    *jsonx.Object `json:"live"`
}

// TranscriberTypes are the connector types whose runtime can Transcribe
// voice notes.
var TranscriberTypes = []string{"voice"}
