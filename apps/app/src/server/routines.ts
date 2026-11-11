// Routines for the dashboard: read views and the three writes (pause, resume,
// delete). Every rule lives in the routines service the agent tools use
// (packages/core/src/routines/service.ts); this file only shapes rows for the
// page and pokes the runtime so its scheduler re-arms at once.
//
// The page is generic on purpose: a routine is shown as trigger (its source),
// action and log, the same three parts a multi-step flow would have.
import { TimeError } from "@palui/core/reminders/time"
import type { ParsedConfig } from "@palui/core/routines/service"
import * as service from "@palui/core/routines/service"
import { checkinOf } from "@palui/core/flows/history"
import { followRoutine, routineMode, runForLegacyOccurrence } from "@palui/core/flows/migrate"
import { previewTimes } from "@palui/core/flows/service"
import { chats, connectors, flowRuns, flows, routineOccurrences, type DB, type RoutineOccurrenceStatus } from "@palui/db"
import { and, eq, inArray, sql } from "drizzle-orm"
import { DateTime } from "luxon"
import { runtime } from "./runtime"

export type RoutineRow = service.RoutineRow
export type OccurrenceRow = service.OccurrenceRow

export const OCCURRENCE_STATUSES: RoutineOccurrenceStatus[] = ["open", "done", "missed", "sent", "skipped", "failed"]
export const PREVIEW_COUNT = 8
export const DEFAULT_LOG_DAYS = 7
export const MAX_LOG_DAYS = 366
const LOG_LIMIT = 2000

function poke() {
  runtime()
    .reconcile()
    .catch(() => {})
}

// --- Shapes -------------------------------------------------------------------------

export type ChatRef = { id: string; title: string; channel: string } | null

// Where a tool source reads from, and whether it can right now.
export type SourceConnector = { id: string; label: string | null; state: "ok" | "missing" | "disabled" | "error"; detail: string | null }

function chatRefs(db: DB, ids: string[]): Map<string, NonNullable<ChatRef>> {
  if (!ids.length) return new Map()
  const rows = db.select({ id: chats.id, title: chats.title, externalId: chats.externalId, channel: chats.channel }).from(chats).where(inArray(chats.id, ids)).all()
  return new Map(rows.map((c) => [c.id, { id: c.id, title: c.title || c.externalId, channel: c.channel }]))
}

function sourceConnectors(db: DB, ids: string[]): Map<string, SourceConnector> {
  const out = new Map<string, SourceConnector>()
  const rows = ids.length ? db.select().from(connectors).where(inArray(connectors.id, ids)).all() : []
  for (const id of ids) {
    const c = rows.find((r) => r.id === id)
    if (!c) out.set(id, { id, label: null, state: "missing", detail: null })
    else if (!c.enabled || c.status === "paused" || c.status === "not_set_up") out.set(id, { id, label: c.label, state: "disabled", detail: c.statusDetail })
    else if (c.status === "error") out.set(id, { id, label: c.label, state: "error", detail: c.statusDetail })
    else out.set(id, { id, label: c.label, state: "ok", detail: null })
  }
  return out
}

// The flow each routine became (RFC flows 3.6): a shadow flow during the shadow
// week, the one that runs it after the handover.
export type FlowRef = { id: string; shadow: boolean } | null

function flowRefs(db: DB, ids: string[]): Map<string, { id: string; shadow: boolean; row: typeof flows.$inferSelect }> {
  if (!ids.length) return new Map()
  const rows = db.select().from(flows).where(inArray(flows.legacyRoutineId, ids)).all()
  return new Map(rows.map((f) => [f.legacyRoutineId!, { id: f.id, shadow: f.shadow, row: f }]))
}

// After the handover the routine row stands still: its next times come from its flow.
function flowNext(db: DB, flow: { shadow: boolean; row: typeof flows.$inferSelect } | undefined, n: number, now: number) {
  if (!flow || flow.shadow || !flow.row.enabled) return null
  try {
    return previewTimes(db, flow.row, n, now).map((t) => ({ at: t.at, label: t.label, fallback: false }))
  } catch {
    return null
  }
}

// A stored config that no longer parses (a schema change, a hand edit) still
// lists, with the problem in place of the schedule.
function safeConfig(r: RoutineRow): { cfg: ParsedConfig; problem: null } | { cfg: null; problem: string } {
  try {
    return { cfg: service.configOf(r), problem: null }
  } catch (e) {
    return { cfg: null, problem: e instanceof Error ? e.message : String(e) }
  }
}

function safePreview(r: RoutineRow, n: number, now: number): { at: number; label: string; fallback: boolean }[] {
  if (!r.enabled) return []
  try {
    return service.previewOccurrences(r, n, now).map((o) => ({ at: o.at, label: o.label, fallback: !!o.fallback }))
  } catch (e) {
    if (e instanceof TimeError || e instanceof service.RoutineError) return []
    throw e
  }
}

// After the handover an open check-in lives on as a flow run (RFC flows 3.6);
// the routine row's copy stays "open" forever, so the run says what it is now.
function viaFlow(db: DB, o: OccurrenceRow): Pick<OccurrenceRow, "status" | "answer" | "followUps" | "nextFollowUpAt" | "deadlineAt"> {
  if (o.status !== "open") return o
  const run = runForLegacyOccurrence(db, o.id)
  if (!run) return o
  const c = checkinOf(run)
  const wait = run.wait as { followUps?: number; nextFollowUpAt?: number | null; deadlineAt?: number | null } | null
  if (!c || c.open) return { status: "open", answer: c?.answer ?? o.answer, followUps: wait?.followUps ?? o.followUps, nextFollowUpAt: wait?.nextFollowUpAt ?? null, deadlineAt: wait?.deadlineAt ?? o.deadlineAt }
  return { status: c.missed ? "missed" : "done", answer: c.answer, followUps: o.followUps, nextFollowUpAt: null, deadlineAt: o.deadlineAt }
}

function openCounts(db: DB, ids: string[], flowIds: Map<string, { id: string; shadow: boolean }> = new Map()): Map<string, number> {
  if (!ids.length) return new Map()
  const live = ids.filter((id) => flowIds.get(id) && !flowIds.get(id)!.shadow)
  const out = new Map<string, number>()
  // Handed over: the flow's waiting check-ins, migrated and new alike.
  for (const id of live) out.set(id, openFlowCheckinsOf(db, flowIds.get(id)!.id))
  const rest = ids.filter((id) => !live.includes(id))
  if (!rest.length) return out
  const rows = db
    .select({ id: routineOccurrences.routineId, n: sql<number>`count(*)` })
    .from(routineOccurrences)
    .where(and(inArray(routineOccurrences.routineId, rest), eq(routineOccurrences.status, "open")))
    .groupBy(routineOccurrences.routineId)
    .all()
  for (const r of rows) out.set(r.id, Number(r.n))
  return out
}

function openFlowCheckinsOf(db: DB, flowId: string): number {
  return db
    .select({ wait: flowRuns.wait })
    .from(flowRuns)
    .where(and(eq(flowRuns.flowId, flowId), eq(flowRuns.status, "waiting")))
    .all()
    .filter((r) => (r.wait as { kind?: string } | null)?.kind === "checkin").length
}

function summaryOf(r: RoutineRow, cfg: ParsedConfig | null, chat: ChatRef, connector: SourceConnector | null, openCheckins: number, now: number, flow: FlowRef = null, next?: { at: number; label: string; fallback: boolean }[] | null) {
  const source = cfg?.source ?? (r.source as { kind?: string })
  const action = cfg?.action ?? (r.action as { kind?: string })
  return {
    id: r.id,
    name: r.name,
    tz: r.tz,
    chat,
    chatId: r.chatId,
    active: r.enabled,
    pausedUntil: r.enabled ? null : r.pausedUntil,
    // The same words daftar_rutinitas shows the owner in chat.
    schedule: cfg ? service.describeSchedule(r, cfg) : null,
    configProblem: cfg ? null : "invalid",
    sourceKind: String(source.kind ?? "unknown"),
    actionKind: String(action.kind ?? "unknown"),
    recap: cfg?.recap ?? null,
    next: next ?? safePreview(r, 2, now),
    lastError: r.lastError,
    flow,
    connector,
    openCheckins,
    createdAt: r.createdAt,
    updatedAt: r.updatedAt,
  }
}

export type RoutineSummary = ReturnType<typeof summaryOf>

// --- Reads --------------------------------------------------------------------------

// Every routine in every chat (Palui has one owner; the session is the boundary).
export function routineList(db: DB, now = Date.now()): RoutineSummary[] {
  const rows = service.listRoutines(db)
  const ids = rows.map((r) => r.id)
  const chatMap = chatRefs(db, [...new Set(rows.map((r) => r.chatId))])
  const parsed = rows.map((r) => ({ r, ...safeConfig(r) }))
  const toolIds = [...new Set(parsed.flatMap(({ cfg }) => (cfg?.source.kind === "tool" ? [cfg.source.connectorId] : [])))]
  const conns = sourceConnectors(db, toolIds)
  const flowMap = flowRefs(db, ids)
  const open = openCounts(db, ids, flowMap)
  return parsed.map(({ r, cfg }) => {
    const f = flowMap.get(r.id)
    return summaryOf(r, cfg, chatMap.get(r.chatId) ?? null, cfg?.source.kind === "tool" ? (conns.get(cfg.source.connectorId) ?? null) : null, open.get(r.id) ?? 0, now, f ? { id: f.id, shadow: f.shadow } : null, flowNext(db, f, 2, now))
  })
}

// A log window as local dates in the routine's zone, both ends included.
// Tool args are free-form JSON; the page shows each value as text.
function viewSource(source: ParsedConfig["source"]) {
  if (source.kind !== "tool") return source
  const args = Object.fromEntries(Object.entries(source.args).map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)]))
  return { ...source, args }
}

export type LogRange = { from: string; to: string }

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/

// The window to read: the dates asked for, or the last 7 days (today included).
// Swapped ends are put in order and the span is capped at a year.
export function logWindow(tz: string, range: Partial<LogRange> = {}, now = Date.now()) {
  const today = DateTime.fromMillis(now, { zone: tz }).startOf("day")
  const parse = (d: string | undefined, fallback: DateTime) => {
    const v = d && ISO_DATE.test(d) ? DateTime.fromISO(d, { zone: tz }) : null
    return v?.isValid ? v.startOf("day") : fallback
  }
  let from = parse(range.from, today.minus({ days: DEFAULT_LOG_DAYS - 1 }))
  let to = parse(range.to, today)
  if (from > to) [from, to] = [to, from]
  if (to.diff(from, "days").days > MAX_LOG_DAYS) from = to.minus({ days: MAX_LOG_DAYS })
  return { from: from.toISODate() as string, to: to.toISODate() as string, since: from.toMillis(), until: to.plus({ days: 1 }).toMillis() }
}

// Per-item tally for check-ins: how many of each label closed, out of how many
// that counted (open and skipped ones don't count, like the recap).
export function labelTally(rows: OccurrenceRow[]): { label: string; done: number; counted: number }[] {
  const by = new Map<string, { label: string; done: number; counted: number }>()
  // Oldest first, so labels keep the order they fire in through the day.
  for (const o of [...rows].sort((a, b) => a.scheduledAt - b.scheduledAt)) {
    if (o.status === "open" || o.status === "skipped") continue
    const t = by.get(o.label) ?? { label: o.label, done: 0, counted: 0 }
    t.counted++
    if (o.status === "done") t.done++
    by.set(o.label, t)
  }
  return [...by.values()]
}

export function routineDetail(db: DB, id: string, range: Partial<LogRange> = {}, now = Date.now()) {
  const r = service.getRoutine(db, id)
  if (!r) return null
  const { cfg, problem } = safeConfig(r)
  const chat = chatRefs(db, [r.chatId]).get(r.chatId) ?? null
  const connector = cfg?.source.kind === "tool" ? (sourceConnectors(db, [cfg.source.connectorId]).get(cfg.source.connectorId) ?? null) : null
  const win = logWindow(r.tz, range, now)
  const f = flowRefs(db, [r.id]).get(r.id)
  const live = !!f && !f.shadow
  const rows = service.occurrencesSince(db, { routineId: id, since: win.since, until: win.until, limit: LOG_LIMIT }).map((o) => (live ? { ...o, ...viaFlow(db, o) } : o))
  const open = rows.filter((o) => o.status === "open")
  const counts = Object.fromEntries(OCCURRENCE_STATUSES.map((s) => [s, 0])) as Record<RoutineOccurrenceStatus, number>
  for (const o of rows) counts[o.status] = (counts[o.status] ?? 0) + 1
  const answerLabels = cfg?.action.kind === "checkin" ? Object.fromEntries(cfg.action.answers.map((a) => [a.id, a.label])) : {}
  const fromFlow = flowNext(db, f, PREVIEW_COUNT, now)
  return {
    routine: summaryOf(r, cfg, chat, connector, live ? openFlowCheckinsOf(db, f!.id) : open.length, now, f ? { id: f.id, shadow: f.shadow } : null, fromFlow?.slice(0, 2)),
    routineMode: routineMode(db),
    config: cfg ? { tz: cfg.tz, days: cfg.days, source: viewSource(cfg.source), action: cfg.action, recap: cfg.recap, lateGraceMin: cfg.lateGraceMin } : null,
    configProblem: problem,
    // The row as stored, for the raw JSON view.
    rawJson: JSON.stringify({ id: r.id, chatId: r.chatId, name: r.name, tz: r.tz, days: r.days, source: r.source, action: r.action, recap: r.recap, lateGraceMin: r.lateGraceMin, enabled: r.enabled, pausedUntil: r.pausedUntil }, null, 2),
    nextFireAt: r.nextFireAt,
    // The engine moves the stored time after each recap; until it does (or after
    // downtime) it can be in the past, so the next one is worked out from now.
    nextRecapAt: r.enabled && cfg ? (r.nextRecapAt && r.nextRecapAt > now ? r.nextRecapAt : service.nextRecapAt(cfg.recap, r.tz, now)) : null,
    preview: fromFlow ?? safePreview(r, PREVIEW_COUNT, now),
    range: { from: win.from, to: win.to },
    log: rows.map((o) => ({
      id: o.id,
      label: o.label,
      status: o.status,
      scheduledAt: o.scheduledAt,
      firedAt: o.firedAt,
      answer: o.answer,
      answerLabel: o.answer ? (answerLabels[o.answer] ?? o.answer) : null,
      answeredAt: o.answeredAt,
      // The owner's own words, when the agent kept them (jawab_checkin's note).
      note: [...(o.answers ?? [])].reverse().find((a) => a.text)?.text ?? null,
      reAsks: o.followUps,
      nextFollowUpAt: o.nextFollowUpAt,
      deadlineAt: o.deadlineAt,
      fallback: o.fallback,
    })),
    truncated: rows.length >= LOG_LIMIT,
    // Every log entry, not just this range: deleting the routine removes them all.
    logTotal: Number(db.select({ n: sql<number>`count(*)` }).from(routineOccurrences).where(eq(routineOccurrences.routineId, id)).get()?.n ?? 0),
    counts,
    tally: cfg?.action.kind === "checkin" ? labelTally(rows) : [],
    open: open.map((o) => ({ id: o.id, label: o.label, scheduledAt: o.scheduledAt, reAsks: o.followUps, nextFollowUpAt: o.nextFollowUpAt, deadlineAt: o.deadlineAt })),
  }
}

export type RoutineDetail = NonNullable<ReturnType<typeof routineDetail>>

// --- Writes -------------------------------------------------------------------------

export class RoutineNotFoundError extends Error {
  constructor() {
    super("That routine doesn't exist anymore.")
  }
}

// `until` is a wall-clock time in the routine's own zone (YYYY-MM-DDTHH:mm),
// like jeda_rutinitas takes it.
export function pauseRoutine(db: DB, id: string, until: string | null, now = Date.now()): RoutineRow {
  const r = service.getRoutine(db, id)
  if (!r) throw new RoutineNotFoundError()
  const at = until ? service.localPausedUntil(until, r.tz) : null
  const row = service.setPaused(db, id, true, at, now)
  // Its flow follows: after the handover the flow is what runs.
  followRoutine(db, id, now)
  poke()
  return row
}

export function resumeRoutine(db: DB, id: string, now = Date.now()): RoutineRow {
  if (!service.getRoutine(db, id)) throw new RoutineNotFoundError()
  const row = service.setPaused(db, id, false, null, now)
  followRoutine(db, id, now)
  poke()
  return row
}

// Deletes the routine and its whole log.
export function removeRoutine(db: DB, id: string): void {
  if (!service.getRoutine(db, id)) throw new RoutineNotFoundError()
  service.deleteRoutine(db, id)
  followRoutine(db, id)
  poke()
}
