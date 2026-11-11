// The router (PLAN 8.2): decides what an inbound message is before any model
// sees it. Order: sender checks, hook events, approval answers, commands,
// reminder actions, then a turn.
import { and, eq, like } from "drizzle-orm"
import { outbox, pendingApprovals, type DB } from "@palui/db"
import { agentForTelegramConnector } from "../agents"
import { askedIn, pendingForChat } from "../approvals/store"
import { ensureChat, findChat, type ChatRow } from "../chats"
import { isOwner } from "../owner"
import type { InboundMessage } from "../connectors/define"
import type { StoredContent } from "./context"
import type { FlowActions, ReminderActions } from "./deps"

export type CommandName = "new" | "stop" | "status" | "connectors" | "agent"

export type Route =
  | { kind: "ignore"; reason: string }
  | { kind: "command"; name: CommandName; args: string[] }
  // `batch`: a bare "ya"/"batal" with several requests waiting answers every
  // pending request of the newest run (the reply says so).
  | { kind: "approval"; approvalId: string; approved: boolean; batch?: string[] }
  // A reminder or routine check-in action, answered without the model.
  | { kind: "reminder"; reply: string }
  | { kind: "hook"; hook: NonNullable<StoredContent["hook"]>; text: string }
  // A flow's chat trigger took it: no reply, the flow answers (RFC flows 4.1).
  | { kind: "handled" }
  | { kind: "turn" }

// Which connector scope a chat lives in: an agent's own Telegram bot keeps its
// chats apart from the main bot's (PLAN 19.6). "" is the channel's main connector.
export function chatScope(msg: Pick<InboundMessage, "channel" | "connectorId">): string {
  return msg.channel === "telegram" && msg.connectorId && msg.connectorId !== "telegram" ? msg.connectorId : ""
}

// The agent a new chat in this scope starts with: an agent bot's own agent.
export function scopeAgentId(db: DB, scope: string): string | null {
  return scope ? (agentForTelegramConnector(db, scope)?.id ?? null) : null
}

// `quiet`: a group message nobody addressed to us. Normal chatter, so it's
// dropped without an event (it would flood Activity).
export type SenderVerdict = { ok: true; fromOwner: boolean } | { ok: false; reason: string; quiet?: boolean }

// Checks that need only the inbound message and the chat row, if any (PLAN 19.2).
// Dropped messages are never stored. A stranger's message in a chat we've never
// seen creates the chat row (not allowed), so the owner can set it up.
export function senderCheck(db: DB, msg: InboundMessage, now = Date.now()): SenderVerdict {
  if (msg.fromMe) return { ok: false, reason: `Dropped a ${msg.channel} message sent from the bot's own account (fromMe).` }
  const owner = isOwner(db, { channel: msg.channel, externalId: msg.senderExternalId, altId: msg.senderAltId })
  const scope = chatScope(msg)
  let chat = findChat(db, msg.channel, msg.chatExternalId, scope)
  const agentId = chat ? chat.agentId : scopeAgentId(db, scope)
  const group = msg.chatKind === "group"
  if (owner) {
    // In a group with an agent, the owner addresses it like everyone else. Commands and buttons always go through.
    if (group && agentId && !msg.addressed && !msg.callback && !msg.text.trim().startsWith("/")) return { ok: false, reason: "Group message not addressed to the agent.", quiet: true }
    return { ok: true, fromOwner: true }
  }
  if (!chat) {
    chat = ensureChat(db, { channel: msg.channel, externalId: msg.chatExternalId, title: msg.chatTitle, kind: msg.chatKind, allowed: false, connectorId: scope, agentId }, now)
  }
  if (group && !msg.addressed) return { ok: false, reason: "Group message not addressed to the agent.", quiet: true }
  if (!chat.allowed || !chat.agentId) {
    const who = `${msg.senderName ? `${msg.senderName} ` : ""}(${msg.senderExternalId}${msg.senderAltId ? `, alt ${msg.senderAltId}` : ""})`
    return { ok: false, reason: `Dropped a ${msg.channel} message from ${who}: not the owner, and this chat isn't set up for guests (allow it and give it an agent on the Chats page).` }
  }
  return { ok: true, fromOwner: false }
}

// Checks that need the chat row.
export function chatCheck(chat: ChatRow): string | null {
  if (chat.channel === "whatsapp" && !chat.allowed) return `Dropped a WhatsApp message in ${chat.externalId}: the chat isn't in Palui's allowlist.`
  return null
}

const COMMANDS: Record<string, CommandName> = { "/new": "new", "/stop": "stop", "/status": "status", "/connectors": "connectors", "/agent": "agent" }

export function parseCommand(text: string): { name: CommandName; args: string[] } | null {
  const t = text.trim()
  if (!t.startsWith("/")) return null
  const [head, ...args] = t.split(/\s+/)
  // Telegram adds @botname in groups: /status@palui_bot
  const name = COMMANDS[head!.toLowerCase().replace(/@\S+$/, "")]
  return name ? { name, args } : null
}

const YES = new Set(["ya", "y", "iya", "yes"])
const NO = new Set(["batal", "no", "tidak", "nggak", "gak"])

export function parseYesNo(text: string): boolean | null {
  const t = text.trim().toLowerCase().replace(/[.!]+$/, "")
  if (YES.has(t)) return true
  if (NO.has(t)) return false
  return null
}

// The approval whose prompt this message quotes: by the prompt's channel id, or
// through the outbox row that sent it (WhatsApp has no buttons, PLAN 8.6).
function quotedApproval(db: DB, chatId: string, channelMsgId: string): string | undefined {
  const direct = db
    .select({ id: pendingApprovals.id })
    .from(pendingApprovals)
    .where(and(askedIn(chatId), eq(pendingApprovals.promptMsgId, channelMsgId)))
    .get()
  if (direct) return direct.id
  const sent = db
    .select({ source: outbox.source })
    .from(outbox)
    .where(and(eq(outbox.chatId, chatId), eq(outbox.channelMsgId, channelMsgId), like(outbox.source, "approval:%")))
    .get()
  return sent?.source.slice("approval:".length)
}

export type RouteInput = {
  db: DB
  chat: ChatRow
  content: StoredContent
  text: string
  replyToChannelMsgId?: string
  // The stored message id (chat triggers key their run on it).
  messageId?: string
  reminderActions?: ReminderActions
  routineActions?: ReminderActions
  // Flow check-in answers ("fl:") and version approvals ("flowver:"), and chat triggers.
  flowActions?: FlowActions
  // false for a guest (PLAN 19.3): approvals, buttons, commands and reminder
  // actions are the owner's, so a guest's "ya" or "/new" is just a message.
  fromOwner?: boolean
  now: number
}

export async function route(i: RouteInput): Promise<Route> {
  const blocked = chatCheck(i.chat)
  if (blocked) return { kind: "ignore", reason: blocked }
  if (i.content.hook) return { kind: "hook", hook: i.content.hook, text: i.text }
  if (i.fromOwner === false) {
    if (i.content.callback) return { kind: "ignore", reason: "A guest pressed a button; buttons are the owner's." }
    if (!i.text.trim()) return { kind: "ignore", reason: "Empty message." }
    return { kind: "turn" }
  }

  const cb = i.content.callback?.data
  if (cb) {
    const m = /^(approve|deny):(.+)$/.exec(cb)
    if (m) return { kind: "approval", approvalId: m[2]!, approved: m[1] === "approve" }
    const s = /^connector:(on|off):(.+)$/.exec(cb)
    if (s) return { kind: "command", name: "connectors", args: [s[1]!, s[2]!] }
    if (cb.startsWith("rem:") && i.reminderActions) {
      const reply = await i.reminderActions.fromCallback(i.db, cb, i.chat.id, i.now)
      if (reply !== null) return { kind: "reminder", reply }
    }
    if (cb.startsWith("rt:") && i.routineActions) {
      const reply = await i.routineActions.fromCallback(i.db, cb, i.chat.id, i.now)
      if (reply !== null) return { kind: "reminder", reply }
    }
    if ((cb.startsWith("fl:") || cb.startsWith("flowver:")) && i.flowActions) {
      const reply = await i.flowActions.fromCallback(i.db, cb, i.chat.id, i.now)
      if (reply !== null) return { kind: "reminder", reply }
    }
    return { kind: "ignore", reason: `Unknown button "${cb}".` }
  }

  const yn = parseYesNo(i.text)
  if (yn !== null) {
    // Quoting a prompt answers that one, even when it was already answered (the reply says so).
    const quoted = i.replyToChannelMsgId ? quotedApproval(i.db, i.chat.id, i.replyToChannelMsgId) : undefined
    if (quoted) return { kind: "approval", approvalId: quoted, approved: yn }
    // Quoting some other message ("ya" to a reminder, say) is not an approval answer.
    const pending = i.replyToChannelMsgId ? [] : pendingForChat(i.db, i.chat.id)
    if (pending.length === 1) return { kind: "approval", approvalId: pending[0]!.id, approved: yn }
    if (pending.length > 1) {
      const newest = pending.at(-1)!.runId
      const batch = pending.filter((p) => p.runId === newest).map((p) => p.id)
      return { kind: "approval", approvalId: batch[0]!, approved: yn, batch }
    }
  }

  const cmd = parseCommand(i.text)
  if (cmd) return { kind: "command", ...cmd }

  if (i.flowActions?.fromChat && i.text.trim() && i.messageId) {
    if (await i.flowActions.fromChat(i.db, { text: i.text, chatId: i.chat.id, messageId: i.messageId, fromOwner: true }, i.now)) return { kind: "handled" }
  }

  if (i.reminderActions && i.text.trim()) {
    const reply = await i.reminderActions.fromText(i.db, i.text, i.replyToChannelMsgId, i.chat.id, i.now)
    if (reply !== null) return { kind: "reminder", reply }
  }
  if (i.routineActions && i.text.trim()) {
    const reply = await i.routineActions.fromText(i.db, i.text, i.replyToChannelMsgId, i.chat.id, i.now)
    if (reply !== null) return { kind: "reminder", reply }
  }
  if (i.flowActions && i.text.trim()) {
    const reply = await i.flowActions.fromText(i.db, i.text, i.replyToChannelMsgId, i.chat.id, i.now)
    if (reply !== null) return { kind: "reminder", reply }
  }
  if (!i.text.trim() && !(i.content.media ?? []).length) return { kind: "ignore", reason: "Empty message." }
  return { kind: "turn" }
}
