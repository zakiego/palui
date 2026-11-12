// Chats and sessions (PLAN 9). A chat is one conversation on one channel; its
// current session holds the summary and the messages in context.
import { and, desc, eq, gte, inArray, like, lt, or, sql, type SQL } from "drizzle-orm"
import { chats, messages, newId, sessions, type Channel, type DB, type ReplyMode } from "@palui/db"

export type ChatRow = typeof chats.$inferSelect
export type SessionRow = typeof sessions.$inferSelect

export const WEB_CHAT_EXTERNAL_ID = "web"

export function getChat(db: DB, id: string): ChatRow | undefined {
  return db.select().from(chats).where(eq(chats.id, id)).get()
}

// `connectorId`: the bot the chat came in on when it isn't the channel's main
// connector (an agent's own Telegram bot, PLAN 19.6). "" means the main one.
export function findChat(db: DB, channel: Channel, externalId: string, connectorId = ""): ChatRow | undefined {
  return db
    .select()
    .from(chats)
    .where(and(eq(chats.channel, channel), eq(chats.connectorId, connectorId), eq(chats.externalId, externalId)))
    .get()
}

// Groups only (PLAN 19.2): answer @mentions and replies, or every message.
export function setReplyMode(db: DB, chatId: string, mode: ReplyMode): ChatRow | undefined {
  return db.update(chats).set({ replyMode: mode }).where(eq(chats.id, chatId)).returning().get()
}

// Find or create the chat and make sure it has an open session. `allowed` and
// `agentId` only apply to a new chat.
export function ensureChat(
  db: DB,
  input: { channel: Channel; externalId: string; title?: string; kind?: "dm" | "group"; allowed?: boolean; connectorId?: string; agentId?: string | null },
  now = Date.now(),
): ChatRow {
  const connectorId = input.connectorId ?? ""
  return db.transaction((tx) => {
    let chat = tx
      .select()
      .from(chats)
      .where(and(eq(chats.channel, input.channel), eq(chats.connectorId, connectorId), eq(chats.externalId, input.externalId)))
      .get()
    if (!chat) {
      chat = tx
        .insert(chats)
        .values({
          id: newId("chat"),
          channel: input.channel,
          connectorId,
          externalId: input.externalId,
          title: input.title ?? "",
          kind: input.kind ?? "dm",
          allowed: input.allowed ?? true,
          agentId: input.agentId ?? null,
          createdAt: now,
        })
        .returning()
        .get()
    } else if (input.title && input.title !== chat.title) {
      chat = tx.update(chats).set({ title: input.title }).where(eq(chats.id, chat.id)).returning().get()
    }
    if (!chat.sessionId) {
      const s = tx.insert(sessions).values({ id: newId("ses"), chatId: chat.id, createdAt: now }).returning().get()
      chat = tx.update(chats).set({ sessionId: s.id }).where(eq(chats.id, chat.id)).returning().get()
    }
    return chat
  })
}

// The dashboard's "Palui (web)" chat.
export function ensureWebChat(db: DB, now = Date.now()): ChatRow {
  return ensureChat(db, { channel: "web", externalId: WEB_CHAT_EXTERNAL_ID, title: "Palui (web)" }, now)
}

export function getSession(db: DB, id: string): SessionRow | undefined {
  return db.select().from(sessions).where(eq(sessions.id, id)).get()
}

// `/new`: close the current session and open a fresh one. Returns both.
export function rotateSession(db: DB, chatId: string, now = Date.now()): { closed: SessionRow | undefined; opened: SessionRow } {
  return db.transaction((tx) => {
    const chat = tx.select().from(chats).where(eq(chats.id, chatId)).get()
    if (!chat) throw new Error(`No chat ${chatId}`)
    let closed: SessionRow | undefined
    if (chat.sessionId) {
      closed = tx.update(sessions).set({ closedAt: now }).where(eq(sessions.id, chat.sessionId)).returning().get()
    }
    const opened = tx.insert(sessions).values({ id: newId("ses"), chatId, createdAt: now }).returning().get()
    tx.update(chats).set({ sessionId: opened.id }).where(eq(chats.id, chatId)).run()
    return { closed, opened }
  })
}

// --- Cross-chat lookup (the chat history skill) --------------------------------------

export type ChatSearch = {
  // Matched against the title, the external id and the chat id (case-insensitive substring).
  query?: string
  channel?: Channel
  kind?: "dm" | "group"
  limit?: number
  // Extra filter on chats, like chatsServedBy() for an agent's tools.
  scope?: SQL
}

export type ChatHit = ChatRow & { messageCount: number }

export const CHAT_SEARCH_MAX = 20

// Only owner and Palui text counts as conversation: tool and system rows are plumbing.
const TALK_ROLES = ["user", "assistant"] as const

function likeArg(q: string): string {
  return `%${q.replace(/[\\%_]/g, (c) => `\\${c}`)}%`
}

function contains(col: Parameters<typeof like>[0], q: string): SQL {
  return sql`${col} like ${likeArg(q)} escape '\\'`
}

// Chats matching the search, most recent conversation first, with how many
// owner and Palui messages each holds.
export function searchChats(db: DB, s: ChatSearch = {}): ChatHit[] {
  const where: SQL[] = []
  const q = s.query?.trim()
  if (q) where.push(or(contains(chats.title, q), contains(chats.externalId, q), contains(chats.id, q))!)
  if (s.channel) where.push(eq(chats.channel, s.channel))
  if (s.kind) where.push(eq(chats.kind, s.kind))
  if (s.scope) where.push(s.scope)
  const limit = Math.min(Math.max(1, s.limit ?? CHAT_SEARCH_MAX), CHAT_SEARCH_MAX)
  // Spelled out: drizzle drops the table name from columns in a single-table select,
  // which would turn the correlation into messages.chat_id = messages.id.
  const messageCount = sql<number>`(select count(*) from "messages" m where m."chat_id" = "chats"."id" and m."role" in ('user', 'assistant'))`
  return db
    .select({ chat: chats, messageCount })
    .from(chats)
    .where(where.length ? and(...where) : undefined)
    .orderBy(sql`${chats.lastMessageAt} is null`, desc(chats.lastMessageAt), desc(chats.createdAt))
    .limit(limit)
    .all()
    .map((r) => ({ ...r.chat, messageCount: Number(r.messageCount) }))
}

export type ChatMessageQuery = {
  limit?: number
  // Only messages strictly before this instant (UTC ms).
  before?: number
  // Only messages at or after this instant (UTC ms).
  since?: number
  // Case-insensitive substring of the message text.
  query?: string
}

export type ChatMessageRow = typeof messages.$inferSelect

export const CHAT_READ_DEFAULT = 30
export const CHAT_READ_MAX = 100

// The newest owner and Palui messages of a chat that match, returned oldest first.
export function readChatMessages(db: DB, chatId: string, q: ChatMessageQuery = {}): ChatMessageRow[] {
  const where: SQL[] = [eq(messages.chatId, chatId), inArray(messages.role, [...TALK_ROLES])]
  if (q.before !== undefined) where.push(lt(messages.createdAt, q.before))
  if (q.since !== undefined) where.push(gte(messages.createdAt, q.since))
  const text = q.query?.trim()
  if (text) where.push(contains(messages.text, text))
  const limit = Math.min(Math.max(1, q.limit ?? CHAT_READ_DEFAULT), CHAT_READ_MAX)
  return db
    .select()
    .from(messages)
    .where(and(...where))
    .orderBy(desc(messages.createdAt), desc(messages.id))
    .limit(limit)
    .all()
    .reverse()
}

// A chat by its id, or by an exact external id (WhatsApp JID, Telegram chat id).
// Several chats can share an external id across channels; pass the channel then.
export function resolveChat(db: DB, ref: string, channel?: Channel): ChatRow[] {
  const byId = getChat(db, ref)
  if (byId && (!channel || byId.channel === channel)) return [byId]
  const where = channel ? and(eq(chats.externalId, ref), eq(chats.channel, channel)) : eq(chats.externalId, ref)
  return db.select().from(chats).where(where).all()
}
