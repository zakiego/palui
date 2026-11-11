// Flow events (RFC flows 4.1): checkin.answered, checkin.missed, run.done and
// run.failed start the flows whose `event` trigger listens for them. A run
// started by an event carries trigger.depth; past 3 it's logged as skipped
// (chain_depth), and a flow can't listen to its own run.failed (checked at save).
import { and, eq } from "drizzle-orm"
import { flowRuns, flows, flowTriggers, newId, type DB } from "@palui/db"
import { notifyScheduleChanged } from "../reminders/signal"
import type { FlowEventName } from "./schema"
import { currentVersion, definitionOf, type RunRow } from "./service"

export const MAX_CHAIN_DEPTH = 3

export type FlowEventData = { step?: string | null; answer?: string | null; error?: string | null }

// Inserts one queued run per listening flow. Called inside the transaction that
// produced the event, so the event and its runs land together. Returns how many
// runs it started.
export function emitFlowEvent(db: DB, name: FlowEventName, src: RunRow, data: FlowEventData, now: number): number {
  if (src.dryRun) return 0
  const listeners = db
    .select()
    .from(flowTriggers)
    .where(and(eq(flowTriggers.kind, "event"), eq(flowTriggers.match, name)))
    .all()
  if (!listeners.length) return 0
  const source = db.select().from(flows).where(eq(flows.id, src.flowId)).get()
  if (!source) return 0
  const depth = Number((src.trigger as { depth?: unknown })?.depth ?? 0) + 1
  let n = 0
  for (const row of listeners) {
    const flow = db.select().from(flows).where(eq(flows.id, row.flowId)).get()
    if (!flow || flow.shadow || flow.chatId !== source.chatId) continue
    let version
    try {
      version = currentVersion(db, flow)
    } catch {
      continue
    }
    const def = definitionOf(version)
    const t = def.triggers.find((x) => x.id === row.triggerId)
    if (t?.kind !== "event") continue
    const target = t.flow === "self" ? source.id === flow.id : t.flow.toLowerCase() === source.name.toLowerCase()
    if (!target) continue
    if (t.flow === "self" && name === "run.failed") continue
    const payload = { kind: "event", event: name, flowId: source.id, flow: source.name, runId: src.id, step: data.step ?? null, answer: data.answer ?? null, error: data.error ?? null, label: (src.trigger as { label?: unknown })?.label ?? null, depth }
    const skip = depth > MAX_CHAIN_DEPTH ? "chain_depth" : !flow.enabled ? "paused" : null
    const inserted = db
      .insert(flowRuns)
      .values({
        id: newId("flr"),
        flowId: flow.id,
        versionId: version.id,
        chatId: flow.chatId,
        triggerId: t.id,
        triggerKind: "event",
        runKey: `${name}:${src.id}:${data.step ?? ""}`,
        status: skip ? "skipped" : "queued",
        skipReason: skip,
        trigger: payload,
        // The source run's data is as trusted as it was there.
        taint: (src.taint ?? []).length ? ["trigger"] : [],
        cursor: skip ? null : t.start,
        wakeAt: skip ? null : now,
        scheduledAt: null,
        startedAt: now,
        endedAt: skip ? now : null,
        updatedAt: now,
      })
      .onConflictDoNothing()
      .returning()
      .all()
    if (inserted.length && !skip) n++
  }
  if (n) notifyScheduleChanged()
  return n
}
