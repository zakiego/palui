import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { memoryDb, outbox, type Channel, type DB } from "@palui/db"
import { ensureChat } from "../chats"
import { fakeChannel, seedConnector, type FakeChannel } from "../chat/testing"
import { listEvents } from "../events"
import { silentLogger } from "../log"
import { setWhatsAppOwner, claimOwner, createClaim } from "../owner"
import { pauseConnector, deleteConnector } from "../saklar/store"
import { setSetting } from "../settings"
import { enqueueOutbox } from "./enqueue"
import { createOutboxWorker, rerouteChannelRows } from "./worker"

function setup() {
  const db = memoryDb()
  let now = 1_000_000
  const handles: Partial<Record<Channel, FakeChannel>> = { telegram: fakeChannel("telegram"), whatsapp: fakeChannel("whatsapp") }
  seedConnector(db, "telegram", "channel")
  seedConnector(db, "whatsapp", "channel")
  const tgChat = ensureChat(db, { channel: "telegram", externalId: "42" }, now)
  const worker = createOutboxWorker({ db, channel: (ch) => handles[ch], log: silentLogger, clock: () => now })
  return {
    db,
    handles,
    tgChat,
    worker,
    get now() {
      return now
    },
    advance(ms: number) {
      now += ms
    },
    row: (id: string) => db.select().from(outbox).where(eq(outbox.id, id)).get()!,
  }
}

function owners(db: DB) {
  const { nonce } = createClaim(db, "telegram", 1)
  claimOwner(db, { channel: "telegram", nonce, externalId: "42" }, 2)
  setWhatsAppOwner(db, "08123456789", 3)
}

describe("outbox worker", () => {
  it("claims, sends and acks with the channel message id", async () => {
    const t = setup()
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "halo" }, source: "system" }, t.now)
    await t.worker.tick()
    expect(t.handles.telegram!.sent).toEqual([{ to: "42", payload: { text: "halo" } }])
    const row = t.row(r.id)
    expect(row.status).toBe("sent")
    expect(row.channelMsgId).toBe("telegram-1")
    await t.worker.tick()
    expect(t.handles.telegram!.sent).toHaveLength(1)
  })

  it("retries with backoff 5 s, 30 s, 2 min, 10 min, 30 min cap", async () => {
    const t = setup()
    t.handles.telegram!.failNext = 10
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "x" }, source: "system" }, t.now)
    const waits: number[] = []
    for (let i = 0; i < 6; i++) {
      await t.worker.tick()
      const row = t.row(r.id)
      expect(row.status).toBe("pending")
      waits.push(row.nextAttemptAt - t.now)
      // Not due yet: nothing happens.
      await t.worker.tick()
      expect(t.row(r.id).attempts).toBe(i + 1)
      t.advance(row.nextAttemptAt - t.now)
    }
    expect(waits).toEqual([5_000, 30_000, 120_000, 600_000, 1_800_000, 1_800_000])
  })

  it("fails a row past expiresAt with an event", async () => {
    const t = setup()
    t.handles.telegram!.failNext = 10
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "x" }, source: "reminder:r1", expiresAt: t.now + 10_000 }, t.now)
    await t.worker.tick()
    t.advance(20_000)
    await t.worker.tick()
    expect(t.row(r.id).status).toBe("failed")
    expect(listEvents(t.db).some((e) => e.kind === "outbox.failed")).toBe(true)
  })

  it("lets rows wait on a paused channel without using attempts, then sends on resume", async () => {
    const t = setup()
    pauseConnector(t.db, "telegram", "test", t.now)
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "x" }, source: "system" }, t.now)
    for (let i = 0; i < 3; i++) {
      await t.worker.tick()
      t.advance(60_000)
    }
    expect(t.row(r.id)).toMatchObject({ status: "pending", attempts: 0 })
    seedConnector(t.db, "telegram", "channel")
    await t.worker.tick()
    expect(t.row(r.id).status).toBe("sent")
  })

  it("moves rows of a removed channel to the fallback, or cancels them when no fallback is on", async () => {
    const t = setup()
    owners(t.db)
    setSetting(t.db, "fallbackChannel", "whatsapp")
    const a = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "a" }, source: "system" }, t.now)
    deleteConnector(t.db, "telegram", t.now)
    await t.worker.tick()
    expect(t.row(a.id)).toMatchObject({ status: "sent", channel: "whatsapp", fallbackFrom: "telegram" })
    expect(t.handles.whatsapp!.sent[0]!.to).toBe("628123456789@s.whatsapp.net")

    setSetting(t.db, "fallbackChannel", "none")
    const b = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "b" }, source: "system" }, t.now)
    expect(rerouteChannelRows(t.db, "telegram", t.now)).toEqual({ moved: 0, cancelled: 1 })
    expect(t.row(b.id).status).toBe("cancelled")
    expect(listEvents(t.db).some((e) => e.kind === "outbox.reroute" && e.message.includes("cancelled"))).toBe(true)
  })

  it("puts rows stuck in sending for over 5 minutes back to pending", async () => {
    const t = setup()
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "x" }, source: "system" }, t.now)
    t.db.update(outbox).set({ status: "sending", claimedAt: t.now }).where(eq(outbox.id, r.id)).run()
    t.advance(4 * 60_000)
    await t.worker.tick()
    expect(t.row(r.id).status).toBe("sending")
    t.advance(2 * 60_000)
    await t.worker.tick()
    expect(t.row(r.id).status).toBe("sent")
  })

  it("moves a reminder to the fallback channel after 2 failures", async () => {
    const t = setup()
    owners(t.db)
    setSetting(t.db, "fallbackChannel", "whatsapp")
    t.handles.telegram!.failNext = 99
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "bayar listrik" }, source: "reminder:r1", expiresAt: t.now + 86_400_000 }, t.now)
    await t.worker.tick()
    expect(t.row(r.id)).toMatchObject({ attempts: 1, channel: "telegram" })
    t.advance(5_000)
    await t.worker.tick()
    // Moved and sent right away on the fallback.
    await t.worker.tick()
    expect(t.row(r.id)).toMatchObject({ status: "sent", channel: "whatsapp", fallbackFrom: "telegram" })
    expect(t.handles.whatsapp!.sent.map((s) => s.payload.text)).toEqual(["bayar listrik"])
    expect(listEvents(t.db).some((e) => e.kind === "outbox.fallback")).toBe(true)
  })

  it("releases claims on stop", async () => {
    const t = setup()
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "x" }, source: "system" }, t.now)
    t.db.update(outbox).set({ status: "sending", claimedAt: t.now }).where(eq(outbox.id, r.id)).run()
    await t.worker.stop()
    expect(t.row(r.id).status).toBe("pending")
  })
})

describe("outbox worker: pauses, hung sends and reroutes", () => {
  it("doesn't expire rows while their channel is paused, and sends them on resume", async () => {
    const t = setup()
    pauseConnector(t.db, "telegram", "test", t.now)
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "telat" }, source: "reminder:r1", expiresAt: t.now + 24 * 3600_000 }, t.now)
    for (let i = 0; i < 30; i++) {
      t.advance(3600_000)
      await t.worker.tick()
    }
    expect(t.row(r.id)).toMatchObject({ status: "pending", attempts: 0 })
    seedConnector(t.db, "telegram", "channel")
    await t.worker.tick()
    expect(t.row(r.id).status).toBe("sent")
  })

  it("leaves a hung send in sending (no retry that could double-send) and keeps other channels moving", async () => {
    const db = memoryDb()
    const now = 1_000_000
    seedConnector(db, "telegram", "channel")
    seedConnector(db, "whatsapp", "channel")
    const tgChat = ensureChat(db, { channel: "telegram", externalId: "42" }, now)
    const waChat = ensureChat(db, { channel: "whatsapp", externalId: "628123456789@s.whatsapp.net" }, now)
    const wa = fakeChannel("whatsapp")
    const hung: FakeChannel = { ...fakeChannel("telegram"), send: () => new Promise(() => {}) }
    const handles: Partial<Record<Channel, FakeChannel>> = { telegram: hung, whatsapp: wa }
    const worker = createOutboxWorker({ db, channel: (ch) => handles[ch], log: silentLogger, clock: () => now, sendTimeoutMs: 50 })
    const a = enqueueOutbox(db, { chatId: tgChat.id, channel: "telegram", payload: { text: "a" }, source: "system" }, now)
    const b = enqueueOutbox(db, { chatId: tgChat.id, channel: "telegram", payload: { text: "b" }, source: "system" }, now)
    const slow = worker.tick()
    // WhatsApp sends while the Telegram send hangs.
    const c = enqueueOutbox(db, { chatId: waChat.id, channel: "whatsapp", payload: { text: "c" }, source: "system" }, now)
    await worker.tick()
    expect(db.select().from(outbox).where(eq(outbox.id, c.id)).get()!.status).toBe("sent")
    await slow
    // Still in flight: not retried, so it can't arrive twice. The stuck-sending sweep reclaims it after 5 min.
    expect(db.select().from(outbox).where(eq(outbox.id, a.id)).get()!).toMatchObject({ status: "sending", attempts: 0 })
    // The rest of the hung channel's batch went back without using an attempt.
    expect(db.select().from(outbox).where(eq(outbox.id, b.id)).get()).toMatchObject({ status: "pending", attempts: 0 })
  })

  it("records a timed-out send that lands late as sent, and one that fails late as a failed attempt", async () => {
    const db = memoryDb()
    const now = 1_000_000
    seedConnector(db, "telegram", "channel")
    const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, now)
    const settle: { ok?: (v: { channelMsgId?: string }) => void; err?: (e: Error) => void }[] = []
    const slowCh: FakeChannel = { ...fakeChannel("telegram"), send: () => new Promise((ok, err) => void settle.push({ ok, err })) }
    const worker = createOutboxWorker({ db, channel: () => slowCh, log: silentLogger, clock: () => now, sendTimeoutMs: 20 })
    const a = enqueueOutbox(db, { chatId: chat.id, channel: "telegram", payload: { text: "a" }, source: "system" }, now)
    await worker.tick()
    settle[0]!.ok!({ channelMsgId: "m1" })
    await new Promise((r) => setTimeout(r, 5))
    expect(db.select().from(outbox).where(eq(outbox.id, a.id)).get()!).toMatchObject({ status: "sent", channelMsgId: "m1" })
    const b = enqueueOutbox(db, { chatId: chat.id, channel: "telegram", payload: { text: "b" }, source: "system" }, now)
    await worker.tick()
    settle[1]!.err!(new Error("socket closed"))
    await new Promise((r) => setTimeout(r, 5))
    expect(db.select().from(outbox).where(eq(outbox.id, b.id)).get()!).toMatchObject({ status: "pending", attempts: 1 })
  })

  it("never reroutes a row that's being sent", () => {
    const t = setup()
    owners(t.db)
    setSetting(t.db, "fallbackChannel", "whatsapp")
    const r = enqueueOutbox(t.db, { chatId: t.tgChat.id, channel: "telegram", payload: { text: "x" }, source: "system" }, t.now)
    t.db.update(outbox).set({ status: "sending", claimedAt: t.now }).where(eq(outbox.id, r.id)).run()
    expect(rerouteChannelRows(t.db, "telegram", t.now)).toEqual({ moved: 0, cancelled: 0 })
    expect(t.row(r.id)).toMatchObject({ status: "sending", channel: "telegram" })
  })
})
