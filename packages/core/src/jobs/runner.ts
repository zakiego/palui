// Runs scheduled jobs as turns (PLAN 7). The scheduler claims a due job and hands
// it to run(); the dashboard's Run now goes through runNow(). The engine's runTurn
// does the actual turn: it excludes destructive tools, asks approval for write
// tools on schedule triggers, and sends nothing when the reply is the silent token.
//
// This module owns the per-job daily token cap: before a run it reads the usage
// counter `job:<id>:<YYYY-MM-DD>` (date in the job's zone) and skips with
// last_status "cap" when it's at or over the cap; after a run it adds the run's
// tokens (from the `runs` row) to that counter.
import { and, eq, sql } from "drizzle-orm"
import { jobs, runs, usage, type DB } from "@palui/db"
import type { Logger } from "../log"
import { utcToLocal } from "../reminders/time"
import { SILENT_TOKEN } from "./presets"
import { getJob, type JobRow } from "./service"

export type RunTurn = (opts: {
  chatId: string
  trigger: "schedule"
  prompt: string
  jobId: string
  toolScope: string[] | null
  modelRole: "default" | "fast" | "vision"
  silentToken: "NOTHING"
}) => Promise<{ runId: string; text: string; status: string }>

export type JobRunResult = { ok: boolean; runId?: string; detail?: string; status: string }

export type JobRunner = {
  // Runs a job the scheduler already claimed (status running). Never throws.
  run(job: JobRow): Promise<JobRunResult>
  // Dashboard "Run now": claims the job itself, runs it even when paused, and
  // leaves next_run_at alone.
  runNow(jobId: string): Promise<{ ok: boolean; runId?: string; detail?: string }>
}

export const MAX_RESULT_CHARS = 500

export function jobUsageKey(job: Pick<JobRow, "id" | "tz">, now: number): string {
  return `job:${job.id}:${utcToLocal(now, job.tz).slice(0, 10)}`
}

export function getUsage(db: DB, key: string): number {
  return db.select().from(usage).where(eq(usage.key, key)).get()?.value ?? 0
}

export function addUsage(db: DB, key: string, n: number, now = Date.now()) {
  if (!n) return
  db.insert(usage)
    .values({ key, value: n, updatedAt: now })
    .onConflictDoUpdate({ target: usage.key, set: { value: sql`${usage.value} + ${n}`, updatedAt: now } })
    .run()
}

function truncate(s: string): string {
  return s.length > MAX_RESULT_CHARS ? `${s.slice(0, MAX_RESULT_CHARS - 1)}…` : s
}

export function createJobRunner(opts: { db: DB; log: Logger; runTurn: RunTurn; now?: () => number }): JobRunner {
  const { db, log, runTurn } = opts
  const clock = opts.now ?? (() => Date.now())

  function finish(jobId: string, set: Partial<JobRow>) {
    db.update(jobs)
      .set({ ...set, status: "idle", updatedAt: clock() })
      .where(eq(jobs.id, jobId))
      .run()
  }

  async function run(job: JobRow): Promise<JobRunResult> {
    const startedAt = clock()
    const key = jobUsageKey(job, startedAt)
    const used = getUsage(db, key)
    if (job.dailyTokenCap > 0 && used >= job.dailyTokenCap) {
      const detail = `Daily token cap reached (${used}/${job.dailyTokenCap}), skipped.`
      log.info("job skipped: daily token cap", { job_id: job.id, used, cap: job.dailyTokenCap })
      finish(job.id, { lastRunAt: startedAt, lastStatus: "cap", lastResult: detail })
      return { ok: false, status: "cap", detail }
    }
    try {
      const res = await runTurn({
        chatId: job.chatId,
        trigger: "schedule",
        prompt: job.prompt,
        jobId: job.id,
        toolScope: job.toolScope ?? null,
        modelRole: job.modelRole,
        silentToken: SILENT_TOKEN,
      })
      const run = db.select({ tokensIn: runs.tokensIn, tokensOut: runs.tokensOut }).from(runs).where(eq(runs.id, res.runId)).get()
      if (run) addUsage(db, key, run.tokensIn + run.tokensOut, clock())
      const silent = res.text.trim() === SILENT_TOKEN
      finish(job.id, { lastRunAt: startedAt, lastStatus: res.status, lastResult: truncate(silent ? `${SILENT_TOKEN} (nothing sent)` : res.text) })
      const ok = res.status === "ok" || res.status === "awaiting_approval"
      return { ok, runId: res.runId, status: res.status, detail: silent ? "Nothing to report." : undefined }
    } catch (e) {
      const msg = (e as Error)?.message ?? String(e)
      log.error("job run failed", { job_id: job.id, err: e as Error })
      finish(job.id, { lastRunAt: startedAt, lastStatus: "error", lastResult: truncate(msg) })
      return { ok: false, status: "error", detail: msg }
    }
  }

  return {
    run,
    async runNow(jobId) {
      const existing = getJob(db, jobId)
      if (!existing) return { ok: false, detail: `No job ${jobId}.` }
      const claimed = db
        .update(jobs)
        .set({ status: "running", updatedAt: clock() })
        .where(and(eq(jobs.id, jobId), eq(jobs.status, "idle")))
        .returning()
        .get()
      if (!claimed) return { ok: false, detail: "This job is already running." }
      const r = await run(claimed)
      return { ok: r.ok, runId: r.runId, detail: r.detail ?? (r.ok ? undefined : `Finished with status ${r.status}.`) }
    },
  }
}
