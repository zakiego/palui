package fixtures

// apps/app/src/dev/fixtures/kit.ts: the small insert helpers every fixture
// uses. Rows go in through the same tables the dashboard reads, so a fixture
// tests the real queries and components, never a mocked shape.

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	MIN int64 = 60_000
	H   int64 = 3600_000
	D   int64 = 24 * H
)

// FixturePasskeyPrefix marks the passkeys a fixture adds, so a reset never
// touches a real one.
const FixturePasskeyPrefix = "fx_"

// The kit's gradientAvatar(48) and gradientAvatar(320) as Node wrote them
// (zlib deflate), so the bytes match the TS fixture exactly. Go's deflate
// writes different bytes for the same pixels. Regenerate with
// testdata/gen/fixtures.ts.
var (
	//go:embed assets/gradient-48.png
	gradient48 []byte
	//go:embed assets/gradient-320.png
	gradient320 []byte
)

// GradientAvatar48 is gradientAvatar(): a 48 px gradient PNG as a data URL,
// the shape a browser-resized avatar photo has.
func GradientAvatar48() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(gradient48)
}

// Kit writes fixture rows. Every write goes through Q (the apply transaction).
type Kit struct {
	Q   db.Q
	Now int64
	Key string
	// Wall is the clock getDefaultAgent reads in TS (the kit calls it without
	// a time, so the default agent gets the real time, not Now).
	Wall    func() int64
	ownerID string
}

func newKit(q db.Q, now int64, key string, wall func() int64) (*Kit, error) {
	k := &Kit{Q: q, Now: now, Key: key, Wall: wall}
	var id string
	err := q.QueryRow("SELECT id FROM user LIMIT 1").Scan(&id)
	if err == nil {
		k.ownerID = id
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return k, nil
}

// col is one column of a raw insert.
type col struct {
	name string
	val  any
}

// insert writes one row; columns left out get the table default (drizzle
// passes the same default, or null where there is none).
func (k *Kit) insert(table string, cols ...col) {
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	vals := make([]any, len(cols))
	for i, c := range cols {
		names[i], marks[i], vals[i] = c.name, "?", c.val
	}
	k.exec("INSERT INTO "+table+" ("+strings.Join(names, ", ")+") VALUES ("+strings.Join(marks, ", ")+")", vals...)
}

// exec panics on a failed write; Apply turns the panic back into an error and
// the transaction rolls back.
func (k *Kit) exec(query string, args ...any) {
	if _, err := k.Q.Exec(query, args...); err != nil {
		panic(fixtureErr{err})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(fixtureErr{err})
	}
	return v
}

func check(err error) {
	if err != nil {
		panic(fixtureErr{err})
	}
}

type fixtureErr struct{ err error }

// js parses a JSON literal into jsonx values (objects keep their key order).
func js(s string) any {
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		panic(fmt.Sprintf("fixture: bad JSON literal: %v: %s", err, s))
	}
	return v
}

// str is JSON.stringify for a column value.
func str(v any) string { return jsonx.Stringify(v) }

// obj builds an ordered object from key, value pairs.
func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func ptr[T any](v T) *T { return &v }

// nullable turns a nil pointer into SQL NULL.
func nullable[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// ConnectorOpts is kit.ts ConnectorOpts.
type ConnectorOpts struct {
	Label  *string
	Status string
	Detail *string
	// Defaults to "running unless paused or a draft". Set it to make an
	// enabled connector that is paused anyway.
	Enabled *bool
	Secrets [][2]string
	Config  *jsonx.Object
	Trust   *string
}

func (k *Kit) Connector(id, typ string, o ConnectorOpts) {
	m, ok := registry.Manifest(typ)
	if !ok {
		panic(fixtureErr{fmt.Errorf(`fixture: unknown connector type "%s"`, typ)})
	}
	enabled := o.Status != "paused" && o.Status != "not_set_up"
	if o.Enabled != nil {
		enabled = *o.Enabled
	}
	in := connectors.UpsertInput{ID: id, Manifest: m.Row(), Label: o.Label, Config: o.Config, Trust: o.Trust, Enabled: &enabled}
	if o.Secrets != nil {
		s := jsonx.NewMap[string]()
		for _, kv := range o.Secrets {
			s.Set(kv[0], kv[1])
		}
		in.Secrets = s
	}
	must(connectors.Upsert(k.Q, in, k.Key, k.Now))
	if o.Status != "not_set_up" {
		check(connectors.SetStatus(k.Q, id, o.Status, o.Detail, k.Now))
	}
}

func (k *Kit) WebChat() db.Chat { return must(chats.EnsureWebChat(k.Q, k.Now)) }

// ChatOpts is kit.chat's options.
type ChatOpts struct {
	Kind      string
	Allowed   *bool
	CreatedAt *int64
	ReplyMode string
	Ack       *bool
	AckEmoji  string
}

func (k *Kit) Chat(channel, externalID, title string, o ChatOpts) db.Chat {
	at := k.Now
	if o.CreatedAt != nil {
		at = *o.CreatedAt
	}
	row := must(chats.Ensure(k.Q, chats.EnsureInput{Channel: channel, ExternalID: externalID, Title: title, Kind: o.Kind, Allowed: o.Allowed}, at))
	if o.ReplyMode != "" {
		k.exec("UPDATE chats SET reply_mode = ? WHERE id = ?", o.ReplyMode, row.ID)
	}
	if o.Ack != nil || o.AckEmoji != "" {
		ack := true
		if o.Ack != nil {
			ack = *o.Ack
		}
		if o.AckEmoji != "" {
			k.exec("UPDATE chats SET ack = ?, ack_emoji = ? WHERE id = ?", db.Bool(ack), o.AckEmoji, row.ID)
		} else {
			k.exec("UPDATE chats SET ack = ? WHERE id = ?", db.Bool(ack), row.ID)
		}
	}
	// ensureChat skips an empty title on update and never sets these two.
	if o.CreatedAt != nil {
		k.exec("UPDATE chats SET created_at = ? WHERE id = ?", *o.CreatedAt, row.ID)
	}
	return row
}

// Agent is kit.agent's values. Raw insert, so a fixture can use values at the
// form limits.
type Agent struct {
	Slug, Name, Persona  string
	OwnerPowers          string
	GuestTools           string
	DailyTokenCap        *int64
	ToolPermissions      any
	RecallContainer      *string
	RecallReadContainers []string
	RecallPerChat        *bool
	TelegramConnectorID  *string
	AvatarImage          *string
	AvatarIcon           *string
	AvatarColor          *string
	TokensToday          int64
}

func (k *Kit) Agent(v Agent) string {
	id := secret.NewID("agt")
	cols := []col{{"id", id}, {"slug", v.Slug}, {"name", v.Name}, {"persona", v.Persona}}
	if v.OwnerPowers != "" {
		cols = append(cols, col{"owner_powers", v.OwnerPowers})
	}
	if v.GuestTools != "" {
		cols = append(cols, col{"guest_tools", v.GuestTools})
	}
	if v.DailyTokenCap != nil {
		cols = append(cols, col{"daily_token_cap", *v.DailyTokenCap})
	}
	if v.ToolPermissions != nil {
		cols = append(cols, col{"tool_permissions", str(v.ToolPermissions)})
	}
	cols = append(cols, col{"recall_container", nullable(v.RecallContainer)})
	if v.RecallReadContainers != nil {
		cols = append(cols, col{"recall_read_containers", str(v.RecallReadContainers)})
	}
	if v.RecallPerChat != nil {
		cols = append(cols, col{"recall_per_chat", db.Bool(*v.RecallPerChat)})
	}
	cols = append(cols,
		col{"telegram_connector_id", nullable(v.TelegramConnectorID)},
		col{"avatar_image", nullable(v.AvatarImage)},
		col{"avatar_icon", nullable(v.AvatarIcon)},
		col{"avatar_color", nullable(v.AvatarColor)},
		col{"created_at", k.Now}, col{"updated_at", k.Now})
	k.insert("agents", cols...)
	if v.TokensToday != 0 {
		tz := must(settings.Timezone(k.Q))
		must(budget.AddUsage(k.Q, budget.AgentKey(id, k.Now, tz), v.TokensToday, k.Now))
	}
	return id
}

// OwnConnector: the agent that owns a tool connector (an agent-scoped MCP server).
func (k *Kit) OwnConnector(id, agentID string) {
	k.exec("UPDATE connectors SET agent_id = ? WHERE id = ?", agentID, id)
}

// Assign: a chat answered by an agent (PLAN 19.2).
func (k *Kit) Assign(chat db.Chat, agentID string, allowed bool) {
	k.exec("UPDATE chats SET agent_id = ?, allowed = ? WHERE id = ?", agentID, db.Bool(allowed), chat.ID)
}

// MsgExtra is the `extra` columns kit.message spreads over the row.
type MsgExtra struct {
	RunID        *string
	FromOwner    *bool
	SenderName   **string
	SenderID     *string
	Status       string
	IgnoreReason *string
}

func (k *Kit) Message(chat db.Chat, role, text string, at int64, x ...MsgExtra) string {
	id := secret.NewID("msg")
	cols := []col{{"id", id}, {"chat_id", chat.ID}, {"session_id", nullable(chat.SessionID)}, {"role", role},
		{"content", str(obj("text", text))}, {"text", text}, {"created_at", at}}
	for _, e := range x {
		if e.RunID != nil {
			cols = append(cols, col{"run_id", *e.RunID})
		}
		if e.FromOwner != nil {
			cols = append(cols, col{"from_owner", db.Bool(*e.FromOwner)})
		}
		if e.SenderName != nil {
			cols = append(cols, col{"sender_name", nullable(*e.SenderName)})
		}
		if e.SenderID != nil {
			cols = append(cols, col{"sender_id", *e.SenderID})
		}
		if e.Status != "" {
			cols = append(cols, col{"status", e.Status})
		}
		if e.IgnoreReason != nil {
			cols = append(cols, col{"ignore_reason", *e.IgnoreReason})
		}
	}
	k.insert("messages", cols...)
	return id
}

// Run is a runs row without its id. Nil pointers on columns with a default
// take the default; on the others they are null.
type Run struct {
	ChatID, AgentID, JobID                              *string
	Trigger                                             string
	Model                                               *string
	TokensIn, TokensOut, TokensCached, TokensCacheWrite *int64
	Steps                                               *int64
	Tainted                                             *bool
	Status                                              string
	Error, ResultPreview                                *string
	StartedAt                                           int64
	Ms                                                  *int64
}

func (k *Kit) Run(v Run) string {
	id := secret.NewID("run")
	cols := []col{{"id", id}, {"chat_id", nullable(v.ChatID)}, {"agent_id", nullable(v.AgentID)}, {"job_id", nullable(v.JobID)},
		{"trigger", v.Trigger}, {"model", nullable(v.Model)}}
	for _, c := range []struct {
		name string
		v    *int64
	}{{"tokens_in", v.TokensIn}, {"tokens_out", v.TokensOut}, {"tokens_cached", v.TokensCached}, {"tokens_cache_write", v.TokensCacheWrite}, {"steps", v.Steps}} {
		if c.v != nil {
			cols = append(cols, col{c.name, *c.v})
		}
	}
	if v.Tainted != nil {
		cols = append(cols, col{"tainted", db.Bool(*v.Tainted)})
	}
	if v.Status != "" {
		cols = append(cols, col{"status", v.Status})
	}
	cols = append(cols, col{"error", nullable(v.Error)}, col{"result_preview", nullable(v.ResultPreview)}, col{"started_at", v.StartedAt}, col{"ms", nullable(v.Ms)})
	k.insert("runs", cols...)
	return id
}

// Tool is a tool_calls row without its id. Args nil is null.
type Tool struct {
	RunID         string
	ConnectorID   *string
	Tool, Class   string
	Args          any
	ResultPreview *string
	Status        string
	Ms            *int64
	CreatedAt     int64
}

func (k *Kit) Tool(v Tool) {
	var args any
	if v.Args != nil {
		args = str(v.Args)
	}
	k.insert("tool_calls", col{"id", secret.NewID("tc")}, col{"run_id", v.RunID}, col{"connector_id", nullable(v.ConnectorID)}, col{"tool", v.Tool},
		col{"class", v.Class}, col{"args", args}, col{"result_preview", nullable(v.ResultPreview)}, col{"status", v.Status}, col{"ms", nullable(v.Ms)}, col{"created_at", v.CreatedAt})
}

// Approval is a pending_approvals row without id, messages and approvalId.
type Approval struct {
	RunID, ChatID, Tool, ToolClass string
	ArgsPreview                    *string
	ExpiresAt, CreatedAt           int64
}

func (k *Kit) Approval(v Approval) {
	id := secret.NewID("apr")
	k.insert("pending_approvals", col{"id", id}, col{"approval_id", id}, col{"messages", "[]"}, col{"run_id", v.RunID}, col{"chat_id", v.ChatID},
		col{"tool", v.Tool}, col{"tool_class", v.ToolClass}, col{"args_preview", nullable(v.ArgsPreview)}, col{"expires_at", v.ExpiresAt}, col{"created_at", v.CreatedAt})
}

// Reminder is kit.reminder's values.
type Reminder struct {
	ChatID, Text string
	RRule        *string
	TZ           string
	FireAt       int64
	Status       string
	SmartPrompt  *string
	LastFiredAt  *int64
	UpdatedAt    *int64
}

func (k *Kit) Reminder(v Reminder) {
	updated := k.Now
	if v.UpdatedAt != nil {
		updated = *v.UpdatedAt
	}
	k.insert("reminders", col{"id", secret.NewID("rem")}, col{"scheduled_for", v.FireAt}, col{"local_start", "2026-11-11T09:00:00"},
		col{"created_at", k.Now}, col{"updated_at", updated}, col{"chat_id", v.ChatID}, col{"text", v.Text}, col{"rrule", nullable(v.RRule)},
		col{"tz", v.TZ}, col{"fire_at", v.FireAt}, col{"status", v.Status}, col{"smart_prompt", nullable(v.SmartPrompt)}, col{"last_fired_at", nullable(v.LastFiredAt)})
}

// Contact is kit.contact's values.
type Contact struct {
	Name, Address string
	Policy        string
	Note          *string
	Removed       bool
	AgentID       string
}

// Send becomes an outbox row (the log) and a done outbound row.
type Send struct {
	Text       string
	Status     string
	At         int64
	Error      *string
	ApprovedBy string
	Held       bool
}

// Scheduled becomes a waiting outbound row.
type Scheduled struct {
	Text   string
	FireAt int64
	RRule  *string
}

func (k *Kit) defaultAgentID() string {
	return must(agents.GetDefault(k.Q, k.Wall())).ID
}

// Contact: a contact plus the messages sent to it.
func (k *Kit) Contact(chat db.Chat, v Contact, sends []Send, scheduled []Scheduled) string {
	id := secret.NewID("con")
	agentOf := func() string {
		if v.AgentID != "" {
			return v.AgentID
		}
		return k.defaultAgentID()
	}
	channel := "whatsapp"
	if chat.Channel == "telegram" {
		channel = "telegram"
	}
	policy := v.Policy
	if policy == "" {
		policy = "ask"
	}
	var removed any
	if v.Removed {
		removed = k.Now
	}
	agentID := agentOf()
	k.insert("contacts", col{"id", id}, col{"agent_id", agentID}, col{"name", v.Name}, col{"channel", channel}, col{"address", v.Address},
		col{"chat_id", chat.ID}, col{"policy", policy}, col{"note", nullable(v.Note)}, col{"created_at", k.Now}, col{"updated_at", k.Now}, col{"removed_at", removed})
	for _, s := range sends {
		obm := secret.NewID("obm")
		sendAt := s.At
		if s.Held {
			sendAt = k.Now + 10_000
		}
		origin := "chat"
		if s.ApprovedBy == "flow_version" {
			origin = "flow"
		}
		approvedBy := s.ApprovedBy
		if approvedBy == "" {
			approvedBy = "owner"
		}
		k.insert("outbound_messages", col{"id", obm}, col{"agent_id", agentOf()}, col{"contact_id", id}, col{"text", s.Text}, col{"origin", origin},
			col{"approved_by", approvedBy}, col{"tz", "Asia/Makassar"}, col{"local_start", "2026-11-11T09:00"}, col{"fire_at", sendAt}, col{"status", "done"},
			col{"created_at", s.At}, col{"updated_at", s.At})
		attempts := int64(0)
		if s.Status == "failed" {
			attempts = 6
		}
		var sentAt any
		if s.Status == "sent" {
			sentAt = s.At
		}
		k.insert("outbox", col{"id", secret.NewID("out")}, col{"chat_id", chat.ID}, col{"channel", chat.Channel},
			col{"payload", str(obj("text", s.Text, "format", "plain"))}, col{"status", s.Status}, col{"attempts", attempts}, col{"next_attempt_at", sendAt},
			col{"source", "outbound:" + obm}, col{"error", nullable(s.Error)}, col{"created_at", s.At}, col{"sent_at", sentAt})
	}
	for _, s := range scheduled {
		k.insert("outbound_messages", col{"id", secret.NewID("obm")}, col{"agent_id", agentOf()}, col{"contact_id", id}, col{"text", s.Text}, col{"origin", "chat"},
			col{"approved_by", "owner"}, col{"rrule", nullable(s.RRule)}, col{"tz", "Asia/Makassar"}, col{"local_start", "2026-11-11T09:00"}, col{"fire_at", s.FireAt},
			col{"status", "scheduled"}, col{"created_at", k.Now}, col{"updated_at", k.Now})
	}
	return id
}

// Job is kit.job's values. Nil pointers on columns with a default take it.
type Job struct {
	Name          string
	Kind          string
	RRule, TZ     string
	Prompt        string
	ChatID        string
	Enabled       *bool
	Status        string
	NextRunAt     *int64
	LastRunAt     *int64
	LastStatus    *string
	LastResult    *string
	DailyTokenCap *int64
	CreatedAt     *int64
}

func (k *Kit) Job(v Job) string {
	id := secret.NewID("job")
	created := k.Now
	if v.CreatedAt != nil {
		created = *v.CreatedAt
	}
	cols := []col{{"id", id}, {"local_start", "2026-01-01T07:00:00"}, {"created_at", created}, {"updated_at", k.Now}, {"name", v.Name}}
	if v.Kind != "" {
		cols = append(cols, col{"kind", v.Kind})
	}
	cols = append(cols, col{"rrule", v.RRule}, col{"tz", v.TZ}, col{"prompt", v.Prompt}, col{"chat_id", v.ChatID})
	if v.Enabled != nil {
		cols = append(cols, col{"enabled", db.Bool(*v.Enabled)})
	}
	if v.Status != "" {
		cols = append(cols, col{"status", v.Status})
	}
	cols = append(cols, col{"next_run_at", nullable(v.NextRunAt)}, col{"last_run_at", nullable(v.LastRunAt)}, col{"last_status", nullable(v.LastStatus)}, col{"last_result", nullable(v.LastResult)})
	if v.DailyTokenCap != nil {
		cols = append(cols, col{"daily_token_cap", *v.DailyTokenCap})
	}
	k.insert("jobs", cols...)
	return id
}

// Event goes straight into the table: no redaction and no listeners, so a
// fixture can write thousands of rows and keep its exact strings.
type Event struct {
	At          int64
	Message     string
	Level       string
	ConnectorID *string
	Kind        string
}

func (k *Kit) Event(v Event) {
	level, kind := v.Level, v.Kind
	if level == "" {
		level = "info"
	}
	if kind == "" {
		kind = "tool"
	}
	k.insert("events", col{"at", v.At}, col{"level", level}, col{"connector_id", nullable(v.ConnectorID)}, col{"kind", kind}, col{"message", v.Message})
}

// Hook is kit.hook's values.
type Hook struct {
	Name      string
	ChatID    string
	Mode      string
	Enabled   *bool
	LastAt    *int64
	CreatedAt *int64
	Slug      string
}

func (k *Kit) Hook(v Hook) {
	id := secret.NewID("hook")
	slug := v.Slug
	if slug == "" {
		slug = id[len(id)-16:]
	}
	mode := v.Mode
	if mode == "" {
		mode = "forward"
	}
	enabled := true
	if v.Enabled != nil {
		enabled = *v.Enabled
	}
	created := k.Now
	if v.CreatedAt != nil {
		created = *v.CreatedAt
	}
	enc := must(secret.Encrypt("phk_fixture-secret-not-a-real-one", k.Key))
	k.insert("hooks", col{"id", id}, col{"slug", slug}, col{"name", v.Name}, col{"secret", enc}, col{"mode", mode}, col{"chat_id", v.ChatID},
		col{"enabled", db.Bool(enabled)}, col{"last_at", nullable(v.LastAt)}, col{"created_at", created})
}

// Passkey is kit.passkey's values. CreatedAtNull writes null; a nil CreatedAt is Now.
type Passkey struct {
	Name          *string
	DeviceType    string
	BackedUp      *bool
	CreatedAt     *int64
	CreatedAtNull bool
}

// Passkey only writes when an owner exists (a passkey row needs a user). The
// key material is fake: these rows are for the Settings list.
func (k *Kit) Passkey(v Passkey) {
	if k.ownerID == "" {
		return
	}
	id := FixturePasskeyPrefix + secret.NewID("pk")
	deviceType := v.DeviceType
	if deviceType == "" {
		deviceType = "multiDevice"
	}
	backedUp := true
	if v.BackedUp != nil {
		backedUp = *v.BackedUp
	}
	var created any = k.Now
	if v.CreatedAtNull {
		created = nil
	} else if v.CreatedAt != nil {
		created = *v.CreatedAt
	}
	k.insert("passkey", col{"id", id}, col{"name", nullable(v.Name)}, col{"public_key", "fixture"}, col{"user_id", k.ownerID}, col{"credential_id", id},
		col{"counter", int64(0)}, col{"device_type", deviceType}, col{"backed_up", db.Bool(backedUp)}, col{"transports", "internal"}, col{"created_at", created})
}

// MCPGrant is a personal token (no Client) or an OAuth connection (Client
// set) for Settings > Security. Like Passkey it needs an owner. The hashes
// are fake, so no fixture token opens /mcp.
type MCPGrant struct {
	Name       string
	Hint       *string
	Client     *string
	ClientURI  *string
	CreatedAt  int64
	LastUsedAt *int64
}

func (k *Kit) MCPGrant(v MCPGrant) {
	if k.ownerID == "" {
		return
	}
	id := FixturePasskeyPrefix + secret.NewID("mcpg")
	kind := "token"
	var clientID, refreshExpires any
	if v.Client != nil {
		kind = "oauth"
		cid := FixturePasskeyPrefix + secret.NewID("plc")
		k.insert("oauth_clients", col{"id", cid}, col{"name", *v.Client}, col{"client_uri", nullable(v.ClientURI)},
			col{"redirect_uris", `["https://claude.ai/api/mcp/auth_callback"]`}, col{"auth_method", "none"}, col{"created_at", v.CreatedAt})
		clientID = cid
		refreshExpires = k.Now + 30*24*60*60*1000
	}
	k.insert("mcp_grants", col{"id", id}, col{"kind", kind}, col{"name", v.Name}, col{"user_id", k.ownerID}, col{"client_id", clientID},
		col{"token_hint", nullable(v.Hint)}, col{"access_hash", "fixture-" + id}, col{"refresh_expires_at", refreshExpires},
		col{"created_at", v.CreatedAt}, col{"last_used_at", nullable(v.LastUsedAt)})
}

// Instruction: no AgentID means Palui's (PLAN 19.1).
type Instruction struct {
	Title, Body string
	Enabled     *bool
	AgentID     *string
	CreatedAt   *int64
}

func (k *Kit) Instruction(v Instruction) {
	at := k.Now
	if v.CreatedAt != nil {
		at = *v.CreatedAt
	}
	enabled := true
	if v.Enabled != nil {
		enabled = *v.Enabled
	}
	k.insert("instructions", col{"id", secret.NewID("ins")}, col{"title", v.Title}, col{"body", v.Body}, col{"enabled", db.Bool(enabled)},
		col{"agent_id", nullable(v.AgentID)}, col{"created_at", at}, col{"updated_at", at})
}

// Preference is a raw insert: fixtures may use values the service would
// normalize or refuse.
func (k *Kit) Preference(key, value string) {
	k.insert("preferences", col{"key", key}, col{"value", value}, col{"updated_at", k.Now})
}

// Attachment is kit.attachment's values. Kind is "pdf", "jpeg" or "png".
type Attachment struct {
	ChatID      string
	Title       string
	Description string
	Kind        string
	Filename    string
	Size        *int64
	CreatedAt   *int64
}

// Attachment saves fake bytes behind the right magic number, so the type
// check passes; a png is a real image.
func (k *Kit) Attachment(v Attachment) string {
	at := k.Now
	if v.CreatedAt != nil {
		at = *v.CreatedAt
	}
	in := attachments.SaveInput{ChatID: v.ChatID, Title: v.Title, Description: v.Description, Filename: v.Filename}
	if v.Kind == "png" {
		in.MimeType, in.Bytes = "image/png", bytes.Clone(gradient320)
		return must(attachments.Save(k.Q, in, k.Key, at)).Row.ID
	}
	magic := []byte("%PDF-1.7\n")
	in.MimeType = "application/pdf"
	if v.Kind != "pdf" {
		magic, in.MimeType = []byte{0xff, 0xd8, 0xff, 0xe0}, "image/jpeg"
	}
	size := int64(2048)
	if v.Size != nil {
		size = *v.Size
	}
	b := make([]byte, max(size, int64(len(magic)+8)))
	copy(b, magic)
	// Buffer.write: as much of the id as fits.
	copy(b[len(magic):], secret.NewID("fx"))
	in.Bytes = b
	return must(attachments.Save(k.Q, in, k.Key, at)).Row.ID
}

// Share is kit.share's values: a share link with forced counters (the token
// is thrown away).
type Share struct {
	Visibility string
	Duration   string
	CreatedBy  string
	OpenCount  int64
	CreatedAt  *int64
}

func (k *Kit) Share(attachmentID string, v Share) {
	at := k.Now
	if v.CreatedAt != nil {
		at = *v.CreatedAt
	}
	duration, by := v.Duration, v.CreatedBy
	if duration == "" {
		duration = "24h"
	}
	if by == "" {
		by = "owner"
	}
	made, ok := func() (attachments.CreatedShare, bool) {
		m, ok, err := attachments.CreateShare(k.Q, attachments.ShareInput{AttachmentID: attachmentID, Visibility: v.Visibility, Duration: duration, CreatedBy: by}, at)
		check(err)
		return m, ok
	}()
	if ok && v.OpenCount != 0 {
		k.exec("UPDATE attachment_shares SET open_count = ?, last_opened_at = ? WHERE id = ?", v.OpenCount, k.Now, made.Share.ID)
	}
}

// FlowVersion is one later version of a fixture flow.
type FlowVersion struct {
	Definition any
	Note       *string
	CreatedBy  string
}

// FlowOpts is kit.flow's options.
type FlowOpts struct {
	Enabled          *bool
	PausedUntil      *int64
	Failures         int64
	LastError        *string
	UnapprovedWrites any
	Versions         []FlowVersion
	State            any
	CreatedAt        *int64
}

// Flow saves a flow through the real store (validated, triggers derived),
// then forces the columns a fixture needs: paused, failing, an unapproved
// version with write calls, flow variables.
func (k *Kit) Flow(chat db.Chat, definition any, o FlowOpts) string {
	at := k.Now - D
	if o.CreatedAt != nil {
		at = *o.CreatedAt
	}
	// TS createFlow builds contactResolver up front, and that calls
	// getDefaultAgent (wall clock), creating the default agent. Go's
	// flows.ContactResolver looks the agent up lazily, so do it here to write
	// the same rows (GO-PORT.md Known gaps, M8b rpcdev).
	k.defaultAgentID()
	saved := must(flows.CreateFlow(k.Q, flows.SaveInput{ChatID: chat.ID, Definition: definition, CreatedBy: "agent:run_fixture"}, at))
	versionID := saved.Version.ID
	for i, v := range o.Versions {
		by := v.CreatedBy
		if by == "" {
			by = "agent:run_fixture"
		}
		versionID = must(flows.UpdateFlow(k.Q, saved.Flow.ID, flows.UpdateInput{Definition: v.Definition, Note: v.Note, CreatedBy: by}, at+int64(i+1)*MIN)).Version.ID
	}
	if o.UnapprovedWrites != nil {
		k.exec("UPDATE flow_versions SET write_calls = ?, approved_at = NULL, approved_via = NULL WHERE id = ?", str(o.UnapprovedWrites), versionID)
	}
	enabled := true
	if o.Enabled != nil {
		enabled = *o.Enabled
	}
	var approved any = versionID
	if o.UnapprovedWrites != nil {
		approved = nil
	}
	state := o.State
	if state == nil {
		state = obj()
	}
	k.exec("UPDATE flows SET enabled = ?, paused_until = ?, consecutive_failures = ?, last_error = ?, approved_version_id = ?, state = ? WHERE id = ?",
		db.Bool(enabled), nullable(o.PausedUntil), o.Failures, nullable(o.LastError), approved, str(obj("values", state, "tainted", []any{})), saved.Flow.ID)
	return saved.Flow.ID
}

// FlowStep is one flow_step_runs row of a fixture run.
type FlowStep struct {
	StepID, Kind, Status string
	Branch               *string
	Error                *string
	Input, Output        any
	Ms                   *int64
	Tainted              bool
	Attempt              int64
}

// FlowRun is kit.flowRun's values.
type FlowRun struct {
	Status      string
	StartedAt   int64
	SkipReason  *string
	TriggerKind string
	TriggerID   *string
	ScheduledAt *int64
	EndedAt     *int64
	Error       *string
	Taint       []string
	Trigger     any
	Vars        any
	ParentRunID *string
	ItemIndex   *int64
	Wait        any
	WakeAt      *int64
	RunKey      string
	Steps       []FlowStep
}

// FlowRun writes a run with its step log, straight into the tables.
func (k *Kit) FlowRun(flowID string, v FlowRun) string {
	var versionID, chatID string
	check(k.Q.QueryRow("SELECT current_version_id, chat_id FROM flows WHERE id = ?", flowID).Scan(&versionID, &chatID))
	id := secret.NewID("flr")
	kind := v.TriggerKind
	if kind == "" {
		kind = "schedule"
	}
	runKey := v.RunKey
	if runKey == "" {
		runKey = "fx:" + id
	}
	trigger, vars := v.Trigger, v.Vars
	if trigger == nil {
		trigger = obj()
	}
	if vars == nil {
		vars = obj("steps", obj(), "vars", obj())
	}
	taint := v.Taint
	if taint == nil {
		taint = []string{}
	}
	var wait any
	if v.Wait != nil {
		wait = str(v.Wait)
	}
	var ended any
	if v.EndedAt != nil {
		ended = *v.EndedAt
	} else {
		switch v.Status {
		case "done", "failed", "skipped", "cancelled":
			ended = v.StartedAt + 1200
		}
	}
	k.insert("flow_runs", col{"id", id}, col{"flow_id", flowID}, col{"version_id", versionID}, col{"chat_id", chatID}, col{"trigger_id", nullable(v.TriggerID)},
		col{"trigger_kind", kind}, col{"run_key", runKey}, col{"parent_run_id", nullable(v.ParentRunID)}, col{"item_index", nullable(v.ItemIndex)},
		col{"status", v.Status}, col{"skip_reason", nullable(v.SkipReason)}, col{"trigger", str(trigger)}, col{"vars", str(vars)}, col{"taint", str(taint)},
		col{"cursor", nil}, col{"wait", wait}, col{"wake_at", nullable(v.WakeAt)}, col{"scheduled_at", nullable(v.ScheduledAt)}, col{"started_at", v.StartedAt},
		col{"ended_at", ended}, col{"error", nullable(v.Error)}, col{"updated_at", v.StartedAt})
	for i, st := range v.Steps {
		attempt := st.Attempt
		if attempt == 0 {
			attempt = 1
		}
		ms := int64(40)
		if st.Ms != nil {
			ms = *st.Ms
		}
		var input, output any
		if st.Input != nil {
			input = str(st.Input)
		}
		if st.Output != nil {
			output = str(st.Output)
		}
		k.insert("flow_step_runs", col{"id", secret.NewID("fls")}, col{"run_id", id}, col{"seq", int64(i + 1)}, col{"step_id", st.StepID}, col{"kind", st.Kind},
			col{"attempt", attempt}, col{"status", st.Status}, col{"input", input}, col{"output", output}, col{"tainted", db.Bool(st.Tainted)},
			col{"branch", nullable(st.Branch)}, col{"error", nullable(st.Error)}, col{"started_at", v.StartedAt + int64(i)*100}, col{"ms", ms})
	}
	return id
}

// Setting writes one typed setting.
func (k *Kit) Setting(key string, value any) {
	check(settings.Set(k.Q, key, value, k.Now))
}

// within throws when a value claims to be at a limit and is over it (UTF-16
// units, as JS .length).
func within(value string, limit int) string {
	if n := strx.Len16(value); n > limit {
		panic(fixtureErr{fmt.Errorf("fixture value is %d characters, over the limit of %d: %s", n, limit, strx.Slice16(value, 40))})
	}
	return value
}

// fill repeats a paragraph up to exactly `limit` characters.
func fill(paragraph string, limit int) string {
	n := strx.Len16(paragraph)
	return strx.Slice16(strings.Repeat(paragraph, (limit+n-1)/n), limit)
}
