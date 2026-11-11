// The connector contract (PLAN 4.1). A connector is one folder in
// packages/connectors/src/<type>/ with:
//   manifest.ts  zod schemas + UI metadata. Imported by the app AND the runtime,
//                so it must only import zod and this file.
//   runtime.ts   test/start. Imported by the runtime only (Baileys, grammY...).
import type { LanguageModel, Tool } from "ai"
import type { z } from "zod"
import type { Channel, ConnectorKind, DB, OutboxPayload, ToolClass, Trust } from "@palui/db"
import type { Logger } from "../log"
import type { ModelRole } from "../settings"

// --- Manifest ------------------------------------------------------------------------

export type SetupStep =
  | {
      field: string
      label: string
      help?: string
      // A command or value the help refers to, shown as a copyable block between
      // the help and the input (guide first, then the box to paste into).
      // `{appUrl}` is replaced with the dashboard's URL.
      command?: string
      secret?: boolean
      placeholder?: string
      optional?: boolean
      // Before the first connect the page shows only required fields. This
      // shows an optional one there too (MCP's auth mode and token).
      askFirst?: boolean
      // Show the field only while another field has one of these values.
      showIf?: { field: string; in: string[] }
    }
  // Wizard actions the dashboard renders specially.
  | { action: "claimOwner" | "pairWhatsApp" | "ownerPhone" | "pickContainer" | "listTools" | "oauth"; label: string; help?: string }

export type ConnectorManifest<C extends z.ZodType = z.ZodType, S extends Record<string, z.ZodType> = Record<string, z.ZodType>> = {
  // Manifest/type id ("telegram", "mcp", "cliproxy"...).
  id: string
  kind: ConnectorKind
  name: string
  // lucide icon name, rendered by the dashboard.
  icon: string
  summary: string
  // Longer explanation for the sheet and the generated docs page.
  description?: string
  config: C
  secrets: S
  setup: SetupStep[]
  // Several instances allowed (MCP servers). Instance ids become "<id>-<slug>".
  multiple?: boolean
  // Default trust for tool results from this connector.
  trust?: Trust
  // Created (paused or on) on first boot without asking: skills like Reminders.
  builtin?: boolean
  // Text shown on the card about what pause does (PLAN 4.3, e.g. Reminders).
  pauseNote?: string
  // Text for the remove dialog ("This unlinks the WhatsApp device...").
  removeWarning?: string
  // Remove calls the running handle's action("unlink") first (WhatsApp logs the
  // linked device out). Connectors without it are just stopped.
  unlinkOnRemove?: boolean
  // When a running handle reports `error` (after start() resolved), the
  // supervisor stops it and restarts it on the backoff ladder (PLAN 4.3).
  // Default: true for channels, false otherwise (tool and memory connectors
  // report soft errors and recover on their own). A connector that reconnects
  // by itself and whose error states are final sets false.
  restartOnError?: boolean
  // Presets for multiple-instance connectors (MCP: vorneo, keuangan...).
  presets?: { id: string; label: string; config: Record<string, unknown>; secretFields?: string[]; trust?: Trust }[]
  docs?: { commands?: string[]; notes?: string[] }
}

export function defineManifest<C extends z.ZodType, S extends Record<string, z.ZodType>>(m: ConnectorManifest<C, S>): ConnectorManifest<C, S> {
  return m
}

export type ConfigOf<M> = M extends ConnectorManifest<infer C, infer _S> ? z.infer<C> : never
export type SecretsOf<M> = M extends ConnectorManifest<infer _C, infer S> ? { [K in keyof S]?: z.infer<S[K]> } : never

// --- Runtime -------------------------------------------------------------------------

// What a running turn tells a connector when it asks for tools.
export type TurnInfo = {
  runId: string
  chatId: string
  channel: Channel | null
  trigger: "message" | "schedule" | "hook" | "approval" | "reminder"
  tz: string
  now: number
  jobId?: string
}

// A tool plus its connector metadata. `class` drives approvals (PLAN 10); `untrusted`
// marks results that taint the turn (web fetch, hooks, external MCP).
export type PaluiTool = {
  tool: Tool
  class: ToolClass
  untrusted?: boolean
  // Shown in the transcript and the Activity page.
  title?: string
}

export type InboundMedia = {
  kind: "image" | "audio" | "document"
  mimeType: string
  // base64
  data: string
  filename?: string
  durationSec?: number
}

export type InboundMessage = {
  channel: Channel
  connectorId: string
  chatExternalId: string
  chatKind: "dm" | "group"
  chatTitle?: string
  senderExternalId: string
  // WhatsApp: the other form of the sender (LID when externalId is a phone JID, or vice versa).
  senderAltId?: string
  senderName?: string
  channelMsgId?: string
  replyToChannelMsgId?: string
  text: string
  media?: InboundMedia[]
  // A button press (Telegram callback_query data).
  callback?: { data: string; callbackId?: string }
  fromMe?: boolean
  at: number
}

export type SendResult = { channelMsgId?: string }

export type ChannelHandle = {
  channel: Channel
  send(chatExternalId: string, payload: OutboxPayload): Promise<SendResult>
  typing?(chatExternalId: string, on: boolean): Promise<void>
  // Whether the socket is up right now (used by the 4.3 "another channel reachable" rule).
  reachable(): boolean
  answerCallback?(callbackId: string, text?: string): Promise<void>
}

export type MemoryHandle = {
  // Auto-recall: profile text for the prompt, or null when skipped/timed out.
  recall(query: string, opts: { timeoutMs: number }): Promise<string | null>
  // Auto-save a transcript slice; sourceId keeps it idempotent.
  save(input: { sourceId: string; content: string; title?: string }): Promise<void>
  autoRecall: boolean
  autoSave: boolean
}

export type RunningConnector = {
  stop(): Promise<void>
  channel?: ChannelHandle
  // Model connectors: an AI SDK model for a model id, built on demand.
  model?(modelId: string, role: ModelRole): LanguageModel
  // Tool connectors: tools for this turn, keyed by tool name WITHOUT the namespace.
  tools?(turn: TurnInfo): Promise<Record<string, PaluiTool>>
  memory?: MemoryHandle
  // Voice-in skills: speech to text for inbound audio (PLAN 5.5).
  transcribe?(audio: InboundMedia): Promise<string>
  // Free-form live state for the dashboard (pairing code, QR, bot username...).
  live?(): Record<string, unknown>
  // Wizard actions routed from the control API (pair, claim link...).
  action?(name: string, input: Record<string, unknown>): Promise<Record<string, unknown>>
}

// Services the runtime gives every connector.
export type ConnectorServices = {
  // Channels hand every inbound message here. It's written to the inbox before
  // this resolves, so the channel can confirm the update afterwards.
  inbound(msg: InboundMessage): Promise<void>
  // Telegram owner claim: true when the nonce was valid and the sender is now owner.
  claimOwner(input: { channel: Channel; nonce: string; externalId: string; displayName?: string }): Promise<boolean>
  // Whether this sender is the owner (also learns the WhatsApp LID).
  isOwner(input: { channel: Channel; externalId: string; altId?: string }): boolean
}

export type ConnectorContext<C = Record<string, unknown>, S = Record<string, string | undefined>> = {
  id: string
  type: string
  config: C
  secrets: S
  trust: Trust
  db: DB
  log: Logger
  dataDir: string
  signal: AbortSignal
  services: ConnectorServices
  // Report a status change. The supervisor records it in `connectors` and `events`.
  setStatus(status: "connecting" | "on" | "error" | "paused", detail?: string): void
  event(kind: string, message: string, level?: "debug" | "info" | "warn" | "error", data?: Record<string, unknown>): void
  // Persist non-secret config learned at runtime (bot username, owner LID...).
  patchConfig(patch: Record<string, unknown>): void
  // Persist a secret learned at runtime (a token swapped for a one-time pairing
  // code), encrypted like the wizard's secrets. An empty string deletes the key.
  // No restart. Optional so test contexts can leave it out.
  patchSecrets?(patch: Record<string, string>): void
}

export type TestResult = { ok: boolean; detail: string; data?: Record<string, unknown> }

export type ConnectorRuntime<M extends ConnectorManifest = ConnectorManifest> = {
  manifest: M
  test(ctx: ConnectorContext<ConfigOf<M>, SecretsOf<M>>): Promise<TestResult>
  start(ctx: ConnectorContext<ConfigOf<M>, SecretsOf<M>>): Promise<RunningConnector>
}

export function defineRuntime<M extends ConnectorManifest>(
  manifest: M,
  impl: Omit<ConnectorRuntime<M>, "manifest">,
): ConnectorRuntime<M> {
  return { manifest, ...impl }
}

// Erased form the supervisor works with.
// biome-ignore lint: any is the point of an erased registry type.
export type AnyRuntime = ConnectorRuntime<any>
