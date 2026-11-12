import { describe, expect, it } from "vitest"
import { eq } from "drizzle-orm"
import { chats, memoryDb, outbox } from "@palui/db"
import { ensureChat } from "./chats"
import { announceDeploy, deployNoticeOn, deployNoticeText, setDeployNotice } from "./deploy-notice"
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
    expect(announceDeploy(db, "0.8.0", NOW)).toEqual({ sent: false, reason: "off" })
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
    expect(db.select().from(outbox).all()).toHaveLength(0)
  })

  it("stays quiet on a fresh install", () => {
    const { db, dm } = setup()
    setDeployNotice(db, dm.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", NOW)).toEqual({ sent: false, reason: "first-run" })
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
  })

  it("sends once to every chat that has it on, through that chat's bot", () => {
    const { db, dm, group } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setDeployNotice(db, dm.id, true, NOW)
    setDeployNotice(db, group.id, true, NOW)
    expect(announceDeploy(db, "0.8.0", NOW)).toMatchObject({ sent: true, skipped: [] })
    const rows = db.select().from(outbox).all()
    expect(rows).toHaveLength(2)
    const toDm = rows.find((r) => r.chatId === dm.id)!
    expect(toDm).toMatchObject({ channel: "telegram", connectorId: "tg-bot", source: "deploy:0.8.0", status: "pending" })
    expect(toDm.payload).toEqual({ text: deployNoticeText("0.8.0", "0.7.0") })
    expect(rows.find((r) => r.chatId === group.id)).toMatchObject({ channel: "whatsapp", connectorId: null })
    // A restart on the same version sends nothing.
    expect(announceDeploy(db, "0.8.0", NOW + 1000)).toEqual({ sent: false, reason: "same-version" })
    expect(db.select().from(outbox).all()).toHaveLength(2)
  })

  it("skips a chat that was removed or switched off", () => {
    const { db, dm, group } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setDeployNotice(db, dm.id, true, NOW)
    setDeployNotice(db, group.id, true, NOW)
    setDeployNotice(db, "chat_missing", true, NOW)
    db.update(chats).set({ allowed: false }).where(eq(chats.id, group.id)).run()
    expect(announceDeploy(db, "0.8.0", NOW)).toEqual({ sent: true, chatIds: [dm.id], skipped: [group.id, "chat_missing"] })
    expect(db.select().from(outbox).all().map((r) => r.chatId)).toEqual([dm.id])
  })
})
