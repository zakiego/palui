import { afterEach, describe, expect, it } from "vitest"
import { memoryDb, migrations, type DB } from "@palui/db"
import { createTurnBus } from "@palui/core/chat/bus"
import { getControlToken, loadConfig } from "@palui/core/config"
import { CONTROL_HEADER, type TurnEvent } from "@palui/core/control"
import { silentLogger } from "@palui/core/log"
import { getConnector } from "@palui/core/connectors/store"
import { authorized, controlHost } from "./control"
import { SchemaMismatchError, startRuntime, type Runtime } from "./main"

const cfg = loadConfig({ MASTER_KEY: "runtime-test-master-key-0123456789abcdef", DATA_DIR: "/tmp/palui-runtime-test" })
const token = getControlToken(cfg)
let rt: Runtime | undefined
// biome-ignore lint: test responses are loosely typed.
const json = async (r: Response): Promise<any> => r.json()

afterEach(async () => {
  await rt?.stop()
  rt = undefined
})

async function boot(db: DB = memoryDb(), deps = {}) {
  const bus = createTurnBus()
  rt = await startRuntime({ db, config: cfg, port: 0, host: "127.0.0.1", log: silentLogger, bus, deps })
  const base = `http://127.0.0.1:${rt.port}`
  const call = (method: string, path: string, body?: unknown, headers: Record<string, string> = { [CONTROL_HEADER]: token }) =>
    fetch(`${base}${path}`, { method, headers: { ...headers, ...(body ? { "content-type": "application/json" } : {}) }, body: body ? JSON.stringify(body) : undefined })
  return { rt, base, call, bus, db }
}

describe("control server", () => {
  it("binds loopback in dev and every interface in prod, unless CONTROL_HOST says otherwise", () => {
    expect(controlHost({})).toBe("127.0.0.1")
    expect(controlHost({ NODE_ENV: "development" })).toBe("127.0.0.1")
    expect(controlHost({ NODE_ENV: "production" })).toBe("0.0.0.0")
    expect(controlHost({ NODE_ENV: "production", CONTROL_HOST: "10.0.0.5" })).toBe("10.0.0.5")
    expect(controlHost({ CONTROL_HOST: "0.0.0.0" })).toBe("0.0.0.0")
  })

  it("compares the token timing-safely", () => {
    expect(authorized(token, token)).toBe(true)
    expect(authorized(`${token}x`, token)).toBe(false)
    expect(authorized(undefined, token)).toBe(false)
    expect(authorized("", "")).toBe(false)
  })

  it("serves /live without a token and refuses everything else without one", async () => {
    const { call } = await boot()
    const live = await call("GET", "/live", undefined, {})
    expect(live.status).toBe(200)
    expect(await live.json()).toMatchObject({ ok: true, schema: migrations.at(-1)!.name })
    expect((await call("GET", "/control/status", undefined, {})).status).toBe(401)
    expect((await call("GET", "/control/status", undefined, { [CONTROL_HEADER]: "wrong" })).status).toBe(401)
    expect((await call("POST", "/control/reconcile", {}, {})).status).toBe(401)
    const st = await call("GET", "/control/status")
    expect(st.status).toBe(200)
    const body = await json(st)
    expect(body.connectors).toContainEqual(expect.objectContaining({ id: "web", running: true, status: "on" }))
    expect(body.turnsPaused).toEqual({ until: null, reason: null })
  })

  it("creates the builtin web connector and takes web chat messages", async () => {
    const { call, db } = await boot()
    expect(getConnector(db, "web")).toMatchObject({ enabled: true, status: "on" })
    const r = await call("POST", "/control/web/send", { text: "halo" })
    expect(r.status).toBe(200)
    const body = await json(r)
    expect(body).toMatchObject({ ok: true })
    expect(body.chatId).toMatch(/^chat_/)
    expect((await call("POST", "/control/web/send", { text: "" })).status).toBe(400)
  })

  it("streams turn events over SSE", async () => {
    const { base, bus } = await boot()
    const ac = new AbortController()
    const res = await fetch(`${base}/control/stream`, { headers: { [CONTROL_HEADER]: token }, signal: ac.signal })
    expect(res.headers.get("content-type")).toBe("text/event-stream")
    const reader = res.body!.getReader()
    const ev: TurnEvent = { type: "turn-start", chatId: "c1", runId: "r1" }
    let text = ""
    const dec = new TextDecoder()
    text += dec.decode((await reader.read()).value)
    bus.publish(ev)
    while (!text.includes("data:")) text += dec.decode((await reader.read()).value)
    expect(text).toContain(`data: ${JSON.stringify(ev)}`)
    ac.abort()
  })

  it("answers 501 when a dependency isn't wired, and 503 for reminder parsing without a model", async () => {
    const { call } = await boot()
    const job = await call("POST", "/control/jobs/job_1/run", {})
    expect(job.status).toBe(501)
    expect((await json(job)).error).toContain("Jobs")
    expect((await call("POST", "/control/reminders/parse", { text: "besok jam 9" })).status).toBe(501)
    await rt!.stop()
    rt = undefined
    const b = await boot(memoryDb(), { parseReminder: async () => ({ ok: true, text: "x", tz: "Asia/Makassar" }) })
    const r = await b.call("POST", "/control/reminders/parse", { text: "besok jam 9" })
    expect(r.status).toBe(503)
    expect((await json(r)).error).toContain("Model lagi off")
  })

  it("routes approvals, stop and unknown paths", async () => {
    const { call } = await boot()
    expect(await json(await call("POST", "/control/approvals/apr_missing", { approved: true }))).toEqual({ ok: false })
    expect((await call("POST", "/control/approvals/apr_missing", {})).status).toBe(400)
    expect(await json(await call("POST", "/control/stop/chat_x", {}))).toEqual({ ok: false })
    expect((await call("POST", "/control/nope", {})).status).toBe(404)
    expect((await call("POST", "/control/remove/nope", {})).status).toBe(404)
  })
})

describe("startRuntime", () => {
  it("refuses to start on a schema it doesn't know", async () => {
    const db = memoryDb()
    db.$client.prepare("DELETE FROM _migrations WHERE name = ?").run(migrations.at(-1)!.name)
    await expect(startRuntime({ db, config: cfg, port: 0, host: "127.0.0.1", log: silentLogger })).rejects.toBeInstanceOf(SchemaMismatchError)
  })
})

describe("removeConnectorFiles", () => {
  it("deletes a removed WhatsApp connector's auth dir and leaves other types alone", async () => {
    const { mkdtempSync, mkdirSync, writeFileSync, existsSync } = await import("node:fs")
    const { tmpdir } = await import("node:os")
    const { join } = await import("node:path")
    const { removeConnectorFiles } = await import("./main")
    const dir = mkdtempSync(join(tmpdir(), "palui-rm-"))
    mkdirSync(join(dir, "wa-auth", "whatsapp"), { recursive: true })
    writeFileSync(join(dir, "wa-auth", "whatsapp", "creds.json"), "{}")
    mkdirSync(join(dir, "wa-auth", "telegram"), { recursive: true })
    const cleanup = removeConnectorFiles(dir)
    await cleanup({ id: "telegram", type: "telegram" } as never)
    expect(existsSync(join(dir, "wa-auth", "telegram"))).toBe(true)
    await cleanup({ id: "whatsapp", type: "whatsapp" } as never)
    expect(existsSync(join(dir, "wa-auth", "whatsapp"))).toBe(false)
  })
})
