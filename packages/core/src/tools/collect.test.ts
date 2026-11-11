import { describe, expect, it } from "vitest"
import type { Tool } from "ai"
import { z } from "zod"
import type { PaluiTool, TurnInfo } from "../connectors/define"
import type { RunningEntry } from "../connectors/supervisor"
import { silentLogger } from "../log"
import { collectTools, SEARCH_TOOL, type CollectOptions } from "./collect"

const turn = { runId: "run_1", chatId: "chat_1", channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: 1 } as TurnInfo

function entry(id: string, kind: string, names: string[]): RunningEntry {
  const tools: Record<string, PaluiTool> = {}
  for (const n of names) tools[n] = { tool: { description: `${id} ${n}`, inputSchema: z.object({}), execute: async () => "ok" } as unknown as Tool, class: "read" }
  return { id, row: { id, kind, label: id, trust: "own" }, handle: { stop: async () => {}, tools: async () => tools } } as unknown as RunningEntry
}

const running = () => [entry("notes", "tools", ["add", "list", "remove"]), entry("recall", "memory", ["search"]), entry("skills", "skill", ["read"]), entry("web", "tools", ["fetch", "search"])]

function opts(extra: Partial<CollectOptions> = {}): CollectOptions {
  return { turn, maxTools: 64, log: silentLogger, onUntrustedResult: () => {}, ...extra }
}

// The names in the order the model is sent them: the tool object's own order, filtered to the active ones.
function offered(c: Awaited<ReturnType<typeof collectTools>>): string[] {
  return Object.keys(c.tools).filter((n) => c.active.includes(n))
}

describe("collectTools order (prompt caching needs the same tool list every turn)", () => {
  it("is the same on every call: by connector kind, then the connector's own order", async () => {
    const first = offered(await collectTools(running(), opts()))
    expect(first).toEqual(["skills__read", "recall__search", "notes__add", "notes__list", "notes__remove", "web__fetch", "web__search"])
    for (let i = 0; i < 3; i++) expect(offered(await collectTools(running(), opts()))).toEqual(first)
  })

  it("is the same on every call with tool search on", async () => {
    const first = await collectTools(running(), opts({ maxTools: 4 }))
    expect(offered(first)).toEqual(["skills__read", "recall__search", "notes__add", SEARCH_TOOL])
    expect(first.deferred).toEqual(["notes__list", "notes__remove", "web__fetch", "web__search"])
    const again = await collectTools(running(), opts({ maxTools: 4 }))
    expect(offered(again)).toEqual(offered(first))
    expect(Object.keys(again.tools)).toEqual(Object.keys(first.tools))
  })

  it("keeps a session's loaded tools in the list's own order, however the session remembers them", async () => {
    const a = await collectTools(running(), opts({ maxTools: 4, sticky: ["web__search", "notes__remove"] }))
    const b = await collectTools(running(), opts({ maxTools: 4, sticky: ["notes__remove", "web__search"] }))
    expect(offered(a)).toEqual(["skills__read", "notes__remove", "web__search", SEARCH_TOOL])
    expect(offered(b)).toEqual(offered(a))
  })
})
