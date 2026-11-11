// Job writes for the dashboard: thin adapters over the jobs service
// (packages/core/src/jobs/*) plus a poke so the scheduler re-arms at once.
import { dailyBriefing, heartbeat } from "@palui/core/jobs/presets"
import * as service from "@palui/core/jobs/service"
import type { DB } from "@palui/db"
import { runtime } from "./runtime"

export type JobRow = service.JobRow
export type JobInput = service.CreateJobInput

function poke() {
  runtime()
    .reconcile()
    .catch(() => {})
}

export function createJob(db: DB, input: JobInput, now = Date.now()): JobRow {
  const row = service.createJob(db, input, now)
  poke()
  return row
}

export function updateJob(db: DB, id: string, patch: Partial<JobInput>, now = Date.now()): JobRow | undefined {
  if (!service.getJob(db, id)) return undefined
  const row = service.updateJob(db, id, patch, now)
  poke()
  return row
}

export function setJobEnabled(db: DB, id: string, enabled: boolean, now = Date.now()): boolean {
  if (!service.getJob(db, id)) return false
  service.setJobEnabled(db, id, enabled, now)
  poke()
  return true
}

export function deleteJob(db: DB, id: string): boolean {
  const ok = service.deleteJob(db, id)
  poke()
  return ok
}

// Presets for the "New job" sheet (PLAN 7).
export const presets = {
  dailyBriefing: (chatId: string, tz: string): JobInput => dailyBriefing(chatId, tz),
  heartbeat: (chatId: string, tz: string): JobInput => heartbeat(chatId, tz),
}
