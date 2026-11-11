// T3 Code tools (start, follow and stop coding threads) plus a watcher that
// tells the chat when a thread Palui started is done. Watches live in the
// connector's config (runtime-learned, no restart), so they survive a restart.
import { createHash } from "node:crypto"
import { tool } from "ai"
import { eq } from "drizzle-orm"
import { z } from "zod"
import { chats, type DB } from "@palui/db"
import { defineRuntime, type ConnectorContext, type PaluiTool, type TurnInfo } from "@palui/core/connectors/define"
import { enqueueOutbox, notifyOutbox } from "@palui/core/outbox/enqueue"
import { codeFingerprint, isAuthError, matchProject, pairWithCode, parseModel, T3Client, T3Error, type T3Item, type T3ThreadSummary } from "./client"
import manifest, { t3Config } from "./manifest"

export const POLL_MS = 20_000
export const WATCH_TTL_MS = 24 * 3600_000
export const MAX_WATCHES = 20
export const MAX_HISTORY = 50
const REPLY_CHARS = 1500
const NOTICE_TTL_MS = 24 * 3600_000

type Config = z.infer<typeof t3Config>

// Runtime-learned keys kept next to the wizard's config. Never in the manifest
// schema, so the form doesn't show them.
const learned = z.object({
  pairedCode: z.string().optional(),
  tokenExpiresAt: z.number().nullable().optional(),
  watches: z
    .array(
      z.object({
        threadId: z.string(),
        runId: z.string().nullable(),
        chatId: z.string(),
        title: z.string(),
        link: z.string().optional(),
        since: z.number(),
        askedInput: z.boolean().optional(),
      }),
    )
    .optional(),
  // Threads started or followed up from each chat, newest last, so the model can
  // answer "which threads did I start here" after the watch is gone.
  history: z
    .array(
      z.object({
        threadId: z.string(),
        chatId: z.string(),
        title: z.string(),
        link: z.string().optional(),
        action: z.enum(["started", "followed"]),
        at: z.number(),
      }),
    )
    .optional(),
})

export type HistoryEntry = NonNullable<z.infer<typeof learned>["history"]>[number]

export type Watch = NonNullable<z.infer<typeof learned>["watches"]>[number]

type Ctx = ConnectorContext<Record<string, unknown>, Record<string, string | undefined>>

export function parseConfig(raw: unknown): Config {
  const r = t3Config.safeParse(raw ?? {})
  return r.success ? r.data : t3Config.parse({})
}

function learnedOf(raw: unknown) {
  const r = learned.safeParse(raw ?? {})
  return r.success ? r.data : {}
}

// The token for this connector, trading the pairing code once when needed.
export async function ensureToken(ctx: Ctx, config: Config, opts: { fetch?: typeof fetch; now?: number } = {}): Promise<string> {
  const code = ctx.secrets.pairingCode?.trim()
  const token = ctx.secrets.token
  const seen = learnedOf(ctx.config).pairedCode
  if (token && (!code || seen === codeFingerprint(code))) return token
  if (!code) throw new T3Error("Paste a pairing code from t3 auth pairing create --ttl 10m.", "no_code")
  const paired = await pairWithCode(config.url, code, opts)
  ctx.patchSecrets?.({ token: paired.token })
  ctx.patchConfig({ pairedCode: codeFingerprint(code), tokenExpiresAt: paired.expiresAt })
  // Later contexts are built from the row; keep this one in step too.
  ctx.secrets.token = paired.token
  ;(ctx.config as Record<string, unknown>).pairedCode = codeFingerprint(code)
  return paired.token
}

export function errorText(err: unknown): string {
  if (isAuthError(err)) return "T3 Code rejected Palui's token (it lasts 30 days). On the Mac, run t3 auth pairing create --ttl 10m and paste the new code in this connector."
  const msg = (err as Error)?.message ?? String(err)
  if (/ECONNREFUSED|fetch failed|ENOTFOUND|EHOSTUNREACH/i.test(msg)) return `Can't reach T3 Code at that URL. Is the app running? (${msg})`
  return msg
}

// Accept a thread id or a T3 Code thread link (…/threads/<env>/<threadId>).
// Ids of threads started over MCP look like "mcp:launch:<hex>" and are
// percent-encoded in links.
export function threadIdOf(input: string): string {
  const s = input.trim()
  const url = URL.parse(s)
  if (!url || !url.pathname.includes("/threads/")) return s
  const last = url.pathname.replace(/\/+$/, "").split("/").pop()
  if (!last) return s
  try {
    return decodeURIComponent(last)
  } catch {
    return last
  }
}

// The T3 Code environment in a thread link (…/threads/<environmentId>/<threadId>).
// It's the same for every thread on the owner's Mac, so it is not a thread id.
export function environmentIdOf(link: string | undefined): string | undefined {
  const url = link ? URL.parse(link) : null
  if (!url) return undefined
  const parts = url.pathname.split("/").filter(Boolean)
  const i = parts.indexOf("threads")
  return i >= 0 && parts.length >= i + 3 ? parts[i + 1] : undefined
}

const LINK_NOTE = "Links look like …/threads/<environmentId>/<threadId>. environmentId is the T3 Code install on the owner's Mac and is the same for every thread; threadId is the thread."

function shortHash(...parts: string[]): string {
  return createHash("sha256").update(parts.join("\0")).digest("hex").slice(0, 12)
}

function clip(text: string, max: number): string {
  const t = text.trim()
  return t.length <= max ? t : `${t.slice(0, max - 1).trimEnd()}…`
}

function threadView(t: T3ThreadSummary) {
  return {
    id: t.threadId,
    title: t.title,
    status: t.status,
    ...(t.link ? { link: t.link } : {}),
    ...(environmentIdOf(t.link) ? { environmentId: environmentIdOf(t.link) } : {}),
    ...(t.branch ? { branch: t.branch } : {}),
    ...(t.pendingRequestCount ? { waitingForAnswer: true } : {}),
    ...(t.pullRequests?.length ? { pullRequests: t.pullRequests.map((p) => p.url).filter(Boolean) } : {}),
    ...(t.updatedAt ? { updatedAt: t.updatedAt } : {}),
  }
}

// --- Watcher ---------------------------------------------------------------------------

function lastReply(items: T3Item[], runId: string | null): string | null {
  const replies = items.filter((i) => i.type === "assistant_message" && i.text)
  const own = runId ? replies.filter((i) => i.runId === runId) : []
  const pick = (own.length ? own : replies).at(-1)
  return pick?.text ?? null
}

export function finishedText(w: Watch, status: string, reply: string | null): string {
  const head = status === "completed" ? `T3 Code beres: ${w.title}` : `T3 Code berhenti (${status}): ${w.title}`
  return [head, reply ? clip(reply, REPLY_CHARS) : null, w.link ?? null].filter(Boolean).join("\n\n")
}

export function waitingText(w: Watch): string {
  return [`T3 Code nunggu jawaban kamu: ${w.title}`, w.link ?? null].filter(Boolean).join("\n\n")
}

function notify(db: DB, chatId: string, text: string, source: string, now: number): boolean {
  const chat = db.select().from(chats).where(eq(chats.id, chatId)).get()
  if (!chat) return false
  enqueueOutbox(db, { chatId, channel: chat.channel, payload: { text }, source, expiresAt: now + NOTICE_TTL_MS }, now)
  notifyOutbox()
  return true
}

export type Watcher = {
  add(w: Omit<Watch, "since">, now?: number): void
  list(): Watch[]
  remember(e: Omit<HistoryEntry, "at">, now?: number): void
  history(chatId: string): HistoryEntry[]
  poll(now?: number): Promise<void>
}

export function createWatcher(ctx: Ctx, client: () => T3Client, onError: (err: unknown) => void): Watcher {
  let watches: Watch[] = learnedOf(ctx.config).watches ?? []
  let history: HistoryEntry[] = learnedOf(ctx.config).history ?? []
  const save = () => ctx.patchConfig({ watches })
  let polling = false

  return {
    add(w, now = Date.now()) {
      watches = [...watches.filter((x) => x.threadId !== w.threadId), { ...w, since: now }].slice(-MAX_WATCHES)
      save()
    },
    list: () => watches,
    remember(e, now = Date.now()) {
      history = [...history.filter((x) => !(x.threadId === e.threadId && x.chatId === e.chatId)), { ...e, at: now }].slice(-MAX_HISTORY)
      ctx.patchConfig({ history })
    },
    history: (chatId) => history.filter((e) => e.chatId === chatId),
    async poll(now = Date.now()) {
      if (polling || !watches.length) return
      polling = true
      try {
        const keep: Watch[] = []
        let changed = false
        for (const w of watches) {
          if (now - w.since > WATCH_TTL_MS) {
            ctx.event("t3code.watch_expired", `Stopped watching T3 Code thread ${w.title} after 24 hours`, "info", { threadId: w.threadId })
            changed = true
            continue
          }
          try {
            const c = client()
            const waited = await c.peek(w.threadId, w.runId)
            if (waited.timedOut) {
              if (!w.askedInput) {
                const { thread } = await c.read({ threadId: w.threadId, limit: 1, maxCharsPerItem: 1 })
                if ((thread.pendingRequestCount ?? 0) > 0) {
                  notify(ctx.db, w.chatId, waitingText(w), `t3code:${w.threadId}`, now)
                  keep.push({ ...w, askedInput: true })
                  changed = true
                  continue
                }
              }
              keep.push(w)
              continue
            }
            let reply: string | null = null
            if (waited.status === "completed") {
              const read = await c.read({ threadId: w.threadId, limit: 6, maxCharsPerItem: REPLY_CHARS + 200 })
              reply = lastReply(read.items, waited.runId ?? w.runId)
            }
            notify(ctx.db, w.chatId, finishedText(w, waited.status, reply), `t3code:${w.threadId}`, now)
            changed = true
          } catch (err) {
            if (err instanceof T3Error && err.code === "thread_not_found") {
              changed = true
              continue
            }
            keep.push(w)
            onError(err)
          }
        }
        if (changed) {
          watches = keep
          save()
        }
      } finally {
        polling = false
      }
    },
  }
}

// --- Tools -----------------------------------------------------------------------------

type Fail = { ok: false; error: string }

const threadInput = z.string().min(1).describe("Thread id, or the thread's T3 Code link")

export function t3Tools(input: {
  client: () => T3Client
  config: Config
  turn: TurnInfo
  watcher: Watcher
  untrusted: boolean
  onError: (err: unknown) => void
}): Record<string, PaluiTool> {
  const { client, config, turn, watcher, untrusted } = input
  // Tool-level failures go back to the model; auth and network trouble also
  // flips the connector's status.
  const guard = async <T>(fn: () => Promise<T>): Promise<T | Fail> => {
    try {
      return await fn()
    } catch (err) {
      if (err instanceof T3Error) return { ok: false, error: err.message }
      input.onError(err)
      return { ok: false, error: errorText(err) }
    }
  }

  return {
    daftar_proyek: {
      class: "read",
      untrusted,
      title: "List T3 Code projects",
      tool: tool({
        description: "List the owner's T3 Code projects (repos on their Mac). Use the name with mulai_thread.",
        inputSchema: z.object({}),
        execute: () =>
          guard(async () => {
            const projects = await client().projects()
            return { ok: true, projects: projects.map((p) => ({ id: p.id, name: p.title, path: p.workspaceRoot })) }
          }),
      }),
    },
    daftar_thread: {
      class: "read",
      untrusted,
      title: "List T3 Code threads",
      tool: tool({
        description: `List recent T3 Code threads, newest first, in one project or across all of them. For the threads started from this chat, use thread_chat_ini. ${LINK_NOTE}`,
        inputSchema: z.object({
          project: z.string().optional().describe("Project name, id or path. Omit for all projects."),
          activeOnly: z.boolean().optional().describe("Only threads that are running, queued or waiting for an answer"),
        }),
        execute: ({ project, activeOnly }) =>
          guard(async () => {
            const c = client()
            let projectId: string | undefined
            if (project) {
              const m = matchProject(await c.projects(), project)
              if ("error" in m) return { ok: false, error: m.error } as Fail
              projectId = m.project.id
            }
            const statuses = activeOnly ? ["preparing", "queued", "starting", "running", "waiting"] : undefined
            const { threads } = await c.threads({ projectId, limit: 10, statuses })
            return { ok: true, threads: threads.map(threadView) }
          }),
      }),
    },
    mulai_thread: {
      class: "write",
      untrusted,
      title: "Start a T3 Code thread",
      tool: tool({
        description:
          "Start a new T3 Code thread: a coding agent on the owner's Mac works on `task` in the project. By default it runs in a fresh git worktree off the default base branch, so the owner's checkout is untouched. Write `task` as a complete brief, in the owner's words plus any detail from this chat. Palui messages this chat when the thread finishes, so don't promise to check back. Give the owner the returned link.",
        inputSchema: z.object({
          project: z.string().min(1).describe("Project name, id or path. Call daftar_proyek when unsure."),
          task: z.string().min(1).max(100_000).describe("The full instructions for the coding agent"),
          title: z.string().max(80).optional().describe("Short thread title. Defaults to the task's first line."),
          baseRef: z.string().max(200).optional().describe(`Branch, tag or commit for the new worktree. Default: ${config.baseRef}`),
          inPlace: z.boolean().optional().describe("Work in the project's own checkout instead of a new worktree. Only when the owner asks."),
        }),
        execute: ({ project, task, title, baseRef, inPlace }) =>
          guard(async () => {
            const c = client()
            const m = matchProject(await c.projects(), project)
            if ("error" in m) return { ok: false, error: m.error } as Fail
            const name = (title?.trim() || task.trim().split("\n")[0] || "Palui task").slice(0, 80)
            const base = inPlace ? undefined : baseRef?.trim() || config.baseRef
            // A project's own default model wins; otherwise the configured one,
            // then whatever T3 Code offers first.
            const model = m.project.defaultModelSelection ? undefined : (parseModel(config.model) ?? (await c.defaultModel()))
            const launched = await c.launch({
              projectId: m.project.id,
              ...(model ? { model } : {}),
              title: name,
              message: task,
              ...(base ? { baseRef: base } : {}),
              clientRequestId: `palui-${turn.runId}-${shortHash(m.project.id, task)}`,
            })
            if (launched.runId) watcher.add({ threadId: launched.threadId, runId: launched.runId, chatId: turn.chatId, title: name, ...(launched.link ? { link: launched.link } : {}) })
            watcher.remember({ threadId: launched.threadId, chatId: turn.chatId, title: name, action: "started", ...(launched.link ? { link: launched.link } : {}) }, turn.now)
            const environmentId = environmentIdOf(launched.link)
            return {
              ok: true,
              threadId: launched.threadId,
              ...(environmentId ? { environmentId } : {}),
              title: name,
              project: m.project.title,
              workspace: base ? `new worktree from ${base}` : "project checkout",
              ...(launched.link ? { link: launched.link } : {}),
              note: launched.runId ? "Palui will message this chat when the thread finishes." : "The thread was created idle.",
            }
          }),
      }),
    },
    kirim_ke_thread: {
      class: "write",
      untrusted,
      title: "Message a T3 Code thread",
      tool: tool({
        description:
          "Send a follow-up to a T3 Code thread. A running thread gets it as a mid-turn update or its next turn; an idle one starts working. Palui messages this chat when that run finishes.",
        inputSchema: z.object({ thread: threadInput, message: z.string().min(1).max(100_000) }),
        execute: ({ thread, message }) =>
          guard(async () => {
            const c = client()
            const threadId = threadIdOf(thread)
            await c.send({ threadId, message, clientRequestId: `palui-${turn.runId}-${shortHash(threadId, message)}` })
            const { thread: t } = await c.read({ threadId, limit: 1, maxCharsPerItem: 1 })
            const runId = t.activeRunId ?? t.latestRunId ?? null
            watcher.add({ threadId, runId, chatId: turn.chatId, title: t.title, ...(t.link ? { link: t.link } : {}) })
            watcher.remember({ threadId, chatId: turn.chatId, title: t.title, action: "followed", ...(t.link ? { link: t.link } : {}) }, turn.now)
            return { ok: true, thread: threadView(t), note: "Palui will message this chat when it finishes." }
          }),
      }),
    },
    thread_chat_ini: {
      class: "read",
      untrusted,
      title: "List this chat's T3 Code threads",
      tool: tool({
        description: `List the T3 Code threads started or followed up from this chat, newest first, with their thread ids and links. Only this chat's threads. ${LINK_NOTE}`,
        inputSchema: z.object({}),
        execute: async () => {
          const threads = watcher
            .history(turn.chatId)
            .toReversed()
            .map((e) => ({ threadId: e.threadId, title: e.title, action: e.action, at: new Date(e.at).toISOString(), ...(e.link ? { link: e.link, environmentId: environmentIdOf(e.link) } : {}) }))
          return { ok: true, chatId: turn.chatId, threads, ...(threads.length ? {} : { message: "Belum ada thread T3 Code dari chat ini." }) }
        },
      }),
    },
    lihat_thread: {
      class: "read",
      untrusted,
      title: "Check a T3 Code thread",
      tool: tool({
        description: "Read a T3 Code thread's status, branch, pull requests and its latest messages.",
        inputSchema: z.object({ thread: threadInput }),
        execute: ({ thread }) =>
          guard(async () => {
            const read = await client().read({ threadId: threadIdOf(thread), limit: 6, maxCharsPerItem: REPLY_CHARS })
            const messages = read.items
              .filter((i) => (i.type === "user_message" || i.type === "assistant_message") && i.text)
              .map((i) => ({ from: i.type === "user_message" ? "owner" : "agent", text: i.text, ...(i.textTruncated ? { cut: true } : {}) }))
            return { ok: true, thread: threadView(read.thread), messages }
          }),
      }),
    },
    hentikan_thread: {
      class: "destructive",
      untrusted,
      title: "Stop a T3 Code thread",
      tool: tool({
        description: "Interrupt a T3 Code thread's running turn. Its work so far stays in the worktree.",
        inputSchema: z.object({ thread: threadInput }),
        execute: ({ thread }) =>
          guard(async () => {
            const threadId = threadIdOf(thread)
            await client().interrupt({ threadId, clientRequestId: `palui-${turn.runId}-stop-${threadId}` })
            return { ok: true, threadId }
          }),
      }),
    },
  }
}

// --- Runtime ---------------------------------------------------------------------------

export function startT3(ctx: Ctx, token: string, opts: { fetch?: typeof fetch; pollMs?: number } = {}) {
  const config = parseConfig(ctx.config)
  let current = new T3Client(config.url, token, opts.fetch)
  let lastError: string | null = null
  const onError = (err: unknown) => {
    const detail = errorText(err)
    if (detail === lastError) return
    lastError = detail
    ctx.setStatus("error", detail)
  }
  const onOk = () => {
    if (!lastError) return
    lastError = null
    ctx.setStatus("on")
  }
  const client = () => current
  const watcher = createWatcher(ctx, client, onError)
  const timer = setInterval(() => void watcher.poll().catch(onError), opts.pollMs ?? POLL_MS)
  timer.unref?.()
  const untrusted = ctx.trust === "external"

  return {
    watcher,
    handle: {
      async stop() {
        clearInterval(timer)
        await current.close()
      },
      async tools(turn: TurnInfo) {
        // A connector in error keeps offering its tools: each call retries, and a
        // success clears the status.
        if (lastError) {
          try {
            await current.projects()
            onOk()
          } catch (err) {
            onError(err)
            await current.close()
            current = new T3Client(config.url, token, opts.fetch)
          }
        }
        return t3Tools({ client, config, turn, watcher, untrusted, onError })
      },
      live() {
        const l = learnedOf(ctx.config)
        return { url: config.url, watching: watcher.list().length, tokenExpiresAt: l.tokenExpiresAt ?? null, error: lastError }
      },
    },
  }
}

export default defineRuntime(manifest, {
  async test(ctx) {
    const config = parseConfig(ctx.config)
    let client: T3Client | null = null
    try {
      client = new T3Client(config.url, await ensureToken(ctx as unknown as Ctx, config))
      const projects = await client.projects()
      return { ok: true, detail: `Connected to T3 Code: ${projects.length} project${projects.length === 1 ? "" : "s"}` }
    } catch (err) {
      return { ok: false, detail: errorText(err) }
    } finally {
      await client?.close()
    }
  },

  async start(ctx) {
    const config = parseConfig(ctx.config)
    let token: string
    try {
      token = await ensureToken(ctx as unknown as Ctx, config)
      const probe = new T3Client(config.url, token)
      try {
        await probe.projects()
      } finally {
        await probe.close()
      }
    } catch (err) {
      throw new Error(errorText(err))
    }
    return startT3(ctx as unknown as Ctx, token).handle
  },
})
