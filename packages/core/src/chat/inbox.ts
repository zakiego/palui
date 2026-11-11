// The inbox (PLAN 8.1): every inbound message is written to `messages` with
// status `pending` before the channel handler returns. Media is turned into
// something the model can read later, at dispatch time, so a slow transcription
// never holds up the channel.
import { and, desc, eq } from "drizzle-orm"
import { chats, messages, newId, outbox, type DB } from "@palui/db"
import { ensureChat, type ChatRow } from "../chats"
import type { Logger } from "../log"
import type { InboundMedia, InboundMessage } from "../connectors/define"
import type { RunningEntry } from "../connectors/supervisor"
import { QUOTE_MAX_CHARS, type StoredContent, type StoredMedia, type StoredQuote } from "./context"

export type MessageRow = typeof messages.$inferSelect

export const MAX_INLINE_DOC_BYTES = 64 * 1024
const TEXT_MIME = /^(text\/|application\/(json|xml|x-yaml|yaml|csv|x-ndjson|javascript|typescript|x-sh|sql))/i
const TEXT_EXT = /\.(txt|md|markdown|csv|tsv|json|ya?ml|xml|log|ini|toml|ts|js|py|sql|sh|html?)$/i

export function writeInbound(db: DB, msg: InboundMessage, now = Date.now()): { chat: ChatRow; row: MessageRow } {
  // WhatsApp is DM only (PLAN 5.1): a new group chat starts outside the allowlist.
  // DMs are always in it, since the router already drops anyone but the owner.
  const waDm = msg.channel === "whatsapp" && msg.chatKind === "dm"
  let chat = ensureChat(
    db,
    { channel: msg.channel, externalId: msg.chatExternalId, title: msg.chatTitle, kind: msg.chatKind, allowed: msg.channel === "whatsapp" ? waDm : true },
    now,
  )
  // A DM left out by the old utusan chat split opens again on its next message.
  if (waDm && !chat.allowed) chat = db.update(chats).set({ allowed: true }).where(eq(chats.id, chat.id)).returning().get()
  const media: StoredMedia[] | undefined = msg.media?.length
    ? msg.media.map((m) => (m.kind === "image" ? { kind: "image", mimeType: m.mimeType, data: m.data } : { kind: m.kind, mimeType: m.mimeType, data: m.data, filename: m.filename }))
    : undefined
  const quoted = resolveQuote(db, chat.id, msg)
  const content: StoredContent = { text: msg.text ?? "", ...(quoted ? { quoted } : {}), ...(media ? { media } : {}), ...(msg.callback ? { callback: msg.callback } : {}) }
  const row = db
    .insert(messages)
    .values({
      id: newId("msg"),
      chatId: chat.id,
      sessionId: chat.sessionId,
      role: "user",
      content,
      text: msg.text ?? "",
      channelMsgId: msg.channelMsgId ?? null,
      replyToChannelMsgId: msg.replyToChannelMsgId ?? null,
      senderId: msg.senderExternalId,
      status: "pending",
      createdAt: msg.at || now,
    })
    .returning()
    .get()
  db.update(chats).set({ lastMessageAt: now }).where(eq(chats.id, chat.id)).run()
  return { chat, row }
}

const QUOTE_FROM: Record<NonNullable<InboundMessage["quoted"]>["from"], StoredQuote["from"]> = { self: "assistant", sender: "user", other: "other", unknown: "unknown" }

function capQuote(text: string): string {
  const t = text.trim()
  return t.length > QUOTE_MAX_CHARS ? `${t.slice(0, QUOTE_MAX_CHARS).trimEnd()}... (truncated)` : t
}

// What the new message quotes. Our own copy wins: a reply Palui sent (messages
// or, for reminders and job results, the outbox) or an earlier owner message,
// whose stored text already has voice notes and files turned into text. When we
// don't have it, use what the channel sent.
export function resolveQuote(db: DB, chatId: string, msg: InboundMessage): StoredQuote | undefined {
  const id = msg.replyToChannelMsgId
  if (id) {
    const row = db
      .select({ role: messages.role, text: messages.text, content: messages.content })
      .from(messages)
      .where(and(eq(messages.chatId, chatId), eq(messages.channelMsgId, id)))
      .orderBy(desc(messages.createdAt))
      .limit(1)
      .get()
    if (row && (row.role === "assistant" || row.role === "user")) {
      const c = (row.content ?? {}) as StoredContent
      const text = capQuote(row.text || c.text || "")
      const media = c.media?.some((m) => m.kind === "image") ? "[image]" : undefined
      if (text || media) return { from: row.role, text, ...(media ? { media } : {}) }
    }
    const sent = db
      .select({ payload: outbox.payload })
      .from(outbox)
      .where(and(eq(outbox.chatId, chatId), eq(outbox.channelMsgId, id)))
      .orderBy(desc(outbox.createdAt))
      .limit(1)
      .get()
    if (sent?.payload.text) return { from: "assistant", text: capQuote(sent.payload.text) }
  }
  const q = msg.quoted
  if (!q) return undefined
  return { from: QUOTE_FROM[q.from] ?? "unknown", text: capQuote(q.text), ...(q.media ? { media: q.media } : {}) }
}

function decodedSize(b64: string): number {
  return Math.floor((b64.length * 3) / 4)
}

function isTextDoc(m: { mimeType: string; filename?: string }): boolean {
  return TEXT_MIME.test(m.mimeType) || (!!m.filename && TEXT_EXT.test(m.filename))
}

// Turn audio into "[voice] ..." text through the first running connector with
// transcribe(), and inline small text documents. Images stay as base64 parts
// for the vision role. Updates the row and returns it.
export async function prepareMedia(db: DB, row: MessageRow, running: RunningEntry[], log: Logger): Promise<MessageRow> {
  const c = (row.content ?? {}) as StoredContent
  if (c.prepared || !c.media?.some((m) => m.kind !== "image")) return row
  const texts: string[] = row.text ? [row.text] : []
  const kept: StoredMedia[] = []
  for (const m of c.media) {
    if (m.kind === "image") {
      kept.push(m)
      continue
    }
    if (m.kind === "audio") {
      const t = running.find((e) => e.handle.transcribe)
      if (!t || !m.data) {
        texts.push("[voice] (pesan suara, tapi gak ada skill transkripsi yang nyala)")
        kept.push({ kind: "audio", mimeType: m.mimeType, note: "not transcribed" })
        continue
      }
      try {
        const said = await t.handle.transcribe!({ kind: "audio", mimeType: m.mimeType, data: m.data } satisfies InboundMedia)
        texts.push(`[voice] ${said.trim()}`)
        kept.push({ kind: "audio", mimeType: m.mimeType, note: `transcribed by ${t.id}` })
      } catch (e) {
        log.warn("transcription failed", { connector_id: t.id, error: e })
        texts.push("[voice] (pesan suara, transkripsinya gagal)")
        kept.push({ kind: "audio", mimeType: m.mimeType, note: "transcription failed" })
      }
      continue
    }
    // Documents: small text files are inlined, the rest is named only. The bytes
    // stay on the row so the attachments tools can save the file in this turn.
    const name = m.filename ?? "dokumen"
    const data = m.data ? { data: m.data } : {}
    if (m.data && isTextDoc(m) && decodedSize(m.data) <= MAX_INLINE_DOC_BYTES) {
      const body = Buffer.from(m.data, "base64").toString("utf8")
      texts.push(`[file: ${name}]\n${body}`)
      kept.push({ kind: "document", mimeType: m.mimeType, filename: name, note: "inlined", ...data })
    } else {
      texts.push(`[file: ${name} (${m.mimeType}), gak bisa dibaca langsung]`)
      kept.push({ kind: "document", mimeType: m.mimeType, filename: name, note: "not readable", ...data })
    }
  }
  const text = texts.join("\n\n")
  const content: StoredContent = { ...c, text, media: kept, prepared: true }
  return db.update(messages).set({ content, text }).where(eq(messages.id, row.id)).returning().get()
}
