import { describe, expect, it } from "vitest"
import { memoryDb, messages, newId, type DB } from "@palui/db"
import { ensureChat } from "@palui/core/chats"
import { readAttachment } from "@palui/core/attachments/service"
import { resolveShare } from "@palui/core/attachments/shares"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import manifest from "./manifest"
import { attachmentTools } from "./runtime"

const NOW = Date.parse("2026-11-10T02:00:00Z")
const KEY = "test-master-key-0123456789abcdef0123456789"
const PDF = Buffer.from("%PDF-1.7 tiket").toString("base64")
const JPG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 0x10]).toString("base64")

function setup() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "whatsapp", externalId: "628@s.whatsapp.net" }, NOW)
  const other = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const turn: TurnInfo = { runId: "run_2", batchRunId: "run_1", chatId: chat.id, channel: "whatsapp", trigger: "message", tz: "Asia/Makassar", now: NOW }
  const tools = attachmentTools(db, turn, { clock: () => NOW, key: () => KEY, appUrl: () => "https://palui.test" })
  return { db, chat, other, turn, tools }
}

function inbound(db: DB, chatId: string, runId: string, media: unknown[], at = NOW, text = "") {
  db.insert(messages).values({ id: newId("msg"), chatId, role: "user", content: { text, media }, runId, createdAt: at }).run()
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

describe("attachments manifest", () => {
  it("is a builtin skill", () => {
    expect(manifest).toMatchObject({ id: "attachments", kind: "skill", builtin: true, icon: "paperclip" })
  })
})

describe("attachment tools", () => {
  it("exposes the tools with classes and tells the model to save only what matters", () => {
    const { tools } = setup()
    expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.class]))).toEqual({
      save_attachment: "write",
      list_attachments: "read",
      get_attachment: "read",
      share_attachment: "write",
      unshare_attachment: "write",
      delete_attachment: "destructive",
    })
    expect(tools.save_attachment!.tool.description).toMatch(/Only save files that are worth keeping/)
    expect(tools.save_attachment!.tool.description).toMatch(/Do NOT save casual photos/)
    expect(tools.save_attachment!.tool.description).toMatch(/pass its `fileRef`/)
  })

  it("saves the file from the batch run, asks for nomor when there are several, and dedupes", async () => {
    const { db, chat, other, tools } = setup()
    expect(await call(tools.save_attachment, { title: "x" })).toMatchObject({ ok: false, error: expect.stringMatching(/no image or document/) })

    inbound(db, chat.id, "run_1", [
      { kind: "image", mimeType: "image/jpeg", data: JPG },
      { kind: "document", mimeType: "application/pdf", filename: "tiket.pdf", data: PDF },
    ])
    inbound(db, other.id, "run_1", [{ kind: "image", mimeType: "image/jpeg", data: JPG }])
    const ambiguous = await call(tools.save_attachment, { title: "Tiket" })
    expect(ambiguous).toMatchObject({ ok: false, files: [{ nomor: 1, kind: "image" }, { nomor: 2, filename: "tiket.pdf" }] })
    expect(await call(tools.save_attachment, { nomor: 3, title: "Tiket" })).toMatchObject({ ok: false, error: "There is no file number 3." })

    const saved = await call(tools.save_attachment, { nomor: 2, title: "Tiket BDJ-CGK", description: "Tiket pesawat 12 Des" })
    expect(saved).toMatchObject({ ok: true, deduped: false, message: 'Oke, "Tiket BDJ-CGK" udah aku simpan.', attachment: { title: "Tiket BDJ-CGK", kind: "document", filename: "tiket.pdf", mimeType: "application/pdf" } })
    expect(saved.attachment.url).toBe(`https://palui.test/app/attachments?id=${saved.attachment.id}`)
    expect(saved.attachment.fileRef).toBe(`palui:attachment/${saved.attachment.id}`)
    expect(saved.sendToOtherApp).toContain("fileRef")
    expect(readAttachment(db, saved.attachment.id, KEY)!.meta).toMatchObject({ chatId: chat.id, runId: "run_2" })
    expect(await call(tools.save_attachment, { nomor: 2, title: "lagi" })).toMatchObject({ ok: true, deduped: true })
  })

  it("saves a receipt from an earlier message after the owner confirms (rpt_0mwdxaorjuq9wfs1q4i)", async () => {
    const { db, chat, other, tools } = setup()
    const GORENG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 1]).toString("base64")
    // Photo, preview, "ya": the run that saves is not the one the photo came with.
    inbound(db, chat.id, "run_a", [{ kind: "image", mimeType: "image/jpeg", data: JPG }], NOW - 60_000, "Nasi goreng")
    inbound(db, chat.id, "run_b", [{ kind: "image", mimeType: "image/jpeg", data: GORENG }], NOW - 30_000, "Gorengan")
    inbound(db, chat.id, "run_x", [{ kind: "image", mimeType: "image/jpeg", data: JPG }], NOW - 25 * 3600_000, "kemarin")
    inbound(db, other.id, "run_y", [{ kind: "image", mimeType: "image/jpeg", data: JPG }], NOW - 1000, "chat lain")
    inbound(db, chat.id, "run_1", [], NOW, "ya")

    const hint = await call(tools.save_attachment, { title: "Struk" })
    expect(hint).toMatchObject({ ok: false, error: expect.stringMatching(/earlier: true/), files: [{ nomor: 1, caption: "Gorengan" }, { nomor: 2, caption: "Nasi goreng" }] })
    expect(hint.files).toHaveLength(2)

    expect(await call(tools.save_attachment, { earlier: true, title: "Struk" })).toMatchObject({ ok: false, files: [{ nomor: 1 }, { nomor: 2 }] })
    expect(await call(tools.save_attachment, { earlier: true, nomor: 3, title: "Struk" })).toMatchObject({ ok: false, error: "There is no earlier file number 3." })

    const saved = await call(tools.save_attachment, { earlier: true, nomor: 2, title: "Struk nasi goreng" })
    expect(saved).toMatchObject({ ok: true, deduped: false, attachment: { title: "Struk nasi goreng", kind: "image" } })
    expect(readAttachment(db, saved.attachment.id, KEY)!.bytes.toString("base64")).toBe(JPG)
  })

  it("says so when nothing was sent recently", async () => {
    const { tools } = setup()
    expect(await call(tools.save_attachment, { earlier: true, title: "x" })).toMatchObject({ ok: false, error: expect.stringMatching(/last 24 hours/) })
  })

  it("refuses files that fail validation", async () => {
    const { db, chat, tools } = setup()
    inbound(db, chat.id, "run_1", [{ kind: "document", mimeType: "application/pdf", filename: "fake.pdf", data: Buffer.from("MZ").toString("base64") }])
    expect(await call(tools.save_attachment, { title: "x" })).toMatchObject({ ok: false, error: expect.stringMatching(/doesn't match/) })
  })

  it("lists, gets and deletes within the turn's chat only", async () => {
    const { db, chat, other, tools } = setup()
    inbound(db, chat.id, "run_1", [{ kind: "image", mimeType: "image/jpeg", data: JPG }])
    const { attachment } = await call(tools.save_attachment, { title: "KTP Zaki" })
    const otherTools = attachmentTools(db, { runId: "run_9", chatId: other.id, channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: NOW }, { key: () => KEY })

    expect(await call(tools.list_attachments, { query: "ktp" })).toMatchObject({ ok: true, count: 1, attachments: [{ id: attachment.id }] })
    expect(await call(otherTools.list_attachments, {})).toMatchObject({ ok: true, count: 0 })
    expect(await call(tools.get_attachment, { id: attachment.id })).toMatchObject({ ok: true, attachment: { title: "KTP Zaki", size: "6 B" } })
    expect(await call(otherTools.get_attachment, { id: attachment.id })).toMatchObject({ ok: false })
    expect(await call(otherTools.delete_attachment, { id: attachment.id })).toMatchObject({ ok: false })
    expect(await call(tools.delete_attachment, { id: attachment.id })).toMatchObject({ ok: true })
    expect(await call(tools.list_attachments, {})).toMatchObject({ count: 0 })
  })

  it("shares a file as a private link by default, public on request, and turns them off", async () => {
    const { db, chat, other, tools } = setup()
    inbound(db, chat.id, "run_1", [{ kind: "image", mimeType: "image/jpeg", data: JPG }])
    const { attachment } = await call(tools.save_attachment, { title: "Struk" })

    const priv = await call(tools.share_attachment, { id: attachment.id })
    expect(priv).toMatchObject({ ok: true, visibility: "private", expiresAt: new Date(NOW + 24 * 3600_000).toISOString() })
    expect(priv.url).toMatch(/^https:\/\/palui\.test\/api\/share\/[A-Za-z0-9_-]{32}$/)
    const pub = await call(tools.share_attachment, { id: attachment.id, visibility: "public", expires: "1h" })
    const token = pub.url.split("/").pop()
    expect(resolveShare(db, token, NOW)).toMatchObject({ ok: true, share: { visibility: "public", createdBy: chat.id } })

    const otherTools = attachmentTools(db, { runId: "run_9", chatId: other.id, channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: NOW }, { key: () => KEY })
    expect(await call(otherTools.share_attachment, { id: attachment.id })).toMatchObject({ ok: false })
    expect(await call(otherTools.unshare_attachment, { id: attachment.id })).toMatchObject({ ok: false })

    expect(await call(tools.unshare_attachment, { id: attachment.id })).toMatchObject({ ok: true, revoked: 2 })
    expect(resolveShare(db, token, NOW)).toEqual({ ok: false, reason: "revoked" })
  })
})
