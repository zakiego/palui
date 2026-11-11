// Dry runs (RFC flows 7.6, uji_flow). A definition, saved or a draft, runs
// against a fake trigger payload: read tools are called for real (so the agent
// sees real output shapes), while write tools, messages, check-ins and prompts
// are stubbed and logged with what they would have sent. Waits are skipped and
// an each runs at most 3 children inline. Nothing is written except one flow_runs
// row with dry_run = true (hidden from the dashboard, pruned after 30 days) and
// its step log.
import { asc, eq } from "drizzle-orm"
import { flowRuns, flowStepRuns, newId, type DB } from "@palui/db"
import { formatClock, formatDate } from "../reminders/time"
import { upcoming } from "../routines/sources"
import type { FlowEngine, RunVars } from "./engine"
import { startOf } from "./graph"
import type { FlowDefinition } from "./schema"
import { currentVersion, definitionOf, scheduleInput, triggersOf, type FlowRow } from "./service"
import type { WriteCall } from "./validate"

export const DRY_EACH_LIMIT = 3

export type DryRunInput = {
  chatId: string
  // A saved flow, or a draft definition (validated by the caller).
  flow?: FlowRow
  definition?: FlowDefinition
  writeCalls?: WriteCall[]
  triggerId?: string | null
  // The fake trigger payload; a sample is built from the trigger when omitted.
  trigger?: Record<string, unknown>
  stubs?: Record<string, unknown>
  start?: string
  tz: string
  now: number
}

export type DryStep = { step: string; kind: string; status: string; branch: string | null; input: unknown; output: unknown; error: string | null; child?: number }

export type DryRunResult = { runId: string; status: string; error: string | null; steps: DryStep[]; vars: unknown; children: { index: number; status: string; steps: DryStep[] }[]; note?: string }

// A trigger payload that looks like the real one, for a dry run without one.
export function samplePayload(db: DB, def: FlowDefinition, flow: FlowRow | undefined, triggerId: string | null | undefined, tz: string, now: number): Record<string, unknown> {
  const t = triggerId ? def.triggers.find((x) => x.id === triggerId) : def.triggers[0]
  if (!t || t.kind === "manual") return { kind: "manual", args: {} }
  switch (t.kind) {
    case "schedule": {
      const row = flow ? triggersOf(db, flow.id).find((r) => r.triggerId === t.id) : undefined
      let occ: { at: number; key: string; label: string } | undefined
      try {
        occ = upcoming(scheduleInput(t, tz, row ?? { sourceState: {} }), { at: now - 1, key: "" }, 1)[0]
      } catch {
        occ = undefined
      }
      const at = occ?.at ?? now
      return { kind: "schedule", id: t.id, at, time: formatClock(at, tz), date: formatDate(at, tz, now), label: occ?.label ?? "Contoh", key: occ?.key ?? "dry", fallback: false, nextAt: null }
    }
    case "webhook":
      return { kind: "webhook", payload: {}, headers: {}, receivedAt: now }
    case "chat":
      return { kind: "chat", text: t.command ?? t.phrases?.[0] ?? "", args: "", messageId: "dry" }
    case "poll":
      return { kind: "poll", value: null, previous: null, checkedAt: now }
    case "event":
      return { kind: "event", event: t.event, flowId: flow?.id ?? "draft", runId: "dry", step: null, answer: null, error: null, depth: 1 }
  }
}

function stepsOf(db: DB, runId: string, child?: number): DryStep[] {
  return db
    .select()
    .from(flowStepRuns)
    .where(eq(flowStepRuns.runId, runId))
    .orderBy(asc(flowStepRuns.seq))
    .all()
    .map((s) => ({ step: s.stepId, kind: s.kind, status: s.status, branch: s.branch, input: s.input, output: s.output, error: s.error, ...(child !== undefined ? { child } : {}) }))
}

export async function dryRunFlow(db: DB, engine: FlowEngine, input: DryRunInput): Promise<DryRunResult> {
  const version = input.flow ? currentVersion(db, input.flow) : undefined
  const def = input.definition ?? (version ? definitionOf(version) : undefined)
  if (!def) throw new Error("Pass a saved flow or a definition.")
  const flowView = {
    id: input.flow?.id ?? `draft_${input.chatId}`,
    name: def.name,
    tz: def.tz ?? input.flow?.tz ?? input.tz,
    chatId: input.chatId,
    state: input.flow?.state ?? { values: {}, tainted: [] },
    agentId: input.flow?.agentId ?? null,
    enabled: true,
    shadow: false,
  }
  const versionView = { id: input.definition ? "draft" : version!.id, writeCalls: (input.writeCalls ?? (version?.writeCalls as unknown as WriteCall[]) ?? []) as never, approvedAt: null }
  const payload = input.trigger ?? samplePayload(db, def, input.flow, input.triggerId, flowView.tz, input.now)
  const start = input.start ?? startOf(def, input.triggerId)
  const triggerKind = (input.triggerId ? def.triggers.find((t) => t.id === input.triggerId)?.kind : def.triggers[0]?.kind) ?? "manual"
  const taint = triggerKind === "webhook" ? ["trigger"] : []
  const insert = (vars: RunVars, cursor: string, extra: { parentRunId?: string; itemIndex?: number; taint?: string[] } = {}) => {
    const id = newId("flr")
    db.insert(flowRuns)
      .values({
        id,
        flowId: flowView.id,
        versionId: versionView.id,
        chatId: input.chatId,
        triggerId: input.triggerId ?? def.triggers[0]?.id ?? null,
        triggerKind,
        runKey: `dry:${id}`,
        parentRunId: extra.parentRunId ?? null,
        itemIndex: extra.itemIndex ?? null,
        status: "running",
        trigger: payload,
        vars: vars as Record<string, unknown>,
        taint: extra.taint ?? taint,
        cursor,
        stepsDone: 0,
        messagesSent: 0,
        scheduledAt: typeof payload.at === "number" ? payload.at : null,
        startedAt: input.now,
        dryRun: true,
        updatedAt: input.now,
      })
      .run()
    return id
  }
  const runId = insert({ steps: {}, vars: {} }, start)
  const opts = { dry: true, stubs: input.stubs ?? {}, def, flow: flowView, version: versionView }
  const run = await engine.execute(runId, opts)
  const result: DryRunResult = { runId, status: run?.status ?? "failed", error: run?.error ?? null, steps: stepsOf(db, runId), vars: run?.vars ?? null, children: [] }
  // An each in a dry run: the first 3 children run inline, in order.
  for (const s of result.steps) {
    if (s.kind !== "each" || !s.output || typeof s.output !== "object") continue
    const items = ((s.output as { dryItems?: { item: unknown; index: number; taint?: boolean }[] }).dryItems ?? []).slice(0, DRY_EACH_LIMIT)
    const body = def.steps.find((x) => x.id === s.step)
    if (body?.kind !== "each") continue
    for (const it of items) {
      const vars = { ...((run?.vars ?? { steps: {}, vars: {} }) as RunVars), item: it.item, index: it.index }
      const childId = insert(vars, body.body, { parentRunId: runId, itemIndex: it.index, taint: it.taint ? [...taint, "item"] : taint })
      const child = await engine.execute(childId, opts)
      result.children.push({ index: it.index, status: child?.status ?? "failed", steps: stepsOf(db, childId, it.index) })
    }
  }
  return result
}
