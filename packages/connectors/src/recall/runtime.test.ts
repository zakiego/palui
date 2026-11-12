import type http from "node:http"
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { afterEach, describe, expect, it } from "vitest"
import { z } from "zod"
import type { ConfigOf, PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { fakeCtx, startMcpServer, type TestMcpServer } from "../mcp/test-helpers"
import { normalizeSourceId, profileText, RecallApi } from "./api"
import manifest from "./manifest"
import runtime, { resolveContainer, shouldRecall } from "./runtime"

const KEY = "rcl_testkey123456"
const turn: TurnInfo = { runId: "run_1", chatId: "chat_1", channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: 0 }

type Seen = { method: string; path: string; auth?: string; body?: unknown }

function registerRecallTools(seenArgs: Record<string, unknown>[]) {
  return (server: McpServer) => {
    const container = z.string().optional()
    server.registerTool(
      "add_memory",
      { inputSchema: { content: z.string().optional(), container }, annotations: { readOnlyHint: false, destructiveHint: false } },
      async (args) => {
        seenArgs.push({ tool: "add_memory", ...args })
        return { content: [{ type: "text", text: "queued" }] }
      },
    )
    for (const name of ["search_memories", "ask_memory", "get_profile", "list_memories"]) {
      server.registerTool(name, { inputSchema: { query: z.string().optional(), container }, annotations: { readOnlyHint: true } }, async (args) => {
        seenArgs.push({ tool: name, ...args })
        return { content: [{ type: "text", text: `${name} ok` }] }
      })
    }
    server.registerTool("purge_container", { inputSchema: { container } }, async () => ({ content: [{ type: "text", text: "purged" }] }))
  }
}

type FakeOpts = { profileDelayMs?: number; profile?: Record<string, unknown>; profileDown?: boolean }

// What the fake recall uses when a call names no container (RECALL_DEFAULT_CONTAINER).
const SERVER_DEFAULT = "zaki"

async function fakeRecall(opts: FakeOpts = {}) {
  const seen: Seen[] = []
  const toolArgs: Record<string, unknown>[] = []
  const send = (res: http.ServerResponse, status: number, body: unknown) => {
    res.writeHead(status, { "content-type": "application/json" })
    res.end(JSON.stringify(body))
  }
  const server = await startMcpServer({
    token: KEY,
    register: registerRecallTools(toolArgs),
    rest: async (req, res, raw) => {
      const url = new URL(req.url ?? "/", "http://x")
      seen.push({ method: req.method ?? "", path: `${url.pathname}${url.search}`, auth: req.headers.authorization, body: raw ? JSON.parse(raw) : undefined })
      if (url.pathname === "/health") return send(res, 200, { ok: true, vec: "v0.1" }), true
      if (req.headers.authorization !== `Bearer ${KEY}`) return send(res, 401, { error: { type: "unauthorized", message: "missing or invalid API key" } }), true
      if (url.pathname === "/v1/containers") {
        return send(res, 200, { containers: [{ container: "default", memories: 3, documents: 1, lastActivity: 1 }, { container: "zaki", memories: 9, documents: 2, lastActivity: 2 }] }), true
      }
      if (url.pathname === "/v1/profile") {
        if (opts.profileDelayMs) await new Promise((r) => setTimeout(r, opts.profileDelayMs))
        if (res.destroyed) return true
        if (opts.profileDown) return send(res, 500, { error: { type: "internal", message: "boom" } }), true
        return (
          send(
            res,
            200,
            opts.profile ?? {
              container: url.searchParams.get("container") ?? SERVER_DEFAULT,
              static: "Zaki lives in Banjarbaru.",
              dynamic: "Building Palui.",
              updatedAt: 1,
              staticMemories: [],
              dynamicMemories: [],
              searchResults: [{ type: "memory", id: "m1", text: "Likes kopi susu", score: 0.9 }],
            },
          ),
          true
        )
      }
      if (url.pathname === "/v1/memories" && req.method === "POST") {
        const b = JSON.parse(raw) as { container: string; source_id?: string }
        return send(res, 202, { id: "doc_1", container: b.container ?? SERVER_DEFAULT, status: "queued", sourceId: b.source_id ?? null, duplicate: false }), true
      }
      if (url.pathname.startsWith("/v1/documents/") && (req.method === "DELETE" || req.method === "GET")) {
        const id = decodeURIComponent(url.pathname.split("/").pop() ?? "")
        const home: Record<string, string> = { doc_1: "zaki", doc_2: "agent-chef" }
        if (!home[id]) return send(res, 404, { error: { type: "not_found", message: "no such document" } }), true
        if (req.method === "DELETE") return send(res, 200, { deleted: true }), true
        return send(res, 200, { document: { id, container: home[id], title: "Old doc" } }), true
      }
      if (url.pathname.startsWith("/v1/memories/") && (req.method === "DELETE" || req.method === "GET")) {
        const id = decodeURIComponent(url.pathname.split("/").pop() ?? "")
        const home: Record<string, string> = { mem_1: "zaki", mem_2: "agent-chef" }
        if (!home[id]) return send(res, 404, { error: { type: "not_found", message: "no such memory" } }), true
        return send(res, 200, { memory: { id, fact: "Old fact", container: home[id] } }), true
      }
      return false
    },
  })
  const base = server.url.replace(/\/mcp$/, "")
  return { server, base, seen, toolArgs }
}

type Config = ConfigOf<typeof manifest>
const cfg = (url: string, extra: Partial<Config> = {}): Config => manifest.config.parse({ url, container: "zaki", ...extra })
// A config as the dashboard stores it when no container was picked: the
// runtime gets it as is, without the manifest's defaults.
const savedWithoutContainer = (url: string) => ({ url, autoRecall: true, autoSave: true }) as unknown as Config

let srv: TestMcpServer | undefined
afterEach(async () => {
  await srv?.close()
  srv = undefined
})

async function exec(t: PaluiTool | undefined, input: unknown) {
  if (!t?.tool.execute) throw new Error("tool missing")
  return t.tool.execute(input, { toolCallId: "c1", messages: [] } as never)
}

describe("recall manifest", () => {
  it("applies defaults", () => {
    expect(manifest.config.parse({})).toEqual({ url: "http://host.docker.internal:4800", container: "default", autoRecall: true, autoSave: true })
    expect(manifest.secrets.apiKey.safeParse("rcl_abc").success).toBe(true)
    expect(manifest.secrets.apiKey.safeParse("sk-abc").success).toBe(false)
  })
})

describe("recall test()", () => {
  it("checks /health then lists containers", async () => {
    const f = await fakeRecall()
    srv = f.server
    const res = await runtime.test(fakeCtx(cfg(f.base), { apiKey: KEY }))
    expect(res.ok).toBe(true)
    expect(res.detail).toBe("Connected, 2 containers")
    expect((res.data?.containers as { container: string }[]).map((c) => c.container)).toEqual(["default", "zaki"])
    expect(f.seen.map((s) => s.path)).toEqual(["/health", "/v1/containers"])
  })

  it("explains a bad key and an unreachable server", async () => {
    const f = await fakeRecall()
    srv = f.server
    expect(await runtime.test(fakeCtx(cfg(f.base), { apiKey: "rcl_wrong" }))).toEqual({ ok: false, detail: "recall rejected the API key (401)" })
    const down = await runtime.test(fakeCtx(cfg("http://127.0.0.1:1"), { apiKey: KEY }))
    expect(down.ok).toBe(false)
    expect(down.detail).toMatch(/Can't reach recall/)
  })
})

describe("recall runtime", () => {
  it("classes MCP tools, adds forget_memory, and locks a turn without a scope to the connector's container", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!(turn)
      expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.class]))).toEqual({
        forget_memory: "destructive",
        forget_document: "destructive",
        add_memory: "write",
        search_memories: "read",
        ask_memory: "read",
        get_profile: "read",
        list_memories: "read",
      })
      expect(Object.values(tools).every((t) => !t.untrusted)).toBe(true)

      await exec(tools.search_memories, { query: "kopi" })
      await expect(exec(tools.add_memory, { content: "x", container: "other" })).rejects.toThrow(/can't use recall container other/)
      expect(f.toolArgs).toEqual([{ tool: "search_memories", query: "kopi", container: "zaki" }])

      expect(await exec(tools.forget_memory, { id: "mem_1" })).toEqual({ forgotten: true, id: "mem_1", fact: "Old fact" })
      expect(await exec(tools.forget_memory, { id: "mem_2" })).toMatchObject({ forgotten: false })
      expect(await exec(tools.forget_memory, { id: "nope" })).toMatchObject({ forgotten: false })
      expect(f.seen.filter((s) => s.method === "DELETE").map((s) => s.path)).toEqual(["/v1/memories/mem_1"])

      expect(await running.action!("listContainers", {})).toMatchObject({ containers: [{ container: "default" }, { container: "zaki" }] })
    } finally {
      await running.stop()
    }
  })

  // The report: Palui in the owner's DM must not read or write the finansial
  // agent's memory either. Palui's scope comes from memoryScope(), locked too.
  it("locks Palui to the owner's container and its shared list", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!({ ...turn, agentId: "agt_palui", memory: { container: undefined, read: ["kb-home"], locked: true } })
      expect(tools.purge_container).toBeUndefined()
      await exec(tools.search_memories, { query: "laundry" })
      await exec(tools.search_memories, { query: "laundry", container: "kb-home" })
      await expect(exec(tools.search_memories, { query: "laundry", container: "agent-finansial" })).rejects.toThrow(/can't use recall container agent-finansial/)
      await expect(exec(tools.add_memory, { content: "x", container: "agent-finansial" })).rejects.toThrow(/can write zaki/)
      expect(f.toolArgs).toEqual([
        { tool: "search_memories", query: "laundry", container: "zaki" },
        { tool: "search_memories", query: "laundry", container: "kb-home" },
      ])
      expect(await exec(tools.forget_memory, { id: "mem_2" })).toMatchObject({ forgotten: false })
      expect(f.seen.filter((s) => s.method === "DELETE")).toEqual([])
    } finally {
      await running.stop()
    }
  })

  // The report: five forget_memory calls meant five approval prompts. One call
  // with every id asks once.
  it("forget_memory takes many ids in one call and still checks each container", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!({ ...turn, memory: { container: "zaki", read: [], locked: true } })
      expect(await exec(tools.forget_memory, { ids: ["mem_1", "mem_2", "nope", "mem_1"] })).toEqual({
        forgotten: 1,
        results: [
          { forgotten: true, id: "mem_1", fact: "Old fact" },
          { forgotten: false, id: "mem_2", error: "No memory with that id" },
          { forgotten: false, id: "nope", error: "No memory with that id" },
        ],
      })
      expect(f.seen.filter((s) => s.method === "DELETE").map((s) => s.path)).toEqual(["/v1/memories/mem_1"])
      expect(await exec(tools.forget_memory, { id: "mem_1" })).toEqual({ forgotten: true, id: "mem_1", fact: "Old fact" })
      expect(await exec(tools.forget_memory, {})).toMatchObject({ forgotten: 0, error: "Pass ids." })
    } finally {
      await running.stop()
    }
  })

  it("forget_document deletes only a document in the turn's own container", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!({ ...turn, memory: { container: "zaki", read: [], locked: true } })
      expect(await exec(tools.forget_document, { id: "doc_1" })).toEqual({ deleted: true, id: "doc_1", title: "Old doc" })
      expect(await exec(tools.forget_document, { id: "doc_2" })).toEqual({ deleted: false, id: "doc_2", error: "No document with that id" })
      expect(await exec(tools.forget_document, { id: "doc_9" })).toEqual({ deleted: false, id: "doc_9", error: "No document with that id" })
      expect(f.seen.filter((s) => s.method === "DELETE").map((s) => s.path)).toEqual(["/v1/documents/doc_1"])
    } finally {
      await running.stop()
    }
  })

  it("locks an agent's tools to its own container and its shared ones", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!({ ...turn, agentId: "agt_chef", memory: { container: "agent-chef", read: ["kb-menu"], locked: true } })
      // Tools recall adds later may reach other containers, so they're left out.
      expect(Object.keys(tools).sort()).toEqual(["add_memory", "ask_memory", "forget_document", "forget_memory", "get_profile", "list_memories", "search_memories"])
      await exec(tools.search_memories, { query: "sambal" })
      await exec(tools.search_memories, { query: "sambal", container: "kb-menu" })
      await exec(tools.add_memory, { content: "x" })
      await expect(exec(tools.search_memories, { query: "x", container: "zaki" })).rejects.toThrow(/can't use recall container zaki/)
      await expect(exec(tools.add_memory, { content: "x", container: "kb-menu" })).rejects.toThrow(/can write agent-chef/)
      expect(f.toolArgs).toEqual([
        { tool: "search_memories", query: "sambal", container: "agent-chef" },
        { tool: "search_memories", query: "sambal", container: "kb-menu" },
        { tool: "add_memory", content: "x", container: "agent-chef" },
      ])
      // Another container's memory looks missing and is never deleted.
      expect(await exec(tools.forget_memory, { id: "mem_1" })).toMatchObject({ forgotten: false })
      expect(await exec(tools.forget_memory, { id: "mem_2" })).toMatchObject({ forgotten: true })
      expect(f.seen.filter((s) => s.method === "DELETE").map((s) => s.path)).toEqual(["/v1/memories/mem_2"])
    } finally {
      await running.stop()
    }
  })

  it("recall() returns the profile text for the configured container", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    const text = await running.memory!.recall("what do I usually drink in the morning", { timeoutMs: 1500 })
    expect(text).toContain("About the owner: Zaki lives in Banjarbaru.")
    expect(text).toContain("- Likes kopi susu")
    const call = f.seen.find((s) => s.path.startsWith("/v1/profile"))
    expect(call?.path).toBe("/v1/profile?container=zaki&q=what+do+I+usually+drink+in+the+morning")
    // Commands and short messages are skipped without a request.
    expect(await running.memory!.recall("/status", { timeoutMs: 1500 })).toBeNull()
    expect(await running.memory!.recall("halo", { timeoutMs: 1500 })).toBeNull()
    expect(f.seen.filter((s) => s.path.startsWith("/v1/profile"))).toHaveLength(1)
    expect(running.live!()).toMatchObject({ recall: { calls: 1, hits: 1, skippedShort: 2, skipRate: 0 } })
    await running.stop()
  })

  it("recall() gives up with null on timeout", async () => {
    const f = await fakeRecall({ profileDelayMs: 500 })
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base), { apiKey: KEY }))
    const started = Date.now()
    expect(await running.memory!.recall("tell me about my plans for this week", { timeoutMs: 50 })).toBeNull()
    expect(Date.now() - started).toBeLessThan(400)
    expect(running.live!()).toMatchObject({ recall: { calls: 1, timeouts: 1, skipRate: 1 } })
    await running.stop()
  })

  it("save() posts to /v1/memories with the container and source_id", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(cfg(f.base, { autoSave: false }), { apiKey: KEY }))
    expect(running.memory!.autoSave).toBe(false)
    expect(running.memory!.autoRecall).toBe(true)
    await running.memory!.save({ sourceId: "ses_1:0-40", content: "Zaki: aku suka kopi\nPalui: noted", title: "Chat 10 Nov" })
    await running.memory!.save({ sourceId: "ses_1:41-50", content: "   " })
    const posts = f.seen.filter((s) => s.method === "POST")
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ container: "zaki", content: "Zaki: aku suka kopi\nPalui: noted", title: "Chat 10 Nov", source_id: "ses_1:0-40" })
    await running.stop()
  })

  it("start() fails when recall is unreachable", async () => {
    await expect(runtime.start(fakeCtx(cfg("http://127.0.0.1:1"), { apiKey: KEY }))).rejects.toThrow(/Can't reach recall/)
  })
})

// The report (2026-11-13): the recall connector was saved without a container
// and Palui has no override, so add_memory landed in recall's default (zaki)
// while forget_memory and forget_document compared against an empty name and
// answered "No memory with that id" for the fact Palui had just saved.
describe("recall's default container (connector saved without one)", () => {
  const palui = { ...turn, agentId: "agt_palui", memory: { container: undefined, read: [] as string[], locked: true } }
  const isDefaultProfile = (s: Seen) => s.path === "/v1/profile"

  it("add_memory, forget_memory (ids) and forget_document all use the same container", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(savedWithoutContainer(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!(palui)
      await exec(tools.add_memory, { content: "Kopi favorit Zaki adalah kopi susu" })
      await exec(tools.search_memories, { query: "kopi susu" })
      expect(f.toolArgs).toEqual([
        { tool: "add_memory", content: "Kopi favorit Zaki adalah kopi susu", container: "zaki" },
        { tool: "search_memories", query: "kopi susu", container: "zaki" },
      ])
      expect(await exec(tools.forget_memory, { ids: ["mem_1"] })).toEqual({ forgotten: 1, results: [{ forgotten: true, id: "mem_1", fact: "Old fact" }] })
      expect(await exec(tools.forget_document, { id: "doc_1" })).toEqual({ deleted: true, id: "doc_1", title: "Old doc" })
      expect(f.seen.filter((s) => s.method === "DELETE").map((s) => s.path)).toEqual(["/v1/memories/mem_1", "/v1/documents/doc_1"])

      // Asked once, not on every turn.
      await running.tools!(palui)
      expect(f.seen.filter(isDefaultProfile)).toHaveLength(1)
    } finally {
      await running.stop()
    }
  })

  it("still refuses another agent's ids without saying what they hold", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(savedWithoutContainer(f.base), { apiKey: KEY }))
    try {
      const paluiTools = await running.tools!(palui)
      expect(await exec(paluiTools.forget_memory, { ids: ["mem_2"] })).toEqual({ forgotten: 0, results: [{ forgotten: false, id: "mem_2", error: "No memory with that id" }] })
      expect(await exec(paluiTools.forget_document, { id: "doc_2" })).toEqual({ deleted: false, id: "doc_2", error: "No document with that id" })

      // And the other way: an agent with its own container can't forget the owner's.
      const chef = await running.tools!({ ...turn, agentId: "agt_chef", memory: { container: "agent-chef", read: [], locked: true } })
      expect(await exec(chef.forget_memory, { ids: ["mem_1"] })).toEqual({ forgotten: 0, results: [{ forgotten: false, id: "mem_1", error: "No memory with that id" }] })
      expect(await exec(chef.forget_document, { id: "doc_1" })).toEqual({ deleted: false, id: "doc_1", error: "No document with that id" })
      expect(f.seen.filter((s) => s.method === "DELETE")).toEqual([])
    } finally {
      await running.stop()
    }
  })

  it("names the containers an agent may use when it refuses one", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(savedWithoutContainer(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!(palui)
      await expect(exec(tools.search_memories, { query: "x", container: "agent-finansial" })).rejects.toThrow("This agent can't use recall container agent-finansial. It can read zaki.")
      await expect(exec(tools.add_memory, { content: "x", container: "agent-finansial" })).rejects.toThrow("It can write zaki.")
    } finally {
      await running.stop()
    }
  })

  it("forget tools refuse while recall won't say its default", async () => {
    const f = await fakeRecall({ profileDown: true })
    srv = f.server
    const running = await runtime.start(fakeCtx(savedWithoutContainer(f.base), { apiKey: KEY }))
    try {
      const tools = await running.tools!(palui)
      expect(await exec(tools.forget_memory, { ids: ["mem_1"] })).toMatchObject({ forgotten: 0, results: [{ forgotten: false, error: expect.stringMatching(/Can't tell which recall container/) }] })
      expect(await exec(tools.forget_document, { id: "doc_1" })).toMatchObject({ deleted: false, error: expect.stringMatching(/Can't tell which recall container/) })
      expect(f.seen.filter((s) => s.method === "DELETE")).toEqual([])
      await expect(exec(tools.search_memories, { query: "x", container: "agent-finansial" })).rejects.toThrow("It can only use its own container.")
    } finally {
      await running.stop()
    }
  })

  it("auto-recall and auto-save send no container instead of the word undefined", async () => {
    const f = await fakeRecall()
    srv = f.server
    const running = await runtime.start(fakeCtx(savedWithoutContainer(f.base), { apiKey: KEY }))
    try {
      await running.memory!.recall("what do I usually drink in the morning", { timeoutMs: 1500 })
      await running.memory!.save({ sourceId: "s:1", content: "Likes kopi susu" })
      expect(f.seen.find((s) => s.path.startsWith("/v1/profile"))?.path).toBe("/v1/profile?q=what+do+I+usually+drink+in+the+morning")
      expect(f.seen.find((s) => s.method === "POST" && s.path === "/v1/memories")?.body).toEqual({ content: "Likes kopi susu", source_id: "s:1" })
    } finally {
      await running.stop()
    }
  })
})

describe("helpers", () => {
  it("resolveContainer fills the default and only unlocks for the owner", () => {
    const locked = { container: "agent-chef", read: ["kb-menu"], locked: true }
    expect(resolveContainer(undefined, "read", locked)).toBe("agent-chef")
    expect(resolveContainer(" ", "write", locked)).toBe("agent-chef")
    expect(resolveContainer("kb-menu", "read", locked)).toBe("kb-menu")
    expect(() => resolveContainer("kb-menu", "write", locked)).toThrow()
    expect(() => resolveContainer("zaki", "read", locked)).toThrow()
    expect(resolveContainer("anything", "write", { container: "zaki", read: [], locked: false })).toBe("anything")
  })

  it("shouldRecall skips commands and short messages", () => {
    expect(shouldRecall("/new")).toBe(false)
    expect(shouldRecall("apa kabar")).toBe(false)
    expect(shouldRecall("ingat nggak aku suka apa")).toBe(true)
  })

  it("profileText falls back to facts and returns null when empty", () => {
    expect(profileText({ container: "c", static: "", dynamic: "", updatedAt: null, staticMemories: [], dynamicMemories: [] })).toBeNull()
    expect(
      profileText({ container: "c", static: "", dynamic: "", updatedAt: null, staticMemories: [{ id: "1", fact: "Has a cat" }], dynamicMemories: [] }),
    ).toBe("Facts about the owner:\n- Has a cat")
  })

  it("normalizeSourceId hashes ids over 200 characters", () => {
    expect(normalizeSourceId("ses_1:0-40")).toBe("ses_1:0-40")
    const long = normalizeSourceId("x".repeat(300))
    expect(long).toMatch(/^palui:[0-9a-f]{64}$/)
    expect(long.length).toBeLessThanOrEqual(200)
  })

  it("RecallApi strips trailing slashes", () => {
    expect(new RecallApi("http://r:4800///", KEY).mcpUrl).toBe("http://r:4800/mcp")
  })
})
