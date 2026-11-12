import { ensureChat } from "@palui/core/chats"
import { saveAttachment } from "@palui/core/attachments/service"
import { memoryDb } from "@palui/db"
import { describe, expect, it } from "vitest"
import { handleAttachmentRequest, type AttachmentRouteDeps } from "@/routes/api/attachments/$id"
import { TEST_MASTER_KEY } from "./test-helpers"

const PDF = Buffer.from("%PDF-1.7 tiket")

function setup(signedIn = true) {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, 1000)
  const { row } = saveAttachment(db, { chatId: chat.id, title: "Tiket", mimeType: "application/pdf", filename: "tiket \"BDJ\".pdf", bytes: PDF }, TEST_MASTER_KEY, 2000)
  const deps: AttachmentRouteDeps = {
    auth: async () => (signedIn ? { db } : Response.json({ error: "UNAUTHORIZED" }, { status: 401 })),
    masterKey: () => TEST_MASTER_KEY,
  }
  return { id: row.id, deps }
}

const get = (id: string) => new Request(`https://palui.example.com/api/attachments/${id}`)

describe("GET /api/attachments/:id", () => {
  it("refuses without a session", async () => {
    const { id, deps } = setup(false)
    expect((await handleAttachmentRequest(get(id), id, deps)).status).toBe(401)
  })

  it("serves the decrypted file as a download with safe headers", async () => {
    const { id, deps } = setup()
    const res = await handleAttachmentRequest(get(id), id, deps)
    expect(res.status).toBe(200)
    expect(Buffer.from(await res.arrayBuffer()).equals(PDF)).toBe(true)
    expect(res.headers.get("content-type")).toBe("application/pdf")
    expect(res.headers.get("content-disposition")).toBe(`attachment; filename="tiket BDJ.pdf"; filename*=UTF-8''tiket%20BDJ.pdf`)
    expect(res.headers.get("x-content-type-options")).toBe("nosniff")
    expect(res.headers.get("cache-control")).toBe("private, no-store")
  })

  it("shows previewable files inline with ?inline=1", async () => {
    const { id, deps } = setup()
    const res = await handleAttachmentRequest(get(`${id}?inline=1`), id, deps)
    expect(res.headers.get("content-disposition")).toMatch(/^inline; filename="tiket BDJ.pdf"/)
    expect(res.headers.get("x-content-type-options")).toBe("nosniff")
  })

  it("404s on unknown or malformed ids", async () => {
    const { deps } = setup()
    expect((await handleAttachmentRequest(get("att_nope"), "att_nope", deps)).status).toBe(404)
    expect((await handleAttachmentRequest(get("x"), "../etc", deps)).status).toBe(404)
  })
})
