import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { memoryDb, outbox, outboundMessages } from "@palui/db"
import { ensureChat } from "@palui/core/chats"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { addContact, listContacts, setOutboundEnabled, updateContact } from "@palui/core/outbound/service"
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
      expect(Object.keys(tools()).sort()).toEqual(["daftar_kontak", "daftar_pesan_keluar"])
    }
    expect(Object.keys(setup().tools()).sort()).toEqual(["batal_pesan", "daftar_kontak", "daftar_pesan_keluar", "kirim_pesan"])
  })
})

describe("approval policy", () => {
  it("always asks for an ask contact, and for a new number", () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const t = tools().kirim_pesan!
    expect(t.needsApproval!(otw)).toBe(true)
    expect(t.needsApproval!({ to: "081111111111", name: "Budi", text: "halo" })).toBe(true)
  })

  it("skips approval for an allow contact only when the owner named it in an untainted turn", () => {
    const named = setup()
    const c = addContact(named.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    updateContact(named.db, c.id, { policy: "allow" }, NOW)
    expect(named.tools().kirim_pesan!.needsApproval!(otw)).toBe(false)

    const tainted = setup({}, true)
    updateContact(tainted.db, addContact(tainted.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW).id, { policy: "allow" }, NOW)
    expect(tainted.tools().kirim_pesan!.needsApproval!(otw)).toBe(true)

    // The owner didn't name Zaki: the name came from somewhere else (recall, a page).
    const unnamed = setup({ ownerText: "kabarin dia aja" })
    updateContact(unnamed.db, addContact(unnamed.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW).id, { policy: "allow" }, NOW)
    expect(unnamed.tools().kirim_pesan!.needsApproval!(otw)).toBe(true)

    // A resumed turn has no owner text: ask.
    const resumed = setup({ ownerText: undefined, trigger: "approval" })
    updateContact(resumed.db, addContact(resumed.db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW).id, { policy: "allow" }, NOW)
    expect(resumed.tools().kirim_pesan!.needsApproval!(otw)).toBe(true)
  })

  it("previews the recipient, the time and the exact text", () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const t = tools().kirim_pesan!
    expect(t.preview!(otw)).toBe("Kirim pesan ke Zaki (+62 822-0000-1001)\nKapan: sekarang (ditahan 15 detik, bisa dibatalin)\nIsi pesan:\nHalo, ini asisten Zaki. Zaki lagi otw ya.")
    expect(t.preview!({ to: "081111111111", name: "Budi", text: "halo", localDateTime: "2026-11-10T20:00" })).toBe(
      "Kirim pesan ke Budi (+62 811-1111-1111), kontak baru\nKapan: Hari ini, 20.00 WITA\nIsi pesan:\nhalo",
    )
    expect(t.preview!({ to: "Zaki", text: "kabar", localDateTime: "2026-11-13T11:00", rrule: "FREQ=WEEKLY;BYDAY=FR" })).toContain("Kapan: Tiap Jumat")
  })
})

describe("kirim_pesan", () => {
  it("sends now to a saved contact with a hold and a cancel notice", async () => {
    const { db, chat, tools } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = await call(tools().kirim_pesan, otw)
    expect(r.ok).toBe(true)
    const row = db.select().from(outbox).where(eq(outbox.source, `outbound:${r.id}`)).get()!
    expect(row).toMatchObject({ chatId: c.chatId, nextAttemptAt: NOW + 15_000 })
    expect(row.payload.text).toBe(otw.text)
    const notice = db.select().from(outbox).where(eq(outbox.source, `outbound-notice:${r.id}`)).get()!
    expect(notice.chatId).toBe(chat.id)
    expect(db.select().from(outboundMessages).get()).toMatchObject({ approvedBy: "policy", origin: "chat", ownerChatId: chat.id })
  })

  it("saves a new number only in an approved run", async () => {
    const plain = setup()
    expect(await call(plain.tools().kirim_pesan, { to: "082200001001", name: "Zaki", text: "otw" })).toEqual({ ok: false, error: "A new number needs the owner's approval first." })
    expect(listContacts(plain.db)).toHaveLength(0)

    const approved = setup({ trigger: "approval", ownerText: undefined })
    const r = await call(approved.tools().kirim_pesan, { to: "082200001001", name: "Zaki", text: "otw" })
    expect(r).toMatchObject({ ok: true, newContact: true })
    expect(listContacts(approved.db)).toMatchObject([{ name: "Zaki", address: "6282200001001", policy: "ask" }])
    expect(approved.db.select().from(outboundMessages).get()!.approvedBy).toBe("owner")
  })

  it("refuses unknown names and numbers without a name", async () => {
    const { tools } = setup()
    expect((await call(tools().kirim_pesan, { to: "Budi", text: "x" })).error).toMatch(/No saved contact named Budi/)
    expect((await call(tools().kirim_pesan, { to: "082200001001", text: "x" })).error).toMatch(/Ask the owner for the person's name/)
  })

  it("schedules a one-off and a repeat", async () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const later = await call(tools().kirim_pesan, { ...otw, localDateTime: "2026-11-10T20:00" })
    expect(later.message).toBe("Oke, pesan ke Zaki dijadwalin: Hari ini, 20.00 WITA.")
    const weekly = await call(tools().kirim_pesan, { to: "Zaki", text: "kabar", localDateTime: "2026-11-13T11:00", rrule: "FREQ=WEEKLY;BYDAY=FR" })
    expect(weekly.message).toContain("Tiap Jumat")
    expect(db.select().from(outbox).all().filter((r) => r.source.startsWith("outbound:"))).toHaveLength(0)
  })

  it("refuses when the kill switch is off", async () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    setOutboundEnabled(db, false, NOW)
    expect((await call(tools().kirim_pesan, otw)).error).toMatch(/turned off/)
  })

  it("cancels a held send", async () => {
    const { db, tools } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = await call(tools().kirim_pesan, otw)
    expect(await call(tools().batal_pesan, { id: r.id })).toEqual({ ok: true, message: "Oke, pesan ke Zaki dibatalin." })
  })
})

describe("per agent", () => {
  it("another agent's turn can't see or message this agent's contacts", async () => {
    const { db, chat } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    updateContact(db, c.id, { policy: "allow" }, NOW)
    const other: TurnInfo = { runId: "run_2", chatId: chat.id, channel: "telegram", trigger: "message", tz: WITA, now: NOW, origin: "message", mode: "owner", agentId: "agt_other", tainted: () => false, ownerText: "chat Zaki" }
    const tools = outboundTools(db, other, () => NOW)
    expect((await call(tools.daftar_kontak, {})).contacts).toEqual([])
    expect((await call(tools.kirim_pesan, otw)).error).toMatch(/No saved contact named Zaki/)
  })
})
