import { describe, expect, it } from "vitest"
import { messages, newId, savedAttachments } from "@palui/db"
import { setup, NOW } from "../reminders/test-helpers"
import {
  AttachmentError,
  checkFile,
  cleanFilename,
  countAttachments,
  deleteAttachment,
  downloadName,
  getAttachment,
  listAttachments,
  MAX_ATTACHMENT_BYTES,
  readAttachment,
  saveAttachment,
  recentCandidates,
  RECENT_FILES_MAX,
  turnCandidates,
} from "./service"

const KEY = "test-master-key-0123456789abcdef0123456789"
const PDF = Buffer.from("%PDF-1.7\nktp")
const PNG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])

describe("checkFile", () => {
  it("accepts allowed types whose bytes match", () => {
    expect(checkFile("application/pdf", PDF)).toBe("document")
    expect(checkFile("image/png; charset=binary", PNG)).toBe("image")
    expect(checkFile("text/plain", Buffer.from("halo"))).toBe("document")
  })

  it("refuses unknown types, mismatched bytes, empty and oversized files", () => {
    expect(() => checkFile("image/svg+xml", Buffer.from("<svg/>"))).toThrow(AttachmentError)
    expect(() => checkFile("text/html", Buffer.from("<script>"))).toThrow(/can't be saved/)
    expect(() => checkFile("audio/ogg", Buffer.from("OggS"))).toThrow(AttachmentError)
    expect(() => checkFile("application/pdf", Buffer.from("MZ\x90\x00"))).toThrow(/doesn't match/)
    expect(() => checkFile("application/pdf", Buffer.alloc(0))).toThrow(/empty/)
    const big = Buffer.alloc(MAX_ATTACHMENT_BYTES + 1)
    PDF.copy(big)
    expect(() => checkFile("application/pdf", big)).toThrow(/over the 10.0 MB limit/)
  })
})

describe("saved attachments", () => {
  it("encrypts at rest, round-trips, dedupes per chat and scopes lists to a chat", () => {
    const { db, chatId, otherChatId } = setup()
    const { row, deduped } = saveAttachment(db, { chatId, title: "KTP Zaki", description: "KTP, NIK 6371...", mimeType: "application/pdf", filename: "../../ktp.pdf", bytes: PDF }, KEY, NOW)
    expect(deduped).toBe(false)
    expect(row).toMatchObject({ title: "KTP Zaki", kind: "document", size: PDF.length, filename: "ktp.pdf", createdAt: NOW })
    expect(row).not.toHaveProperty("data")
    const stored = db.select().from(savedAttachments).get()!
    expect(stored.data.includes(PDF)).toBe(false)
    expect(readAttachment(db, row.id, KEY)!.bytes.equals(PDF)).toBe(true)
    expect(() => readAttachment(db, row.id, `${KEY}x`)).toThrow()

    expect(saveAttachment(db, { chatId, title: "lagi", mimeType: "application/pdf", bytes: PDF }, KEY, NOW + 1)).toMatchObject({ deduped: true, row: { id: row.id } })
    saveAttachment(db, { chatId: otherChatId, title: "KTP di chat lain", mimeType: "application/pdf", bytes: PDF }, KEY, NOW + 2)
    saveAttachment(db, { chatId, title: "Struk 50%_off", mimeType: "image/png", bytes: PNG }, KEY, NOW + 3)

    expect(countAttachments(db)).toBe(3)
    expect(listAttachments(db, { chatId }).map((a) => a.title)).toEqual(["Struk 50%_off", "KTP Zaki"])
    expect(listAttachments(db, { chatId, query: "ktp" }).map((a) => a.title)).toEqual(["KTP Zaki"])
    expect(listAttachments(db, { chatId, query: "6371" }).map((a) => a.title)).toEqual(["KTP Zaki"])
    expect(listAttachments(db, { chatId, query: "%" }).map((a) => a.title)).toEqual(["Struk 50%_off"])
    expect(listAttachments(db).length).toBe(3)

    expect(deleteAttachment(db, row.id)).toBe(true)
    expect(getAttachment(db, row.id)).toBeUndefined()
    expect(deleteAttachment(db, row.id)).toBe(false)
  })

  it("finds images and documents with bytes in the run's user messages, in order", () => {
    const { db, chatId, otherChatId } = setup()
    const add = (chat: string, runId: string, media: unknown[], at: number) =>
      db.insert(messages).values({ id: newId("msg"), chatId: chat, role: "user", content: { text: "", media }, runId, createdAt: at }).run()
    add(chatId, "run_1", [{ kind: "image", mimeType: "image/png", data: PNG.toString("base64") }, { kind: "audio", mimeType: "audio/ogg", note: "transcribed" }], NOW)
    add(chatId, "run_1", [{ kind: "document", mimeType: "application/pdf", filename: "tiket.pdf", data: PDF.toString("base64") }, { kind: "document", mimeType: "application/pdf", filename: "old.pdf" }], NOW + 1)
    add(chatId, "run_0", [{ kind: "image", mimeType: "image/png", data: PNG.toString("base64") }], NOW - 1)
    add(otherChatId, "run_1", [{ kind: "image", mimeType: "image/png", data: PNG.toString("base64") }], NOW)
    const c = turnCandidates(db, chatId, "run_1")
    expect(c.map((x) => [x.index, x.kind, x.filename])).toEqual([
      [1, "image", null],
      [2, "document", "tiket.pdf"],
    ])
    expect(c[1]!.size).toBe(PDF.length)
  })

  it("finds recent files in the chat, newest first, within 24 hours and capped", () => {
    const { db, chatId, otherChatId } = setup()
    const add = (chat: string, text: string, media: unknown[], at: number, role: "user" | "assistant" = "user") =>
      db.insert(messages).values({ id: newId("msg"), chatId: chat, role, content: { text, media }, runId: "run_x", createdAt: at }).run()
    const png = { kind: "image", mimeType: "image/png", data: PNG.toString("base64") }
    add(chatId, "lama", [png], NOW - 25 * 3600_000)
    add(chatId, "Nasi   goreng", [png], NOW - 60_000)
    add(chatId, "voice", [{ kind: "audio", mimeType: "audio/ogg", data: "AAAA" }], NOW - 50_000)
    add(chatId, "Gorengan", [png, { kind: "document", mimeType: "application/pdf", filename: "a.pdf", data: PDF.toString("base64") }], NOW - 30_000)
    add(chatId, "dari bot", [png], NOW - 20_000, "assistant")
    add(otherChatId, "lain", [png], NOW - 10_000)
    const c = recentCandidates(db, chatId, NOW)
    expect(c.map((x) => [x.index, x.kind, x.caption])).toEqual([
      [1, "image", "Gorengan"],
      [2, "document", "Gorengan"],
      [3, "image", "Nasi goreng"],
    ])
    for (let i = 0; i < RECENT_FILES_MAX; i++) add(chatId, `n${i}`, [png], NOW - 1000 + i)
    expect(recentCandidates(db, chatId, NOW)).toHaveLength(RECENT_FILES_MAX)
  })

  it("makes safe download names", () => {
    expect(cleanFilename('C:\\x\\a"b\n.pdf')).toBe("ab.pdf")
    expect(cleanFilename("///")).toBe("file")
    expect(downloadName({ filename: null, title: "KTP Zaki", mimeType: "image/jpeg" })).toBe("KTP Zaki.jpg")
    expect(downloadName({ filename: "tiket.pdf", title: "x", mimeType: "application/pdf" })).toBe("tiket.pdf")
  })
})
