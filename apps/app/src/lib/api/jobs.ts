// Jobs page.
import { noInput, withInput } from "./client"
import type { ChatOption, JobKind, ModelRole, Ok } from "./types"

export type JobItem = {
  id: string
  name: string
  kind: JobKind
  rrule: string
  tz: string
  localStart: string
  prompt: string
  chatId: string
  modelRole: ModelRole
  toolScope: string[] | null
  enabled: boolean
  nextRunAt: number | null
  status: "idle" | "running"
  lastRunAt: number | null
  lastStatus: string | null
  lastResult: string | null
  dailyTokenCap: number
  createdAt: number
  updatedAt: number
  chatTitle: string | null
  tokensToday: number
}

export type JobsPage = { jobs: JobItem[]; chats: ChatOption[]; timezone: string }

export type SaveJobInput = {
  id?: string
  name: string
  kind?: JobKind
  rrule: string
  tz: string
  localStart: string
  prompt: string
  chatId: string
  modelRole: ModelRole
  dailyTokenCap: number
}

// A preset's fields for the job form (core's CreateJobInput).
export type JobPreset = {
  name: string
  kind?: JobKind
  rrule: string
  tz: string
  localStart?: string
  prompt: string
  chatId: string
  modelRole?: ModelRole
  toolScope?: string[] | null
  enabled?: boolean
  dailyTokenCap?: number
}

export const getJobs = noInput<JobsPage>("getJobs", "GET")
export const runJobNow = withInput<{ id: string }, { ok: boolean; runId?: string; detail?: string }>("runJobNow", "POST")
export const setJobEnabledFn = withInput<{ id: string; enabled: boolean }, Ok>("setJobEnabledFn", "POST")
export const saveJob = withInput<SaveJobInput, { id: string }>("saveJob", "POST")
export const deleteJobFn = withInput<{ id: string }, Ok>("deleteJobFn", "POST")
export const getJobPreset = withInput<{ preset: "dailyBriefing" | "heartbeat"; chatId: string }, JobPreset>("getJobPreset", "GET")
