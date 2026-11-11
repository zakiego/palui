// A flow engine on an in-memory database with fake connectors and a settable
// clock, for tests (not imported by production code).
import { asc, eq } from "drizzle-orm"
import { flowRuns, flowStepRuns, outbox, type DB } from "@palui/db"
import { silentLogger } from "../log"
import { NOW, setup } from "../reminders/test-helpers"
import { localToUtc } from "../reminders/time"
import { connectorFlowCaller } from "./call"
import { createFlowEngine, type FlowEngineOptions, type PromptRequest, type PromptResponse } from "./engine"
import { createFlow, type SaveInput } from "./service"
import { fakeEntries, type CallLog, type FakeConnector } from "./test-entries"
import { EXAMPLE_TOOLS } from "./test-helpers"
import { resolverFrom } from "./validate"

export const WITA = "Asia/Makassar"
export const at = (local: string) => localToUtc(local, WITA)

// Connectors that answer like the examples' tools.
export function exampleConnectors(over: Partial<Record<string, FakeConnector["tools"][string]["run"]>> = {}): FakeConnector[] {
  const byConnector = new Map<string, FakeConnector>()
  for (const t of EXAMPLE_TOOLS) {
    const c = byConnector.get(t.connectorId) ?? { id: t.connectorId, type: t.connectorType, trust: t.untrusted && t.connectorType === "mcp" ? "external" : "own", tools: {} }
    c.tools[t.tool] = { class: t.class, untrusted: t.untrusted, flowApproval: t.flowApproval, run: over[t.name] ?? (() => ({ ok: true })) }
    byConnector.set(t.connectorId, c)
  }
  return [...byConnector.values()]
}

export function flowHarness(o: { connectors?: FakeConnector[]; engine?: Partial<FlowEngineOptions>; prompt?: (r: PromptRequest) => Promise<PromptResponse> | PromptResponse } = {}) {
  const { db, chatId, otherChatId } = setup()
  let t = NOW
  const calls: CallLog = []
  const entries = fakeEntries(o.connectors ?? exampleConnectors(), calls)
  const tools = connectorFlowCaller(() => entries)
  const prompts: PromptRequest[] = []
  const engine = createFlowEngine({
    db,
    log: silentLogger,
    now: () => t,
    tools,
    runPrompt: async (r) => {
      prompts.push(r)
      return o.prompt ? await o.prompt(r) : { status: "ok", text: "Semangat!", runId: r.turnRunId }
    },
    ...o.engine,
  })
  const run = async (when: number) => {
    t = when
    const n = engine.sweep(when)
    await engine.idle()
    // Runs woken by the first pass (a step queued another run) get one more.
    engine.sweep(when)
    await engine.idle()
    return n
  }
  const create = (definition: unknown, extra: Partial<SaveInput> = {}, now = t) =>
    createFlow(db, { chatId, definition, createdBy: "test", validate: { resolveTool: resolverFrom(EXAMPLE_TOOLS), hookExists: () => true }, ...extra }, now)
  return {
    db,
    chatId,
    otherChatId,
    engine,
    tools,
    calls,
    prompts,
    run,
    create,
    setNow: (x: number) => (t = x),
    now: () => t,
  }
}

export function sentTexts(db: DB) {
  return db
    .select()
    .from(outbox)
    .orderBy(asc(outbox.createdAt), asc(outbox.id))
    .all()
    .map((r) => ({ source: r.source, text: r.payload.text, actions: r.payload.actions?.map((a) => a.id) ?? [] }))
}

export function runsOf(db: DB, flowId: string) {
  return db.select().from(flowRuns).where(eq(flowRuns.flowId, flowId)).orderBy(asc(flowRuns.startedAt), asc(flowRuns.id)).all()
}

export function stepLog(db: DB, runId: string) {
  return db
    .select()
    .from(flowStepRuns)
    .where(eq(flowStepRuns.runId, runId))
    .orderBy(asc(flowStepRuns.seq))
    .all()
    .map((s) => ({ step: s.stepId, status: s.status, branch: s.branch, error: s.error }))
}

export { NOW }
