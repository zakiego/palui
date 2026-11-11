// Telegram channel runtime (PLAN 5.1): grammY long polling, owner claim through
// `/start <nonce>`, media in, HTML replies split at 4096 with inline buttons.
//
// The bot factory and file download are injectable so tests drive everything
// through a fake API transformer and never touch the network.
import { Bot, GrammyError, HttpError } from "grammy"
import type { InlineKeyboardMarkup, Message, User } from "grammy/types"
import QRCode from "qrcode"
import type { OutboxPayload } from "@palui/db"
import { sha256 } from "@palui/db/crypto"
import { config } from "@palui/core/config"
import { createClaim, getOwner } from "@palui/core/owner"
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
import { markdownToTelegramHtml, splitMessage } from "../channel-utils"
import manifest from "./manifest"

export const TELEGRAM_LIMIT = 4096
export const CALLBACK_DATA_MAX_BYTES = 64
export const DOCUMENT_MAX_BYTES = 1024 * 1024

export const COPY = {
  claimed: "Halo! Kamu sekarang owner Palui.",
  claimFailed: "Link-nya udah kedaluwarsa atau udah dipakai. Bikin link baru dari dashboard.",
} as const

export const ERRORS = {
  missingToken: "Bot token is missing. Paste it from @BotFather.",
  unauthorized: "Telegram rejected the bot token (401 Unauthorized). Copy it again from @BotFather.",
  conflict: "Telegram says another process is already polling this bot token (409 Conflict). Stop the other one, or give dev its own bot in @BotFather.",
  devGuard: "This is the prod bot token, and dev never polls with it (it would knock prod off with 409s). Create a separate dev bot in @BotFather and paste that token.",
} as const

export type TelegramDeps = {
  createBot(token: string): Bot
  // Downloads a file from Telegram's file endpoint.
  fetchFile(url: string, signal?: AbortSignal): Promise<Buffer>
  typingIntervalMs: number
  // Safety cap: a typing loop nobody switched off stops by itself.
  typingMaxMs: number
}

type Ctx = ConnectorContext<ConfigOf<typeof manifest>, SecretsOf<typeof manifest>>

async function defaultFetchFile(url: string, signal?: AbortSignal): Promise<Buffer> {
  const res = await fetch(url, { signal })
  if (!res.ok) throw new Error(`file download failed: HTTP ${res.status}`)
  return Buffer.from(await res.arrayBuffer())
}

const defaultDeps: TelegramDeps = {
  createBot: (token) => new Bot(token),
  fetchFile: defaultFetchFile,
  typingIntervalMs: 4000,
  typingMaxMs: 10 * 60_000,
}

export function describeTelegramError(err: unknown): string {
  if (err instanceof GrammyError) {
    if (err.error_code === 401) return ERRORS.unauthorized
    if (err.error_code === 409) return ERRORS.conflict
    return `Telegram error ${err.error_code}: ${err.description}`
  }
  if (err instanceof HttpError) return `Can't reach Telegram: ${err.message}`
  return err instanceof Error ? err.message : String(err)
}

export function isProdToken(token: string, cfg = config()): boolean {
  if (cfg.mode !== "dev" || !cfg.prodTokenHashes.length) return false
  const hash = sha256(token)
  return cfg.prodTokenHashes.some((h) => h.toLowerCase() === hash)
}

export function validCallbackData(data: string): boolean {
  const n = Buffer.byteLength(data, "utf8")
  return n >= 1 && n <= CALLBACK_DATA_MAX_BYTES
}

export function buildKeyboard(actions: OutboxPayload["actions"], onInvalid?: (id: string) => void): InlineKeyboardMarkup | undefined {
  const valid = (actions ?? []).filter((a) => {
    const ok = validCallbackData(a.id)
    if (!ok) onInvalid?.(a.id)
    return ok
  })
  if (!valid.length) return undefined
  const rows: { text: string; callback_data: string }[][] = []
  for (let i = 0; i < valid.length; i += 3) rows.push(valid.slice(i, i + 3).map((a) => ({ text: a.label, callback_data: a.id })))
  return { inline_keyboard: rows }
}

export type RenderedChunk = { html: string; plain: string }

// Markdown split first (so each chunk is valid on its own), then converted. A chunk
// whose HTML still runs over the limit is split again with a smaller budget.
export function renderTelegramChunks(md: string, limit = TELEGRAM_LIMIT): RenderedChunk[] {
  const out: RenderedChunk[] = []
  const go = (text: string, budget: number) => {
    for (const plain of splitMessage(text, budget)) {
      const html = markdownToTelegramHtml(plain)
      if (html.length <= limit || budget <= 256) out.push({ html, plain })
      else go(plain, Math.floor(budget * 0.6))
    }
  }
  go(md, limit)
  return out
}

function isParseError(err: unknown): boolean {
  return err instanceof GrammyError && err.error_code === 400 && /parse|entit|tag/i.test(err.description)
}

function displayName(user: User): string {
  return [user.first_name, user.last_name].filter(Boolean).join(" ") || user.username || String(user.id)
}

const START_RE = /^\/start(?:@\w+)?(?:\s+(\S+))?\s*$/

export function createTelegramRuntime(overrides: Partial<TelegramDeps> = {}) {
  const deps: TelegramDeps = { ...defaultDeps, ...overrides }

  return defineRuntime(manifest, {
    async test(ctx): Promise<TestResult> {
      const token = ctx.secrets.botToken
      if (!token) return { ok: false, detail: ERRORS.missingToken }
      if (isProdToken(token)) return { ok: false, detail: ERRORS.devGuard }
      try {
        const me = await deps.createBot(token).api.getMe()
        return { ok: true, detail: `@${me.username} reachable`, data: { username: me.username } }
      } catch (err) {
        return { ok: false, detail: describeTelegramError(err) }
      }
    },

    async start(ctx): Promise<RunningConnector> {
      return startTelegram(ctx, deps)
    },
  })
}

async function startTelegram(ctx: Ctx, deps: TelegramDeps): Promise<RunningConnector> {
  const token = ctx.secrets.botToken
  if (!token) {
    ctx.setStatus("error", ERRORS.missingToken)
    throw new Error(ERRORS.missingToken)
  }
  if (isProdToken(token)) {
    ctx.setStatus("error", ERRORS.devGuard)
    throw new Error(ERRORS.devGuard)
  }

  const log = ctx.log.child({ connector_id: ctx.id })
  const bot = deps.createBot(token)
  let stopping = false
  let username: string | undefined = ctx.config.botUsername
  const typingLoops = new Map<string, { interval: ReturnType<typeof setInterval>; cap: ReturnType<typeof setTimeout> }>()

  const learnUsername = (name: string | undefined) => {
    if (!name || name === username) return
    username = name
    ctx.patchConfig({ botUsername: name })
  }

  async function download(fileId: string): Promise<Buffer | null> {
    try {
      const file = await bot.api.getFile(fileId)
      if (!file.file_path) return null
      return await deps.fetchFile(`https://api.telegram.org/file/bot${token}/${file.file_path}`, ctx.signal)
    } catch (err) {
      log.warn("telegram file download failed", { err: describeTelegramError(err) })
      return null
    }
  }

  async function mediaOf(msg: Message, isOwner: boolean): Promise<{ media: InboundMedia[]; note?: string }> {
    // Strangers are dropped by the router anyway; don't spend bandwidth on them.
    if (!isOwner) return { media: [] }
    if (msg.photo?.length) {
      const largest = msg.photo.reduce((a, b) => (b.width * b.height > a.width * a.height ? b : a))
      const data = await download(largest.file_id)
      return { media: data ? [{ kind: "image", mimeType: "image/jpeg", data: data.toString("base64") }] : [] }
    }
    if (msg.voice) {
      const data = await download(msg.voice.file_id)
      return {
        media: data ? [{ kind: "audio", mimeType: msg.voice.mime_type ?? "audio/ogg", data: data.toString("base64"), durationSec: msg.voice.duration }] : [],
      }
    }
    if (msg.document) {
      const doc = msg.document
      const name = doc.file_name ?? "document"
      if ((doc.file_size ?? 0) > DOCUMENT_MAX_BYTES) {
        const mb = ((doc.file_size ?? 0) / 1024 / 1024).toFixed(1)
        return { media: [], note: `[document ${name} (${mb} MB) is over the 1 MB limit and was not read]` }
      }
      const data = await download(doc.file_id)
      if (!data) return { media: [] }
      if (data.length > DOCUMENT_MAX_BYTES) return { media: [], note: `[document ${name} is over the 1 MB limit and was not read]` }
      return { media: [{ kind: "document", mimeType: doc.mime_type ?? "application/octet-stream", data: data.toString("base64"), filename: name }] }
    }
    return { media: [] }
  }

  bot.on("message", async (gctx) => {
    const msg = gctx.message
    const from = msg.from
    if (!from || from.is_bot) return
    const text = msg.text ?? msg.caption ?? ""

    const start = msg.text ? START_RE.exec(msg.text) : null
    if (start?.[1]) {
      const ok = await ctx.services.claimOwner({ channel: "telegram", nonce: start[1], externalId: String(from.id), displayName: displayName(from) })
      ctx.event(ok ? "owner_claimed" : "owner_claim_failed", ok ? `Telegram owner claimed by ${from.id}` : `Telegram claim refused for ${from.id}`, ok ? "info" : "warn")
      await gctx.reply(ok ? COPY.claimed : COPY.claimFailed)
      return
    }

    const isOwner = ctx.services.isOwner({ channel: "telegram", externalId: String(from.id) })
    const { media, note } = await mediaOf(msg, isOwner)
    const body = [text, note].filter(Boolean).join("\n")
    if (!body && !media.length) {
      log.debug("telegram message without supported content dropped", { chat_id: msg.chat.id })
      return
    }

    const inbound: InboundMessage = {
      channel: "telegram",
      connectorId: ctx.id,
      chatExternalId: String(msg.chat.id),
      chatKind: msg.chat.type === "private" ? "dm" : "group",
      chatTitle: msg.chat.type === "private" ? displayName(from) : msg.chat.title,
      senderExternalId: String(from.id),
      senderName: displayName(from),
      channelMsgId: String(msg.message_id),
      text: body,
      at: msg.date * 1000,
    }
    if (msg.reply_to_message) inbound.replyToChannelMsgId = String(msg.reply_to_message.message_id)
    if (media.length) inbound.media = media
    await ctx.services.inbound(inbound)
  })

  bot.on("callback_query:data", async (gctx) => {
    const q = gctx.callbackQuery
    const chat = q.message?.chat
    const inbound: InboundMessage = {
      channel: "telegram",
      connectorId: ctx.id,
      chatExternalId: String(chat?.id ?? q.from.id),
      chatKind: !chat || chat.type === "private" ? "dm" : "group",
      senderExternalId: String(q.from.id),
      senderName: displayName(q.from),
      text: "",
      callback: { data: q.data, callbackId: q.id },
      at: Date.now(),
    }
    if (q.message) inbound.replyToChannelMsgId = String(q.message.message_id)
    await ctx.services.inbound(inbound)
    // Stop the spinner on the button. The router can't add a toast afterwards:
    // Telegram accepts one answer per query.
    await answerCallback(q.id)
  })

  bot.catch((err) => {
    log.error("telegram update handler failed", { err: err.error instanceof Error ? err.error.message : String(err.error) })
  })

  async function answerCallback(callbackId: string, text?: string): Promise<void> {
    try {
      await bot.api.answerCallbackQuery(callbackId, text ? { text } : undefined)
    } catch (err) {
      log.debug("answerCallbackQuery failed", { err: describeTelegramError(err) })
    }
  }

  function stopTyping(chatId: string) {
    const loop = typingLoops.get(chatId)
    if (!loop) return
    clearInterval(loop.interval)
    clearTimeout(loop.cap)
    typingLoops.delete(chatId)
  }

  const channel: ChannelHandle = {
    channel: "telegram",

    async send(chatExternalId, payload) {
      stopTyping(chatExternalId)
      const chunks =
        payload.format === "plain"
          ? splitMessage(payload.text, TELEGRAM_LIMIT).map((t) => ({ html: "", plain: t }))
          : renderTelegramChunks(payload.text)
      if (!chunks.length) chunks.push({ html: "", plain: " " })
      const keyboard = buildKeyboard(payload.actions, (id) => log.warn("telegram button dropped: callback_data must be 1 to 64 bytes", { id }))
      const replyTo = payload.replyToChannelMsgId ? Number(payload.replyToChannelMsgId) : Number.NaN
      let lastId: number | undefined
      for (const [i, chunk] of chunks.entries()) {
        const other: Record<string, unknown> = {}
        if (i === 0 && Number.isInteger(replyTo)) other.reply_parameters = { message_id: replyTo, allow_sending_without_reply: true }
        if (i === chunks.length - 1 && keyboard) other.reply_markup = keyboard
        let sent: Message.TextMessage
        if (chunk.html) {
          try {
            sent = await bot.api.sendMessage(chatExternalId, chunk.html, { ...other, parse_mode: "HTML" })
          } catch (err) {
            if (!isParseError(err)) throw err
            log.warn("telegram rejected HTML, sending plain text", { err: describeTelegramError(err) })
            sent = await bot.api.sendMessage(chatExternalId, chunk.plain, other)
          }
        } else {
          sent = await bot.api.sendMessage(chatExternalId, chunk.plain, other)
        }
        lastId = sent.message_id
      }
      return { channelMsgId: lastId === undefined ? undefined : String(lastId) }
    },

    async typing(chatExternalId, on) {
      stopTyping(chatExternalId)
      if (!on || stopping) return
      const tick = () => {
        bot.api.sendChatAction(chatExternalId, "typing").catch((err) => log.debug("sendChatAction failed", { err: describeTelegramError(err) }))
      }
      tick()
      const interval = setInterval(tick, deps.typingIntervalMs)
      const cap = setTimeout(() => stopTyping(chatExternalId), deps.typingMaxMs)
      interval.unref?.()
      cap.unref?.()
      typingLoops.set(chatExternalId, { interval, cap })
    },

    reachable: () => !stopping && bot.isRunning(),

    answerCallback,
  }

  ctx.setStatus("connecting")
  const polling = bot.start({
    allowed_updates: ["message", "callback_query"],
    onStart: (me) => {
      learnUsername(me.username)
      log.info("telegram polling started", { username: me.username })
      ctx.setStatus("on")
    },
  })
  polling.then(
    () => {
      if (!stopping) ctx.setStatus("error", "Telegram polling stopped unexpectedly")
    },
    (err: unknown) => {
      if (stopping) return
      const detail = describeTelegramError(err)
      log.error("telegram polling failed", { err: detail })
      ctx.setStatus("error", detail)
    },
  )

  const stop = async () => {
    if (stopping) return
    stopping = true
    for (const id of [...typingLoops.keys()]) stopTyping(id)
    if (bot.isRunning()) {
      try {
        await bot.stop()
      } catch (err) {
        log.debug("bot.stop failed", { err: describeTelegramError(err) })
      }
    }
    await polling.catch(() => {})
  }
  ctx.signal.addEventListener("abort", () => void stop(), { once: true })

  return {
    stop,
    channel,

    live() {
      return {
        username: username ?? null,
        ownerClaimed: !!getOwner(ctx.db, "telegram"),
        polling: bot.isRunning(),
      }
    },

    async action(name) {
      if (name !== "claimLink") throw new Error(`Unknown Telegram action: ${name}`)
      if (!username) {
        const me = await bot.api.getMe()
        learnUsername(me.username)
      }
      if (!username) throw new Error("Bot username unknown")
      const { nonce, expiresAt } = createClaim(ctx.db, "telegram")
      const url = `https://t.me/${username}?start=${nonce}`
      const qrSvg = await QRCode.toString(url, { type: "svg", margin: 1 })
      return { url, expiresAt, qrSvg }
    },
  }
}

export default createTelegramRuntime()
