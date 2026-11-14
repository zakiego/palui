// Attachments page: files the agent saved from chats. Metadata only; the bytes
// go through GET /api/attachments/:id or a share link (GET /api/share/:token).
import { noInput, withInput } from "./client"
import type { AttachmentKind, AttachmentShare, Ok, PreviewKind, ShareDuration, ShareVisibility } from "./types"

export type AttachmentItem = {
  chat: string
  preview: PreviewKind | null
  shares: number
  chatId: string
  createdAt: number
  description: string
  filename: string | null
  id: string
  kind: AttachmentKind
  mimeType: string
  size: number
  title: string
}

export type AttachmentsPage = { attachments: AttachmentItem[]; timezone: string }

export const getAttachments = noInput<AttachmentsPage>("getAttachments", "GET")
export const deleteAttachmentFn = withInput<{ id: string }, Ok>("deleteAttachmentFn", "POST")
// Live links for one file. The link itself isn't here: only its hash is stored.
export const getAttachmentShares = withInput<{ id: string }, { shares: AttachmentShare[] }>("getAttachmentShares", "GET")
export const createAttachmentShareFn = withInput<{ id: string; visibility: ShareVisibility; duration: ShareDuration }, { share: AttachmentShare; url: string }>(
  "createAttachmentShareFn",
  "POST",
)
export const revokeAttachmentShareFn = withInput<{ shareId: string }, Ok>("revokeAttachmentShareFn", "POST")
