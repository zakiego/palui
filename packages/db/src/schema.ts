// Palui's SQLite schema (PLAN.md section 9). Timestamps are unix milliseconds.
// JSON columns are stored as text and typed with $type<>().
import { blob, index, integer, sqliteTable, text, uniqueIndex } from "drizzle-orm/sqlite-core"

const ms = (name: string) => integer(name, { mode: "timestamp_ms" })
const bool = (name: string) => integer(name, { mode: "boolean" })
const json = <T>(name: string) => text(name, { mode: "json" }).$type<T>()

// --- Better Auth (passkey plugin, username kept for the CLI-created owner) ---------

export const user = sqliteTable("user", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  email: text("email").notNull().unique(),
  emailVerified: bool("email_verified").notNull().default(false),
  image: text("image"),
  username: text("username").unique(),
  displayUsername: text("display_username"),
  createdAt: ms("created_at").notNull(),
  updatedAt: ms("updated_at").notNull(),
})

export const session = sqliteTable(
  "session",
  {
    id: text("id").primaryKey(),
    expiresAt: ms("expires_at").notNull(),
    token: text("token").notNull().unique(),
    createdAt: ms("created_at").notNull(),
    updatedAt: ms("updated_at").notNull(),
    ipAddress: text("ip_address"),
    userAgent: text("user_agent"),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
  },
  (t) => [index("session_user_id_idx").on(t.userId)],
)

export const account = sqliteTable(
  "account",
  {
    id: text("id").primaryKey(),
    accountId: text("account_id").notNull(),
    providerId: text("provider_id").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    accessToken: text("access_token"),
    refreshToken: text("refresh_token"),
    idToken: text("id_token"),
    accessTokenExpiresAt: ms("access_token_expires_at"),
    refreshTokenExpiresAt: ms("refresh_token_expires_at"),
    scope: text("scope"),
    password: text("password"),
    createdAt: ms("created_at").notNull(),
    updatedAt: ms("updated_at").notNull(),
  },
  (t) => [index("account_user_id_idx").on(t.userId)],
)

export const verification = sqliteTable(
  "verification",
  {
    id: text("id").primaryKey(),
    identifier: text("identifier").notNull(),
    value: text("value").notNull(),
    expiresAt: ms("expires_at").notNull(),
    createdAt: ms("created_at").notNull(),
    updatedAt: ms("updated_at").notNull(),
  },
  (t) => [index("verification_identifier_idx").on(t.identifier)],
)

export const passkey = sqliteTable(
  "passkey",
  {
    id: text("id").primaryKey(),
    name: text("name"),
    publicKey: text("public_key").notNull(),
    userId: text("user_id")
      .notNull()
      .references(() => user.id, { onDelete: "cascade" }),
    credentialID: text("credential_id").notNull().unique(),
    counter: integer("counter").notNull(),
    deviceType: text("device_type").notNull(),
    backedUp: bool("backed_up").notNull(),
    transports: text("transports"),
    createdAt: ms("created_at"),
    aaguid: text("aaguid"),
  },
  (t) => [index("passkey_user_id_idx").on(t.userId)],
)

// Single-use owner invite links from `palui owner invite` (15 minutes). Also the
// recovery path for a lost passkey. Only the token's sha256 is stored.
export const ownerInvites = sqliteTable("owner_invites", {
  id: text("id").primaryKey(),
  tokenHash: text("token_hash").notNull().unique(),
  expiresAt: integer("expires_at").notNull(),
  usedAt: integer("used_at"),
  createdAt: integer("created_at").notNull(),
})

// --- Connectors -----------------------------------------------------------------------

export const CONNECTOR_KINDS = ["channel", "model", "memory", "tools", "skill"] as const
export type ConnectorKind = (typeof CONNECTOR_KINDS)[number]

// not_set_up: a draft (wizard progress kept). connecting/on/paused/error per PLAN 4.2.
export const CONNECTOR_STATUSES = ["not_set_up", "connecting", "on", "paused", "error"] as const
export type ConnectorStatus = (typeof CONNECTOR_STATUSES)[number]

export type Trust = "own" | "external"

export const connectors = sqliteTable("connectors", {
  // Instance id: "telegram", "cliproxy", "mcp-vorneo"...
  id: text("id").primaryKey(),
  // Manifest id the instance was made from: "telegram", "mcp"...
  type: text("type").notNull(),
  kind: text("kind").$type<ConnectorKind>().notNull(),
  label: text("label").notNull(),
  // Desired state: true means the supervisor should keep it running.
  enabled: bool("enabled").notNull().default(false),
  status: text("status").$type<ConnectorStatus>().notNull().default("not_set_up"),
  statusDetail: text("status_detail"),
  trust: text("trust").$type<Trust>().notNull().default("own"),
  config: json<Record<string, unknown>>("config").notNull().default({}),
  // The one agent that owns this connector (an agent-scoped MCP server): only its
  // turns get the tools. Null means global, every agent. Never more than one
  // owner: two agents that need the same server with different logins get a
  // copy each.
  agentId: text("agent_id"),
  // AES-256-GCM blob (packages/db/src/crypto.ts) of a JSON object, or null.
  secrets: text("secrets"),
  configHash: text("config_hash").notNull().default(""),
  // When the connector entered `error`, for the 5 minute alert.
  errorSince: integer("error_since"),
  alertedAt: integer("alerted_at"),
  sortOrder: integer("sort_order").notNull().default(0),
  createdAt: integer("created_at").notNull(),
  updatedAt: integer("updated_at").notNull(),
})

// --- Channels, chats, sessions, messages ------------------------------------------

export type Channel = "telegram" | "whatsapp" | "web"
export const REPLY_MODES = ["mention", "all"] as const
export type ReplyMode = (typeof REPLY_MODES)[number]

export const identities = sqliteTable(
  "identities",
  {
    id: text("id").primaryKey(),
    channel: text("channel").$type<Channel>().notNull(),
    // Telegram user id, WhatsApp phone JID (628...@s.whatsapp.net), or the web user id.
    externalId: text("external_id").notNull(),
    // WhatsApp LID (...@lid) learned from the first DM that matches the number.
    altId: text("alt_id"),
    displayName: text("display_name"),
    role: text("role").$type<"owner">().notNull().default("owner"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [uniqueIndex("identities_channel_external").on(t.channel, t.externalId), index("identities_alt").on(t.channel, t.altId)],
)

export const claims = sqliteTable("claims", {
  nonce: text("nonce").primaryKey(),
  channel: text("channel").$type<Channel>().notNull(),
  expiresAt: integer("expires_at").notNull(),
  usedAt: integer("used_at"),
  usedBy: text("used_by"),
  createdAt: integer("created_at").notNull(),
})

export const chats = sqliteTable(
  "chats",
  {
    id: text("id").primaryKey(),
    channel: text("channel").$type<Channel>().notNull(),
    // Telegram chat id, WhatsApp JID, "web" for the dashboard chat.
    externalId: text("external_id").notNull(),
    title: text("title").notNull().default(""),
    kind: text("kind").$type<"dm" | "group">().notNull().default("dm"),
    sessionId: text("session_id"),
    // Which bot the chat came in on, when a channel has several (an agent's own
    // Telegram bot, PLAN 19.6). "" for the channel's main connector.
    connectorId: text("connector_id").notNull().default(""),
    // WhatsApp allowlist (PLAN 5.1): Palui only answers allowed chats. Shared
    // chats also need an agent before guests are answered (PLAN 19.2).
    allowed: bool("allowed").notNull().default(true),
    // The agent that answers here; null means the default agent (Palui).
    agentId: text("agent_id"),
    // Groups only: "mention" answers @mentions and replies to the bot, "all"
    // answers every message. DMs always answer every message.
    replyMode: text("reply_mode").$type<ReplyMode>().notNull().default("mention"),
    lastMessageAt: integer("last_message_at"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [uniqueIndex("chats_channel_connector_external").on(t.channel, t.connectorId, t.externalId)],
)

export const sessions = sqliteTable(
  "sessions",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    summary: text("summary"),
    // Messages created at or before this time are folded into `summary`.
    summarizedUntil: integer("summarized_until"),
    savedToRecallAt: integer("saved_to_recall_at"),
    // Namespaced tool names loaded through tool search, most recent first. They
    // stay in the turn's active tools for the rest of the session.
    loadedTools: json<string[]>("loaded_tools"),
    createdAt: integer("created_at").notNull(),
    closedAt: integer("closed_at"),
  },
  (t) => [index("sessions_chat").on(t.chatId)],
)

export type MessageRole = "user" | "assistant" | "tool" | "system"
export type MessageStatus = "pending" | "handled" | "ignored"

// Content is AI SDK ModelMessage content, or { text } for plain inbound.
export const messages = sqliteTable(
  "messages",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    sessionId: text("session_id"),
    role: text("role").$type<MessageRole>().notNull(),
    content: json<unknown>("content").notNull(),
    // Plain text for transcripts, search and auto-save.
    text: text("text").notNull().default(""),
    channelMsgId: text("channel_msg_id"),
    // Inbound: the message this one quotes (reply-to), for WhatsApp reminder actions.
    replyToChannelMsgId: text("reply_to_channel_msg_id"),
    senderId: text("sender_id"),
    // Inbound: the sender's display name, and whether it was the owner (PLAN 19.3).
    senderName: text("sender_name"),
    fromOwner: bool("from_owner").notNull().default(true),
    status: text("status").$type<MessageStatus>().notNull().default("handled"),
    ignoreReason: text("ignore_reason"),
    runId: text("run_id"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [
    index("messages_chat_created").on(t.chatId, t.createdAt),
    index("messages_status").on(t.status),
    index("messages_session").on(t.sessionId, t.createdAt),
  ],
)

// --- Runs and tools ---------------------------------------------------------------

// flow: a prompt step of a flow (RFC flows 5.6).
export type RunTrigger = "message" | "schedule" | "hook" | "approval" | "reminder" | "flow"
export type RunStatus = "running" | "ok" | "error" | "aborted" | "awaiting_approval" | "budget"

export const runs = sqliteTable(
  "runs",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id"),
    // The agent that ran it (PLAN 19); null for runs from before agents.
    agentId: text("agent_id"),
    jobId: text("job_id"),
    trigger: text("trigger").$type<RunTrigger>().notNull(),
    model: text("model"),
    modelConnectorId: text("model_connector_id"),
    tokensIn: integer("tokens_in").notNull().default(0),
    tokensOut: integer("tokens_out").notNull().default(0),
    // Input counts (PLAN 5.2). `tokensIn` is everything the model was sent. Of it,
    // `tokensCached` was read from the prompt cache (about a tenth of the price)
    // and `tokensCacheWrite` was stored in it for later turns (1.25x to 2x).
    tokensCached: integer("tokens_cached").notNull().default(0),
    tokensCacheWrite: integer("tokens_cache_write").notNull().default(0),
    steps: integer("steps").notNull().default(0),
    tainted: bool("tainted").notNull().default(false),
    status: text("status").$type<RunStatus>().notNull().default("running"),
    error: text("error"),
    resultPreview: text("result_preview"),
    startedAt: integer("started_at").notNull(),
    ms: integer("ms"),
  },
  (t) => [index("runs_started").on(t.startedAt), index("runs_job").on(t.jobId, t.startedAt)],
)

export type ToolClass = "read" | "write" | "destructive"

export const toolCalls = sqliteTable(
  "tool_calls",
  {
    id: text("id").primaryKey(),
    runId: text("run_id").notNull(),
    connectorId: text("connector_id"),
    tool: text("tool").notNull(),
    class: text("class").$type<ToolClass>().notNull(),
    args: json<unknown>("args"),
    resultPreview: text("result_preview"),
    status: text("status").$type<"ok" | "error" | "denied" | "pending">().notNull(),
    ms: integer("ms"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [index("tool_calls_run").on(t.runId), index("tool_calls_created").on(t.createdAt)],
)

// `superseded`: the same call was made again before the owner answered this request.
export type ApprovalStatus = "pending" | "approved" | "denied" | "expired" | "superseded" | "resumed"

export const pendingApprovals = sqliteTable(
  "pending_approvals",
  {
    id: text("id").primaryKey(),
    runId: text("run_id").notNull(),
    chatId: text("chat_id").notNull(),
    // Where the prompt went when that isn't chatId: a shared chat asks in the
    // owner's DM (PLAN 19.4). Only an answer from here counts.
    askChatId: text("ask_chat_id"),
    // AI SDK approvalId from the tool-approval-request part.
    approvalId: text("approval_id").notNull(),
    toolCallId: text("tool_call_id"),
    tool: text("tool").notNull(),
    toolClass: text("tool_class").$type<ToolClass>().notNull(),
    argsPreview: text("args_preview"),
    // The response messages to resume from (AI SDK ModelMessage[]), plus turn context.
    messages: json<unknown[]>("messages").notNull(),
    context: json<Record<string, unknown>>("context").notNull().default({}),
    promptMsgId: text("prompt_msg_id"),
    expiresAt: integer("expires_at").notNull(),
    status: text("status").$type<ApprovalStatus>().notNull().default("pending"),
    decidedAt: integer("decided_at"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [index("approvals_chat_status").on(t.chatId, t.status), index("approvals_status_expires").on(t.status, t.expiresAt)],
)

// --- Saved attachments ------------------------------------------------------------

export type AttachmentKind = "image" | "document"

// Files the agent chose to keep from a chat (KTP, receipts, tickets). The bytes
// live in `data`, AES-256-GCM encrypted with MASTER_KEY (encryptBytes), so they
// ride along in the SQLite backups and never sit on disk in the clear.
export const savedAttachments = sqliteTable(
  "saved_attachments",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    // The inbound message the file came with.
    messageId: text("message_id"),
    runId: text("run_id"),
    title: text("title").notNull(),
    description: text("description").notNull().default(""),
    kind: text("kind").$type<AttachmentKind>().notNull(),
    mimeType: text("mime_type").notNull(),
    filename: text("filename"),
    // Plaintext size in bytes.
    size: integer("size").notNull(),
    // sha256 of the plaintext, hex. Saving the same file twice in a chat returns the first row.
    sha256: text("sha256").notNull(),
    data: blob("data", { mode: "buffer" }).$type<Buffer>().notNull(),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [index("saved_attachments_chat_created").on(t.chatId, t.createdAt), index("saved_attachments_chat_sha").on(t.chatId, t.sha256)],
)

export type ShareVisibility = "public" | "private"

// Temporary links to one saved attachment, served at GET /api/share/:token.
// "public" opens for anyone holding the link, "private" also needs a dashboard
// sign-in. Only the token's sha256 is stored, so a link is shown once, when it
// is made. Rows go when the attachment is deleted.
export const attachmentShares = sqliteTable(
  "attachment_shares",
  {
    id: text("id").primaryKey(),
    attachmentId: text("attachment_id").notNull(),
    tokenHash: text("token_hash").notNull().unique(),
    visibility: text("visibility").$type<ShareVisibility>().notNull(),
    // "owner" from the dashboard, or the chat id the agent shared it in.
    createdBy: text("created_by").notNull(),
    expiresAt: integer("expires_at").notNull(),
    revokedAt: integer("revoked_at"),
    openCount: integer("open_count").notNull().default(0),
    lastOpenedAt: integer("last_opened_at"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [index("attachment_shares_attachment").on(t.attachmentId, t.createdAt)],
)

// --- Reminders and jobs -----------------------------------------------------------

export type ReminderStatus = "pending" | "firing" | "done" | "cancelled"

export const reminders = sqliteTable(
  "reminders",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    text: text("text").notNull(),
    // RRULE body without DTSTART (e.g. "FREQ=WEEKLY;BYDAY=MO"), null for one-off.
    rrule: text("rrule"),
    tz: text("tz").notNull(),
    // Wall-clock anchor in `tz` (ISO local, no offset): the series start and time of day.
    localStart: text("local_start").notNull(),
    // Next fire time, UTC ms.
    fireAt: integer("fire_at").notNull(),
    // The time this occurrence was meant for (fireAt before snoozes), for the "telat" note.
    scheduledFor: integer("scheduled_for").notNull(),
    status: text("status").$type<ReminderStatus>().notNull().default("pending"),
    lastFiredAt: integer("last_fired_at"),
    snoozedUntil: integer("snoozed_until"),
    // "Smart reminder": run this prompt as a turn at fire time.
    smartPrompt: text("smart_prompt"),
    // The outbox row of the last fire, so "done"/"tunda" replies can find it.
    lastOutboxId: text("last_outbox_id"),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [index("reminders_status_fire").on(t.status, t.fireAt), index("reminders_chat").on(t.chatId)],
)

export type JobKind = "job" | "heartbeat" | "briefing"

export const jobs = sqliteTable(
  "jobs",
  {
    id: text("id").primaryKey(),
    name: text("name").notNull(),
    kind: text("kind").$type<JobKind>().notNull().default("job"),
    rrule: text("rrule").notNull(),
    tz: text("tz").notNull(),
    localStart: text("local_start").notNull(),
    prompt: text("prompt").notNull(),
    chatId: text("chat_id").notNull(),
    modelRole: text("model_role").$type<"default" | "fast" | "vision">().notNull().default("default"),
    // Connector ids whose tools the job may use. null means every enabled connector.
    toolScope: json<string[] | null>("tool_scope"),
    enabled: bool("enabled").notNull().default(true),
    nextRunAt: integer("next_run_at"),
    status: text("status").$type<"idle" | "running">().notNull().default("idle"),
    lastRunAt: integer("last_run_at"),
    lastStatus: text("last_status"),
    lastResult: text("last_result"),
    dailyTokenCap: integer("daily_token_cap").notNull().default(50_000),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [index("jobs_next").on(t.enabled, t.nextRunAt)],
)

// --- Routines (retired) -------------------------------------------------------------
// Routines were replaced by flows. No code reads or writes these two tables any
// more; they stay in the schema for one release so a rollback still finds them,
// and a later migration drops them (with flows.legacy_routine_id, flows.shadow
// and flow_runs.legacy_occurrence_id).

export type RoutineConfigJson = { kind: string } & Record<string, unknown>

export const routines = sqliteTable(
  "routines",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    name: text("name").notNull(),
    tz: text("tz").notNull(),
    // Weekday codes (MO..SU) the routine runs on, null for every day.
    days: json<string[] | null>("days"),
    source: json<RoutineConfigJson>("source").notNull(),
    action: json<RoutineConfigJson>("action").notNull(),
    recap: json<RoutineConfigJson>("recap").notNull(),
    // Message and prompt actions skip an occurrence more than this late (downtime).
    lateGraceMin: integer("late_grace_min").notNull().default(60),
    enabled: bool("enabled").notNull().default(true),
    // Paused with an end date: resumes on its own at this time (UTC ms).
    pausedUntil: integer("paused_until"),
    // idle -> firing while one sweep handles the due occurrence.
    status: text("status").$type<"idle" | "firing">().notNull().default("idle"),
    nextFireAt: integer("next_fire_at"),
    nextOcc: json<{ at: number; key: string; label: string; fallback?: boolean } | null>("next_occ"),
    // The last occurrence handled; the next one is computed after it.
    cursor: json<SourceCursor | null>("cursor"),
    sourceState: json<SourceState>("source_state").notNull().default({}),
    nextRefreshAt: integer("next_refresh_at"),
    nextRecapAt: integer("next_recap_at"),
    lastError: text("last_error"),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [index("routines_chat").on(t.chatId), index("routines_next").on(t.enabled, t.status, t.nextFireAt)],
)

// open: a check-in waiting for an answer. done: answered with a closing answer.
// missed: deadline passed without one. sent: a message or prompt went out.
// skipped: too late to send (downtime). failed: the prompt turn failed.
export type RoutineOccurrenceStatus = "open" | "done" | "missed" | "sent" | "skipped" | "failed"

export const routineOccurrences = sqliteTable(
  "routine_occurrences",
  {
    id: text("id").primaryKey(),
    routineId: text("routine_id").notNull(),
    chatId: text("chat_id").notNull(),
    // "<local date>:<item key>" (or the local datetime for RRULE): one fire per key.
    key: text("key").notNull(),
    label: text("label").notNull(),
    scheduledAt: integer("scheduled_at").notNull(),
    firedAt: integer("fired_at").notNull(),
    status: text("status").$type<RoutineOccurrenceStatus>().notNull(),
    // Last answer id given (check-ins), with every answer in `answers`.
    answer: text("answer"),
    answeredAt: integer("answered_at"),
    answers: json<{ answer: string; text?: string; at: number }[]>("answers").notNull().default([]),
    followUps: integer("follow_ups").notNull().default(0),
    nextFollowUpAt: integer("next_follow_up_at"),
    deadlineAt: integer("deadline_at"),
    // Times came from a stale cache because the source failed.
    fallback: bool("fallback").notNull().default(false),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [
    uniqueIndex("routine_occ_key").on(t.routineId, t.key),
    index("routine_occ_chat_status").on(t.chatId, t.status),
    index("routine_occ_follow").on(t.status, t.nextFollowUpAt),
    index("routine_occ_deadline").on(t.status, t.deadlineAt),
  ],
)

// --- Flows (RFC flows, PLAN 6c) ---------------------------------------------------------
// A flow is a definition (triggers + steps) in immutable versions. Triggers are
// derived per version so the scheduler can index due times; runs and their step
// log record every firing, skips included. JSON columns are validated by zod in
// core/flows, so a new step or trigger kind needs no migration.

// What a time source remembers between refreshes (tool sources cache fetched times).
export type SourceState = {
  // Fetched occurrences, UTC ms, with the local date they were fetched for.
  entries?: { date: string; key: string; label: string; at: number }[]
  // Local dates a per-day fetch succeeded for.
  fetched?: string[]
  failures?: number
  lastError?: string | null
  lastOkAt?: number | null
  // The local date the owner was last told about a failing source.
  noticeFor?: string | null
}

export type SourceCursor = { at: number; key: string }

export type FlowState = { values: Record<string, unknown>; tainted: string[] }

export const flows = sqliteTable(
  "flows",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    name: text("name").notNull(),
    tz: text("tz").notNull(),
    enabled: bool("enabled").notNull().default(true),
    pausedUntil: integer("paused_until"),
    currentVersionId: text("current_version_id"),
    // The version whose write calls the owner approved, or null.
    approvedVersionId: text("approved_version_id"),
    state: json<FlowState>("state").notNull().default({ values: {}, tainted: [] }),
    consecutiveFailures: integer("consecutive_failures").notNull().default(0),
    // The agent whose persona and model prompt steps use; null is the default agent.
    agentId: text("agent_id"),
    // Retired with routines (unused, dropped in a later migration).
    legacyRoutineId: text("legacy_routine_id").unique(),
    shadow: bool("shadow").notNull().default(false),
    lastError: text("last_error"),
    // The local date the owner was last told about a failing run.
    noticeFor: text("notice_for"),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [uniqueIndex("flows_chat_name").on(t.chatId, t.name)],
)

export type FlowWriteCallJson = { stepId: string; kind: string } & Record<string, unknown>

export const flowVersions = sqliteTable(
  "flow_versions",
  {
    id: text("id").primaryKey(),
    flowId: text("flow_id").notNull(),
    n: integer("n").notNull(),
    definition: json<Record<string, unknown>>("definition").notNull(),
    hash: text("hash").notNull(),
    writeCalls: json<FlowWriteCallJson[]>("write_calls").notNull().default([]),
    // "agent:<runId>", "owner:dashboard", "migration"
    createdBy: text("created_by").notNull(),
    note: text("note"),
    approvedAt: integer("approved_at"),
    approvedVia: text("approved_via"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [uniqueIndex("flow_versions_n").on(t.flowId, t.n)],
)

export type FlowPollState = { lastHash?: string | null; lastValuePreview?: string | null; lastCheckedAt?: number | null; failures?: number; lastError?: string | null; seeded?: boolean }

export const flowTriggers = sqliteTable(
  "flow_triggers",
  {
    id: text("id").primaryKey(),
    flowId: text("flow_id").notNull(),
    triggerId: text("trigger_id").notNull(),
    kind: text("kind").notNull(),
    // Lookup key: the hook id (webhook), the normalized command (chat), the event name (event).
    match: text("match"),
    status: text("status").$type<"idle" | "firing">().notNull().default("idle"),
    nextFireAt: integer("next_fire_at"),
    nextOcc: json<{ at: number; key: string; label: string; fallback?: boolean } | null>("next_occ"),
    cursor: json<SourceCursor | null>("cursor"),
    sourceState: json<SourceState>("source_state").notNull().default({}),
    nextRefreshAt: integer("next_refresh_at"),
    pollState: json<FlowPollState>("poll_state").notNull().default({}),
    lastError: text("last_error"),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [uniqueIndex("flow_triggers_id").on(t.flowId, t.triggerId), index("flow_triggers_match").on(t.kind, t.match), index("flow_triggers_due").on(t.status, t.nextFireAt)],
)

export type FlowRunStatus = "queued" | "running" | "waiting" | "awaiting_approval" | "done" | "failed" | "skipped" | "cancelled"
export type FlowSkipReason = "late" | "duplicate" | "paused" | "rate_limited" | "budget" | "not_approved" | "chain_depth"

export const flowRuns = sqliteTable(
  "flow_runs",
  {
    id: text("id").primaryKey(),
    flowId: text("flow_id").notNull(),
    versionId: text("version_id").notNull(),
    chatId: text("chat_id").notNull(),
    triggerId: text("trigger_id"),
    triggerKind: text("trigger_kind").notNull(),
    runKey: text("run_key").notNull(),
    parentRunId: text("parent_run_id"),
    itemIndex: integer("item_index"),
    status: text("status").$type<FlowRunStatus>().notNull(),
    skipReason: text("skip_reason").$type<FlowSkipReason>(),
    trigger: json<Record<string, unknown>>("trigger").notNull().default({}),
    vars: json<Record<string, unknown>>("vars").notNull().default({}),
    taint: json<string[]>("taint").notNull().default([]),
    // The step to run next; null when finished.
    cursor: text("cursor"),
    wait: json<Record<string, unknown> | null>("wait"),
    wakeAt: integer("wake_at"),
    leaseUntil: integer("lease_until"),
    stepsDone: integer("steps_done").notNull().default(0),
    messagesSent: integer("messages_sent").notNull().default(0),
    scheduledAt: integer("scheduled_at"),
    startedAt: integer("started_at").notNull(),
    endedAt: integer("ended_at"),
    error: text("error"),
    // Retired with routines (unused, dropped in a later migration).
    legacyOccurrenceId: text("legacy_occurrence_id").unique(),
    dryRun: bool("dry_run").notNull().default(false),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [
    uniqueIndex("flow_runs_key").on(t.flowId, t.runKey),
    index("flow_runs_wake").on(t.status, t.wakeAt),
    index("flow_runs_chat").on(t.chatId, t.status),
    index("flow_runs_started").on(t.flowId, t.startedAt),
    index("flow_runs_parent").on(t.parentRunId),
  ],
)

export type FlowStepStatus = "running" | "ok" | "error" | "skipped" | "waiting" | "stubbed"

export const flowStepRuns = sqliteTable(
  "flow_step_runs",
  {
    id: text("id").primaryKey(),
    runId: text("run_id").notNull(),
    seq: integer("seq").notNull(),
    stepId: text("step_id").notNull(),
    kind: text("kind").notNull(),
    attempt: integer("attempt").notNull().default(1),
    status: text("status").$type<FlowStepStatus>().notNull(),
    input: json<unknown>("input"),
    output: json<unknown>("output"),
    tainted: bool("tainted").notNull().default(false),
    branch: text("branch"),
    toolCallId: text("tool_call_id"),
    turnRunId: text("turn_run_id"),
    error: text("error"),
    startedAt: integer("started_at").notNull(),
    ms: integer("ms"),
  },
  (t) => [uniqueIndex("flow_step_runs_seq").on(t.runId, t.seq), index("flow_step_runs_started").on(t.startedAt)],
)

// --- Outbox -----------------------------------------------------------------------

export type OutboxStatus = "pending" | "sending" | "sent" | "failed" | "cancelled"

export type OutboxPayload = {
  text: string
  // Inline actions (Telegram buttons; WhatsApp gets a text hint instead).
  actions?: { id: string; label: string }[]
  replyToChannelMsgId?: string
  // "html" for Telegram formatted text, "plain" otherwise.
  format?: "plain" | "markdown"
}

export const outbox = sqliteTable(
  "outbox",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    // Channel connector id at enqueue time; may change on fallback.
    channel: text("channel").$type<Channel>().notNull(),
    // The exact connector to send through when the channel has several (an
    // agent's Telegram bot); null means the channel's main connector.
    connectorId: text("connector_id"),
    payload: json<OutboxPayload>("payload").notNull(),
    status: text("status").$type<OutboxStatus>().notNull().default("pending"),
    attempts: integer("attempts").notNull().default(0),
    nextAttemptAt: integer("next_attempt_at").notNull(),
    claimedAt: integer("claimed_at"),
    channelMsgId: text("channel_msg_id"),
    // "reply:<runId>", "reminder:<id>", "job:<id>", "system", "approval:<id>"
    source: text("source").notNull(),
    error: text("error"),
    fallbackFrom: text("fallback_from"),
    createdAt: integer("created_at").notNull(),
    // Give up after this (24 h for reminders).
    expiresAt: integer("expires_at"),
    sentAt: integer("sent_at"),
  },
  (t) => [index("outbox_status_next").on(t.status, t.nextAttemptAt), index("outbox_chat").on(t.chatId)],
)

// --- Outbound messages (docs/design/outbound-messages.md) ---------------------------

// ask: every message needs the owner's approval. allow: messages the owner asked
// for himself skip it (set from the dashboard only).
export type ContactPolicy = "ask" | "allow"

// People an agent may message on the owner's behalf. Each agent has its own list
// (PLAN 19), like instructions. Soft-deleted so the log keeps names.
export const contacts = sqliteTable(
  "contacts",
  {
    id: text("id").primaryKey(),
    // The agent this contact belongs to; null means the default agent.
    agentId: text("agent_id"),
    name: text("name").notNull(),
    channel: text("channel").$type<"whatsapp" | "telegram">().notNull(),
    // WhatsApp digits ("6282200001001") or the Telegram chat's external id.
    address: text("address").notNull(),
    // The chat the outbox sends to. A new number gets a chat row with allowed=false.
    chatId: text("chat_id").notNull(),
    policy: text("policy").$type<ContactPolicy>().notNull().default("ask"),
    note: text("note"),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
    removedAt: integer("removed_at"),
  },
  (t) => [index("contacts_agent").on(t.agentId, t.removedAt), index("contacts_chat").on(t.chatId)],
)

export type OutboundStatus = "scheduled" | "firing" | "done" | "cancelled"

// One row per send request: now (held for the undo window), at a time, or a series.
// Every actual send is an outbox row with source "outbound:<id>".
export const outboundMessages = sqliteTable(
  "outbound_messages",
  {
    id: text("id").primaryKey(),
    // The contact's agent, copied so a list per agent needs no join.
    agentId: text("agent_id"),
    contactId: text("contact_id").notNull(),
    // The exact text that goes out, signature included.
    text: text("text").notNull(),
    origin: text("origin").$type<"chat" | "flow">().notNull(),
    // The run that asked (chat) or the flow run id (flow).
    originRef: text("origin_ref"),
    approvedBy: text("approved_by").$type<"owner" | "policy" | "flow_version">().notNull(),
    // The chat the request came from, for notices about it.
    ownerChatId: text("owner_chat_id"),
    // RRULE body without DTSTART for a series, null for a one-off.
    rrule: text("rrule"),
    tz: text("tz").notNull(),
    // Wall-clock anchor in tz (ISO local, no offset).
    localStart: text("local_start").notNull(),
    // Next send time, UTC ms.
    fireAt: integer("fire_at").notNull(),
    status: text("status").$type<OutboundStatus>().notNull().default("scheduled"),
    lastOutboxId: text("last_outbox_id"),
    createdAt: integer("created_at").notNull(),
    updatedAt: integer("updated_at").notNull(),
  },
  (t) => [index("outbound_status_fire").on(t.status, t.fireAt), index("outbound_contact").on(t.contactId)],
)

// --- Events and settings -----------------------------------------------------------

export type EventLevel = "debug" | "info" | "warn" | "error"

export const events = sqliteTable(
  "events",
  {
    id: integer("id").primaryKey({ autoIncrement: true }),
    at: integer("at").notNull(),
    level: text("level").$type<EventLevel>().notNull(),
    connectorId: text("connector_id"),
    kind: text("kind").notNull(),
    message: text("message").notNull(),
    data: json<Record<string, unknown>>("data"),
  },
  (t) => [index("events_at").on(t.at), index("events_connector").on(t.connectorId, t.at)],
)

// Debug snapshots the owner asked for with /report. `text` is exactly what was
// sent to the chat, so the id alone is enough to hand to a coding agent.
export const reports = sqliteTable(
  "reports",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id").notNull(),
    sessionId: text("session_id"),
    // What the owner typed after /report.
    note: text("note").notNull().default(""),
    text: text("text").notNull(),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [index("reports_chat").on(t.chatId, t.createdAt)],
)

export const settings = sqliteTable("settings", {
  key: text("key").primaryKey(),
  value: json<unknown>("value").notNull(),
  updatedAt: integer("updated_at").notNull(),
})

// --- Inbound hooks (PLAN 5.5) -------------------------------------------------------

export const hooks = sqliteTable("hooks", {
  id: text("id").primaryKey(),
  slug: text("slug").notNull().unique(),
  name: text("name").notNull(),
  // Encrypted HMAC secret.
  secret: text("secret").notNull(),
  // flow: the body starts a flow run (the flow's webhook trigger names this hook).
  mode: text("mode").$type<"forward" | "summarize" | "flow">().notNull().default("forward"),
  chatId: text("chat_id").notNull(),
  enabled: bool("enabled").notNull().default(true),
  lastAt: integer("last_at"),
  createdAt: integer("created_at").notNull(),
})

// Usage counters for budgets that must survive restarts (tokens per day, runs per hour).
export const usage = sqliteTable("usage", {
  // "tokens:2026-11-10", "runs:2026-11-10T07", "job:<id>:2026-11-10"
  key: text("key").primaryKey(),
  value: integer("value").notNull().default(0),
  updatedAt: integer("updated_at").notNull(),
})

// --- Owner notes ---------------------------------------------------------------------

// Standing instructions the owner saved (from chat after approval, or the
// dashboard). Every turn's system prompt carries the enabled ones.
export const instructions = sqliteTable("instructions", {
  id: text("id").primaryKey(),
  // The agent these belong to; null means the default agent (PLAN 19.1).
  agentId: text("agent_id"),
  title: text("title").notNull(),
  body: text("body").notNull(),
  enabled: bool("enabled").notNull().default(true),
  createdAt: integer("created_at").notNull(),
  updatedAt: integer("updated_at").notNull(),
})

// The owner's defaults as plain key-value pairs ("default_city" -> "Banjarmasin").
export const preferences = sqliteTable("preferences", {
  key: text("key").primaryKey(),
  value: text("value").notNull(),
  updatedAt: integer("updated_at").notNull(),
})

// --- Agents (PLAN 19) ----------------------------------------------------------------

export type OwnerPowers = "off" | "approve"
export type GuestTools = "none" | "safe"
// Per-agent tool permissions, set by the owner on the agent page. `allow` runs
// without asking in turns the owner started, `ask` always asks, `deny` hides the
// tool from the agent. A tool with no entry follows `allowAll`, else its class rule.
export type ToolPermission = "allow" | "ask" | "deny"
export type ToolPermissions = { allowAll: boolean; tools: Record<string, ToolPermission> }

// A named persona with its own instructions, memory container and daily cap.
// Exactly one is the default (Palui, the owner's personal agent).
export const agents = sqliteTable("agents", {
  id: text("id").primaryKey(),
  slug: text("slug").notNull().unique(),
  name: text("name").notNull(),
  persona: text("persona").notNull(),
  isDefault: bool("is_default").notNull().default(false),
  // null: the recall connector's own container (the default agent).
  recallContainer: text("recall_container"),
  // Extra containers it reads but never writes (shared notes the owner curates).
  recallReadContainers: json<string[]>("recall_read_containers"),
  // One container per chat, so people in one chat never see what was said in another.
  recallPerChat: bool("recall_per_chat").notNull().default(false),
  ownerPowers: text("owner_powers").$type<OwnerPowers>().notNull().default("off"),
  guestTools: text("guest_tools").$type<GuestTools>().notNull().default("safe"),
  // Tokens per day across all its chats; 0 means no cap of its own.
  dailyTokenCap: integer("daily_token_cap").notNull().default(0),
  // null: no overrides, every tool follows its class rule.
  toolPermissions: json<ToolPermissions>("tool_permissions"),
  telegramConnectorId: text("telegram_connector_id").unique(),
  // Avatar (PLAN 19.7): an uploaded photo wins, else an icon on a color, else the default.
  // The photo is a small data URL (resized in the browser, capped in core/agents.ts).
  avatarImage: text("avatar_image"),
  avatarIcon: text("avatar_icon"),
  avatarColor: text("avatar_color"),
  createdAt: integer("created_at").notNull(),
  updatedAt: integer("updated_at").notNull(),
})
