// WhatsApp channel runtime (PLAN 5.1) over Baileys 7, as its own linked device.
//
// Lessons carried over from utusan:
// - One socket per auth dir, guarded by a lock file (lock.ts).
// - One close per socket starts at most one replacement, or sockets multiply and
//   evict each other with 440s.
// - 440 means another session took over: pause with that reason, never fight.
// - 401 means the link is gone: error "unlinked", a human has to pair again.
// - An interrupted pairing leaves creds that block the next pairing: clear them.
// - Pairing codes push a notification to the phone, so a code is requested once
//   per pairing attempt, never on every reconnect.
import { rm } from "node:fs/promises"
import { join } from "node:path"
import type { WAMessage, WAMessageKey } from "baileys"
import QRCode from "qrcode"
import { getOwner, phoneToJid, setWhatsAppOwner } from "@palui/core/owner"
import {
  defineRuntime,
  type ChannelHandle,
  type ConfigOf,
  type ConnectorContext,
  type InboundMedia,
  type InboundMessage,
  type RunningConnector,
  type SecretsOf,
  type TestResult,
} from "@palui/core/connectors/define"
import { actionsHint, markdownToWhatsApp, splitMessage } from "../channel-utils"
import { acquireAuthLock, AuthLockedError, type AuthLock, type LockOptions } from "./lock"
import manifest from "./manifest"
import {
  bareJid,
  formatPairingCode,
  isAddressed,
  isDirectChat,
  isGroupChat,
  isLid,
  jidDigits,
  mediaOf,
  pairingDigits,
  quoteAuthor,
  quoteOf,
  textOf,
  timestampMs,
  unwrap,
} from "./messages"
import {
  clearIncompleteSession,
  createBaileysSocket,
  downloadBaileysMedia,
  inspectSession,
  isLinked,
  type MediaDownloader,
  type SocketFactory,
  type WaConnectionUpdate,
  type WaSocket,
  type WaUpsert,
} from "./socket"

export const WA_TEXT_LIMIT = 4096
export const DOCUMENT_MAX_BYTES = 1024 * 1024
export const MEDIA_MAX_BYTES = 16 * 1024 * 1024
export const THINKING_REACTION = "⏳"
export const EXPIRES_HINT = "expires in about 2 min"

export const REASONS = {
  replaced: "Another session took over this WhatsApp link",
  unlinked: "unlinked",
  pairingExpired: "Pairing expired. Press Pair again in the dashboard.",
  waitingForPairing: "Waiting for pairing",
  notConnected: "WhatsApp is not connected",
} as const

const STATUS_REPLACED = 440
const STATUS_LOGGED_OUT = 401
const STATUS_RESTART_REQUIRED = 515

export type WhatsAppDeps = {
  createSocket: SocketFactory
  downloadMedia: MediaDownloader
  lock: LockOptions
  backoffBaseMs: number
  backoffMaxMs: number
  presenceRefreshMs: number
  // How long "typing..." waits after the thinking reaction. WhatsApp clears the
  // typing indicator when a message (a reaction too) lands, and presence travels
  // faster than messages, so typing sent right away flashes and vanishes before ⏳.
  reactionLeadMs: number
  typingMaxMs: number
}

const defaultDeps: WhatsAppDeps = {
  createSocket: createBaileysSocket,
  downloadMedia: downloadBaileysMedia,
  lock: {},
  backoffBaseMs: 1000,
  backoffMaxMs: 60_000,
  presenceRefreshMs: 8000,
  reactionLeadMs: 1500,
  typingMaxMs: 10 * 60_000,
}

type Ctx = ConnectorContext<ConfigOf<typeof manifest>, SecretsOf<typeof manifest>>

export function authDirFor(dataDir: string, connectorId: string): string {
  return join(dataDir, "wa-auth", connectorId)
}

export function lockPathFor(dataDir: string, connectorId: string): string {
  // A sibling of the auth dir, so wiping the session never removes the lock.
  return join(dataDir, "wa-auth", `${connectorId}.lock`)
}

export function backoffDelay(attempt: number, baseMs = 1000, maxMs = 60_000): number {
  return Math.min(baseMs * 2 ** attempt, maxMs)
}

function statusCodeOf(update: WaConnectionUpdate): number | undefined {
  const err = update.lastDisconnect?.error as { output?: { statusCode?: number } } | undefined
  return err?.output?.statusCode
}

export type WhatsAppLive = {
  connection: "idle" | "connecting" | "pairing" | "open" | "closed"
  linked: boolean
  linkedNumber: string | null
  qr: string | null
  qrSvg: string | null
  pairingCode: string | null
  pairPhone: string | null
  expiresHint: string | null
  reason: string | null
}

export function createWhatsAppRuntime(overrides: Partial<WhatsAppDeps> = {}) {
  const deps: WhatsAppDeps = { ...defaultDeps, ...overrides }
  return defineRuntime(manifest, {
    async test(ctx): Promise<TestResult> {
      if (!ctx.config.ownerPhone) return { ok: false, detail: "Enter the owner's phone number first." }
      const state = await inspectSession(authDirFor(ctx.dataDir, ctx.id))
      if (state === "linked") {
        const n = ctx.config.linkedNumber
        return { ok: true, detail: n ? `Linked as +${n}` : "Linked session found", data: { linked: true } }
      }
      return { ok: true, detail: "Not paired yet. Start pairing to link the number.", data: { linked: false } }
    },
    start: (ctx) => startWhatsApp(ctx, deps),
  })
}

async function startWhatsApp(ctx: Ctx, deps: WhatsAppDeps): Promise<RunningConnector> {
  const log = ctx.log.child({ connector_id: ctx.id })
  const authDir = authDirFor(ctx.dataDir, ctx.id)

  let lock: AuthLock
  try {
    lock = acquireAuthLock(lockPathFor(ctx.dataDir, ctx.id), deps.lock)
  } catch (err) {
    const detail = err instanceof AuthLockedError ? err.message : `Could not lock the WhatsApp session: ${String(err)}`
    ctx.setStatus("error", detail)
    throw err instanceof Error ? err : new Error(detail)
  }

  // Keep the owner identity in step with the configured number. Same number keeps
  // the learned LID; a new number replaces the identity.
  if (ctx.config.ownerPhone) {
    const want = phoneToJid(ctx.config.ownerPhone)
    if (getOwner(ctx.db, "whatsapp")?.externalId !== want) setWhatsAppOwner(ctx.db, ctx.config.ownerPhone)
  }

  let sock: WaSocket | undefined
  let stopped = false
  // No automatic reconnects (440, 401, pairing expired, unlinked). A pair action clears it.
  let halted = false
  let attempt = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined
  let wantPairingCode = true
  let detach: (() => void) | undefined
  // Bumped by every connect, pair, unlink and stop. A connect that finds the number
  // changed after one of its awaits gives up, so a Pair pressed while a backoff
  // reconnect is mid-flight can't leave two sockets on the same creds (440s).
  let generation = 0
  // Set on a 401: the creds on disk are logged out and safe to replace by pairing.
  let loggedOut = false

  const live: WhatsAppLive = {
    connection: "idle",
    linked: false,
    linkedNumber: ctx.config.linkedNumber ?? null,
    qr: null,
    qrSvg: null,
    pairingCode: null,
    pairPhone: ctx.config.pairPhone ? pairingDigits(ctx.config.pairPhone) : null,
    expiresHint: null,
    reason: null,
  }

  // Recent messages by id, for quoting replies (inbound and our own).
  const recent = new Map<string, WAMessage>()
  const remember = (m: WAMessage | undefined) => {
    const id = m?.key?.id
    if (!m || !id) return
    recent.delete(id)
    recent.set(id, m)
    if (recent.size > 200) recent.delete(recent.keys().next().value as string)
  }
  const lastInbound = new Map<string, WAMessageKey>()
  const thinking = new Map<string, { key?: WAMessageKey; lead?: ReturnType<typeof setTimeout>; refresh?: ReturnType<typeof setInterval>; cap: ReturnType<typeof setTimeout> }>()

  const clearPairing = () => {
    live.qr = null
    live.qrSvg = null
    live.pairingCode = null
    live.expiresHint = null
  }

  const endSocket = () => {
    detach?.()
    detach = undefined
    const s = sock
    sock = undefined
    if (s) {
      try {
        s.end(undefined)
      } catch {
        // already closed
      }
    }
  }

  function scheduleReconnect(code: number | undefined) {
    const delay = backoffDelay(attempt, deps.backoffBaseMs, deps.backoffMaxMs)
    attempt++
    live.connection = "connecting"
    ctx.setStatus("connecting", `Reconnecting in ${Math.round(delay / 1000)}s (close ${code ?? "unknown"})`)
    reconnectTimer = setTimeout(() => {
      reconnectTimer = undefined
      void connect()
    }, delay)
    reconnectTimer.unref?.()
  }

  function onClose(s: WaSocket, update: WaConnectionUpdate) {
    endSocket()
    live.connection = "closed"
    if (stopped || halted) return
    const code = statusCodeOf(update)
    if (code === STATUS_REPLACED) {
      halted = true
      clearPairing()
      live.reason = REASONS.replaced
      log.warn("whatsapp connection replaced, not reconnecting")
      ctx.event("wa_replaced", REASONS.replaced, "warn")
      ctx.setStatus("paused", REASONS.replaced)
      return
    }
    if (code === STATUS_LOGGED_OUT) {
      halted = true
      loggedOut = true
      clearPairing()
      live.linked = false
      live.reason = REASONS.unlinked
      log.warn("whatsapp logged out")
      ctx.event("wa_unlinked", "WhatsApp device was unlinked", "warn")
      ctx.setStatus("error", REASONS.unlinked)
      return
    }
    if (!isLinked(s.authState.creds) && code !== STATUS_RESTART_REQUIRED) {
      // The pairing window closed without a link. Don't loop: every new code
      // pushes a notification to the phone.
      halted = true
      clearPairing()
      live.reason = REASONS.pairingExpired
      ctx.setStatus("error", REASONS.pairingExpired)
      return
    }
    scheduleReconnect(code)
  }

  function onUpdate(s: WaSocket, update: WaConnectionUpdate) {
    if (s !== sock) return
    if (update.qr) {
      const qr = update.qr
      const first = live.connection !== "pairing"
      live.connection = "pairing"
      live.qr = qr
      live.expiresHint = EXPIRES_HINT
      QRCode.toString(qr, { type: "svg", margin: 1 })
        .then((svg) => {
          if (live.qr === qr) live.qrSvg = svg
        })
        .catch((err: unknown) => log.debug("qr render failed", { err: String(err) }))
      if (first) ctx.setStatus("connecting", REASONS.waitingForPairing)
      if (wantPairingCode && live.pairPhone && !isLinked(s.authState.creds)) {
        wantPairingCode = false
        const digits = live.pairPhone
        s.requestPairingCode(digits)
          .then((code) => {
            if (s !== sock) return
            live.pairingCode = formatPairingCode(code)
            live.expiresHint = EXPIRES_HINT
            ctx.event("wa_pairing_code", `Pairing code requested for +${digits}`)
          })
          .catch((err: unknown) => {
            log.warn("could not request a pairing code", { err: String(err) })
            live.reason = `Could not request a pairing code: ${err instanceof Error ? err.message : String(err)}`
          })
      }
    }
    if (update.connection === "open") {
      attempt = 0
      clearPairing()
      live.connection = "open"
      live.linked = true
      live.reason = null
      const number = jidDigits(s.user?.id ?? "")
      if (number && number !== live.linkedNumber) {
        live.linkedNumber = number
        ctx.patchConfig({ linkedNumber: number })
      }
      log.info("whatsapp connected", { linked: number })
      ctx.setStatus("on")
      return
    }
    if (update.connection === "close") onClose(s, update)
  }

  // Group subjects, so the Chats page shows a name (re-read after an hour).
  const groupTitles = new Map<string, { title: string; at: number }>()
  async function groupTitle(s: WaSocket, jid: string): Promise<string | undefined> {
    const hit = groupTitles.get(jid)
    if (hit && Date.now() - hit.at < 3600_000) return hit.title
    try {
      const meta = await s.groupMetadata?.(jid)
      const title = meta?.subject?.trim()
      if (title) groupTitles.set(jid, { title, at: Date.now() })
      return title ?? hit?.title
    } catch (err) {
      log.debug("group metadata lookup failed", { err: String(err) })
      return hit?.title
    }
  }

  async function deliver(s: WaSocket, m: WAMessage) {
    const key = m.key
    const jid = key?.remoteJid
    if (!key || !jid || key.fromMe) return
    const group = isGroupChat(jid)
    if (!group && !isDirectChat(jid)) return
    // In a group the chat is the group and the sender is the participant.
    const sender = group ? key.participant : jid
    if (!sender) return

    const content = unwrap(m.message)
    const senderAlt = group ? key.participantAlt : key.remoteJidAlt
    let alt = senderAlt ? bareJid(senderAlt) : undefined
    if (!alt) {
      try {
        const mapping = s.signalRepository?.lidMapping
        const found = isLid(sender) ? await mapping?.getPNForLID(sender) : await mapping?.getLIDForPN(sender)
        if (found) alt = bareJid(found)
      } catch (err) {
        log.debug("lid mapping lookup failed", { err: String(err) })
      }
    }

    const text = textOf(content)
    const info = mediaOf(content)
    const media: InboundMedia[] = []
    let note: string | undefined
    if (info) {
      const owner = ctx.services.isOwner({ channel: "whatsapp", externalId: bareJid(sender), ...(alt ? { altId: alt } : {}) })
      const max = info.kind === "document" ? DOCUMENT_MAX_BYTES : MEDIA_MAX_BYTES
      if (!owner) {
        // Strangers are dropped by the router; don't download their files.
      } else if ((info.bytes ?? 0) > max) {
        note = `[${info.kind}${info.filename ? ` ${info.filename}` : ""} is over the ${max / 1024 / 1024} MB limit and was not read]`
      } else {
        try {
          const data = await deps.downloadMedia(m, s)
          if (data.length > max) {
            note = `[${info.kind}${info.filename ? ` ${info.filename}` : ""} is over the ${max / 1024 / 1024} MB limit and was not read]`
          } else {
            const item: InboundMedia = { kind: info.kind, mimeType: info.mimeType, data: data.toString("base64") }
            if (info.filename) item.filename = info.filename
            if (info.durationSec !== undefined) item.durationSec = info.durationSec
            media.push(item)
          }
        } catch (err) {
          log.warn("whatsapp media download failed", { err: String(err) })
        }
      }
    }

    const body = [text, note].filter(Boolean).join("\n")
    if (!body && !media.length) return

    remember(m)
    lastInbound.set(jid, key)

    const msg: InboundMessage = {
      channel: "whatsapp",
      connectorId: ctx.id,
      chatExternalId: jid,
      chatKind: group ? "group" : "dm",
      senderExternalId: bareJid(sender),
      text: body,
      at: timestampMs(m.messageTimestamp),
    }
    if (alt && alt !== bareJid(sender)) msg.senderAltId = alt
    if (m.pushName) msg.senderName = m.pushName
    if (group) {
      msg.addressed = isAddressed(content, [s.user?.id, s.user?.lid])
      const title = await groupTitle(s, jid)
      if (title) msg.chatTitle = title
    } else if (m.pushName) msg.chatTitle = m.pushName
    if (key.id) msg.channelMsgId = key.id
    const quote = quoteOf(content)
    if (quote?.id) msg.replyToChannelMsgId = quote.id
    if (quote) msg.quoted = { from: quoteAuthor(quote.participant, [s.user?.id, s.user?.lid], [sender, alt], !group), text: quote.text, ...(quote.media ? { media: quote.media } : {}) }
    if (media.length) msg.media = media
    await ctx.services.inbound(msg)
  }

  async function connect() {
    if (stopped || halted) return
    const mine = ++generation
    const superseded = () => stopped || halted || mine !== generation
    endSocket()
    live.connection = "connecting"
    const state = await clearIncompleteSession(authDir)
    if (state === "incomplete") log.info("cleared an unfinished WhatsApp pairing")
    if (superseded()) return
    let s: WaSocket
    try {
      s = await deps.createSocket({ authDir })
    } catch (err) {
      log.error("whatsapp socket creation failed", { err: String(err) })
      if (!superseded()) scheduleReconnect(undefined)
      return
    }
    if (superseded()) {
      try {
        s.end(undefined)
      } catch {
        // already closed
      }
      return
    }
    sock = s
    live.linked = isLinked(s.authState.creds)
    let closed = false
    const update = (u: WaConnectionUpdate) => {
      if (u.connection === "close") {
        if (closed) return
        closed = true
      }
      onUpdate(s, u)
    }
    const upsert = ({ messages, type }: WaUpsert) => {
      if (type !== "notify" || s !== sock) return
      for (const m of messages) {
        deliver(s, m).catch((err: unknown) => log.error("whatsapp inbound failed", { err: String(err) }))
      }
    }
    s.ev.on("connection.update", update)
    s.ev.on("messages.upsert", upsert)
    detach = () => {
      s.ev.off("connection.update", update)
      s.ev.off("messages.upsert", upsert)
    }
  }

  async function stopThinking(jid: string) {
    const t = thinking.get(jid)
    if (!t) return
    thinking.delete(jid)
    clearTimeout(t.lead)
    clearInterval(t.refresh)
    clearTimeout(t.cap)
    const s = sock
    if (!s || live.connection !== "open") return
    if (t.key) await s.sendMessage(jid, { react: { text: "", key: t.key } }).catch((err: unknown) => log.debug("clear reaction failed", { err: String(err) }))
    await s.sendPresenceUpdate("paused", jid).catch(() => {})
  }

  const channel: ChannelHandle = {
    channel: "whatsapp",

    async send(chatExternalId, payload) {
      const s = sock
      if (!s || live.connection !== "open") throw new Error(REASONS.notConnected)
      const body = payload.format === "plain" ? payload.text : markdownToWhatsApp(payload.text)
      const hint = actionsHint(payload.actions)
      const full = hint ? `${body}\n\n${hint}` : body
      const quoted = payload.replyToChannelMsgId ? recent.get(payload.replyToChannelMsgId) : undefined
      let lastId: string | undefined
      for (const [i, chunk] of splitMessage(full || " ", WA_TEXT_LIMIT).entries()) {
        const sent = await s.sendMessage(chatExternalId, { text: chunk }, i === 0 && quoted ? { quoted } : undefined)
        remember(sent)
        lastId = sent?.key?.id ?? lastId
      }
      await stopThinking(chatExternalId)
      return { channelMsgId: lastId }
    },

    async typing(chatExternalId, on) {
      if (!on) return stopThinking(chatExternalId)
      const s = sock
      if (!s || live.connection !== "open" || thinking.has(chatExternalId)) return
      const key = lastInbound.get(chatExternalId)
      const compose = () => {
        const cur = sock
        if (!cur) return
        ;(cur.presenceSubscribe ? cur.presenceSubscribe(chatExternalId) : Promise.resolve())
          .then(() => cur.sendPresenceUpdate("composing", chatExternalId))
          .catch(() => {})
      }
      const cap = setTimeout(() => void stopThinking(chatExternalId), deps.typingMaxMs)
      cap.unref?.()
      const t: { key?: WAMessageKey; lead?: ReturnType<typeof setTimeout>; refresh?: ReturnType<typeof setInterval>; cap: ReturnType<typeof setTimeout> } = { ...(key ? { key } : {}), cap }
      thinking.set(chatExternalId, t)
      const startTyping = () => {
        t.lead = undefined
        if (thinking.get(chatExternalId) !== t) return
        compose()
        t.refresh = setInterval(compose, deps.presenceRefreshMs)
        t.refresh.unref?.()
      }
      if (!key) return startTyping()
      // The reaction goes first and typing follows once it has landed (see reactionLeadMs).
      await s.sendMessage(chatExternalId, { react: { text: THINKING_REACTION, key } }).catch((err: unknown) => log.debug("reaction failed", { err: String(err) }))
      if (thinking.get(chatExternalId) !== t) return
      t.lead = setTimeout(startTyping, deps.reactionLeadMs)
      t.lead.unref?.()
    },

    reachable: () => !stopped && live.connection === "open",
  }

  async function pair(input: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (typeof input.phone === "string" && input.phone.trim()) {
      const digits = pairingDigits(input.phone)
      if (digits.length < 8) return { ok: false, error: "That phone number looks too short." }
      live.pairPhone = digits
      ctx.patchConfig({ pairPhone: digits })
    }
    if (live.connection === "open" && sock && isLinked(sock.authState.creds)) {
      return { ok: false, error: "Already linked. Unlink first to pair again." }
    }
    // A linked session on disk is never wiped by Pair (it may just be reconnecting,
    // or paused after a 440). Only Unlink removes it, or a 401 that logged it out.
    if (!loggedOut && (await inspectSession(authDir)) === "linked") {
      return { ok: false, error: "This number is already linked. Unlink first to pair again." }
    }
    // Cancel any connect in flight before touching the auth dir.
    generation++
    if (reconnectTimer) clearTimeout(reconnectTimer)
    reconnectTimer = undefined
    endSocket()
    // A fresh pairing: whatever session is on disk is logged out or unfinished.
    await rm(authDir, { recursive: true, force: true })
    loggedOut = false
    clearPairing()
    halted = false
    attempt = 0
    wantPairingCode = true
    live.linked = false
    live.reason = null
    ctx.setStatus("connecting", REASONS.waitingForPairing)
    await connect()
    return { ok: true, pairPhone: live.pairPhone }
  }

  async function unlink(): Promise<Record<string, unknown>> {
    halted = true
    generation++
    if (reconnectTimer) clearTimeout(reconnectTimer)
    reconnectTimer = undefined
    const s = sock
    if (s) {
      detach?.()
      detach = undefined
      try {
        await s.logout()
      } catch (err) {
        log.warn("whatsapp logout failed, deleting the session anyway", { err: String(err) })
      }
    }
    endSocket()
    await rm(authDir, { recursive: true, force: true })
    clearPairing()
    live.connection = "closed"
    live.linked = false
    live.linkedNumber = null
    live.reason = REASONS.unlinked
    ctx.event("wa_unlinked", "WhatsApp device unlinked from the dashboard")
    return { ok: true }
  }

  async function stop() {
    if (stopped) return
    stopped = true
    generation++
    if (reconnectTimer) clearTimeout(reconnectTimer)
    reconnectTimer = undefined
    for (const t of thinking.values()) {
      clearTimeout(t.lead)
      clearInterval(t.refresh)
      clearTimeout(t.cap)
    }
    thinking.clear()
    endSocket()
    live.connection = "closed"
    lock.release()
  }
  ctx.signal.addEventListener("abort", () => void stop(), { once: true })

  ctx.setStatus("connecting")
  try {
    await connect()
  } catch (err) {
    await stop()
    throw err
  }

  return {
    stop,
    channel,
    live: () => ({ ...live }),
    async action(name, input) {
      if (name === "pair") return pair(input)
      if (name === "unlink") return unlink()
      throw new Error(`Unknown WhatsApp action: ${name}`)
    },
  }
}

export default createWhatsAppRuntime()
