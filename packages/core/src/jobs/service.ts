// Scheduled jobs (PLAN 7): an RRULE, a zone, a prompt, a target chat, a tool scope
// and a model role. next_run_at is computed in wall-clock time with the same code
// as reminders (reminders/time.ts). The scheduler claims due jobs; the job runner
// (runner.ts) turns them into schedule-triggered turns.
import { and, asc, eq, type SQL } from "drizzle-orm"
import { jobs, newId, type DB, type JobKind } from "@palui/db"
import { getChat } from "../chats"
import { notifyScheduleChanged } from "../reminders/signal"
import { assertZone, formatEcho, formatWhen, nextOccurrence, normalizeLocal, normalizeRrule, TimeError, utcToLocal, type Schedule } from "../reminders/time"
import type { ModelRole } from "../settings"

export type JobRow = typeof jobs.$inferSelect

export class JobError extends Error {}

export type CreateJobInput = {
  name: string
  kind?: JobKind
  rrule: string
  tz: string
  // Series anchor and time of day (ISO local, no offset). Default: today 09:00 in tz.
  localStart?: string
  prompt: string
  chatId: string
  modelRole?: ModelRole
  // Connector ids the job may use; null = every enabled connector.
  toolScope?: string[] | null
  enabled?: boolean
  dailyTokenCap?: number
}

export type JobPatch = Partial<Omit<CreateJobInput, "chatId">> & { chatId?: string }

function wrap<T>(fn: () => T): T {
  try {
    return fn()
  } catch (e) {
    if (e instanceof TimeError) throw new JobError(e.message)
    throw e
  }
}

function scheduleOf(j: Pick<JobRow, "rrule" | "tz" | "localStart">): Schedule {
  return { rrule: j.rrule, tz: j.tz, localStart: j.localStart }
}

// The next run strictly after `now`, or null when the rule has ended.
export function computeNextRun(j: Pick<JobRow, "rrule" | "tz" | "localStart">, now: number): number | null {
  return wrap(() => nextOccurrence(scheduleOf(j), now))
}

function checkText(name: string, prompt: string) {
  if (!name.trim()) throw new JobError("A job needs a name.")
  if (!prompt.trim()) throw new JobError("A job needs a prompt.")
  if (prompt.length > 8000) throw new JobError("The job prompt is longer than 8000 characters.")
}

function checkCap(cap: number) {
  if (!Number.isInteger(cap) || cap < 0) throw new JobError("The daily token cap must be a whole number, 0 or more.")
}

export function createJob(db: DB, input: CreateJobInput, now = Date.now()): JobRow {
  const row = wrap(() => {
    checkText(input.name, input.prompt)
    if (!getChat(db, input.chatId)) throw new JobError(`No chat ${input.chatId}.`)
    assertZone(input.tz)
    if (input.dailyTokenCap !== undefined) checkCap(input.dailyTokenCap)
    const s: Schedule = {
      rrule: normalizeRrule(input.rrule),
      tz: input.tz,
      localStart: input.localStart ? normalizeLocal(input.localStart) : `${utcToLocal(now, input.tz).slice(0, 10)}T09:00:00`,
    }
    const enabled = input.enabled ?? true
    return db
      .insert(jobs)
      .values({
        id: newId("job"),
        name: input.name.trim(),
        kind: input.kind ?? "job",
        rrule: s.rrule as string,
        tz: s.tz,
        localStart: s.localStart,
        prompt: input.prompt.trim(),
        chatId: input.chatId,
        modelRole: input.modelRole ?? "default",
        toolScope: input.toolScope ?? null,
        enabled,
        nextRunAt: enabled ? nextOccurrence(s, now) : null,
        ...(input.dailyTokenCap !== undefined ? { dailyTokenCap: input.dailyTokenCap } : {}),
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
  })
  notifyScheduleChanged()
  return row
}

export function getJob(db: DB, id: string): JobRow | undefined {
  return db.select().from(jobs).where(eq(jobs.id, id)).get()
}

function mustGet(db: DB, id: string): JobRow {
  const j = getJob(db, id)
  if (!j) throw new JobError(`No job ${id}.`)
  return j
}

export function updateJob(db: DB, id: string, patch: JobPatch, now = Date.now()): JobRow {
  const row = wrap(() => {
    const j = mustGet(db, id)
    const set: Partial<JobRow> = { updatedAt: now }
    if (patch.name !== undefined || patch.prompt !== undefined) checkText(patch.name ?? j.name, patch.prompt ?? j.prompt)
    if (patch.name !== undefined) set.name = patch.name.trim()
    if (patch.prompt !== undefined) set.prompt = patch.prompt.trim()
    if (patch.kind !== undefined) set.kind = patch.kind
    if (patch.modelRole !== undefined) set.modelRole = patch.modelRole
    if (patch.toolScope !== undefined) set.toolScope = patch.toolScope
    if (patch.dailyTokenCap !== undefined) {
      checkCap(patch.dailyTokenCap)
      set.dailyTokenCap = patch.dailyTokenCap
    }
    if (patch.chatId !== undefined) {
      if (!getChat(db, patch.chatId)) throw new JobError(`No chat ${patch.chatId}.`)
      set.chatId = patch.chatId
    }
    if (patch.enabled !== undefined) set.enabled = patch.enabled
    const s: Schedule = {
      rrule: patch.rrule !== undefined ? normalizeRrule(patch.rrule) : j.rrule,
      tz: patch.tz ?? j.tz,
      localStart: patch.localStart !== undefined ? normalizeLocal(patch.localStart) : j.localStart,
    }
    assertZone(s.tz)
    Object.assign(set, { rrule: s.rrule, tz: s.tz, localStart: s.localStart })
    const enabled = set.enabled ?? j.enabled
    set.nextRunAt = enabled ? nextOccurrence(s, now) : null
    return db.update(jobs).set(set).where(eq(jobs.id, id)).returning().get()
  })
  notifyScheduleChanged()
  return row
}

// Pause/resume. Resuming computes the next run from now: runs missed while paused
// are skipped, not caught up.
export function setJobEnabled(db: DB, id: string, enabled: boolean, now = Date.now()): JobRow {
  return updateJob(db, id, { enabled }, now)
}

export function listJobs(db: DB, opts: { chatId?: string; enabled?: boolean; kind?: JobKind } = {}): JobRow[] {
  const where: SQL[] = []
  if (opts.chatId) where.push(eq(jobs.chatId, opts.chatId))
  if (opts.enabled !== undefined) where.push(eq(jobs.enabled, opts.enabled))
  if (opts.kind) where.push(eq(jobs.kind, opts.kind))
  const rows = db
    .select()
    .from(jobs)
    .where(where.length ? and(...where) : undefined)
    .orderBy(asc(jobs.nextRunAt), asc(jobs.name))
    .all()
  // Paused jobs (next_run_at null) sort last.
  return [...rows.filter((r) => r.nextRunAt !== null), ...rows.filter((r) => r.nextRunAt === null)]
}

export function deleteJob(db: DB, id: string): boolean {
  const n = db.delete(jobs).where(eq(jobs.id, id)).run().changes
  if (n) notifyScheduleChanged()
  return n > 0
}

// "Tiap hari, 07.00 WITA"
export function describeJobSchedule(j: JobRow, now = Date.now()): string {
  return wrap(() => formatEcho(scheduleOf(j), j.nextRunAt ?? now, now))
}

// "Besok, Rabu 11 Nov, 07.00 WITA", or null when paused or finished.
export function describeNextRun(j: JobRow, now = Date.now()): string | null {
  return j.nextRunAt === null ? null : formatWhen(j.nextRunAt, j.tz, now)
}
