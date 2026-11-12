import { describe, expect, it } from "vitest"
import { eq } from "drizzle-orm"
import { chats, memoryDb, outbox } from "@palui/db"
import { ensureChat } from "./chats"
import { announceDeploy, deployNoticeOn, deployNoticeText, setDeployNotice } from "./deploy-notice"
import type { BuildInfo } from "./build-info"
import { getSetting, setSetting } from "./settings"

const NOW = Date.parse("2026-11-12T02:00:00Z")

function setup() {
  const db = memoryDb()
  const dm = ensureChat(db, { channel: "telegram", externalId: "42", title: "Zaki", connectorId: "tg-bot" }, NOW)
  const group = ensureChat(db, { channel: "whatsapp", externalId: "120@g.us", title: "Keluarga", kind: "group" }, NOW)
  return { db, dm, group }
}

describe("setDeployNotice", () => {
  it("turns one chat on and off without touching the others", () => {
    const { db, dm, group } = setup()
    expect(deployNoticeOn(db, dm.id)).toBe(false)
    setDeployNotice(db, dm.id, true, NOW)
    setDeployNotice(db, group.id, true, NOW)
    setDeployNotice(db, dm.id, true, NOW)
    expect(getSetting(db, "deployNoticeChats")).toEqual([group.id, dm.id])
    setDeployNotice(db, group.id, false, NOW)
    expect(deployNoticeOn(db, group.id)).toBe(false)
    expect(deployNoticeOn(db, dm.id)).toBe(true)
  })
})

describe("announceDeploy", () => {
  it("is off by default and still records the version", () => {
    const { db } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    expect(announceDeploy(db, "0.8.0", null, NOW)).toEqual({ sent: false, reason: "off" })
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
    expect(db.select().from(outbox).all()).toHaveLength(0)
  })

  it("stays quiet on a fresh install", () => {
    const { db, dm } = setup()
    setDeployNotice(db, dm.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", null, NOW)).toEqual({ sent: false, reason: "first-run" })
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
  })

  it("sends once to every chat that has it on, through that chat's bot", () => {
    const { db, dm, group } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setDeployNotice(db, dm.id, true, NOW)
    setDeployNotice(db, group.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", null, NOW)).toMatchObject({ sent: true, skipped: [] })
    const rows = db.select().from(outbox).all()
    expect(rows).toHaveLength(2)
    const toDm = rows.find((r) => r.chatId === dm.id)!
    expect(toDm).toMatchObject({ channel: "telegram", connectorId: "tg-bot", source: "deploy:0.8.0", status: "pending" })
    expect(toDm.payload).toEqual({ text: deployNoticeText({ version: "0.8.0", previousVersion: "0.7.0", commit: null, previousCommit: null }) })
    expect(rows.find((r) => r.chatId === group.id)).toMatchObject({ channel: "whatsapp", connectorId: null })
    // A restart on the same version sends nothing.
    expect(announceDeploy(db, "0.8.0", null, NOW + 1000)).toEqual({ sent: false, reason: "unchanged" })
    expect(db.select().from(outbox).all()).toHaveLength(2)
  })

  it("skips a chat that was removed or switched off", () => {
    const { db, dm, group } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setDeployNotice(db, dm.id, true, NOW)
    setDeployNotice(db, group.id, true, NOW)
    setDeployNotice(db, "chat_missing", true, NOW)
    db.update(chats).set({ allowed: false }).where(eq(chats.id, group.id)).run()
    expect(announceDeploy(db, "0.8.0", null, NOW)).toEqual({ sent: true, chatIds: [dm.id], skipped: [group.id, "chat_missing"] })
    expect(db.select().from(outbox).all().map((r) => r.chatId)).toEqual([dm.id])
  })
})

const A = "aaaaaaa1111111111111111111111111111111111"
const B = "bbbbbbb2222222222222222222222222222222222"
const C = "ccccccc3333333333333333333333333333333333"

function build(commit: string, history: BuildInfo["history"] = []): BuildInfo {
  return { commit, history }
}

function texts(db: ReturnType<typeof memoryDb>) {
  return db.select().from(outbox).all().map((r) => (r.payload as { text: string }).text)
}

describe("announceDeploy with a commit", () => {
  it("adds the short commit to a version bump", () => {
    const { db, dm } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setSetting(db, "lastRunCommit", A, NOW)
    setDeployNotice(db, dm.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", build(B), NOW)).toMatchObject({ sent: true })
    expect(texts(db)).toEqual(["Palui sudah jalan di versi 0.8.0 (sebelumnya 0.7.0), commit bbbbbbb. Yang baru: https://palui.zakiego.com/docs/changelog"])
    expect(db.select().from(outbox).get()!.source).toBe("deploy:0.8.0:bbbbbbb")
    expect(getSetting(db, "lastRunCommit")).toBe(B)
  })

  it("announces a new commit on the same version, with the subjects since the last one", () => {
    const { db, dm } = setup()
    setSetting(db, "lastRunVersion", "0.8.0", NOW)
    setSetting(db, "lastRunCommit", A, NOW)
    setDeployNotice(db, dm.id, true, NOW)
    const info = build(C, [
      { sha: C, subjects: ["fix(app): open a flow from anywhere", "feat(core): early answers"] },
      { sha: B, subjects: ["fix(core): reply on limits"] },
      { sha: A, subjects: ["chore(release): v0.8.0"] },
    ])
    expect(announceDeploy(db, "0.8.0", info, NOW)).toMatchObject({ sent: true, chatIds: [dm.id] })
    expect(texts(db)).toEqual([
      "Palui di-update ke commit ccccccc (sebelumnya aaaaaaa), versi 0.8.0\n\n- fix(app): open a flow from anywhere\n- feat(core): early answers\n- fix(core): reply on limits",
    ])
    expect(getSetting(db, "lastRunCommit")).toBe(C)
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
  })

  it("sends nothing on restarts with the same commit", () => {
    const { db, dm } = setup()
    setSetting(db, "lastRunVersion", "0.8.0", NOW)
    setSetting(db, "lastRunCommit", A, NOW)
    setDeployNotice(db, dm.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", build(B), NOW)).toMatchObject({ sent: true })
    for (let i = 1; i <= 3; i++) {
      expect(announceDeploy(db, "0.8.0", build(B), NOW + i * 1000)).toEqual({ sent: false, reason: "unchanged" })
    }
    expect(db.select().from(outbox).all()).toHaveLength(1)
  })

  it("names only the new commit when the previous one isn't known", () => {
    const { db, dm } = setup()
    // The first start after build-info.json shipped: a version, no commit yet.
    setSetting(db, "lastRunVersion", "0.8.0", NOW)
    setDeployNotice(db, dm.id, true, NOW)
    announceDeploy(db, "0.8.0", build(B, [{ sha: B, subjects: ["feat(core): commit notices"] }, { sha: A, subjects: ["older"] }]), NOW)
    expect(texts(db)).toEqual(["Palui di-update ke commit bbbbbbb, versi 0.8.0\n\n- feat(core): commit notices"])
  })

  it("caps a long list of subjects", () => {
    const subjects = Array.from({ length: 11 }, (_, i) => `commit ${i + 1}`)
    const text = deployNoticeText({ version: "0.8.0", previousVersion: "0.8.0", commit: C, previousCommit: A, subjects })
    expect(text.split("\n").slice(2)).toEqual([...subjects.slice(0, 8).map((s) => `- ${s}`), "- dan 3 lainnya"])
    expect(text.endsWith(".")).toBe(false)
  })

  it("falls back to the version alone when the build has no commit", () => {
    const { db, dm } = setup()
    setSetting(db, "lastRunVersion", "0.8.0", NOW)
    setSetting(db, "lastRunCommit", A, NOW)
    setDeployNotice(db, dm.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", null, NOW)).toEqual({ sent: false, reason: "unchanged" })
    // The last known commit stays, so going back to it isn't news.
    expect(getSetting(db, "lastRunCommit")).toBe(A)
    expect(announceDeploy(db, "0.9.0", null, NOW)).toMatchObject({ sent: true })
    expect(texts(db)).toEqual([deployNoticeText({ version: "0.9.0", previousVersion: "0.8.0", commit: null, previousCommit: null })])
    expect(texts(db)[0]).not.toContain("commit")
    expect(announceDeploy(db, "0.9.0", build(A), NOW)).toEqual({ sent: false, reason: "unchanged" })
  })

  it("records the commit on a fresh install without sending", () => {
    const { db, dm } = setup()
    setDeployNotice(db, dm.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", build(A), NOW)).toEqual({ sent: false, reason: "first-run" })
    expect(getSetting(db, "lastRunCommit")).toBe(A)
  })
})
