// The shell (status, command menu data) and the Home page.
import { noInput } from "./client"
import type { ConnectorKind, ConnectorStatus, DayTokens, Ok, RunListItem, StatusSummary, Viewer } from "./types"

export type Shell = {
  viewer: Viewer
  summary: StatusSummary
  connectors: { id: string; label: string; status: ConnectorStatus; enabled: boolean; kind: ConnectorKind }[]
  uiLanguage: "en" | "id"
  timezone: string
}

export type Checklist = { passkey: boolean; model: boolean; channel: boolean; hi: boolean; recall: boolean; mcp: boolean; dismissed: boolean }

export type HomePage = {
  checklist: Checklist
  connectors: { id: string; type: string; kind: ConnectorKind; label: string; status: ConnectorStatus; statusDetail: string | null; enabled: boolean }[]
  reminders: { id: string; text: string; fireAt: number; tz: string; rrule: string | null }[]
  runs: RunListItem[]
  tokens: { tokensIn: number; tokensOut: number; runs: number }
  // Tokens per day for the last 7 days, today last.
  week: DayTokens[]
  timezone: string
  webChatId: string
  runtimeUp: boolean
  appUrl: string
}

export const getShell = noInput<Shell>("getShell", "GET")
export const getHome = noInput<HomePage>("getHome", "GET")
export const dismissChecklist = noInput<Ok>("dismissChecklist", "POST")
export const getStatusSummary = noInput<StatusSummary>("getStatusSummary", "GET")
