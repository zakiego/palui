// The outbox worker (PLAN 8.7, 6, 4.3): claim, send through the channel handle,
// ack, retry with backoff. Delivery is at least once: a row is only `sent` after
// the channel acks it.
import { and, eq, inArray, isNotNull, isNull, lt, lte, or, sql } from "drizzle-orm"
import { connectors, messages, outbox, pendingApprovals, type Channel, type DB } from "@palui/db"
import { getChat } from "../chats"
import { logEvent } from "../events"
import type { Logger } from "../log"
import type { ChannelHandle, SendResult } from "../connectors/define"
import { ownerChat } from "../connectors/rules"
import { getSetting } from "../settings"
import { outboundFailed } from "../outbound/service"
import { onOutboxEnqueued, type OutboxRow } from "./enqueue"

export const OUTBOX_BACKOFF_MS = [5_000, 30_000, 120_000, 600_000, 1_800_000]
export const STUCK_SENDING_MS = 5 * 60_000
// A send that takes longer counts as a failed attempt (a hung socket must not
// stall the outbox).
export const SEND_TIMEOUT_MS = 30_000
// Rows on a paused channel don't expire (PLAN 4.3: they send on resume). Their
// expiry is pushed this far past now on every tick while paused.
export const PAUSED_EXPIRY_GRACE_MS = 60 * 60_000
const STOP_WAIT_MS = 10_000
const BATCH = 20
const TICK_MS = 2_000

// How the worker sees a channel right now.
//  ready:   running and reachable, send.
//  paused:  disabled by the owner, rows wait without using attempts.
//  down:    enabled but not running/reachable, sends fail and back off.
//  removed: no connector row, rows go to the fallback or get cancelled.
export type ChannelState = "ready" | "paused" | "down" | "removed"

export type OutboxWorkerOptions = {
  db: DB
  channel(ch: Channel): ChannelHandle | undefined
  // A specific connector's channel (an agent's Telegram bot, PLAN 19.6), for
  // rows with outbox.connectorId set.
  connector?(id: string): ChannelHandle | undefined
  log: Logger
  clock?: () => number
  sendTimeoutMs?: number
}

// `ch` is a connector id: the channel's main connector shares the channel's name.
export function channelState(db: DB, ch: Channel | string, handle: ChannelHandle | undefined): ChannelState {
  if (ch === "web") return handle ? "ready" : "down"
  const row = db.select().from(connectors).where(eq(connectors.id, ch)).get()
  if (!row) return "removed"
  if (!row.enabled || row.status === "paused") return "paused"
  if (!handle) return "down"
  try {
    return handle.reachable() ? "ready" : "down"
  } catch {
    return "down"
  }
}

export function backoffFor(attempts: number): number {
  return OUTBOX_BACKOFF_MS[Math.min(Math.max(attempts, 1) - 1, OUTBOX_BACKOFF_MS.length - 1)]!
}

// Move waiting rows of a channel to the fallback channel, or cancel them (PLAN 4.3).
// Only `pending` rows: one in `sending` is in flight and may still be delivered,
// so moving it could send it twice. If that send fails it goes back to pending
// and the worker's next tick moves it.
export function rerouteChannelRows(db: DB, from: Channel, now = Date.now(), handleFor?: (ch: Channel) => ChannelHandle | undefined): { moved: number; cancelled: number } {
  const rows = db
    .select()
    .from(outbox)
    .where(and(eq(outbox.channel, from), eq(outbox.status, "pending")))
    .all()
  if (!rows.length) return { moved: 0, cancelled: 0 }
  const target = fallbackTarget(db, from, handleFor, false)
  let moved = 0
  let cancelled = 0
  for (const r of rows) {
    // A message to a contact never goes to the owner's chat instead.
    if (target && !r.source.startsWith("outbound:")) {
      db.update(outbox)
        .set({ channel: target.channel, chatId: target.chatId, fallbackFrom: from, status: "pending", claimedAt: null, nextAttemptAt: now })
        .where(eq(outbox.id, r.id))
        .run()
      moved++
    } else {
      db.update(outbox).set({ status: "cancelled", error: `channel ${from} removed`, claimedAt: null }).where(eq(outbox.id, r.id)).run()
      cancelled++
    }
  }
  logEvent(
    db,
    {
      connectorId: from,
      kind: "outbox.reroute",
      level: "warn",
      message: target ? `${moved} waiting message(s) moved from ${from} to ${target.channel}` : `${cancelled} waiting message(s) cancelled: ${from} was removed and no fallback channel is on`,
      data: { moved, cancelled },
    },
    now,
  )
  return { moved, cancelled }
}

// The fallback channel when it's on (and, when asked, ready) and has an owner chat.
function fallbackTarget(db: DB, from: Channel, handleFor: ((ch: Channel) => ChannelHandle | undefined) | undefined, requireReady: boolean): { channel: Channel; chatId: string } | null {
  const fb = getSetting(db, "fallbackChannel")
  if (fb === "none" || fb === from) return null
  const row = db.select().from(connectors).where(eq(connectors.id, fb)).get()
  if (!row || !row.enabled) return null
  if (requireReady && channelState(db, fb, handleFor?.(fb)) !== "ready") return null
  const chat = ownerChat(db, fb)
  return chat ? { channel: fb, chatId: chat.id } : null
}

export function createOutboxWorker(opts: OutboxWorkerOptions) {
  const { db, log } = opts
  const clock = opts.clock ?? Date.now
  const sendTimeoutMs = opts.sendTimeoutMs ?? SEND_TIMEOUT_MS
  let timer: NodeJS.Timeout | undefined
  let stopped = true
  // Set by stop(): send loops hand their remaining rows back instead of sending.
  let stopping = false
  let unsubscribe: (() => void) | undefined
  // One send loop per channel (or agent bot, keyed "bot:<id>") at a time; a slow one never holds up the others.
  const busy = new Map<string, Promise<void>>()
  // Sends still in flight, so a timed-out one can be settled when it finishes.
  const pendingSends = new Map<string, Promise<SendResult>>()

  function resetStuck(now: number) {
    const n = db
      .update(outbox)
      .set({ status: "pending", claimedAt: null })
      .where(and(eq(outbox.status, "sending"), lt(outbox.claimedAt, now - STUCK_SENDING_MS)))
      .run().changes
    if (n) log.warn("outbox rows stuck in sending went back to pending", { count: n })
  }

  function expire(now: number) {
    const due = db
      .select({ id: outbox.id, channel: outbox.channel })
      .from(outbox)
      .where(and(eq(outbox.status, "pending"), isNotNull(outbox.expiresAt), lt(outbox.expiresAt, now)))
      .all()
    if (!due.length) return
    const states = new Map<Channel, ChannelState>()
    const stateOf = (ch: Channel) => states.get(ch) ?? states.set(ch, channelState(db, ch, opts.channel(ch))).get(ch)!
    // Only an owner pause (the Switch) stops the clock. A connector that reports
    // `paused` itself (WhatsApp after a 440) may stay that way for days, and its
    // stale replies and reminders shouldn't all arrive at once when it's back.
    const ownerPaused = (ch: Channel) => stateOf(ch) === "paused" && db.select({ enabled: connectors.enabled }).from(connectors).where(eq(connectors.id, ch)).get()?.enabled === false
    const waiting = due.filter((r) => ownerPaused(r.channel)).map((r) => r.id)
    const dead = due.filter((r) => !ownerPaused(r.channel)).map((r) => r.id)
    if (waiting.length) db.update(outbox).set({ expiresAt: now + PAUSED_EXPIRY_GRACE_MS }).where(inArray(outbox.id, waiting)).run()
    if (!dead.length) return
    const rows = db
      .update(outbox)
      .set({ status: "failed", claimedAt: null, error: sql`coalesce(${outbox.error}, 'expired')` })
      .where(and(eq(outbox.status, "pending"), inArray(outbox.id, dead)))
      .returning()
      .all()
    for (const r of rows) {
      outboundFailed(db, r, now)
      logEvent(
        db,
        { connectorId: r.channel, kind: "outbox.failed", level: "error", message: `Gave up sending ${r.source} after ${r.attempts} attempt(s): ${r.error ?? "expired"}`, data: { outboxId: r.id } },
        now,
      )
    }
  }

  function handleRemoved(now: number) {
    const chans = db.selectDistinct({ channel: outbox.channel }).from(outbox).where(and(eq(outbox.status, "pending"), isNull(outbox.connectorId))).all()
    for (const { channel } of chans) {
      if (channelState(db, channel, opts.channel(channel)) === "removed") rerouteChannelRows(db, channel, now, opts.channel)
    }
    // An agent bot that was removed: its rows try the channel's main connector.
    const bots = db.selectDistinct({ id: outbox.connectorId }).from(outbox).where(and(eq(outbox.status, "pending"), isNotNull(outbox.connectorId))).all()
    for (const { id } of bots) {
      if (id && channelState(db, id, opts.connector?.(id)) === "removed") {
        db.update(outbox).set({ connectorId: null }).where(and(eq(outbox.connectorId, id), eq(outbox.status, "pending"))).run()
      }
    }
  }

  function claim(now: number, sendable: Channel[], sendableBots: string[] = []): OutboxRow[] {
    if (!sendable.length && !sendableBots.length) return []
    const which = or(
      sendable.length ? and(isNull(outbox.connectorId), inArray(outbox.channel, sendable)) : sql`0`,
      sendableBots.length ? inArray(outbox.connectorId, sendableBots) : sql`0`,
    )
    // Idempotent claim: only rows still pending flip to sending.
    return db
      .update(outbox)
      .set({ status: "sending", claimedAt: now })
      .where(
        and(
          eq(outbox.status, "pending"),
          inArray(
            outbox.id,
            db
              .select({ id: outbox.id })
              .from(outbox)
              .where(and(eq(outbox.status, "pending"), lte(outbox.nextAttemptAt, now), which))
              .orderBy(outbox.createdAt)
              .limit(BATCH),
          ),
        ),
      )
      .returning()
      .all()
  }

  // Resolves "timeout" when the channel didn't answer in time (counted as a failed attempt).
  async function sendOne(row: OutboxRow): Promise<"ok" | "failed" | "timeout"> {
    const handle = row.connectorId ? opts.connector?.(row.connectorId) : opts.channel(row.channel)
    const chat = getChat(db, row.chatId)
    let timer: NodeJS.Timeout | undefined
    // The kill switch holds at send time too, whatever enqueued the row.
    if (row.source.startsWith("outbound:") && !getSetting(db, "outbound").enabled) {
      db.update(outbox).set({ status: "cancelled", error: "outbound turned off", claimedAt: null }).where(eq(outbox.id, row.id)).run()
      return "failed"
    }
    try {
      if (!chat) throw new Error(`chat ${row.chatId} not found`)
      if (!handle) throw new Error(`channel ${row.connectorId ?? row.channel} isn't running`)
      const sending = handle.send(chat.externalId, row.payload)
      pendingSends.set(row.id, sending)
      sending.then(
        () => {},
        () => {},
      )
      const res = await Promise.race([
        sending,
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new SendTimeout(`send took longer than ${Math.round(sendTimeoutMs / 1000)} s`)), sendTimeoutMs)
          timer.unref()
        }),
      ])
      const sentAt = clock()
      db.update(outbox)
        .set({ status: "sent", channelMsgId: res.channelMsgId ?? null, sentAt, claimedAt: null, error: null })
        .where(eq(outbox.id, row.id))
        .run()
      if (res.channelMsgId) linkChannelMsg(row, res.channelMsgId)
      return "ok"
    } catch (e) {
      if (e instanceof SendTimeout) {
        // The send can't be cancelled and may still land. Leave the row in
        // `sending`: a late success marks it sent, a late failure fails it, and
        // if it never settles the stuck-sending sweep (5 min) gives it back.
        // Retrying now could deliver it twice.
        lateSettle(row, handle, chat)
        log.warn("send timed out, waiting for it to settle", { outbox_id: row.id, channel: row.channel })
        return "timeout"
      }
      fail(row, (e as Error)?.message ?? String(e), clock())
      return "failed"
    } finally {
      clearTimeout(timer)
      if (!late.has(row)) pendingSends.delete(row.id)
    }
  }

  const late = new WeakMap<OutboxRow, Promise<unknown>>()
  function lateSettle(row: OutboxRow, _handle: ChannelHandle | undefined, _chat: unknown) {
    const p = pendingSends.get(row.id)
    if (!p || late.has(row)) return
    late.set(row, p)
    p.then(
      (res) => {
        db.update(outbox)
          .set({ status: "sent", channelMsgId: res.channelMsgId ?? null, sentAt: clock(), claimedAt: null, error: null })
          .where(and(eq(outbox.id, row.id), eq(outbox.status, "sending")))
          .run()
        if (res.channelMsgId) linkChannelMsg(row, res.channelMsgId)
      },
      (e) => {
        const still = db.select({ status: outbox.status }).from(outbox).where(eq(outbox.id, row.id)).get()
        if (still?.status === "sending") fail(row, (e as Error)?.message ?? String(e), clock())
      },
    ).finally(() => pendingSends.delete(row.id))
  }

  // Give a claimed row back without using an attempt (shutdown, or its channel hung).
  function release(row: OutboxRow) {
    db.update(outbox).set({ status: "pending", claimedAt: null }).where(and(eq(outbox.id, row.id), eq(outbox.status, "sending"))).run()
  }

  // Send one channel's rows in order. After a timeout the channel is treated as
  // hung for this round: the rest go back to pending for a later tick.
  async function sendChannel(rows: OutboxRow[]) {
    let hung = false
    for (const row of rows) {
      if (stopping || hung) {
        release(row)
        continue
      }
      if ((await sendOne(row)) === "timeout") hung = true
    }
  }

  // Remember what the channel called the message, for replies that quote it.
  function linkChannelMsg(row: OutboxRow, channelMsgId: string) {
    try {
      if (row.source.startsWith("approval:")) {
        db.update(pendingApprovals).set({ promptMsgId: channelMsgId }).where(eq(pendingApprovals.id, row.source.slice("approval:".length))).run()
      } else if (row.source.startsWith("reply:")) {
        const runId = row.source.slice("reply:".length)
        db.update(messages)
          .set({ channelMsgId })
          .where(and(eq(messages.runId, runId), eq(messages.role, "assistant"), sql`${messages.channelMsgId} is null`))
          .run()
      }
    } catch (e) {
      log.warn("linking channel message id failed", { error: e })
    }
  }

  function fail(row: OutboxRow, error: string, now: number) {
    const attempts = row.attempts + 1
    // Reminders fall back to the other channel after N failures (PLAN 6).
    if (row.source.startsWith("reminder:") && !row.fallbackFrom && attempts >= getSetting(db, "reminderFallbackAfterAttempts")) {
      const target = fallbackTarget(db, row.channel, opts.channel, true)
      if (target) {
        db.update(outbox)
          .set({ status: "pending", channel: target.channel, chatId: target.chatId, fallbackFrom: row.channel, attempts, nextAttemptAt: now, claimedAt: null, error })
          .where(eq(outbox.id, row.id))
          .run()
        logEvent(db, { connectorId: row.channel, kind: "outbox.fallback", level: "warn", message: `${row.source} moved from ${row.channel} to ${target.channel} after ${attempts} failed attempts`, data: { outboxId: row.id } }, now)
        wake()
        return
      }
    }
    db.update(outbox)
      .set({ status: "pending", attempts, nextAttemptAt: now + backoffFor(attempts), claimedAt: null, error: error.slice(0, 500) })
      .where(eq(outbox.id, row.id))
      .run()
    log.warn("outbox send failed", { outbox_id: row.id, channel: row.channel, attempts, error })
  }

  // Housekeeping and claims are synchronous (SQLite), so ticks never interleave
  // there. Each channel with rows gets its own send loop unless one is already
  // running. Resolves once the loops this tick started have settled.
  function tick(): Promise<void> {
    let started: Promise<void>[] = []
    try {
      const now = clock()
      resetStuck(now)
      expire(now)
      handleRemoved(now)
      const chans = db.selectDistinct({ channel: outbox.channel }).from(outbox).where(and(eq(outbox.status, "pending"), isNull(outbox.connectorId))).all()
      const sendable = chans
        .map((c) => c.channel)
        .filter((ch) => {
          if (busy.has(ch)) return false
          const st = channelState(db, ch, opts.channel(ch))
          return st === "ready" || st === "down"
        })
      const bots = db.selectDistinct({ id: outbox.connectorId }).from(outbox).where(and(eq(outbox.status, "pending"), isNotNull(outbox.connectorId))).all()
      const sendableBots = bots
        .map((b) => b.id!)
        .filter((id) => {
          if (busy.has(`bot:${id}`)) return false
          const st = channelState(db, id, opts.connector?.(id))
          return st === "ready" || st === "down"
        })
      const rows = claim(now, sendable, sendableBots)
      const byChannel = new Map<string, OutboxRow[]>()
      for (const r of rows) {
        const key = r.connectorId ? `bot:${r.connectorId}` : r.channel
        byChannel.set(key, [...(byChannel.get(key) ?? []), r])
      }
      started = [...byChannel].map(([ch, list]) => {
        const loop = sendChannel(list)
          .catch((e) => log.error("outbox send loop failed", { channel: ch, error: e }))
          .finally(() => {
            busy.delete(ch)
            // More may be waiting (a full batch, or rows that arrived meanwhile).
            if (list.length === BATCH) wake()
          })
        busy.set(ch, loop)
        return loop
      })
    } catch (e) {
      log.error("outbox tick failed", { error: e })
    }
    return Promise.all(started).then(() => {})
  }

  function wake() {
    if (!stopped) setImmediate(() => void tick())
  }

  return {
    tick,
    wake,
    start() {
      stopped = false
      stopping = false
      unsubscribe = onOutboxEnqueued(wake)
      timer = setInterval(() => void tick(), TICK_MS)
      timer.unref()
      void tick()
    },
    async stop() {
      stopped = true
      stopping = true
      unsubscribe?.()
      if (timer) clearInterval(timer)
      // Let sends in flight finish (each is capped by the send timeout), up to a limit.
      await Promise.race([Promise.allSettled([...busy.values()]), new Promise((r) => setTimeout(r, STOP_WAIT_MS).unref())])
      // Release claims so the next process sends them.
      db.update(outbox).set({ status: "pending", claimedAt: null }).where(eq(outbox.status, "sending")).run()
    },
  }
}

class SendTimeout extends Error {}
