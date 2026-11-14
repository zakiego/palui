// Types the RPC wrappers share. Written out by hand (no import from the
// backend packages or the old server functions) so the dashboard compiles on
// its own once the TypeScript backend is gone. api-types.test.ts checks them
// against the server functions while those exist.
//
// Everything here is the JSON the Go server sends: times are Unix ms numbers,
// and a key whose value would be undefined is left out (so it's optional).
import type { ConnectorKind, Trust } from "@/shared/connectors/define"

export type { ConnectorKind, Trust }
export type { Json, JsonObject } from "@/lib/json"

// --- Rows ---------------------------------------------------------------------------

export type Channel = "telegram" | "whatsapp" | "web"
export type ConnectorStatus = "not_set_up" | "connecting" | "on" | "paused" | "error"
export type EventLevel = "debug" | "info" | "warn" | "error"
export type RunTrigger = "message" | "schedule" | "hook" | "approval" | "reminder" | "flow"
export type RunStatus = "running" | "ok" | "error" | "aborted" | "awaiting_approval" | "budget"
export type MessageRole = "user" | "assistant" | "tool" | "system"
export type MessageStatus = "pending" | "handled" | "ignored"
export type ToolClass = "read" | "write" | "destructive"
export type ReplyMode = "mention" | "all"
export type ChatKind = "dm" | "group"
export type OwnerPowers = "off" | "approve"
export type GuestTools = "none" | "safe"
export type ToolPermission = "allow" | "ask" | "deny"
export type ToolPermissions = { allowAll: boolean; tools: { [tool: string]: ToolPermission } }
export type JobKind = "job" | "heartbeat" | "briefing"
export type ModelRole = "default" | "fast" | "vision"
export type ReminderStatus = "pending" | "firing" | "done" | "cancelled"
export type ContactPolicy = "ask" | "allow"
export type OutboxStatus = "pending" | "sending" | "sent" | "failed" | "cancelled"
export type AttachmentKind = "image" | "document"
export type PreviewKind = "image" | "pdf" | "text"
export type ShareVisibility = "public" | "private"
export type ShareDuration = "1h" | "24h" | "7d"
export type HookMode = "forward" | "summarize" | "flow"
export type FlowRunStatus = "queued" | "running" | "waiting" | "awaiting_approval" | "done" | "failed" | "skipped" | "cancelled"
export type FlowSkipReason = "late" | "duplicate" | "paused" | "rate_limited" | "budget" | "not_approved" | "chain_depth"
export type FlowStepStatus = "running" | "ok" | "error" | "skipped" | "waiting" | "stubbed"
export type FlowTriggerKind = "chat" | "event" | "manual" | "poll" | "schedule" | "webhook"
export type FlowStepKind = "checkin" | "each" | "if" | "message" | "prompt" | "set" | "tool" | "wait"
export type FlowApproval = "approved" | "not_needed" | "waiting"
export type FixtureName = "demo" | "worst" | "empty" | "one" | "huge"

// --- Shared shapes -------------------------------------------------------------------

export type Ok = { ok: true }

export type Viewer = { sessionId: string; user: { id: string; username: string; name: string } }

export type StatusSummary = { total: number; on: number; attention: { id: string; label: string; status: string }[] }

// A chat a reminder, job or hook can target.
export type ChatOption = { id: string; channel: Channel; title: string }

export type RunListItem = {
  id: string
  trigger: RunTrigger
  model: string | null
  status: RunStatus
  error: string | null
  tokensIn: number
  tokensOut: number
  tainted: boolean
  startedAt: number
  ms: number | null
  chat: string | null
  job: string | null
  chatGone: boolean
  jobGone: boolean
}

export type ToolCallView = {
  id: string
  tool: string
  connectorId: string | null
  class: string
  status: string
  ms: number | null
  args: string
  result: string | null
}

export type DayTokens = { day: string; tokensIn: number; tokensOut: number }

export type Instruction = { agentId: string | null; body: string; createdAt: number; enabled: boolean; id: string; title: string; updatedAt: number }

export type AgentView = {
  id: string
  slug: string
  name: string
  persona: string
  isDefault: boolean
  ownerPowers: OwnerPowers
  guestTools: GuestTools
  dailyTokenCap: number
  toolPermissions: ToolPermissions
  recallContainer: string | null
  recallReadContainers: string[]
  recallPerChat: boolean
  container: string | null
  telegramConnectorId: string | null
  avatarImage: string | null
  avatarIcon: string | null
  avatarColor: string | null
  chats: number
  instructions: number
  tokensToday: number
}

export type TelegramBot = { id: string; label: string; username: string | null; status: ConnectorStatus }

// An agent as a picker shows it (with its avatar).
export type AgentChoice = { id: string; name: string; isDefault: boolean; avatarImage: string | null; avatarIcon: string | null; avatarColor: string | null }

export type PasskeyItem = { id: string; name: string | null; deviceType: string; backedUp: boolean; createdAt: number | null }

export type HookView = { chatId: string; createdAt: number; enabled: boolean; id: string; lastAt: number | null; mode: HookMode; name: string; slug: string }

export type AttachmentShare = {
  attachmentId: string
  createdAt: number
  createdBy: string
  expiresAt: number
  id: string
  lastOpenedAt: number | null
  openCount: number
  revokedAt: number | null
  visibility: ShareVisibility
}
