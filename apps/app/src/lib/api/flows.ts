// Flows pages: read-only. Flows are built and changed from chat.
import { noInput, withInput } from "./client"
import type { FlowApproval, FlowRunStatus, FlowSkipReason, FlowStepKind, FlowTriggerKind } from "./types"

export type FlowListItem = {
  id: string
  name: string
  description: string | null
  enabled: boolean
  pausedUntil: number | null
  failures: number
  lastError: string | null
  approval: FlowApproval
  chatTitle: string | null
  tz: string
  triggers: { id: string; kind: FlowTriggerKind; summary: string }[]
  steps: number
  nextAt: number | null
  lastRun: { id: string; status: FlowRunStatus; skipReason: FlowSkipReason | null; startedAt: number; error: string | null } | null
  runs7d: number
}

export type FlowDetail = {
  id: string
  name: string
  description: string | null
  enabled: boolean
  pausedUntil: number | null
  failures: number
  lastError: string | null
  approval: FlowApproval
  chatId: string
  chatTitle: string | null
  tz: string
  createdAt: number
  version: number
  state: string | null
  triggers: { id: string; kind: FlowTriggerKind; start: string; summary: string; nextAt: number | null; lastError: string | null }[]
  steps: {
    id: string
    kind: FlowStepKind
    name: string | null
    summary: string
    write: boolean
    startOf: string[]
    edges: { label: string; to: string | null }[]
    ends: boolean
  }[]
  writeCalls: string[]
  versions: { n: number; createdBy: string; note: string | null; approved: boolean; writes: number; createdAt: number; current: boolean; diff: string[] }[]
  runs: {
    id: string
    status: FlowRunStatus
    skipReason: FlowSkipReason | null
    trigger: string
    triggerKind: string
    startedAt: number
    scheduledAt: number | null
    label: string | null
    error: string | null
    child: boolean
    answer: string | null
  }[]
}

export const getFlows = noInput<{ flows: FlowListItem[]; timezone: string }>("getFlows", "GET")
export const getFlowFn = withInput<{ id: string }, { flow: FlowDetail | null; timezone: string }>("getFlowFn", "GET")
