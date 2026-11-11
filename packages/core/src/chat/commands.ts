// Chat commands (PLAN 8.2, 4.6). They never reach the model and keep working
// when every model connector is off.
import { agentForChat, assignAgent, getAgent, getAgentBySlug, getDefaultAgent, isSharedChat, listAgents } from "../agents"
import { getSetting } from "../settings"
import { activePause, formatClock, tokensToday, getUsage, usageKeys } from "../budget"
import { eq } from "drizzle-orm"
import { chats } from "@palui/db"
import { rotateSession, type ChatRow } from "../chats"
import { autoSave, findMemory } from "../memory"
import { channelName, selfChannelOffUndo } from "../connectors/rules"
import { getConnector, listConnectors, pauseConnector, resumeConnector } from "../connectors/store"
import type { CommandName } from "./router"
import type { EngineCtx } from "./turn"

export type CommandReply = {
  text: string
  actions?: { id: string; label: string }[]
  // Runs after the reply was sent (turning off the channel the reply goes through).
  after?: () => Promise<void>
}

const STATUS_LABEL: Record<string, string> = { on: "nyala", paused: "dijeda", error: "error", connecting: "nyambung...", not_set_up: "belum disetup" }

export async function runCommand(ctx: EngineCtx, chat: ChatRow, name: CommandName, args: string[]): Promise<CommandReply> {
  switch (name) {
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

async function cmdAgent(ctx: EngineCtx, chat: ChatRow, args: string[]): Promise<CommandReply> {
  const { db } = ctx
  const current = agentForChat(db, chat)
  const arg = args[0]?.toLowerCase()
  if (!arg) {
    const lines = [`Agent di chat ini: ${current.name}`, "", "Agent yang ada:", ...listAgents(db).map((a) => `• ${a.slug}: ${a.name}${a.isDefault ? " (default)" : ""}`), "", "Ganti pakai /agent <slug>. Balik ke Palui: /agent off."]
    return { text: lines.join("\n") }
  }
  if (chat.connectorId) return { text: `Chat ini masuk lewat bot milik ${current.name}, jadi agent-nya tetap.` }
  const target = arg === "off" ? getDefaultAgent(db) : getAgentBySlug(db, arg)
  if (!target) return { text: `Gak ada agent \`${arg}\`. Cek daftarnya pakai /agent.` }
  if (target.id === current.id) return { text: `Udah pakai ${target.name}.` }
  await switchAgent(ctx, chat.id, target.isDefault ? null : target.id)
  const shared = isSharedChat(db, chat)
  if (target.isDefault) return { text: shared ? "Oke, balik ke Palui. Di sini cuma kamu yang aku jawab." : "Oke, balik ke Palui. Mulai sesi baru." }
  const who = shared ? (chat.kind === "group" ? " Orang lain di grup ini bisa ngobrol sama dia kalau mention atau reply pesannya." : " Orang di chat ini sekarang dijawab sama dia.") : ""
  return { text: `Oke, sekarang ${target.name} yang jawab di sini. Mulai sesi baru.${who}` }
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
