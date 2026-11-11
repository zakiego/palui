// The control API (packages/core/src/control.ts) on node:http. Compose network
// only; every route except GET /live needs the derived token in x-palui-control.
import http from "node:http"
import { schemaState, type DB } from "@palui/db"
import { safeEqual } from "@palui/db/crypto"
import type { TurnBus } from "@palui/core/chat/bus"
import type { EngineDeps } from "@palui/core/chat/deps"
import type { Engine } from "@palui/core/chat/engine"
import { MODEL_OFF_TEXT } from "@palui/core/chat/turn"
import { AgentError } from "@palui/core/agents"
import { WEB_CHAT_EXTERNAL_ID } from "@palui/core/chats"
import { CONTROL_HEADER, type RuntimeStatus } from "@palui/core/control"
import type { Logger } from "@palui/core/log"
import { PALUI_VERSION } from "@palui/core/version"
import { failoverModel, resolveChain } from "@palui/core/models/failover"
import { rerouteChannelRows } from "@palui/core/outbox/worker"
import type { Supervisor } from "@palui/core/connectors/supervisor"
import { getSetting } from "@palui/core/settings"

export const SSE_KEEPALIVE_MS = 15_000
const MAX_BODY = 1024 * 1024
const MAX_LOOP_LAG_MS = 10_000

export type ControlServerOptions = {
  db: DB
  token: string
  engine: Engine
  supervisor: Supervisor
  deps: EngineDeps
  bus: TurnBus
  log: Logger
  startedAt: number
  clock?: () => number
}

class HttpError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

function send(res: http.ServerResponse, status: number, body: unknown) {
  const text = JSON.stringify(body)
  res.writeHead(status, { "content-type": "application/json; charset=utf-8", "content-length": Buffer.byteLength(text) })
  res.end(text)
}

async function readJson(req: http.IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = []
  let size = 0
  for await (const c of req) {
    size += (c as Buffer).length
    if (size > MAX_BODY) throw new HttpError(413, "Body too large")
    chunks.push(c as Buffer)
  }
  if (!size) return {}
  try {
    const v = JSON.parse(Buffer.concat(chunks).toString("utf8"))
    return v && typeof v === "object" ? (v as Record<string, unknown>) : {}
  } catch {
    throw new HttpError(400, "Body isn't valid JSON")
  }
}

export function authorized(header: string | string[] | undefined, token: string): boolean {
  const got = Array.isArray(header) ? header[0] : header
  return !!got && !!token && safeEqual(got, token)
}

// Where the control API listens (PLAN 10: compose network only). In prod the app
// reaches it over the compose network, so it binds every interface. In dev the
// token comes from the public DEV_MASTER_KEY, so it stays on loopback.
// CONTROL_HOST overrides both.
export function controlHost(env: Record<string, string | undefined> = process.env): string {
  if (env.CONTROL_HOST) return env.CONTROL_HOST
  return env.NODE_ENV === "production" ? "0.0.0.0" : "127.0.0.1"
}

export function createControlServer(o: ControlServerOptions) {
  const clock = o.clock ?? Date.now
  // Event loop lag, for /live: a stuck loop answers late or not at all, but a
  // loop that was blocked for a long while recently is reported too.
  let lastTick = Date.now()
  let lag = 0
  const lagTimer = setInterval(() => {
    const now = Date.now()
    lag = Math.max(0, now - lastTick - 1_000)
    lastTick = now
  }, 1_000)
  lagTimer.unref()
  const streams = new Set<http.ServerResponse>()

  function live(): { status: number; body: { ok: boolean; schema: string; version: string } } {
    let schema = "unknown"
    let ok = lag < MAX_LOOP_LAG_MS
    try {
      o.db.$client.prepare("SELECT 1").get()
      const st = schemaState(o.db.$client)
      if (st.ok) schema = st.version
      else ok = false
    } catch {
      ok = false
    }
    return { status: ok ? 200 : 503, body: { ok, schema, version: PALUI_VERSION } }
  }

  function status(): RuntimeStatus {
    const st = schemaState(o.db.$client)
    return {
      ok: true,
      startedAt: o.startedAt,
      schema: st.ok ? st.version : `not ok: ${st.reason}`,
      connectors: o.supervisor.status(),
      turnsPaused: { until: getSetting(o.db, "turnsPausedUntil"), reason: getSetting(o.db, "turnsPausedReason") },
      activeTurns: o.engine.activeTurns(),
    }
  }

  function stream(req: http.IncomingMessage, res: http.ServerResponse) {
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache, no-transform", connection: "keep-alive", "x-accel-buffering": "no" })
    res.write(": connected\n\n")
    streams.add(res)
    const unsubscribe = o.bus.subscribe((e) => {
      res.write(`data: ${JSON.stringify(e)}\n\n`)
    })
    const keepalive = setInterval(() => res.write(": keepalive\n\n"), SSE_KEEPALIVE_MS)
    const close = () => {
      clearInterval(keepalive)
      unsubscribe()
      streams.delete(res)
    }
    req.on("close", close)
    res.on("close", close)
  }

  async function handle(req: http.IncomingMessage, res: http.ServerResponse) {
    const url = new URL(req.url ?? "/", "http://runtime")
    const path = url.pathname.replace(/\/+$/, "") || "/"
    const method = req.method ?? "GET"

    if (method === "GET" && path === "/live") {
      const l = live()
      return send(res, l.status, l.body)
    }
    if (!authorized(req.headers[CONTROL_HEADER], o.token)) throw new HttpError(401, "Missing or wrong control token")

    if (method === "GET" && path === "/control/status") return send(res, 200, status())
    if (method === "GET" && path === "/control/stream") return stream(req, res)
    if (method !== "POST") throw new HttpError(404, `No route ${method} ${path}`)

    const body = await readJson(req)
    let m: RegExpExecArray | null

    if (path === "/control/reconcile") {
      void o.supervisor.reconcile()
      o.deps.scheduler?.poke()
      // Hook events arrive as pending inbox rows the app wrote.
      void o.engine.replayPending()
      return send(res, 200, { ok: true })
    }
    if ((m = /^\/control\/test\/([^/]+)$/.exec(path))) return send(res, 200, await o.supervisor.test(decodeURIComponent(m[1]!)))
    if ((m = /^\/control\/action\/([^/]+)$/.exec(path))) {
      if (typeof body.name !== "string" || !body.name) throw new HttpError(400, "name is required")
      try {
        return send(res, 200, await o.supervisor.action(decodeURIComponent(m[1]!), body.name, (body.input as Record<string, unknown>) ?? {}))
      } catch (e) {
        const st = (e as { status?: number }).status
        throw new HttpError(st ?? 500, (e as Error).message)
      }
    }
    if ((m = /^\/control\/remove\/([^/]+)$/.exec(path))) {
      const r = await o.supervisor.remove(decodeURIComponent(m[1]!), (ch) => rerouteChannelRows(o.db, ch, clock(), o.supervisor.channel))
      return send(res, r.ok ? 200 : 404, r)
    }
    if (path === "/control/web/send") {
      const text = typeof body.text === "string" ? body.text : ""
      if (!text.trim()) throw new HttpError(400, "text is required")
      const r = await o.engine.inbound({ channel: "web", connectorId: "web", chatExternalId: WEB_CHAT_EXTERNAL_ID, chatKind: "dm", chatTitle: "Palui (web)", senderExternalId: "owner", text, at: clock() })
      if (!r) throw new HttpError(500, "The message was dropped")
      return send(res, 200, { ok: true, chatId: r.chatId, messageId: r.messageId })
    }
    if ((m = /^\/control\/approvals\/([^/]+)$/.exec(path))) {
      if (typeof body.approved !== "boolean") throw new HttpError(400, "approved must be true or false")
      return send(res, 200, { ok: await o.engine.decideApproval(decodeURIComponent(m[1]!), body.approved, "dashboard") })
    }
    if ((m = /^\/control\/jobs\/([^/]+)\/run$/.exec(path))) {
      if (!o.deps.jobs) throw new HttpError(501, "Jobs aren't wired into this runtime build yet")
      return send(res, 200, await o.deps.jobs.runNow(decodeURIComponent(m[1]!)))
    }
    if ((m = /^\/control\/stop\/([^/]+)$/.exec(path))) return send(res, 200, { ok: o.engine.stopChat(decodeURIComponent(m[1]!)) })
    if ((m = /^\/control\/chats\/([^/]+)\/agent$/.exec(path))) {
      const agentId = body.agentId === null ? null : typeof body.agentId === "string" && body.agentId ? body.agentId : undefined
      if (agentId === undefined) throw new HttpError(400, "agentId must be an agent id or null")
      try {
        await o.engine.switchAgent(decodeURIComponent(m[1]!), agentId)
      } catch (e) {
        throw new HttpError(e instanceof AgentError ? 400 : 404, (e as Error).message)
      }
      return send(res, 200, { ok: true })
    }
    if (path === "/control/reminders/parse") {
      if (!o.deps.parseReminder) throw new HttpError(501, "Reminder parsing isn't wired into this runtime build yet")
      const text = typeof body.text === "string" ? body.text : ""
      if (!text.trim()) throw new HttpError(400, "text is required")
      const chain = resolveChain(o.db, o.supervisor, "fast")
      if (!chain.length) throw new HttpError(503, MODEL_OFF_TEXT)
      const tz = typeof body.tz === "string" && body.tz ? body.tz : getSetting(o.db, "timezone")
      return send(res, 200, await o.deps.parseReminder(text, tz, clock(), failoverModel(chain)))
    }
    throw new HttpError(404, `No route ${method} ${path}`)
  }

  const server = http.createServer((req, res) => {
    handle(req, res).catch((e) => {
      const status = e instanceof HttpError ? e.status : 500
      if (status >= 500) o.log.error("control request failed", { path: req.url, error: e })
      if (!res.headersSent) send(res, status, { error: (e as Error).message })
      else res.end()
    })
  })

  return {
    server,
    listen(port: number, host = controlHost()): Promise<number> {
      return new Promise((resolve, reject) => {
        server.once("error", reject)
        server.listen(port, host, () => {
          const addr = server.address()
          resolve(typeof addr === "object" && addr ? addr.port : port)
        })
      })
    },
    async close() {
      clearInterval(lagTimer)
      for (const s of streams) s.end()
      await new Promise<void>((resolve) => server.close(() => resolve()))
    },
  }
}
