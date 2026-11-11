// Budgets (PLAN 5.2). Per turn: steps, wall clock, tool count (enforced by the
// turn). Global: daily tokens and runs per hour; per job: daily tokens. Counters
// live in the `usage` table so they survive restarts.
import { eq, sql } from "drizzle-orm"
import { DateTime } from "luxon"
import { usage, type DB } from "@palui/db"
import { getSetting, setSetting } from "../settings"

export function dayKey(now: number, tz: string): string {
  return DateTime.fromMillis(now, { zone: tz }).toFormat("yyyy-LL-dd")
}

export function hourKey(now: number, tz: string): string {
  return DateTime.fromMillis(now, { zone: tz }).toFormat("yyyy-LL-dd'T'HH")
}

export const usageKeys = {
  tokens: (now: number, tz: string) => `tokens:${dayKey(now, tz)}`,
  runs: (now: number, tz: string) => `runs:${hourKey(now, tz)}`,
  job: (jobId: string, now: number, tz: string) => `job:${jobId}:${dayKey(now, tz)}`,
}

export function getUsage(db: DB, key: string): number {
  return db.select().from(usage).where(eq(usage.key, key)).get()?.value ?? 0
}

export function addUsage(db: DB, key: string, n: number, now = Date.now()): number {
  if (!n) return getUsage(db, key)
  const row = db
    .insert(usage)
    .values({ key, value: n, updatedAt: now })
    .onConflictDoUpdate({ target: usage.key, set: { value: sql`${usage.value} + ${n}`, updatedAt: now } })
    .returning()
    .get()
  return row.value
}

export type BudgetCheck = { ok: true } | { ok: false; reason: string; until: number; fresh: boolean }

function nextHour(now: number, tz: string): number {
  return DateTime.fromMillis(now, { zone: tz }).startOf("hour").plus({ hours: 1 }).toMillis()
}

function nextDay(now: number, tz: string): number {
  return DateTime.fromMillis(now, { zone: tz }).startOf("day").plus({ days: 1 }).toMillis()
}

export function formatClock(ms: number, tz: string): string {
  return DateTime.fromMillis(ms, { zone: tz }).toFormat("HH.mm")
}

// Whether a new turn may start. A hit cap pauses turns (settings) until the
// window rolls over; `fresh` is true the first time, so the caller tells the owner once.
export function checkGlobalBudget(db: DB, now = Date.now()): BudgetCheck {
  const tz = getSetting(db, "timezone")
  const until = getSetting(db, "turnsPausedUntil")
  if (until && until > now) return { ok: false, reason: getSetting(db, "turnsPausedReason") ?? "budget", until, fresh: false }
  if (until && until <= now) {
    setSetting(db, "turnsPausedUntil", null, now)
    setSetting(db, "turnsPausedReason", null, now)
  }
  const b = getSetting(db, "budgets")
  if (getUsage(db, usageKeys.runs(now, tz)) >= b.maxRunsPerHour) {
    return pauseTurns(db, nextHour(now, tz), `batas ${b.maxRunsPerHour} run per jam kena`, now)
  }
  if (getUsage(db, usageKeys.tokens(now, tz)) >= b.dailyTokenCap) {
    return pauseTurns(db, nextDay(now, tz), `batas ${b.dailyTokenCap.toLocaleString("id-ID")} token per hari kena`, now)
  }
  return { ok: true }
}

export function pauseTurns(db: DB, until: number, reason: string, now = Date.now()): BudgetCheck {
  setSetting(db, "turnsPausedUntil", until, now)
  setSetting(db, "turnsPausedReason", reason, now)
  return { ok: false, reason, until, fresh: true }
}

export function pausedNotice(db: DB, check: Extract<BudgetCheck, { ok: false }>): string {
  const tz = getSetting(db, "timezone")
  return `Turn lagi dijeda sampai ${formatClock(check.until, tz)}: ${check.reason}. Command kayak /status tetap jalan.`
}

// Per-job usage is keyed by the day in the job's own zone and written by the job
// runner (packages/core/src/jobs/runner.ts), which owns the per-job cap. A cap of 0
// (or less) means no cap, the same as the runner.
export function jobOverCap(db: DB, jobId: string, cap: number, now = Date.now(), tz = getSetting(db, "timezone")): boolean {
  if (!(cap > 0)) return false
  return getUsage(db, usageKeys.job(jobId, now, tz)) >= cap
}

// After a run: add tokens to the global daily counter. The job runner adds the
// same tokens to the per-job counter, so this never writes it (no double count).
export function recordRunTokens(db: DB, tokens: number, _jobId: string | undefined, now = Date.now()): void {
  const tz = getSetting(db, "timezone")
  addUsage(db, usageKeys.tokens(now, tz), tokens, now)
}

export function recordRunStart(db: DB, now = Date.now()): void {
  addUsage(db, usageKeys.runs(now, getSetting(db, "timezone")), 1, now)
}

export function tokensToday(db: DB, now = Date.now()): number {
  return getUsage(db, usageKeys.tokens(now, getSetting(db, "timezone")))
}
