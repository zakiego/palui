import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { claimOwner, createClaim, getOwner, isOwner, phoneToJid, setWhatsAppOwner } from "./owner"
import { ensureChat, rotateSession } from "./chats"
import { enqueueOutbox } from "./outbox/enqueue"

describe("telegram owner claim", () => {
  it("accepts a fresh nonce once and only on its channel", () => {
    const db = memoryDb()
    const { nonce } = createClaim(db, "telegram", 1000)
    expect(claimOwner(db, { channel: "whatsapp", nonce, externalId: "1" }, 2000)).toBe(false)
    expect(claimOwner(db, { channel: "telegram", nonce, externalId: "42", displayName: "Zaki" }, 2000)).toBe(true)
    expect(claimOwner(db, { channel: "telegram", nonce, externalId: "43" }, 3000)).toBe(false)
    expect(getOwner(db, "telegram")?.externalId).toBe("42")
    expect(isOwner(db, { channel: "telegram", externalId: "42" })).toBe(true)
    expect(isOwner(db, { channel: "telegram", externalId: "43" })).toBe(false)
  })

  it("rejects an expired nonce", () => {
    const db = memoryDb()
    const { nonce, expiresAt } = createClaim(db, "telegram", 1000)
    expect(claimOwner(db, { channel: "telegram", nonce, externalId: "42" }, expiresAt)).toBe(false)
  })
})

describe("whatsapp owner", () => {
  it("normalizes numbers", () => {
    expect(phoneToJid("0812-3456-789")).toBe("628123456789@s.whatsapp.net")
    expect(phoneToJid("+62 812 3456 789")).toBe("628123456789@s.whatsapp.net")
  })

  it("matches the phone JID, learns the LID, then matches LID-only messages", () => {
    const db = memoryDb()
    setWhatsAppOwner(db, "08123456789")
    expect(isOwner(db, { channel: "whatsapp", externalId: "999@lid" })).toBe(false)
    expect(isOwner(db, { channel: "whatsapp", externalId: "555@lid", altId: "628123456789@s.whatsapp.net" })).toBe(true)
    expect(getOwner(db, "whatsapp")?.altId).toBe("555@lid")
    expect(isOwner(db, { channel: "whatsapp", externalId: "555@lid" })).toBe(true)
    expect(isOwner(db, { channel: "whatsapp", externalId: "628999@s.whatsapp.net" })).toBe(false)
  })
})

describe("chats and outbox", () => {
  it("creates a chat with a session and rotates it", () => {
    const db = memoryDb()
    const chat = ensureChat(db, { channel: "telegram", externalId: "42", title: "Zaki" })
    expect(chat.sessionId).toBeTruthy()
    expect(ensureChat(db, { channel: "telegram", externalId: "42" }).id).toBe(chat.id)
    const { closed, opened } = rotateSession(db, chat.id, 5000)
    expect(closed?.closedAt).toBe(5000)
    expect(opened.id).not.toBe(chat.sessionId)
    const row = enqueueOutbox(db, { chatId: chat.id, channel: "telegram", payload: { text: "hi" }, source: "system" }, 100)
    expect(row).toMatchObject({ status: "pending", attempts: 0, nextAttemptAt: 100 })
  })
})
