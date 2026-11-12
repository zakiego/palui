// Attachments page: files the agent saved from chats. Metadata only; the bytes
// go through GET /api/attachments/:id, which checks the session itself, or a
// share link (GET /api/share/:token).
import { deleteAttachment, getAttachment, listAttachments, previewKind } from "@palui/core/attachments/service"
import { activeShareCounts, createShare, listActiveShares, revokeShare, SHARE_DURATION_KEYS, shareUrl } from "@palui/core/attachments/shares"
import { config } from "@palui/core/config"
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { chatTitles } from "@/server/queries"
import { requireUser, requireWriter } from "@/server/session"

const attachmentId = z.string().min(1).max(100)

export const getAttachments = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const titles = chatTitles(db)
  const rows = listAttachments(db, { limit: 500 })
  const shares = activeShareCounts(db)
  return {
    attachments: rows.map(({ sha256: _sha, messageId: _msg, runId: _run, ...a }) => ({
      ...a,
      chat: titles.get(a.chatId) ?? a.chatId,
      preview: previewKind(a.mimeType),
      shares: shares.get(a.id) ?? 0,
    })),
    timezone: getSetting(db, "timezone"),
  }
})

export const deleteAttachmentFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: attachmentId }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!deleteAttachment(db, data.id)) throw new Error("That file doesn't exist anymore.")
    return { ok: true }
  })

// Live links for one file. The link itself isn't here: only its hash is stored.
export const getAttachmentShares = createServerFn({ method: "GET" })
  .validator(z.object({ id: attachmentId }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    return { shares: listActiveShares(db, data.id) }
  })

export const createAttachmentShareFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: attachmentId, visibility: z.enum(["public", "private"]), duration: z.enum(SHARE_DURATION_KEYS) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!getAttachment(db, data.id)) throw new Error("That file doesn't exist anymore.")
    const made = createShare(db, { attachmentId: data.id, visibility: data.visibility, duration: data.duration, createdBy: "owner" })
    if (!made) throw new Error("That file doesn't exist anymore.")
    return { share: made.share, url: shareUrl(config().appUrl, made.token) }
  })

export const revokeAttachmentShareFn = createServerFn({ method: "POST" })
  .validator(z.object({ shareId: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    revokeShare(db, data.shareId)
    return { ok: true }
  })
