// Chat commands (PLAN 8.2, 4.6). They never reach the model and keep working
// when every model connector is off.
import { agentForChat, assignAgent, getAgent, getAgentBySlug, getDefaultAgent, isSharedChat, listAgents } from "../agents"
import { getSetting } from "../settings"
import { activePause, formatClock, tokensToday, getUsage, usageKeys } from "../budget"
import { eq } from "drizzle-orm"
import { chats } from "@palui/db"
import { rotateSession, setAck, setReplyMode, type ChatRow } from "../chats"
import { parseAckEmoji } from "../ack"
import { autoSave, findMemory } from "../memory"
import { channelName, ownerChat, selfChannelOffUndo } from "../connectors/rules"
import { getConnector, listConnectors, pauseConnector, resumeConnector } from "../connectors/store"
import type { CommandName } from "./command-names"
import { helpText, idText, toolsText, usageText } from "./commands-info"
import { cmdBtw, cmdCompact, cmdModel, modelsText } from "./commands-model"
import { createReport } from "./report"
import { sendToChat, type EngineCtx } from "./turn"

export type CommandReply = {
  // Empty when the whole answer comes from `later`.
  text: string
  actions?: { id: string; label: string }[]
  // "plain" skips the channel's markdown conversion (the default for replies).
  format?: "plain" | "markdown"
  // Runs after the reply was sent (turning off the channel the reply goes through).
  after?: () => Promise<void>
  // Slow work (a model or network call): the router moves on to the next
  // message and the text this returns is sent when it's done. `queued` runs it
  // in the chat's order, so it never lands in the middle of a turn.
  later?: { run: () => Promise<string>; queued?: boolean; format?: "plain" | "markdown" }
}

// The message that carried the command: /btw reads its text, /id its ids.
export type CommandMessage = { id: string; text: string; replyToChannelMsgId?: string | null }

const STATUS_LABEL: Record<string, string> = { on: "nyala", paused: "dijeda", error: "error", connecting: "nyambung...", not_set_up: "belum disetup" }

export async function runCommand(ctx: EngineCtx, chat: ChatRow, name: CommandName, args: string[], msg?: CommandMessage): Promise<CommandReply> {
  switch (name) {
    case "help":
      return { text: helpText(ctx, chat) }
    case "id":
      return { text: idText(ctx, chat, msg) }
    case "usage":
      return { text: usageText(ctx, chat) }
    case "tools":
      return { text: "", later: { run: () => toolsText(ctx, chat, args) } }
    case "models":
      return { text: "", later: { run: () => modelsText(ctx, args) } }
    case "model":
      return cmdModel(ctx, chat, args)
    case "btw":
      return cmdBtw(ctx, chat, msg)
    case "compact":
      return cmdCompact(ctx, chat)
    case "new":
      return cmdNew(ctx, chat)
    case "stop":
      return cmdStop(ctx, chat)
    case "status":
      return { text: statusText(ctx) }
    case "connectors":
      return cmdConnectors(ctx, chat, args)
    case "agent":
      return cmdAgent(ctx, chat, args)
    case "reply":
      return cmdReply(ctx, chat, args)
    case "ack":
      return cmdAck(ctx, chat, args)
    case "report":
      return cmdReport(ctx, chat, args)
  }
}

// Switch a chat's agent (PLAN 19.2): save the session to the old agent's
// memory, assign, then start a fresh session so the new persona isn't
// confused by the old one's replies. Shared by /agent and the dashboard.
export async function switchAgent(ctx: EngineCtx, chatId: string, agentId: string | null) {
  const before = ctx.db.select().from(chats).where(eq(chats.id, chatId)).get()
  if (!before) throw new Error(`No chat ${chatId}`)
  const target = agentId ? getAgent(ctx.db, agentId) : getDefaultAgent(ctx.db)
  if (!target) throw new Error(`No agent ${agentId}`)
  const same = agentForChat(ctx.db, before).id === target.id
  if (!same && before.sessionId) {
    try {
      await autoSave(ctx.db, findMemory(ctx.host.running()), before.sessionId, ctx.log)
    } catch (e) {
      ctx.log.warn("auto-save before an agent switch failed", { chat_id: chatId, error: e })
    }
  }
  const r = assignAgent(ctx.db, chatId, target.isDefault ? null : target.id)
  if (!same) rotateSession(ctx.db, chatId, ctx.clock())
  return { ...r, changed: !same }
}

const REPLY_MODE_TEXT = { mention: "kalau di-mention atau di-reply", all: "semua pesan" } as const

async function cmdAgent(ctx: EngineCtx, chat: ChatRow, args: string[]): Promise<CommandReply> {
  const { db } = ctx
  const current = agentForChat(db, chat)
  const arg = args[0]?.toLowerCase()
  const where = chat.kind === "group" ? "grup ini" : "chat ini"
  // A chat on an agent's own bot keeps that agent.
  const pickable = chat.connectorId ? [current] : listAgents(db)
  const choices = () => pickable.map((a) => `• /agent ${a.slug}: ${a.name}${a.isDefault && isSharedChat(db, chat) ? ", cuma jawab kamu" : ""}`)
  if (!arg && !chat.allowed) {
    // The setup prompt: the first thing the owner sees in a new group.
    const head = chat.channel === "whatsapp" ? `Aku belum aktif di ${where}.` : `Orang lain di ${where} belum dijawab.`
    const more = chat.connectorId ? [] : ["", "Agent baru bisa dibikin di dashboard, menu Agents."]
    return { text: [`${head} Pilih siapa yang jawab:`, ...choices(), ...more].join("\n") }
  }
  if (!arg) {
    const mode = chat.kind === "group" ? [`Jawab ${REPLY_MODE_TEXT[chat.replyMode]}. Ganti pakai /reply.`] : []
    if (chat.connectorId) return { text: [`Agent di ${where}: ${current.name} (lewat bot-nya sendiri)`, ...mode].join("\n") }
    return { text: [`Agent di ${where}: ${current.name}`, ...mode, "", "Agent yang ada:", ...choices(), "", "Balik ke Palui: /agent off."].join("\n") }
  }
  const target = arg === "off" ? getDefaultAgent(db) : getAgentBySlug(db, arg)
  if (chat.connectorId && target?.id !== current.id) return { text: `Chat ini masuk lewat bot milik ${current.name}, jadi agent-nya tetap.` }
  if (!target) return { text: `Gak ada agent \`${arg}\`. Cek daftarnya pakai /agent.` }
  if (target.id === current.id && chat.allowed) return { text: `Udah pakai ${target.name}.` }
  const { changed } = await switchAgent(ctx, chat.id, target.isDefault ? null : target.id)
  const fresh = changed ? " Mulai sesi baru." : ""
  const shared = isSharedChat(db, chat)
  if (target.isDefault) return { text: shared ? `Oke, ${target.name} yang jawab di sini, cuma buat kamu.${fresh}` : `Oke, ${target.name} yang jawab di sini.${fresh}` }
  if (chat.kind === "group") {
    const mode = chat.replyMode === "all" ? "Dia jawab semua pesan di grup ini." : "Dia jawab kalau di-mention atau di-reply. Biar jawab semua pesan: /reply all"
    return { text: `Oke, sekarang ${target.name} yang jawab di sini.${fresh}\n${mode}` }
  }
  const who = shared ? " Orang di chat ini sekarang dijawab sama dia." : ""
  return { text: `Oke, sekarang ${target.name} yang jawab di sini.${fresh}${who}` }
}

// Groups only (PLAN 19.2): answer @mentions and replies, or every message.
function cmdReply(ctx: EngineCtx, chat: ChatRow, args: string[]): CommandReply {
  if (chat.kind !== "group") return { text: "Di chat pribadi semua pesan selalu dijawab. /reply cuma buat grup." }
  const name = agentForChat(ctx.db, chat).name
  const arg = args[0]?.toLowerCase()
  if (!arg) {
    const other = chat.replyMode === "all" ? "mention" : "all"
    return { text: `${name} jawab ${REPLY_MODE_TEXT[chat.replyMode]}. Ganti: /reply ${other}` }
  }
  if (arg !== "all" && arg !== "mention") return { text: "Pilih /reply all atau /reply mention." }
  setReplyMode(ctx.db, chat.id, arg)
  const off = chat.allowed ? "" : "\nGrup ini belum nyala. Pilih agent dulu: /agent"
  if (arg === "mention") return { text: `Oke, ${name} sekarang cuma jawab ${REPLY_MODE_TEXT.mention}.${off}` }
  // A Telegram bot with privacy mode on never sees messages that don't mention
  // it, and the change only reaches groups it joins afterwards.
  const tg = chat.channel === "telegram" ? "\nDi Telegram, matiin dulu privacy mode bot-nya di @BotFather, lalu keluarin dan masukin lagi bot-nya ke grup." : ""
  return { text: `Oke, ${name} sekarang jawab semua pesan di grup ini.${off}${tg}` }
}

// The reaction while a reply is being written (PLAN 5): on, off, or which
// emoji. Only WhatsApp sends one; typing keeps showing either way.
function cmdAck(ctx: EngineCtx, chat: ChatRow, args: string[]): CommandReply {
  const raw = args.join(" ").trim()
  const arg = raw.toLowerCase()
  const only = chat.channel === "whatsapp" ? "" : "\nReaksi cuma ada di WhatsApp, di sini yang muncul cuma typing."
  if (!arg) {
    const state = chat.ack ? `nyala pakai ${chat.ackEmoji}` : `mati (emoji-nya ${chat.ackEmoji})`
    return { text: `Reaksi pas mikir di chat ini ${state}. Ganti: /ack ${chat.ack ? "off" : "on"}, atau /ack <emoji> buat ganti emoji.${only}` }
  }
  if (arg === "on" || arg === "off") {
    setAck(ctx.db, chat.id, { on: arg === "on" })
    return { text: arg === "on" ? `Oke, reaksi ${chat.ackEmoji} nyala lagi di chat ini.${only}` : `Oke, gak pakai reaksi lagi di chat ini. Typing tetap muncul.${only}` }
  }
  const emoji = parseAckEmoji(raw)
  if (!emoji) return { text: "Pilih /ack on, /ack off, atau satu emoji, misal /ack 👀" }
  setAck(ctx.db, chat.id, { on: true, emoji })
  return { text: `Oke, reaksinya sekarang ${emoji}.${only}` }
}

const REPORT_NOTE_TIP = "Lain kali tulis juga apa yang salah, biar lebih gampang dilacak: /report <apa yang salah>"

// Plain, so the channel sends exactly what `reports` holds. From a shared chat
// the snapshot goes to the owner's DM, like every other admin detail (PLAN 19.4).
// Without a note the agent has to guess what went wrong, so the reply asks for
// one next time. In a DM that's a second message, after the report.
function cmdReport(ctx: EngineCtx, chat: ChatRow, args: string[]): CommandReply {
  const note = args.join(" ").trim()
  const report = createReport(ctx.db, chat, note, ctx.clock())
  const tip = note ? "" : `\n\n${REPORT_NOTE_TIP}`
  if (!isSharedChat(ctx.db, chat)) return { text: report.text, format: "plain", ...(note ? {} : { later: { run: async () => REPORT_NOTE_TIP } }) }
  const dm = ownerChat(ctx.db, chat.channel) ?? ownerChat(ctx.db, "web")
  if (!dm) return { text: `Report ${report.id} udah aku simpan.${tip}` }
  sendToChat(ctx, dm, report.text, "system", { format: "plain" })
  return { text: `Report ${report.id} udah aku kirim ke chat pribadi kamu.${tip}` }
}

async function cmdNew(ctx: EngineCtx, chat: ChatRow): Promise<CommandReply> {
  if (chat.sessionId) {
    try {
      await autoSave(ctx.db, findMemory(ctx.host.running()), chat.sessionId, ctx.log)
    } catch (e) {
      ctx.log.warn("auto-save on /new failed", { chat_id: chat.id, error: e })
    }
  }
  rotateSession(ctx.db, chat.id, ctx.clock())
  return { text: "Oke, mulai sesi baru. Obrolan sebelumnya udah aku simpan." }
}

function cmdStop(ctx: EngineCtx, chat: ChatRow): CommandReply {
  const active = ctx.active.get(chat.id)
  if (!active) return { text: "Gak ada yang lagi jalan." }
  active.controller.abort(new Error("stopped by the owner"))
  return { text: "Oke, aku stop." }
}

export function statusText(ctx: EngineCtx): string {
  const { db } = ctx
  const now = ctx.clock()
  const tz = getSetting(db, "timezone")
  const roles = getSetting(db, "modelRoles")
  const budgets = getSetting(db, "budgets")
  const running = new Set(ctx.host.running().map((e) => e.id))
  const chain = (roles.default ?? []).map((l) => `${l.connectorId}/${l.model}${running.has(l.connectorId) ? "" : " (off)"}`).join(" → ")
  const lines = [
    `Model: ${chain || "belum diatur"}`,
    `Token hari ini: ${tokensToday(db, now).toLocaleString("id-ID")} / ${budgets.dailyTokenCap > 0 ? budgets.dailyTokenCap.toLocaleString("id-ID") : "tanpa batas"}`,
    `Run jam ini: ${getUsage(db, usageKeys.runs(now, tz))} / ${budgets.maxRunsPerHour}`,
  ]
  const paused = activePause(db, now)
  if (paused) lines.push(`Turn dijeda sampai ${formatClock(paused.until, tz)}: ${paused.reason}`)
  lines.push("", "Konektor:")
  for (const c of ctx.host.status()) lines.push(`• ${c.id}: ${STATUS_LABEL[c.status] ?? c.status}${c.detail ? ` (${c.detail})` : ""}`)
  return lines.join("\n")
}

async function cmdConnectors(ctx: EngineCtx, chat: ChatRow, args: string[]): Promise<CommandReply> {
  const { db } = ctx
  const [verb, id] = args.map((a) => a.toLowerCase())
  if (!verb) {
    const rows = listConnectors(db).filter((r) => r.status !== "not_set_up")
    const text = ["Konektor:", ...rows.map((r) => `• ${r.id}: ${STATUS_LABEL[r.status] ?? r.status}`), "", "Pakai /connectors on <id> atau /connectors off <id>."].join("\n")
    const actions = rows
      .filter((r) => r.id !== "web")
      .map((r) => (r.enabled ? { id: `connector:off:${r.id}`, label: `Matikan ${r.label}` } : { id: `connector:on:${r.id}`, label: `Nyalakan ${r.label}` }))
    return { text, actions: chat.channel === "telegram" ? actions : undefined }
  }
  if ((verb !== "on" && verb !== "off") || !id) return { text: "Formatnya: /connectors, /connectors on <id>, atau /connectors off <id>." }
  const row = getConnector(db, id)
  if (!row) return { text: `Gak ada konektor \`${id}\`. Cek daftarnya pakai /connectors.` }
  if (row.status === "not_set_up") return { text: `${row.label} belum disetup. Sambungkan dulu dari dashboard.` }
  if (verb === "on") {
    if (row.enabled) return { text: `${row.label} udah nyala.` }
    resumeConnector(db, id, `chat (${chat.channel})`, ctx.clock())
    await ctx.host.reconcile()
    return { text: `${row.label} dinyalakan.` }
  }
  if (id === "web") return { text: "Web chat selalu nyala, gak bisa dimatikan." }
  if (!row.enabled) return { text: `${row.label} udah mati.` }
  const apply = async () => {
    pauseConnector(db, id, `chat (${chat.channel})`, ctx.clock())
    await ctx.host.reconcile()
  }
  if (row.kind === "channel" && (row.type === chat.channel || id === chat.channel)) {
    const way = selfChannelOffUndo(db, chat.channel, ctx.host.running(), id)
    if (!way) return { text: `Gak bisa matiin ${channelName(chat.channel)} dari sini: gak ada channel lain atau dashboard buat nyalain lagi.` }
    // The reply goes out through this channel, so pause only after it's sent.
    return { text: `${row.label} dimatikan. Buat nyalain lagi: ${way}.`, after: apply }
  }
  await apply()
  return { text: `${row.label} dimatikan. Buat nyalain lagi: kirim \`/connectors on ${id}\`.` }
}
