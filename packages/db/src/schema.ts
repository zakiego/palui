// Palui's SQLite schema (PLAN.md section 9). Timestamps are unix milliseconds.
// JSON columns are stored as text and typed with $type<>().
import { index, integer, sqliteTable, text, uniqueIndex } from "drizzle-orm/sqlite-core"

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
    // WhatsApp allowlist (PLAN 5.1): Palui only answers allowed chats.
    allowed: bool("allowed").notNull().default(true),
    lastMessageAt: integer("last_message_at"),
    createdAt: integer("created_at").notNull(),
  },
  (t) => [uniqueIndex("chats_channel_external").on(t.channel, t.externalId)],
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

export type RunTrigger = "message" | "schedule" | "hook" | "approval" | "reminder"
export type RunStatus = "running" | "ok" | "error" | "aborted" | "awaiting_approval" | "budget"

export const runs = sqliteTable(
  "runs",
  {
    id: text("id").primaryKey(),
    chatId: text("chat_id"),
    jobId: text("job_id"),
    trigger: text("trigger").$type<RunTrigger>().notNull(),
    model: text("model"),
    modelConnectorId: text("model_connector_id"),
    tokensIn: integer("tokens_in").notNull().default(0),
    tokensOut: integer("tokens_out").notNull().default(0),
    tokensCached: integer("tokens_cached").notNull().default(0),
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
  mode: text("mode").$type<"forward" | "summarize">().notNull().default("forward"),
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
