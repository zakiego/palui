// Routines to flows (RFC flows 3.6, 11 phase 2, 12.6). The `routineMode`
// setting picks who sends:
//
//   legacy  the routine engine, and nothing is migrated (the way back)
//   shadow  the routine engine sends. Every routine also has a shadow flow that
//           refreshes and fires on its own but only records what it would have
//           started; shadowCompare() logs any difference from what the routine
//           engine actually did. This is the default after the upgrade.
//   flows   the flow engine sends. On the first sweep in this mode, handover()
//           copies each routine's schedule state (cursor, cached times, next
//           occurrence) and its occurrence log into its flow, so nothing fires
//           twice or gets skipped, and the routine engine stops.
//
// Everything here is idempotent: flows are keyed by flows.legacy_routine_id and
// runs by flow_runs.legacy_occurrence_id. The routine tables stay (they're
// dropped two releases later, after a pre-deploy snapshot).
import { and, eq, gt, gte, isNotNull, isNull, lte, sql } from "drizzle-orm"
import { events, flowRuns, flows, flowStepRuns, flowTriggers, newId, routineOccurrences, routines, type DB } from "@palui/db"
import { logEvent } from "../events"
import { notifyScheduleChanged } from "../reminders/signal"
import { formatClock, formatDate } from "../reminders/time"
import { configOf, type OccurrenceRow, type RoutineRow } from "../routines/service"
import { getSetting, setSetting } from "../settings"
import { MAIN_TRIGGER, RECAP_TRIGGER, routineToFlow } from "./convert"
import type { FlowDefinition } from "./schema"
import { createFlow, currentVersion, definitionOf, deleteFlow, findFlowByName, getFlow, hashDefinition, nextScheduled, setFlowPaused, triggersOf, updateFlow, type FlowRow, type RunRow } from "./service"
import { parseDefinition } from "./validate"

export type RoutineMode = "legacy" | "shadow" | "flows"

export function routineMode(db: DB): RoutineMode {
  const m = getSetting(db, "routineMode")
  return m === "legacy" || m === "flows" ? m : "shadow"
}

export function setRoutineMode(db: DB, mode: RoutineMode, now = Date.now()) {
  setSetting(db, "routineMode", mode, now)
  logEvent(db, { kind: "flow.routine_mode", message: `Routines now run on: ${mode === "flows" ? "the flow engine" : mode === "shadow" ? "the routine engine, with flows in shadow" : "the routine engine"}.`, data: { mode } }, now)
  notifyScheduleChanged()
}

export function flowForRoutine(db: DB, routineId: string): FlowRow | undefined {
  return db.select().from(flows).where(eq(flows.legacyRoutineId, routineId)).get()
}

// The run a migrated routine occurrence became (old rt: buttons, quoted replies).
export function runForLegacyOccurrence(db: DB, occurrenceId: string): RunRow | undefined {
  return db.select().from(flowRuns).where(eq(flowRuns.legacyOccurrenceId, occurrenceId)).get()
}

function definitionFor(r: RoutineRow, name: string): FlowDefinition | null {
  try {
    const def = parseDefinition({ ...routineToFlow(configOf(r)), name }).definition
    return def
  } catch {
    return null
  }
}

function uniqueName(db: DB, chatId: string, name: string): string {
  if (!findFlowByName(db, chatId, name)) return name
  for (let i = 1; i < 50; i++) {
    const n = `${name} (rutinitas${i > 1 ? ` ${i}` : ""})`.slice(0, 80)
    if (!findFlowByName(db, chatId, n)) return n
  }
  return `${name.slice(0, 60)} ${newId("r").slice(-6)}`
}

// The routine's schedule state, onto its flow's triggers, so the flow continues
// exactly where the routine was.
function copyState(db: DB, flow: FlowRow, r: RoutineRow, now: number) {
  const def = definitionOf(currentVersion(db, flow))
  db.update(flows).set({ enabled: r.enabled, pausedUntil: r.pausedUntil, updatedAt: now }).where(eq(flows.id, flow.id)).run()
  for (const row of triggersOf(db, flow.id)) {
    if (row.triggerId === MAIN_TRIGGER) {
      db.update(flowTriggers)
        .set({ status: "idle", cursor: r.cursor, nextOcc: r.enabled ? r.nextOcc : null, nextFireAt: r.enabled ? r.nextFireAt : null, sourceState: r.sourceState ?? {}, nextRefreshAt: r.nextRefreshAt, lastError: r.lastError, updatedAt: now })
        .where(eq(flowTriggers.id, row.id))
        .run()
    } else if (row.triggerId === RECAP_TRIGGER) {
      const spec = def.triggers.find((t) => t.id === RECAP_TRIGGER)
      const cursor = { at: (r.nextRecapAt ?? now) - 1, key: "" }
      const next = spec?.kind === "schedule" && r.enabled ? nextScheduled(spec, flow.tz, { cursor, sourceState: {} }, now) : null
      db.update(flowTriggers).set({ status: "idle", cursor, nextOcc: next, nextFireAt: next?.at ?? null, updatedAt: now }).where(eq(flowTriggers.id, row.id)).run()
    }
  }
}

export type SyncResult = { created: number; updated: number; removed: number; failed: number }

// Makes sure every routine has its flow with the current config: shadow flows in
// shadow mode, live ones in flows mode (only before handover finishes). Run by
// the app after the SQL migrations, and by the runtime while in shadow mode.
export function syncRoutineFlows(db: DB, now = Date.now()): SyncResult {
  const res: SyncResult = { created: 0, updated: 0, removed: 0, failed: 0 }
  const mode = routineMode(db)
  if (mode === "legacy") return res
  const all = db.select().from(routines).all()
  for (const r of all) {
    try {
      const flow = flowForRoutine(db, r.id)
      if (!flow) {
        if (mode === "flows" && handedOver(db)) continue
        const name = uniqueName(db, r.chatId, r.name)
        const def = definitionFor(r, name)
        if (!def) {
          res.failed++
          continue
        }
        const created = createFlow(db, { chatId: r.chatId, definition: def, createdBy: "migration", legacyRoutineId: r.id, shadow: true }, now)
        copyState(db, created.flow, r, now)
        res.created++
        continue
      }
      if (!flow.shadow) continue
      const def = definitionFor(r, flow.name)
      if (!def) continue
      if (hashDefinition(def) !== currentVersion(db, flow).hash) {
        updateFlow(db, flow.id, { definition: def, createdBy: "migration", note: "routine changed" }, now)
        copyState(db, getFlow(db, flow.id)!, r, now)
        res.updated++
      } else if (mirrorPause(db, flow, r, now)) res.updated++
    } catch (e) {
      res.failed++
      logEvent(db, { level: "warn", kind: "flow.migration_failed", message: `Routine "${r.name}" couldn't become a flow: ${(e as Error).message}`.slice(0, 500), data: { routineId: r.id } }, now)
    }
  }
  // Shadow flows whose routine was deleted go too.
  for (const f of db.select().from(flows).where(and(eq(flows.shadow, true), isNotNull(flows.legacyRoutineId))).all()) {
    if (!all.some((r) => r.id === f.legacyRoutineId)) {
      deleteFlow(db, f.id)
      res.removed++
    }
  }
  if (res.created || res.updated || res.removed) logEvent(db, { kind: "flow.migration", message: `Routines to flows: ${res.created} created, ${res.updated} updated, ${res.removed} removed${res.failed ? `, ${res.failed} failed` : ""}.`, data: res }, now)
  return res
}

// A routine paused or resumed (from chat or the Routines page): its flow follows,
// so a shadow flow doesn't observe occurrences the routine skips. True when it changed.
function mirrorPause(db: DB, flow: FlowRow, r: RoutineRow, now: number): boolean {
  const until = !r.enabled && r.pausedUntil && r.pausedUntil > now ? r.pausedUntil : null
  if (flow.enabled === r.enabled && (flow.enabled || (flow.pausedUntil ?? null) === until)) return false
  setFlowPaused(db, flow.id, !r.enabled, until, now)
  return true
}

// After the Routines page paused, resumed or deleted a routine: the same on its flow.
export function followRoutine(db: DB, routineId: string, now = Date.now()): void {
  const flow = flowForRoutine(db, routineId)
  if (!flow) return
  const r = db.select().from(routines).where(eq(routines.id, routineId)).get()
  if (!r) {
    deleteFlow(db, flow.id)
    return
  }
  mirrorPause(db, flow, r, now)
}

function handedOver(db: DB): boolean {
  return !db.select({ id: flows.id }).from(flows).where(eq(flows.shadow, true)).limit(1).get() && !!db.select({ id: flows.id }).from(flows).where(isNotNull(flows.legacyRoutineId)).limit(1).get()
}

// --- Occurrences to runs ------------------------------------------------------------------

const STATUS: Record<OccurrenceRow["status"], RunRow["status"]> = { open: "waiting", done: "done", missed: "done", sent: "done", skipped: "skipped", failed: "failed" }

export function migrateOccurrences(db: DB, flow: FlowRow, r: RoutineRow, now: number): number {
  const version = currentVersion(db, flow)
  const def = definitionOf(version)
  const stepId = def.triggers.find((t) => t.id === MAIN_TRIGGER)?.start ?? def.steps[0]!.id
  const step = def.steps.find((s) => s.id === stepId)
  const occs = db.select().from(routineOccurrences).where(eq(routineOccurrences.routineId, r.id)).all()
  let n = 0
  for (const o of occs) {
    if (runForLegacyOccurrence(db, o.id)) continue
    const status = STATUS[o.status]
    const payload = { kind: "schedule", id: MAIN_TRIGGER, at: o.scheduledAt, time: formatClock(o.scheduledAt, flow.tz), date: formatDate(o.scheduledAt, flow.tz, o.firedAt), label: o.label, key: o.key, fallback: o.fallback, nextAt: o.deadlineAt }
    const checkin = step?.kind === "checkin"
    const answerOf = (id: string | null) => (id && checkin ? (step.answers.find((a) => a.id === id)?.label ?? null) : null)
    const output = checkin
      ? { answer: o.answer, label: answerOf(o.answer), text: o.answers.at(-1)?.text ?? null, answeredAt: o.answeredAt, answers: o.answers, followUps: o.followUps, missed: o.status === "missed" }
      : { text: null, chatId: o.chatId, migrated: true }
    const runId = newId("flr")
    const open = o.status === "open" && checkin
    const wait = open ? { kind: "checkin", stepId, seq: 1, followUps: o.followUps, nextFollowUpAt: o.nextFollowUpAt, deadlineAt: o.deadlineAt, answers: o.answers, askedAt: o.firedAt } : null
    const wakeAt = open ? Math.min(o.nextFollowUpAt ?? Infinity, o.deadlineAt ?? Infinity) : null
    const inserted = db
      .insert(flowRuns)
      .values({
        id: runId,
        flowId: flow.id,
        versionId: version.id,
        chatId: flow.chatId,
        triggerId: MAIN_TRIGGER,
        triggerKind: "schedule",
        runKey: o.key,
        status,
        skipReason: o.status === "skipped" ? "late" : null,
        trigger: payload,
        vars: { steps: open || o.status === "skipped" ? {} : { [stepId]: output }, vars: {} },
        taint: [],
        cursor: open ? stepId : null,
        wait,
        wakeAt: wakeAt !== null && Number.isFinite(wakeAt) ? wakeAt : open ? now : null,
        stepsDone: open ? 0 : 1,
        messagesSent: o.status === "skipped" ? 0 : 1,
        scheduledAt: o.scheduledAt,
        startedAt: o.firedAt,
        endedAt: open ? null : o.updatedAt,
        error: o.status === "failed" ? "The routine's prompt turn failed." : null,
        legacyOccurrenceId: o.id,
        updatedAt: now,
      })
      .onConflictDoNothing()
      .returning()
      .all()
    if (!inserted.length) continue
    if (o.status !== "skipped") {
      db.insert(flowStepRuns)
        .values({ id: newId("fls"), runId, seq: 1, stepId, kind: step?.kind ?? "message", status: open ? "waiting" : o.status === "failed" ? "error" : "ok", output: open ? null : output, branch: o.status === "missed" ? "missed" : null, startedAt: o.firedAt })
        .run()
    }
    n++
  }
  return n
}

// The first sweep in flows mode: each shadow flow takes over from its routine.
export function handover(db: DB, now = Date.now()): number {
  if (routineMode(db) !== "flows") return 0
  const shadow = db.select().from(flows).where(eq(flows.shadow, true)).all()
  if (!shadow.length) return 0
  syncRoutineFlowsForHandover(db, now)
  let n = 0
  for (const f of db.select().from(flows).where(eq(flows.shadow, true)).all()) {
    const r = f.legacyRoutineId ? db.select().from(routines).where(eq(routines.id, f.legacyRoutineId)).get() : undefined
    db.transaction(() => {
      if (!r) {
        deleteFlow(db, f.id)
        return
      }
      copyState(db, f, r, now)
      migrateOccurrences(db, f, r, now)
      db.update(flows).set({ shadow: false, updatedAt: now }).where(eq(flows.id, f.id)).run()
      n++
    })
  }
  logEvent(db, { kind: "flow.handover", message: `Flows took over ${n} routine${n === 1 ? "" : "s"} from the routine engine.`, data: { count: n } }, now)
  notifyScheduleChanged()
  return n
}

// Before the handover: every routine has its (shadow) flow with the latest config.
function syncRoutineFlowsForHandover(db: DB, now: number) {
  for (const r of db.select().from(routines).all()) {
    const flow = flowForRoutine(db, r.id)
    if (flow) {
      const def = definitionFor(r, flow.name)
      if (def && flow.shadow && hashDefinition(def) !== currentVersion(db, flow).hash) updateFlow(db, flow.id, { definition: def, createdBy: "migration", note: "routine changed" }, now)
      continue
    }
    const name = uniqueName(db, r.chatId, r.name)
    const def = definitionFor(r, name)
    if (def) createFlow(db, { chatId: r.chatId, definition: def, createdBy: "migration", legacyRoutineId: r.id, shadow: true }, now)
  }
}

// --- Shadow compare -----------------------------------------------------------------------

export const SHADOW_SETTLE_MS = 10 * 60_000
// Shadow and routine occurrence times may differ this much (a refresh nudge).
export const SHADOW_TOLERANCE_MS = 60_000

export type ShadowResult = "match" | "time_differs" | "missing_in_routine" | "missing_in_flow" | "not_compared"

function diff(db: DB, flow: FlowRow, key: string, result: ShadowResult, at: number | null, routineAt: number | null, now: number) {
  logEvent(
    db,
    {
      level: "warn",
      kind: "flow.shadow_diff",
      message: `Shadow flow "${flow.name}" differs from its routine at ${key}: ${result.replaceAll("_", " ")}.`,
      data: { flowId: flow.id, routineId: flow.legacyRoutineId, key, result, flowAt: at, routineAt },
    },
    now,
  )
}

// Compares what each shadow flow would have started with what its routine did,
// once both settled (10 minutes). Differences become `flow.shadow_diff` events.
export function shadowCompare(db: DB, now = Date.now()): { compared: number; diffs: number } {
  let compared = 0
  let diffs = 0
  const until = now - SHADOW_SETTLE_MS
  const pending = db
    .select()
    .from(flowRuns)
    .where(and(eq(flowRuns.skipReason, "shadow"), isNull(flowRuns.endedAt), lte(flowRuns.scheduledAt, until)))
    .limit(500)
    .all()
  for (const run of pending) {
    const flow = getFlow(db, run.flowId)
    let result: ShadowResult = "not_compared"
    let routineAt: number | null = null
    if (flow?.legacyRoutineId && run.triggerId === MAIN_TRIGGER) {
      const key = run.runKey.slice("shadow:".length)
      const occ = db
        .select()
        .from(routineOccurrences)
        .where(and(eq(routineOccurrences.routineId, flow.legacyRoutineId), eq(routineOccurrences.key, key)))
        .get()
      routineAt = occ?.scheduledAt ?? null
      result = !occ ? "missing_in_routine" : Math.abs(occ.scheduledAt - (run.scheduledAt ?? 0)) <= SHADOW_TOLERANCE_MS ? "match" : "time_differs"
      if (result !== "match") {
        diffs++
        diff(db, flow, key, result, run.scheduledAt, routineAt, now)
      }
    }
    db.update(flowRuns)
      .set({ endedAt: now, vars: { steps: {}, vars: {}, shadow: { result, routineAt } } as Record<string, unknown>, updatedAt: now })
      .where(eq(flowRuns.id, run.id))
      .run()
    compared++
  }
  // The other way: occurrences the routine had that no shadow run matched.
  for (const flow of db.select().from(flows).where(and(eq(flows.shadow, true), isNotNull(flows.legacyRoutineId))).all()) {
    const row = triggersOf(db, flow.id).find((t) => t.triggerId === MAIN_TRIGGER)
    if (!row) continue
    const from = (row.pollState as { shadowCheckedUntil?: number }).shadowCheckedUntil ?? flow.createdAt
    if (until <= from) continue
    const occs = db
      .select()
      .from(routineOccurrences)
      .where(and(eq(routineOccurrences.routineId, flow.legacyRoutineId!), gt(routineOccurrences.scheduledAt, from), lte(routineOccurrences.scheduledAt, until)))
      .all()
    for (const o of occs) {
      const seen = db
        .select({ id: flowRuns.id })
        .from(flowRuns)
        .where(and(eq(flowRuns.flowId, flow.id), eq(flowRuns.runKey, `shadow:${o.key}`)))
        .get()
      if (!seen) {
        diffs++
        diff(db, flow, o.key, "missing_in_flow", null, o.scheduledAt, now)
      }
    }
    db.update(flowTriggers)
      .set({ pollState: { ...(row.pollState ?? {}), shadowCheckedUntil: until } as never })
      .where(eq(flowTriggers.id, row.id))
      .run()
  }
  return { compared, diffs }
}

export type ShadowReport = { mode: RoutineMode; flows: number; compared: number; matches: number; diffs: { flow: string; key: string; result: string; at: number }[] }

export function shadowReport(db: DB, since: number): ShadowReport {
  const shadowFlows = db.select().from(flows).where(isNotNull(flows.legacyRoutineId)).all()
  const rows = db
    .select({ vars: flowRuns.vars, flowId: flowRuns.flowId, key: flowRuns.runKey, at: flowRuns.scheduledAt })
    .from(flowRuns)
    .where(and(eq(flowRuns.skipReason, "shadow"), isNotNull(flowRuns.endedAt), sql`${flowRuns.startedAt} >= ${since}`))
    .all()
  const names = new Map(shadowFlows.map((f) => [f.id, f.name]))
  const out: ShadowReport = { mode: routineMode(db), flows: shadowFlows.filter((f) => f.shadow).length, compared: 0, matches: 0, diffs: [] }
  for (const r of rows) {
    const res = (r.vars as { shadow?: { result?: string } }).shadow?.result
    if (!res || res === "not_compared") continue
    out.compared++
    if (res === "match") out.matches++
    else out.diffs.push({ flow: names.get(r.flowId) ?? r.flowId, key: r.key.slice("shadow:".length), result: res, at: r.at ?? 0 })
  }
  // Occurrences only the routine had are events (there is no shadow run for them).
  for (const e of db.select().from(events).where(and(eq(events.kind, "flow.shadow_diff"), gte(events.at, since))).all()) {
    const d = (e.data ?? {}) as { flowId?: string; key?: string; result?: string; routineAt?: number }
    if (d.result !== "missing_in_flow") continue
    out.diffs.push({ flow: names.get(d.flowId ?? "") ?? d.flowId ?? "?", key: d.key ?? "?", result: d.result, at: d.routineAt ?? e.at })
  }
  return out
}
