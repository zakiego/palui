// Read-only views of flows for the dashboard (RFC flows 10): the list, one flow
// with its steps, versions and recent runs, and one run's step log. Flows are
// built and changed from chat; the dashboard only shows them.
import { and, desc, eq, isNull, sql } from "drizzle-orm"
import { chats, flowRuns, flows, flowStepRuns, type DB } from "@palui/db"
import { edgesOf } from "@palui/core/flows/graph"
import { approvalState, checkinOf } from "@palui/core/flows/history"
import type { FlowDefinition, FlowStep } from "@palui/core/flows/schema"
import { currentVersion, definitionOf, describeTrigger, diffDefinitions, getFlow, getRun, listVersions, triggersOf, type FlowRow } from "@palui/core/flows/service"
import { describeWriteCall, type WriteCall } from "@palui/core/flows/validate"

const DAY = 24 * 3600_000

// Pretty JSON for the step log, capped so one huge value can't swamp the page.
function json(v: unknown): string {
  let s: string
  try {
    s = JSON.stringify(v, null, 2) ?? "null"
  } catch {
    s = String(v)
  }
  return s.length > 6000 ? `${s.slice(0, 5999)}…` : s
}

function chatTitle(db: DB, chatId: string): string | null {
  return db.select({ title: chats.title }).from(chats).where(eq(chats.id, chatId)).get()?.title ?? null
}

function safeDef(db: DB, flow: FlowRow): FlowDefinition | null {
  try {
    return definitionOf(currentVersion(db, flow))
  } catch {
    return null
  }
}

export function flowList(db: DB, now = Date.now()) {
  const rows = db.select().from(flows).orderBy(flows.createdAt).all()
  const counts = new Map(
    db
      .select({ flowId: flowRuns.flowId, n: sql<number>`count(*)` })
      .from(flowRuns)
      .where(and(sql`${flowRuns.startedAt} >= ${now - 7 * DAY}`, eq(flowRuns.dryRun, false), isNull(flowRuns.parentRunId)))
      .groupBy(flowRuns.flowId)
      .all()
      .map((r) => [r.flowId, Number(r.n)]),
  )
  return rows.map((f) => {
    const def = safeDef(db, f)
    const triggers = triggersOf(db, f.id)
    const next = triggers
      .map((t) => t.nextFireAt)
      .filter((x): x is number => typeof x === "number")
      .sort((a, b) => a - b)[0]
    const last = db
      .select({ id: flowRuns.id, status: flowRuns.status, skipReason: flowRuns.skipReason, startedAt: flowRuns.startedAt, error: flowRuns.error })
      .from(flowRuns)
      .where(and(eq(flowRuns.flowId, f.id), eq(flowRuns.dryRun, false), isNull(flowRuns.parentRunId)))
      .orderBy(desc(flowRuns.startedAt))
      .limit(1)
      .get()
    return {
      id: f.id,
      name: f.name,
      description: def?.description ?? null,
      enabled: f.enabled,
      pausedUntil: f.pausedUntil,
      failures: f.consecutiveFailures,
      lastError: f.lastError,
      approval: approvalState(db, f),
      chatTitle: chatTitle(db, f.chatId),
      tz: f.tz,
      triggers: (def?.triggers ?? []).map((t) => ({ id: t.id, kind: t.kind, summary: describeTrigger(t) })),
      steps: def?.steps.length ?? 0,
      nextAt: f.enabled ? (next ?? null) : null,
      lastRun: last ?? null,
      runs7d: counts.get(f.id) ?? 0,
    }
  })
}

// One line per step for the vertical list: what it does, in plain words.
function stepSummary(s: FlowStep): string {
  switch (s.kind) {
    case "tool":
      return `${s.connectorId ? `${s.connectorId}.` : ""}${s.tool}`
    case "if":
      return s.when
    case "message":
      return s.text
    case "checkin":
      return s.text
    case "wait":
      return s.minutes !== undefined ? `${s.minutes} min` : `until ${s.until}${s.plusMin ? ` + ${s.plusMin} min` : ""}`
    case "prompt":
      return s.prompt
    case "set":
      return Object.entries(s.values)
        .map(([k, v]) => `${s.scope === "flow" ? "flow" : "vars"}.${k} = ${v}`)
        .join("; ")
    case "each":
      return `${s.items}${s.where ? ` where ${s.where}` : ""}`
  }
}

export function flowDetail(db: DB, id: string) {
  const f = getFlow(db, id)
  if (!f) return null
  const versions = listVersions(db, f.id)
  const current = versions.find((v) => v.id === f.currentVersionId) ?? versions[0]
  if (!current) return null
  let def: FlowDefinition | null = null
  try {
    def = definitionOf(current)
  } catch {
    def = null
  }
  const triggerRows = triggersOf(db, f.id)
  const starts = new Map<string, string[]>()
  for (const t of def?.triggers ?? []) starts.set(t.start, [...(starts.get(t.start) ?? []), t.id])
  const writeSteps = new Set((current.writeCalls as unknown as WriteCall[]).map((w) => w.stepId))
  const recent = db
    .select()
    .from(flowRuns)
    .where(and(eq(flowRuns.flowId, f.id), eq(flowRuns.dryRun, false)))
    .orderBy(desc(flowRuns.startedAt))
    .limit(30)
    .all()
  return {
    id: f.id,
    name: f.name,
    description: def?.description ?? null,
    enabled: f.enabled,
    pausedUntil: f.pausedUntil,
    failures: f.consecutiveFailures,
    lastError: f.lastError,
    approval: approvalState(db, f),
    chatId: f.chatId,
    chatTitle: chatTitle(db, f.chatId),
    tz: f.tz,
    createdAt: f.createdAt,
    version: current.n,
    // Flow variables as JSON text (values are data of any shape).
    state: Object.keys(f.state?.values ?? {}).length ? json(f.state.values) : null,
    triggers: (def?.triggers ?? []).map((t) => {
      const row = triggerRows.find((r) => r.triggerId === t.id)
      return { id: t.id, kind: t.kind, start: t.start, summary: describeTrigger(t), nextAt: f.enabled ? (row?.nextFireAt ?? null) : null, lastError: row?.lastError ?? null }
    }),
    steps: (def?.steps ?? []).map((s) => ({
      id: s.id,
      kind: s.kind,
      name: s.name ?? null,
      summary: stepSummary(s),
      write: writeSteps.has(s.id),
      startOf: starts.get(s.id) ?? [],
      // An if branch that ends the run shows as "else -> ends".
      edges: def ? [...edgesOf(def, s).map((e) => ({ label: e.label, to: e.to as string | null })), ...(s.kind === "if" ? [...(s.then === null ? [{ label: "then", to: null }] : []), ...(s.else === null ? [{ label: "else", to: null }] : [])] : [])] : [],
      ends: def ? !edgesOf(def, s).some((e) => e.label === "next" || e.label === "then" || e.label === "else") : true,
    })),
    writeCalls: (current.writeCalls as unknown as WriteCall[]).map(describeWriteCall),
    versions: versions.map((v, i) => {
      const prev = versions[i + 1]
      let diff: string[] = []
      if (prev) {
        try {
          diff = diffDefinitions(definitionOf(prev), definitionOf(v))
        } catch {
          diff = []
        }
      }
      return { n: v.n, createdBy: v.createdBy, note: v.note, approved: v.approvedAt !== null || !(v.writeCalls ?? []).length, writes: (v.writeCalls ?? []).length, createdAt: v.createdAt, current: v.id === f.currentVersionId, diff }
    }),
    runs: recent.map((r) => ({ id: r.id, status: r.status, skipReason: r.skipReason, trigger: r.triggerId ?? r.triggerKind, triggerKind: r.triggerKind, startedAt: r.startedAt, scheduledAt: r.scheduledAt, label: String((r.trigger as { label?: unknown })?.label ?? "") || null, error: r.error, child: !!r.parentRunId, answer: checkinOf(r)?.answer ?? null })),
  }
}

export function flowRunDetail(db: DB, id: string) {
  const r = getRun(db, id)
  if (!r || r.dryRun) return null
  const f = getFlow(db, r.flowId)
  const steps = db.select().from(flowStepRuns).where(eq(flowStepRuns.runId, r.id)).orderBy(flowStepRuns.seq).all()
  const children = db.select({ id: flowRuns.id, status: flowRuns.status, item: flowRuns.itemIndex }).from(flowRuns).where(eq(flowRuns.parentRunId, r.id)).all()
  return {
    id: r.id,
    flowId: r.flowId,
    flowName: f?.name ?? null,
    status: r.status,
    skipReason: r.skipReason,
    trigger: r.triggerId ?? r.triggerKind,
    triggerKind: r.triggerKind,
    runKey: r.runKey,
    startedAt: r.startedAt,
    endedAt: r.endedAt,
    scheduledAt: r.scheduledAt,
    wakeAt: r.status === "waiting" ? r.wakeAt : null,
    waiting: r.status === "waiting" ? ((r.wait as { kind?: string } | null)?.kind ?? null) : null,
    error: r.error,
    tainted: (r.taint ?? []).length > 0,
    taint: r.taint ?? [],
    parentRunId: r.parentRunId,
    payload: json(r.trigger),
    steps: steps.map((s) => ({ seq: s.seq, stepId: s.stepId, kind: s.kind, status: s.status, branch: s.branch, error: s.error, ms: s.ms, tainted: s.tainted, attempt: s.attempt, input: s.input == null ? null : json(s.input), output: s.output == null ? null : json(s.output), turnRunId: s.turnRunId })),
    children,
  }
}

export function flowsExist(db: DB): boolean {
  return !!db.select({ id: flows.id }).from(flows).limit(1).get()
}
