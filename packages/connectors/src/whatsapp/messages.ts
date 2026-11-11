// Pure helpers for reading Baileys messages and JIDs. No socket, no IO.
import type { WAMessage } from "baileys"

type Content = NonNullable<WAMessage["message"]>

// Strip a device suffix: "628123:12@s.whatsapp.net" -> "628123@s.whatsapp.net".
export function bareJid(jid: string): string {
  const at = jid.indexOf("@")
  if (at === -1) return jid
  const local = jid.slice(0, at)
  const colon = local.indexOf(":")
  return `${colon === -1 ? local : local.slice(0, colon)}${jid.slice(at)}`
}

export function jidDigits(jid: string): string {
  return bareJid(jid).split("@")[0]?.replace(/\D/g, "") ?? ""
}

export const isLid = (jid: string) => jid.endsWith("@lid")
export const isPhoneJid = (jid: string) => jid.endsWith("@s.whatsapp.net")

// v1 is DM only: no groups, status updates, broadcast lists or newsletters.
export function isDirectChat(jid: string): boolean {
  if (jid === "status@broadcast") return false
  if (jid.endsWith("@g.us") || jid.endsWith("@broadcast") || jid.endsWith("@newsletter")) return false
  return isPhoneJid(jid) || isLid(jid)
}

// Unwrap the envelopes WhatsApp puts around real content (disappearing messages,
// view once, documents with a caption).
export function unwrap(content: WAMessage["message"]): Content | undefined {
  let m = content ?? undefined
  for (let i = 0; i < 5 && m; i++) {
    const inner =
      m.ephemeralMessage?.message ??
      m.viewOnceMessage?.message ??
      m.viewOnceMessageV2?.message ??
      m.viewOnceMessageV2Extension?.message ??
      m.documentWithCaptionMessage?.message ??
      m.editedMessage?.message
    if (!inner) break
    m = inner
  }
  return m ?? undefined
}

export function textOf(content: Content | undefined): string {
  if (!content) return ""
  return (
    content.conversation ??
    content.extendedTextMessage?.text ??
    content.imageMessage?.caption ??
    content.videoMessage?.caption ??
    content.documentMessage?.caption ??
    ""
  )
}

// The id of the message this one quotes, from whichever part carries contextInfo.
export function quotedIdOf(content: Content | undefined): string | undefined {
  if (!content) return undefined
  for (const value of Object.values(content)) {
    const id = (value as { contextInfo?: { stanzaId?: string | null } } | null)?.contextInfo?.stanzaId
    if (id) return id
  }
  return undefined
}

export type MediaInfo = {
  kind: "image" | "audio" | "document"
  mimeType: string
  bytes?: number
  filename?: string
  durationSec?: number
}

export function mediaOf(content: Content | undefined): MediaInfo | undefined {
  if (!content) return undefined
  const num = (v: unknown) => (v == null ? undefined : Number(v))
  if (content.imageMessage) return { kind: "image", mimeType: content.imageMessage.mimetype ?? "image/jpeg", bytes: num(content.imageMessage.fileLength) }
  if (content.audioMessage) {
    const info: MediaInfo = { kind: "audio", mimeType: content.audioMessage.mimetype ?? "audio/ogg", bytes: num(content.audioMessage.fileLength) }
    if (content.audioMessage.seconds != null) info.durationSec = content.audioMessage.seconds
    return info
  }
  if (content.documentMessage) {
    const info: MediaInfo = { kind: "document", mimeType: content.documentMessage.mimetype ?? "application/octet-stream", bytes: num(content.documentMessage.fileLength) }
    if (content.documentMessage.fileName) info.filename = content.documentMessage.fileName
    return info
  }
  return undefined
}

// "ABCDEFGH" -> "ABCD-EFGH".
export function formatPairingCode(code: string): string {
  const c = code.replace(/[^A-Za-z0-9]/g, "").toUpperCase()
  return c.length === 8 ? `${c.slice(0, 4)}-${c.slice(4)}` : c
}

// Digits for requestPairingCode: country code included, leading 0 -> 62.
export function pairingDigits(phone: string): string {
  let digits = phone.replace(/\D/g, "")
  if (digits.startsWith("0")) digits = `62${digits.slice(1)}`
  return digits
}

export function timestampMs(ts: WAMessage["messageTimestamp"], fallback = Date.now()): number {
  const seconds = typeof ts === "number" ? ts : Number(ts ?? 0)
  return seconds > 0 ? seconds * 1000 : fallback
}
