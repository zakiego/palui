import { describe, expect, it } from "vitest"
import { attachmentShares } from "@palui/db"
import { HOUR, NOW, setup } from "../reminders/test-helpers"
import { deleteAttachment, previewKind, saveAttachment } from "./service"
import { activeShareCounts, createShare, listActiveShares, recordOpen, resolveShare, revokeShare, revokeShares, shareUrl } from "./shares"

const KEY = "test-master-key-0123456789abcdef0123456789"
const PDF = Buffer.from("%PDF-1.7\nktp")

function withFile() {
  const s = setup()
  const { row } = saveAttachment(s.db, { chatId: s.chatId, title: "KTP", mimeType: "application/pdf", bytes: PDF }, KEY, NOW)
  return { ...s, id: row.id }
}

describe("attachment shares", () => {
  it("makes a link that resolves until it expires, storing only the hash", () => {
    const { db, id } = withFile()
    const made = createShare(db, { attachmentId: id, visibility: "public", duration: "1h", createdBy: "owner" }, NOW)!
    expect(made.token).toMatch(/^[A-Za-z0-9_-]{32}$/)
    expect(made.share).not.toHaveProperty("tokenHash")
    expect(made.share.expiresAt).toBe(NOW + HOUR)
    expect(db.select().from(attachmentShares).get()!.tokenHash).not.toContain(made.token)
    expect(shareUrl("https://palui.test", made.token)).toBe(`https://palui.test/api/share/${made.token}`)

    expect(resolveShare(db, made.token, NOW + HOUR - 1)).toMatchObject({ ok: true, attachment: { id, title: "KTP" }, share: { visibility: "public" } })
    expect(resolveShare(db, made.token, NOW + HOUR)).toEqual({ ok: false, reason: "expired" })
    expect(resolveShare(db, "nope", NOW)).toEqual({ ok: false, reason: "not_found" })
    expect(resolveShare(db, "x".repeat(32), NOW)).toEqual({ ok: false, reason: "not_found" })
  })

  it("refuses a missing attachment", () => {
    const { db } = setup()
    expect(createShare(db, { attachmentId: "att_nope", visibility: "private", duration: "24h", createdBy: "owner" }, NOW)).toBeUndefined()
  })

  it("counts opens, lists live links and revokes them", () => {
    const { db, id } = withFile()
    const a = createShare(db, { attachmentId: id, visibility: "public", duration: "24h", createdBy: "owner" }, NOW)!
    const b = createShare(db, { attachmentId: id, visibility: "private", duration: "1h", createdBy: "chat_1" }, NOW + 1)!
    recordOpen(db, a.share.id, NOW + 5)
    recordOpen(db, a.share.id, NOW + 6)

    expect(listActiveShares(db, id, NOW + 10).map((s) => s.id)).toEqual([b.share.id, a.share.id])
    expect(listActiveShares(db, id, NOW + 10)[1]).toMatchObject({ openCount: 2, lastOpenedAt: NOW + 6 })
    expect(listActiveShares(db, id, NOW + 2 * HOUR).map((s) => s.id)).toEqual([a.share.id])
    expect(activeShareCounts(db, NOW + 10).get(id)).toBe(2)

    expect(revokeShare(db, a.share.id, NOW + 20)).toBe(true)
    expect(revokeShare(db, a.share.id, NOW + 21)).toBe(false)
    expect(resolveShare(db, a.token, NOW + 30)).toEqual({ ok: false, reason: "revoked" })
    expect(revokeShares(db, id, NOW + 30)).toBe(1)
    expect(activeShareCounts(db, NOW + 40).get(id)).toBeUndefined()
  })

  it("drops the links when the file is deleted", () => {
    const { db, id } = withFile()
    const made = createShare(db, { attachmentId: id, visibility: "public", duration: "7d", createdBy: "owner" }, NOW)!
    expect(deleteAttachment(db, id)).toBe(true)
    expect(resolveShare(db, made.token, NOW)).toEqual({ ok: false, reason: "not_found" })
    expect(db.select().from(attachmentShares).all()).toEqual([])
  })
})

describe("previewKind", () => {
  it("previews images, PDFs and text, and nothing else", () => {
    expect(previewKind("image/png")).toBe("image")
    expect(previewKind("application/pdf")).toBe("pdf")
    expect(previewKind("text/csv")).toBe("text")
    expect(previewKind("image/heic")).toBeNull()
    expect(previewKind("application/msword")).toBeNull()
  })
})
