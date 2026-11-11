// The inbox (PLAN 8.1): every inbound message is written to `messages` with
// status `pending` before the channel handler returns. Media is turned into
// something the model can read later, at dispatch time, so a slow transcription
// never holds up the channel.
import { eq } from "drizzle-orm"
import { chats, messages, newId, type DB } from "@palui/db"
import { ensureChat, findChat, type ChatRow } from "../chats"
import { logEvent } from "../events"
import type { Logger } from "../log"
import { getConnector } from "../saklar/store"
import { checkUtusan, UTUSAN_CONFIG_PATH } from "../utusan"
import type { InboundMedia, InboundMessage } from "../saklar/define"
import type { RunningEntry } from "../saklar/supervisor"
import type { StoredContent, StoredMedia } from "./context"

export type MessageRow = typeof messages.$inferSelect

export const MAX_INLINE_DOC_BYTES = 64 * 1024
const TEXT_MIME = /^(text\/|application\/(json|xml|x-yaml|yaml|csv|x-ndjson|javascript|typescript|x-sh|sql))/i
const TEXT_EXT = /\.(txt|md|markdown|csv|tsv|json|ya?ml|xml|log|ini|toml|ts|js|py|sql|sh|html?)$/i

export type InboxOptions = {
  // utusan's config, read to keep WhatsApp chats split (default: the compose mount).
  utusanFile?: string
}

// Whether Palui may answer a WhatsApp DM (PLAN 5.1: chats are split, never
// shared). Checked on every message, since the file read is cheap: removing the
// utusan binding takes effect on the next message, without a restart.
//  bound:   utusan still answers it, so Palui doesn't (both would reply).
//  free:    allowed.
//  unknown: utusan.json isn't readable; allowed only when the owner ticked
//           utusanCleared, otherwise a chat keeps what it had (new ones start out).
function whatsAppDecision(db: DB, msg: InboundMessage, existing: ChatRow | undefined, file: string): { allowed: boolean; state: "bound" | "free" | "unknown"; bindingId?: string } {
  const verdict = checkUtusan([msg.chatExternalId, msg.senderAltId], file)
  if (verdict.state === "bound") return { allowed: false, state: "bound", bindingId: verdict.bindingId }
  if (verdict.state === "free") return { allowed: true, state: "free" }
  const cleared = (getConnector(db, msg.connectorId)?.config as { utusanCleared?: unknown } | undefined)?.utusanCleared === true
  return { allowed: cleared || (existing?.allowed ?? false), state: "unknown" }
}

export function writeInbound(db: DB, msg: InboundMessage, now = Date.now(), opts: InboxOptions = {}): { chat: ChatRow; row: MessageRow } {
  const waDm = msg.channel === "whatsapp" && msg.chatKind === "dm"
  const existing = waDm ? findChat(db, msg.channel, msg.chatExternalId) : undefined
  const decision = waDm ? whatsAppDecision(db, msg, existing, opts.utusanFile ?? UTUSAN_CONFIG_PATH) : undefined
  let chat = ensureChat(
    db,
    {
      channel: msg.channel,
      externalId: msg.chatExternalId,
      title: msg.chatTitle,
      kind: msg.chatKind,
      // WhatsApp is DM only (PLAN 5.1): a new group chat starts outside the allowlist.
      allowed: msg.channel === "whatsapp" ? (decision?.allowed ?? false) : true,
    },
    now,
  )
  if (decision) {
    const changed = existing && existing.allowed !== decision.allowed
    if (changed) chat = db.update(chats).set({ allowed: decision.allowed }).where(eq(chats.id, chat.id)).returning().get()
    if (changed || (!existing && !decision.allowed)) logAllowance(db, msg, decision, now)
  }
  const media: StoredMedia[] | undefined = msg.media?.length
    ? msg.media.map((m) => (m.kind === "image" ? { kind: "image", mimeType: m.mimeType, data: m.data } : { kind: m.kind, mimeType: m.mimeType, data: m.data, filename: m.filename }))
    : undefined
  const content: StoredContent = { text: msg.text ?? "", ...(media ? { media } : {}), ...(msg.callback ? { callback: msg.callback } : {}) }
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

function logAllowance(db: DB, msg: InboundMessage, d: { allowed: boolean; state: string; bindingId?: string }, now: number) {
  const jid = msg.chatExternalId
  const message = d.allowed
    ? `WhatsApp chat ${jid} is no longer bound in utusan; Palui answers it now.`
    : d.state === "bound"
      ? `WhatsApp chat ${jid} is still bound in utusan${d.bindingId ? ` (binding "${d.bindingId}")` : ""}, so Palui won't answer it: both would reply. Remove that binding from ~/.utusan/utusan.json (PLAN 18); Palui picks it up on the next message.`
      : `Palui can't read utusan's config to check WhatsApp chat ${jid}, so it won't answer it yet. Mount ~/.utusan/utusan.json read-only at /utusan/utusan.json, or remove the chat's utusan binding and tick "This chat isn't bound in utusan" on the WhatsApp connector.`
  logEvent(db, { connectorId: msg.connectorId, kind: "whatsapp.allowlist", level: d.allowed ? "info" : "warn", message, data: { chat: jid, state: d.state } }, now)
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
    // Documents: small text files are inlined, the rest is named only.
    const name = m.filename ?? "dokumen"
    if (m.data && isTextDoc(m) && decodedSize(m.data) <= MAX_INLINE_DOC_BYTES) {
      const body = Buffer.from(m.data, "base64").toString("utf8")
      texts.push(`[file: ${name}]\n${body}`)
      kept.push({ kind: "document", mimeType: m.mimeType, filename: name, note: "inlined" })
    } else {
      texts.push(`[file: ${name} (${m.mimeType}), gak bisa dibaca langsung]`)
      kept.push({ kind: "document", mimeType: m.mimeType, filename: name, note: "not readable" })
    }
  }
  const text = texts.join("\n\n")
  const content: StoredContent = { ...c, text, media: kept, prepared: true }
  return db.update(messages).set({ content, text }).where(eq(messages.id, row.id)).returning().get()
}
