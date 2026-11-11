// The connector supervisor (PLAN 4.4). Desired state: the `connectors` rows
// (enabled, config_hash). Running state: the handles held here. It reconciles on
// a control poke and every 30 s. Each connector is isolated: a throw never takes
// the process down.
import { and, eq, isNull, lte } from "drizzle-orm"
import { connectors, type Channel, type DB } from "@palui/db"
import type { ConnectorLive } from "../control"
import { logEvent } from "../events"
import type { Logger } from "../log"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import { ownerChat } from "./rules"
import type { AnyRuntime, ChannelHandle, ConnectorContext, ConnectorManifest, ConnectorServices, RunningConnector, TestResult } from "./define"
import {
  connectConnector,
  deleteConnector,
  getConnector,
  getSecrets,
  listConnectors,
  patchConnectorConfig,
  setConnectorStatus,
  upsertConnector,
  type ConnectorRow,
} from "./store"

export const RECONCILE_INTERVAL_MS = 30_000
export const RESTART_BACKOFF_MS = [5_000, 30_000, 120_000, 600_000]
export const ERROR_ALERT_AFTER_MS = 5 * 60_000
const STOP_TIMEOUT_MS = 10_000
export const START_TIMEOUT_MS = 60_000
// A handle that ran this long before reporting an error starts the ladder over.
export const STABLE_RUN_MS = 10 * 60_000

export type RunningEntry = { id: string; row: ConnectorRow; handle: RunningConnector }

type Slot = {
  id: string
  handle: RunningConnector | null
  configHash: string
  abort: AbortController
  // Set while start() runs, so a reconcile doesn't start it twice.
  starting: boolean
  failures: number
  nextRetryAt: number
  // A disabled connector still used by a running turn waits here until it's free.
  stopWhenIdle: boolean
  // The connector reported its own status during start (e.g. waiting for pairing).
  reported: boolean
  // Errors reported by a running handle in a row (Telegram polling dying on a
  // 409), for the restart ladder. Reset after STABLE_RUN_MS of running.
  crashes: number
  startedAt: number
}

export type SupervisorOptions = {
  db: DB
  registry: Record<string, AnyRuntime>
  services: ConnectorServices
  log: Logger
  masterKey: string
  dataDir: string
  clock?: () => number
  // Whether a running turn is using this connector's tools (pause applies from the next turn).
  inUse?: (connectorId: string) => boolean
  // Cleanup after a connector is removed (the runtime deletes a WhatsApp auth dir).
  // Runs whether or not it was running, so a paused connector's credentials go too.
  onRemoved?: (row: ConnectorRow, info: { unlinked: boolean }) => Promise<void>
  // Restart backoff (tests); default RESTART_BACKOFF_MS.
  backoffMs?: number[]
}

export type Supervisor = ReturnType<typeof createSupervisor>

export function createSupervisor(opts: SupervisorOptions) {
  const { db, registry, services, log } = opts
  const clock = opts.clock ?? Date.now
  const slots = new Map<string, Slot>()
  const backoff = opts.backoffMs ?? RESTART_BACKOFF_MS
  // One retry timer per connector, so the first retry comes after ~5 s instead
  // of waiting for the 30 s reconcile.
  const retryTimers = new Map<string, NodeJS.Timeout>()
  let timer: NodeJS.Timeout | undefined
  let chain: Promise<void> = Promise.resolve()
  let stopped = false

  function runtimeFor(row: ConnectorRow): AnyRuntime | undefined {
    return registry[row.type]
  }

  function waitFor(n: number): number {
    return backoff[Math.min(Math.max(n, 1) - 1, backoff.length - 1)]!
  }

  function armRetry(id: string, at: number) {
    clearTimeout(retryTimers.get(id))
    const t = setTimeout(
      () => {
        retryTimers.delete(id)
        if (!stopped) void reconcile()
      },
      Math.max(0, at - clock()),
    )
    t.unref()
    retryTimers.set(id, t)
  }

  function restartsOnError(row: ConnectorRow): boolean {
    return runtimeFor(row)?.manifest.restartOnError ?? row.kind === "channel"
  }

  // A running handle reported `error` (PLAN 4.3: restarts with backoff). Stop it,
  // keep the slot (and its counters) without a handle, and arm the retry.
  async function restartAfterError(slot: Slot, detail: string | undefined) {
    if (slots.get(slot.id) !== slot || !slot.handle) return
    const handle = slot.handle
    slot.handle = null
    slot.abort.abort(new Error("connector reported an error"))
    if (clock() - slot.startedAt >= STABLE_RUN_MS) slot.crashes = 0
    slot.crashes++
    const wait = waitFor(slot.crashes)
    slot.nextRetryAt = clock() + wait
    armRetry(slot.id, slot.nextRetryAt)
    log.warn("connector reported an error while running; restarting it", { connector_id: slot.id, detail, retry_in_ms: wait })
    try {
      await Promise.race([handle.stop(), new Promise((r) => setTimeout(r, STOP_TIMEOUT_MS).unref())])
    } catch (e) {
      log.warn("connector stop threw", { connector_id: slot.id, error: e })
    }
  }

  function buildContext(row: ConnectorRow, signal: AbortSignal, slot?: Slot): ConnectorContext {
    let secrets: Record<string, string> = {}
    try {
      secrets = getSecrets(row, opts.masterKey)
    } catch (e) {
      log.error("connector secrets unreadable", { connector_id: row.id, error: e })
    }
    const clog = log.child({ connector_id: row.id })
    return {
      id: row.id,
      type: row.type,
      config: row.config ?? {},
      secrets,
      trust: row.trust,
      db,
      log: clog,
      dataDir: opts.dataDir,
      signal,
      services,
      setStatus(status, detail) {
        if (slot && slot.abort.signal.aborted) return
        if (slot) slot.reported = true
        // Coming back after a crash: keep showing the error until it's really on,
        // so errorSince keeps counting toward the 5 minute alert.
        if (slot && slot.crashes > 0 && status === "connecting") return
        try {
          setConnectorStatus(db, row.id, status, detail ?? null, clock())
        } catch (e) {
          clog.error("setStatus failed", { error: e })
        }
        if (status === "error" && slot?.handle && !slot.starting && restartsOnError(row)) void restartAfterError(slot, detail)
      },
      event(kind, message, level = "info", data) {
        try {
          logEvent(db, { connectorId: row.id, kind, message, level, data }, clock())
        } catch (e) {
          clog.error("event failed", { error: e })
        }
      },
      patchConfig(patch) {
        try {
          patchConnectorConfig(db, row.id, patch, clock())
        } catch (e) {
          clog.error("patchConfig failed", { error: e })
        }
      },
    }
  }

  async function startSlot(row: ConnectorRow): Promise<void> {
    const runtime = runtimeFor(row)
    if (!runtime) {
      setConnectorStatus(db, row.id, "error", `No runtime for connector type "${row.type}" in this build`, clock())
      return
    }
    const prev = slots.get(row.id)
    const slot: Slot = {
      id: row.id,
      handle: null,
      configHash: row.configHash,
      abort: new AbortController(),
      starting: true,
      failures: prev?.failures ?? 0,
      nextRetryAt: 0,
      stopWhenIdle: false,
      reported: false,
      crashes: prev?.crashes ?? 0,
      startedAt: clock(),
    }
    slots.set(row.id, slot)
    // While retrying after failures the row stays in `error`, so errorSince keeps
    // counting toward the 5 minute alert.
    if (slot.failures === 0 && slot.crashes === 0) setConnectorStatus(db, row.id, "connecting", null, clock())
    try {
      let timer: NodeJS.Timeout | undefined
      const handle = await Promise.race([
        runtime.start(buildContext(row, slot.abort.signal, slot)),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error(`start took longer than ${START_TIMEOUT_MS / 1000} s`)), START_TIMEOUT_MS)
          timer.unref()
        }),
      ]).finally(() => clearTimeout(timer))
      if (slot.abort.signal.aborted || slots.get(row.id) !== slot) {
        // Paused, removed or timed out while starting: don't keep the handle.
        await handle.stop().catch(() => {})
        return
      }
      slot.handle = handle
      slot.starting = false
      slot.failures = 0
      slot.startedAt = clock()
      // Keep a status the connector reported itself during start (e.g. waiting for pairing).
      if (!slot.reported) setConnectorStatus(db, row.id, "on", null, clock())
      log.info("connector started", { connector_id: row.id })
    } catch (e) {
      slot.starting = false
      slot.handle = null
      if (slots.get(row.id) !== slot) return
      slot.abort.abort(e)
      slot.failures++
      const wait = waitFor(slot.failures + slot.crashes)
      slot.nextRetryAt = clock() + wait
      armRetry(row.id, slot.nextRetryAt)
      const msg = (e as Error)?.message ?? String(e)
      log.error("connector start failed", { connector_id: row.id, error: e, retry_in_ms: wait })
      setConnectorStatus(db, row.id, "error", msg.slice(0, 300), clock())
    }
  }

  async function stopSlot(slot: Slot, reason: string): Promise<void> {
    clearTimeout(retryTimers.get(slot.id))
    retryTimers.delete(slot.id)
    slot.abort.abort(new Error(reason))
    const handle = slot.handle
    slot.handle = null
    slots.delete(slot.id)
    if (!handle) return
    try {
      await Promise.race([handle.stop(), new Promise((r) => setTimeout(r, STOP_TIMEOUT_MS).unref())])
    } catch (e) {
      log.warn("connector stop threw", { connector_id: slot.id, error: e })
    }
    log.info("connector stopped", { connector_id: slot.id, reason })
  }

  // Decide what one connector needs. Starts are pushed to `starts` instead of
  // awaited, so a slow start never holds up a later poke (a /connectors off).
  async function reconcileRow(row: ConnectorRow, starts: Promise<void>[]): Promise<void> {
    const slot = slots.get(row.id)
    try {
      if (!row.enabled) {
        if (slot?.handle || slot?.starting) {
          if (opts.inUse?.(row.id)) {
            slot.stopWhenIdle = true
            return
          }
          await stopSlot(slot, "disabled")
        } else if (slot) slots.delete(row.id)
        if (row.status !== "paused" && row.status !== "not_set_up") setConnectorStatus(db, row.id, "paused", null, clock())
        return
      }
      if (slot?.starting) return
      if (slot?.stopWhenIdle) slot.stopWhenIdle = false
      if (slot?.handle) {
        if (slot.configHash !== row.configHash) {
          await stopSlot(slot, "config changed")
          starts.push(startSlot(row))
        }
        return
      }
      if (slot && slot.nextRetryAt > clock()) return
      starts.push(startSlot(row))
    } catch (e) {
      log.error("reconcile failed for connector", { connector_id: row.id, error: e })
    }
  }

  async function reconcileOnce(starts: Promise<void>[]): Promise<void> {
    if (stopped) return
    const rows = listConnectors(db)
    const byId = new Map(rows.map((r) => [r.id, r]))
    // Rows that vanished (removed from the dashboard): stop.
    for (const slot of [...slots.values()]) {
      if (!byId.has(slot.id)) await stopSlot(slot, "removed")
    }
    await Promise.all(rows.map((row) => reconcileRow(row, starts)))
    alertErrors()
  }

  // After 5 minutes in `error`, tell the owner once on another running channel (PLAN 4.3).
  function alertErrors() {
    const now = clock()
    const due = db
      .select()
      .from(connectors)
      .where(and(eq(connectors.status, "error"), lte(connectors.errorSince, now - ERROR_ALERT_AFTER_MS), isNull(connectors.alertedAt)))
      .all()
    for (const row of due) {
      const target = otherChannel(row.id)
      const text = `Konektor ${row.label} error lebih dari 5 menit: ${row.statusDetail ?? "tanpa detail"}. Cek dashboard.`
      if (target) {
        enqueueOutbox(db, { chatId: target.chatId, channel: target.channel, payload: { text }, source: "system", expiresAt: now + 24 * 3600_000 }, now)
        notifyOutbox()
      }
      db.update(connectors).set({ alertedAt: now }).where(eq(connectors.id, row.id)).run()
      logEvent(
        db,
        {
          connectorId: row.id,
          kind: "alert",
          level: "warn",
          message: target ? `Owner alerted on ${target.channel}` : "In error for 5 minutes and no other channel is reachable to alert the owner",
        },
        now,
      )
    }
  }

  function otherChannel(exceptId: string): { channel: Channel; chatId: string } | null {
    for (const e of running()) {
      const ch = e.handle.channel
      if (!ch || e.id === exceptId || ch.channel === "web" || !safeReachable(ch)) continue
      const chat = ownerChat(db, ch.channel)
      if (chat) return { channel: ch.channel, chatId: chat.id }
    }
    return null
  }

  function safeReachable(ch: ChannelHandle): boolean {
    try {
      return ch.reachable()
    } catch {
      return false
    }
  }

  // Resolves once this pass's decisions are applied and its starts settled.
  function reconcile(): Promise<void> {
    const starts: Promise<void>[] = []
    const run = () => reconcileOnce(starts)
    const step = chain.then(run, run)
    chain = step.catch(() => {})
    return step.then(async () => {
      await Promise.allSettled(starts)
      alertErrors()
    })
  }

  // Handles whose row is still enabled right now: pausing applies from the next turn
  // even before the reconcile stops the handle.
  function running(): RunningEntry[] {
    const out: RunningEntry[] = []
    const rows = new Map(listConnectors(db).map((r) => [r.id, r]))
    for (const slot of slots.values()) {
      const row = rows.get(slot.id)
      if (!slot.handle || !row || !row.enabled || slot.stopWhenIdle) continue
      out.push({ id: slot.id, row, handle: slot.handle })
    }
    return out.sort((a, b) => a.row.sortOrder - b.row.sortOrder || a.row.createdAt - b.row.createdAt)
  }

  function get(id: string): RunningEntry | undefined {
    return running().find((e) => e.id === id)
  }

  function channel(ch: Channel): ChannelHandle | undefined {
    return running().find((e) => e.handle.channel?.channel === ch)?.handle.channel
  }

  function status(): ConnectorLive[] {
    return listConnectors(db).map((row) => {
      const slot = slots.get(row.id)
      let live: Record<string, unknown> = {}
      try {
        live = slot?.handle?.live?.() ?? {}
      } catch {
        live = {}
      }
      return { id: row.id, running: !!slot?.handle, status: row.status, detail: row.statusDetail, live }
    })
  }

  async function test(id: string): Promise<TestResult> {
    const row = getConnector(db, id)
    if (!row) return { ok: false, detail: `No connector ${id}` }
    const runtime = runtimeFor(row)
    if (!runtime) return { ok: false, detail: `No runtime for connector type "${row.type}"` }
    const ac = new AbortController()
    const t = setTimeout(() => ac.abort(new Error("test timed out")), 25_000)
    try {
      return await runtime.test(buildContext(row, ac.signal))
    } catch (e) {
      return { ok: false, detail: (e as Error)?.message ?? String(e) }
    } finally {
      clearTimeout(t)
    }
  }

  async function action(id: string, name: string, input: Record<string, unknown>): Promise<Record<string, unknown>> {
    const slot = slots.get(id)
    if (!slot?.handle) {
      // Wizard lookups on a draft (not started yet) come from test(), which returns
      // the same rows: MCP tools, recall containers.
      if (name === "listTools" || name === "listContainers") {
        const r = await test(id)
        if (!r.ok) throw Object.assign(new Error(r.detail), { status: 400 })
        return r.data ?? {}
      }
      throw Object.assign(new Error(`Connector ${id} isn't running`), { status: 409 })
    }
    if (!slot.handle.action) throw Object.assign(new Error(`Connector ${id} has no actions`), { status: 404 })
    return slot.handle.action(name, input)
  }

  // Remove (PLAN 4.2): unlink, stop, reroute or cancel its outbox rows, delete the row.
  async function remove(id: string, reroute: (channel: Channel) => void): Promise<{ ok: boolean; detail?: string }> {
    const row = getConnector(db, id)
    if (!row) return { ok: false, detail: `No connector ${id}` }
    const slot = slots.get(id)
    let detail: string | undefined
    let unlinked = false
    // Only connectors that declare it (WhatsApp) get unlinked; others would just throw "Unknown action".
    if (runtimeFor(row)?.manifest.unlinkOnRemove && slot?.handle?.action) {
      try {
        await slot.handle.action("unlink", {})
        unlinked = true
      } catch (e) {
        detail = `unlink failed: ${(e as Error)?.message ?? e}`
        log.warn("unlink failed", { connector_id: id, error: e })
      }
    }
    const ch = slot?.handle?.channel?.channel ?? (row.kind === "channel" ? (row.type as Channel) : undefined)
    if (slot) await stopSlot(slot, "removed")
    if (opts.onRemoved) {
      try {
        await opts.onRemoved(row, { unlinked })
      } catch (e) {
        detail = [detail, `cleanup failed: ${(e as Error)?.message ?? e}`].filter(Boolean).join("; ")
        log.warn("connector cleanup after remove failed", { connector_id: id, error: e })
      }
    }
    deleteConnector(db, id, clock())
    if (ch) reroute(ch)
    return { ok: true, detail }
  }

  // Builtin manifests get a row on first boot, enabled.
  function ensureBuiltins(manifests: ConnectorManifest[]) {
    for (const m of manifests) {
      if (!m.builtin || getConnector(db, m.id)) continue
      upsertConnector(db, { id: m.id, manifest: m, enabled: true }, opts.masterKey, clock())
      connectConnector(db, m.id, clock())
    }
  }

  // A turn ended: stop connectors that were disabled while in use.
  function release() {
    for (const slot of slots.values()) {
      if (slot.stopWhenIdle && !opts.inUse?.(slot.id)) void reconcile()
    }
  }

  return {
    reconcile,
    poke: () => void reconcile(),
    running,
    get,
    channel,
    status,
    test,
    action,
    remove,
    ensureBuiltins,
    release,
    isRunning: (id: string) => !!slots.get(id)?.handle,
    start() {
      stopped = false
      timer = setInterval(() => void reconcile(), RECONCILE_INTERVAL_MS)
      timer.unref()
      return reconcile()
    },
    async stop() {
      stopped = true
      if (timer) clearInterval(timer)
      for (const t of retryTimers.values()) clearTimeout(t)
      retryTimers.clear()
      await chain
      for (const slot of [...slots.values()]) await stopSlot(slot, "shutdown")
    },
  }
}
