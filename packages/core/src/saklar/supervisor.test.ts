import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { z } from "zod"
import { memoryDb, outbox } from "@palui/db"
import { fakeChannel, seedConnector, TEST_KEY } from "../chat/testing"
import { listEvents } from "../events"
import { silentLogger } from "../log"
import { claimOwner, createClaim } from "../owner"
import { defineManifest, defineRuntime, type ConnectorServices, type RunningConnector } from "./define"
import { getConnector, pauseConnector, resumeConnector, upsertConnector } from "./store"
import { createSupervisor, ERROR_ALERT_AFTER_MS } from "./supervisor"

const services: ConnectorServices = { inbound: async () => {}, claimOwner: () => Promise.resolve(false), isOwner: () => false }

function fakeRuntime(id: string, kind: "channel" | "tools" | "skill", behavior: { fail?: () => boolean; reportStatus?: "connecting"; channel?: ReturnType<typeof fakeChannel> } = {}) {
  const starts: number[] = []
  const stops: number[] = []
  const manifest = defineManifest({ id, kind, name: id, icon: "x", summary: "", config: z.object({}), secrets: {}, setup: [], builtin: kind === "skill" })
  const runtime = defineRuntime(manifest, {
    test: async () => ({ ok: true, detail: "fine" }),
    start: async (ctx) => {
      starts.push(Date.now())
      if (behavior.fail?.()) throw new Error(`${id} exploded`)
      if (behavior.reportStatus) ctx.setStatus(behavior.reportStatus, "waiting for pairing")
      const h: RunningConnector = { stop: async () => void stops.push(1), channel: behavior.channel, action: async (name) => ({ did: name }) }
      return h
    },
  })
  return { runtime, starts, stops, manifest }
}

function setup(registry: Record<string, ReturnType<typeof fakeRuntime>>) {
  const db = memoryDb()
  let now = 1_000_000
  const sup = createSupervisor({
    db,
    registry: Object.fromEntries(Object.entries(registry).map(([k, v]) => [k, v.runtime])),
    services,
    log: silentLogger,
    masterKey: TEST_KEY,
    dataDir: "/tmp/palui-test",
    clock: () => now,
  })
  return {
    db,
    sup,
    advance: (ms: number) => {
      now += ms
    },
    get now() {
      return now
    },
  }
}

describe("saklar supervisor", () => {
  it("starts enabled connectors (connecting -> on) and keeps a status the connector reports", async () => {
    const a = fakeRuntime("alpha", "tools")
    const wa = fakeRuntime("whatsapp", "channel", { reportStatus: "connecting", channel: fakeChannel("whatsapp") })
    const t = setup({ alpha: a, whatsapp: wa })
    seedConnector(t.db, "alpha", "tools", { status: "connecting" })
    seedConnector(t.db, "whatsapp", "channel", { status: "connecting" })
    await t.sup.reconcile()
    expect(getConnector(t.db, "alpha")!.status).toBe("on")
    expect(getConnector(t.db, "whatsapp")).toMatchObject({ status: "connecting", statusDetail: "waiting for pairing" })
    expect(t.sup.running().map((e) => e.id).sort()).toEqual(["alpha", "whatsapp"])
    expect(t.sup.channel("whatsapp")).toBeDefined()
    await t.sup.reconcile()
    expect(a.starts).toHaveLength(1)
  })

  it("stops a paused connector and keeps it paused, restarts on resume and on config change", async () => {
    const a = fakeRuntime("alpha", "tools")
    const t = setup({ alpha: a })
    seedConnector(t.db, "alpha", "tools")
    await t.sup.reconcile()
    pauseConnector(t.db, "alpha", "test")
    // Pausing applies from the next turn even before the reconcile.
    expect(t.sup.running()).toHaveLength(0)
    await t.sup.reconcile()
    expect(a.stops).toHaveLength(1)
    expect(getConnector(t.db, "alpha")!.status).toBe("paused")
    resumeConnector(t.db, "alpha", "test")
    await t.sup.reconcile()
    expect(a.starts).toHaveLength(2)
    expect(getConnector(t.db, "alpha")!.status).toBe("on")
    upsertConnector(t.db, { id: "alpha", manifest: a.manifest, config: { x: 2 } }, TEST_KEY)
    await t.sup.reconcile()
    expect(a.stops).toHaveLength(2)
    expect(a.starts).toHaveLength(3)
  })

  it("waits for a running turn before stopping a paused connector", async () => {
    const a = fakeRuntime("alpha", "tools")
    let busy = true
    const db = memoryDb()
    const sup = createSupervisor({ db, registry: { alpha: a.runtime }, services, log: silentLogger, masterKey: TEST_KEY, dataDir: "/tmp", inUse: () => busy })
    seedConnector(db, "alpha", "tools")
    await sup.reconcile()
    pauseConnector(db, "alpha", "test")
    await sup.reconcile()
    expect(a.stops).toHaveLength(0)
    busy = false
    await sup.reconcile()
    expect(a.stops).toHaveLength(1)
  })

  it("puts a throwing connector in error, retries with backoff and alerts the owner once after 5 minutes", async () => {
    let failing = true
    const bad = fakeRuntime("bad", "tools", { fail: () => failing })
    const tgChannel = fakeChannel("telegram")
    const tg = fakeRuntime("telegram", "channel", { channel: tgChannel })
    const t = setup({ bad, telegram: tg })
    const { nonce } = createClaim(t.db, "telegram", 1)
    claimOwner(t.db, { channel: "telegram", nonce, externalId: "42" }, 2)
    seedConnector(t.db, "telegram", "channel")
    seedConnector(t.db, "bad", "tools", { status: "connecting" })
    await t.sup.reconcile()
    expect(getConnector(t.db, "bad")!.status).toBe("error")
    expect(bad.starts).toHaveLength(1)
    // Backoff: 5 s, 30 s, 2 min, 10 min cap.
    const waits = [5_000, 30_000, 120_000, 600_000, 600_000]
    for (const w of waits) {
      t.advance(w - 1)
      await t.sup.reconcile()
      const before = bad.starts.length
      t.advance(1)
      await t.sup.reconcile()
      expect(bad.starts.length).toBe(before + 1)
    }
    // errorSince kept counting across retries, so the alert went out once.
    const alerts = t.db.select().from(outbox).where(eq(outbox.source, "system")).all()
    expect(alerts).toHaveLength(1)
    expect(alerts[0]!.payload.text).toContain("bad")
    expect(alerts[0]!.channel).toBe("telegram")
    expect(listEvents(t.db).filter((e) => e.kind === "alert")).toHaveLength(1)
    t.advance(ERROR_ALERT_AFTER_MS)
    await t.sup.reconcile()
    expect(t.db.select().from(outbox).where(eq(outbox.source, "system")).all()).toHaveLength(1)
    failing = false
    t.advance(600_000)
    await t.sup.reconcile()
    expect(getConnector(t.db, "bad")).toMatchObject({ status: "on", errorSince: null, alertedAt: null })
  })

  it("creates builtin rows on boot and survives a throwing stop", async () => {
    const skill = fakeRuntime("reminders", "skill")
    const t = setup({ reminders: skill })
    t.sup.ensureBuiltins([skill.manifest])
    expect(getConnector(t.db, "reminders")).toMatchObject({ enabled: true, status: "connecting" })
    await t.sup.reconcile()
    expect(getConnector(t.db, "reminders")!.status).toBe("on")
  })

  it("removes: unlinks, stops, reroutes and deletes the row", async () => {
    const tg = fakeRuntime("telegram", "channel", { channel: fakeChannel("telegram") })
    const t = setup({ telegram: tg })
    seedConnector(t.db, "telegram", "channel")
    await t.sup.reconcile()
    const rerouted: string[] = []
    const res = await t.sup.remove("telegram", (ch) => rerouted.push(ch))
    expect(res.ok).toBe(true)
    expect(tg.stops).toHaveLength(1)
    expect(rerouted).toEqual(["telegram"])
    expect(getConnector(t.db, "telegram")).toBeUndefined()
    expect(t.sup.status()).toEqual([])
  })
})

describe("saklar supervisor: errors after start and remove", () => {
  type Extra = { restartOnError?: boolean; unlinkOnRemove?: boolean }
  function live(id: string, kind: "channel" | "tools", extra: Extra = {}, startFails?: () => boolean) {
    const ctxs: import("./define").ConnectorContext[] = []
    const stops: number[] = []
    const actions: string[] = []
    const manifest = defineManifest({ id, kind, name: id, icon: "x", summary: "", config: z.object({}), secrets: {}, setup: [], ...extra })
    const runtime = defineRuntime(manifest, {
      test: async () => ({ ok: true, detail: "" }),
      start: async (ctx) => {
        if (startFails?.()) throw new Error(`${id} exploded`)
        ctxs.push(ctx as never)
        return {
          stop: async () => void stops.push(1),
          action: async (name) => {
            actions.push(name)
            if (name !== "unlink") throw new Error(`Unknown ${id} action: ${name}`)
            return { ok: true }
          },
        }
      },
    })
    return { runtime, ctxs, stops, actions, manifest }
  }

  function boot(registry: Record<string, ReturnType<typeof live>>, onRemoved?: (row: { id: string }, info: { unlinked: boolean }) => Promise<void>) {
    const db = memoryDb()
    const clock = { now: 1_000_000 }
    const sup = createSupervisor({
      db,
      registry: Object.fromEntries(Object.entries(registry).map(([k, v]) => [k, v.runtime])),
      services,
      log: silentLogger,
      masterKey: TEST_KEY,
      dataDir: "/tmp/palui-test",
      clock: () => clock.now,
      backoffMs: [20, 40, 80],
      onRemoved,
    })
    return { db, sup, clock }
  }
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

  it("restarts a channel whose running handle reports an error, on the backoff ladder", async () => {
    const tg = live("telegram", "channel")
    const t = boot({ telegram: tg })
    seedConnector(t.db, "telegram", "channel")
    await t.sup.reconcile()
    expect(tg.ctxs).toHaveLength(1)
    tg.ctxs[0]!.setStatus("error", "409: Conflict: terminated by other getUpdates request")
    await sleep(5)
    expect(tg.stops).toHaveLength(1)
    expect(t.sup.isRunning("telegram")).toBe(false)
    expect(getConnector(t.db, "telegram")).toMatchObject({ status: "error", statusDetail: "409: Conflict: terminated by other getUpdates request" })
    // A report from the dead handle is ignored.
    tg.ctxs[0]!.setStatus("on")
    expect(getConnector(t.db, "telegram")!.status).toBe("error")
    // The retry timer fires without waiting for the 30 s reconcile.
    t.clock.now += 20
    await sleep(60)
    expect(tg.ctxs).toHaveLength(2)
    expect(t.sup.isRunning("telegram")).toBe(true)
    // Crashing again right away moves up the ladder (40 ms, not 20 ms).
    tg.ctxs[1]!.setStatus("error", "409 again")
    await sleep(5)
    t.clock.now += 20
    await t.sup.reconcile()
    expect(tg.ctxs).toHaveLength(2)
    t.clock.now += 20
    await t.sup.reconcile()
    expect(tg.ctxs).toHaveLength(3)
    await t.sup.stop()
  })

  it("leaves tool connectors and connectors that opt out alone", async () => {
    const mcp = live("mcp", "tools")
    const wa = live("whatsapp", "channel", { restartOnError: false })
    const t = boot({ mcp, whatsapp: wa })
    seedConnector(t.db, "mcp", "tools")
    seedConnector(t.db, "whatsapp", "channel")
    await t.sup.reconcile()
    mcp.ctxs[0]!.setStatus("error", "listTools failed")
    wa.ctxs[0]!.setStatus("error", "unlinked")
    await sleep(5)
    expect(mcp.stops).toHaveLength(0)
    expect(wa.stops).toHaveLength(0)
    expect(t.sup.isRunning("mcp")).toBe(true)
    await t.sup.stop()
  })

  it("retries a failed start after the first backoff step without a reconcile poke", async () => {
    let failing = true
    const bad = live("bad", "tools", {}, () => failing)
    const t = boot({ bad })
    seedConnector(t.db, "bad", "tools")
    await t.sup.reconcile()
    expect(getConnector(t.db, "bad")!.status).toBe("error")
    failing = false
    t.clock.now += 20
    await sleep(60)
    expect(t.sup.isRunning("bad")).toBe(true)
    await t.sup.stop()
  })

  it("unlinks only connectors that declare it, and cleans up removed connectors that aren't running", async () => {
    const tg = live("telegram", "channel")
    const wa = live("whatsapp", "channel", { unlinkOnRemove: true, restartOnError: false })
    const removed: { id: string; unlinked: boolean }[] = []
    const t = boot({ telegram: tg, whatsapp: wa }, async (row, info) => void removed.push({ id: row.id, unlinked: info.unlinked }))
    seedConnector(t.db, "telegram", "channel")
    seedConnector(t.db, "whatsapp", "channel")
    await t.sup.reconcile()
    const r1 = await t.sup.remove("telegram", () => {})
    expect(r1).toEqual({ ok: true, detail: undefined })
    expect(tg.actions).toEqual([])
    await t.sup.remove("whatsapp", () => {})
    expect(wa.actions).toEqual(["unlink"])
    // A paused WhatsApp: nothing running to unlink, but its session is still cleaned up.
    seedConnector(t.db, "whatsapp", "channel", { enabled: false })
    await t.sup.reconcile()
    await t.sup.remove("whatsapp", () => {})
    expect(wa.actions).toEqual(["unlink"])
    expect(removed).toEqual([
      { id: "telegram", unlinked: false },
      { id: "whatsapp", unlinked: true },
      { id: "whatsapp", unlinked: false },
    ])
  })
})
