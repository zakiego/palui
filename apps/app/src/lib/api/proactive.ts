// An agent's proactive settings (docs/rfc/proactive-prompt.md 12): the
// Proactive tab on the agent page.
import { withInput } from "./client"

export type ProactiveSummary = { on: boolean; at: string }

export type ProactiveConfig = {
  schema: number
  // The owner's instructions, with @<connectorId>__<tool> mentions.
  prompt: string
  checkEveryMin: number
  quietHours: { start: string; end: string; urgentPasses: boolean }
  maxPerHour: number
  morning: ProactiveSummary
  evening: ProactiveSummary
  modelRole: "fast" | "default"
}

// "item" is what a run said; rows from before prompts keep their old kinds.
export type NudgeKind = "item" | "sum_morning" | "sum_evening" | (string & {})
export type NudgeStatus = "pending" | "scheduled" | "firing" | "sent" | "done" | "snoozed" | "dropped" | "expired" | "tracking"

export type ProactiveNudge = {
  id: string
  kind: NudgeKind
  subject: string
  status: NudgeStatus
  label: string
  reason: string | null
  sentAt: number | null
  wakeAt: number | null
  updatedAt: number
}

// A tool the prompt may mention.
export type ProactiveTool = {
  name: string
  connectorId: string
  connectorLabel: string
  tool: string
  class: "read" | "write" | "destructive"
  suggestedClass?: string
  title: string | null
  untrusted: boolean
}

// A mention in the saved prompt; reason is "" when it resolves.
export type ProactiveMention = { mention: string; reason: string }

export type ProactiveNote = {
  id: string
  agentId: string
  text: string
  // reply: the owner's reply to a proactive message, saved on its own.
  source: "owner" | "reply" | "model"
  expiresAt: number | null
  createdAt: number
  updatedAt: number
}

export type ProactiveView = {
  agentId: string
  enabled: boolean
  chatId: string | null
  pausedReason: string | null
  lastTickAt: number | null
  nextTickAt: number | null
  config: ProactiveConfig
  starter: string
  chats: { id: string; title: string; channel: string }[]
  tools: ProactiveTool[]
  mentions: ProactiveMention[]
  notes: ProactiveNote[]
  notesChars: number
  mutes: {
    agentId: string
    subject: string
    label: string
    createdAt: number
  }[]
  activity: ProactiveNudge[]
  ticksToday: number
  sentToday: number
  tokensToday: number
  tokenCap: number
  lastRun: { at: number; full: boolean; skipped: string; calls: number; error: string | null } | null
}

export type ProactiveTestResult = {
  calls: { tool: string; args: unknown; hash: string; error?: string }[]
  items: { key: string; subject: string; text: string; sendAt: number | null; urgent: boolean; dropped?: string }[]
  closed: string[]
  remember: string[]
  forget: string[]
  ignored: string[]
  tokens: number
  error?: string
}

// A JSON merge patch: nested objects merge.
export type ProactivePatch = {
  [K in keyof ProactiveConfig]?: ProactiveConfig[K] extends object ? Partial<ProactiveConfig[K]> : ProactiveConfig[K]
}

// Limits the server enforces (internal/proactive), shown as counters.
export const MAX_PROMPT_CHARS = 4000
export const MAX_MENTIONS = 16
export const MAX_NOTE_CHARS = 300
export const MAX_NOTES_CHARS = 4000

export const getProactiveFn = withInput<{ agentId: string }, ProactiveView>("getProactiveFn", "GET")
export const saveProactiveFn = withInput<
  {
    agentId: string
    enabled?: boolean
    chatId?: string
    config?: ProactivePatch
  },
  ProactiveView
>("saveProactiveFn", "POST")
export const unmuteProactiveFn = withInput<{ agentId: string; subject: string }, ProactiveView>("unmuteProactiveFn", "POST")
export const checkProactiveNowFn = withInput<{ agentId: string }, ProactiveView>("checkProactiveNowFn", "POST")
export const testProactiveFn = withInput<{ agentId: string; prompt?: string }, ProactiveTestResult>("testProactiveFn", "POST")
export const addProactiveNoteFn = withInput<{ agentId: string; text: string; days?: number }, ProactiveView>("addProactiveNoteFn", "POST")
export const editProactiveNoteFn = withInput<{ agentId: string; noteId: string; text?: string; keep?: boolean }, ProactiveView>("editProactiveNoteFn", "POST")
export const deleteProactiveNoteFn = withInput<{ agentId: string; noteId: string }, ProactiveView>("deleteProactiveNoteFn", "POST")
