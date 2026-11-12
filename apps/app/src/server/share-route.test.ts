import { ensureChat } from "@palui/core/chats"
import { saveAttachment } from "@palui/core/attachments/service"
import { createShare } from "@palui/core/attachments/shares"
import { memoryDb } from "@palui/db"
import { describe, expect, it } from "vitest"
import { handleShareRequest, type ShareRouteDeps } from "@/routes/api/share/$token"
import { TEST_MASTER_KEY } from "./test-helpers"

const NOW = Date.parse("2026-11-13T02:00:00Z")
const PNG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
const DOCX = Buffer.from([0x50, 0x4b, 0x03, 0x04, 0, 0])

function setup(signedIn = false) {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const png = saveAttachment(db, { chatId: chat.id, title: "Struk", mimeType: "image/png", filename: "struk.png", bytes: PNG }, TEST_MASTER_KEY, NOW).row
  const docx = saveAttachment(
    db,
    { chatId: chat.id, title: "Kontrak", mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", filename: "kontrak.docx", bytes: DOCX },
    TEST_MASTER_KEY,
    NOW,
  ).row
  let now = NOW
  const deps: ShareRouteDeps = { db: () => db, signedIn: async () => signedIn, masterKey: () => TEST_MASTER_KEY, now: () => now }
  const share = (id: string, visibility: "public" | "private") => createShare(db, { attachmentId: id, visibility, duration: "1h", createdBy: "owner" }, NOW)!.token
  return { db, png, docx, deps, share, later: (ms: number) => (now = NOW + ms) }
}

const get = (token: string, q = "") => new Request(`https://palui.example.com/api/share/${token}${q}`)

describe("GET /api/share/:token", () => {
  it("opens a public image inline without a session and counts the open", async () => {
    const { png, deps, share } = setup()
    const token = share(png.id, "public")
    const res = await handleShareRequest(get(token), token, deps)
    expect(res.status).toBe(200)
    expect(Buffer.from(await res.arrayBuffer()).equals(PNG)).toBe(true)
    expect(res.headers.get("content-type")).toBe("image/png")
    expect(res.headers.get("content-disposition")).toMatch(/^inline; filename="struk.png"/)
    expect(res.headers.get("x-content-type-options")).toBe("nosniff")
    expect(res.headers.get("content-security-policy")).toContain("sandbox")

    const forced = await handleShareRequest(get(token, "?download=1"), token, deps)
    expect(forced.headers.get("content-disposition")).toMatch(/^attachment;/)
  })

  it("always downloads a type the browser can't preview", async () => {
    const { docx, deps, share } = setup()
    const token = share(docx.id, "public")
    const res = await handleShareRequest(get(token), token, deps)
    expect(res.headers.get("content-disposition")).toMatch(/^attachment; filename="kontrak.docx"/)
  })

  it("needs a session for a private link", async () => {
    const out = setup(false)
    const token = out.share(out.png.id, "private")
    expect((await handleShareRequest(get(token), token, out.deps)).status).toBe(401)

    const inn = setup(true)
    const t2 = inn.share(inn.png.id, "private")
    expect((await handleShareRequest(get(t2), t2, inn.deps)).status).toBe(200)
  })

  it("410s an expired link and 404s an unknown one", async () => {
    const { png, deps, share, later } = setup()
    const token = share(png.id, "public")
    later(60 * 60 * 1000)
    const res = await handleShareRequest(get(token), token, deps)
    expect(res.status).toBe(410)
    expect(res.headers.get("content-type")).toBe("text/plain; charset=utf-8")
    expect((await handleShareRequest(get("x".repeat(32)), "x".repeat(32), deps)).status).toBe(404)
  })
})
