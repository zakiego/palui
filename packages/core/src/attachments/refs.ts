// File references in tool arguments. The model can't write a file's bytes into a
// tool call, so the attachment tools hand it a short reference instead. When a
// tool argument is exactly that reference, Palui swaps in the file as a base64
// data URL just before the call leaves for the server. Logs and approvals keep
// the short reference, never the bytes.
import type { DB } from "@palui/db"
import { formatBytes, getAttachment, readAttachment } from "./service"

const PREFIX = "palui:attachment/"
const REF = /^palui:attachment\/(att_[A-Za-z0-9]+)$/

// The most file bytes one tool call may carry, all refs together.
export const MAX_REF_BYTES = 15 * 1024 * 1024

export function attachmentRef(id: string): string {
  return `${PREFIX}${id}`
}

export class AttachmentRefError extends Error {}

function mapStrings(value: unknown, fn: (s: string) => string): unknown {
  if (typeof value === "string") return fn(value)
  if (Array.isArray(value)) return value.map((v) => mapStrings(v, fn))
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, mapStrings(v, fn)]))
  return value
}

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

export type ResolvedRefs<T> = {
  value: T
  // ref -> data URL, to put the ref back if a server echoes the file.
  files: { ref: string; url: string }[]
}

// A copy of `value` with every reference replaced by its file's data URL. Only
// files saved in `chatId` resolve, and all of them together must fit in
// MAX_REF_BYTES; anything else throws, so a turn can't hand a file from another
// chat to a server.
export function resolveFileRefs<T>(db: DB, value: T, opts: { chatId: string; masterKey: string; maxBytes?: number }): ResolvedRefs<T> {
  const ids = findAttachmentRefs(value)
  if (!ids.length) return { value, files: [] }
  const max = opts.maxBytes ?? MAX_REF_BYTES
  let total = 0
  for (const id of ids) {
    const meta = getAttachment(db, id)
    if (!meta || meta.chatId !== opts.chatId) throw new AttachmentRefError(`No saved attachment ${id} in this chat. Call list_attachments to get the ids.`)
    total += meta.size
  }
  if (total > max) throw new AttachmentRefError(`These files add up to ${formatBytes(total)}, over the ${formatBytes(max)} one call can carry. Send fewer at a time.`)
  const urls = new Map<string, string>()
  for (const id of ids) {
    const file = readAttachment(db, id, opts.masterKey)
    if (!file) throw new AttachmentRefError(`No saved attachment ${id} in this chat. Call list_attachments to get the ids.`)
    urls.set(id, `data:${file.meta.mimeType};base64,${file.bytes.toString("base64")}`)
  }
  const out = mapStrings(value, (s) => {
    const m = REF.exec(s)
    return m?.[1] ? (urls.get(m[1]) ?? s) : s
  }) as T
  return { value: out, files: [...urls].map(([id, url]) => ({ ref: attachmentRef(id), url })) }
}

export function resolveAttachmentRefs<T>(db: DB, value: T, opts: { chatId: string; masterKey: string; maxBytes?: number }): T {
  return resolveFileRefs(db, value, opts).value
}

// Puts refs back where a result repeats a file's data URL, so a server that
// echoes its input doesn't pour megabytes of base64 into the model's context.
export function scrubFileUrls<T>(value: T, files: { ref: string; url: string }[]): T {
  if (!files.length) return value
  return mapStrings(value, (s) => {
    let out = s
    for (const f of files) if (out.includes(f.url)) out = out.split(f.url).join(f.ref)
    return out
  }) as T
}

// For approval prompts: each ref as the file's title and size, so the owner
// sees which file a call would send.
export function labelFileRefs<T>(db: DB, value: T): T {
  if (!findAttachmentRefs(value).length) return value
  return mapStrings(value, (s) => {
    const m = REF.exec(s)
    if (!m?.[1]) return s
    const a = getAttachment(db, m[1])
    return a ? `[file: ${a.title}, ${formatBytes(a.size)}]` : s
  }) as T
}
