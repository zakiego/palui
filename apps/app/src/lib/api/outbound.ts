// Outbound page: the kill switch and limits, contacts, scheduled messages and
// the send log.
import { noInput, withInput } from "./client"
import type { Channel, ContactPolicy, Ok, OutboxStatus } from "./types"

// How many sends the log shows.
export const LOG_LIMIT = 100

export type OutboundSettings = {
  enabled: boolean
  perContactPerDay: number
  perDay: number
  holdSeconds: number
  signatureOn: boolean
  signature: string
  pastSignatures: string[]
}

export type OutboundContact = {
  id: string
  agentId: string
  name: string
  channel: "telegram" | "whatsapp"
  address: string
  policy: ContactPolicy
  note: string | null
  sentToday: number
}

export type OutboundScheduled = { id: string; agentId: string; contact: string; text: string; fireAt: number; tz: string; rrule: string | null; origin: "chat" | "flow" }

export type OutboundLogItem = {
  id: string
  agentId: string | null
  contact: string
  text: string
  status: OutboxStatus
  error: string | null
  origin: "chat" | "flow" | null
  approvedBy: "flow_version" | "owner" | "policy" | null
  createdAt: number
  sendAt: number
  sentAt: number | null
  outboundId: string | null
}

export type OutboundPage = {
  agents: { id: string; name: string; isDefault: boolean }[]
  defaultAgentId: string
  settings: OutboundSettings
  usage: number
  contacts: OutboundContact[]
  scheduled: OutboundScheduled[]
  log: OutboundLogItem[]
  telegramChats: { id: string; title: string; channel: Channel; externalId: string }[]
  timezone: string
}

export type OutboundLimits = { perContactPerDay: number; perDay: number; holdSeconds: number; signatureOn: boolean; signature: string }

export type AddContactInput = { agentId: string; name: string; phone?: string; chatId?: string; policy?: ContactPolicy; note?: string }

export const getOutbound = noInput<OutboundPage>("getOutbound", "GET")
export const setOutboundSwitch = withInput<{ enabled: boolean }, { cancelled: number }>("setOutboundSwitch", "POST")
export const saveOutboundLimits = withInput<OutboundLimits, Ok>("saveOutboundLimits", "POST")
export const addContactFn = withInput<AddContactInput, { id: string }>("addContactFn", "POST")
export const setContactPolicy = withInput<{ id: string; policy: ContactPolicy }, Ok>("setContactPolicy", "POST")
export const removeContactFn = withInput<{ id: string }, { cancelled: number }>("removeContactFn", "POST")
export const cancelOutboundFn = withInput<{ id: string }, Ok>("cancelOutboundFn", "POST")
