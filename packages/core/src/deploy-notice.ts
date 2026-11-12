// Tells the owner when a new build is running: a new version, or a new commit on
// the same version. Off by default; the owner turns it on per chat from an
// agent's Chats tab, and each of those chats gets one message through the bot
// that answers there. The runtime calls this once on start.
import { inArray } from "drizzle-orm"
import { chats, type DB } from "@palui/db"
import { type BuildInfo, shortSha, subjectsSince } from "./build-info"
import { enqueueOutbox, notifyOutbox } from "./outbox/enqueue"
import { getSetting, setSetting } from "./settings"

export const CHANGELOG_URL = "https://palui.zakiego.com/docs/changelog"

export type DeployNoticeResult =
  | { sent: false; reason: "unchanged" | "first-run" | "off" }
  | { sent: true; chatIds: string[]; skipped: string[] }

// What is running now and what ran before. `commit` is null for a build without
// build-info.json (local, dev); then only the version decides.
export type DeployChange = {
  version: string
  previousVersion: string
  commit: string | null
  previousCommit: string | null
  // Commit subjects since the previous commit, newest first. Empty when unknown.
  subjects?: string[]
}

const MAX_SUBJECTS = 8

export function deployNoticeText(change: DeployChange): string {
  const { version, previousVersion, commit, previousCommit } = change
  if (version !== previousVersion) {
    const at = commit ? `, commit ${shortSha(commit)}` : ""
    return `Palui sudah jalan di versi ${version} (sebelumnya ${previousVersion})${at}. Yang baru: ${CHANGELOG_URL}`
  }
  const was = previousCommit ? ` (sebelumnya ${shortSha(previousCommit)})` : ""
  let text = `Palui di-update ke commit ${shortSha(commit ?? "")}${was}, versi ${version}`
  const subjects = change.subjects ?? []
  if (subjects.length > 0) {
    const shown = subjects.slice(0, MAX_SUBJECTS).map((s) => `- ${s}`)
    if (subjects.length > MAX_SUBJECTS) shown.push(`- dan ${subjects.length - MAX_SUBJECTS} lainnya`)
    text += `\n\n${shown.join("\n")}`
  }
  return text
}

export function deployNoticeOn(db: DB, chatId: string): boolean {
  return getSetting(db, "deployNoticeChats").includes(chatId)
}

export function setDeployNotice(db: DB, chatId: string, on: boolean, now = Date.now()) {
  const current = getSetting(db, "deployNoticeChats").filter((id) => id !== chatId)
  setSetting(db, "deployNoticeChats", on ? [...current, chatId] : current, now)
}

// Runs once per runtime start. Records the running version and commit every
// time, even while no chat has it on, so turning it on later doesn't announce a
// deploy from long ago. A fresh install has no previous version and stays quiet.
// The same version and commit as last time (a plain restart) sends nothing, and
// so does a build without a commit on the same version.
export function announceDeploy(db: DB, version: string, build: BuildInfo | null = null, now = Date.now()): DeployNoticeResult {
  // One transaction: the new version/commit and the outbox rows land together, so
  // a crash halfway neither loses the notice nor sends it twice on the next start.
  const result = db.transaction((tx) => recordDeploy(tx as unknown as DB, version, build, now))
  if (result.sent && result.chatIds.length > 0) notifyOutbox()
  return result
}

function recordDeploy(db: DB, version: string, build: BuildInfo | null, now: number): DeployNoticeResult {
  const previousVersion = getSetting(db, "lastRunVersion")
  const previousCommit = getSetting(db, "lastRunCommit")
  const commit = build?.commit ?? null
  const newCommit = commit !== null && commit !== previousCommit
  if (previousVersion === version && !newCommit) return { sent: false, reason: "unchanged" }
  setSetting(db, "lastRunVersion", version, now)
  // A build without a commit leaves the last known one, so going back to it later
  // isn't announced as new.
  if (commit) setSetting(db, "lastRunCommit", commit, now)
  if (previousVersion === null) return { sent: false, reason: "first-run" }

  const wanted = getSetting(db, "deployNoticeChats")
  if (wanted.length === 0) return { sent: false, reason: "off" }
  // A chat that was removed or switched off is skipped, not sent to.
  const rows = db.select().from(chats).where(inArray(chats.id, wanted)).all().filter((c) => c.allowed)
  const subjects = build && newCommit ? subjectsSince(build, previousCommit) : []
  const text = deployNoticeText({ version, previousVersion, commit, previousCommit: newCommit ? previousCommit : null, subjects })
  // The outbox waits for the channel to be ready (WhatsApp connected), and a
  // source per build makes the row easy to find.
  const source = commit ? `deploy:${version}:${shortSha(commit)}` : `deploy:${version}`
  for (const chat of rows) {
    enqueueOutbox(
      db,
      { chatId: chat.id, channel: chat.channel, connectorId: chat.connectorId, payload: { text }, source, expiresAt: now + 24 * 3600_000 },
      now,
    )
  }
  const sentIds = rows.map((c) => c.id)
  return { sent: true, chatIds: sentIds, skipped: wanted.filter((id) => !sentIds.includes(id)) }
}
