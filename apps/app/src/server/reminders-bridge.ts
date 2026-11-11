// Reminder writes for the dashboard: thin adapters over the reminders service
// (packages/core/src/reminders/service.ts) plus a poke so the runtime's
// scheduler re-arms right away instead of on its 30 s sweep.
import * as service from "@palui/core/reminders/service"
import type { DB } from "@palui/db"
import { runtime } from "./runtime"

export type ReminderRow = service.ReminderRow
export type SnoozePreset = "10m" | "1h" | "tomorrow"
export type CreateReminderInput = service.CreateReminderInput

function poke() {
  runtime()
    .reconcile()
    .catch(() => {})
}

export function createReminder(db: DB, input: CreateReminderInput, now = Date.now()): ReminderRow {
  const row = service.createReminder(db, input, now)
  poke()
  return row
}

export function cancelReminder(db: DB, id: string, now = Date.now()): boolean {
  if (!service.getReminder(db, id)) return false
  service.cancelReminder(db, id, now)
  poke()
  return true
}

export function snoozeReminder(db: DB, id: string, preset: SnoozePreset, now = Date.now()): ReminderRow | undefined {
  if (!service.getReminder(db, id)) return undefined
  const row = service.snoozeReminder(db, id, preset, now)
  poke()
  return row
}
