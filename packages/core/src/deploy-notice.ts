// Tells the owner when a new version is running. Off by default; the owner turns
// it on in Settings and picks the chat. The runtime calls this once on start.
import { eq } from "drizzle-orm"
import { chats, type DB } from "@palui/db"
import { enqueueOutbox, notifyOutbox } from "./outbox/enqueue"
import { getSetting, setSetting } from "./settings"

export const CHANGELOG_URL = "https://palui.zakiego.com/docs/changelog"

export type DeployNoticeResult =
  | { sent: false; reason: "same-version" | "first-run" | "off" | "no-chat" | "chat-gone" }
  | { sent: true; chatId: string; outboxId: string }

export function deployNoticeText(version: string, previous: string): string {
  return `Palui sudah jalan di versi ${version} (sebelumnya ${previous}). Yang baru: ${CHANGELOG_URL}`
}

// Records the running version every start, even while the notice is off, so
// turning it on later doesn't announce a deploy that happened long ago. A fresh
// install has no previous version and stays quiet.
export function announceDeploy(db: DB, version: string, now = Date.now()): DeployNoticeResult {
  const previous = getSetting(db, "lastRunVersion")
  if (previous === version) return { sent: false, reason: "same-version" }
  setSetting(db, "lastRunVersion", version, now)
  if (previous === null) return { sent: false, reason: "first-run" }

  const notice = getSetting(db, "deployNotice")
  if (!notice.enabled) return { sent: false, reason: "off" }
  if (!notice.chatId) return { sent: false, reason: "no-chat" }
  const chat = db.select().from(chats).where(eq(chats.id, notice.chatId)).get()
  if (!chat || !chat.allowed) return { sent: false, reason: "chat-gone" }

  const row = enqueueOutbox(
    db,
    {
      chatId: chat.id,
      channel: chat.channel,
      connectorId: chat.connectorId,
      payload: { text: deployNoticeText(version, previous) },
      source: `deploy:${version}`,
      expiresAt: now + 24 * 3600_000,
    },
    now,
  )
  notifyOutbox()
  return { sent: true, chatId: chat.id, outboxId: row.id }
}
