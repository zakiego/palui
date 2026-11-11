// Chat commands (PLAN 8.2, 4.6). They never reach the model and keep working
// when every model connector is off.
import { getSetting } from "../settings"
import { formatClock, tokensToday, getUsage, usageKeys } from "../budget"
import { rotateSession, type ChatRow } from "../chats"
import { autoSave, findMemory } from "../memory"
import { channelName, selfChannelOffUndo } from "../saklar/rules"
import { getConnector, listConnectors, pauseConnector, resumeConnector } from "../saklar/store"
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
    case "saklar":
      return cmdSaklar(ctx, chat, args)
  }
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
    `Token hari ini: ${tokensToday(db, now).toLocaleString("id-ID")} / ${budgets.dailyTokenCap.toLocaleString("id-ID")}`,
    `Run jam ini: ${getUsage(db, usageKeys.runs(now, tz))} / ${budgets.maxRunsPerHour}`,
  ]
  const paused = getSetting(db, "turnsPausedUntil")
  if (paused && paused > now) lines.push(`Turn dijeda sampai ${formatClock(paused, tz)}: ${getSetting(db, "turnsPausedReason") ?? ""}`)
  lines.push("", "Saklar:")
  for (const c of ctx.host.status()) lines.push(`• ${c.id}: ${STATUS_LABEL[c.status] ?? c.status}${c.detail ? ` (${c.detail})` : ""}`)
  return lines.join("\n")
}

async function cmdSaklar(ctx: EngineCtx, chat: ChatRow, args: string[]): Promise<CommandReply> {
  const { db } = ctx
  const [verb, id] = args.map((a) => a.toLowerCase())
  if (!verb) {
    const rows = listConnectors(db).filter((r) => r.status !== "not_set_up")
    const text = ["Saklar:", ...rows.map((r) => `• ${r.id}: ${STATUS_LABEL[r.status] ?? r.status}`), "", "Pakai /saklar on <id> atau /saklar off <id>."].join("\n")
    const actions = rows
      .filter((r) => r.id !== "web")
      .map((r) => (r.enabled ? { id: `saklar:off:${r.id}`, label: `Matikan ${r.label}` } : { id: `saklar:on:${r.id}`, label: `Nyalakan ${r.label}` }))
    return { text, actions: chat.channel === "telegram" ? actions : undefined }
  }
  if ((verb !== "on" && verb !== "off") || !id) return { text: "Formatnya: /saklar, /saklar on <id>, atau /saklar off <id>." }
  const row = getConnector(db, id)
  if (!row) return { text: `Gak ada saklar \`${id}\`. Cek daftarnya pakai /saklar.` }
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
  return { text: `${row.label} dimatikan. Buat nyalain lagi: kirim \`/saklar on ${id}\`.` }
}
