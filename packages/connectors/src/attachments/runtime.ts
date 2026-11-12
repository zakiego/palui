// Attachment tools for the model: save an important file from the current
// message (or a recent earlier one in the chat), list and look up saved ones, share one as a temporary link, delete one. Every tool works on the
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
  RECENT_FILES_MAX,
  recentCandidates,
  saveAttachment,
  turnCandidates,
  type AttachmentMeta,
  type Candidate,
  type RecentCandidate,
} from "@palui/core/attachments/service"
import { createShare, revokeShares, SHARE_DURATION_KEYS, shareUrl } from "@palui/core/attachments/shares"
import manifest from "./manifest"

type Fail = { ok: false; error: string }

const FILE_REF_HINT =
  "To hand a saved file to another app's tool (for example to upload a receipt and attach it to a transaction there), pass its `fileRef` as that tool's file argument where it asks for the file's contents or a base64 data URL; Palui fills in the file. Another app can't read Palui's ids, so never pass `id` as that app's own attachment id: upload the file there first and use the id that app returns. Check that app's size and type limits against `size` and `mimeType` first."

// Next to the files in every result, so the rule is in front of the model when it
// picks the next call, not only in a description it read earlier.
const SEND_NOTE =
  "To send a file to another app, pass its fileRef as that app's file or data URL argument (upload it there first), then use the id that app returns. Never pass `id` to another app."

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
    // The dashboard page, which needs a sign-in. share_attachment makes a link that opens the file itself.
    url: `${appUrl}/app/attachments?id=${a.id}`,
    // Pass as another tool's file argument; Palui swaps in the file's contents.
    fileRef: attachmentRef(a.id),
  }
}

function describe(c: Candidate) {
  return { nomor: c.index, kind: c.kind, mimeType: c.mimeType, filename: c.filename, size: formatBytes(c.size) }
}

function describeRecent(c: RecentCandidate) {
  return { ...describe(c), sentAt: new Date(c.sentAt).toISOString(), caption: c.caption }
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
          "Save a file the owner sent in this chat (photo, PDF, document) so it can be found later. Only save files that are worth keeping: identity documents (KTP, SIM, passport, KK), receipts and invoices, tickets and bookings, contracts, certificates, medical or school records, or anything the owner asks you to keep. Do NOT save casual photos, memes, screenshots of chats or files that are only needed for this one answer, unless the owner asks. When unsure, ask the owner first. By default it saves from the current message. A file from an earlier message (shown as [gambar dari sebelumnya]) can still be saved with `earlier: true`, for example a receipt sent before the owner confirmed: call it without `nomor` first to see the recent files with when they were sent and their caption, then again with the right `nomor`. Never tell the owner a file is saved unless this tool returned ok. Give it a clear title and a one-sentence description of what it is (dates, numbers, names that help find it later). Tell the owner it was saved. " + FILE_REF_HINT,
        inputSchema: z.object({
          nomor: z
            .number()
            .int()
            .min(1)
            .optional()
            .describe("Which file, 1-based. In the current message: in the order they were sent. With `earlier`: the number from the list, 1 being the most recent. Omit when there is only one."),
          earlier: z
            .boolean()
            .optional()
            .describe(`Save from an earlier message in this chat instead of the current one: the last 24 hours, at most ${RECENT_FILES_MAX} files, newest first.`),
          title: z.string().min(1).max(200).describe('Short title in the owner\'s language, e.g. "KTP Zaki" or "Tiket pesawat BDJ-CGK 12 Des".'),
          description: z.string().max(1000).optional().describe("One sentence on what the file is and what's in it."),
        }),
        execute: async (input) => {
          let c: Candidate | undefined
          if (input.earlier) {
            const recent = recentCandidates(db, chatId, clock())
            if (!recent.length) return { ok: false as const, error: "No image or document was sent in this chat in the last 24 hours. Ask the owner to send it again." }
            if (input.nomor === undefined && recent.length > 1)
              return { ok: false as const, error: "Pick one: call again with `earlier: true` and its `nomor`. Match it by caption and time; ask the owner if it's still unclear.", files: recent.map(describeRecent) }
            c = recent[(input.nomor ?? 1) - 1]
            if (!c) return { ok: false as const, error: `There is no earlier file number ${input.nomor}.`, files: recent.map(describeRecent) }
          } else {
            const candidates = turnCandidates(db, chatId, turn.batchRunId ?? turn.runId)
            if (!candidates.length) {
              const recent = recentCandidates(db, chatId, clock())
              return recent.length
                ? { ok: false as const, error: "The current message has no file. To save one from an earlier message, call again with `earlier: true` and the `nomor` of the right file below.", files: recent.map(describeRecent) }
                : { ok: false as const, error: "The current message has no image or document to save, and none was sent in this chat in the last 24 hours." }
            }
            if (input.nomor === undefined && candidates.length > 1)
              return { ok: false as const, error: "The current message has more than one file. Call again with `nomor`.", files: candidates.map(describe) }
            c = candidates[(input.nomor ?? 1) - 1]
            if (!c) return { ok: false as const, error: `There is no file number ${input.nomor}.`, files: candidates.map(describe) }
          }
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
            return { ok: true as const, deduped, message, attachment: view(row, appUrl()), sendToOtherApp: SEND_NOTE }
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
          "List files saved in this chat with save_attachment, newest first. Use `query` to search titles, descriptions and file names, e.g. when the owner asks where their KTP or a ticket is. " + FILE_REF_HINT,
        inputSchema: z.object({
          query: z.string().max(100).optional().describe("Words to look for, e.g. \"ktp\" or \"tiket\"."),
          limit: z.number().int().min(1).max(50).optional(),
        }),
        execute: async (input) => {
          const rows = listAttachments(db, { chatId, query: input.query, limit: input.limit ?? 20 })
          return { ok: true as const, count: rows.length, attachments: rows.map((a) => view(a, appUrl())), ...(rows.length ? { sendToOtherApp: SEND_NOTE } : {}) }
        },
      }),
    },
    get_attachment: {
      class: "read",
      title: "Get a saved attachment",
      tool: tool({
        description:
          "Get one saved file's details by id: title, description, type, size, when it was saved, and the dashboard link where the owner can open or download it after signing in. Files can't be sent back into the chat yet; to give someone the file itself, call share_attachment. " + FILE_REF_HINT,
        inputSchema: z.object({ id: z.string().min(1).max(64) }),
        execute: async (input) => {
          const a = own(input.id)
          if ("ok" in a) return a
          return { ok: true as const, attachment: view(a, appUrl()), sendToOtherApp: SEND_NOTE }
        },
      }),
    },
    share_attachment: {
      class: "write",
      title: "Share a saved attachment",
      tool: tool({
        description:
          'Make a temporary link that opens a saved file directly (images and PDFs show in the browser, other files download). visibility "private" (the default) only opens for the owner signed in to the dashboard. visibility "public" opens for anyone who has the link, so only use it when the owner asks to share the file with someone else, and never for identity documents unless the owner says so explicitly. Links expire after `expires` (default 24h, at most 7d) and the owner can turn them off on the Attachments page. Each call makes a new link; send the url as it is.',
        inputSchema: z.object({
          id: z.string().min(1).max(64),
          visibility: z.enum(["private", "public"]).optional(),
          expires: z.enum(SHARE_DURATION_KEYS).optional().describe("How long the link works: 1h, 24h or 7d."),
        }),
        execute: async (input) => {
          const a = own(input.id)
          if ("ok" in a) return a
          const visibility = input.visibility ?? "private"
          const duration = input.expires ?? "24h"
          const made = createShare(db, { attachmentId: a.id, visibility, duration, createdBy: chatId }, clock())
          if (!made) return { ok: false as const, error: `No saved attachment ${input.id}.` }
          return {
            ok: true as const,
            url: shareUrl(appUrl(), made.token),
            visibility,
            expiresAt: new Date(made.share.expiresAt).toISOString(),
            note: visibility === "public" ? "Anyone with this link can open the file until it expires." : "Opens only after signing in to the dashboard.",
          }
        },
      }),
    },
    unshare_attachment: {
      class: "write",
      title: "Turn off links to an attachment",
      tool: tool({
        description: "Turn off every live share link to a saved file, e.g. when the owner says a link shouldn't work anymore. The file itself stays saved.",
        inputSchema: z.object({ id: z.string().min(1).max(64) }),
        execute: async (input) => {
          const a = own(input.id)
          if ("ok" in a) return a
          const n = revokeShares(db, a.id, clock())
          return { ok: true as const, revoked: n, message: n ? `${n} tautan ke "${a.title}" udah dimatikan.` : `Nggak ada tautan aktif ke "${a.title}".` }
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
