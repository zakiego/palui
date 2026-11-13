// Read-only chat commands: /help, /id, /usage and /tools. None of them calls a model.
import { and, desc, eq, gte } from "drizzle-orm"
import { DateTime } from "luxon"
import { messages, outbox, runs, type DB } from "@palui/db"
import { agentForChat, listAgents } from "../agents"
import { agentTokensToday, getUsage, tokensToday, usageKeys } from "../budget"
import { getSession, type ChatRow } from "../chats"
import { currentVersion, definitionOf, listFlows } from "../flows/service"
import { getSetting } from "../settings"
import { collectTools, SEARCH_TOOL, type ToolMeta } from "../tools/collect"
import { deniedTools } from "../tools/permissions"
import type { CommandMessage } from "./commands"
import type { EngineCtx } from "./turn"

const num = (n: number) => n.toLocaleString("id-ID")

// --- /help ----------------------------------------------------------------------------

const HELP: [string, string][] = [
  ["/help", "daftar perintah ini"],
  ["/status", "model, token hari ini dan status konektor"],
  ["/usage", "pemakaian token hari ini per model dan agent"],
  ["/new", "simpan sesi ini, mulai sesi baru"],
  ["/compact", "ringkas obrolan lama biar hemat token, sesinya lanjut"],
  ["/stop", "hentikan turn yang lagi jalan"],
  ["/btw <pertanyaan>", "tanya sampingan, gak masuk ke sesi"],
  ["/model [fast|vision] <nama>", "lihat atau ganti model"],
  ["/models [kata]", "daftar model dari tiap konektor model"],
  ["/tools [id]", "tool yang bisa dipakai agent di chat ini"],
  ["/connectors [on|off <id>]", "lihat, nyalakan atau matikan konektor"],
  ["/agent [slug|off]", "lihat atau ganti agent di chat ini"],
  ["/reply [all|mention]", "di grup: jawab semua pesan atau cuma mention"],
  ["/ack [on|off|emoji]", "reaksi pas lagi mikir di chat ini: nyalakan, matikan, atau ganti emoji"],
  ["/id", "id chat, sesi dan pesan. Quote sebuah pesan buat lihat id-nya"],
  ["/report [catatan]", "snapshot debug chat ini buat ditempel ke coding agent"],
]

// Commands the flows of this chat answer to ("/beres").
export function flowCommands(db: DB, chatId: string): { command: string; flow: string }[] {
  const out: { command: string; flow: string }[] = []
  for (const flow of listFlows(db, chatId)) {
    if (!flow.enabled) continue
    try {
      for (const t of definitionOf(currentVersion(db, flow)).triggers) if (t.kind === "chat" && t.command) out.push({ command: t.command, flow: flow.name })
    } catch {
      // A flow without a readable version has no command to offer.
    }
  }
  return out
}

export function helpText(ctx: EngineCtx, chat: ChatRow): string {
  const lines = ["Perintah:", ...HELP.map(([c, what]) => `• ${c}: ${what}`)]
  const own = flowCommands(ctx.db, chat.id)
  if (own.length) lines.push("", "Dari flow di chat ini:", ...own.map((f) => `• ${f.command}: flow "${f.flow}"`))
  lines.push("", "Persetujuan: balas `ya` atau `batal`. Quote pesannya kalau yang nunggu lebih dari satu.")
  return lines.join("\n")
}

// --- /id ------------------------------------------------------------------------------

// The stored row behind a quoted channel message: an inbound or reply message,
// or an outbox row (command replies, reminders and prompts have no message row).
function quotedLines(db: DB, chatId: string, channelMsgId: string): string[] {
  const m = db
    .select()
    .from(messages)
    .where(and(eq(messages.chatId, chatId), eq(messages.channelMsgId, channelMsgId)))
    .orderBy(desc(messages.createdAt))
    .limit(1)
    .get()
  if (m) {
    const lines = [`• Pesan: \`${m.id}\` (${m.role})`]
    if (m.runId) lines.push(`• Run: \`${m.runId}\``)
    if (m.sessionId) lines.push(`• Sesi: \`${m.sessionId}\``)
    return [...lines, `• Id di channel: \`${channelMsgId}\``]
  }
  const o = db
    .select()
    .from(outbox)
    .where(and(eq(outbox.chatId, chatId), eq(outbox.channelMsgId, channelMsgId)))
    .orderBy(desc(outbox.createdAt))
    .limit(1)
    .get()
  if (!o) return [`• Gak ketemu di database (id di channel: \`${channelMsgId}\`).`]
  const lines = [`• Outbox: \`${o.id}\` (${o.source})`]
  // A reply sent in several parts: only the first part is linked to the message row.
  if (o.source.startsWith("reply:")) {
    const runId = o.source.slice("reply:".length)
    const reply = db
      .select()
      .from(messages)
      .where(and(eq(messages.chatId, chatId), eq(messages.runId, runId), eq(messages.role, "assistant")))
      .limit(1)
      .get()
    if (reply) lines.push(`• Pesan: \`${reply.id}\` (assistant)`)
    lines.push(`• Run: \`${runId}\``)
    if (reply?.sessionId) lines.push(`• Sesi: \`${reply.sessionId}\``)
  }
  return [...lines, `• Id di channel: \`${channelMsgId}\``]
}

export function idText(ctx: EngineCtx, chat: ChatRow, msg?: CommandMessage): string {
  const agent = agentForChat(ctx.db, chat)
  const lines = [`Chat: \`${chat.id}\` (${chat.channel}, ${chat.externalId})`, `Sesi: ${chat.sessionId ? `\`${chat.sessionId}\`` : "belum ada"}`, `Agent: ${agent.slug} (\`${agent.id}\`)`]
  if (msg) lines.push(`Pesan ini: \`${msg.id}\``)
  if (msg?.replyToChannelMsgId) lines.push("", "Yang di-quote:", ...quotedLines(ctx.db, chat.id, msg.replyToChannelMsgId))
  else lines.push("", "Quote sebuah pesan lalu kirim /id buat lihat id pesan itu.")
  return lines.join("\n")
}

// --- /usage ---------------------------------------------------------------------------

type Tally = { runs: number; tokensIn: number; tokensOut: number; cached: number; cacheWrite: number }

const emptyTally = (): Tally => ({ runs: 0, tokensIn: 0, tokensOut: 0, cached: 0, cacheWrite: 0 })

function add(t: Tally, r: { tokensIn: number; tokensOut: number; tokensCached: number; tokensCacheWrite: number }) {
  t.runs++
  t.tokensIn += r.tokensIn
  t.tokensOut += r.tokensOut
  t.cached += r.tokensCached
  t.cacheWrite += r.tokensCacheWrite
}

// Today in the settings zone, from the budget counters and the `runs` rows. The
// counters are what the caps are checked against; the rows give the breakdown.
export function usageText(ctx: EngineCtx, chat: ChatRow): string {
  const { db } = ctx
  const now = ctx.clock()
  const tz = getSetting(db, "timezone")
  const budgets = getSetting(db, "budgets")
  const dayStart = DateTime.fromMillis(now, { zone: tz }).startOf("day").toMillis()
  const weekStart = DateTime.fromMillis(dayStart, { zone: tz }).minus({ days: 6 }).toMillis()
  const rows = db
    .select({ startedAt: runs.startedAt, chatId: runs.chatId, agentId: runs.agentId, model: runs.model, modelConnectorId: runs.modelConnectorId, tokensIn: runs.tokensIn, tokensOut: runs.tokensOut, tokensCached: runs.tokensCached, tokensCacheWrite: runs.tokensCacheWrite })
    .from(runs)
    .where(gte(runs.startedAt, weekStart))
    .all()

  const today = emptyTally()
  const week = emptyTally()
  const here = emptyTally()
  const byModel = new Map<string, Tally>()
  const byAgent = new Map<string, Tally>()
  for (const r of rows) {
    add(week, r)
    if (r.startedAt < dayStart) continue
    add(today, r)
    if (r.chatId === chat.id) add(here, r)
    if (r.model) {
      const key = `${r.modelConnectorId ?? "?"}/${r.model}`
      add(byModel.get(key) ?? byModel.set(key, emptyTally()).get(key)!, r)
    }
    if (r.agentId) add(byAgent.get(r.agentId) ?? byAgent.set(r.agentId, emptyTally()).get(r.agentId)!, r)
  }

  const cap = budgets.dailyTokenCap > 0 ? num(budgets.dailyTokenCap) : "tanpa batas"
  const lines = [
    "Pemakaian hari ini:",
    `Token ke budget: ${num(tokensToday(db, now))} / ${cap}`,
    `Run: ${num(today.runs)} (jam ini ${getUsage(db, usageKeys.runs(now, tz))} / ${budgets.maxRunsPerHour})`,
    `Input ${num(today.tokensIn)} (cache read ${num(today.cached)}, cache write ${num(today.cacheWrite)}), output ${num(today.tokensOut)}`,
  ]
  const bySize = (a: [string, Tally], b: [string, Tally]) => b[1].tokensIn + b[1].tokensOut - (a[1].tokensIn + a[1].tokensOut)
  if (byModel.size) {
    lines.push("", "Per model:")
    for (const [key, t] of [...byModel].sort(bySize)) lines.push(`• ${key}: ${num(t.runs)} run, input ${num(t.tokensIn)}, output ${num(t.tokensOut)}`)
  }
  const agents = listAgents(db).filter((a) => byAgent.has(a.id))
  if (agents.length) {
    lines.push("", "Per agent (token ke budget):")
    for (const a of agents) lines.push(`• ${a.name}: ${num(byAgent.get(a.id)!.runs)} run, ${num(agentTokensToday(db, a.id, now))} / ${a.dailyTokenCap > 0 ? num(a.dailyTokenCap) : "tanpa batas"}`)
  }
  lines.push("", `Chat ini: ${num(here.runs)} run, input ${num(here.tokensIn)}, output ${num(here.tokensOut)}`)
  lines.push(`7 hari terakhir: ${num(week.runs)} run, input ${num(week.tokensIn)}, output ${num(week.tokensOut)}`)
  lines.push("", "Cache read dihitung 10% ke budget. Ringkasan sesi dan /btw masuk budget, tapi bukan run.")
  return lines.join("\n")
}

// --- /tools ---------------------------------------------------------------------------

const CLASSES = ["read", "write", "destructive"] as const

// What a turn in this chat would be offered right now, without running one.
export async function toolsText(ctx: EngineCtx, chat: ChatRow, args: string[]): Promise<string> {
  const { db } = ctx
  const agent = agentForChat(db, chat)
  const session = chat.sessionId ? getSession(db, chat.sessionId) : undefined
  const tools = await collectTools(ctx.host.running(), {
    turn: { runId: "", chatId: chat.id, channel: chat.channel, trigger: "message", tz: getSetting(db, "timezone"), now: ctx.clock(), agentId: agent.id },
    maxTools: getSetting(db, "budgets").maxTools,
    sticky: session?.loadedTools,
    deny: deniedTools(agent.toolPermissions),
    log: ctx.log,
    onUntrustedResult: () => {},
  })
  const metas = Object.values(tools.meta).filter((m) => m.name !== SEARCH_TOOL)
  if (!metas.length) return "Gak ada tool yang nyala sekarang. Cek /connectors."
  const byConnector = new Map<string, ToolMeta[]>()
  for (const m of metas) (byConnector.get(m.connectorId) ?? byConnector.set(m.connectorId, []).get(m.connectorId)!).push(m)

  const id = args[0]?.toLowerCase()
  if (id) {
    const own = byConnector.get(id)
    if (!own) return `Gak ada tool dari \`${id}\`. Yang ada: ${[...byConnector.keys()].join(", ")}.`
    return [`${id} (${own.length} tool):`, ...own.map((m) => `• ${m.tool} (${m.class})${m.title ? `: ${m.title}` : ""}`)].join("\n")
  }
  const deferred = tools.deferred.length
  const lines = [`Tool buat ${agent.name} di chat ini: ${metas.length}${deferred ? ` (${metas.length - deferred} langsung, ${deferred} lewat pencarian tool)` : ""}`]
  for (const [connectorId, own] of byConnector) {
    const counts = CLASSES.map((c) => [c, own.filter((m) => m.class === c).length] as const).filter(([, n]) => n)
    lines.push(`• ${connectorId} (${own.length}): ${counts.map(([c, n]) => `${c} ${n}`).join(", ")}`)
  }
  lines.push("", agent.toolPermissions?.allowAll ? "Detail: /tools <id>. Semua tool jalan tanpa minta izin di chat kamu, kecuali yang diatur minta izin." : "Detail: /tools <id>. Destructive selalu minta izin, write minta izin kalau turn-nya kena data dari luar.")
  return lines.join("\n")
}
