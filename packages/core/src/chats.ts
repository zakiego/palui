// Chats and sessions (PLAN 9). A chat is one conversation on one channel; its
// current session holds the summary and the messages in context.
import { and, eq } from "drizzle-orm"
import { chats, newId, sessions, type Channel, type DB } from "@palui/db"

export type ChatRow = typeof chats.$inferSelect
export type SessionRow = typeof sessions.$inferSelect

export const WEB_CHAT_EXTERNAL_ID = "web"

export function getChat(db: DB, id: string): ChatRow | undefined {
  return db.select().from(chats).where(eq(chats.id, id)).get()
}

export function findChat(db: DB, channel: Channel, externalId: string): ChatRow | undefined {
  return db
    .select()
    .from(chats)
    .where(and(eq(chats.channel, channel), eq(chats.externalId, externalId)))
    .get()
}

// Find or create the chat and make sure it has an open session.
export function ensureChat(
  db: DB,
  input: { channel: Channel; externalId: string; title?: string; kind?: "dm" | "group"; allowed?: boolean },
  now = Date.now(),
): ChatRow {
  return db.transaction((tx) => {
    let chat = tx
      .select()
      .from(chats)
      .where(and(eq(chats.channel, input.channel), eq(chats.externalId, input.externalId)))
      .get()
    if (!chat) {
      chat = tx
        .insert(chats)
        .values({
          id: newId("chat"),
          channel: input.channel,
          externalId: input.externalId,
          title: input.title ?? "",
          kind: input.kind ?? "dm",
          allowed: input.allowed ?? true,
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
