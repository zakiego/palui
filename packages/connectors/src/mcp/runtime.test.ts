import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { afterEach, describe, expect, it } from "vitest"
import { z } from "zod"
import type { ConfigOf, PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { saveAttachment } from "@palui/core/attachments/service"
import { masterKey } from "@palui/core/config"
import { ensureChat } from "@palui/core/chats"
import { suggestClass } from "./client"
import manifest from "./manifest"
import runtime from "./runtime"
import { fakeCtx, startMcpServer, type TestMcpServer } from "./test-helpers"

const TOKEN = "zkg_test_token_123456"
const turn: TurnInfo = { runId: "run_1", chatId: "chat_1", channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: 0 }

function register(server: McpServer) {
  server.registerTool(
    "search",
    { description: "Search things", inputSchema: { q: z.string() }, annotations: { readOnlyHint: true } },
    async ({ q }) => ({ content: [{ type: "text", text: `found ${q}` }] }),
  )
  server.registerTool(
    "add_note",
    { description: "Add a note", inputSchema: { text: z.string() }, annotations: { readOnlyHint: false, destructiveHint: false } },
    async ({ text }) => ({ content: [{ type: "text", text: `saved ${text}` }] }),
  )
  server.registerTool("delete_all", { description: "Delete everything", annotations: { destructiveHint: true } }, async () => ({
    content: [{ type: "text", text: "gone" }],
  }))
  server.registerTool("mystery", { description: "No hints" }, async () => ({ content: [{ type: "text", text: "?" }] }))
}

let srv: TestMcpServer | undefined
afterEach(async () => {
  await srv?.close()
  srv = undefined
})

type Config = ConfigOf<typeof manifest>
const config = (url: string, extra: Partial<Config> = {}): Config => manifest.config.parse({ url, authMode: "bearer", ...extra })

async function exec(t: PaluiTool | undefined, input: unknown) {
  if (!t?.tool.execute) throw new Error("tool missing")
  return t.tool.execute(input, { toolCallId: "call_1", messages: [] } as never)
}

describe("suggestClass", () => {
  it("maps annotations, failing closed", () => {
    expect(suggestClass({ readOnlyHint: true })).toBe("read")
    expect(suggestClass({ readOnlyHint: false, destructiveHint: false })).toBe("write")
    expect(suggestClass({ destructiveHint: true })).toBe("destructive")
    expect(suggestClass({ readOnlyHint: false })).toBe("destructive")
    expect(suggestClass(undefined)).toBe("destructive")
  })
})

describe("mcp connector", () => {
  it("test() lists tools with class prefill", async () => {
    srv = await startMcpServer({ register, token: TOKEN })
    const res = await runtime.test(fakeCtx(config(srv.url), { token: TOKEN }))
    expect(res.ok).toBe(true)
    expect(res.detail).toBe("4 tools available")
    const tools = res.data?.tools as { name: string; suggestedClass: string; class: string; enabled: boolean; saved: boolean }[]
    expect(Object.fromEntries(tools.map((t) => [t.name, t.suggestedClass]))).toEqual({
      search: "read",
      add_note: "write",
      delete_all: "destructive",
      mystery: "destructive",
    })
    expect(tools.every((t) => t.enabled && !t.saved)).toBe(true)
  })

  it("requires the bearer token", async () => {
    srv = await startMcpServer({ register, token: TOKEN })
    const bad = await runtime.test(fakeCtx(config(srv.url), { token: "wrong" }))
    expect(bad.ok).toBe(false)
    expect(srv.auths.at(-1)).toBe("Bearer wrong")
    const missing = await runtime.test(fakeCtx(config(srv.url), {}))
    expect(missing).toEqual({ ok: false, detail: "This server needs a token" })
  })

  it("sends a custom header in header mode", async () => {
    srv = await startMcpServer({ register, token: TOKEN })
    const res = await runtime.test(fakeCtx(config(srv.url, { authMode: "header", headerName: "Authorization" }), { token: `Bearer ${TOKEN}` }))
    expect(res.ok).toBe(true)
  })

  it("offers only enabled tools with their class, unset ones as destructive", async () => {
    srv = await startMcpServer({ register, token: TOKEN })
    const cfg = config(srv.url, {
      toolPrefs: {
        search: { enabled: true, class: "read" },
        add_note: { enabled: true, class: "write" },
        delete_all: { enabled: false, class: "destructive" },
      },
    })
    const running = await runtime.start(fakeCtx(cfg, { token: TOKEN }, { trust: "external" }))
    try {
      const tools = await running.tools!(turn)
      expect(Object.keys(tools).sort()).toEqual(["add_note", "mystery", "search"])
      expect(tools.search?.class).toBe("read")
      expect(tools.add_note?.class).toBe("write")
      expect(tools.mystery?.class).toBe("destructive")
      expect(tools.search?.untrusted).toBe(true)

      const out = (await exec(tools.search, { q: "kopi" })) as { content: { text: string }[] }
      expect(JSON.stringify(out)).toContain("found kopi")

      const listed = await running.action!("listTools", {})
      const rows = listed.tools as { name: string; enabled: boolean; saved: boolean }[]
      expect(rows.find((r) => r.name === "delete_all")).toMatchObject({ enabled: false, saved: true })
      expect(rows.find((r) => r.name === "mystery")).toMatchObject({ enabled: true, saved: false })
    } finally {
      await running.stop()
    }
  })

  it("callTool() calls a tool directly with its class, refusing turned-off tools and not running non-read ones", async () => {
    srv = await startMcpServer({ register, token: TOKEN })
    const cfg = config(srv.url, { toolPrefs: { delete_all: { enabled: false, class: "destructive" }, add_note: { enabled: true, class: "write" } } })
    const running = await runtime.start(fakeCtx(cfg, { token: TOKEN }))
    try {
      // No saved pref: the server's readOnlyHint makes it read.
      const res = await running.callTool!("search", { q: "jadwal" })
      expect(res.class).toBe("read")
      expect(JSON.stringify(res.output)).toContain("found jadwal")
      expect(await running.callTool!("add_note", { text: "x" })).toEqual({ class: "write", output: null })
      expect(await running.callTool!("mystery", {})).toEqual({ class: "destructive", output: null })
      await expect(running.callTool!("delete_all", {})).rejects.toThrow(/turned off/)
      await expect(running.callTool!("nope", {})).rejects.toThrow(/No tool nope/)
    } finally {
      await running.stop()
    }
  })

  it("marks tools trusted for an own server", async () => {
    srv = await startMcpServer({ register })
    const running = await runtime.start(fakeCtx(config(srv.url, { authMode: "none" }), {}, { trust: "own" }))
    try {
      const tools = await running.tools!(turn)
      expect(tools.search?.untrusted).toBe(false)
    } finally {
      await running.stop()
    }
  })

  it("start() fails clearly when the server is down, and tools() recovers after an outage", async () => {
    srv = await startMcpServer({ register, token: TOKEN })
    const url = srv.url
    await expect(runtime.start(fakeCtx(config("http://127.0.0.1:1/mcp"), { token: TOKEN }))).rejects.toThrow()

    const ctx = fakeCtx(config(url), { token: TOKEN })
    const running = await runtime.start(ctx)
    try {
      const before = await running.tools!(turn)
      expect(Object.keys(before)).toHaveLength(4)
      await srv.close()
      srv = undefined
      // The call fails, which drops the cached client and tool list...
      await expect(exec(before.search, { q: "x" })).rejects.toThrow()
      // ...so the next turn gets no tools and the card shows the error.
      expect(await running.tools!(turn)).toEqual({})
      expect(ctx.statuses.at(-1)?.status).toBe("error")
      await expect(running.action!("listTools", {})).rejects.toThrow()

      // The server comes back on the same port: the next turn reconnects.
      srv = await startMcpServer({ register, token: TOKEN, port: Number(new URL(url).port) })
      expect(Object.keys(await running.tools!(turn))).toHaveLength(4)
      expect(ctx.statuses.at(-1)?.status).toBe("on")
    } finally {
      await running.stop()
    }
  })
})

describe("flow approval per tool (RFC flows 7.2)", () => {
  it("defaults to once per version and carries every_run from the saved prefs", async () => {
    const { listingFor, selectTools } = await import("./runtime")
    const defs = [
      { name: "send_email", description: "", inputSchema: { type: "object" as const }, annotations: { destructiveHint: false } },
      { name: "list", description: "", inputSchema: { type: "object" as const }, annotations: { readOnlyHint: true } },
    ]
    const prefs = { send_email: { enabled: true, class: "write" as const, flowApproval: "every_run" as const }, list: { enabled: true, class: "read" as const } }
    expect(selectTools(defs as never, prefs).map((t) => [t.def.name, t.flowApproval])).toEqual([
      ["send_email", "every_run"],
      ["list", "version"],
    ])
    expect(listingFor(defs as never, prefs).map((t) => t.flowApproval)).toEqual(["every_run", "version"])
  })
})

describe("saved files in tool args", () => {
  const JPEG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46])
  const received: string[] = []
  const registerUpload = (server: McpServer) => {
    server.registerTool("upload_attachment", { description: "Upload a file", inputSchema: { file: z.string() } }, async ({ file }) => {
      if (file.startsWith("data:")) received.push(file)
      return { content: [{ type: "text", text: `got ${file}` }] }
    })
  }

  async function setupFile(trust: "own" | "external") {
    srv = await startMcpServer({ register: registerUpload })
    const ctx = fakeCtx(config(srv.url, { authMode: "none" }), {}, { trust })
    const chat = ensureChat(ctx.db, { channel: "whatsapp", externalId: "120363@g.us" }, 0)
    const other = ensureChat(ctx.db, { channel: "telegram", externalId: "42" }, 0)
    const { row } = saveAttachment(ctx.db, { chatId: chat.id, title: "Struk", mimeType: "image/jpeg", bytes: JPEG }, masterKey(), 0)
    const running = await runtime.start(ctx)
    const tools = await running.tools!({ ...turn, chatId: chat.id })
    return { running, tools, id: row.id, otherChatId: other.id, chatId: chat.id }
  }

  it("sends the file's contents to an own server in place of its ref", async () => {
    const { running, tools, id } = await setupFile("own")
    try {
      const out = (await exec(tools.upload_attachment, { file: `palui:attachment/${id}` })) as { content: { text: string }[] }
      // The server received the data URL; its echo comes back as the short ref.
      expect(out.content[0]?.text).toBe(`got palui:attachment/${id}`)
      expect(received).toEqual([`data:image/jpeg;base64,${JPEG.toString("base64")}`])
      // A plain id isn't a ref and goes through as is.
      expect(((await exec(tools.upload_attachment, { file: id })) as { content: { text: string }[] }).content[0]?.text).toBe(`got ${id}`)
    } finally {
      await running.stop()
    }
  })

  it("refuses a ref from another chat and never sends files to an external server", async () => {
    const own = await setupFile("own")
    try {
      const other = await own.running.tools!({ ...turn, chatId: own.otherChatId })
      expect(await exec(other.upload_attachment, { file: `palui:attachment/${own.id}` })).toMatchObject({ isError: true, content: [{ text: expect.stringContaining("No saved attachment") }] })
    } finally {
      await own.running.stop()
      await srv?.close()
      srv = undefined
    }
    const ext = await setupFile("external")
    try {
      expect(await exec(ext.tools.upload_attachment, { file: `palui:attachment/${ext.id}` })).toMatchObject({ isError: true, content: [{ text: expect.stringContaining("own") }] })
    } finally {
      await ext.running.stop()
    }
  })
})
