// Chats page: the list, a transcript, the web chat input and approvals.
import { noInput, withInput } from "./client"
import type { AgentChoice, ApprovalReason, Channel, ChatKind, MessageRole, MessageStatus, ReplyMode, ToolCallView, ToolClass } from "./types"

export type ChatListItem = {
  id: string
  channel: Channel
  title: string
  kind: ChatKind
  allowed: boolean
  replyMode: ReplyMode
  ack: boolean
  ackEmoji: string
  lastAt: number
  preview: string
  previewRole: MessageRole | null
}

export type ChatsPage = { chats: ChatListItem[]; webChatId: string }

export type TranscriptMessage = {
  id: string
  role: "assistant" | "user"
  source: "hook" | null
  sender: string | null
  text: string
  status: MessageStatus
  createdAt: number
  tools: ToolCallView[]
  // Files the run sent with send_media (assistant rows only).
  media: SentMediaView[]
}

export type SentMediaKind = "image" | "video" | "voice" | "audio" | "document" | "sticker"

export type SentMediaView = {
  id: string
  kind: SentMediaKind
  mimeType: string
  filename: string
  size: number
  caption: string
  status: "pending" | "sending" | "sent" | "failed" | "cancelled"
  error: string | null
}

export type Transcript = {
  chat: {
    id: string
    channel: Channel
    title: string
    kind: ChatKind
    agentId: string | null
    allowed: boolean
    replyMode: ReplyMode
    ack: boolean
    ackEmoji: string
    connectorId: string
    saveMedia: boolean
    saveMediaDays: number
    autoSaved: number
    autoSavedSize: number
  }
  messages: TranscriptMessage[]
  approvals: { id: string; tool: string; class: ToolClass; argsPreview: string | null; expiresAt: number; reason: ApprovalReason | null }[]
  agent: { id: string; name: string; isDefault: boolean; slug: string }
  shared: boolean
  agents: AgentChoice[]
}

export const getChats = noInput<ChatsPage>("getChats", "GET")
export const getTranscript = withInput<{ chatId: string }, Transcript>("getTranscript", "GET")
export const sendWebMessage = withInput<{ text: string }, { ok: true; chatId: string; messageId: string }>("sendWebMessage", "POST")
export const decideApproval = withInput<{ id: string; approved: boolean }, { ok: boolean }>("decideApproval", "POST")
export const stopTurn = withInput<{ chatId: string }, { ok: boolean }>("stopTurn", "POST")
