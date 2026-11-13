import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { memoryDb, outbox, outboundMessages } from "@palui/db"
import { ensureChat } from "@palui/core/chats"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { addContact, listContacts, setOutboundEnabled, updateContact } from "@palui/core/outbound/service"
import { DEFAULT_OUTBOUND_SIGNATURE as SIG } from "@palui/core/settings"
import manifest from "./manifest"
import { outboundTools } from "./runtime"

// Tuesday 2026-11-10 10:00 WITA.
const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"

function setup(turn: Partial<TurnInfo> = {}, tainted = false) {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const t: TurnInfo = { runId: "run_1", chatId: chat.id, channel: "telegram", trigger: "message", tz: WITA, now: NOW, origin: "message", mode: "owner", agentId: AGENT, tainted: () => tainted, ownerText: "chat Zaki sekarang, bilang gue otw", ...turn }
  return { db, chat, turn: t, tools: () => outboundTools(db, t, () => NOW) }
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

const AGENT = "agt_palui"

const otw = { to: "Zaki", text: "Halo, ini asisten Zaki. Zaki lagi otw ya." }

describe("outbound manifest", () => {
  it("is a builtin skill", () => {
    expect(manifest).toMatchObject({ id: "outbound", kind: "skill", builtin: true })
  })
})

describe("who gets the send tools", () => {
  it("only turns the owner started in his own chat", () => {
    for (const turn of [{ origin: "flow" as const }, { origin: "schedule" as const }, { origin: "hook" as const }, { origin: "reminder" as const }, { mode: "guest" as const }, { mode: "sharedOwner" as const }, { origin: undefined }]) {
      const { tools } = setup(turn)
      expect(Object.keys(tools()).sort()).toEqual(["list_contacts", "list_outbound_messages"])
    }
    expect(Object.keys(setup().tools()).sort()).toEqual(["add_contact", "cancel_message", "list_contacts", "list_outbound_messages", "send_message"])
  })
})

describe("approval policy", () => {
  it("always asks for an ask contact, and for a new number", () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const t = tools().send_message!
    expect(t.needsApproval!(otw)).toBe(true)
    expect(t.needsApproval!({ to: "081111111111", name: "Budi", text: "halo" })).toBe(true)
  })

  it("skips approval for an allow contact only when the owner named it in an untainted turn", () => {
    const named = setup()
    const c = addContact(named.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    updateContact(named.db, c.id, { policy: "allow" }, NOW)
    expect(named.tools().send_message!.needsApproval!(otw)).toBe(false)

    const tainted = setup({}, true)
    updateContact(tainted.db, addContact(tainted.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW).id, { policy: "allow" }, NOW)
    expect(tainted.tools().send_message!.needsApproval!(otw)).toBe(true)

    // The owner didn't name Zaki: the name came from somewhere else (recall, a page).
    const unnamed = setup({ ownerText: "kabarin dia aja" })
    updateContact(unnamed.db, addContact(unnamed.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW).id, { policy: "allow" }, NOW)
    expect(unnamed.tools().send_message!.needsApproval!(otw)).toBe(true)

    // A resumed turn has no owner text: ask.
    const resumed = setup({ ownerText: undefined, trigger: "approval" })
    updateContact(resumed.db, addContact(resumed.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW).id, { policy: "allow" }, NOW)
    expect(resumed.tools().send_message!.needsApproval!(otw)).toBe(true)
  })

  it("previews the recipient, the time and the exact text", () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const t = tools().send_message!
    expect(t.preview!(otw)).toBe(`Kirim pesan ke Zaki (+62 822-0000-1001)\nKapan: sekarang (ditahan 15 detik, bisa dibatalin)\nIsi pesan:\nHalo, ini asisten Zaki. Zaki lagi otw ya.\n\n${SIG}`)
    expect(t.preview!({ to: "081111111111", name: "Budi", text: "halo", localDateTime: "2026-11-10T20:00" })).toBe(
      `Kirim pesan ke Budi (+62 811-1111-1111), kontak baru\nKapan: Hari ini, 20.00 WITA\nIsi pesan:\nhalo\n\n${SIG}`,
    )
    expect(t.preview!({ to: "Zaki", text: "kabar", localDateTime: "2026-11-13T11:00", rrule: "FREQ=WEEKLY;BYDAY=FR" })).toContain("Kapan: Tiap Jumat")
  })
})

describe("send_message", () => {
  it("sends now to a saved contact with a hold and a cancel notice", async () => {
    const { db, chat, tools } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = await call(tools().send_message, otw)
    expect(r.ok).toBe(true)
    const row = db.select().from(outbox).where(eq(outbox.source, `outbound:${r.id}`)).get()!
    expect(row).toMatchObject({ chatId: c.chatId, nextAttemptAt: NOW + 15_000 })
    expect(row.payload.text).toBe(`${otw.text}\n\n${SIG}`)
    const notice = db.select().from(outbox).where(eq(outbox.source, `outbound-notice:${r.id}`)).get()!
    expect(notice.chatId).toBe(chat.id)
    expect(notice.payload.text).toContain(`${otw.text}\n\n${SIG}`)
    expect(db.select().from(outboundMessages).get()).toMatchObject({ approvedBy: "policy", origin: "chat", ownerChatId: chat.id })
  })

  it("saves a new number only in an approved run", async () => {
    const plain = setup()
    expect(await call(plain.tools().send_message, { to: "082200001001", name: "Zaki", text: "otw" })).toEqual({ ok: false, error: "A new number needs the owner's approval first." })
    expect(listContacts(plain.db)).toHaveLength(0)

    const approved = setup({ trigger: "approval", ownerText: undefined })
    const r = await call(approved.tools().send_message, { to: "082200001001", name: "Zaki", text: "otw" })
    expect(r).toMatchObject({ ok: true, newContact: true })
    expect(listContacts(approved.db)).toMatchObject([{ name: "Zaki", address: "6282200001001", policy: "ask" }])
    expect(approved.db.select().from(outboundMessages).get()!.approvedBy).toBe("owner")
  })

  it("refuses unknown names and numbers without a name", async () => {
    const { tools } = setup()
    expect((await call(tools().send_message, { to: "Budi", text: "x" })).error).toMatch(/No saved contact named Budi/)
    expect((await call(tools().send_message, { to: "082200001001", text: "x" })).error).toMatch(/Ask the owner for the person's name/)
  })

  it("schedules a one-off and a repeat", async () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const later = await call(tools().send_message, { ...otw, localDateTime: "2026-11-10T20:00" })
    expect(later.message).toBe("Oke, pesan ke Zaki dijadwalin: Hari ini, 20.00 WITA.")
    const weekly = await call(tools().send_message, { to: "Zaki", text: "kabar", localDateTime: "2026-11-13T11:00", rrule: "FREQ=WEEKLY;BYDAY=FR" })
    expect(weekly.message).toContain("Tiap Jumat")
    expect(db.select().from(outbox).all().filter((r) => r.source.startsWith("outbound:"))).toHaveLength(0)
  })

  it("refuses when the kill switch is off", async () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    setOutboundEnabled(db, false, NOW)
    expect((await call(tools().send_message, otw)).error).toMatch(/turned off/)
  })

  it("cancels a held send", async () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = await call(tools().send_message, otw)
    expect(await call(tools().cancel_message, { id: r.id })).toEqual({ ok: true, message: "Oke, pesan ke Zaki dibatalin." })
  })
})

describe("per agent", () => {
  it("another agent's turn can't see or message this agent's contacts", async () => {
    const { db, chat } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    updateContact(db, c.id, { policy: "allow" }, NOW)
    const other: TurnInfo = { runId: "run_2", chatId: chat.id, channel: "telegram", trigger: "message", tz: WITA, now: NOW, origin: "message", mode: "owner", agentId: "agt_other", tainted: () => false, ownerText: "chat Zaki" }
    const tools = outboundTools(db, other, () => NOW)
    expect((await call(tools.list_contacts, {})).contacts).toEqual([])
    expect((await call(tools.send_message, otw)).error).toMatch(/No saved contact named Zaki/)
  })
})

describe("add_contact", () => {
  const ana = { name: "Ana", phone: "+62 822-0000-1002" }
  const approvedRun = () => setup({ trigger: "approval", ownerText: undefined })

  it("normalizes the number the same way the dashboard form does", async () => {
    for (const phone of ["082200001002", "0822 0000 1002", "0822-0000-1002", "+62 822-0000-1002", "+6282200001002", "6282200001002"]) {
      const { db, tools } = approvedRun()
      const r = await call(tools().add_contact, { name: "Ana", phone })
      expect(r).toMatchObject({ ok: true, existing: false })
      expect(listContacts(db)).toMatchObject([{ name: "Ana", channel: "whatsapp", address: "6282200001002" }])
    }
    expect((await call(approvedRun().tools().add_contact, { name: "Ana", phone: "12345" })).error).toMatch(/isn't a phone number/)
  })

  it("always asks, even with allow-all permissions or a tainted turn, and previews the normalized number", () => {
    const t = setup().tools().add_contact!
    expect(t.class).toBe("write")
    expect(t.needsApproval!(ana)).toBe(true)
    expect(t.needsApproval!({ ...ana, policy: "allow" })).toBe(true)
    expect(setup({}, true).tools().add_contact!.needsApproval!(ana)).toBe(true)
    expect(t.preview!({ ...ana, note: "istri" })).toBe("Tambah kontak Ana (+62 822-0000-1002)\nCatatan: istri\nPersetujuan kirim: selalu tanya")
  })

  it("saves nothing until the owner approves", async () => {
    const { db, tools } = setup()
    expect(await call(tools().add_contact, ana)).toEqual({ ok: false, error: "Adding a contact needs the owner's approval first." })
    expect(listContacts(db)).toHaveLength(0)
  })

  it("only exists in turns the owner started, so flows, hooks and guests can't add contacts", () => {
    for (const turn of [{ origin: "flow" as const, trigger: "flow" as const }, { origin: "hook" as const }, { origin: "schedule" as const }, { mode: "guest" as const }]) {
      expect(setup(turn).tools().add_contact).toBeUndefined()
    }
  })

  it("new contacts always start at always ask, whatever the args say", async () => {
    const { db, tools } = approvedRun()
    await call(tools().add_contact, { ...ana, policy: "allow", note: "istri" })
    expect(listContacts(db)).toMatchObject([{ name: "Ana", policy: "ask", note: "istri", agentId: AGENT }])
  })

  it("returns an existing number instead of saving it twice", async () => {
    const { db, tools } = approvedRun()
    addContact(db, { agentId: AGENT, name: "Ana", phone: "082200001002" }, NOW)
    const t = tools().add_contact!
    expect(t.needsApproval!({ name: "Istri", phone: "0822 0000 1002" })).toBe(false)
    const r = await call(t, { name: "Istri", phone: "0822 0000 1002" })
    expect(r).toMatchObject({ ok: true, existing: true })
    expect(r.message).toMatch(/sudah ada sebagai Eva/)
    expect(listContacts(db)).toHaveLength(1)
  })

  it("says so when the name belongs to another number", async () => {
    const { db, tools } = approvedRun()
    addContact(db, { agentId: AGENT, name: "Ana", phone: "081111111111" }, NOW)
    const r = await call(tools().add_contact, { name: "ana", phone: "082200001002" })
    expect(r.ok).toBe(false)
    expect(r.error).toMatch(/Eva is already used by another number/)
    expect(listContacts(db)).toHaveLength(1)
  })

  it("saves to the calling agent", async () => {
    const { db, chat } = setup()
    const other: TurnInfo = { runId: "run_2", chatId: chat.id, channel: "telegram", trigger: "approval", tz: WITA, now: NOW, origin: "message", mode: "owner", agentId: "agt_other", tainted: () => false }
    await call(outboundTools(db, other, () => NOW).add_contact, ana)
    expect(listContacts(db, "agt_other")).toHaveLength(1)
    expect(listContacts(db, AGENT)).toHaveLength(0)
  })
})
