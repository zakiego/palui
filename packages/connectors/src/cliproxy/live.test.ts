// Opt-in live test: one real turn through CLIProxyAPI with a tool.
//   PALUI_LIVE=1 npx vitest run src/cliproxy/live.test.ts
// The key is read from ~/.config/palui/cliproxy-key and never printed.
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { tool } from "ai"
import { describe, expect, it } from "vitest"
import { z } from "zod"
import { memoryDb, toolCalls, runs } from "@palui/db"
import { createTurnBus } from "@palui/core/chat/bus"
import { harness, TEST_KEY, toolsRuntime } from "@palui/core/chat/testing"
import { connectConnector, upsertConnector } from "@palui/core/connectors/store"
import { setSetting } from "@palui/core/settings"
import type { PaluiTool } from "@palui/core/connectors/define"
import type { TurnEvent } from "@palui/core/control"
import { webRuntime } from "../web/runtime"
import manifest from "./manifest"
import runtime from "./runtime"

const live = process.env.PALUI_LIVE === "1"
const keyFile = path.join(os.homedir(), ".config/palui/cliproxy-key")

describe.skipIf(!live)("live: cliproxy", () => {
  it("runs one real turn with a tool call", async () => {
    const apiKey = fs.readFileSync(keyFile, "utf8").trim()
    const db = memoryDb()
    upsertConnector(db, { id: "cliproxy", manifest, config: { baseUrl: process.env.PALUI_LIVE_BASE || "http://127.0.0.1:8317" }, secrets: { apiKey }, enabled: true }, TEST_KEY)
    connectConnector(db, "cliproxy")
    const model = process.env.PALUI_LIVE_MODEL || "claude-haiku-4-5-20251001"
    setSetting(db, "modelRoles", { default: [{ connectorId: "cliproxy", model }], fast: [{ connectorId: "cliproxy", model }], vision: [{ connectorId: "cliproxy", model }] })
    const bus = createTurnBus()
    const replies: string[] = []
    bus.subscribe((e: TurnEvent) => {
      if (e.type === "message" && e.role === "assistant") replies.push(e.text)
    })
    const add = tool({ description: "Add two integers a and b and return the sum.", inputSchema: z.object({ a: z.number().int(), b: z.number().int() }), execute: async ({ a, b }) => ({ sum: a + b }) })
    const h = await harness({
      db,
      registry: { cliproxy: runtime, web: webRuntime(bus), calc: toolsRuntime("calc", () => ({ add: { tool: add, class: "read" } })) },
      rows: { web: "channel", calc: "tools" },
      clock: { now: Date.now() },
    })
    const { createOutboxWorker } = await import("@palui/core/outbox/worker")
    const worker = createOutboxWorker({ db, channel: (ch) => h.sup.channel(ch), log: h.engine.ctx.log })
    await h.engine.inbound({ channel: "web", connectorId: "web", chatExternalId: "web", chatKind: "dm", senderExternalId: "owner", text: "Pakai tool calc__add untuk menghitung 17 + 25, lalu jawab angkanya saja.", at: 0 })
    await h.engine.idle()
    await worker.tick()
    const run = db.select().from(runs).get()!
    const calls = db.select().from(toolCalls).all()
    expect(run.status, run.error ?? "").toBe("ok")
    expect(calls.map((c) => c.tool)).toContain("calc__add")
    expect(replies.join(" ")).toContain("42")
    console.log(JSON.stringify({ model, steps: run.steps, tokensIn: run.tokensIn, tokensOut: run.tokensOut, ms: run.ms, reply: replies.join(" ").slice(0, 80) }))
    await h.sup.stop()
  }, 90_000)

  // The next turn replays the earlier turn's tool call as real tool parts. The
  // gateway must take them, even when that tool is no longer in the tool list.
  it("accepts an earlier turn's tool call in the history of the next turn", async () => {
    const apiKey = fs.readFileSync(keyFile, "utf8").trim()
    const db = memoryDb()
    upsertConnector(db, { id: "cliproxy", manifest, config: { baseUrl: process.env.PALUI_LIVE_BASE || "http://127.0.0.1:8317" }, secrets: { apiKey }, enabled: true }, TEST_KEY)
    connectConnector(db, "cliproxy")
    const model = process.env.PALUI_LIVE_MODEL || "claude-haiku-4-5-20251001"
    setSetting(db, "modelRoles", { default: [{ connectorId: "cliproxy", model }], fast: [{ connectorId: "cliproxy", model }], vision: [{ connectorId: "cliproxy", model }] })
    const bus = createTurnBus()
    const add = tool({ description: "Add two integers a and b and return the sum.", inputSchema: z.object({ a: z.number().int(), b: z.number().int() }), execute: async ({ a, b }) => ({ sum: a + b }) })
    const echo = tool({ description: "Echo a text back.", inputSchema: z.object({ text: z.string() }), execute: async ({ text }) => ({ text }) })
    let renamed = false
    const h = await harness({
      db,
      registry: { cliproxy: runtime, web: webRuntime(bus), calc: toolsRuntime("calc", (): Record<string, PaluiTool> => (renamed ? { echo: { tool: echo, class: "read" } } : { add: { tool: add, class: "read" } })) },
      rows: { web: "channel", calc: "tools" },
      clock: { now: Date.now() },
    })
    const ask = async (text: string) => {
      await h.engine.inbound({ channel: "web", connectorId: "web", chatExternalId: "web", chatKind: "dm", senderExternalId: "owner", text, at: 0 })
      await h.engine.idle()
    }
    await ask("Pakai tool calc__add untuk menghitung 17 + 25, lalu jawab angkanya saja.")
    await ask("Tadi hasilnya berapa? Jawab angkanya saja, jangan pakai tool.")
    renamed = true
    await ask("Sekali lagi, tadi hasilnya berapa? Jawab angkanya saja, jangan pakai tool.")
    const all = db.select().from(runs).all()
    expect(all.map((r) => [r.status, r.error ?? ""])).toEqual([["ok", ""], ["ok", ""], ["ok", ""]])
    expect(db.select().from(toolCalls).all().map((c) => c.tool)).toEqual(["calc__add"])
    expect(all.map((r) => r.resultPreview).join(" ")).toContain("42")
    console.log(JSON.stringify({ model, runs: all.map((r) => ({ steps: r.steps, tokensIn: r.tokensIn, reply: (r.resultPreview ?? "").slice(0, 60) })) }))
    await h.sup.stop()
  }, 120_000)
})
