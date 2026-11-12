import { describe, expect, it } from "vitest"
import { eq } from "drizzle-orm"
import { chats, memoryDb, outbox } from "@palui/db"
import { ensureChat } from "./chats"
import { announceDeploy, deployNoticeText } from "./deploy-notice"
import { getSetting, setSetting } from "./settings"
import { settingsPatchSchema } from "./settings-schema"

const NOW = Date.parse("2026-11-12T02:00:00Z")

function setup() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42", title: "Zaki", connectorId: "tg-bot" }, NOW)
  return { db, chat }
}

describe("announceDeploy", () => {
  it("is off by default and still records the version", () => {
    const { db } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    expect(announceDeploy(db, "0.8.0", NOW)).toEqual({ sent: false, reason: "off" })
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
    expect(db.select().from(outbox).all()).toHaveLength(0)
  })

  it("stays quiet on a fresh install", () => {
    const { db, chat } = setup()
    setSetting(db, "deployNotice", { enabled: true, chatId: chat.id }, NOW)
    expect(announceDeploy(db, "0.8.0", NOW)).toEqual({ sent: false, reason: "first-run" })
    expect(getSetting(db, "lastRunVersion")).toBe("0.8.0")
  })

  it("sends once to the picked chat when the version changes", () => {
    const { db, chat } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setSetting(db, "deployNotice", { enabled: true, chatId: chat.id }, NOW)
    const res = announceDeploy(db, "0.8.0", NOW)
    expect(res).toMatchObject({ sent: true, chatId: chat.id })
    const rows = db.select().from(outbox).all()
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ chatId: chat.id, channel: "telegram", connectorId: "tg-bot", source: "deploy:0.8.0", status: "pending" })
    expect(rows[0].payload).toEqual({ text: deployNoticeText("0.8.0", "0.7.0") })
    // A restart on the same version sends nothing.
    expect(announceDeploy(db, "0.8.0", NOW + 1000)).toEqual({ sent: false, reason: "same-version" })
    expect(db.select().from(outbox).all()).toHaveLength(1)
  })

  it("skips a chat that was removed or switched off", () => {
    const { db, chat } = setup()
    setSetting(db, "lastRunVersion", "0.7.0", NOW)
    setSetting(db, "deployNotice", { enabled: true, chatId: chat.id }, NOW)
    db.update(chats).set({ allowed: false }).where(eq(chats.id, chat.id)).run()
    expect(announceDeploy(db, "0.8.0", NOW)).toEqual({ sent: false, reason: "chat-gone" })
    setSetting(db, "deployNotice", { enabled: true, chatId: "chat_missing" }, NOW)
    expect(announceDeploy(db, "0.9.0", NOW)).toEqual({ sent: false, reason: "chat-gone" })
  })
})

describe("deployNotice setting", () => {
  it("needs a chat when it is on", () => {
    expect(settingsPatchSchema.safeParse({ deployNotice: { enabled: true, chatId: null } }).success).toBe(false)
    expect(settingsPatchSchema.safeParse({ deployNotice: { enabled: false, chatId: null } }).success).toBe(true)
    expect(settingsPatchSchema.safeParse({ deployNotice: { enabled: true, chatId: "chat_1" } }).success).toBe(true)
  })
})
