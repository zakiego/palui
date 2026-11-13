// Flows: create, edit (new immutable versions), pause, delete, approve, and the
// trigger rows derived from each version. The engine (engine.ts) runs them;
// this module owns the rows. Every write that moves a due time calls
// notifyScheduleChanged() so the scheduler re-arms at once.
import { getDefaultAgent } from "../agents"
import { findContact } from "../outbound/service"
import { createHash } from "node:crypto"
import { and, asc, desc, eq, inArray } from "drizzle-orm"
import { chats, flowRuns, flows, flowStepRuns, flowTriggers, flowVersions, newId, pendingApprovals, type DB, type SourceCursor } from "@palui/db"
import { getChat } from "../chats"
import { ownerChat } from "../connectors/rules"
import { logEvent } from "../events"
import { markResumed } from "../approvals/store"
import { claimHooksForFlow } from "./entry"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import { notifyScheduleChanged } from "../reminders/signal"
import { formatWhen, isValidZone } from "../reminders/time"
import { describeSource } from "./source-config"
import { upcoming, type Occurrence, type SourceInput } from "./sources"
import { getSetting } from "../settings"
import { canonicalJson, type FlowDefinition, type FlowTrigger, type ScheduleTrigger } from "./schema"
import { describeWriteCall, parseDefinition, sameWriteCalls, validateDefinition, type Problem, type ValidateContext, type WriteCall } from "./validate"

export type FlowRow = typeof flows.$inferSelect
export type VersionRow = typeof flowVersions.$inferSelect
export type TriggerRow = typeof flowTriggers.$inferSelect
export type RunRow = typeof flowRuns.$inferSelect
export type StepRunRow = typeof flowStepRuns.$inferSelect

export class FlowError extends Error {
  constructor(
    message: string,
    readonly problems: Problem[] = [],
  ) {
    super(message)
  }
}

export const MAX_FLOWS_PER_CHAT = 50
export const VERSION_APPROVAL_SOURCE = "flowver:"

// --- Reads --------------------------------------------------------------------------------

export function getFlow(db: DB, id: string): FlowRow | undefined {
  return db.select().from(flows).where(eq(flows.id, id)).get()
}

export function listFlows(db: DB, chatId?: string): FlowRow[] {
  const q = db.select().from(flows)
  return (chatId ? q.where(eq(flows.chatId, chatId)) : q).orderBy(asc(flows.createdAt)).all()
}

export function findFlowByName(db: DB, chatId: string, name: string): FlowRow | undefined {
  const n = name.trim().toLowerCase()
  return listFlows(db, chatId).find((f) => f.name.toLowerCase() === n)
}

// An id, or a name in this chat.
export function resolveFlow(db: DB, chatId: string, ref: string): FlowRow | undefined {
  const byId = getFlow(db, ref)
  if (byId && byId.chatId === chatId) return byId
  return findFlowByName(db, chatId, ref)
}

export function getVersion(db: DB, id: string): VersionRow | undefined {
  return db.select().from(flowVersions).where(eq(flowVersions.id, id)).get()
}

export function listVersions(db: DB, flowId: string): VersionRow[] {
  return db.select().from(flowVersions).where(eq(flowVersions.flowId, flowId)).orderBy(desc(flowVersions.n)).all()
}

export function getRun(db: DB, id: string): RunRow | undefined {
  return db.select().from(flowRuns).where(eq(flowRuns.id, id)).get()
}

export function stepRunsOf(db: DB, runId: string): StepRunRow[] {
  return db.select().from(flowStepRuns).where(eq(flowStepRuns.runId, runId)).orderBy(asc(flowStepRuns.seq)).all()
}

export function triggersOf(db: DB, flowId: string): TriggerRow[] {
  return db.select().from(flowTriggers).where(eq(flowTriggers.flowId, flowId)).all()
}

// Versions are immutable, so their parsed definitions are cached.
const defCache = new Map<string, FlowDefinition>()

export function definitionOf(v: Pick<VersionRow, "id" | "definition">): FlowDefinition {
  const hit = defCache.get(v.id)
  if (hit) return hit
  const r = parseDefinition(v.definition)
  if (!r.definition) throw new FlowError(`Version ${v.id} can't be read: ${r.problems.map((p) => `${p.path}: ${p.message}`).join("; ")}`)
  if (defCache.size > 500) defCache.clear()
  defCache.set(v.id, r.definition)
  return r.definition
}

export function currentVersion(db: DB, flow: FlowRow): VersionRow {
  const v = flow.currentVersionId ? getVersion(db, flow.currentVersionId) : undefined
  if (!v) throw new FlowError(`Flow "${flow.name}" has no current version.`)
  return v
}

export function hashDefinition(def: FlowDefinition): string {
  return createHash("sha256").update(canonicalJson(def)).digest("hex")
}

// --- Chats a flow may write to ------------------------------------------------------------

// "own": the flow's chat or one of the owner's DMs (or the web chat). "allowed": a
// chat the owner allowed (PLAN 19.2). false: anything else.
// Message steps with `contact`: a saved contact by name or id.
// Only the flow's own agent's contacts (PLAN 19); null is the default agent.
export function contactResolver(db: DB, agentId: string | null | undefined): (ref: string) => { id: string; name: string } | null {
  const agent = agentId ?? getDefaultAgent(db).id
  return (ref) => {
    const c = findContact(db, agent, ref)
    return c ? { id: c.id, name: c.name } : null
  }
}

export function chatAccess(db: DB, flowChatId: string): (chatId: string) => "own" | "allowed" | false {
  return (chatId) => {
    if (chatId === flowChatId) return "own"
    const chat = getChat(db, chatId)
    if (!chat) return false
    for (const ch of ["telegram", "whatsapp", "web"] as const) {
      if (ownerChat(db, ch)?.id === chat.id) return "own"
    }
    return chat.allowed ? "allowed" : false
  }
}

// --- Approval state -----------------------------------------------------------------------

// A version's write calls may run: it was approved, or it has none.
export function writesApproved(v: Pick<VersionRow, "approvedAt" | "writeCalls">): boolean {
  return v.approvedAt !== null || !(v.writeCalls ?? []).length
}

// --- Triggers -----------------------------------------------------------------------------

export function triggerMatch(t: FlowTrigger): string | null {
  switch (t.kind) {
    case "webhook":
      return t.hookId
    case "chat":
      return t.command ? t.command.toLowerCase() : "phrases"
    case "event":
      return t.event
    default:
      return null
  }
}

export function scheduleInput(t: ScheduleTrigger, tz: string, row: Pick<TriggerRow, "sourceState">): SourceInput {
  return { source: t.source, tz, days: t.days ?? null, state: row.sourceState ?? {} }
}

// The occurrence a schedule trigger fires next: after the cursor, but never more
// than a day in the past (a long downtime doesn't replay a week of runs).
export function nextScheduled(t: ScheduleTrigger, tz: string, row: Pick<TriggerRow, "sourceState" | "cursor">, now: number): Occurrence | null {
  const cursor = row.cursor ?? { at: now, key: "" }
  const floor = now - 24 * 3600_000
  const from: SourceCursor = cursor.at < floor ? { at: floor, key: "" } : cursor
  return upcoming(scheduleInput(t, tz, row), from, 1)[0] ?? null
}

// Trigger rows for a version. A trigger whose config didn't change keeps its
// state (cursor, cached times, poll hash); a changed one starts from now.
export function deriveTriggers(flow: Pick<FlowRow, "id" | "tz" | "enabled">, def: FlowDefinition, prev: TriggerRow[], now: number, prevDef?: FlowDefinition, tzChanged = false): TriggerRow[] {
  const out: TriggerRow[] = []
  for (const t of def.triggers) {
    if (t.kind === "manual") continue
    const old = prev.find((r) => r.triggerId === t.id && r.kind === t.kind)
    const before = prevDef?.triggers.find((x) => x.id === t.id)
    const same = !!old && !tzChanged && !!before && canonicalJson(before) === canonicalJson(t)
    const row: TriggerRow = {
      id: old?.id ?? newId("flt"),
      flowId: flow.id,
      triggerId: t.id,
      kind: t.kind,
      match: triggerMatch(t),
      status: "idle",
      nextFireAt: null,
      nextOcc: null,
      cursor: same ? (old!.cursor ?? { at: now, key: "" }) : { at: Math.max(now, old?.cursor?.at ?? 0), key: "" },
      sourceState: same ? (old!.sourceState ?? {}) : {},
      nextRefreshAt: null,
      pollState: same ? (old!.pollState ?? {}) : {},
      lastError: same ? (old!.lastError ?? null) : null,
      updatedAt: now,
    }
    if (t.kind === "schedule") {
      row.nextRefreshAt = t.source.kind === "tool" ? (same ? (old!.nextRefreshAt ?? now) : now) : null
      if (flow.enabled) {
        try {
          const occ = nextScheduled(t, flow.tz, row, now)
          row.nextOcc = occ
          row.nextFireAt = occ?.at ?? null
        } catch (e) {
          row.lastError = String((e as Error)?.message ?? e).slice(0, 300)
        }
      }
    }
    if (t.kind === "poll") row.nextFireAt = flow.enabled ? (same ? (old!.nextFireAt ?? now) : now) : null
    out.push(row)
  }
  return out
}

function writeTriggers(db: DB, flowId: string, rows: TriggerRow[]) {
  const keep = rows.map((r) => r.id)
  const existing = triggersOf(db, flowId)
  for (const r of existing) if (!keep.includes(r.id)) db.delete(flowTriggers).where(eq(flowTriggers.id, r.id)).run()
  for (const r of rows) {
    if (existing.some((e) => e.id === r.id)) db.update(flowTriggers).set(r).where(eq(flowTriggers.id, r.id)).run()
    else db.insert(flowTriggers).values(r).run()
  }
}

// --- Create and edit ----------------------------------------------------------------------

export type SaveInput = {
  chatId: string
  definition: unknown
  createdBy: string
  note?: string | null
  agentId?: string | null
  validate?: ValidateContext
}

export type SaveResult = {
  flow: FlowRow
  version: VersionRow
  changed: boolean
  needsApproval: boolean
  writeCalls: WriteCall[]
  diff: string[]
}

function check(db: DB, chatId: string, definition: unknown, ctx: ValidateContext | undefined, selfName?: string, agentId?: string | null) {
  const names = listFlows(db, chatId)
    .map((f) => f.name)
    .filter((n) => n !== selfName)
  const r = validateDefinition(definition, { chatId, chatAllowed: chatAccess(db, chatId), resolveContact: contactResolver(db, agentId), flowNames: [...names, ...(selfName ? [selfName] : [])], ...ctx })
  if (!r.ok || !r.definition) {
    const lines = r.problems.slice(0, 8).map((p) => `${p.path}: ${p.message}`)
    throw new FlowError(`The flow has ${r.problems.length} problem${r.problems.length === 1 ? "" : "s"}: ${lines.join(" | ")}`, r.problems)
  }
  return r
}

export function createFlow(db: DB, input: SaveInput, now = Date.now()): SaveResult {
  const r = check(db, input.chatId, input.definition, input.validate, undefined, input.agentId)
  const def = r.definition!
  if (!getChat(db, input.chatId)) throw new FlowError(`No chat ${input.chatId}.`)
  if (listFlows(db, input.chatId).length >= MAX_FLOWS_PER_CHAT) throw new FlowError(`This chat already has ${MAX_FLOWS_PER_CHAT} flows. Delete one first.`)
  const tz = def.tz ?? getSetting(db, "timezone")
  if (!isValidZone(tz)) throw new FlowError(`Unknown time zone "${tz}".`)
  if (findFlowByName(db, input.chatId, def.name)) throw new FlowError(`A flow named "${def.name}" already exists in this chat. Pick another name or change that one with update_flow.`)
  const flowId = newId("flw")
  const versionId = newId("flv")
  const needsApproval = r.writeCalls.length > 0
  const flow: FlowRow = {
    id: flowId,
    chatId: input.chatId,
    name: def.name,
    tz,
    enabled: true,
    pausedUntil: null,
    currentVersionId: versionId,
    approvedVersionId: needsApproval ? null : versionId,
    state: { values: {}, tainted: [] },
    consecutiveFailures: 0,
    agentId: input.agentId ?? null,
    legacyRoutineId: null,
    shadow: false,
    lastError: null,
    noticeFor: null,
    createdAt: now,
    updatedAt: now,
  }
  const version: VersionRow = {
    id: versionId,
    flowId,
    n: 1,
    definition: def as unknown as Record<string, unknown>,
    hash: hashDefinition(def),
    writeCalls: r.writeCalls as unknown as VersionRow["writeCalls"],
    createdBy: input.createdBy,
    note: input.note ?? null,
    approvedAt: needsApproval ? null : now,
    approvedVia: needsApproval ? null : "no_writes",
    createdAt: now,
  }
  db.transaction(() => {
    db.insert(flows).values(flow).run()
    db.insert(flowVersions).values(version).run()
    writeTriggers(db, flowId, deriveTriggers(flow, def, [], now))
    claimHooksForFlow(db, webhookIds(def), now)
    if (needsApproval) requestVersionApproval(db, flow, version, null, now)
  })
  logEvent(db, { kind: "flow.created", message: `Flow "${flow.name}" created (version 1${needsApproval ? ", waiting for approval" : ""}).`, data: { flowId, versionId } }, now)
  notifyScheduleChanged()
  return { flow, version, changed: true, needsApproval, writeCalls: r.writeCalls, diff: [] }
}

function webhookIds(def: FlowDefinition): string[] {
  return def.triggers.flatMap((t) => (t.kind === "webhook" ? [t.hookId] : []))
}

// RFC 7386 merge patch, plus one extension: when `steps` or `triggers` in the
// patch is an object instead of an array, it patches entries by id (null removes
// one, a new id appends it).
export function applyPatch(target: unknown, patch: unknown): unknown {
  if (!patch || typeof patch !== "object" || Array.isArray(patch)) return patch
  const out: Record<string, unknown> = target && typeof target === "object" && !Array.isArray(target) ? { ...(target as Record<string, unknown>) } : {}
  for (const [k, v] of Object.entries(patch as Record<string, unknown>)) {
    if ((k === "steps" || k === "triggers") && v && typeof v === "object" && !Array.isArray(v) && Array.isArray(out[k])) {
      let list = [...(out[k] as { id: string }[])]
      for (const [id, p] of Object.entries(v as Record<string, unknown>)) {
        const i = list.findIndex((x) => x.id === id)
        if (p === null) list = list.filter((x) => x.id !== id)
        else if (i >= 0) list[i] = applyPatch(list[i], p) as { id: string }
        else list.push({ id, ...(p as object) })
      }
      out[k] = list
    } else if (v === null) delete out[k]
    else out[k] = applyPatch(out[k], v)
  }
  return out
}

export type UpdateInput = { definition?: unknown; patch?: unknown; createdBy: string; note?: string | null; validate?: ValidateContext }

export function updateFlow(db: DB, id: string, input: UpdateInput, now = Date.now()): SaveResult {
  const flow = getFlow(db, id)
  if (!flow) throw new FlowError(`No flow ${id}.`)
  const cur = currentVersion(db, flow)
  const curDef = definitionOf(cur)
  if (!input.definition && !input.patch) throw new FlowError("Pass a definition or a patch.")
  const raw = input.definition ?? applyPatch(curDef, input.patch)
  const r = check(db, flow.chatId, raw, input.validate, flow.name, flow.agentId)
  const def = r.definition!
  const hash = hashDefinition(def)
  if (hash === cur.hash) return { flow, version: cur, changed: false, needsApproval: !writesApproved(cur), writeCalls: r.writeCalls, diff: [] }
  if (def.name.toLowerCase() !== flow.name.toLowerCase() && findFlowByName(db, flow.chatId, def.name)) throw new FlowError(`A flow named "${def.name}" already exists in this chat.`)
  const tz = def.tz ?? flow.tz
  if (!isValidZone(tz)) throw new FlowError(`Unknown time zone "${tz}".`)
  const approved = flow.approvedVersionId ? getVersion(db, flow.approvedVersionId) : undefined
  // Same write calls as the approved version: the approval carries over.
  const carries = !r.writeCalls.length || (!!approved && approved.approvedAt !== null && sameWriteCalls(approved.writeCalls as unknown as WriteCall[], r.writeCalls))
  const versionId = newId("flv")
  const version: VersionRow = {
    id: versionId,
    flowId: flow.id,
    n: cur.n + 1,
    definition: def as unknown as Record<string, unknown>,
    hash,
    writeCalls: r.writeCalls as unknown as VersionRow["writeCalls"],
    createdBy: input.createdBy,
    note: input.note ?? null,
    approvedAt: carries ? now : null,
    approvedVia: carries ? (r.writeCalls.length ? `carried:${approved!.id}` : "no_writes") : null,
    createdAt: now,
  }
  const next: FlowRow = { ...flow, name: def.name, tz, currentVersionId: versionId, approvedVersionId: carries ? versionId : flow.approvedVersionId, consecutiveFailures: 0, lastError: null, updatedAt: now }
  const diff = diffDefinitions(curDef, def)
  db.transaction(() => {
    db.insert(flowVersions).values(version).run()
    db.update(flows).set(next).where(eq(flows.id, flow.id)).run()
    writeTriggers(db, flow.id, deriveTriggers(next, def, triggersOf(db, flow.id), now, curDef, tz !== flow.tz))
    claimHooksForFlow(db, webhookIds(def), now)
    if (!carries) requestVersionApproval(db, next, version, approved ?? null, now)
  })
  logEvent(db, { kind: "flow.updated", message: `Flow "${next.name}" changed to version ${version.n}${carries ? "" : ", waiting for approval"}.`, data: { flowId: flow.id, versionId, n: version.n } }, now)
  notifyScheduleChanged()
  return { flow: next, version, changed: true, needsApproval: !carries, writeCalls: r.writeCalls, diff }
}

// Step-level changes, for the owner and the approval prompt.
export function diffDefinitions(a: FlowDefinition, b: FlowDefinition): string[] {
  const out: string[] = []
  const as = new Map(a.steps.map((s) => [s.id, canonicalJson(s)]))
  const bs = new Map(b.steps.map((s) => [s.id, canonicalJson(s)]))
  for (const id of bs.keys()) if (!as.has(id)) out.push(`+ step ${id}`)
  for (const id of as.keys()) if (!bs.has(id)) out.push(`- step ${id}`)
  for (const [id, j] of bs) if (as.has(id) && as.get(id) !== j) out.push(`~ step ${id}`)
  const at = new Map(a.triggers.map((t) => [t.id, canonicalJson(t)]))
  const bt = new Map(b.triggers.map((t) => [t.id, canonicalJson(t)]))
  for (const id of bt.keys()) if (!at.has(id)) out.push(`+ trigger ${id}`)
  for (const id of at.keys()) if (!bt.has(id)) out.push(`- trigger ${id}`)
  for (const [id, j] of bt) if (at.has(id) && at.get(id) !== j) out.push(`~ trigger ${id}`)
  if (a.name !== b.name) out.push(`~ name "${a.name}" -> "${b.name}"`)
  return out
}

// One prompt per version with write calls: the calls with their arg templates, and
// what changed since the last approved version.
export function requestVersionApproval(db: DB, flow: FlowRow, version: VersionRow, approved: VersionRow | null, now: number) {
  const chat = getChat(db, flow.chatId)
  if (!chat) return
  const calls = (version.writeCalls as unknown as WriteCall[]).map((w) => `- ${describeWriteCall(w)}`)
  const diff = approved ? diffDefinitions(definitionOf(approved), definitionOf(version)) : []
  const text = [
    `Flow "${flow.name}" versi ${version.n} mau jalanin aksi yang mengubah data tanpa nanya lagi tiap kali:`,
    ...calls,
    ...(diff.length ? ["", `Berubah dari versi ${approved!.n} yang disetujui: ${diff.slice(0, 10).join(", ")}`] : []),
    "",
    "Setujui? Balas ya atau batal (quote pesan ini). Sampai disetujui, bagian yang mengubah data nggak jalan; sisanya tetap jalan.",
  ].join("\n")
  enqueueOutbox(
    db,
    {
      chatId: chat.id,
      channel: chat.channel,
      connectorId: chat.connectorId,
      payload: { text, format: "plain", actions: [{ id: `flowver:approve:${version.id}`, label: "Setujui" }, { id: `flowver:deny:${version.id}`, label: "Batal" }] },
      source: `${VERSION_APPROVAL_SOURCE}${version.id}`,
      expiresAt: now + 24 * 3600_000,
    },
    now,
  )
  notifyOutbox()
}

export type ApproveResult = { ok: boolean; reply: string; flow?: FlowRow }

// The owner's answer to a version prompt. Runs that stopped at a write step of
// this version continue (approved) or are cancelled (denied).
export function decideVersion(db: DB, versionId: string, approved: boolean, via: string, now = Date.now()): ApproveResult {
  const v = getVersion(db, versionId)
  if (!v) return { ok: false, reply: "Versi flow itu nggak ketemu." }
  const flow = getFlow(db, v.flowId)
  if (!flow) return { ok: false, reply: "Flow-nya udah dihapus." }
  if (v.approvedAt !== null) return { ok: false, reply: `Versi ${v.n} flow "${flow.name}" udah disetujui.`, flow }
  if (flow.currentVersionId !== v.id) return { ok: false, reply: `Itu versi lama flow "${flow.name}"; yang jalan sekarang versi lain.`, flow }
  const waiting = db
    .select()
    .from(flowRuns)
    .where(and(eq(flowRuns.versionId, v.id), eq(flowRuns.status, "awaiting_approval")))
    .all()
  if (approved) {
    db.transaction(() => {
      db.update(flowVersions).set({ approvedAt: now, approvedVia: via }).where(eq(flowVersions.id, v.id)).run()
      db.update(flows).set({ approvedVersionId: v.id, updatedAt: now }).where(eq(flows.id, flow.id)).run()
      for (const r of waiting) {
        // A run that waited more than a day is stale: its moment passed.
        if (now - r.startedAt > 24 * 3600_000) db.update(flowRuns).set({ status: "cancelled", skipReason: null, error: "Approved more than a day after it stopped.", endedAt: now, wait: null, updatedAt: now }).where(eq(flowRuns.id, r.id)).run()
        // The wait stays, so the step updates the row it left waiting.
        else db.update(flowRuns).set({ status: "queued", skipReason: null, wakeAt: now, updatedAt: now }).where(eq(flowRuns.id, r.id)).run()
      }
    })
    logEvent(db, { kind: "flow.approved", message: `Flow "${flow.name}" version ${v.n} approved from ${via}.`, data: { flowId: flow.id, versionId: v.id } }, now)
    notifyScheduleChanged()
    return { ok: true, reply: `Sip, flow "${flow.name}" versi ${v.n} disetujui.${waiting.length ? ` ${waiting.length} run yang nunggu lanjut sekarang.` : ""}`, flow }
  }
  db.transaction(() => {
    db.update(flowVersions).set({ approvedVia: `denied:${via}` }).where(eq(flowVersions.id, v.id)).run()
    for (const r of waiting) db.update(flowRuns).set({ status: "cancelled", error: "The owner didn't approve this version.", endedAt: now, wait: null, updatedAt: now }).where(eq(flowRuns.id, r.id)).run()
  })
  logEvent(db, { kind: "flow.denied", message: `Flow "${flow.name}" version ${v.n} not approved.`, data: { flowId: flow.id, versionId: v.id } }, now)
  return { ok: true, reply: `Oke, versi ${v.n} flow "${flow.name}" nggak disetujui. Bagian yang mengubah data nggak akan jalan.`, flow }
}

// A flow run waiting on a per-run approval: the owner answered (or it expired),
// so it wakes now and its step reads the decision.
export function wakeFlowRun(db: DB, runId: string, now = Date.now()): void {
  db.update(flowRuns)
    .set({ wakeAt: now, updatedAt: now })
    .where(and(eq(flowRuns.id, runId), eq(flowRuns.status, "waiting")))
    .run()
  markResumed(db, runId, now)
  notifyScheduleChanged()
}

export function isFlowRunId(id: string): boolean {
  return id.startsWith("flr_")
}

// --- Pause and delete ---------------------------------------------------------------------

export function setFlowPaused(db: DB, id: string, paused: boolean, until: number | null, now = Date.now(), reason?: string): FlowRow {
  const flow = getFlow(db, id)
  if (!flow) throw new FlowError(`No flow ${id}.`)
  if (paused && until !== null && until <= now) throw new FlowError("The pause end must be in the future.")
  const next: FlowRow = { ...flow, enabled: !paused, pausedUntil: paused ? until : null, consecutiveFailures: paused ? flow.consecutiveFailures : 0, lastError: paused ? (reason ?? flow.lastError) : null, updatedAt: now }
  const def = definitionOf(currentVersion(db, flow))
  const rows = triggersOf(db, id).map((r) => {
    // Resuming starts from now: what was due while paused doesn't fire.
    const t = { ...r, cursor: paused ? r.cursor : { at: now, key: "" }, updatedAt: now }
    const spec = def.triggers.find((x) => x.id === r.triggerId)
    if (paused) return { ...t, nextFireAt: null, nextOcc: null }
    if (spec?.kind === "schedule") {
      if (spec.source.kind === "tool") t.nextRefreshAt = now
      const occ = nextScheduled(spec, flow.tz, t, now)
      return { ...t, nextOcc: occ, nextFireAt: occ?.at ?? null }
    }
    if (spec?.kind === "poll") return { ...t, nextFireAt: now }
    return t
  })
  db.transaction(() => {
    db.update(flows).set(next).where(eq(flows.id, id)).run()
    for (const r of rows) db.update(flowTriggers).set(r).where(eq(flowTriggers.id, r.id)).run()
  })
  if (paused) logEvent(db, { kind: "flow.paused", message: `Flow "${flow.name}" paused${until ? " until a set time" : ""}${reason ? `: ${reason}` : ""}.`, data: { flowId: id } }, now)
  notifyScheduleChanged()
  return next
}

export function deleteFlow(db: DB, id: string): void {
  db.transaction(() => {
    const runIds = db.select({ id: flowRuns.id }).from(flowRuns).where(eq(flowRuns.flowId, id)).all().map((r) => r.id)
    for (let i = 0; i < runIds.length; i += 500) {
      const chunk = runIds.slice(i, i + 500)
      db.delete(flowStepRuns).where(inArray(flowStepRuns.runId, chunk)).run()
      db.update(pendingApprovals).set({ status: "expired", decidedAt: Date.now() }).where(and(inArray(pendingApprovals.runId, chunk), eq(pendingApprovals.status, "pending"))).run()
    }
    db.delete(flowRuns).where(eq(flowRuns.flowId, id)).run()
    db.delete(flowTriggers).where(eq(flowTriggers.flowId, id)).run()
    db.delete(flowVersions).where(eq(flowVersions.flowId, id)).run()
    db.delete(flows).where(eq(flows.id, id)).run()
  })
  notifyScheduleChanged()
}

// --- Views --------------------------------------------------------------------------------

export function describeTrigger(t: FlowTrigger): string {
  switch (t.kind) {
    case "schedule":
      return describeSource(t.source, t.days ?? null)
    case "webhook":
      return `Webhook ${t.hookId}`
    case "chat":
      return t.command ? `Perintah ${t.command}` : `Pesan: ${(t.phrases ?? []).map((p) => JSON.stringify(p)).join(", ")}`
    case "poll":
      return `Cek ${t.connectorId ? `${t.connectorId}.` : ""}${t.tool} tiap ${t.everyMin} menit (${t.fire === "change" ? "kalau berubah" : "kalau ada isinya"})`
    case "event":
      return `Event ${t.event} dari ${t.flow === "self" ? "flow ini" : `flow "${t.flow}"`}`
    case "manual":
      return "Manual"
  }
}

// The next `n` times of every schedule trigger, merged.
export function previewTimes(db: DB, flow: FlowRow, n: number, now: number): { trigger: string; at: number; label: string; when: string }[] {
  const def = definitionOf(currentVersion(db, flow))
  const rows = triggersOf(db, flow.id)
  const out: { trigger: string; at: number; label: string; when: string }[] = []
  for (const t of def.triggers) {
    if (t.kind !== "schedule") continue
    const row = rows.find((r) => r.triggerId === t.id)
    try {
      for (const o of upcoming(scheduleInput(t, flow.tz, row ?? { sourceState: {} }), { at: now, key: "" }, n)) out.push({ trigger: t.id, at: o.at, label: o.label, when: formatWhen(o.at, flow.tz, now) })
    } catch {
      // A broken rule shows no times.
    }
  }
  return out.sort((a, b) => a.at - b.at).slice(0, n)
}

export function chatOfFlow(db: DB, flow: FlowRow) {
  return db.select().from(chats).where(eq(chats.id, flow.chatId)).get()
}
