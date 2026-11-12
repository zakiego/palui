// Attachment tools for the model: save an important file from the current
// message, list and look up saved ones, delete one. Every tool works on the
// turn's chat only. The descriptions carry the "only what matters" rule, so the
// model doesn't file every photo the owner sends.
import { tool } from "ai"
import { z } from "zod"
import type { DB } from "@palui/db"
import { config, masterKey } from "@palui/core/config"
import { defineRuntime, type PaluiTool, type TurnInfo } from "@palui/core/connectors/define"
import { attachmentRef } from "@palui/core/attachments/refs"
import {
  AttachmentError,
  countAttachments,
  deleteAttachment,
  formatBytes,
  getAttachment,
  listAttachments,
  saveAttachment,
  turnCandidates,
  type AttachmentMeta,
  type Candidate,
} from "@palui/core/attachments/service"
import manifest from "./manifest"

type Fail = { ok: false; error: string }

const FILE_REF_HINT =
  "To hand the file to another app's tool (for example to upload a receipt and attach it to a transaction there), pass `fileRef` as that tool's file argument where it asks for the file's contents or a base64 data URL; Palui fills in the file. Another app can't read this file's id, so never pass `id` as that app's own attachment id: upload the file there first and use the id that app returns."

function fail(e: unknown): Fail {
  if (e instanceof AttachmentError) return { ok: false, error: e.message }
  throw e
}

export type AttachmentToolOptions = {
  clock?: () => number
  key?: () => string
  appUrl?: () => string
}

function view(a: AttachmentMeta, appUrl: string) {
  return {
    id: a.id,
    title: a.title,
    description: a.description,
    kind: a.kind,
    mimeType: a.mimeType,
    filename: a.filename,
    size: formatBytes(a.size),
    savedAt: new Date(a.createdAt).toISOString(),
    // Needs a dashboard sign-in; there's no public link.
    url: `${appUrl}/app/attachments?id=${a.id}`,
    // Pass as another tool's file argument; Palui swaps in the file's contents.
    fileRef: attachmentRef(a.id),
  }
}

function describe(c: Candidate) {
  return { nomor: c.index, kind: c.kind, mimeType: c.mimeType, filename: c.filename, size: formatBytes(c.size) }
}

export function attachmentTools(db: DB, turn: TurnInfo, opts: AttachmentToolOptions = {}): Record<string, PaluiTool> {
  const chatId = turn.chatId
  const clock = opts.clock ?? Date.now
  const key = opts.key ?? (() => masterKey())
  const appUrl = opts.appUrl ?? (() => config().appUrl)
  const own = (id: string): AttachmentMeta | Fail => {
    const a = getAttachment(db, id)
    if (!a || a.chatId !== chatId) return { ok: false, error: `No saved attachment ${id} in this chat. Call list_attachments to get the ids.` }
    return a
  }

  return {
    save_attachment: {
      class: "write",
      title: "Save an attachment",
      tool: tool({
        description:
          "Save a file the owner sent in the current message (photo, PDF, document) so it can be found later. Only save files that are worth keeping: identity documents (KTP, SIM, passport, KK), receipts and invoices, tickets and bookings, contracts, certificates, medical or school records, or anything the owner asks you to keep. Do NOT save casual photos, memes, screenshots of chats or files that are only needed for this one answer, unless the owner asks. When unsure, ask the owner first. Give it a clear title and a one-sentence description of what it is (dates, numbers, names that help find it later). Tell the owner it was saved. " + FILE_REF_HINT,
        inputSchema: z.object({
          nomor: z
            .number()
            .int()
            .min(1)
            .optional()
            .describe("Which file in the current message, 1-based in the order they were sent. Omit when there is only one."),
          title: z.string().min(1).max(200).describe('Short title in the owner\'s language, e.g. "KTP Zaki" or "Tiket pesawat BDJ-CGK 12 Des".'),
          description: z.string().max(1000).optional().describe("One sentence on what the file is and what's in it."),
        }),
        execute: async (input) => {
          const candidates = turnCandidates(db, chatId, turn.batchRunId ?? turn.runId)
          if (!candidates.length) return { ok: false as const, error: "The current message has no image or document to save. Only files sent with the message being answered can be saved." }
          if (input.nomor === undefined && candidates.length > 1)
            return { ok: false as const, error: "The current message has more than one file. Call again with `nomor`.", files: candidates.map(describe) }
          const c = candidates[(input.nomor ?? 1) - 1]
          if (!c) return { ok: false as const, error: `There is no file number ${input.nomor}.`, files: candidates.map(describe) }
          try {
            const { row, deduped } = saveAttachment(
              db,
              {
                chatId,
                runId: turn.runId,
                messageId: c.messageId,
                title: input.title,
                description: input.description,
                mimeType: c.mimeType,
                filename: c.filename,
                bytes: Buffer.from(c.data, "base64"),
              },
              key(),
              clock(),
            )
            const message = deduped ? `File ini udah pernah disimpan sebagai "${row.title}".` : `Oke, "${row.title}" udah aku simpan.`
            return { ok: true as const, deduped, message, attachment: view(row, appUrl()) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },
    list_attachments: {
      class: "read",
      title: "List saved attachments",
      tool: tool({
        description:
          "List files saved in this chat with save_attachment, newest first. Use `query` to search titles, descriptions and file names, e.g. when the owner asks where their KTP or a ticket is.",
        inputSchema: z.object({
          query: z.string().max(100).optional().describe("Words to look for, e.g. \"ktp\" or \"tiket\"."),
          limit: z.number().int().min(1).max(50).optional(),
        }),
        execute: async (input) => {
          const rows = listAttachments(db, { chatId, query: input.query, limit: input.limit ?? 20 })
          return { ok: true as const, count: rows.length, attachments: rows.map((a) => view(a, appUrl())) }
        },
      }),
    },
    get_attachment: {
      class: "read",
      title: "Get a saved attachment",
      tool: tool({
        description:
          "Get one saved file's details by id: title, description, type, size, when it was saved, and the dashboard link where the owner can open or download it after signing in. Files can't be sent back into the chat yet, so share the link. " + FILE_REF_HINT,
        inputSchema: z.object({ id: z.string().min(1).max(64) }),
        execute: async (input) => {
          const a = own(input.id)
          if ("ok" in a) return a
          return { ok: true as const, attachment: view(a, appUrl()) }
        },
      }),
    },
    delete_attachment: {
      class: "destructive",
      title: "Delete a saved attachment",
      tool: tool({
        description: "Delete a saved file for good, only when the owner asks. Always needs the owner's approval.",
        inputSchema: z.object({ id: z.string().min(1).max(64) }),
        execute: async (input) => {
          const a = own(input.id)
          if ("ok" in a) return a
          deleteAttachment(db, a.id)
          return { ok: true as const, message: `"${a.title}" udah dihapus.` }
        },
      }),
    },
  }
}

export default defineRuntime(manifest, {
  test: async (ctx) => {
    const n = countAttachments(ctx.db)
    return { ok: true, detail: n ? `${n} saved file${n === 1 ? "" : "s"}` : "Ready, nothing saved yet" }
  },
  start: async (ctx) => ({
    stop: async () => {},
    tools: async (turn) => attachmentTools(ctx.db, turn),
  }),
})
