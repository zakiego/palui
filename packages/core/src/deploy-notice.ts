// Tells the owner when a new version is running. Off by default; the owner turns
// it on per chat from an agent's Chats tab, and each of those chats gets one
// message through the bot that answers there. The runtime calls this once on start.
import { inArray } from "drizzle-orm"
import { chats, type DB } from "@palui/db"
import { enqueueOutbox, notifyOutbox } from "./outbox/enqueue"
import { getSetting, setSetting } from "./settings"

export const CHANGELOG_URL = "https://palui.zakiego.com/docs/changelog"

export type DeployNoticeResult =
  | { sent: false; reason: "same-version" | "first-run" | "off" }
  | { sent: true; chatIds: string[]; skipped: string[] }

export function deployNoticeText(version: string, previous: string): string {
  return `Palui sudah jalan di versi ${version} (sebelumnya ${previous}). Yang baru: ${CHANGELOG_URL}`
}

export function deployNoticeOn(db: DB, chatId: string): boolean {
  return getSetting(db, "deployNoticeChats").includes(chatId)
}

export function setDeployNotice(db: DB, chatId: string, on: boolean, now = Date.now()) {
  const current = getSetting(db, "deployNoticeChats").filter((id) => id !== chatId)
  setSetting(db, "deployNoticeChats", on ? [...current, chatId] : current, now)
}

// Records the running version every start, even while no chat has it on, so
// turning it on later doesn't announce a deploy that happened long ago. A fresh
// install has no previous version and stays quiet.
export function announceDeploy(db: DB, version: string, now = Date.now()): DeployNoticeResult {
  const previous = getSetting(db, "lastRunVersion")
  if (previous === version) return { sent: false, reason: "same-version" }
  setSetting(db, "lastRunVersion", version, now)
  if (previous === null) return { sent: false, reason: "first-run" }

  const wanted = getSetting(db, "deployNoticeChats")
  if (wanted.length === 0) return { sent: false, reason: "off" }
  // A chat that was removed or switched off is skipped, not sent to.
  const rows = db.select().from(chats).where(inArray(chats.id, wanted)).all().filter((c) => c.allowed)
  const text = deployNoticeText(version, previous)
  for (const chat of rows) {
    enqueueOutbox(
      db,
      { chatId: chat.id, channel: chat.channel, connectorId: chat.connectorId, payload: { text }, source: `deploy:${version}`, expiresAt: now + 24 * 3600_000 },
      now,
    )
  }
  if (rows.length > 0) notifyOutbox()
  const sentIds = rows.map((c) => c.id)
  return { sent: true, chatIds: sentIds, skipped: wanted.filter((id) => !sentIds.includes(id)) }
}
