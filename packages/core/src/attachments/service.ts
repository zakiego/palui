// Saved attachments: files from a chat the agent decided to keep (KTP, receipts,
// tickets, PDFs). Only files that came with the current turn's own messages can
// be saved, never something from an older message or a URL. The bytes are
// checked against an allowlist of types and a size cap, then stored encrypted
// in SQLite (see savedAttachments in the schema).
import { createHash } from "node:crypto"
import { and, asc, count, desc, eq, or, sql, type Column } from "drizzle-orm"
import { attachmentShares, messages, newId, savedAttachments, type AttachmentKind, type DB } from "@palui/db"
import { decryptBytes, encryptBytes } from "@palui/db/crypto"
import type { StoredContent } from "../chat/context"

export const MAX_ATTACHMENT_BYTES = 10 * 1024 * 1024

// What may be saved. Anything else (audio, SVG, HTML, executables, archives) is refused.
export const ALLOWED_MIME: Record<string, AttachmentKind> = {
  "image/jpeg": "image",
  "image/png": "image",
  "image/webp": "image",
  "image/gif": "image",
  "image/heic": "image",
  "image/heif": "image",
  "application/pdf": "document",
  "text/plain": "document",
  "text/csv": "document",
  "application/msword": "document",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document": "document",
  "application/vnd.ms-excel": "document",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "document",
  "application/vnd.ms-powerpoint": "document",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation": "document",
}

// Magic bytes for the types that have them, so a renamed file can't pass as a PDF or an image.
const OOXML = [0x50, 0x4b, 0x03, 0x04]
const OLE = [0xd0, 0xcf, 0x11, 0xe0]
const MAGIC: Record<string, number[][]> = {
  "image/jpeg": [[0xff, 0xd8, 0xff]],
  "image/png": [[0x89, 0x50, 0x4e, 0x47]],
  "image/gif": [[0x47, 0x49, 0x46, 0x38]],
  "image/webp": [[0x52, 0x49, 0x46, 0x46]],
  "application/pdf": [[0x25, 0x50, 0x44, 0x46]],
  "application/msword": [OLE],
  "application/vnd.ms-excel": [OLE],
  "application/vnd.ms-powerpoint": [OLE],
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document": [OOXML],
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": [OOXML],
  "application/vnd.openxmlformats-officedocument.presentationml.presentation": [OOXML],
}

export class AttachmentError extends Error {}

export type AttachmentRow = typeof savedAttachments.$inferSelect
export type AttachmentMeta = Omit<AttachmentRow, "data">

// A file in the current turn's messages that could be saved.
export type Candidate = {
  // 1-based, in message order: what the model passes as `nomor`.
  index: number
  messageId: string
  kind: AttachmentKind
  mimeType: string
  filename: string | null
  size: number
  data: string
}

export function normalizeMime(mime: string): string {
  return mime.split(";")[0]!.trim().toLowerCase()
}

export function checkFile(mimeType: string, bytes: Buffer): AttachmentKind {
  const mime = normalizeMime(mimeType)
  const kind = ALLOWED_MIME[mime]
  if (!kind) throw new AttachmentError(`File type ${mime} can't be saved. Allowed: images (JPEG, PNG, WebP, GIF, HEIC), PDF, plain text, CSV and Office documents.`)
  if (bytes.length === 0) throw new AttachmentError("The file is empty.")
  if (bytes.length > MAX_ATTACHMENT_BYTES) throw new AttachmentError(`The file is ${formatBytes(bytes.length)}, over the ${formatBytes(MAX_ATTACHMENT_BYTES)} limit.`)
  const magic = MAGIC[mime]
  if (magic && !magic.some((m) => m.every((b, i) => bytes[i] === b))) throw new AttachmentError(`The file says it is ${mime} but its content doesn't match.`)
  return kind
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

// Images and documents (with their bytes still there) from the user messages a run answers.
export function turnCandidates(db: DB, chatId: string, runId: string): Candidate[] {
  const rows = db
    .select({ id: messages.id, content: messages.content })
    .from(messages)
    .where(and(eq(messages.chatId, chatId), eq(messages.runId, runId), eq(messages.role, "user")))
    .orderBy(asc(messages.createdAt), asc(messages.id))
    .all()
  const out: Candidate[] = []
  for (const r of rows) {
    for (const m of ((r.content ?? {}) as StoredContent).media ?? []) {
      if (m.kind === "audio" || !m.data) continue
      out.push({
        index: out.length + 1,
        messageId: r.id,
        kind: m.kind,
        mimeType: normalizeMime(m.mimeType),
        filename: m.kind === "document" ? (m.filename ?? null) : null,
        size: Math.floor((m.data.length * 3) / 4),
        data: m.data,
      })
    }
  }
  return out
}

export type SaveInput = {
  chatId: string
  runId?: string | null
  messageId?: string | null
  title: string
  description?: string
  mimeType: string
  filename?: string | null
  bytes: Buffer
}

// Returns the existing row (deduped: true) when the same bytes were already saved in this chat.
export function saveAttachment(db: DB, input: SaveInput, masterKey: string, now = Date.now()): { row: AttachmentMeta; deduped: boolean } {
  const kind = checkFile(input.mimeType, input.bytes)
  const sha256 = createHash("sha256").update(input.bytes).digest("hex")
  const existing = db
    .select(metaColumns)
    .from(savedAttachments)
    .where(and(eq(savedAttachments.chatId, input.chatId), eq(savedAttachments.sha256, sha256)))
    .get()
  if (existing) return { row: existing, deduped: true }
  const row = db
    .insert(savedAttachments)
    .values({
      id: newId("att"),
      chatId: input.chatId,
      messageId: input.messageId ?? null,
      runId: input.runId ?? null,
      title: input.title.trim().slice(0, 200),
      description: (input.description ?? "").trim().slice(0, 1000),
      kind,
      mimeType: normalizeMime(input.mimeType),
      filename: input.filename ? cleanFilename(input.filename) : null,
      size: input.bytes.length,
      sha256,
      data: encryptBytes(input.bytes, masterKey),
      createdAt: now,
    })
    .returning(metaColumns)
    .get()
  return { row, deduped: false }
}

// Never select `data` for lists.
const metaColumns = {
  id: savedAttachments.id,
  chatId: savedAttachments.chatId,
  messageId: savedAttachments.messageId,
  runId: savedAttachments.runId,
  title: savedAttachments.title,
  description: savedAttachments.description,
  kind: savedAttachments.kind,
  mimeType: savedAttachments.mimeType,
  filename: savedAttachments.filename,
  size: savedAttachments.size,
  sha256: savedAttachments.sha256,
  createdAt: savedAttachments.createdAt,
}

export function listAttachments(db: DB, opts: { chatId?: string; query?: string; limit?: number } = {}): AttachmentMeta[] {
  const q = opts.query?.trim()
  // instr instead of LIKE, so % and _ in the query match literally.
  const has = (col: Column) => sql`instr(lower(coalesce(${col}, '')), lower(${q})) > 0`
  return db
    .select(metaColumns)
    .from(savedAttachments)
    .where(
      and(
        opts.chatId ? eq(savedAttachments.chatId, opts.chatId) : undefined,
        q ? or(has(savedAttachments.title), has(savedAttachments.description), has(savedAttachments.filename)) : undefined,
      ),
    )
    .orderBy(desc(savedAttachments.createdAt), desc(savedAttachments.id))
    .limit(Math.min(opts.limit ?? 50, 500))
    .all()
}

export function countAttachments(db: DB): number {
  return db.select({ n: count() }).from(savedAttachments).get()?.n ?? 0
}

export function getAttachment(db: DB, id: string): AttachmentMeta | undefined {
  return db.select(metaColumns).from(savedAttachments).where(eq(savedAttachments.id, id)).get()
}

export function readAttachment(db: DB, id: string, masterKey: string): { meta: AttachmentMeta; bytes: Buffer } | undefined {
  const row = db.select().from(savedAttachments).where(eq(savedAttachments.id, id)).get()
  if (!row) return undefined
  const { data, ...meta } = row
  return { meta, bytes: decryptBytes(data, masterKey) }
}

// Deleting a file also kills every link to it.
export function deleteAttachment(db: DB, id: string): boolean {
  return db.transaction((tx) => {
    tx.delete(attachmentShares).where(eq(attachmentShares.attachmentId, id)).run()
    return tx.delete(savedAttachments).where(eq(savedAttachments.id, id)).run().changes > 0
  })
}

export type PreviewKind = "image" | "pdf" | "text"

// What a browser can show inline. HEIC, HEIF and the Office formats only download.
export function previewKind(mimeType: string): PreviewKind | null {
  if (mimeType === "application/pdf") return "pdf"
  if (mimeType === "text/plain" || mimeType === "text/csv") return "text"
  if (["image/jpeg", "image/png", "image/webp", "image/gif"].includes(mimeType)) return "image"
  return null
}

// Safe for a Content-Disposition header and a file system: no paths, quotes or control characters.
export function cleanFilename(name: string): string {
  const base = name.split(/[\\/]/).pop() ?? ""
  // biome-ignore lint/suspicious/noControlCharactersInRegex: stripping them is the point.
  const clean = base.replace(/[\u0000-\u001f\u007f"<>:|?*]/g, "").trim().slice(0, 200)
  return clean || "file"
}

const EXT: Record<string, string> = {
  "image/jpeg": "jpg",
  "image/png": "png",
  "image/webp": "webp",
  "image/gif": "gif",
  "image/heic": "heic",
  "image/heif": "heif",
  "application/pdf": "pdf",
  "text/plain": "txt",
  "text/csv": "csv",
}

// The name a download gets: the original file name, or the title plus an extension.
export function downloadName(meta: Pick<AttachmentMeta, "filename" | "title" | "mimeType">): string {
  if (meta.filename) return cleanFilename(meta.filename)
  const ext = EXT[meta.mimeType]
  return cleanFilename(ext ? `${meta.title}.${ext}` : meta.title)
}
