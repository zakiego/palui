package chat

// The agent turn (turn.ts, PLAN 8.4 to 8.8): context, tools, the step loop
// within the budget, approvals, reply, bookkeeping.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/approvals"
	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/chatrules"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/memory"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/tools"
	"github.com/zakiego/palui/apps/server/internal/version"
)

const (
	ModelOffText = "Model lagi off. Nyalakan: `/connectors on cliproxy`"
	// What a shared chat hears instead of admin details.
	SharedBusyText = "Maaf, lagi gak bisa jawab sekarang. Coba lagi nanti ya."
	ReplyTTLMs     = 24 * 3600_000
	// A run cut short by a shutdown stays `running` with this error, so the
	// next start recovers it the same way as a crash.
	InterruptedError = "interrupted by a restart"
	InterruptedText  = "Turn tadi kepotong restart di tengah jalan, dan sempat jalanin aksi yang mengubah data. Cek dulu hasilnya ya, terus kirim ulang kalau masih perlu."
)

// What the model is told about each request when a run resumes.
var approvalReasons = map[string]string{
	"approved":   "Approved by the owner",
	"expired":    "No answer from the owner within 10 minutes",
	"superseded": "Dropped: the same call was made again later, so this older request no longer counts",
}

// Host is EngineHost: what the engine needs from the supervisor.
type Host interface {
	models.ModelHost
	Running() []connectors.RunningEntry
	Get(id string) (connectors.RunningEntry, bool)
	ChannelFor(channel string) connectors.ChannelHandle
	Status() []connectors.LiveStatus
	Reconcile()
	Release()
}

// ActiveTurn is a running turn: /stop cancels it, the supervisor asks which
// connectors it uses.
type ActiveTurn struct {
	RunID        string
	cancel       context.CancelCauseFunc
	mu           sync.Mutex
	connectorIDs map[string]bool
}

// Stop aborts the turn with this reason.
func (a *ActiveTurn) Stop(reason string) { a.cancel(errors.New(reason)) }

// Uses says whether the turn's tools come from the connector.
func (a *ActiveTurn) Uses(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connectorIDs[id]
}

// EngineCtx is the TS EngineCtx.
type EngineCtx struct {
	DB    *db.DB
	Host  Host
	Log   *logx.Logger
	Clock func() int64
	Bus   *Bus
	Deps  Deps
	// AppURL is config().appUrl (the dashboard prompt, /report).
	AppURL string
	// DataDir is config().dataDir (/report).
	DataDir       string
	ApprovalTTLMs int64
	// Tests only: overrides settings.budgets.turnTimeoutMs.
	TurnTimeoutMs int64
	// RetryDelay is the model retry's first wait (2 s); tests shorten it.
	RetryDelay time.Duration
	// RetryDelaysMs overrides RetryDelaysMs, the waits before a failed message
	// turn runs again (tests).
	RetryDelaysMs []int64
	// GroupLookupTimeout overrides the wait on a group's participant list
	// (GroupLookupTimeout, 3 s); tests shorten it.
	GroupLookupTimeout time.Duration
	// Monotonic is performance.now() in ms.
	Monotonic func() float64
	// ResumeRun queues the resume of a run whose requests are all decided.
	ResumeRun func(runID string)

	mu       sync.Mutex
	active   map[string]*ActiveTurn
	draining atomic.Bool
}

func (c *EngineCtx) setActive(chatID string, a *ActiveTurn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		c.active = map[string]*ActiveTurn{}
	}
	c.active[chatID] = a
}

func (c *EngineCtx) clearActive(chatID string, a *ActiveTurn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[chatID] == a {
		delete(c.active, chatID)
	}
}

// Active is the running turn of a chat, or nil.
func (c *EngineCtx) Active(chatID string) *ActiveTurn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active[chatID]
}

func (c *EngineCtx) allActive() map[string]*ActiveTurn {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]*ActiveTurn, len(c.active))
	for k, v := range c.active {
		out[k] = v
	}
	return out
}

// Draining says whether the engine is shutting down.
func (c *EngineCtx) Draining() bool { return c.draining.Load() }

// SetDraining is ctx.draining = v (tests use it to hold dispatch).
func (c *EngineCtx) SetDraining(v bool) { c.draining.Store(v) }

// TurnOptions are TurnOptions: how a turn starts.
type TurnOptions struct {
	ChatID string
	// message, schedule, hook, approval, reminder or flow.
	Trigger string
	Prompt  string
	JobID   string
	// Connector ids allowed (jobs); nil means all.
	ToolScope *[]string
	ModelRole string
	// nil is true.
	Deliver        *bool
	SilentToken    string
	NoTools        bool
	UntrustedInput bool
	// Flow prompt steps (RFC flows 5.6): a run id chosen before the call, the
	// agent whose persona answers, and namespaced tools allowed ("*": all).
	RunID     string
	AgentID   string
	ToolNames *[]string
}

// TurnResult is TurnResult. Status is ok, error, aborted, awaiting_approval,
// budget, no_model, draining, interrupted or retry (the messages failed for a
// temporary reason and run again in RetryInMs).
type TurnResult struct {
	RunID     string `json:"runId"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Tainted   bool   `json:"tainted"`
	RetryInMs int64  `json:"retryInMs,omitempty"`
}

type resumeData struct {
	PreviousRunID string
	Approvals     []db.Approval
	Context       approvals.ResumeContext
	Messages      []models.Message
}

// ExecInput is ExecInput: TurnOptions plus a message batch or a resume.
type ExecInput struct {
	TurnOptions
	BatchIDs []string
	Resume   *resumeData
}

// SendExtra is sendToChat's extra payload.
type SendExtra struct {
	Actions []db.OutboxAction
	Format  string
	// The channel message the reply quotes.
	ReplyTo string
}

// SendToChat enqueues a message to a chat's channel.
func SendToChat(c *EngineCtx, chat db.Chat, text, source string, extra SendExtra) (db.Outbox, error) {
	return sendToChatQ(c, c.DB, chat, text, source, extra)
}

// sendToChatQ is SendToChat inside a transaction.
func sendToChatQ(c *EngineCtx, q db.Q, chat db.Chat, text, source string, extra SendExtra) (db.Outbox, error) {
	now := c.Clock()
	exp := now + ReplyTTLMs
	row, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: text, Actions: extra.Actions, Format: extra.Format, ReplyToChannelMsgID: extra.ReplyTo}, Source: source, ExpiresAt: &exp}, now)
	if err != nil {
		return row, err
	}
	outbox.Notify()
	return row, nil
}

func (c *EngineCtx) send(chat db.Chat, text, source string, extra SendExtra) {
	if _, err := SendToChat(c, chat, text, source, extra); err != nil {
		c.Log.Error("enqueue failed", logx.Fields{"chat_id": chat.ID, "error": err})
	}
}

func (c *EngineCtx) event(in events.Input, now int64) {
	if _, err := events.Log(c.DB, in, now); err != nil {
		c.Log.Error("event write failed", logx.Fields{"kind": in.Kind, "error": err})
	}
}

// capabilityList: one line per enabled skill or tool connector.
// An outside turn names only connectors it has tools from.
func capabilityList(c *EngineCtx, collected *tools.Collected, agentID string, outside bool) []string {
	out := []string{}
	active := map[string]bool{}
	for _, n := range collected.Active() {
		active[n] = true
	}
	names := collected.Names()
	for _, e := range c.Host.Running() {
		if e.Row.Kind == "model" || e.Row.Kind == "channel" {
			continue
		}
		// Another agent's own MCP server isn't named either.
		if !connectors.ServesAgent(e.Row, agentID) {
			continue
		}
		loaded := 0
		var waiting []string
		for _, n := range names {
			m := collected.Meta[n]
			if m == nil || m.ConnectorID != e.ID {
				continue
			}
			if active[n] {
				loaded++
			} else {
				waiting = append(waiting, m.Tool)
			}
		}
		if len(waiting) == 0 {
			if loaded > 0 {
				out = append(out, fmt.Sprintf("%s (%d tools)", e.Row.Label, loaded))
			} else if e.Row.Kind == "memory" && !outside {
				// Recall still works without tools: it runs before the turn.
				out = append(out, e.Row.Label)
			}
			// A connector with no tools in this turn (a guest turn, a denied
			// connector) isn't named: the model would promise what it can't do.
			continue
		}
		shown := strings.Join(waiting[:min(8, len(waiting))], ", ")
		if len(waiting) > 8 {
			shown += fmt.Sprintf(", +%d more", len(waiting)-8)
		}
		pre := ""
		if loaded > 0 {
			pre = fmt.Sprintf("%d tools loaded, ", loaded)
		}
		out = append(out, fmt.Sprintf("%s (%s%d to load with %s: %s)", e.Row.Label, pre, len(waiting), tools.SearchTool, shown))
	}
	return out
}

// rememberLoaded keeps tools loaded by search for the rest of the session.
func rememberLoaded(q db.Q, sessionID string, names []string) error {
	var cur db.NullJSON[[]string]
	if err := q.QueryRow("SELECT loaded_tools FROM sessions WHERE id = ?", sessionID).Scan(&cur); err != nil && !isNoRows(err) {
		return err
	}
	seen := map[string]bool{}
	next := []string{}
	for _, n := range append(append([]string{}, names...), cur.V...) {
		if !seen[n] {
			seen[n] = true
			next = append(next, n)
		}
	}
	if len(next) > tools.StickyMax {
		next = next[:tools.StickyMax]
	}
	_, err := q.Exec("UPDATE sessions SET loaded_tools = ? WHERE id = ?", db.SomeJSON(next), sessionID)
	return err
}

func unattended(origin string) bool {
	return origin == "schedule" || origin == "reminder" || origin == "flow"
}

// IsSilent is isSilent: only the final step's text is judged against the token.
func IsSilent(finalStepText, token string) bool {
	t := strings.ToLower(strx.Trim(token))
	return t != "" && strings.ToLower(strx.Trim(finalStepText)) == t
}

func shortError(e string) string {
	if e == "" {
		e = "error"
	}
	return strx.Slice16(spaceRun.ReplaceAllString(logx.Redact(e), " "), 160)
}

func markBatch(q db.Q, ids []string, runID *string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{runID}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := q.Exec("UPDATE messages SET status = 'handled', run_id = ? WHERE id IN ("+placeholders(len(ids))+")", args...)
	return err
}

// previewValue is approvals/store preview: a string as is, anything else as
// JSON, nothing ("undefined") as "".
func previewValue(v any, undefined bool, limit int) string {
	if undefined {
		return ""
	}
	return approvals.Preview(v, limit)
}

type toolTimer struct {
	mu     sync.Mutex
	starts map[string]int64
}

func (t *toolTimer) start(id string, at int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.starts[id] = at
}

func (t *toolTimer) get(id string) (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.starts[id]
	return v, ok
}

func finishToolCall(c *EngineCtx, chatID, runID, previousRunID, toolCallID, tool, status, resultPreview string, timer *toolTimer) {
	var ms *int64
	if started, ok := timer.get(toolCallID); ok {
		d := c.Clock() - started
		ms = &d
	}
	ids := []any{runID + ":" + toolCallID}
	if previousRunID != "" {
		ids = append(ids, previousRunID+":"+toolCallID)
	}
	args := append([]any{status, resultPreview, ms}, ids...)
	if _, err := c.DB.Exec("UPDATE tool_calls SET status = ?, result_preview = ?, ms = ? WHERE id IN ("+placeholders(len(ids))+")", args...); err != nil {
		c.Log.Error("tool call update failed", logx.Fields{"run_id": runID, "error": err})
	}
	var m int64
	if ms != nil {
		m = *ms
	}
	c.Bus.Publish(TurnEvent{Type: "tool-result", ChatID: chatID, RunID: runID, ToolCallID: toolCallID, Tool: tool, Preview: resultPreview, Ms: m, Status: status})
}

// DeliverReply is deliverReply: the final reply, stored in the session
// (context and auto-save) and sent.
func DeliverReply(c *EngineCtx, chat db.Chat, runID, text string) {
	deliverReply(c, chat, runID, text, "")
}

// deliverReply is DeliverReply quoting the channel message replyTo ("" for none).
func deliverReply(c *EngineCtx, chat db.Chat, runID, text, replyTo string) {
	now := c.Clock()
	content, _ := db.Raw(map[string]string{"text": text})
	if _, err := c.DB.Exec("INSERT INTO messages (id, chat_id, session_id, role, content, text, status, run_id, created_at) VALUES (?, ?, ?, 'assistant', ?, ?, 'handled', ?, ?)", secret.NewID("msg"), chat.ID, chat.SessionID, content, text, runID, now); err != nil {
		c.Log.Error("reply write failed", logx.Fields{"chat_id": chat.ID, "error": err})
	}
	c.send(chat, text, "reply:"+runID, SendExtra{Format: "markdown", ReplyTo: replyTo})
}

// ownerChatFor is ownerChat(from.channel) ?? ownerChat("web").
func ownerChatFor(c *EngineCtx, channel string) (db.Chat, bool) {
	now := c.Clock()
	if ch, ok, err := chats.OwnerChat(c.DB, channel, now); err == nil && ok {
		return ch, true
	}
	if ch, ok, err := chats.OwnerChat(c.DB, "web", now); err == nil && ok {
		return ch, true
	}
	return db.Chat{}, false
}

// sendToOwner: admin notices from a shared chat go to the owner's DM (PLAN 19.4).
func sendToOwner(c *EngineCtx, from db.Chat, text string) {
	if to, ok := ownerChatFor(c, from.Channel); ok {
		c.send(to, text, "system", SendExtra{})
	}
}

// SendApprovalPrompt is sendApprovalPrompt. The prompt is asked in the chat
// the request came from, which is always the owner's (PLAN 19.4).
func SendApprovalPrompt(c *EngineCtx, chat db.Chat, row db.Approval, title string) {
	name := title
	if name == "" {
		name = strings.Replace(row.Tool, "__", " › ", 1)
	}
	kind := "aksi yang mengubah data"
	if row.ToolClass == "destructive" {
		kind = "aksi destruktif"
	}
	preview := ""
	if row.ArgsPreview != nil {
		preview = *row.ArgsPreview
	}
	text := fmt.Sprintf("Boleh aku jalanin %s? (%s)\n%s\n\nBalas ya atau batal. Kalau 10 menit gak dijawab, aku anggap batal.", name, kind, preview)
	c.send(chat, text, "approval:"+row.ID, SendExtra{Actions: []db.OutboxAction{{ID: "approve:" + row.ID, Label: "Ya"}, {ID: "deny:" + row.ID, Label: "Batal"}}})
	c.Bus.Publish(TurnEvent{Type: "approval", ChatID: chat.ID, RunID: row.RunID, ApprovalID: row.ID, Tool: row.Tool, Class: row.ToolClass, ArgsPreview: preview})
}

// jobCap reads a job's name, cap and zone (jobs is M4's table; the row is
// read here the way turn.ts did).
func jobCap(q db.Q, jobID string) (name string, cap int64, tz string, ok bool, err error) {
	err = q.QueryRow("SELECT name, daily_token_cap, tz FROM jobs WHERE id = ?", jobID).Scan(&name, &cap, &tz)
	if isNoRows(err) {
		return "", 0, "", false, nil
	}
	return name, cap, tz, err == nil, err
}

// ResumeBlocker is resumeBlocker: why a resume can't start right now, as text
// for the owner; "" when it can. Checked before the run is claimed.
func ResumeBlocker(c *EngineCtx, rc approvals.ResumeContext) (string, error) {
	now := c.Clock()
	check, err := budget.CheckGlobal(c.DB, now)
	if err != nil {
		return "", err
	}
	if !check.OK {
		if check.Fresh {
			c.event(events.Input{Kind: "budget.pause", Level: "warn", Message: "Turns paused: " + check.Reason}, now)
		}
		return budget.PausedNotice(c.DB, check)
	}
	if rc.JobID != "" {
		name, cap, tz, ok, err := jobCap(c.DB, rc.JobID)
		if err != nil {
			return "", err
		}
		if ok {
			over, err := budget.JobOverCap(c.DB, rc.JobID, cap, now, tz)
			if err != nil {
				return "", err
			}
			if over {
				return fmt.Sprintf("Job %s udah kena batas token hariannya.", name), nil
			}
		}
	}
	var own db.AgentModels
	if rc.AgentID != "" {
		agent, found, err := agents.Get(c.DB, rc.AgentID)
		if err != nil {
			return "", err
		}
		if found {
			own = agent.ModelRoles.V
		}
	}
	chain, err := models.ResolveAgentChain(c.DB, c.Host, own, rc.ModelRole)
	if err != nil {
		return "", err
	}
	if len(chain) == 0 {
		return ModelOffText, nil
	}
	return "", nil
}

// ResumeInput is resumeInput: the input that resumes a run whose approvals
// are all decided; nil when the run has none.
func ResumeInput(q db.Q, runID string) (*ExecInput, error) {
	rows, err := approvals.ForRun(q, runID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	first := rows[0]
	rc, err := approvals.DecodeContext(first.Context)
	if err != nil {
		return nil, err
	}
	msgs, err := models.ParseMessages(first.Messages)
	if err != nil {
		return nil, err
	}
	return &ExecInput{TurnOptions: TurnOptions{ChatID: first.ChatID, Trigger: "approval"}, Resume: &resumeData{PreviousRunID: runID, Approvals: rows, Context: rc, Messages: msgs}}, nil
}

// FinishResumedRun marks the asking run ok once its resume starts.
func FinishResumedRun(q db.Q, runID string) error {
	_, err := q.Exec("UPDATE runs SET status = 'ok' WHERE id = ?", runID)
	return err
}

// taintFromReplay marks the turn replay-tainted when the history it replays
// holds the result of an untrusted call: that content is in front of the
// model again, so approvals treat the turn as tainted. It doesn't touch the
// run's own taint (runs.tainted, TurnResult.Tainted, the auto-save skip),
// which keeps meaning "this run read something untrusted". A call to a tool
// this turn doesn't have counts as untrusted (fail closed), and as other
// people's words only when its name is a read-down tool. Calls that never ran
// carry nothing.
func taintFromReplay(calls []ToolCallRow, meta map[string]*tools.ToolMeta, tainted, othersWrote *atomic.Bool, log *logx.Logger) {
	for _, pc := range calls {
		if pc.Status != "ok" && pc.Status != "error" {
			continue
		}
		m := meta[pc.Tool]
		if m != nil && !m.Untrusted {
			continue
		}
		if !tainted.Load() {
			log.Info("turn replay-tainted by an untrusted call in its history", logx.Fields{"tool": pc.Tool})
		}
		tainted.Store(true)
		if (m == nil && agents.IsReadDownTool(pc.Tool)) || (m != nil && m.OthersWrote) {
			othersWrote.Store(true)
		}
	}
}

// resumeContextJSON writes the ResumeContext in the TS key order.
func resumeContextJSON(rc approvals.ResumeContext, scope, names *[]string, system []models.Message) *jsonx.Object {
	o := jsonx.NewMap[any]()
	o.Set("trigger", rc.Trigger)
	o.Set("originTrigger", rc.OriginTrigger)
	if rc.JobID != "" {
		o.Set("jobId", rc.JobID)
	}
	if scope != nil {
		o.Set("toolScope", *scope)
	} else {
		o.Set("toolScope", nil)
	}
	if names != nil {
		o.Set("toolNames", *names)
	} else {
		o.Set("toolNames", nil)
	}
	o.Set("modelRole", rc.ModelRole)
	o.Set("deliver", rc.Deliver)
	if rc.SilentToken != "" {
		o.Set("silentToken", rc.SilentToken)
	}
	o.Set("tainted", rc.Tainted)
	if rc.OthersWrote {
		o.Set("othersWrote", true)
	}
	if rc.ReplayTainted {
		o.Set("replayTainted", true)
	}
	o.Set("sessionId", rc.SessionID)
	o.Set("system", system)
	o.Set("batchRunId", rc.BatchRunID)
	o.Set("agentId", rc.AgentID)
	o.Set("mode", rc.Mode)
	return o
}

func approvalPreview(meta *tools.ToolMeta, inputJSON string, fallback func() string) string {
	if meta != nil && meta.Preview != nil {
		if s, ok := safePreview(meta.Preview, inputJSON); ok {
			return s
		}
	}
	return fallback()
}

func safePreview(fn func(json.RawMessage) string, input string) (s string, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return fn(json.RawMessage(input)), true
}

func safeNeedsApproval(fn func(json.RawMessage) bool, input string) (ask bool) {
	defer func() {
		if recover() != nil {
			ask = true
		}
	}()
	return fn(json.RawMessage(input))
}

// errText is (p.error as Error)?.message ?? String(p.error).
func errText(err error) string {
	if err == nil {
		return "undefined"
	}
	return err.Error()
}

func isToolChoiceViolation(err error) bool {
	var v *ToolChoiceViolationError
	return errors.As(err, &v)
}

// ExecuteTurn is executeTurn.
func ExecuteTurn(c *EngineCtx, input ExecInput) (TurnResult, error) {
	q := c.DB
	startedAt := c.Clock()
	chat, ok, err := chats.Get(q, input.ChatID)
	if err != nil {
		return TurnResult{}, err
	}
	if !ok {
		return TurnResult{}, fmt.Errorf("No chat %s", input.ChatID)
	}
	all, err := settings.All(q)
	if err != nil {
		return TurnResult{}, err
	}
	rs := input.Resume
	// The agent and whether this is a shared chat (PLAN 19).
	agentID := input.AgentID
	if rs != nil && rs.Context.AgentID != "" {
		agentID = rs.Context.AgentID
	}
	var agent db.Agent
	found := false
	if agentID != "" {
		if agent, found, err = agents.Get(q, agentID); err != nil {
			return TurnResult{}, err
		}
	}
	if !found {
		if agent, err = agents.ForChat(q, chat.AgentID, startedAt); err != nil {
			return TurnResult{}, err
		}
	}
	// Palui too: a group it answers in is read by everyone there (PLAN 19.4).
	shared, err := agents.IsSharedChat(q, chat)
	if err != nil {
		return TurnResult{}, err
	}
	deliver := true
	if rs != nil {
		deliver = rs.Context.Deliver
	} else if input.Deliver != nil {
		deliver = *input.Deliver
	}
	origin := input.Trigger
	if rs != nil {
		origin = rs.Context.OriginTrigger
	} else if input.Trigger == "approval" {
		origin = "message"
	}
	tlog := c.Log.Child(logx.Fields{"chat_id": chat.ID})
	isMessage := input.Trigger == "message"
	notify := isMessage || input.Trigger == "approval"

	// Budgets: a hit cap pauses turns and tells the owner (PLAN 5.2).
	check, err := budget.CheckGlobal(q, startedAt)
	if err != nil {
		return TurnResult{}, err
	}
	if !check.OK {
		notice, err := budget.PausedNotice(q, check)
		if err != nil {
			return TurnResult{}, err
		}
		if shared {
			if check.Fresh {
				sendToOwner(c, chat, notice)
			}
			if isMessage {
				c.send(chat, SharedBusyText, "system", SendExtra{})
			}
		} else if check.Fresh || isMessage {
			c.send(chat, notice, "system", SendExtra{})
		}
		if check.Fresh {
			c.event(events.Input{Kind: "budget.pause", Level: "warn", Message: "Turns paused: " + check.Reason}, startedAt)
		}
		return TurnResult{Status: "budget"}, markBatch(q, input.BatchIDs, nil)
	}
	over, err := budget.AgentOverCap(q, agent.ID, agent.DailyTokenCap, startedAt)
	if err != nil {
		return TurnResult{}, err
	}
	if over {
		first, err := budget.FirstAgentNoticeToday(q, agent.ID, startedAt)
		if err != nil {
			return TurnResult{}, err
		}
		if first {
			c.event(events.Input{Kind: "budget.agent", Level: "warn", Message: fmt.Sprintf("Agent %s hit its daily token cap (%d); it answers again tomorrow", agent.Name, agent.DailyTokenCap)}, startedAt)
			if isMessage {
				c.send(chat, fmt.Sprintf("%s lagi istirahat, jatah hari ini udah habis. Besok lagi ya.", agent.Name), "system", SendExtra{})
			}
		}
		return TurnResult{Status: "budget"}, markBatch(q, input.BatchIDs, nil)
	}
	jobID := input.JobID
	if rs != nil && rs.Context.JobID != "" {
		jobID = rs.Context.JobID
	}
	if jobID != "" {
		name, cap, tz, ok, err := jobCap(q, jobID)
		if err != nil {
			return TurnResult{}, err
		}
		if ok {
			over, err := budget.JobOverCap(q, jobID, cap, startedAt, tz)
			if err != nil {
				return TurnResult{}, err
			}
			if over {
				c.event(events.Input{Kind: "budget.job", Level: "warn", Message: fmt.Sprintf("Job %s hit its daily token cap (%d); run skipped", name, cap)}, startedAt)
				return TurnResult{Status: "budget"}, nil
			}
		}
	}

	// Context pieces that decide the model role.
	var session *db.Session
	if chat.SessionID != nil {
		if s, ok, err := chats.GetSession(q, *chat.SessionID); err != nil {
			return TurnResult{}, err
		} else if ok {
			session = &s
		}
	}
	batch := map[string]bool{}
	for _, id := range input.BatchIDs {
		batch[id] = true
	}
	history := []db.Message{}
	if isMessage && session != nil {
		if history, err = LoadHistory(q, session.ID, session.SummarizedUntil); err != nil {
			return TurnResult{}, err
		}
	}
	// Messages of this batch always make it in, even past the window.
	if len(batch) > 0 {
		inHistory := false
		for _, h := range history {
			if batch[h.ID] {
				inHistory = true
				break
			}
		}
		if !inHistory {
			args := make([]any, len(input.BatchIDs))
			for i, id := range input.BatchIDs {
				args[i] = id
			}
			rows, err := q.Query("SELECT "+db.MessageCols+" FROM messages WHERE id IN ("+placeholders(len(args))+") ORDER BY created_at ASC, id ASC", args...)
			extra, err := db.All(rows, err, db.ScanMessage)
			if err != nil {
				return TurnResult{}, err
			}
			history = append(history, extra...)
		}
	}
	role := input.ModelRole
	if rs != nil && rs.Context.ModelRole != "" {
		role = rs.Context.ModelRole
	}
	if role == "" {
		role = "default"
		if HasImages(history, batch) {
			role = "vision"
		}
	}
	// Who can read the chat decides the tools, not who sent the message (PLAN 19.4).
	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}
	audience, err := TurnAudience(c, chat, agent, sessionID, history)
	if err != nil {
		return TurnResult{}, err
	}
	mode := audience

	chain, err := models.ResolveAgentChain(q, c.Host, agent.ModelRoles.V, role)
	if err != nil {
		return TurnResult{}, err
	}
	if len(chain) == 0 {
		// The model connector is enabled but still starting (a restart, or
		// recovering from an error): keep the messages for when it's on.
		if isMessage && rs == nil && len(input.BatchIDs) > 0 {
			starting, err := modelStarting(q, agent.ModelRoles.V, role)
			if err != nil {
				return TurnResult{}, err
			}
			plan, err := planRetry(c, input.BatchIDs, "", nil, starting)
			if err != nil {
				return TurnResult{}, err
			}
			if plan.ok {
				if err := requeue(c, input.BatchIDs, "", plan, "no model connector is on yet"); err != nil {
					return TurnResult{}, err
				}
				tlog.Warn("no model connector running yet; turn retried later", logx.Fields{"role": role, "retry": plan.attempt, "delay_ms": plan.delayMs})
				return TurnResult{Status: "retry", RetryInMs: plan.delayMs}, nil
			}
			if starting {
				tlog.Warn("no model connector running; not retried", logx.Fields{"role": role, "reason": plan.reason})
			}
		}
		if isMessage {
			text := ModelOffText
			if shared {
				text = SharedBusyText
			}
			c.send(chat, text, "system", SendExtra{})
		}
		if err := markBatch(q, input.BatchIDs, nil); err != nil {
			return TurnResult{}, err
		}
		tlog.Warn("no model connector running", logx.Fields{"role": role})
		return TurnResult{Status: "no_model"}, nil
	}

	runID := input.RunID
	if runID == "" {
		runID = secret.NewID("run")
	}
	rlog := tlog.Child(logx.Fields{"run_id": runID})
	if _, err := q.Exec("INSERT INTO runs (id, chat_id, agent_id, job_id, trigger, status, started_at) VALUES (?, ?, ?, ?, ?, 'running', ?)", runID, chat.ID, agent.ID, ptrOrNil(jobID), input.Trigger, startedAt); err != nil {
		return TurnResult{}, err
	}
	if err := budget.RecordRunStart(q, startedAt); err != nil {
		return TurnResult{}, err
	}
	if err := markBatch(q, input.BatchIDs, &runID); err != nil {
		return TurnResult{}, err
	}

	ctrl, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	active := &ActiveTurn{RunID: runID, cancel: cancel, connectorIDs: map[string]bool{}}
	c.setActive(chat.ID, active)
	var tainted atomic.Bool
	tainted.Store(input.UntrustedInput || (rs != nil && rs.Context.Tainted) || QuotesOutsider(history, batch))
	// The turn read what other people wrote (another agent's chats or memory):
	// from then on no write runs on its own, "allow" included.
	var othersWrote atomic.Bool
	othersWrote.Store(rs != nil && rs.Context.OthersWrote)
	// The history replays an untrusted result: approvals see a tainted turn,
	// the run's own taint stays as is.
	var replayTainted atomic.Bool
	replayTainted.Store(rs != nil && rs.Context.ReplayTainted)
	approvalTainted := func() bool { return tainted.Load() || replayTainted.Load() }
	timer := &toolTimer{starts: map[string]int64{}}
	channel := channelOf(c, chat)
	typing := func(on bool) {
		t, ok := channel.(connectors.Typer)
		if !ok {
			return
		}
		reaction := ""
		if chat.Ack {
			reaction = chat.AckEmoji
		}
		go func() {
			defer func() { _ = recover() }()
			_ = t.Typing(context.Background(), chat.ExternalID, on, connectors.TypingOptions{Reaction: &reaction})
		}()
	}

	status := "ok"
	errMsg := ""
	hasErr := false
	setErr := func(s string) { errMsg, hasErr = s, true }
	text := ""
	var texts []string
	lastStepText := ""
	lastFinishReason := ""
	var usage TotalUsage
	steps := int64(0)
	type approvalReq struct {
		approvalID, toolCallID, toolName string
		input                            any
		inputJSON                        string
	}
	var requests []approvalReq
	var model *models.FailoverModel
	interrupted := false
	timeoutMs := all.Budgets.TurnTimeoutMs
	if c.TurnTimeoutMs > 0 {
		timeoutMs = c.TurnTimeoutMs
	}
	var ran []RanTool
	wrapUp := ""
	toolChoiceViolated := false
	var turnErr error
	retryInMs := int64(0)
	// A batch that already failed once: its answer quotes the first message
	// in a group, where other talk came in between.
	retried := false
	if isMessage && rs == nil && len(input.BatchIDs) > 0 {
		n, err := countRetries(q, input.BatchIDs)
		if err != nil {
			return TurnResult{}, err
		}
		retried = n > 0
	}
	replyTo := ""
	if retried && chat.Kind == "group" {
		replyTo = quoteTarget(q, input.BatchIDs)
	}
	failNotice := func() {
		if shared {
			c.send(chat, SharedBusyText, "system", SendExtra{})
			return
		}
		if toolChoiceViolated {
			limit := Limit{TimeoutMs: timeoutMs}
			if wrapUp == "steps" {
				limit = Limit{MaxSteps: all.Budgets.MaxSteps, Steps: true}
			}
			DeliverReply(c, chat, runID, LimitNotice(limit, ran))
			return
		}
		c.send(chat, fmt.Sprintf("Waduh, gagal jawab: %s. Coba lagi bentar ya.", shortError(errMsg)), "system", SendExtra{})
	}
	// retryOrFail: a message turn that failed for a temporary reason goes back
	// to the queue (the first retry silently, then once with a holding notice);
	// anything else, or past the cap or the window, gets the failure notice.
	retryOrFail := func() {
		if isMessage && rs == nil && !toolChoiceViolated {
			plan, err := planRetry(c, input.BatchIDs, runID, turnErr, models.IsTransient(turnErr))
			if err != nil {
				rlog.Error("retry check failed", logx.Fields{"error": err})
			} else if plan.ok {
				if err := requeue(c, input.BatchIDs, runID, plan, errMsg); err != nil {
					rlog.Error("requeue failed", logx.Fields{"error": err})
				} else {
					rlog.Warn("turn failed for a temporary reason; retried later", logx.Fields{"retry": plan.attempt, "delay_ms": plan.delayMs, "error": shortError(errMsg)})
					cause := errMsg
					errMsg += fmt.Sprintf("; retry %d in %ds", plan.attempt, plan.delayMs/1000)
					retryInMs = plan.delayMs
					if plan.attempt == 2 {
						if shared {
							c.send(chat, RetryHoldText, "system", SendExtra{})
						} else {
							c.send(chat, fmt.Sprintf(RetryHoldOwnerText, shortError(cause)), "system", SendExtra{})
						}
					}
					return
				}
			} else {
				rlog.Warn("turn failed; not retried", logx.Fields{"reason": plan.reason, "error": shortError(errMsg)})
				if plan.reason != "permanent error" {
					c.event(events.Input{Kind: "turn.retry_exhausted", Level: "warn", Message: fmt.Sprintf("Run %s failed and wasn't retried: %s", runID, plan.reason), Data: obj("runId", runID, "messageIds", input.BatchIDs)}, c.Clock())
				}
			}
		}
		failNotice()
	}

	body := func() error {
		batchRunID := runID
		if rs != nil {
			batchRunID = rs.Context.BatchRunID
			if batchRunID == "" {
				batchRunID = rs.PreviousRunID
			}
		}
		var ownerText *string
		if rs == nil {
			var lines []string
			for _, h := range history {
				if batch[h.ID] && h.Role == "user" && h.FromOwner {
					lines = append(lines, h.Text)
				}
			}
			if s := strings.Join(lines, "\n"); s != "" {
				ownerText = &s
			}
		}
		scope := agents.Scope(agent, chat.ID)
		ruleSetter, err := RuleSetterFor(q, chat, agent, audience, origin, rs != nil, history, batch)
		if err != nil {
			return err
		}
		turnInfo := connectors.TurnInfo{
			RunID: runID, ChatID: chat.ID, Channel: chat.Channel, Trigger: input.Trigger, TZ: all.Timezone, Now: startedAt,
			JobID: jobID, BatchRunID: batchRunID, AgentID: agent.ID, Origin: origin, Mode: mode,
			Tainted: approvalTainted, OwnerText: ownerText,
			Memory:     &connectors.MemoryScope{Container: scope.Container, Read: scope.Read, Locked: scope.Locked},
			RuleSetter: ruleSetter,
		}
		outside := audience == tools.AudienceOutside
		// Members and outside turns have nobody to ask (PLAN 19.4).
		open := tools.NoApprovals(audience)
		noTools := input.NoTools || origin == "hook" || (outside && agent.GuestTools == "none")
		toolScope := input.ToolScope
		toolNames := input.ToolNames
		if rs != nil {
			toolScope, toolNames = rs.Context.ToolScope, rs.Context.ToolNames
		}
		perms := agent.ToolPermissions.V
		collectAudience := ""
		deny := tools.DeniedTools(&perms)
		if open {
			collectAudience = audience
			// No approvals here: a tool the owner wants a say on isn't offered at all.
			deny = append(deny, tools.AskTools(&perms)...)
		}
		var collected *tools.Collected
		if noTools {
			collected = tools.Collect(ctrl, nil, tools.CollectOptions{})
		} else {
			var sticky []string
			if session != nil {
				sticky = session.LoadedTools.V
			}
			var scopeList, onlyList []string
			if toolScope != nil {
				scopeList = *toolScope
			}
			if toolNames != nil {
				onlyList = *toolNames
			}
			collected = tools.Collect(ctrl, c.Host.Running(), tools.CollectOptions{
				Turn:               turnInfo,
				MaxTools:           int(all.Budgets.MaxTools),
				Scope:              scopeList,
				Only:               onlyList,
				ExcludeDestructive: unattended(origin),
				Deny:               deny,
				Audience:           collectAudience,
				Sticky:             sticky,
				OnLoad: func(names []string) {
					if chat.SessionID != nil {
						if err := rememberLoaded(q, *chat.SessionID, names); err != nil {
							rlog.Warn("could not keep loaded tools", logx.Fields{"error": err})
						}
					}
				},
				Log: rlog,
				OnUntrustedResult: func(m *tools.ToolMeta) {
					if !tainted.Load() {
						rlog.Info("turn tainted by an untrusted tool result", logx.Fields{"tool": m.Name})
					}
					tainted.Store(true)
					if m.OthersWrote {
						othersWrote.Store(true)
					}
				},
				OnToolStart: func(id string) { timer.start(id, c.Clock()) },
			})
		}
		active.mu.Lock()
		for _, id := range collected.ConnectorIDs {
			active.connectorIDs[id] = true
		}
		active.mu.Unlock()
		if len(collected.Deferred) > 0 {
			rlog.Info("more tools than maxTools, the rest load through tool search", logx.Fields{"max_tools": all.Budgets.MaxTools, "active": len(collected.Active()), "deferred": len(collected.Deferred)})
		}

		// Messages and system prompt.
		var system []models.Message
		var modelMessages []models.Message
		if rs != nil {
			// Approvals asked before the prompt was split into blocks stored one string.
			var s string
			if json.Unmarshal(rs.Context.System, &s) == nil {
				system = []models.Message{models.System(s)}
			} else if system, err = models.ParseMessages(rs.Context.System); err != nil {
				return err
			}
			var responses []models.Part
			approvedIDs := map[string]bool{}
			// A resume lands in a turn with no approvals only from a request asked
			// before PLAN 19.9 step 1, or when the chat stopped being the owner's since.
			// Nothing it approved runs there.
			if open {
				rlog.Warn("approval resumed in a chat with no approvals; its calls are denied", logx.Fields{"mode": rs.Context.Mode})
			}
			for _, a := range rs.Approvals {
				approved := a.Status == "approved" && !open
				reason, ok := approvalReasons[a.Status]
				if !ok {
					reason = "Denied by the owner"
				}
				if open {
					reason = tools.OutsideApprovalError
				}
				responses = append(responses, models.ToolApprovalResponsePart(a.ApprovalID, approved, &reason))
				if approved && a.ToolCallID != nil && *a.ToolCallID != "" {
					approvedIDs[*a.ToolCallID] = true
				}
			}
			modelMessages = append(MarkStaleReads(rs.Messages, approvedIDs), models.ToolMessage(responses...))
		} else {
			var lines []string
			for _, h := range history {
				if batch[h.ID] {
					lines = append(lines, h.Text)
				}
			}
			batchText := strings.Join(lines, "\n")
			recall := ""
			if isMessage {
				recall, _ = memory.AutoRecall(ctrl, q, memory.Find(c.Host.Running()), batchText, rlog, startedAt, &agent, chat.ID)
			}
			notesText, err := notes.Prompt(q, agent, startedAt)
			if err != nil {
				return err
			}
			rulesText := ""
			if shared {
				if rulesText, err = chatrules.Prompt(q, agent.ID, chat.ID); err != nil {
					return err
				}
			}
			var sharedInfo *SharedInfo
			if shared {
				sharedInfo = &SharedInfo{Kind: chat.Kind, Guest: open, Members: audience == tools.AudienceMembers}
			}
			dashboard := ""
			if origin == "message" {
				dashboard = DashboardPrompt(c.AppURL, agent.Slug)
			}
			var inbound *InboundInfo
			if origin == "message" {
				inbound = &InboundInfo{}
				for _, e := range c.Host.Running() {
					if agent.TranscribeVoice && e.Handle.Transcribe != nil {
						inbound.Voice = true
						break
					}
				}
			}
			replyMode := ""
			if chat.Kind == "group" {
				replyMode = chat.ReplyMode
			}
			summary := ""
			if isMessage && session != nil && session.Summary != nil {
				summary = *session.Summary
			}
			system = SystemBlocks(BuildSystem(SystemInput{
				Persona:      agent.Persona,
				Channel:      chat.Channel,
				Chat:         &SystemChat{ID: chat.ID, ExternalID: chat.ExternalID, Kind: chat.Kind, SessionID: chat.SessionID},
				Capabilities: capabilityList(c, collected, agent.ID, outside),
				ToolSearch:   len(collected.Deferred) > 0,
				Notes:        notesText,
				ChatRules:    rulesText,
				RuleSetter:   ruleSetter != nil,
				Trigger:      origin,
				Shared:       sharedInfo,
				Dashboard:    dashboard,
				ReplyMode:    replyMode,
				Inbound:      inbound,
			}), summary)
			checkins := ""
			// Flow check-ins are the owner's; a shared chat never sees them.
			if isMessage && !shared && c.Deps.Flows != nil {
				if checkins, err = c.Deps.Flows.CheckinsPrompt(q, chat.ID, startedAt); err != nil {
					return err
				}
			}
			recallAbout := "chats"
			if agent.IsDefault {
				recallAbout = "owner"
			}
			setupNote := ""
			if isMessage && session != nil {
				snap := TakeSetupSnapshot(SetupInput{
					Version: version.Version, Commit: currentCommit(), Audience: audience, Tools: collected,
					Persona: agent.Persona, Notes: notesText, ChatRules: rulesText,
				})
				if setupNote, err = SessionSetupNote(q, session, snap, buildSubjects); err != nil {
					return err
				}
				if setupNote != "" {
					rlog.Info("setup changed since the last turn", logx.Fields{"note": setupNote})
				}
			}
			turnContext := BuildTurnContext(TurnContextInput{Now: startedAt, TZ: all.Timezone, SetupChanges: setupNote, Checkins: checkins, Recall: recall, RecallAbout: recallAbout})
			// Past tool calls are replayed only next to a tool list.
			var pastCalls []ToolCallRow
			if collected.Tools.Len() > 0 {
				if pastCalls, err = LoadToolCalls(q, history); err != nil {
					return err
				}
				if open {
					// A tool this turn doesn't have (one an older turn ran) isn't
					// replayed by name and result.
					kept := pastCalls[:0]
					for _, pc := range pastCalls {
						if collected.Meta[pc.Tool] != nil {
							kept = append(kept, pc)
						}
					}
					pastCalls = kept
				}
				taintFromReplay(pastCalls, collected.Meta, &replayTainted, &othersWrote, rlog)
			}
			var base []models.Message
			if isMessage {
				// In a mention-mode group the batch sits among group talk, so
				// its messages are labeled as the ones to answer.
				var addressed map[string]bool
				if chat.Kind == "group" && chat.ReplyMode == "mention" {
					addressed = batch
				}
				base = ToModelMessages(history, batch, pastCalls, shared, addressed)
			} else {
				base = []models.Message{models.User(input.Prompt)}
			}
			// Only a chat's history comes back on the next turn; a job prompt stands alone.
			modelMessages = WithTurnContext(base, turnContext, isMessage)
		}

		model, err = models.NewFailoverModel(chain, func(from models.ModelLink, e error) {
			rlog.Warn("model failed, trying the next one in the chain", logx.Fields{"connector_id": from.ConnectorID, "model": from.Model, "error": e.Error()})
		})
		if err != nil {
			return err
		}
		// Jobs and hooks: every write needs approval.
		strict := unattended(origin) || origin == "hook"
		// The owner's own message: the agent's tool permissions may skip the prompt here, nowhere else.
		ownerTurn := origin == "message" && mode == tools.AudienceOwner
		typing(true)
		c.Bus.Publish(TurnEvent{Type: "turn-start", ChatID: chat.ID, RunID: runID})
		clock := NewStepClock(c.Monotonic())
		hasTools := collected.Tools.Len() > 0
		deferred := len(collected.Deferred) > 0

		supersede := func(toolName, inputJSON string) {
			args, _ := jsonx.Parse([]byte(inputJSON))
			r, err := approvals.SupersedeSameCall(q, approvals.SameCall{ChatID: chat.ID, RunID: runID, Tool: toolName, Args: args}, c.Clock())
			if err != nil {
				rlog.Error("supersede failed", logx.Fields{"error": err})
				return
			}
			for _, a := range r.Superseded {
				c.event(events.Input{Kind: "approval.superseded", Message: fmt.Sprintf("%s was called again; the older request was dropped", a.Tool), Data: obj("approvalId", a.ID, "runId", a.RunID)}, c.Clock())
			}
			for _, id := range r.Resume {
				c.ResumeRun(id)
			}
		}
		noteRan := func(toolName string, ok bool) {
			meta := collected.Meta[toolName]
			name := toolName
			if meta != nil {
				name = meta.Tool
			} else if i := strings.LastIndex(toolName, "__"); i >= 0 {
				name = toolName[i+2:]
			}
			write := true
			if meta != nil {
				write = meta.Class != "read"
			}
			ran = append(ran, RanTool{Name: name, Write: write, OK: ok})
		}
		classOf := func(name string) string {
			if m := collected.Meta[name]; m != nil {
				return m.Class
			}
			return "destructive"
		}
		previousRunID := ""
		if rs != nil {
			previousRunID = rs.PreviousRunID
		}

		timeoutCtx, stopTimeout := context.WithTimeout(ctrl, time.Duration(timeoutMs)*time.Millisecond)
		defer stopTimeout()
		stepText := ""
		maxSteps := int(all.Budgets.MaxSteps)
		var activeTools []string
		if deferred {
			activeTools = collected.Active()
		}
		result := RunLoop(timeoutCtx, LoopOptions{
			Model:       model,
			System:      system,
			Messages:    modelMessages,
			Tools:       collected.Tools,
			ActiveTools: activeTools,
			RetryDelay:  c.RetryDelay,
			// With tool search on, only the active names reach the model; near
			// the step or time limit, the step answers with no tools.
			PrepareStep: func(stepNumber int, msgs []models.Message) StepPrep {
				var prep StepPrep
				if deferred {
					prep.ActiveTools = collected.Active()
				}
				reason := ""
				if hasTools {
					reason = ShouldWrapUp(clock, WrapUpInput{StepNumber: stepNumber, MaxSteps: maxSteps, TimeoutMs: timeoutMs, Now: c.Monotonic()})
				}
				if reason == "" {
					return prep
				}
				wrapUp = reason
				rlog.Info("turn near its limit, asking for the reply now", logx.Fields{"reason": reason, "step": stepNumber, "elapsed_ms": int64(c.Monotonic() - clock.StartedAt + 0.5)})
				prep.ToolChoice = models.ChoiceNone
				prep.Messages = append(append([]models.Message{}, msgs...), models.User(WrapUpNote))
				return prep
			},
			MaxRetries: 1,
			StopWhen:   func(n int) bool { return n == maxSteps || c.Draining() },
			ToolApproval: func(call ToolCallView) bool {
				meta := collected.Meta[call.ToolName]
				// Taint at call time: the approvals of a step are decided before
				// any of its calls run.
				if meta != nil && meta.Untrusted && !tainted.Load() {
					rlog.Info("turn tainted by an untrusted tool call", logx.Fields{"tool": meta.Name})
					tainted.Store(true)
				}
				if meta != nil && meta.OthersWrote {
					othersWrote.Store(true)
				}
				// A members or outside turn has no approvals. Its tools run right
				// away, and the tool wrapper refuses a call whose tool asks on its own.
				if open {
					return false
				}
				// A tool's own rule asks first, and no per-agent "allow" skips it.
				if meta != nil && meta.NeedsApproval != nil && safeNeedsApproval(meta.NeedsApproval, call.InputJSON) {
					return true
				}
				class := "destructive"
				if meta != nil {
					class = meta.Class
				}
				return tools.NeedsApproval(tools.ApprovalInput{
					Class:         class,
					Permission:    tools.PermissionFor(&perms, call.ToolName),
					OwnerTurn:     ownerTurn,
					Tainted:       tainted.Load(),
					ReplayTainted: replayTainted.Load(),
					OwnerReply:    meta != nil && meta.OwnerReply,
					OthersWrote:   othersWrote.Load(),
					Strict:        strict,
					SendsFile:     len(attachments.FindRefs(call.Input)) > 0,
				})
			},
			Emit: func(p Part) {
				switch p.Type {
				case "start-step":
					stepText = ""
				case "text-delta":
					stepText += p.Text
					c.Bus.Publish(TurnEvent{Type: "text-delta", ChatID: chat.ID, RunID: runID, Delta: p.Text})
				case "finish-step":
					steps++
					lastFinishReason = p.FinishReason
					lastStepText = strx.Trim(stepText)
					if lastStepText != "" {
						texts = append(texts, lastStepText)
					}
					stepText = ""
				case "tool-call":
					var connectorID *string
					if m := collected.Meta[p.ToolName]; m != nil {
						connectorID = &m.ConnectorID
					}
					if _, err := q.Exec("INSERT INTO tool_calls (id, run_id, connector_id, tool, class, args, status, created_at) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?) ON CONFLICT DO NOTHING",
						runID+":"+p.ToolCallID, runID, connectorID, p.ToolName, classOf(p.ToolName), db.RawJSON(p.InputJSON), c.Clock()); err != nil {
						rlog.Error("tool call write failed", logx.Fields{"error": err})
					}
					c.Bus.Publish(TurnEvent{Type: "tool-call", ChatID: chat.ID, RunID: runID, ToolCallID: p.ToolCallID, Tool: p.ToolName, Class: classOf(p.ToolName), Args: json.RawMessage(p.InputJSON)})
				case "tool-approval-request":
					requests = append(requests, approvalReq{approvalID: p.ApprovalID, toolCallID: p.ToolCallID, toolName: p.ToolName, input: p.Input, inputJSON: p.InputJSON})
				case "tool-result":
					supersede(p.ToolName, p.InputJSON)
					noteRan(p.ToolName, true)
					finishToolCall(c, chat.ID, runID, previousRunID, p.ToolCallID, p.ToolName, "ok", logx.Redact(previewValue(p.Output, p.OutputUndefined, 500)), timer)
				case "tool-error":
					supersede(p.ToolName, p.InputJSON)
					noteRan(p.ToolName, false)
					finishToolCall(c, chat.ID, runID, previousRunID, p.ToolCallID, p.ToolName, "error", logx.Redact(approvals.Preview(errText(p.Err), 500)), timer)
				case "tool-output-denied":
					finishToolCall(c, chat.ID, runID, previousRunID, p.ToolCallID, p.ToolName, "denied", "denied", timer)
				case "error":
					status = "error"
					turnErr = p.Err
					setErr(errText(p.Err))
					if isToolChoiceViolation(p.Err) {
						toolChoiceViolated = true
					}
				case "abort":
					status = "aborted"
				case "finish":
					usage = p.Usage
				}
			},
		})
		if s := strx.Trim(stepText); s != "" {
			lastStepText = s
			texts = append(texts, lastStepText)
		}
		text = strings.Join(texts, "\n\n")
		silentToken := input.SilentToken
		if rs != nil {
			silentToken = rs.Context.SilentToken
		}
		silent := IsSilent(lastStepText, silentToken)
		// stopWhen ends the loop on a drain right after a tool step: the answer never came.
		if c.Draining() && len(requests) == 0 && (status == "aborted" || (status == "ok" && lastFinishReason == "tool-calls")) {
			interrupted = true
		}

		switch {
		case interrupted:
			rlog.Warn("turn interrupted by a shutdown; it is recovered on the next start")
		case len(requests) > 0 && status != "error" && status != "aborted":
			rc := approvals.ResumeContext{
				Trigger: input.Trigger, OriginTrigger: origin, JobID: jobID, ModelRole: role, Deliver: deliver,
				SilentToken: silentToken, Tainted: tainted.Load(), OthersWrote: othersWrote.Load(), ReplayTainted: replayTainted.Load(), SessionID: chat.SessionID,
				BatchRunID: batchRunID, AgentID: agent.ID, Mode: mode,
			}
			ctxJSON := resumeContextJSON(rc, toolScope, toolNames, system)
			stored, err := models.MarshalMessages(append(append([]models.Message{}, modelMessages...), result.ResponseMessages...))
			if err != nil {
				return err
			}
			if text != "" && deliver && !silent {
				DeliverReply(c, chat, runID, text)
			}
			for _, r := range requests {
				meta := collected.Meta[r.toolName]
				supersede(r.toolName, r.inputJSON)
				preview := approvalPreview(meta, r.inputJSON, func() string {
					labeled, err := attachments.LabelFileRefs(q, r.input)
					if err != nil {
						labeled = r.input
					}
					return approvals.Preview(labeled, 300)
				})
				row, err := approvals.Create(q, approvals.CreateInput{
					RunID: runID, ChatID: chat.ID, ApprovalID: r.approvalID, ToolCallID: r.toolCallID,
					Tool: r.toolName, ToolClass: classOf(r.toolName), ArgsPreview: preview, Messages: db.RawJSON(stored), ContextJSON: ctxJSON,
				}, c.Clock(), c.ApprovalTTLMs)
				if err != nil {
					return err
				}
				title := ""
				if meta != nil {
					title = meta.Title
				}
				SendApprovalPrompt(c, chat, row, title)
			}
			status = "awaiting_approval"
		case status == "ok":
			if text != "" && deliver && !silent {
				deliverReply(c, chat, runID, text, replyTo)
			}
		case status == "error" && notify:
			retryOrFail()
		}
		return nil
	}
	if err := body(); err != nil {
		// turn.ts's catch: a thrown error, not a stream error part.
		aborted := ctrl.Err() != nil
		if aborted {
			status = "aborted"
		} else {
			status = "error"
		}
		setErr(err.Error())
		turnErr = err
		if aborted && c.Draining() {
			interrupted = true
		} else if !aborted {
			rlog.Error("turn failed", logx.Fields{"error": err})
		}
		if !interrupted && status == "error" && notify {
			retryOrFail()
		}
	}
	typing(false)
	c.clearActive(chat.ID, active)
	c.Host.Release()

	if interrupted {
		status = "running"
		setErr(InterruptedError)
	} else if status == "aborted" {
		if ctrl.Err() != nil {
			if !hasErr {
				setErr("stopped by the owner")
			}
		} else {
			setErr("turn timed out")
			// The owner gets told, and the notice is saved as the reply so the
			// next turn knows why it stopped.
			if notify {
				if shared {
					c.send(chat, SharedBusyText, "system", SendExtra{})
				} else {
					DeliverReply(c, chat, runID, LimitNotice(Limit{TimeoutMs: timeoutMs}, ran))
				}
			}
		}
	}
	ms := c.Clock() - startedAt
	modelName, modelConnector := chain[0].Model, chain[0].ConnectorID
	var served *models.ModelLink
	if model != nil {
		served = model.Served()
	}
	if served != nil {
		modelName, modelConnector = served.Model, served.ConnectorID
	}
	var dbErr *string
	if hasErr && errMsg != "" {
		r := logx.Redact(errMsg)
		dbErr = &r
	}
	if _, err := q.Exec("UPDATE runs SET status = ?, error = ?, tokens_in = ?, tokens_out = ?, tokens_cached = ?, tokens_cache_write = ?, steps = ?, tainted = ?, model = ?, model_connector_id = ?, result_preview = ?, ms = ? WHERE id = ?",
		status, dbErr, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, steps, db.Bool(tainted.Load()), modelName, modelConnector, approvals.Preview(text, 300), ms, runID); err != nil {
		return TurnResult{}, err
	}
	// Cache reads count for a tenth against the caps.
	cached := usage.CacheReadTokens
	spent := budget.Tokens(budget.Usage{TokensIn: usage.InputTokens, TokensOut: usage.OutputTokens, TokensCached: &cached})
	if err := budget.RecordRunTokens(q, spent, c.Clock()); err != nil {
		return TurnResult{}, err
	}
	if err := budget.RecordAgentTokens(q, agent.ID, spent, c.Clock()); err != nil {
		return TurnResult{}, err
	}
	after, err := budget.CheckGlobal(q, c.Clock())
	if err != nil {
		return TurnResult{}, err
	}
	if !after.OK && after.Fresh {
		notice, err := budget.PausedNotice(q, after)
		if err != nil {
			return TurnResult{}, err
		}
		if shared {
			sendToOwner(c, chat, notice)
		} else {
			c.send(chat, notice, "system", SendExtra{})
		}
		c.event(events.Input{Kind: "budget.pause", Level: "warn", Message: "Turns paused: " + after.Reason}, c.Clock())
	}
	endStatus := status
	if interrupted {
		endStatus = "aborted"
	}
	var endErr *string
	if hasErr {
		e := errMsg
		endErr = &e
	}
	c.Bus.Publish(TurnEvent{Type: "turn-end", ChatID: chat.ID, RunID: runID, Status: endStatus, Error: endErr})
	logStatus := status
	if interrupted {
		logStatus = "interrupted"
	}
	fields := logx.Fields{"status": logStatus, "steps": steps, "tokens_in": usage.InputTokens, "tokens_out": usage.OutputTokens, "tokens_cached": usage.CacheReadTokens, "tokens_cache_write": usage.CacheWriteTokens, "ms": ms, "tainted": tainted.Load()}
	if served != nil {
		fields["connector_id"] = served.ConnectorID
	}
	if len(batch) > 1 {
		// Several messages answered by one reply: the first thing to check
		// when someone says theirs was skipped.
		fields["batch"] = len(batch)
	}
	rlog.Info("turn finished", fields)
	if interrupted {
		return TurnResult{RunID: runID, Status: "interrupted", Tainted: tainted.Load()}, nil
	}
	if retryInMs > 0 {
		return TurnResult{RunID: runID, Status: "retry", Tainted: tainted.Load(), RetryInMs: retryInMs}, nil
	}
	return TurnResult{RunID: runID, Text: text, Status: status, Tainted: tainted.Load()}, nil
}

// --- crash recovery -------------------------------------------------------------------------

// MaxRecoveries is MAX_RECOVERIES.
const MaxRecoveries = 2

const poisonText = "Pesan tadi bikin Palui keputus berkali-kali, jadi aku lewati dulu. Coba kirim ulang dengan versi yang lebih pendek atau tanpa lampiran."

// countRecoveries: how many times any of these messages was already re-queued after a crash.
func countRecoveries(q db.Q, ids []string) (int, error) {
	best := 0
	for _, id := range ids {
		var n int
		if err := q.QueryRow("SELECT count(*) FROM events WHERE kind = 'run.recovered' AND data LIKE ?", "%\""+id+"\"%").Scan(&n); err != nil {
			return 0, err
		}
		best = max(best, n)
	}
	return best, nil
}

// Recovered is recoverInterruptedRuns' result.
type Recovered struct {
	Requeued int `json:"requeued"`
	Failed   int `json:"failed"`
}

// RecoverInterruptedRuns is recoverInterruptedRuns: runs still `running` when
// the engine starts were cut off by a crash or a drain. A message turn that
// changed nothing is run again; anything that may have changed data is marked
// `error` and the owner is told.
func RecoverInterruptedRuns(c *EngineCtx) (Recovered, error) {
	q := c.DB
	now := c.Clock()
	var out Recovered
	rows, err := q.Query("SELECT " + db.RunCols + " FROM runs WHERE status = 'running'")
	runs, err := db.All(rows, err, db.ScanRun)
	if err != nil {
		return out, err
	}
	for _, run := range runs {
		var x string
		err := q.QueryRow("SELECT id FROM pending_approvals WHERE run_id = ?", run.ID).Scan(&x)
		if err == nil {
			// It got as far as asking for approval: it's waiting, not lost.
			if _, err := q.Exec("UPDATE runs SET status = 'awaiting_approval', error = NULL WHERE id = ?", run.ID); err != nil {
				return out, err
			}
			continue
		}
		if !isNoRows(err) {
			return out, err
		}
		replied, err := exists(q, "SELECT id FROM messages WHERE run_id = ? AND role = 'assistant'", run.ID)
		if err != nil {
			return out, err
		}
		// Only calls that ran: rows are written when the model emits the call.
		changed, err := exists(q, "SELECT id FROM tool_calls WHERE run_id = ? AND class IN ('write', 'destructive') AND status IN ('ok', 'error')", run.ID)
		if err != nil {
			return out, err
		}
		var batch []string
		if run.Trigger == "message" {
			rows, err := q.Query("SELECT id FROM messages WHERE run_id = ? AND role = 'user'", run.ID)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return out, err
				}
				batch = append(batch, id)
			}
			rows.Close()
		}
		recoveries := 0
		if len(batch) > 0 {
			if recoveries, err = countRecoveries(q, batch); err != nil {
				return out, err
			}
		}
		chatFor := func() (db.Chat, bool) {
			if run.ChatID == nil {
				return db.Chat{}, false
			}
			ch, ok, err := chats.Get(q, *run.ChatID)
			return ch, ok && err == nil
		}
		batchIDs := make([]any, len(batch))
		for i, id := range batch {
			batchIDs[i] = id
		}
		safe := !replied && !changed && run.Trigger != "approval" && len(batch) > 0
		if safe && recoveries >= MaxRecoveries {
			// A batch that already crashed the process twice is a poison message.
			if _, err := q.Exec("UPDATE runs SET status = 'error', error = ? WHERE id = ?", fmt.Sprintf("%s; skipped after %d recoveries", InterruptedError, recoveries), run.ID); err != nil {
				return out, err
			}
			c.event(events.Input{Kind: "run.poison", Level: "error", Message: fmt.Sprintf("Run %s was cut off %d times; its messages were skipped", run.ID, recoveries+1), Data: obj("runId", run.ID, "messageIds", batch)}, now)
			if ch, ok := chatFor(); ok {
				c.send(ch, poisonText, "system", SendExtra{})
			}
			out.Failed++
			continue
		}
		if safe {
			err := q.Tx(func(tx db.Q) error {
				if _, err := tx.Exec("UPDATE messages SET status = 'pending', run_id = NULL WHERE run_id = ? AND role = 'user'", run.ID); err != nil {
					return err
				}
				_, err := tx.Exec("UPDATE runs SET status = 'error', error = ? WHERE id = ?", InterruptedError+"; its messages were queued again", run.ID)
				return err
			})
			if err != nil {
				return out, err
			}
			c.event(events.Input{Kind: "run.recovered", Level: "warn", Message: fmt.Sprintf("Run %s was cut off by a restart; %d message(s) queued again", run.ID, len(batch)), Data: obj("runId", run.ID, "messageIds", batch)}, now)
			out.Requeued++
			continue
		}
		if _, err := q.Exec("UPDATE runs SET status = 'error', error = ? WHERE id = ?", InterruptedError, run.ID); err != nil {
			return out, err
		}
		out.Failed++
		unsafe := !replied && (changed || run.Trigger == "approval")
		msg := fmt.Sprintf("Run %s (%s) was cut off by a restart", run.ID, run.Trigger)
		if unsafe {
			msg += " after it may have changed data; the owner was told"
		}
		c.event(events.Input{Kind: "run.interrupted", Level: "warn", Message: msg, Data: obj("runId", run.ID)}, now)
		if unsafe {
			if ch, ok := chatFor(); ok {
				c.send(ch, InterruptedText, "system", SendExtra{})
			}
		}
	}
	return out, nil
}

// obj builds an ordered JSON object from key/value pairs (event data keeps
// the TS key order).
func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func exists(q db.Q, query string, args ...any) (bool, error) {
	var id string
	err := q.QueryRow(query+" LIMIT 1", args...).Scan(&id)
	if isNoRows(err) {
		return false, nil
	}
	return err == nil, err
}
