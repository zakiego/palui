// Contract test against the REAL recall on this machine. Opt in with
// PALUI_LIVE=1. READ ONLY: /health, GET /v1/containers, POST /v1/search and
// GET /v1/profile. It never adds, edits or deletes memories. The key is read
// from ~/.config/recall/api-key and never printed.
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { describe, expect, it } from "vitest"
import { fakeCtx } from "../mcp/test-helpers"
import { profileText, RecallApi } from "./api"
import manifest from "./manifest"
import runtime, { createMemoryHandle, type RecallStats } from "./runtime"

const LIVE = process.env.PALUI_LIVE === "1"
const URL_ = process.env.PALUI_RECALL_URL || "http://127.0.0.1:4800"

function readKey(): string {
  const file = path.join(os.homedir(), ".config", "recall", "api-key")
  return fs.readFileSync(file, "utf8").trim()
}

describe.skipIf(!LIVE)("recall live contract (read only)", () => {
  it("GET /health is ok", async () => {
    const api = new RecallApi(URL_, readKey())
    const h = await api.health(AbortSignal.timeout(5000))
    expect(h.ok).toBe(true)
  })

  it("GET /v1/containers returns a list of containers", async () => {
    const api = new RecallApi(URL_, readKey())
    const list = await api.containers(AbortSignal.timeout(5000))
    expect(Array.isArray(list)).toBe(true)
    for (const c of list) {
      expect(typeof c.container).toBe("string")
      expect(typeof c.memories).toBe("number")
      expect(typeof c.documents).toBe("number")
    }
  })

  it("test() succeeds with the real key", async () => {
    const res = await runtime.test(fakeCtx(manifest.config.parse({ url: URL_ }), { apiKey: readKey() }))
    expect(res).toMatchObject({ ok: true })
  })

  it("POST /v1/search answers with results", async () => {
    const api = new RecallApi(URL_, readKey())
    const res = await api.search("default", "kopi", 3, AbortSignal.timeout(15_000))
    expect(res.container).toBe("default")
    expect(Array.isArray(res.results)).toBe(true)
    for (const r of res.results) {
      expect(typeof r.id).toBe("string")
      expect(typeof r.text).toBe("string")
    }
  })

  it("GET /v1/profile has the shape profileText reads", async () => {
    const api = new RecallApi(URL_, readKey())
    const p = await api.profile("default", "what does the owner like to drink", AbortSignal.timeout(15_000))
    expect(typeof p.static).toBe("string")
    expect(typeof p.dynamic).toBe("string")
    expect(Array.isArray(p.staticMemories)).toBe(true)
    expect(Array.isArray(p.dynamicMemories)).toBe(true)
    expect(Array.isArray(p.searchResults)).toBe(true)
    const text = profileText(p)
    expect(text === null || typeof text === "string").toBe(true)
  })

  it("memory.recall() answers within a generous budget", async () => {
    const api = new RecallApi(URL_, readKey())
    const stats: RecallStats = { calls: 0, hits: 0, empty: 0, skippedShort: 0, timeouts: 0, errors: 0 }
    const handle = createMemoryHandle(api, manifest.config.parse({ url: URL_ }), stats)
    await handle.recall("what does the owner usually work on", { timeoutMs: 10_000 })
    expect(stats.errors).toBe(0)
    expect(stats.timeouts).toBe(0)
  })
})
