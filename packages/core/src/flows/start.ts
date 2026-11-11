// Starting a flow run (RFC flows 7.1, 7.5): every entry point (the schedule
// fire, run_flow, a webhook, a chat command, an event) inserts its run here,
// keyed (flow, run_key) so a trigger fires at most once per key, with the hourly
// limits checked first. A skipped start is still a row, so "why didn't it run"
// always has an answer.
import { and, eq, gte, isNull, sql } from "drizzle-orm"
import { flowRuns, newId, type DB, type FlowRunStatus, type FlowSkipReason } from "@palui/db"
import { notifyScheduleChanged } from "../reminders/signal"
import type { FlowDefinition } from "./schema"
import type { RunVars } from "./scope"
import { currentVersion, definitionOf, getFlow, type FlowRow, type RunRow, type VersionRow } from "./service"
import type { Taint } from "./taint"
import { compactJson } from "./values"

export const LIMITS = {
  stepExecutionsPerRun: 100,
  // A check-in chain (ask, four growing reminders, a last one) is six asks.
  messagesPerRun: 10,
  runsPerFlowPerHour: 30,
  runsPerHour: 120,
  runningPerFlow: 3,
  flowDailyTokens: 50_000,
  runLifetimeMs: 8 * 24 * 3600_000,
  triggerBytes: 64 * 1024,
  varsBytes: 256 * 1024,
  stepIoBytes: 8 * 1024,
  flowStateBytes: 16 * 1024,
  autoPauseAfter: 5,
  chainDepth: 3,
}

export type StartInput = {
  flowId: string
  triggerId?: string | null
  triggerKind: string
  runKey: string
  payload: Record<string, unknown>
  taint?: Taint
  scheduledAt?: number | null
  now: number
  start?: string
}

export type InsertRunInput = StartInput & {
  flow: FlowRow
  version: VersionRow
  status?: FlowRunStatus
  skipReason?: FlowSkipReason | null
  error?: string | null
  parentRunId?: string | null
  itemIndex?: number | null
  vars?: RunVars
  dryRun?: boolean
}

function hourCount(db: DB, now: number, flowId?: string): number {
  const conds = [gte(flowRuns.startedAt, now - 3600_000), isNull(flowRuns.parentRunId), eq(flowRuns.dryRun, false), sql`${flowRuns.status} != 'skipped'`]
  if (flowId) conds.push(eq(flowRuns.flowId, flowId))
  return db.select({ n: sql<number>`count(*)` }).from(flowRuns).where(and(...conds)).get()?.n ?? 0
}

// Why a new run can't start right now, or null.
export function runLimitReason(db: DB, flow: FlowRow, def: FlowDefinition, now: number): FlowSkipReason | null {
  if (hourCount(db, now, flow.id) >= Math.min(def.limits?.runsPerHour ?? LIMITS.runsPerFlowPerHour, LIMITS.runsPerFlowPerHour)) return "rate_limited"
  if (hourCount(db, now) >= LIMITS.runsPerHour) return "rate_limited"
  return null
}

export function insertFlowRun(db: DB, input: InsertRunInput): RunRow | null {
  const def = definitionOf(input.version)
  let payload = input.payload
  if (Buffer.byteLength(compactJson(payload), "utf8") > LIMITS.triggerBytes) payload = { $truncated: true, preview: compactJson(payload).slice(0, 4000) }
  const status = input.status ?? "queued"
  const row: RunRow = {
    id: newId("flr"),
    flowId: input.flow.id,
    versionId: input.version.id,
    chatId: input.flow.chatId,
    triggerId: input.triggerId ?? null,
    triggerKind: input.triggerKind,
    runKey: input.runKey,
    parentRunId: input.parentRunId ?? null,
    itemIndex: input.itemIndex ?? null,
    status,
    skipReason: input.skipReason ?? null,
    trigger: payload,
    vars: (input.vars ?? { steps: {}, vars: {} }) as Record<string, unknown>,
    taint: input.taint ?? [],
    cursor: status === "queued" ? (input.start ?? def.triggers.find((t) => t.id === input.triggerId)?.start ?? def.steps[0]!.id) : null,
    wait: null,
    wakeAt: status === "queued" ? input.now : null,
    leaseUntil: null,
    stepsDone: 0,
    messagesSent: 0,
    scheduledAt: input.scheduledAt ?? null,
    startedAt: input.now,
    endedAt: status === "queued" ? null : input.now,
    error: input.error ?? null,
    legacyOccurrenceId: null,
    dryRun: !!input.dryRun,
    updatedAt: input.now,
  }
  const inserted = db.insert(flowRuns).values(row).onConflictDoNothing().returning().all()
  return inserted[0] ?? null
}

// A run of a flow, now: queued (the scheduler picks it up at once), or a skipped
// row saying why (paused, rate limited), or null on a duplicate key.
export function startFlowRun(db: DB, input: StartInput): { run: RunRow | null; skipped?: FlowSkipReason } {
  const flow = getFlow(db, input.flowId)
  if (!flow) return { run: null }
  const version = currentVersion(db, flow)
  const def = definitionOf(version)
  const reason: FlowSkipReason | null = !flow.enabled ? "paused" : runLimitReason(db, flow, def, input.now)
  const run = insertFlowRun(db, { ...input, flow, version, ...(reason ? { status: "skipped" as const, skipReason: reason } : {}) })
  if (run && !reason) notifyScheduleChanged()
  return { run, skipped: reason ?? (run ? undefined : "duplicate") }
}
