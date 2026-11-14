// Activity page: events, runs, usage.
import { noInput, withInput } from "./client"
import type { EventLevel, FlowRunStatus, FlowSkipReason, FlowStepStatus, MessageRole, RunListItem, RunStatus, RunTrigger, ToolCallView } from "./types"

export type EventsInput = { connectorId?: string; levels?: EventLevel[] }
export type EventItem = { id: number; at: number; level: EventLevel; connectorId: string | null; kind: string; message: string }
export type EventsPage = { events: EventItem[]; total: number; connectors: { id: string; label: string }[]; timezone: string }

export type RunsPage = { runs: RunListItem[]; total: number; timezone: string }

export type RunMessage = { id: string; role: MessageRole; text: string; createdAt: number }

export type RunDetail = {
  id: string
  trigger: RunTrigger
  model: string | null
  status: RunStatus
  error: string | null
  resultPreview: string | null
  tokensIn: number
  tokensOut: number
  tokensCached: number
  tokensCacheWrite: number
  tokensCounted: number
  steps: number
  tainted: boolean
  startedAt: number
  ms: number | null
  chatId: string | null
  jobId: string | null
  chat: string | null
  job: string | null
  chatGone: boolean
  jobGone: boolean
  prompt: RunMessage[]
  reply: RunMessage[]
  tools: ToolCallView[]
}

export type FlowRunStep = {
  seq: number
  stepId: string
  kind: string
  status: FlowStepStatus
  branch: string | null
  error: string | null
  ms: number | null
  tainted: boolean
  attempt: number
  input: string | null
  output: string | null
  turnRunId: string | null
}

export type FlowRunDetail = {
  id: string
  flowId: string
  flowName: string | null
  status: FlowRunStatus
  skipReason: FlowSkipReason | null
  trigger: string
  triggerKind: string
  runKey: string
  startedAt: number
  endedAt: number | null
  scheduledAt: number | null
  wakeAt: number | null
  waiting: string | null
  error: string | null
  tainted: boolean
  taint: string[]
  parentRunId: string | null
  payload: string
  steps: FlowRunStep[]
  children: { id: string; status: FlowRunStatus; item: number | null }[]
}

// A flow run id (flr_...) answers with flowRun, anything else with run.
export type RunPage = { run: null; flowRun: FlowRunDetail | null; timezone: string } | { run: RunDetail | null; flowRun: null; timezone: string }

export type UsageRow = { key: string; tokens: number; runs: number }
export type UsageGroup = { rows: UsageRow[]; total: number }
export type UsagePage = {
  days: { day: string; tokensIn: number; tokensOut: number }[]
  perModel: UsageGroup
  perAgent: UsageGroup
  perChat: UsageGroup
  perJob: UsageGroup
  timezone: string
}

export const getEvents = withInput<EventsInput, EventsPage>("getEvents", "GET")
export const getRuns = noInput<RunsPage>("getRuns", "GET")
export const getRun = withInput<{ id: string }, RunPage>("getRun", "GET")
export const getUsage = noInput<UsagePage>("getUsage", "GET")
