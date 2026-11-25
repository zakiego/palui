// Watches page (PLAN 6e). Watches are made from chat, so there's no create.
import { noInput, withInput } from "./client"
import type { Channel, WatchStatus } from "./types"

export type WatchItem = {
  id: string
  topic: string
  question: string
  doneWhen: string
  everyMin: number
  expiresAt: number
  // When checking begins; null means when it was made.
  startsAt: number | null
  status: WatchStatus
  nextCheckAt: number | null
  checks: number
  fails: number
  lastCheckAt: number | null
  lastRunId: string | null
  lastError: string | null
  lastSummary: string
  // The message sent when it was found.
  result: string | null
  tokensUsed: number
  tokenCap: number
  endedAt: number | null
  createdAt: number
  createdBy: string
  chat: { id: string; title: string; channel: Channel } | null
}

export type WatchesPage = {
  active: WatchItem[]
  ended: WatchItem[]
  // The lists are capped; these are how many rows there really are.
  totals: { active: number; ended: number }
  timezone: string
}

export const getWatches = noInput<WatchesPage>("getWatches", "GET")
export const cancelWatchFn = withInput<{ id: string }, { status: WatchStatus }>("cancelWatchFn", "POST")
