import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { afterEach, describe, expect, it } from "vitest"
import { z } from "zod"
import { outbox } from "@palui/db"
import { ensureChat } from "@palui/core/chats"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { fakeCtx, startMcpServer, type TestMcpServer } from "../mcp/test-helpers"
import { codeFingerprint, matchProject, pairWithCode, parseModel, T3Client } from "./client"
import manifest from "./manifest"
import runtime, { createWatcher, ensureToken, environmentIdOf, finishedText, parseConfig, startT3, threadIdOf, WATCH_TTL_MS } from "./runtime"

const TOKEN = "t3.session.token"
const CODE = "PAIR-CODE-1234"

type Run = { status: string }
type State = {
  pairs: number
  launches: Record<string, unknown>[]
  sends: Record<string, unknown>[]
  interrupts: string[]
  runs: Record<string, Run>
  pending: number
  reply: string
}

function fresh(): State {
  return { pairs: 0, launches: [], sends: [], interrupts: [], runs: {}, pending: 0, reply: "Done: fixed the bug and opened PR #12." }
}

const projects = [
  { id: "p1", title: "palui", workspaceRoot: "/Users/z/Code/palui", archivedAt: null, deletedAt: null },
  { id: "p2", title: "vorneo", workspaceRoot: "/Users/z/Code/vorneo", archivedAt: null, deletedAt: null },
  { id: "p3", title: "old", workspaceRoot: "/Users/z/Code/old", archivedAt: "2026-01-01", deletedAt: null },
  { id: "p5", title: "site", workspaceRoot: "/Users/z/Code/site", defaultModelSelection: { instanceId: "codex", model: "gpt" }, archivedAt: null, deletedAt: null },
]

const json = (v: unknown) => ({ content: [{ type: "text" as const, text: JSON.stringify(v) }] })
const failure = (code: string, message: string) => ({ isError: true, content: [{ type: "text" as const, text: JSON.stringify({ _tag: "OrchestratorMcpFailure", code, message }) }] })

function thread(id: string, s: State) {
  return {
    threadId: id,
    projectId: "p1",
    title: "Fix the bug",
    link: `https://app.t3.codes/threads/env1/${id}`,
    status: s.runs["run-1"]?.status ?? "idle",
    latestRunId: "run-1",
    activeRunId: s.runs["run-1"]?.status === "running" ? "run-1" : null,
    branch: "zaki/fix-bug",
    pendingRequestCount: s.pending,
    pullRequests: [],
  }
}

function register(s: State) {
  return (server: McpServer) => {
    server.registerTool("t3_project_list", { inputSchema: { cursor: z.number().optional(), limit: z.number().optional() } }, async () => json({ projects, nextCursor: null }))
    server.registerTool("t3_thread_list", { inputSchema: { projectId: z.string().optional(), allProjects: z.boolean().optional(), statuses: z.array(z.string()).optional(), includeSubagents: z.boolean().optional(), limit: z.number().optional() } }, async () =>
      json({ threads: [thread("11111111-aaaa", s)], nextCursor: null }),
    )
    server.registerTool("orchestrator_capabilities", { inputSchema: {} }, async () =>
      json({
        providers: [
          { providerInstanceId: "codex", models: [], constraints: ["Provider instance is disabled."] },
          { providerInstanceId: "claudeAgent", models: [{ id: "claude-opus-5-5" }, { id: "claude-sonnet-5-5" }], constraints: [] },
        ],
      }),
    )
    server.registerTool(
      "t3_thread_launch",
      {
        inputSchema: {
          projectId: z.string(),
          title: z.string(),
          message: z.string(),
          clientRequestId: z.string(),
          modelSelection: z.object({ instanceId: z.string(), model: z.string() }).optional(),
          workspaceStrategy: z.object({ type: z.string(), baseRef: z.string() }).optional(),
        },
      },
      async (args) => {
        s.launches.push(args)
        s.runs["run-1"] = { status: "running" }
        return json({ threadId: "11111111-aaaa", projectId: args.projectId, runId: "run-1", link: "https://app.t3.codes/threads/env1/11111111-aaaa" })
      },
    )
    server.registerTool("t3_thread_send", { inputSchema: { threadId: z.string(), message: z.string(), mode: z.string().optional(), clientRequestId: z.string().optional() } }, async (args) => {
      s.sends.push(args)
      s.runs["run-1"] = { status: "running" }
      return json({ threadId: args.threadId, delivered: "started" })
    })
    server.registerTool("t3_thread_wait", { inputSchema: { threadId: z.string(), runId: z.string().optional(), timeoutMs: z.number().optional() } }, async ({ threadId, runId }) => {
      if (threadId === "gone") return failure("thread_not_found", `Thread ${threadId} was not found.`)
      const run = s.runs[runId ?? "run-1"]
      const status = run?.status ?? "idle"
      return json({ threadId, runId: runId ?? "run-1", status, timedOut: status === "running" })
    })
    server.registerTool(
      "t3_thread_read",
      { inputSchema: { threadId: z.string(), view: z.string().optional(), limit: z.number().optional(), runLimit: z.number().optional(), maxCharsPerItem: z.number().optional() } },
      async ({ threadId }) => {
        if (threadId === "gone") return failure("thread_not_found", `Thread ${threadId} was not found.`)
        return json({
          thread: thread(threadId, s),
          items: [
            { itemId: "i1", runId: "run-1", type: "user_message", text: "Fix the bug", position: 0 },
            { itemId: "i2", runId: "run-1", type: "assistant_message", text: s.reply, position: 1 },
          ],
        })
      },
    )
    server.registerTool("t3_thread_interrupt", { inputSchema: { threadId: z.string(), reason: z.string().optional(), clientRequestId: z.string().optional() } }, async ({ threadId }) => {
      s.interrupts.push(threadId)
      return json({ threadId, interrupted: true })
    })
  }
}

function oauth(s: State) {
  return async (req: import("node:http").IncomingMessage, res: import("node:http").ServerResponse, body: string) => {
    const path = new URL(req.url ?? "/", "http://x").pathname
    const send = (status: number, v: unknown) => {
      res.writeHead(status, { "content-type": "application/json" })
      res.end(JSON.stringify(v))
      return true
    }
    if (path === "/oauth/mcp/register") return send(201, { client_id: "client-1", client_name: JSON.parse(body).client_name })
    if (path === "/oauth/mcp/decision") {
      const { authorization, decision } = JSON.parse(body)
      if (decision.code !== CODE || decision.access !== "full-access") return send(400, { _tag: "AuthMcpApprovalError", message: "That pairing code is unknown or used." })
      if (!String(authorization.resource).endsWith("/mcp")) return send(400, { message: "bad resource" })
      return send(200, { redirectTo: `${authorization.redirect_uri}?code=auth-code&state=${authorization.state}&iss=x` })
    }
    if (path === "/oauth/mcp/token") {
      if (req.headers["content-type"] !== "application/x-www-form-urlencoded") return send(415, { message: "Unsupported content-type" })
      const form = new URLSearchParams(body)
      if (form.get("code") !== "auth-code" || !form.get("code_verifier")) return send(400, { error: "invalid_grant" })
      s.pairs++
      return send(200, { access_token: TOKEN, token_type: "Bearer", expires_in: 2_592_000, scope: "orchestration:read" })
    }
    return false
  }
}

let srv: TestMcpServer | undefined
afterEach(async () => {
  await srv?.close()
  srv = undefined
})

async function server(s: State) {
  srv = await startMcpServer({ register: register(s), token: TOKEN, rest: oauth(s) })
  return srv
}

function ctxFor(url: string, secrets: Record<string, string | undefined>, config: Record<string, unknown> = {}) {
  const base = fakeCtx({ url, ...config } as Record<string, unknown>, secrets as Record<string, string | undefined>, { id: "t3code" })
  const secretPatches: Record<string, string>[] = []
  return Object.assign(base, { patchSecrets: (p: Record<string, string>) => secretPatches.push(p), secretPatches })
}

async function exec(t: PaluiTool | undefined, input: unknown) {
  if (!t?.tool.execute) throw new Error("tool missing")
  return t.tool.execute(input, { toolCallId: "call_1", messages: [] } as never)
}

describe("t3code helpers", () => {
  it("parses config with defaults and reads thread ids from links", () => {
    expect(parseConfig({})).toEqual({ url: "http://host.docker.internal:3773", baseRef: "main" })
    expect(parseModel("claudeAgent/claude-opus-5-5")).toEqual({ instanceId: "claudeAgent", model: "claude-opus-5-5" })
    expect(parseModel("opus")).toBeNull()
    expect(manifest.config.safeParse({ model: "opus" }).success).toBe(false)
    expect(manifest.config.parse({}).url).toBe("http://host.docker.internal:3773")
    expect(threadIdOf("https://app.t3.codes/threads/8b457a52-5eaa/b3b33950-a26e-4e45-a9a3-533bfa7efb92")).toBe("b3b33950-a26e-4e45-a9a3-533bfa7efb92")
    expect(threadIdOf(" b3b33950-a26e ")).toBe("b3b33950-a26e")
    expect(threadIdOf("https://app.t3.codes/threads/8b457a52/mcp%3Alaunch%3Aa5c604d2/")).toBe("mcp:launch:a5c604d2")
    expect(threadIdOf("mcp:launch:a5c604d2")).toBe("mcp:launch:a5c604d2")
  })

  it("matches projects by name, folder, id or path and skips archived ones", () => {
    const live = projects.filter((p) => !p.archivedAt)
    expect(matchProject(live, "Palui")).toMatchObject({ project: { id: "p1" } })
    expect(matchProject(live, "p2")).toMatchObject({ project: { id: "p2" } })
    expect(matchProject(live, "vor")).toMatchObject({ project: { id: "p2" } })
    expect(matchProject(live, "/Users/z/Code/palui")).toMatchObject({ project: { id: "p1" } })
    const twins = [...live, { id: "p4", title: "palui-site", workspaceRoot: "/Users/z/Code/palui-site" }]
    expect(matchProject(twins, "pal")).toMatchObject({ error: expect.stringContaining("matches 2 projects") })
    expect(matchProject(twins, "palui")).toMatchObject({ project: { id: "p1" } })
    expect(matchProject(live, "nope")).toMatchObject({ error: expect.stringContaining("palui, vorneo, site") })
  })

  it("writes the finished message in Indonesian with a clipped reply and the link", () => {
    const w = { threadId: "t", runId: "r", chatId: "c", title: "Fix bug", link: "https://app.t3.codes/threads/e/t", since: 0 }
    expect(finishedText(w, "completed", "All good.")).toBe("T3 Code beres: Fix bug\n\nAll good.\n\nhttps://app.t3.codes/threads/e/t")
    expect(finishedText(w, "failed", null)).toBe("T3 Code berhenti (failed): Fix bug\n\nhttps://app.t3.codes/threads/e/t")
    expect(finishedText(w, "completed", "x".repeat(5000)).length).toBeLessThan(1600)
  })
})

describe("t3code pairing", () => {
  it("trades a pairing code for a token once and keeps it", async () => {
    const s = fresh()
    const { url } = await server(s)
    const res = await pairWithCode(url, CODE, { now: 1_000 })
    expect(res).toEqual({ token: TOKEN, expiresAt: 1_000 + 2_592_000_000 })
    await expect(pairWithCode(url, "WRONG-CODE-1")).rejects.toThrow(/unknown or used/)

    const ctx = ctxFor(url, { pairingCode: CODE })
    expect(await ensureToken(ctx, parseConfig(ctx.config))).toBe(TOKEN)
    expect(ctx.secretPatches).toEqual([{ token: TOKEN }])
    expect(ctx.patches[0]).toMatchObject({ pairedCode: codeFingerprint(CODE) })
    // Same code again: no second trade.
    expect(await ensureToken(ctx, parseConfig(ctx.config))).toBe(TOKEN)
    expect(s.pairs).toBe(2)
  })

  it("test() pairs and counts live projects; a bad code reads clearly", async () => {
    const s = fresh()
    const { url } = await server(s)
    const ok = await runtime.test(ctxFor(url, { pairingCode: CODE }) as never)
    expect(ok).toEqual({ ok: true, detail: "Connected to T3 Code: 3 projects" })
    const bad = await runtime.test(ctxFor(url, { pairingCode: "WRONG-CODE-1" }) as never)
    expect(bad.ok).toBe(false)
    expect(bad.detail).toMatch(/pairing code/)
  })

  it("a rejected token says how to fix it", async () => {
    const s = fresh()
    const { url } = await server(s)
    const res = await runtime.test(ctxFor(url, { pairingCode: CODE, token: "expired" }, { pairedCode: codeFingerprint(CODE) }) as never)
    expect(res.ok).toBe(false)
    expect(res.detail).toMatch(/t3 auth pairing create/)
  })
})

describe("t3code tools and watcher", () => {
  async function setup() {
    const s = fresh()
    const { url } = await server(s)
    const ctx = ctxFor(url, { pairingCode: CODE, token: TOKEN }, { pairedCode: codeFingerprint(CODE) })
    const chat = ensureChat(ctx.db, { channel: "telegram", externalId: "42" })
    const turn: TurnInfo = { runId: "run_a", chatId: chat.id, channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: 0 }
    const started = startT3(ctx, TOKEN, { pollMs: 3_600_000 })
    const tools = await started.handle.tools(turn)
    return { s, ctx, chat, turn, started, tools }
  }

  it("offers seven tools with the right classes", async () => {
    const { tools, started } = await setup()
    expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.class]))).toEqual({
      list_projects: "read",
      list_threads: "read",
      start_thread: "write",
      send_to_thread: "write",
      list_chat_threads: "read",
      get_thread: "read",
      stop_thread: "destructive",
    })
    expect(Object.values(tools).every((t) => t.untrusted === false)).toBe(true)
    await started.handle.stop()
  })

  it("starts a thread in a worktree, then tells the chat when it finishes", async () => {
    const { s, ctx, tools, started } = await setup()
    const out = await exec(tools.start_thread, { project: "palui", task: "Fix the bug\nin the router" })
    expect(out).toMatchObject({ ok: true, threadId: "11111111-aaaa", environmentId: "env1", title: "Fix the bug", project: "palui", workspace: "new worktree from main" })
    expect(s.launches[0]).toMatchObject({
      projectId: "p1",
      title: "Fix the bug",
      modelSelection: { instanceId: "claudeAgent", model: "claude-opus-5-5" },
      workspaceStrategy: { type: "worktree", baseRef: "main" },
    })
    expect(String(s.launches[0]!.clientRequestId)).toMatch(/^palui-run_a-/)
    expect(started.watcher.list()).toHaveLength(1)

    // Still running: nothing sent.
    await started.watcher.poll()
    expect(ctx.db.select().from(outbox).all()).toHaveLength(0)

    s.runs["run-1"] = { status: "completed" }
    await started.watcher.poll()
    const rows = ctx.db.select().from(outbox).all()
    expect(rows).toHaveLength(1)
    expect(rows[0]!.source).toBe("t3code:11111111-aaaa")
    expect(rows[0]!.payload.text).toBe(`T3 Code beres: Fix the bug\n\n${s.reply}\n\nhttps://app.t3.codes/threads/env1/11111111-aaaa`)
    expect(started.watcher.list()).toHaveLength(0)
    expect(ctx.patches.at(-1)).toEqual({ watches: [] })
    // The thread stays listed for this chat after the watch is gone.
    expect(await exec(tools.list_chat_threads, {})).toMatchObject({ ok: true, threads: [{ threadId: "11111111-aaaa", action: "started", environmentId: "env1" }] })
    await started.handle.stop()
  })

  it("works in place when asked and reports an unknown project", async () => {
    const { s, tools, started } = await setup()
    await exec(tools.start_thread, { project: "vorneo", task: "Bump deps", inPlace: true })
    expect(s.launches[0]).not.toHaveProperty("workspaceStrategy")
    // A project with its own default model keeps it.
    await exec(tools.start_thread, { project: "site", task: "Fix CSS" })
    expect(s.launches[1]).not.toHaveProperty("modelSelection")
    expect(await exec(tools.start_thread, { project: "nope", task: "x" })).toMatchObject({ ok: false, error: expect.stringContaining("No T3 Code project") })
    await started.handle.stop()
  })

  it("says once when a thread waits for an answer", async () => {
    const { s, ctx, tools, started } = await setup()
    await exec(tools.start_thread, { project: "palui", task: "Fix it" })
    s.pending = 1
    await started.watcher.poll()
    await started.watcher.poll()
    const rows = ctx.db.select().from(outbox).all()
    expect(rows.map((r) => r.payload.text)).toEqual(["T3 Code nunggu jawaban kamu: Fix it\n\nhttps://app.t3.codes/threads/env1/11111111-aaaa"])
    await started.handle.stop()
  })

  it("sends follow-ups, reads and stops threads by id or link", async () => {
    const { s, tools, started } = await setup()
    const link = "https://app.t3.codes/threads/env1/11111111-aaaa"
    expect(await exec(tools.send_to_thread, { thread: link, message: "Also add a test" })).toMatchObject({ ok: true, thread: { id: link.split("/").pop(), status: "running" } })
    expect(s.sends[0]).toMatchObject({ threadId: "11111111-aaaa", message: "Also add a test", mode: "auto" })
    expect(started.watcher.list()[0]).toMatchObject({ threadId: "11111111-aaaa", runId: "run-1" })

    const read = (await exec(tools.get_thread, { thread: "11111111-aaaa" })) as { messages: { from: string }[] }
    expect(read.messages.map((m) => m.from)).toEqual(["owner", "agent"])
    expect(await exec(tools.list_threads, { project: "palui", activeOnly: true })).toMatchObject({ ok: true, threads: [{ id: "11111111-aaaa" }] })
    expect(await exec(tools.list_projects, {})).toMatchObject({ ok: true, projects: [{ name: "palui" }, { name: "vorneo" }, { name: "site" }] })

    expect(await exec(tools.stop_thread, { thread: link })).toEqual({ ok: true, threadId: "11111111-aaaa" })
    expect(s.interrupts).toEqual(["11111111-aaaa"])
    expect(await exec(tools.get_thread, { thread: "gone" })).toEqual({ ok: false, error: "Thread gone was not found." })
    await started.handle.stop()
  })

  it("lists only the threads started or followed from this chat", async () => {
    const { ctx, tools, turn, started } = await setup()
    expect(await exec(tools.list_chat_threads, {})).toEqual({ ok: true, chatId: turn.chatId, threads: [], message: "Belum ada thread T3 Code dari chat ini." })
    await exec(tools.start_thread, { project: "palui", task: "Fix it" })
    await exec(tools.send_to_thread, { thread: "11111111-aaaa", message: "More" })

    const other = ensureChat(ctx.db, { channel: "whatsapp", externalId: "628@s.whatsapp.net" })
    const otherTools = await started.handle.tools({ ...turn, chatId: other.id })
    expect(await exec(otherTools.list_chat_threads, {})).toMatchObject({ ok: true, chatId: other.id, threads: [] })

    const out = (await exec(tools.list_chat_threads, {})) as { threads: Record<string, unknown>[] }
    expect(out.threads).toEqual([
      { threadId: "11111111-aaaa", title: "Fix the bug", action: "followed", at: new Date(0).toISOString(), link: "https://app.t3.codes/threads/env1/11111111-aaaa", environmentId: "env1" },
    ])
    expect(await exec(tools.list_threads, {})).toMatchObject({ threads: [{ id: "11111111-aaaa", environmentId: "env1" }] })
    await started.handle.stop()
  })

  it("reads the environment id out of a thread link", () => {
    expect(environmentIdOf("https://app.t3.codes/threads/8b457a52-1111/mcp%3Alaunch%3Aabc")).toBe("8b457a52-1111")
    expect(environmentIdOf("https://app.t3.codes/projects/x")).toBeUndefined()
    expect(environmentIdOf(undefined)).toBeUndefined()
  })

  it("uses the configured model over T3 Code's first one", async () => {
    const s = fresh()
    const { url } = await server(s)
    const ctx = ctxFor(url, { token: TOKEN }, { model: "claudeAgent/claude-sonnet-5-5", baseRef: "develop" })
    const chat = ensureChat(ctx.db, { channel: "telegram", externalId: "42" })
    const started = startT3(ctx, TOKEN, { pollMs: 3_600_000 })
    const tools = await started.handle.tools({ runId: "r", chatId: chat.id, channel: "telegram", trigger: "message", tz: "UTC", now: 0 })
    await exec(tools.start_thread, { project: "palui", task: "Do it" })
    expect(s.launches[0]).toMatchObject({ modelSelection: { instanceId: "claudeAgent", model: "claude-sonnet-5-5" }, workspaceStrategy: { baseRef: "develop" } })
    await started.handle.stop()
  })

  it("drops watches for missing threads and after 24 hours", async () => {
    const s = fresh()
    const { url } = await server(s)
    const now = 10 * WATCH_TTL_MS
    const ctx = ctxFor(url, { token: TOKEN }, {
      watches: [
        { threadId: "gone", runId: null, chatId: "c", title: "Gone", since: now },
        { threadId: "11111111-aaaa", runId: "run-1", chatId: "c", title: "Old", since: now - WATCH_TTL_MS - 1 },
      ],
    })
    const client = new T3Client(url, TOKEN)
    const w = createWatcher(ctx, () => client, () => {})
    expect(w.list()).toHaveLength(2)
    await w.poll(now)
    expect(w.list()).toEqual([])
    expect(ctx.events.map((e) => e.kind)).toEqual(["t3code.watch_expired"])
    await client.close()
  })
})
