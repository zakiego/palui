// Test helpers shared by the engine, outbox and supervisor tests. Not imported
// by runtime code.
import { eq } from "drizzle-orm"
import { connectors, type Channel, type ConnectorKind, type DB, type OutboxPayload } from "@palui/db"
import type { ChannelHandle } from "../saklar/define"
import { upsertConnector } from "../saklar/store"

export const TEST_KEY = "test-master-key-test-master-key-1234"

export type FakeChannel = ChannelHandle & {
  sent: { to: string; payload: OutboxPayload }[]
  failNext: number
  up: boolean
  typingCalls: boolean[]
}

export function fakeChannel(channel: Channel): FakeChannel {
  let n = 0
  const ch: FakeChannel = {
    channel,
    sent: [],
    failNext: 0,
    up: true,
    typingCalls: [],
    async send(to, payload) {
      if (ch.failNext > 0) {
        ch.failNext--
        throw new Error(`${channel} send failed`)
      }
      ch.sent.push({ to, payload })
      return { channelMsgId: `${channel}-${++n}` }
    },
    async typing(_to, on) {
      ch.typingCalls.push(on)
    },
    reachable: () => ch.up,
  }
  return ch
}

// A connector row in the given state, bypassing the wizard.
export function seedConnector(
  db: DB,
  id: string,
  kind: ConnectorKind,
  opts: { type?: string; enabled?: boolean; status?: "connecting" | "on" | "paused" | "error" | "not_set_up"; config?: Record<string, unknown>; secrets?: Record<string, string>; trust?: "own" | "external" } = {},
  now = 1,
) {
  upsertConnector(
    db,
    { id, manifest: { id: opts.type ?? id, kind, name: id }, enabled: opts.enabled ?? true, config: opts.config, secrets: opts.secrets, trust: opts.trust },
    TEST_KEY,
    now,
  )
  db.update(connectors)
    .set({ status: opts.status ?? (opts.enabled === false ? "paused" : "on") })
    .where(eq(connectors.id, id))
    .run()
}

// --- Engine harness -------------------------------------------------------------------

import { APICallError, type Tool } from "ai"
import { MockLanguageModelV4 } from "ai/test"
import { z } from "zod"
import { memoryDb } from "@palui/db"
import { silentLogger } from "../log"
import { defineManifest, defineRuntime, type AnyRuntime, type InboundMessage, type MemoryHandle, type PaluiTool, type RunningConnector } from "../saklar/define"
import { createSupervisor } from "../saklar/supervisor"
import { createTurnBus } from "./bus"
import type { EngineDeps } from "./deps"
import { createEngine } from "./engine"

const usage = (n = 10) => ({
  inputTokens: { total: n, noCache: n, cacheRead: undefined, cacheWrite: undefined },
  outputTokens: { total: 5, text: 5, reasoning: undefined },
})

export type Chunk = Record<string, unknown>

function streamOf(chunks: Chunk[]) {
  return {
    stream: new ReadableStream({
      start(c) {
        for (const ch of chunks) c.enqueue(ch)
        c.close()
      },
    }),
  }
}

export const say = (text: string): Chunk[] => [
  { type: "text-start", id: "t" },
  { type: "text-delta", id: "t", delta: text },
  { type: "text-end", id: "t" },
  { type: "finish", finishReason: { unified: "stop", raw: undefined }, usage: usage() },
]

export const callTool = (toolName: string, input: unknown, toolCallId = `call-${Math.random().toString(36).slice(2, 8)}`): Chunk[] => [
  { type: "tool-call", toolCallId, toolName, input: JSON.stringify(input) },
  { type: "finish", finishReason: { unified: "tool-calls", raw: undefined }, usage: usage() },
]

// Several tool calls in ONE step (parallel calls), in this order.
export const callTools = (calls: { toolName: string; input: unknown; toolCallId: string }[]): Chunk[] => [
  ...calls.map((c) => ({ type: "tool-call" as const, toolCallId: c.toolCallId, toolName: c.toolName, input: JSON.stringify(c.input) })),
  { type: "finish", finishReason: { unified: "tool-calls", raw: undefined }, usage: usage() },
]

export const httpError = (statusCode: number) => new APICallError({ message: `HTTP ${statusCode}`, url: "http://x", requestBodyValues: {}, statusCode, isRetryable: false })

export type Step = Chunk[] | Error | (() => Promise<Chunk[]>)

// A mock model that plays steps in order; the prompts it saw are kept.
export function scriptedModel(steps: Step[], fallback: Step = say("ok")) {
  const prompts: unknown[] = []
  const model = new MockLanguageModelV4({
    doStream: async (options) => {
      prompts.push(options.prompt)
      const s = steps.length ? steps.shift()! : fallback
      if (s instanceof Error) throw s
      const chunks = typeof s === "function" ? await s() : s
      return streamOf(chunks) as never
    },
    doGenerate: async (options) => {
      prompts.push(options.prompt)
      const s = steps.length ? steps.shift()! : fallback
      if (s instanceof Error) throw s
      const chunks = typeof s === "function" ? await s() : s
      const text = chunks.filter((c) => c.type === "text-delta").map((c) => c.delta as string).join("")
      return { content: text ? [{ type: "text", text }] : [], finishReason: { unified: "stop", raw: undefined }, usage: usage(), warnings: [] } as never
    },
  })
  return { model, prompts, steps }
}

export function promptText(prompt: unknown): string {
  return JSON.stringify(prompt)
}

export function modelRuntime(id: string, model: () => MockLanguageModelV4): AnyRuntime {
  const m = defineManifest({ id, kind: "model", name: id, icon: "cpu", summary: "", config: z.object({}), secrets: {}, setup: [] })
  return defineRuntime(m, { test: async () => ({ ok: true, detail: "" }), start: async () => ({ stop: async () => {}, model: () => model() }) })
}

export function toolsRuntime(id: string, tools: () => Record<string, PaluiTool>, kind: "tools" | "skill" | "memory" = "tools", extra: Partial<RunningConnector> = {}): AnyRuntime {
  const m = defineManifest({ id, kind, name: id, icon: "wrench", summary: "", config: z.object({}), secrets: {}, setup: [] })
  return defineRuntime(m, { test: async () => ({ ok: true, detail: "" }), start: async () => ({ stop: async () => {}, tools: async () => tools(), ...extra }) })
}

export function channelRuntime(id: "telegram" | "whatsapp" | "web", handle: FakeChannel): AnyRuntime {
  const m = defineManifest({ id, kind: "channel", name: id, icon: "send", summary: "", config: z.object({}), secrets: {}, setup: [] })
  return defineRuntime(m, { test: async () => ({ ok: true, detail: "" }), start: async () => ({ stop: async () => {}, channel: handle }) })
}

export function memoryRuntime(handle: MemoryHandle): AnyRuntime {
  const m = defineManifest({ id: "recall", kind: "memory", name: "recall", icon: "brain", summary: "", config: z.object({}), secrets: {}, setup: [] })
  return defineRuntime(m, { test: async () => ({ ok: true, detail: "" }), start: async () => ({ stop: async () => {}, memory: handle }) })
}

export function fnTool(description: string, fn: (input: Record<string, unknown>) => unknown): Tool {
  return { description, inputSchema: z.object({}).passthrough(), execute: async (input: Record<string, unknown>) => fn(input) } as unknown as Tool
}

export type HarnessOptions = {
  db?: DB
  registry: Record<string, AnyRuntime>
  // Connector rows to seed (id -> kind); enabled and on.
  rows?: Record<string, ConnectorKind>
  deps?: EngineDeps
  approvalTtlMs?: number
  clock?: { now: number }
  utusanFile?: string
}

export async function harness(o: HarnessOptions) {
  const db = o.db ?? memoryDb()
  const clock = o.clock ?? { now: Date.UTC(2026, 10, 10, 6, 0, 0) }
  for (const [id, kind] of Object.entries(o.rows ?? {})) if (!db.select().from(connectors).where(eq(connectors.id, id)).get()) seedConnector(db, id, kind)
  const services = {} as import("../saklar/define").ConnectorServices
  let engineRef: ReturnType<typeof createEngine> | undefined
  const sup = createSupervisor({ db, registry: o.registry, services, log: silentLogger, masterKey: TEST_KEY, dataDir: "/tmp/palui-test", clock: () => clock.now, inUse: (id) => engineRef?.inUse(id) ?? false })
  const bus = createTurnBus()
  const engine = createEngine({ db, host: sup, log: silentLogger, clock: () => clock.now, bus, deps: o.deps, approvalTtlMs: o.approvalTtlMs, sendWaitMs: 20, utusanFile: o.utusanFile })
  engineRef = engine
  Object.assign(services, engine.services)
  await sup.reconcile()
  return { db, sup, engine, bus, clock }
}

let msgSeq = 0
export function tgMessage(text: string, extra: Partial<InboundMessage> = {}): InboundMessage {
  return { channel: "telegram", connectorId: "telegram", chatExternalId: "42", chatKind: "dm", senderExternalId: "42", channelMsgId: `m${++msgSeq}`, text, at: 0, ...extra }
}
