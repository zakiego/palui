import { existsSync, mkdirSync, mkdtempSync, rmSync, utimesSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { WAMessage } from "baileys"
import { getOwner } from "@palui/core/owner"
import type { RunningConnector } from "@palui/core/connectors/define"
import { testContext } from "../channel-utils/testing"
import { AuthLockedError, STALE_AFTER_MS, acquireAuthLock } from "./lock"
import manifest from "./manifest"
import { formatPairingCode, pairingDigits } from "./messages"
import { EXPIRES_HINT, REASONS, THINKING_REACTION, authDirFor, backoffDelay, createWhatsAppRuntime, lockPathFor, type WhatsAppDeps, type WhatsAppLive } from "./runtime"
import { FakeSocket } from "./test-socket"

const OWNER_JID = "628123456789@s.whatsapp.net"
const OWNER_LID = "555000111@lid"

type Config = { ownerPhone?: string; pairPhone?: string; linkedNumber?: string }

let dataDir: string
const running: RunningConnector[] = []

beforeEach(() => {
  dataDir = mkdtempSync(join(tmpdir(), "palui-wa-"))
})

afterEach(async () => {
  vi.useRealTimers()
  for (const r of running.splice(0)) await r.stop()
  rmSync(dataDir, { recursive: true, force: true })
})

// Lets real IO (fs) and promise chains settle, without relying on timers.
async function until(cond: () => boolean, max = 2000) {
  for (let i = 0; i < max && !cond(); i++) await new Promise((r) => setImmediate(r))
  if (!cond()) throw new Error("condition not met")
}
const settle = async () => {
  for (let i = 0; i < 30; i++) await new Promise((r) => setImmediate(r))
}

function setup(opts: { config?: Config; registered?: boolean; deps?: Partial<WhatsAppDeps>; id?: string } = {}) {
  const sockets: FakeSocket[] = []
  const downloads: string[] = []
  const runtime = createWhatsAppRuntime({
    createSocket: async ({ authDir }) => {
      const s = new FakeSocket({ registered: opts.registered ?? existsSync(join(authDir, "creds.json")) })
      sockets.push(s)
      return s
    },
    downloadMedia: async (m) => {
      downloads.push(m.key.id ?? "")
      return Buffer.from(`media:${m.key.id}`)
    },
    ...opts.deps,
  })
  const ctx = testContext({ type: "whatsapp", id: opts.id ?? "whatsapp", config: opts.config ?? { ownerPhone: "0812-3456-789" }, dataDir })
  const start = async () => {
    const r = await runtime.start(ctx)
    running.push(r)
    return r
  }
  const sock = () => {
    const s = sockets.at(-1)
    if (!s) throw new Error("no socket yet")
    return s
  }
  return { runtime, ctx, sockets, downloads, start, sock }
}

const live = (r: RunningConnector) => r.live?.() as WhatsAppLive

async function openRunning(opts: Parameters<typeof setup>[0] = {}) {
  const s = setup({ registered: true, ...opts })
  const r = await s.start()
  s.sock().open()
  return { ...s, r }
}

function dm(id: string, extra: Partial<WAMessage> = {}, key: Partial<WAMessage["key"]> = {}): Partial<WAMessage> & { key: WAMessage["key"] } {
  return {
    key: { remoteJid: OWNER_JID, fromMe: false, id, ...key },
    message: { conversation: `pesan ${id}` },
    messageTimestamp: 1_700_000_000,
    pushName: "Zaki",
    ...extra,
  }
}

describe("whatsapp manifest", () => {
  it("has the wizard steps and remove warning", () => {
    expect(manifest.setup.map((s) => ("action" in s ? s.action : s.field))).toEqual(["ownerPhone", "pairWhatsApp", "utusanCleared"])
    expect(manifest.removeWarning).toBe("This unlinks the WhatsApp device. You'll need to pair again.")
    expect(manifest.config.safeParse({ ownerPhone: "+62 812 3456 789" }).success).toBe(true)
    expect(manifest.config.safeParse({ ownerPhone: "abc" }).success).toBe(false)
  })

  it("formats pairing codes and phone digits", () => {
    expect(formatPairingCode("abcd1234")).toBe("ABCD-1234")
    expect(pairingDigits("0898-0000-1472")).toBe("6289800001472")
    expect(pairingDigits("+62 898 0000 1472")).toBe("6289800001472")
  })
})

describe("whatsapp pairing", () => {
  it("requests one pairing code on the first qr and tracks qr rotation", async () => {
    const s = setup({ registered: false, config: { ownerPhone: "0812-3456-789", pairPhone: "0898-0000-1472" } })
    const r = await s.start()
    s.sock().qr("QR-1")
    await until(() => live(r).pairingCode !== null && live(r).qrSvg !== null)
    expect(s.sock().pairingRequests).toEqual(["6289800001472"])
    expect(live(r)).toMatchObject({ connection: "pairing", qr: "QR-1", pairingCode: "ABCD-1234", expiresHint: EXPIRES_HINT, pairPhone: "6289800001472" })
    expect(live(r).qrSvg).toContain("<svg")
    expect(s.ctx.statuses.at(-1)).toEqual({ status: "connecting", detail: REASONS.waitingForPairing })

    s.sock().qr("QR-2")
    expect(live(r).qr).toBe("QR-2")
    await settle()
    expect(s.sock().pairingRequests).toHaveLength(1)

    s.sock().open()
    expect(s.ctx.statuses.at(-1)).toEqual({ status: "on" })
    expect(live(r)).toMatchObject({ connection: "open", linked: true, linkedNumber: "628980001472", qr: null, qrSvg: null, pairingCode: null, expiresHint: null })
    expect(s.ctx.patches).toContainEqual({ linkedNumber: "628980001472" })
    expect(r.channel?.reachable()).toBe(true)
  })

  it("shows only the QR when no pairing number is known", async () => {
    const s = setup({ registered: false })
    const r = await s.start()
    s.sock().qr("QR-ONLY")
    await settle()
    expect(s.sock().pairingRequests).toEqual([])
    expect(live(r)).toMatchObject({ qr: "QR-ONLY", pairingCode: null })
  })

  it("stops after the pairing window closes, and pair restarts it", async () => {
    const s = setup({ registered: false, config: { pairPhone: "6289800001472" } })
    const r = await s.start()
    s.sock().qr("QR-1")
    await settle()
    s.sock().close(408)
    expect(s.ctx.statuses.at(-1)).toEqual({ status: "error", detail: REASONS.pairingExpired })
    await settle()
    expect(s.sockets).toHaveLength(1)

    const res = await r.action?.("pair", { phone: "0811-2222-3333" })
    expect(res).toEqual({ ok: true, pairPhone: "6281122223333" })
    expect(s.ctx.patches).toContainEqual({ pairPhone: "6281122223333" })
    expect(s.sockets).toHaveLength(2)
    s.sock().qr("QR-2")
    await until(() => live(r).pairingCode !== null)
    expect(s.sock().pairingRequests).toEqual(["6281122223333"])
  })

  it("refuses to re-pair while linked and open", async () => {
    const { r } = await openRunning()
    expect(await r.action?.("pair", {})).toMatchObject({ ok: false })
  })

  it("clears an unfinished pairing session before connecting", async () => {
    const authDir = authDirFor(dataDir, "whatsapp")
    mkdirSync(authDir, { recursive: true })
    writeFileSync(join(authDir, "creds.json"), JSON.stringify({ me: { id: "628@s.whatsapp.net" }, registered: false }))
    const s = setup({})
    await s.start()
    expect(existsSync(join(authDir, "creds.json"))).toBe(false)
  })

  it("keeps a linked session on disk", async () => {
    const authDir = authDirFor(dataDir, "whatsapp")
    mkdirSync(authDir, { recursive: true })
    writeFileSync(join(authDir, "creds.json"), JSON.stringify({ me: { id: "628@s.whatsapp.net" }, account: { details: "x" } }))
    const s = setup({})
    await s.start()
    expect(existsSync(join(authDir, "creds.json"))).toBe(true)
    expect((await s.runtime.test(s.ctx)).data).toEqual({ linked: true })
  })
})

describe("whatsapp pair safety", () => {
  function writeLinkedCreds() {
    const authDir = authDirFor(dataDir, "whatsapp")
    mkdirSync(authDir, { recursive: true })
    writeFileSync(join(authDir, "creds.json"), JSON.stringify({ me: { id: "628@s.whatsapp.net" }, account: { details: "x" }, registered: true }))
    return authDir
  }

  it("refuses Pair while a linked session is on disk, even when not open", async () => {
    const authDir = writeLinkedCreds()
    const s = setup({})
    const r = await s.start()
    // Still connecting (never opened), e.g. during a reconnect.
    expect(await r.action?.("pair", {})).toMatchObject({ ok: false, error: expect.stringMatching(/already linked/) })
    expect(existsSync(join(authDir, "creds.json"))).toBe(true)
    expect(s.sockets).toHaveLength(1)
  })

  it("allows Pair over a session that a 401 logged out", async () => {
    const authDir = writeLinkedCreds()
    const s = setup({ registered: true })
    const r = await s.start()
    s.sock().open()
    s.sock().close(401)
    expect(await r.action?.("pair", {})).toMatchObject({ ok: true })
    expect(existsSync(join(authDir, "creds.json"))).toBe(false)
    expect(s.sockets).toHaveLength(2)
  })

  it("never ends up with two sockets when Pair lands during a reconnect", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
    const sockets: FakeSocket[] = []
    let release: (() => void) | undefined
    let slow = false
    const runtime = createWhatsAppRuntime({
      createSocket: async () => {
        const sock = new FakeSocket({ registered: false })
        sockets.push(sock)
        if (slow) {
          slow = false
          await new Promise<void>((resolve) => {
            release = resolve
          })
        }
        return sock
      },
      downloadMedia: async () => Buffer.from(""),
    })
    const ctx = testContext({ type: "whatsapp", id: "whatsapp", config: { ownerPhone: "0812-3456-789" }, dataDir })
    const r = await runtime.start(ctx)
    running.push(r)
    sockets[0]?.qr("QR-1")
    // A restart-required close schedules a backoff reconnect; its socket is slow.
    sockets[0]?.close(515)
    slow = true
    await vi.advanceTimersByTimeAsync(1000)
    await until(() => sockets.length === 2)
    // Pair while that reconnect is still waiting on createSocket.
    expect(await r.action?.("pair", { phone: "0811-2222-3333" })).toMatchObject({ ok: true })
    expect(sockets).toHaveLength(3)
    release?.()
    await settle()
    // The stale reconnect's socket is ended and never wired up.
    expect(sockets[1]?.ended).toBe(true)
    expect(sockets[1]?.listenerCount("connection.update")).toBe(0)
    expect(sockets[2]?.ended).toBe(false)
    expect(sockets[2]?.listenerCount("connection.update")).toBe(1)
  })
})

describe("whatsapp connection lifecycle", () => {
  it("pauses on 440 and does not reconnect", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
    const { r, sock, sockets, ctx } = await openRunning()
    sock().close(440)
    expect(ctx.statuses.at(-1)).toEqual({ status: "paused", detail: REASONS.replaced })
    expect(live(r).reason).toBe(REASONS.replaced)
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    await settle()
    expect(sockets).toHaveLength(1)
    expect(r.channel?.reachable()).toBe(false)
  })

  it("reports unlinked on 401 and does not reconnect", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
    const { r, sock, sockets, ctx } = await openRunning()
    sock().close(401)
    expect(ctx.statuses.at(-1)).toEqual({ status: "error", detail: "unlinked" })
    expect(live(r).linked).toBe(false)
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    await settle()
    expect(sockets).toHaveLength(1)
  })

  it("reconnects 515 and other closes with exponential backoff, reset on open", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
    const { sock, sockets, ctx } = await openRunning()
    sock().close(515)
    expect(ctx.statuses.at(-1)?.detail).toMatch(/Reconnecting in 1s/)
    await vi.advanceTimersByTimeAsync(999)
    expect(sockets).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    await until(() => sockets.length === 2)

    sock().close(428)
    expect(ctx.statuses.at(-1)?.detail).toMatch(/Reconnecting in 2s/)
    await vi.advanceTimersByTimeAsync(2000)
    await until(() => sockets.length === 3)

    sock().close(500)
    await vi.advanceTimersByTimeAsync(3999)
    expect(sockets).toHaveLength(3)
    await vi.advanceTimersByTimeAsync(1)
    await until(() => sockets.length === 4)

    sock().open()
    sock().close(515)
    expect(ctx.statuses.at(-1)?.detail).toMatch(/Reconnecting in 1s/)
    // The replaced sockets no longer have listeners.
    expect(sockets[0]?.listenerCount("connection.update")).toBe(0)
  })

  it("starts one replacement per socket even when close is reported twice", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] })
    const { sock, sockets } = await openRunning()
    const first = sock()
    first.close(428)
    first.close(428)
    await vi.advanceTimersByTimeAsync(10_000)
    await until(() => sockets.length === 2)
    await settle()
    expect(sockets).toHaveLength(2)
  })

  it("caps the backoff at 60 s", () => {
    expect([0, 1, 2, 5, 6, 10].map((a) => backoffDelay(a))).toEqual([1000, 2000, 4000, 32000, 60000, 60000])
  })

  it("syncs the owner identity from the configured phone", async () => {
    const { ctx } = await openRunning()
    expect(getOwner(ctx.db, "whatsapp")?.externalId).toBe(OWNER_JID)
  })
})

describe("whatsapp inbound", () => {
  it("forwards a phone JID DM with its LID from the mapping", async () => {
    const { sock, ctx } = await openRunning()
    sock().lids.set(OWNER_LID, OWNER_JID)
    sock().message(dm("IN1"))
    await until(() => ctx.inbound.length === 1)
    expect(ctx.inbound[0]).toEqual({
      channel: "whatsapp",
      connectorId: "whatsapp",
      chatExternalId: OWNER_JID,
      chatKind: "dm",
      chatTitle: "Zaki",
      senderExternalId: OWNER_JID,
      senderAltId: OWNER_LID,
      senderName: "Zaki",
      channelMsgId: "IN1",
      text: "pesan IN1",
      at: 1_700_000_000_000,
    })
  })

  it("maps a LID DM through remoteJidAlt", async () => {
    const { sock, ctx } = await openRunning()
    sock().message(dm("IN2", {}, { remoteJid: OWNER_LID, remoteJidAlt: OWNER_JID }))
    await until(() => ctx.inbound.length === 1)
    expect(ctx.inbound[0]).toMatchObject({ chatExternalId: OWNER_LID, senderExternalId: OWNER_LID, senderAltId: OWNER_JID })
  })

  it("maps a LID DM through the LID mapping when no alt is given", async () => {
    const { sock, ctx } = await openRunning()
    sock().lids.set(OWNER_LID, "628123456789:5@s.whatsapp.net")
    sock().message(dm("IN3", {}, { remoteJid: OWNER_LID }))
    await until(() => ctx.inbound.length === 1)
    expect(ctx.inbound[0]).toMatchObject({ senderExternalId: OWNER_LID, senderAltId: OWNER_JID })
  })

  it("drops fromMe, groups, status, newsletters and non-notify batches", async () => {
    const { sock, ctx } = await openRunning()
    sock().message(dm("A", {}, { fromMe: true }))
    sock().message(dm("B", {}, { remoteJid: "120363@g.us", participant: OWNER_JID }))
    sock().message(dm("C", {}, { remoteJid: "status@broadcast" }))
    sock().message(dm("D", {}, { remoteJid: "1203630@newsletter" }))
    sock().message(dm("E"), "append")
    sock().message(dm("F"))
    await until(() => ctx.inbound.length === 1)
    await settle()
    expect(ctx.inbound.map((m) => m.channelMsgId)).toEqual(["F"])
  })

  it("reads extended text with the quoted id", async () => {
    const { sock, ctx } = await openRunning()
    sock().message(dm("IN4", { message: { extendedTextMessage: { text: "yang ini", contextInfo: { stanzaId: "OLD1" } } } }))
    await until(() => ctx.inbound.length === 1)
    expect(ctx.inbound[0]).toMatchObject({ text: "yang ini", replyToChannelMsgId: "OLD1" })
  })

  it("downloads owner media and skips stranger media", async () => {
    const { sock, ctx, downloads } = await openRunning()
    sock().message(dm("IMG1", { message: { imageMessage: { caption: "ini apa", mimetype: "image/jpeg" } } }))
    await until(() => ctx.inbound.length === 1)
    expect(ctx.inbound[0]).toMatchObject({ text: "ini apa", media: [{ kind: "image", mimeType: "image/jpeg", data: Buffer.from("media:IMG1").toString("base64") }] })

    sock().message(dm("VN1", { message: { audioMessage: { mimetype: "audio/ogg; codecs=opus", seconds: 4 } } }))
    await until(() => ctx.inbound.length === 2)
    expect(ctx.inbound[1]?.media?.[0]).toMatchObject({ kind: "audio", durationSec: 4 })

    sock().message(dm("IMG2", { message: { imageMessage: { caption: "spam" } } }, { remoteJid: "629999@s.whatsapp.net" }))
    await until(() => ctx.inbound.length === 3)
    expect(ctx.inbound[2]?.media).toBeUndefined()
    expect(downloads).toEqual(["IMG1", "VN1"])
  })

  it("does not download documents over 1 MB", async () => {
    const { sock, ctx, downloads } = await openRunning()
    sock().message(dm("DOC1", { message: { documentMessage: { fileName: "big.pdf", fileLength: 5 * 1024 * 1024, mimetype: "application/pdf" } } }))
    await until(() => ctx.inbound.length === 1)
    expect(ctx.inbound[0]?.text).toContain("over the 1 MB limit")
    expect(downloads).toEqual([])
  })
})

describe("whatsapp typing and send", () => {
  it("reacts while thinking and clears the reaction on off", async () => {
    const { r, sock, ctx } = await openRunning()
    sock().message(dm("IN1"))
    await until(() => ctx.inbound.length === 1)
    await r.channel?.typing?.(OWNER_JID, true)
    expect(sock().sent.at(-1)).toMatchObject({ jid: OWNER_JID, content: { react: { text: THINKING_REACTION, key: { id: "IN1" } } } })
    await settle()
    expect(sock().presence).toContainEqual({ type: "composing", jid: OWNER_JID })
    await r.channel?.typing?.(OWNER_JID, false)
    expect(sock().sent.at(-1)).toMatchObject({ content: { react: { text: "", key: { id: "IN1" } } } })
    expect(sock().presence.at(-1)).toEqual({ type: "paused", jid: OWNER_JID })
  })

  it("sends WhatsApp formatting with an actions hint, quoting the original", async () => {
    const { r, sock, ctx } = await openRunning()
    sock().message(dm("IN1"))
    await until(() => ctx.inbound.length === 1)
    await r.channel?.typing?.(OWNER_JID, true)
    const res = await r.channel?.send(OWNER_JID, {
      text: "**Minum obat** sekarang",
      replyToChannelMsgId: "IN1",
      actions: [
        { id: "rem:done:r1", label: "Done" },
        { id: "rem:snooze:r1:1h", label: "1 jam" },
        { id: "rem:snooze:r1:tomorrow", label: "Besok" },
      ],
    })
    const textSends = sock().sent.filter((x) => "text" in x.content)
    expect(textSends).toHaveLength(1)
    expect(textSends[0]?.content).toEqual({ text: "*Minum obat* sekarang\n\nBalas: done / tunda 1j / tunda besok" })
    expect(textSends[0]?.options?.quoted?.key.id).toBe("IN1")
    expect(res?.channelMsgId).toMatch(/^OUT\d+$/)
    // The thinking reaction is cleared on reply.
    expect(sock().sent.at(-1)).toMatchObject({ content: { react: { text: "" } } })
  })

  it("sends without a quote when the message is unknown and refuses when offline", async () => {
    const { r, sock } = await openRunning()
    await r.channel?.send(OWNER_JID, { text: "halo", replyToChannelMsgId: "NOPE", format: "plain" })
    expect(sock().sent.at(-1)).toEqual({ jid: OWNER_JID, content: { text: "halo" } })
    sock().close(440)
    await expect(r.channel?.send(OWNER_JID, { text: "halo" })).rejects.toThrow(REASONS.notConnected)
  })
})

describe("whatsapp auth lock", () => {
  it("refuses a second runtime on the same auth dir until the first stops", async () => {
    const a = setup({})
    const ra = await a.start()
    const b = setup({})
    await expect(b.start()).rejects.toBeInstanceOf(AuthLockedError)
    expect(b.ctx.statuses.at(-1)?.status).toBe("error")
    expect(b.sockets).toHaveLength(0)
    await ra.stop()
    await b.start()
    expect(b.sockets).toHaveLength(1)
  })

  function writeLock(name: string, info: object, ageMs = 0) {
    const path = lockPathFor(dataDir, name)
    mkdirSync(join(dataDir, "wa-auth"), { recursive: true })
    writeFileSync(path, JSON.stringify(info))
    if (ageMs) {
      const t = new Date(Date.now() - ageMs)
      utimesSync(path, t, t)
    }
    return path
  }

  it("recovers a lock whose heartbeat stopped over 2 minutes ago", async () => {
    writeLock("whatsapp", { pid: 424242, nonce: "old", startedAt: 1 }, STALE_AFTER_MS + 1000)
    const s = setup({})
    await s.start()
    expect(s.sockets).toHaveLength(1)
  })

  it("respects a fresh lock with our own pid (another container is pid 1 too)", () => {
    const path = writeLock("x", { pid: process.pid, nonce: "other-container", startedAt: 1 })
    expect(() => acquireAuthLock(path)).toThrow(AuthLockedError)
    expect(existsSync(path)).toBe(true)
  })

  it("respects a fresh lock even when its pid isn't visible here", () => {
    const path = writeLock("y", { pid: 4242, nonce: "n", startedAt: 1 })
    expect(() => acquireAuthLock(path)).toThrow(/pid 4242/)
  })

  it("keeps a held lock fresh with the heartbeat and frees it on release", () => {
    const path = writeLock("z", { pid: 1, nonce: "crashed", startedAt: 1 }, STALE_AFTER_MS + 1000)
    let clock = Date.now()
    const lock = acquireAuthLock(path, { now: () => clock })
    // Three minutes later, with the heartbeat running, nobody else can take it.
    clock += 3 * 60_000
    lock.touch()
    expect(() => acquireAuthLock(path, { now: () => clock })).toThrow(AuthLockedError)
    expect(() => acquireAuthLock(path, { now: () => clock + 60_000 })).toThrow(AuthLockedError)
    lock.release()
    expect(existsSync(path)).toBe(false)
  })

  it("lets a new process take over once the holder's heartbeat stops", () => {
    const path = writeLock("w", { pid: 1, nonce: "old", startedAt: 1 }, STALE_AFTER_MS - 10_000)
    expect(() => acquireAuthLock(path)).toThrow(AuthLockedError)
    const later = Date.now() + 20_000
    const lock = acquireAuthLock(path, { now: () => later })
    expect(existsSync(path)).toBe(true)
    lock.release()
  })
})

describe("whatsapp unlink", () => {
  it("logs out and deletes the auth dir", async () => {
    const authDir = authDirFor(dataDir, "whatsapp")
    mkdirSync(authDir, { recursive: true })
    writeFileSync(join(authDir, "creds.json"), JSON.stringify({ account: {}, me: { id: "628@s.whatsapp.net" } }))
    const { r, sock, ctx, sockets } = await openRunning()
    const before = ctx.statuses.length
    expect(await r.action?.("unlink", {})).toEqual({ ok: true })
    expect(sock().loggedOut).toBe(true)
    expect(existsSync(authDir)).toBe(false)
    expect(live(r)).toMatchObject({ connection: "closed", linked: false, linkedNumber: null })
    expect(ctx.statuses.slice(before)).toEqual([])
    await settle()
    expect(sockets).toHaveLength(1)
  })
})
