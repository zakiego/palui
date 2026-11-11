// The scheduler (PLAN 6, 7). One timer armed for min(next due - now, 60 s) plus a
// 30 s safety sweep. Node clamps setTimeout delays over ~24.8 days to 1 ms, so the
// 60 s cap is what keeps a far-off reminder from turning into a tight loop.
//
// Both paths run the same sweep, and the sweep only acts on rows it claimed with a
// single UPDATE ... RETURNING, so two sweeps (or two processes) never fire the same
// reminder twice:
//   reminders: pending -> firing, then done (one-off) or pending at the next fire
//   jobs:      idle -> running, next_run_at advanced, back to idle after the run
//
// A plain reminder never calls the model: it becomes an outbox row with Done and
// Snooze actions. A smart reminder (smart_prompt set) goes to onSmartReminder.
import { and, eq, isNotNull, lte, min } from "drizzle-orm"
import { chats, jobs, reminders, type DB, type OutboxPayload } from "@palui/db"
import { logEvent } from "../events"
import type { Logger } from "../log"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import type { ReminderRow } from "./service"
import { onScheduleChanged } from "./signal"
import { formatClock, formatDate, latestAtOrBefore, nextOccurrence } from "./time"

export type JobRow = typeof jobs.$inferSelect

export type SchedulerTimers = {
  setTimeout: (fn: () => void, ms: number) => unknown
  clearTimeout: (handle: never) => void
  setInterval: (fn: () => void, ms: number) => unknown
  clearInterval: (handle: never) => void
}

export type SchedulerOptions = {
  db: DB
  log: Logger
  now?: () => number
  timers?: SchedulerTimers
  // Runs the reminder's smart prompt as a turn. When it throws, the plain text is
  // sent instead so the reminder isn't lost.
  onSmartReminder?: (r: ReminderRow) => Promise<void>
  // Runs a claimed job (createJobRunner().run). The scheduler sets the job back to
  // idle once this settles.
  onJobDue?: (job: JobRow) => Promise<void>
  // More things to sweep on the same timer (flows). Each claims its own rows.
  extensions?: SchedulerExtension[]
}

export type SchedulerExtension = {
  recover?(now: number): void
  sweep(now: number): number
  nextDue(): number | null
  idle?(): Promise<void>
}

export type Scheduler = {
  // Recovers rows left mid-fire by a crash, sweeps once (overdue reminders fire
  // now, with the "telat" note) and arms the timers. Resolves after that sweep.
  start(): Promise<void>
  stop(): void
  // Sweep now and re-arm (after a reminder or job changed).
  poke(): void
  // One sweep; what the timer and the safety interval both call. Exposed for tests.
  sweep(): Promise<{ reminders: number; jobs: number }>
  // Resolves once the smart reminders and job runs started so far have settled.
  idle(): Promise<void>
}

export const MAX_TIMER_MS = 60_000
export const SWEEP_INTERVAL_MS = 30_000
export const LATE_AFTER_MS = 2 * 60_000
export const REMINDER_OUTBOX_TTL_MS = 24 * 3600_000
// When something is due but still pending right after a sweep (a fire failed), wait
// this long before trying again instead of spinning.
const RETRY_MS = 1000

const defaultTimers: SchedulerTimers = {
  setTimeout: (fn, ms) => {
    const t = setTimeout(fn, ms)
    t.unref?.()
    return t
  },
  clearTimeout: (h) => clearTimeout(h as unknown as NodeJS.Timeout),
  setInterval: (fn, ms) => {
    const t = setInterval(fn, ms)
    t.unref?.()
    return t
  },
  clearInterval: (h) => clearInterval(h as unknown as NodeJS.Timeout),
}

export function reminderActions(id: string): { id: string; label: string }[] {
  return [
    { id: `rem:done:${id}`, label: "Done" },
    { id: `rem:snooze:${id}:10m`, label: "10m" },
    { id: `rem:snooze:${id}:1h`, label: "1j" },
    { id: `rem:snooze:${id}:tomorrow`, label: "Besok 09.00" },
  ]
}

// "⏰ Bayar listrik" plus "(telat, harusnya 09.00)" when the fire is more than two
// minutes late (downtime). The date is added when it was meant for another day.
// For a series that missed several runs, the note names the latest missed one.
export function reminderMessage(r: Pick<ReminderRow, "text" | "fireAt" | "tz" | "rrule" | "localStart" | "snoozedUntil">, now: number): string {
  let note = ""
  if (now - r.fireAt > LATE_AFTER_MS) {
    let meant = r.fireAt
    if (r.rrule && r.snoozedUntil !== r.fireAt) {
      try {
        meant = latestAtOrBefore({ rrule: r.rrule, tz: r.tz, localStart: r.localStart }, r.fireAt, now)
      } catch {
        // Keep fireAt.
      }
    }
    const sameDay = formatDate(meant, r.tz, now) === formatDate(now, r.tz, now)
    const when = sameDay ? formatClock(meant, r.tz) : `${formatDate(meant, r.tz, now)} ${formatClock(meant, r.tz)}`
    note = ` (telat, harusnya ${when})`
  }
  return `⏰ ${r.text}${note}`
}

export function createScheduler(opts: SchedulerOptions): Scheduler {
  const { db, log } = opts
  const clock = opts.now ?? (() => Date.now())
  const timers = opts.timers ?? defaultTimers
  let timer: unknown = null
  let interval: unknown = null
  let running = false
  let unsubscribe: (() => void) | null = null
  const inFlight = new Set<Promise<void>>()

  function track(p: Promise<void>) {
    inFlight.add(p)
    void p.finally(() => inFlight.delete(p))
  }

  // Moves a fired reminder on: one-off -> done; series -> next occurrence after
  // `now` (so several missed runs collapse into the one fire that just happened).
  function advance(r: ReminderRow, now: number, outboxId: string | null) {
    const set: Partial<ReminderRow> = { lastFiredAt: now, snoozedUntil: null, updatedAt: now }
    if (outboxId) set.lastOutboxId = outboxId
    if (!r.rrule) {
      set.status = "done"
    } else {
      let next: number | null = null
      try {
        next = nextOccurrence({ rrule: r.rrule, tz: r.tz, localStart: r.localStart }, now)
      } catch (e) {
        log.error("reminder rule broke, stopping the series", { reminder_id: r.id, err: e as Error })
      }
      if (next === null) set.status = "done"
      else Object.assign(set, { status: "pending", fireAt: next, scheduledFor: next })
    }
    db.update(reminders)
      .set(set)
      .where(and(eq(reminders.id, r.id), eq(reminders.status, "firing")))
      .run()
  }

  // Enqueue the reminder text and advance, in one transaction. Returns false when
  // the chat is gone (the reminder is cancelled then).
  function firePlain(r: ReminderRow, now: number): boolean {
    const chat = db.select().from(chats).where(eq(chats.id, r.chatId)).get()
    if (!chat) {
      db.update(reminders).set({ status: "cancelled", updatedAt: now }).where(eq(reminders.id, r.id)).run()
      logEvent(db, { level: "warn", kind: "reminder.orphan", message: `Reminder ${r.id} was cancelled: its chat ${r.chatId} no longer exists.` }, now)
      return false
    }
    const payload: OutboxPayload = { text: reminderMessage(r, now), actions: reminderActions(r.id) }
    db.transaction(() => {
      const row = enqueueOutbox(db, { chatId: r.chatId, channel: chat.channel, payload, source: `reminder:${r.id}`, expiresAt: now + REMINDER_OUTBOX_TTL_MS }, now)
      advance(r, now, row.id)
    })
    return true
  }

  async function fireSmart(r: ReminderRow) {
    try {
      await (opts.onSmartReminder as (r: ReminderRow) => Promise<void>)(r)
      advance(r, clock(), null)
    } catch (e) {
      log.warn("smart reminder failed, sending the plain text", { reminder_id: r.id, err: e as Error })
      if (firePlain(r, clock())) notifyOutbox()
    }
  }

  function sweepReminders(now: number): number {
    const claimed = db
      .update(reminders)
      .set({ status: "firing", updatedAt: now })
      .where(and(eq(reminders.status, "pending"), lte(reminders.fireAt, now)))
      .returning()
      .all()
    let enqueued = 0
    for (const r of claimed) {
      try {
        if (r.smartPrompt && opts.onSmartReminder) {
          track(fireSmart(r))
        } else if (firePlain(r, now)) {
          enqueued++
        }
      } catch (e) {
        // Leave it pending so the next sweep retries it (delivery is at least once).
        log.error("reminder fire failed", { reminder_id: r.id, err: e as Error })
        db.update(reminders).set({ status: "pending" }).where(and(eq(reminders.id, r.id), eq(reminders.status, "firing"))).run()
      }
    }
    if (enqueued) notifyOutbox()
    return claimed.length
  }

  function sweepJobs(now: number): number {
    const claimed = db
      .update(jobs)
      .set({ status: "running", updatedAt: now })
      .where(and(eq(jobs.status, "idle"), eq(jobs.enabled, true), isNotNull(jobs.nextRunAt), lte(jobs.nextRunAt, now)))
      .returning()
      .all()
    for (const job of claimed) {
      let next: number | null = null
      try {
        next = nextOccurrence({ rrule: job.rrule, tz: job.tz, localStart: job.localStart }, now)
      } catch (e) {
        log.error("job rule broke, it won't run again until edited", { job_id: job.id, err: e as Error })
      }
      db.update(jobs).set({ nextRunAt: next }).where(eq(jobs.id, job.id)).run()
      const setIdle = () => db.update(jobs).set({ status: "idle", updatedAt: clock() }).where(eq(jobs.id, job.id)).run()
      if (!opts.onJobDue) {
        log.warn("job due but no job runner is wired", { job_id: job.id })
        setIdle()
        continue
      }
      track(
        opts
          .onJobDue({ ...job, nextRunAt: next })
          .catch((e) => {
            log.error("job run failed", { job_id: job.id, err: e as Error })
            db.update(jobs)
              .set({ lastStatus: "error", lastResult: String((e as Error)?.message ?? e).slice(0, 500), lastRunAt: clock() })
              .where(eq(jobs.id, job.id))
              .run()
          })
          .finally(setIdle),
      )
    }
    return claimed.length
  }

  async function sweep(): Promise<{ reminders: number; jobs: number }> {
    const now = clock()
    let r = 0
    let j = 0
    try {
      r = sweepReminders(now)
    } catch (e) {
      log.error("reminder sweep failed", { err: e as Error })
    }
    try {
      j = sweepJobs(now)
    } catch (e) {
      log.error("job sweep failed", { err: e as Error })
    }
    for (const ext of opts.extensions ?? []) {
      try {
        ext.sweep(now)
      } catch (e) {
        log.error("scheduler extension sweep failed", { err: e as Error })
      }
    }
    return { reminders: r, jobs: j }
  }

  function nextDue(): number | null {
    const a = db.select({ v: min(reminders.fireAt) }).from(reminders).where(eq(reminders.status, "pending")).get()?.v ?? null
    const b =
      db
        .select({ v: min(jobs.nextRunAt) })
        .from(jobs)
        .where(and(eq(jobs.enabled, true), eq(jobs.status, "idle"), isNotNull(jobs.nextRunAt)))
        .get()?.v ?? null
    const all = [a, b, ...(opts.extensions ?? []).map((e) => e.nextDue())].filter((v): v is number => typeof v === "number")
    return all.length ? Math.min(...all) : null
  }

  function arm() {
    if (!running) return
    if (timer !== null) timers.clearTimeout(timer as never)
    let delay = MAX_TIMER_MS
    try {
      const due = nextDue()
      if (due !== null) {
        const d = due - clock()
        delay = d <= 0 ? RETRY_MS : Math.min(d, MAX_TIMER_MS)
      }
    } catch (e) {
      log.error("could not read the next due time", { err: e as Error })
    }
    timer = timers.setTimeout(() => {
      timer = null
      void tick()
    }, delay)
  }

  async function tick() {
    if (!running) return
    await sweep()
    arm()
  }

  return {
    async start() {
      if (running) return
      running = true
      // Rows a crash left mid-fire: fire them again (at least once beats never).
      const now = clock()
      db.update(reminders).set({ status: "pending", updatedAt: now }).where(eq(reminders.status, "firing")).run()
      db.update(jobs).set({ status: "idle", updatedAt: now }).where(eq(jobs.status, "running")).run()
      for (const ext of opts.extensions ?? []) ext.recover?.(now)
      unsubscribe = onScheduleChanged(() => this.poke())
      interval = timers.setInterval(() => void tick(), SWEEP_INTERVAL_MS)
      await tick()
    },
    stop() {
      running = false
      unsubscribe?.()
      unsubscribe = null
      if (timer !== null) timers.clearTimeout(timer as never)
      if (interval !== null) timers.clearInterval(interval as never)
      timer = null
      interval = null
    },
    poke() {
      if (running) void tick()
    },
    sweep,
    async idle() {
      while (inFlight.size) await Promise.all([...inFlight])
      for (const ext of opts.extensions ?? []) await ext.idle?.()
    },
  }
}
