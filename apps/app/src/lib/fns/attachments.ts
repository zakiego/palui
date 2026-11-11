// Attachments page: files the agent saved from chats. Metadata only; the bytes
// go through GET /api/attachments/:id, which checks the session itself.
import { deleteAttachment, listAttachments } from "@palui/core/attachments/service"
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { chatTitles } from "@/server/queries"
import { requireUser, requireWriter } from "@/server/session"

export const getAttachments = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const titles = chatTitles(db)
  const rows = listAttachments(db, { limit: 500 })
  return {
    attachments: rows.map(({ sha256: _sha, messageId: _msg, runId: _run, ...a }) => ({ ...a, chat: titles.get(a.chatId) ?? a.chatId })),
    timezone: getSetting(db, "timezone"),
  }
})

export const deleteAttachmentFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!deleteAttachment(db, data.id)) throw new Error("That file doesn't exist anymore.")
    return { ok: true }
  })
