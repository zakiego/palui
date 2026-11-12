import { describe, expect, it } from "vitest"
import { setup, NOW } from "../reminders/test-helpers"
import { AttachmentRefError, attachmentRef, findAttachmentRefs, resolveAttachmentRefs } from "./refs"
import { saveAttachment } from "./service"

const KEY = "test-master-key-0123456789abcdef0123456789"
const JPEG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46])

describe("attachment refs", () => {
  it("swaps a ref anywhere in the args for the file's data URL", () => {
    const { db, chatId } = setup()
    const { row } = saveAttachment(db, { chatId, title: "Struk laundry", mimeType: "image/jpeg", bytes: JPEG }, KEY, NOW)
    const ref = attachmentRef(row.id)
    const args = { file: ref, name: "Struk", nested: { files: [ref, "keep me"] }, note: `see ${ref}`, n: 3 }

    expect(findAttachmentRefs(args)).toEqual([row.id])
    const out = resolveAttachmentRefs(db, args, { chatId, masterKey: KEY })
    const url = `data:image/jpeg;base64,${JPEG.toString("base64")}`
    expect(out).toEqual({ file: url, name: "Struk", nested: { files: [url, "keep me"] }, note: `see ${ref}`, n: 3 })
    // The model's args stay untouched, so logs and approvals keep the short ref.
    expect(args.file).toBe(ref)
  })

  it("leaves args without refs alone", () => {
    const { db, chatId } = setup()
    const args = { attachmentId: "att_from_another_app", ids: ["a"] }
    expect(resolveAttachmentRefs(db, args, { chatId, masterKey: KEY })).toBe(args)
  })

  it("refuses a file saved in another chat or one that doesn't exist", () => {
    const { db, chatId, otherChatId } = setup()
    const { row } = saveAttachment(db, { chatId: otherChatId, title: "KTP", mimeType: "image/jpeg", bytes: JPEG }, KEY, NOW)
    expect(() => resolveAttachmentRefs(db, { file: attachmentRef(row.id) }, { chatId, masterKey: KEY })).toThrow(AttachmentRefError)
    expect(() => resolveAttachmentRefs(db, { file: attachmentRef("att_missing") }, { chatId, masterKey: KEY })).toThrow(/No saved attachment att_missing/)
  })
})
