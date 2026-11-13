import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { chats, memoryDb, outbox, outboundMessages } from "@palui/db"
import { createAgent, deleteAgent } from "../agents"
import { ensureChat } from "../chats"
import { fakeChannel, seedConnector } from "../chat/testing"
import { silentLogger } from "../log"
import { createOutboxWorker } from "../outbox/worker"
import { setSetting, DEFAULT_SETTINGS } from "../settings"
import { NOW, WITA, MIN, HOUR } from "../reminders/test-helpers"
import { formatPhone, normalizePhone, phonesIn } from "./phone"
import { createOutboundScheduler } from "./scheduler"
import {
  addContact,
  cancelOutbound,
  findContact,
  heldFromNotice,
  holdNotice,
  listContacts,
  mentionsContact,
  newestHeld,
  OutboundError,
  removeContact,
  scheduleSend,
  sendNow,
  setOutboundEnabled,
  updateContact,
} from "./service"

const AGENT = "agt_test"

function setup() {
  const db = memoryDb()
  const owner = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  return { db, owner }
}

const meta = (ownerChatId: string) => ({ origin: "chat" as const, approvedBy: "owner" as const, ownerChatId })

describe("phone numbers", () => {
  it("normalizes Indonesian local and international forms", () => {
    expect(normalizePhone("082200001001")).toBe("6282200001001")
    expect(normalizePhone("0822 0000-1001")).toBe("6282200001001")
    expect(normalizePhone("+62 822 0000 1001")).toBe("6282200001001")
    expect(normalizePhone("6282200001001")).toBe("6282200001001")
    expect(normalizePhone("+62 0822 0000 1001")).toBe("6282200001001")
    expect(normalizePhone("82200001001")).toBe("6282200001001")
    expect(normalizePhone("+1 415 555 0100")).toBe("14155550100")
    expect(normalizePhone("0065 9123 4567")).toBe("6591234567")
  })

  it("refuses names, short numbers and ambiguous country codes", () => {
    expect(normalizePhone("Zaki")).toBeNull()
    expect(normalizePhone("0812")).toBeNull()
    expect(normalizePhone("14155550100")).toBeNull()
  })

  it("formats and finds numbers in text", () => {
    expect(formatPhone("6282200001001")).toBe("+62 822-0000-1001")
    expect(phonesIn("chat Zaki (082200001001) sekarang")).toEqual(["6282200001001"])
  })
})

describe("contacts", () => {
  it("adds a number as a chat Palui won't answer, and finds it by name or number", () => {
    const { db } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    expect(c).toMatchObject({ channel: "whatsapp", address: "6282200001001", policy: "ask" })
    const chat = db.select().from(chats).where(eq(chats.id, c.chatId)).get()!
    expect(chat).toMatchObject({ externalId: "6282200001001@s.whatsapp.net", allowed: false, title: "Zaki" })
    expect(findContact(db, AGENT, "zaki")?.id).toBe(c.id)
    expect(findContact(db, AGENT, "+62 822 0000 1001")?.id).toBe(c.id)
    expect(findContact(db, AGENT, "Budi")).toBeUndefined()
  })

  it("keeps an existing chat's settings", () => {
    const { db } = setup()
    const existing = ensureChat(db, { channel: "whatsapp", externalId: "6281111111111@s.whatsapp.net", title: "Ibu", allowed: true }, NOW)
    const c = addContact(db, { agentId: AGENT, name: "Ibu", phone: "081111111111" }, NOW)
    expect(c.chatId).toBe(existing.id)
    expect(db.select().from(chats).where(eq(chats.id, existing.id)).get()!.allowed).toBe(true)
  })

  it("refuses duplicate names and numbers, groups and the web chat", () => {
    const { db } = setup()
    addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    expect(() => addContact(db, { agentId: AGENT, name: "zaki", phone: "081111111111" }, NOW)).toThrow(OutboundError)
    expect(() => addContact(db, { agentId: AGENT, name: "Other", phone: "6282200001001" }, NOW)).toThrow(/already saved as Zaki/)
    const group = ensureChat(db, { channel: "telegram", externalId: "-100", kind: "group" }, NOW)
    expect(() => addContact(db, { agentId: AGENT, name: "Grup", chatId: group.id }, NOW)).toThrow(/groups/)
    const web = ensureChat(db, { channel: "web", externalId: "web" }, NOW)
    expect(() => addContact(db, { agentId: AGENT, name: "Web", chatId: web.id }, NOW)).toThrow(/web chat/)
    expect(() => addContact(db, { agentId: AGENT, name: "Bad", phone: "12345" }, NOW)).toThrow(/isn't a phone number/)
  })

  it("adds a Telegram contact from an existing DM", () => {
    const { db } = setup()
    const dm = ensureChat(db, { channel: "telegram", externalId: "777", title: "Budi" }, NOW)
    const c = addContact(db, { agentId: AGENT, name: "Budi", chatId: dm.id }, NOW)
    expect(c).toMatchObject({ channel: "telegram", address: "777", chatId: dm.id })
  })

  it("matches the owner's words by whole name or number", () => {
    const c = { name: "Zaki", channel: "whatsapp" as const, address: "6282200001001" }
    expect(mentionsContact(c, "chat Zaki sekarang")).toBe(true)
    expect(mentionsContact(c, "kabarin zaki ya")).toBe(true)
    expect(mentionsContact(c, "chat 0822-0000-1001")).toBe(true)
    expect(mentionsContact(c, "chat Zakiyah sekarang")).toBe(false)
    expect(mentionsContact(c, "chat dia sekarang")).toBe(false)
    expect(mentionsContact(c, undefined)).toBe(false)
  })
})

describe("sending", () => {
  it("holds a now send for the undo window and can cancel it", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = sendNow(db, c, "Halo, ini asisten Zaki. Dia lagi otw.", { ...meta(owner.id), holdMs: 15_000 }, NOW)
    const row = db.select().from(outbox).where(eq(outbox.id, r.outboxId)).get()!
    expect(row).toMatchObject({ chatId: c.chatId, source: `outbound:${r.row.id}`, status: "pending", nextAttemptAt: NOW + 15_000 })
    expect(row.payload.text).toBe("Halo, ini asisten Zaki. Dia lagi otw.")
    expect(newestHeld(db, owner.id, NOW + 1000)?.id).toBe(r.row.id)
    expect(cancelOutbound(db, r.row.id, NOW + 1000).ok).toBe(true)
    expect(db.select().from(outbox).where(eq(outbox.id, r.outboxId)).get()!.status).toBe("cancelled")
    expect(newestHeld(db, owner.id, NOW + 2000)).toBeUndefined()
    expect(cancelOutbound(db, r.row.id, NOW + 2000).ok).toBe(false)
  })

  it("appends the signature", () => {
    const { db, owner } = setup()
    setSetting(db, "outbound", { ...DEFAULT_SETTINGS.outbound, signature: "(dikirim asisten Zaki)" })
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 0 }, NOW)
    expect(r.row.text).toBe("otw\n\n(dikirim asisten Zaki)")
  })

  it("enforces per-contact and total limits over a rolling day", () => {
    const { db, owner } = setup()
    setSetting(db, "outbound", { ...DEFAULT_SETTINGS.outbound, perContactPerDay: 2, perDay: 3 })
    const a = addContact(db, { agentId: AGENT, name: "A", phone: "081111111111" }, NOW)
    const b = addContact(db, { agentId: AGENT, name: "B", phone: "082222222222" }, NOW)
    sendNow(db, a, "1", { ...meta(owner.id), holdMs: 0 }, NOW)
    const second = sendNow(db, a, "2", { ...meta(owner.id), holdMs: 15_000 }, NOW)
    expect(() => sendNow(db, a, "3", { ...meta(owner.id), holdMs: 0 }, NOW)).toThrow(/2 messages to A/)
    // A cancelled send doesn't count.
    cancelOutbound(db, second.row.id, NOW)
    sendNow(db, a, "3", { ...meta(owner.id), holdMs: 0 }, NOW)
    sendNow(db, b, "1", { ...meta(owner.id), holdMs: 0 }, NOW)
    expect(() => sendNow(db, b, "2", { ...meta(owner.id), holdMs: 0 }, NOW)).toThrow(/3 messages to contacts/)
    // A day later the window has moved on.
    sendNow(db, b, "2", { ...meta(owner.id), holdMs: 0 }, NOW + 24 * HOUR + MIN)
  })

  it("kill switch refuses sends and cancels waiting ones", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 15_000 }, NOW)
    expect(setOutboundEnabled(db, false, NOW).cancelled).toBe(1)
    expect(db.select().from(outbox).where(eq(outbox.id, r.outboxId)).get()!.status).toBe("cancelled")
    expect(() => sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 0 }, NOW)).toThrow(/turned off/)
    expect(() => scheduleSend(db, c, "otw", { localStart: "2026-11-10T20:00", tz: WITA }, meta(owner.id), NOW)).toThrow(/turned off/)
    setOutboundEnabled(db, true, NOW)
    expect(() => sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 0 }, NOW)).not.toThrow()
  })

  it("removing a contact cancels its scheduled and held sends", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const held = sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 15_000 }, NOW)
    const later = scheduleSend(db, c, "malam", { localStart: "2026-11-10T20:00", tz: WITA }, meta(owner.id), NOW)
    expect(removeContact(db, c.id, NOW).cancelled).toBe(2)
    expect(db.select().from(outbox).where(eq(outbox.id, held.outboxId)).get()!.status).toBe("cancelled")
    expect(db.select().from(outboundMessages).where(eq(outboundMessages.id, later.id)).get()!.status).toBe("cancelled")
    expect(listContacts(db)).toHaveLength(0)
    expect(() => sendNow(db, c, "x", { ...meta(owner.id), holdMs: 0 }, NOW)).toThrow(/removed/)
  })

  it("refuses a time already past today instead of rolling to tomorrow", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    // NOW is 10:00 WITA.
    expect(() => scheduleSend(db, c, "pagi", { localStart: "2026-11-10T08:00", tz: WITA }, meta(owner.id), NOW)).toThrow(/already passed/)
    const r = scheduleSend(db, c, "malam", { localStart: "2026-11-10T20:00", tz: WITA }, meta(owner.id), NOW)
    expect(r.fireAt).toBe(NOW + 10 * HOUR)
  })

  it("finds a held send from a reply quoting its notice", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 15_000 }, NOW)
    holdNotice(db, r.row, c, r.sendAt, NOW)
    const notice = db.select().from(outbox).where(eq(outbox.source, `outbound-notice:${r.row.id}`)).get()!
    expect(notice.chatId).toBe(owner.id)
    expect(notice.payload.text).toContain("15 detik")
    expect(notice.payload.actions).toEqual([{ id: `ob:cancel:${r.row.id}`, label: "Batalin" }])
    db.update(outbox).set({ channelMsgId: "m1" }).where(eq(outbox.id, notice.id)).run()
    expect(heldFromNotice(db, owner.id, "m1")).toBe(r.row.id)
  })

  it("updates a contact's policy", () => {
    const { db } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    expect(updateContact(db, c.id, { policy: "allow" }, NOW).policy).toBe("allow")
  })
})

describe("outbound scheduler", () => {
  it("fires a one-off once at its time", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const ext = createOutboundScheduler({ db, log: silentLogger })
    const r = scheduleSend(db, c, "malam", { localStart: "2026-11-10T20:00", tz: WITA }, meta(owner.id), NOW)
    expect(ext.nextDue()).toBe(r.fireAt)
    expect(ext.sweep(r.fireAt - 1)).toBe(0)
    expect(ext.sweep(r.fireAt)).toBe(1)
    expect(ext.sweep(r.fireAt + 1000)).toBe(0)
    const sent = db.select().from(outbox).where(eq(outbox.source, `outbound:${r.id}`)).all()
    expect(sent).toHaveLength(1)
    expect(sent[0]!.chatId).toBe(c.chatId)
    expect(db.select().from(outboundMessages).where(eq(outboundMessages.id, r.id)).get()!.status).toBe("done")
  })

  it("moves a series to its next occurrence", () => {
    const { db, owner } = setup()
    const c = addContact(db, { agentId: AGENT, name: "X", phone: "082200001001" }, NOW)
    const ext = createOutboundScheduler({ db, log: silentLogger })
    // Friday 2026-11-13 11:00 WITA, weekly.
    const r = scheduleSend(db, c, "kabar", { localStart: "2026-11-13T11:00", tz: WITA, rrule: "FREQ=WEEKLY;BYDAY=FR" }, meta(owner.id), NOW)
    ext.sweep(r.fireAt)
    const after = db.select().from(outboundMessages).where(eq(outboundMessages.id, r.id)).get()!
    expect(after.status).toBe("scheduled")
    expect(after.fireAt).toBe(r.fireAt + 7 * 24 * HOUR)
  })

  it("skips an occurrence over the limit or long overdue and tells the owner", () => {
    const { db, owner } = setup()
    setSetting(db, "outbound", { ...DEFAULT_SETTINGS.outbound, perContactPerDay: 1 })
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const ext = createOutboundScheduler({ db, log: silentLogger })
    sendNow(db, c, "pagi", { ...meta(owner.id), holdMs: 0 }, NOW)
    const r = scheduleSend(db, c, "malam", { localStart: "2026-11-10T20:00", tz: WITA }, meta(owner.id), NOW)
    ext.sweep(r.fireAt)
    expect(db.select().from(outbox).where(eq(outbox.source, `outbound:${r.id}`)).all()).toHaveLength(0)
    const notices = db.select().from(outbox).where(eq(outbox.chatId, owner.id)).all()
    expect(notices.at(-1)!.payload.text).toContain("nggak aku kirim")

    const late = scheduleSend(db, c, "late", { localStart: "2026-11-11T20:00", tz: WITA }, meta(owner.id), NOW)
    ext.sweep(late.fireAt + 31 * MIN)
    expect(db.select().from(outbox).where(eq(outbox.source, `outbound:${late.id}`)).all()).toHaveLength(0)
    expect(db.select().from(outboundMessages).where(eq(outboundMessages.id, late.id)).get()!.status).toBe("done")
  })
})

describe("outbox worker and outbound rows", () => {
  it("cancels at send time when the kill switch is off, and never reroutes to the owner", async () => {
    const db = memoryDb()
    seedConnector(db, "whatsapp", "channel")
    const wa = fakeChannel("whatsapp")
    const owner = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    const r = sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 0 }, NOW)
    setSetting(db, "outbound", { ...DEFAULT_SETTINGS.outbound, enabled: false })
    const worker = createOutboxWorker({ db, channel: (ch) => (ch === "whatsapp" ? wa : undefined), log: silentLogger, clock: () => NOW })
    await worker.tick()
    expect(wa.sent).toHaveLength(0)
    expect(db.select().from(outbox).where(eq(outbox.id, r.outboxId)).get()!.status).toBe("cancelled")
  })

  it("sends to the contact's WhatsApp JID", async () => {
    const db = memoryDb()
    seedConnector(db, "whatsapp", "channel")
    const wa = fakeChannel("whatsapp")
    const owner = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
    const c = addContact(db, { agentId: AGENT, name: "Zaki", phone: "082200001001" }, NOW)
    sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 0 }, NOW)
    const worker = createOutboxWorker({ db, channel: (ch) => (ch === "whatsapp" ? wa : undefined), log: silentLogger, clock: () => NOW })
    await worker.tick()
    expect(wa.sent).toEqual([{ to: "6282200001001@s.whatsapp.net", payload: { text: "otw", format: "plain" } }])
  })
})

describe("contacts per agent", () => {
  it("keeps each agent's list apart, and the same number can be in both", () => {
    const { db } = setup()
    const a = addContact(db, { agentId: "agt_a", name: "Zaki", phone: "082200001001" }, NOW)
    const b = addContact(db, { agentId: "agt_b", name: "Zaki", phone: "082200001001" }, NOW)
    expect(a.chatId).toBe(b.chatId)
    expect(findContact(db, "agt_a", "Zaki")?.id).toBe(a.id)
    expect(findContact(db, "agt_b", "Zaki")?.id).toBe(b.id)
    expect(findContact(db, "agt_c", "Zaki")).toBeUndefined()
    expect(listContacts(db, "agt_a")).toHaveLength(1)
    expect(listContacts(db)).toHaveLength(2)
  })

  it("counts the per-contact limit per agent's contact, and the daily total across agents", () => {
    const { db, owner } = setup()
    setSetting(db, "outbound", { ...DEFAULT_SETTINGS.outbound, perContactPerDay: 5, perDay: 2 })
    const a = addContact(db, { agentId: "agt_a", name: "Zaki", phone: "082200001001" }, NOW)
    const b = addContact(db, { agentId: "agt_b", name: "Zaki", phone: "082200001001" }, NOW)
    sendNow(db, a, "1", { ...meta(owner.id), holdMs: 0 }, NOW)
    sendNow(db, b, "1", { ...meta(owner.id), holdMs: 0 }, NOW)
    expect(() => sendNow(db, a, "2", { ...meta(owner.id), holdMs: 0 }, NOW)).toThrow(/2 messages to contacts/)
  })

  it("deleting an agent removes its contacts and cancels what's waiting", () => {
    const { db, owner } = setup()
    const agent = createAgent(db, { slug: "arisan", name: "Arisan", persona: "x" }, NOW)
    const c = addContact(db, { agentId: agent.id, name: "Zaki", phone: "082200001001" }, NOW)
    const held = sendNow(db, c, "otw", { ...meta(owner.id), holdMs: 15_000 }, NOW)
    const later = scheduleSend(db, c, "malam", { localStart: "2026-11-10T20:00", tz: WITA }, meta(owner.id), NOW)
    expect(held.row.agentId).toBe(agent.id)
    deleteAgent(db, agent.id)
    expect(listContacts(db, agent.id)).toHaveLength(0)
    expect(db.select().from(outbox).where(eq(outbox.id, held.outboxId)).get()!.status).toBe("cancelled")
    expect(db.select().from(outboundMessages).where(eq(outboundMessages.id, later.id)).get()!.status).toBe("cancelled")
  })
})
