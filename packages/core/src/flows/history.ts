// Run history and the views the agent tools, the recap tool and the dashboard
// share: what a flow is, what its runs did, and counts by status and answer.
import { and, desc, eq, gte, inArray, isNotNull, isNull, lt, type SQL } from "drizzle-orm"
import { flowRuns, flowStepRuns, type DB, type FlowRunStatus } from "@palui/db"
import { formatWhen } from "../reminders/time"
import {
  currentVersion,
  definitionOf,
  describeTrigger,
  getVersion,
  listVersions,
  stepRunsOf,
  triggersOf,
  writesApproved,
  type FlowRow,
  type RunRow,
} from "./service"
import type { CheckinStep, FlowDefinition } from "./schema"
import { describeWriteCall, type WriteCall } from "./validate"

export type RunQuery = { chatId?: string; flowId?: string; since?: number; until?: number; status?: FlowRunStatus[]; triggerId?: string; limit?: number; topLevelOnly?: boolean }

export function listRuns(db: DB, q: RunQuery): RunRow[] {
  const conds: SQL[] = [eq(flowRuns.dryRun, false)]
  if (q.chatId) conds.push(eq(flowRuns.chatId, q.chatId))
  if (q.flowId) conds.push(eq(flowRuns.flowId, q.flowId))
  if (q.since !== undefined) conds.push(gte(flowRuns.startedAt, q.since))
  if (q.status?.length) conds.push(inArray(flowRuns.status, q.status))
  if (q.triggerId) conds.push(eq(flowRuns.triggerId, q.triggerId))
  if (q.topLevelOnly) conds.push(isNull(flowRuns.parentRunId))
  const rows = db
    .select()
    .from(flowRuns)
    .where(and(...conds))
    .orderBy(desc(flowRuns.startedAt), desc(flowRuns.id))
    .limit(q.limit ?? 500)
    .all()
  return q.until !== undefined ? rows.filter((r) => r.startedAt <= q.until!) : rows
}

// A check-in step's answer for a run, from its output (or its wait state while open).
export function checkinOf(run: RunRow): { stepId: string; answer: string | null; missed: boolean; open: boolean; label: string } | null {
  const vars = (run.vars ?? {}) as { steps?: Record<string, { answer?: string | null; missed?: boolean }> }
  const wait = run.wait as { kind?: string; stepId?: string; answers?: { answer: string }[] } | null
  const label = String((run.trigger as { label?: unknown })?.label ?? "")
  if (run.status === "waiting" && wait?.kind === "checkin" && wait.stepId) return { stepId: wait.stepId, answer: wait.answers?.at(-1)?.answer ?? null, missed: false, open: true, label }
  for (const [stepId, out] of Object.entries(vars.steps ?? {})) {
    if (out && typeof out === "object" && "answers" in out && "missed" in out) return { stepId, answer: out.answer ?? null, missed: !!out.missed, open: false, label }
  }
  return null
}

export type RunSummary = {
  total: number
  byStatus: Record<string, number>
  bySkipReason: Record<string, number>
  // Closed check-ins by final answer id.
  byAnswer: Record<string, number>
  missed: number
  open: number
  missedLabels: string[]
}

// Counts per status, skip reason and check-in answer.
export function summarizeRuns(rows: RunRow[]): RunSummary {
  const s: RunSummary = { total: 0, byStatus: {}, bySkipReason: {}, byAnswer: {}, missed: 0, open: 0, missedLabels: [] }
  for (const r of rows) {
    s.total++
    s.byStatus[r.status] = (s.byStatus[r.status] ?? 0) + 1
    if (r.skipReason) s.bySkipReason[r.skipReason] = (s.bySkipReason[r.skipReason] ?? 0) + 1
    const c = checkinOf(r)
    if (!c) continue
    if (c.open) s.open++
    else if (c.missed) {
      s.missed++
      if (c.label) s.missedLabels.push(c.label)
    } else if (c.answer) s.byAnswer[c.answer] = (s.byAnswer[c.answer] ?? 0) + 1
  }
  return s
}

// The recap text for a flow's runs (counts per answer, missed labels).
export function recapText(name: string, def: FlowDefinition, rows: RunRow[], days: number): string {
  const s = summarizeRuns(rows)
  const head = `📊 Rekap ${name}, ${days === 1 ? "24 jam terakhir" : days === 7 ? "7 hari terakhir" : `${days} hari terakhir`}:`
  const lines = [head]
  const checkin = def.steps.find((x): x is CheckinStep => x.kind === "checkin")
  const skipped = s.byStatus.skipped ?? 0
  if (checkin) {
    for (const a of checkin.answers) if (s.byAnswer[a.id]) lines.push(`- ${a.label}: ${s.byAnswer[a.id]}`)
    const counted = s.total - skipped - s.open
    const closed = Object.values(s.byAnswer).reduce((a, b) => a + b, 0)
    lines.push(`- Terlewat: ${s.missed}${s.missedLabels.length ? ` (${[...new Set([...s.missedLabels].reverse())].slice(0, 8).join(", ")})` : ""}`)
    if (s.open) lines.push(`- Masih nunggu jawaban: ${s.open}`)
    lines.push(`Selesai ${closed}/${counted}.`)
  } else {
    lines.push(`- Terkirim: ${s.byStatus.done ?? 0}`)
    if (s.byStatus.failed) lines.push(`- Gagal: ${s.byStatus.failed}`)
  }
  if (skipped) lines.push(`- Dilewati (Palui lagi mati): ${skipped}`)
  return lines.join("\n")
}

export function approvalState(db: DB, flow: FlowRow): "approved" | "waiting" | "not_needed" {
  const v = flow.currentVersionId ? getVersion(db, flow.currentVersionId) : undefined
  if (!v || !(v.writeCalls ?? []).length) return "not_needed"
  return writesApproved(v) ? "approved" : "waiting"
}

export function flowSummaryView(db: DB, flow: FlowRow, now: number) {
  const version = currentVersion(db, flow)
  const def = definitionOf(version)
  const rows = triggersOf(db, flow.id)
  const last = listRuns(db, { flowId: flow.id, limit: 1, topLevelOnly: true })[0]
  return {
    id: flow.id,
    name: flow.name,
    description: def.description ?? null,
    active: flow.enabled,
    pausedUntil: !flow.enabled && flow.pausedUntil ? formatWhen(flow.pausedUntil, flow.tz, now) : null,
    version: version.n,
    approval: approvalState(db, flow),
    triggers: [
      ...def.triggers.map((t) => {
        const row = rows.find((r) => r.triggerId === t.id)
        return { id: t.id, kind: t.kind, summary: describeTrigger(t), next: flow.enabled && row?.nextFireAt ? formatWhen(row.nextFireAt, flow.tz, now) : null, lastError: row?.lastError ?? null }
      }),
      { id: "manual", kind: "manual", summary: "jalankan_flow", next: null, lastError: null },
    ],
    lastRun: last ? { id: last.id, status: last.status, skipReason: last.skipReason, at: formatWhen(last.startedAt, flow.tz, now), error: last.error } : null,
    lastError: flow.lastError,
  }
}

export function flowDetailView(db: DB, flow: FlowRow, now: number, versionN?: number) {
  const all = listVersions(db, flow.id)
  const version = versionN ? all.find((v) => v.n === versionN) : all.find((v) => v.id === flow.currentVersionId)
  if (!version) return null
  return {
    ...flowSummaryView(db, flow, now),
    shownVersion: version.n,
    definition: version.definition,
    writeCalls: (version.writeCalls as unknown as WriteCall[]).map(describeWriteCall),
    versions: all.map((v) => ({ n: v.n, createdBy: v.createdBy, note: v.note, approved: v.approvedAt !== null || !(v.writeCalls ?? []).length, at: formatWhen(v.createdAt, flow.tz, now), current: v.id === flow.currentVersionId })),
    triggerState: triggersOf(db, flow.id).map((r) => ({ trigger: r.triggerId, kind: r.kind, cursor: r.cursor, nextFireAt: r.nextFireAt, lastError: r.lastError, sourceFailures: r.sourceState?.failures ?? 0, poll: r.kind === "poll" ? r.pollState : undefined })),
    state: flow.state?.values ?? {},
  }
}

export function runView(db: DB, run: RunRow, tz: string, now: number, withSteps = false) {
  const base = {
    id: run.id,
    status: run.status,
    skipReason: run.skipReason,
    trigger: run.triggerId ?? run.triggerKind,
    at: formatWhen(run.startedAt, tz, now),
    scheduledAt: run.scheduledAt ? formatWhen(run.scheduledAt, tz, now) : null,
    label: (run.trigger as { label?: string })?.label ?? null,
    endedAt: run.endedAt ? formatWhen(run.endedAt, tz, now) : null,
    error: run.error,
    ...(run.parentRunId ? { parent: run.parentRunId, item: run.itemIndex } : {}),
    ...(run.status === "waiting" && run.wait ? { waiting: (run.wait as { kind?: string }).kind, wakeAt: run.wakeAt ? formatWhen(run.wakeAt, tz, now) : null } : {}),
    checkin: checkinOf(run),
  }
  if (!withSteps) return base
  const children = db.select({ id: flowRuns.id, status: flowRuns.status, item: flowRuns.itemIndex }).from(flowRuns).where(eq(flowRuns.parentRunId, run.id)).all()
  return {
    ...base,
    triggerPayload: run.trigger,
    taint: run.taint,
    steps: stepRunsOf(db, run.id).map((s) => ({ seq: s.seq, step: s.stepId, kind: s.kind, status: s.status, branch: s.branch, input: s.input, output: s.output, error: s.error, tainted: s.tainted, ms: s.ms, attempt: s.attempt })),
    ...(children.length ? { children } : {}),
  }
}

export function stepRowsCount(db: DB, runId: string): number {
  return db.select({ id: flowStepRuns.id }).from(flowStepRuns).where(eq(flowStepRuns.runId, runId)).all().length
}

// flows__ringkasan (RFC 8): counts by answer, missed labels and the recap
// text for a flow over the last `days`, so a recap is a plain tool step.
export function flowRecap(db: DB, flow: FlowRow, input: { days: number; triggerId?: string | null }, now: number) {
  const def = definitionOf(currentVersion(db, flow))
  const rows = listRuns(db, { flowId: flow.id, since: now - input.days * 24 * 3600_000, triggerId: input.triggerId ?? undefined, limit: 5000 })
  const s = summarizeRuns(rows)
  return {
    flow: flow.name,
    days: input.days,
    total: s.total,
    byAnswer: s.byAnswer,
    byStatus: s.byStatus,
    missed: s.missed,
    open: s.open,
    missedLabels: [...new Set(s.missedLabels)].slice(0, 20),
    text: recapText(flow.name, def, rows, input.days),
  }
}

// Retention (RFC 3.5): step inputs and outputs go after 30 days (the rows and
// statuses stay), finished runs after 180, dry runs after 30.
export const STEP_IO_DAYS = 30
export const RUN_DAYS = 180

export function pruneFlows(db: DB, now = Date.now()): { io: number; runs: number; dry: number } {
  const day = 24 * 3600_000
  const io = db
    .update(flowStepRuns)
    .set({ input: null, output: null })
    .where(and(lt(flowStepRuns.startedAt, now - STEP_IO_DAYS * day), isNotNull(flowStepRuns.output)))
    .run().changes
  const old = (cutoff: number, dry: boolean) =>
    db
      .select({ id: flowRuns.id })
      .from(flowRuns)
      .where(and(eq(flowRuns.dryRun, dry), lt(flowRuns.startedAt, cutoff), inArray(flowRuns.status, ["done", "failed", "skipped", "cancelled"])))
      .limit(5000)
      .all()
      .map((r) => r.id)
  let runs = 0
  let dry = 0
  for (const [ids, isDry] of [
    [old(now - RUN_DAYS * day, false), false],
    [old(now - STEP_IO_DAYS * day, true), true],
  ] as const) {
    for (let i = 0; i < ids.length; i += 500) {
      const chunk = ids.slice(i, i + 500)
      db.delete(flowStepRuns).where(inArray(flowStepRuns.runId, chunk)).run()
      const n = db.delete(flowRuns).where(inArray(flowRuns.id, chunk)).run().changes
      if (isDry) dry += n
      else runs += n
    }
  }
  return { io, runs, dry }
}
