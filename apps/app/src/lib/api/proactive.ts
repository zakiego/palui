// An agent's proactive settings (docs/rfc/proactive.md 12): the Proactive tab
// on the agent page.
import { withInput } from "./client"
import type { ConnectorStatus } from "./types"

export type ProactiveSummary = { on: boolean; at: string }

export type CalendarSource = {
  connectorId: string
  departLeadMin: number
  onlineLeadMin: number
  notifyChanges: boolean
}

export type WhatsAppSource = {
  connectorId: string
  account: string
  waitHours: number
  escalateHours: number
  maxAgeDays: number
  skipGroups: boolean
}

export type ProactiveContact = { subject: string; label: string }

export type ProactiveConfig = {
  schema: number
  checkEveryMin: number
  maxPerHour: number
  quietHours: { start: string; end: string; urgentPasses: boolean }
  morning: ProactiveSummary
  evening: ProactiveSummary
  calendar: CalendarSource | null
  whatsapp: WhatsAppSource | null
  contactsAuto: boolean
  contacts: ProactiveContact[]
  modelRole: "fast" | "default"
}

export type NudgeKind = "cal_depart" | "cal_start" | "cal_changed" | "wa_waiting" | "sum_morning" | "sum_evening"
export type NudgeStatus = "pending" | "scheduled" | "firing" | "sent" | "done" | "snoozed" | "dropped" | "expired"

export type ProactiveNudge = {
  id: string
  kind: NudgeKind
  subject: string
  status: NudgeStatus
  label: string
  reason: string | null
  sentAt: number | null
  escalatedAt: number | null
  wakeAt: number | null
  updatedAt: number
}

export type ProactiveConnector = {
  id: string
  label: string
  status: ConnectorStatus
}

export type ProactiveView = {
  agentId: string
  enabled: boolean
  chatId: string | null
  pausedReason: string | null
  lastTickAt: number | null
  nextTickAt: number | null
  config: ProactiveConfig
  defaults: { calendar: CalendarSource; whatsapp: WhatsAppSource }
  chats: { id: string; title: string; channel: string }[]
  connectors: {
    calendar: ProactiveConnector[]
    whatsapp: ProactiveConnector[]
  }
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
  lastErrors: Partial<Record<"calendar" | "whatsapp", string>>
}

// A JSON merge patch: nested objects merge, null removes.
export type ProactivePatch = {
  [K in keyof ProactiveConfig]?: ProactiveConfig[K] extends object | null ? Partial<NonNullable<ProactiveConfig[K]>> | null | ProactiveConfig[K] : ProactiveConfig[K]
}

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
