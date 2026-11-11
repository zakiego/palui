// Reminder CRUD (PLAN 6). Every write recomputes fire_at in wall-clock time
// (time.ts) and tells the scheduler through notifyScheduleChanged (signal.ts) so a reminder due
// in a few seconds doesn't wait for the next sweep.
import { and, asc, desc, eq, inArray, isNotNull, type SQL } from "drizzle-orm"
import { newId, reminders, type DB } from "@palui/db"
import { getChat } from "../chats"
import { notifyScheduleChanged } from "./signal"
import { assertZone, formatEcho, formatWhen, nextOccurrence, normalizeLocal, normalizeRrule, resolveFirstFire, TimeError, tomorrowAt, type Schedule } from "./time"

export type ReminderRow = typeof reminders.$inferSelect

export class ReminderError extends Error {}

export const MAX_REMINDER_TEXT = 500

// --- Helpers ---------------------------------------------------------------------------

function wrap<T>(fn: () => T): T {
  try {
    return fn()
  } catch (e) {
    if (e instanceof TimeError) throw new ReminderError(e.message)
    throw e
  }
}

function cleanText(text: string): string {
  const t = text.trim()
  if (!t) throw new ReminderError("The reminder text is empty.")
  if (t.length > MAX_REMINDER_TEXT) throw new ReminderError(`The reminder text is longer than ${MAX_REMINDER_TEXT} characters.`)
  return t
}

function scheduleOf(r: Pick<ReminderRow, "rrule" | "tz" | "localStart">): Schedule {
  return { rrule: r.rrule, tz: r.tz, localStart: r.localStart }
}

// "Besok, Rabu 11 Nov, 09.00 WITA" for one-offs, "Tiap Senin, 08.00 WITA" for series.
export function reminderEcho(r: ReminderRow, now = Date.now()): string {
  return formatEcho(scheduleOf(r), r.fireAt, now)
}

// The next fire as a date ("Senin 16 Nov, 08.00 WITA"), useful for series.
export function reminderNext(r: ReminderRow, now = Date.now()): string {
  return formatWhen(r.fireAt, r.tz, now)
}

// --- CRUD ----------------------------------------------------------------------------------

export type CreateReminderInput = {
  chatId: string
  text: string
  localStart: string
  tz: string
  rrule?: string | null
  smartPrompt?: string | null
}

export function createReminder(db: DB, input: CreateReminderInput, now = Date.now()): ReminderRow {
  const row = wrap(() => {
    if (!getChat(db, input.chatId)) throw new ReminderError(`No chat ${input.chatId}.`)
    assertZone(input.tz)
    // Same rules as the parser: a one-off earlier today rolls to tomorrow, far-off
    // dates are refused (resolveFirstFire).
    const { schedule: s, fireAt } = resolveFirstFire(
      {
        rrule: input.rrule ? normalizeRrule(input.rrule) : null,
        tz: input.tz,
        localStart: normalizeLocal(input.localStart),
      },
      now,
    )
    return db
      .insert(reminders)
      .values({
        id: newId("rem"),
        chatId: input.chatId,
        text: cleanText(input.text),
        rrule: s.rrule,
        tz: s.tz,
        localStart: s.localStart,
        fireAt,
        scheduledFor: fireAt,
        status: "pending",
        smartPrompt: input.smartPrompt?.trim() || null,
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
  })
  notifyScheduleChanged()
  return row
}

export function getReminder(db: DB, id: string): ReminderRow | undefined {
  return db.select().from(reminders).where(eq(reminders.id, id)).get()
}

function mustGet(db: DB, id: string): ReminderRow {
  const r = getReminder(db, id)
  if (!r) throw new ReminderError(`No reminder ${id}.`)
  return r
}

export type ReminderFilter = "upcoming" | "recurring" | "done"

// upcoming: pending/firing by next fire. recurring: live series by next fire.
// done: finished and cancelled, most recently changed first.
export function listReminders(db: DB, opts: { filter: ReminderFilter; chatId?: string; limit?: number }): ReminderRow[] {
  const where: SQL[] = []
  if (opts.chatId) where.push(eq(reminders.chatId, opts.chatId))
  const limit = Math.min(Math.max(opts.limit ?? 50, 1), 500)
  if (opts.filter === "done") {
    where.push(inArray(reminders.status, ["done", "cancelled"]))
    return db
      .select()
      .from(reminders)
      .where(and(...where))
      .orderBy(desc(reminders.updatedAt))
      .limit(limit)
      .all()
  }
  where.push(inArray(reminders.status, ["pending", "firing"]))
  if (opts.filter === "recurring") where.push(isNotNull(reminders.rrule))
  return db
    .select()
    .from(reminders)
    .where(and(...where))
    .orderBy(asc(reminders.fireAt))
    .limit(limit)
    .all()
}

// The next `n` pending reminders. Overdue ones (fire_at < now) are about to fire
// and stay listed first; `now` is kept for the shared signature convention.
export function nextReminders(db: DB, n: number, _now = Date.now()): ReminderRow[] {
  return db.select().from(reminders).where(eq(reminders.status, "pending")).orderBy(asc(reminders.fireAt)).limit(n).all()
}

export type ReminderPatch = {
  text?: string
  localStart?: string
  tz?: string
  rrule?: string | null
  smartPrompt?: string | null
}

// Editing the time reschedules: fire_at is recomputed from the new schedule and a
// finished one-off goes back to pending. Cancelled reminders can't be edited.
export function updateReminder(db: DB, id: string, patch: ReminderPatch, now = Date.now()): ReminderRow {
  const row = wrap(() => {
    const r = mustGet(db, id)
    if (r.status === "cancelled") throw new ReminderError("That reminder was cancelled. Create a new one instead.")
    const set: Partial<ReminderRow> = { updatedAt: now }
    if (patch.text !== undefined) set.text = cleanText(patch.text)
    if (patch.smartPrompt !== undefined) set.smartPrompt = patch.smartPrompt?.trim() || null
    const timeChanged = patch.localStart !== undefined || patch.tz !== undefined || patch.rrule !== undefined
    if (timeChanged) {
      const tz = patch.tz ?? r.tz
      assertZone(tz)
      const { schedule: s, fireAt } = resolveFirstFire(
        {
          tz,
          localStart: patch.localStart !== undefined ? normalizeLocal(patch.localStart) : r.localStart,
          rrule: patch.rrule !== undefined ? (patch.rrule ? normalizeRrule(patch.rrule) : null) : r.rrule,
        },
        now,
      )
      Object.assign(set, { ...s, fireAt, scheduledFor: fireAt, status: "pending", snoozedUntil: null })
    }
    return db.update(reminders).set(set).where(eq(reminders.id, id)).returning().get()
  })
  notifyScheduleChanged()
  return row
}

export function cancelReminder(db: DB, id: string, now = Date.now()): ReminderRow {
  mustGet(db, id)
  const row = db.update(reminders).set({ status: "cancelled", updatedAt: now }).where(eq(reminders.id, id)).returning().get()
  notifyScheduleChanged()
  return row
}

export type SnoozePreset = "10m" | "1h" | "tomorrow" | { until: number }

export function snoozeUntil(preset: SnoozePreset, tz: string, now: number): number {
  if (typeof preset === "object") return preset.until
  if (preset === "10m") return now + 10 * 60_000
  if (preset === "1h") return now + 3600_000
  return tomorrowAt(now, tz)
}

// Moves the next fire to the snooze time. A done one-off comes back as pending. A
// series keeps its rule: after the snoozed fire, the next occurrence follows the
// rule again.
export function snoozeReminder(db: DB, id: string, preset: SnoozePreset, now = Date.now()): ReminderRow {
  const r = mustGet(db, id)
  if (r.status === "cancelled") throw new ReminderError("That reminder was cancelled.")
  const until = snoozeUntil(preset, r.tz, now)
  if (!Number.isFinite(until) || until <= now) throw new ReminderError("The snooze time must be in the future.")
  const row = db
    .update(reminders)
    .set({ fireAt: until, snoozedUntil: until, status: "pending", updatedAt: now })
    .where(eq(reminders.id, id))
    .returning()
    .get()
  notifyScheduleChanged()
  return row
}

// Done. A one-off is finished. A series just acknowledges this occurrence and keeps
// going; if it was snoozed, the snooze is dropped and the rule's next occurrence
// comes back.
export function markDone(db: DB, id: string, now = Date.now()): ReminderRow {
  const r = mustGet(db, id)
  if (r.status === "cancelled") throw new ReminderError("That reminder was cancelled.")
  let set: Partial<ReminderRow>
  if (!r.rrule) {
    set = { status: "done", snoozedUntil: null, updatedAt: now }
  } else if (r.snoozedUntil !== null && r.fireAt === r.snoozedUntil) {
    const next = wrap(() => nextOccurrence(scheduleOf(r), now))
    set =
      next === null
        ? { status: "done", snoozedUntil: null, updatedAt: now }
        : { fireAt: next, scheduledFor: next, snoozedUntil: null, status: "pending", updatedAt: now }
  } else {
    set = { updatedAt: now }
  }
  const row = db.update(reminders).set(set).where(eq(reminders.id, id)).returning().get()
  notifyScheduleChanged()
  return row
}
