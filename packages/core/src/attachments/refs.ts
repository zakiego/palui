// File references in tool arguments. The model can't write a file's bytes into a
// tool call, so save_attachment hands it a short reference instead. When a tool
// argument is exactly that reference, Palui swaps in the file as a base64 data
// URL just before the call leaves for the server. Logs and approvals keep the
// short reference, never the bytes.
import type { DB } from "@palui/db"
import { readAttachment } from "./service"

const PREFIX = "palui:attachment/"
const REF = /^palui:attachment\/(att_[A-Za-z0-9]+)$/

export function attachmentRef(id: string): string {
  return `${PREFIX}${id}`
}

export class AttachmentRefError extends Error {}

// Every reference anywhere in the value (nested objects and arrays included).
export function findAttachmentRefs(value: unknown): string[] {
  const ids = new Set<string>()
  const walk = (v: unknown) => {
    if (typeof v === "string") {
      const m = REF.exec(v)
      if (m?.[1]) ids.add(m[1])
    } else if (Array.isArray(v)) v.forEach(walk)
    else if (v && typeof v === "object") Object.values(v).forEach(walk)
  }
  walk(value)
  return [...ids]
}

// A copy of `value` with every reference replaced by its file's data URL. Only
// files saved in `chatId` resolve; anything else throws, so a turn can't hand a
// file from another chat to a server.
export function resolveAttachmentRefs<T>(db: DB, value: T, opts: { chatId: string; masterKey: string }): T {
  const ids = findAttachmentRefs(value)
  if (!ids.length) return value
  const urls = new Map<string, string>()
  for (const id of ids) {
    const file = readAttachment(db, id, opts.masterKey)
    if (!file || file.meta.chatId !== opts.chatId)
      throw new AttachmentRefError(`No saved attachment ${id} in this chat. Call list_attachments to get the ids.`)
    urls.set(id, `data:${file.meta.mimeType};base64,${file.bytes.toString("base64")}`)
  }
  const swap = (v: unknown): unknown => {
    if (typeof v === "string") {
      const m = REF.exec(v)
      return m?.[1] ? urls.get(m[1]) : v
    }
    if (Array.isArray(v)) return v.map(swap)
    if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, swap(x)]))
    return v
  }
  return swap(value) as T
}
