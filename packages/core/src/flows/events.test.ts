import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { outbox, outboundMessages } from "@palui/db"
import { ensureChat } from "../chats"
import { getDefaultAgent } from "../agents"
import { addContact, removeContact, setOutboundEnabled, updateContact } from "../outbound/service"
import { decideVersion } from "./service"
import { at, exampleConnectors, flowHarness, runsOf, sentTexts, stepLog } from "./test-harness"


describe("event triggers", () => {
  const ASK = {
    name: "Olahraga",
    triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "06:00", label: "pagi" }] } }],
    steps: [{ id: "ask", kind: "checkin", text: "Udah olahraga?", deadline: { kind: "after", minutes: 60 }, followUp: { everyMin: 30, max: 0 } }],
  }
  const STREAK = {
    name: "Streak",
    triggers: [{ id: "miss", kind: "event", event: "checkin.missed", flow: "Olahraga", start: "count" }],
    steps: [
      { id: "count", kind: "set", scope: "flow", values: { missed: "flow.missed + 1" } },
      { id: "warn", kind: "if", when: "flow.missed >= 2", then: "say", else: null },
      { id: "say", kind: "message", text: "Udah {{flow.missed}} kali kelewat ({{trigger.label}})." },
    ],
  }

  it("starts a listening flow when another flow's check-in is missed", async () => {
    const h = flowHarness()
    h.setNow(at("2026-11-10T20:00"))
    h.create(ASK)
    const { flow: streak } = h.create(STREAK)
    for (const day of ["11", "12"]) {
      await h.run(at(`2026-11-${day}T06:00`))
      await h.run(at(`2026-11-${day}T07:00`))
    }
    const runs = runsOf(h.db, streak.id)
    expect(runs.map((r) => [r.triggerKind, r.status])).toEqual([
      ["event", "done"],
      ["event", "done"],
    ])
    expect(runs[0]!.trigger).toMatchObject({ event: "checkin.missed", flow: "Olahraga", step: "ask", depth: 1, label: "pagi" })
    expect(sentTexts(h.db).at(-1)!.text).toBe("Udah 2 kali kelewat (pagi).")
  })

  it("stops an event chain deeper than 3", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Loop", triggers: [{ id: "again", kind: "event", event: "run.done", flow: "self", start: "m" }], steps: [{ id: "m", kind: "set", values: { x: "1" } }] })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "m0", payload: {}, now: h.now() })
    for (let i = 0; i < 6; i++) await h.run(h.now())
    const runs = runsOf(h.db, flow.id)
    expect(runs.map((r) => (r.trigger as { depth?: number }).depth ?? 0)).toEqual([0, 1, 2, 3, 4])
    expect(runs.at(-1)).toMatchObject({ status: "skipped", skipReason: "chain_depth" })
  })

  it("passes run.failed from one flow to another", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ reminders__list_reminders: () => { throw new Error("down") } }) })
    const { flow: a } = h.create({ name: "A", steps: [{ id: "t", kind: "tool", tool: "reminders__list_reminders", retry: { max: 0, backoffSec: 5 } }] })
    const { flow: b } = h.create({ name: "B", triggers: [{ id: "f", kind: "event", event: "run.failed", flow: "A", start: "m" }], steps: [{ id: "m", kind: "message", text: "A gagal: {{trigger.error}}" }] })
    h.engine.startRun({ flowId: a.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(runsOf(h.db, b.id)[0]).toMatchObject({ status: "done", trigger: { event: "run.failed", error: "Step t: down" } })
  })
})

describe("message.to another chat (RFC 5.3)", () => {
  it("needs the version approval, then posts to the allowed group", async () => {
    const h = flowHarness()
    const family = ensureChat(h.db, { channel: "telegram", externalId: "-1001", kind: "group", title: "Keluarga", allowed: true }, h.now())
    const saved = h.create({ name: "Kabar", steps: [{ id: "m", kind: "message", text: "Halo keluarga", to: family.id }] })
    expect(saved.needsApproval).toBe(true)
    expect(saved.writeCalls).toEqual([{ stepId: "m", kind: "message", to: family.id }])
    h.engine.startRun({ flowId: saved.flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(runsOf(h.db, saved.flow.id)[0]!.status).toBe("awaiting_approval")
    decideVersion(h.db, saved.version.id, true, "test", h.now())
    await h.run(h.now())
    expect(sentTexts(h.db).filter((s) => s.text === "Halo keluarga")).toHaveLength(1)
  })
})

describe("message.contact (docs/design/outbound-messages.md)", () => {
  it("needs the version approval even for an always-allow contact, then sends through the outbound log", async () => {
    const h = flowHarness()
    const zaki = addContact(h.db, { agentId: getDefaultAgent(h.db).id, name: "Zaki", phone: "082200001001" }, h.now())
    updateContact(h.db, zaki.id, { policy: "allow" }, h.now())
    const saved = h.create({ name: "Kabar Zaki", steps: [{ id: "m", kind: "message", text: "Zaki belum jawab check-in.", contact: "Zaki" }] })
    expect(saved.needsApproval).toBe(true)
    expect(saved.writeCalls).toEqual([{ stepId: "m", kind: "message", to: `contact:${zaki.id}`, label: "Zaki" }])
    h.engine.startRun({ flowId: saved.flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(runsOf(h.db, saved.flow.id)[0]!.status).toBe("awaiting_approval")
    decideVersion(h.db, saved.version.id, true, "test", h.now())
    await h.run(h.now())
    const sent = h.db.select().from(outbox).where(eq(outbox.chatId, zaki.chatId)).all()
    expect(sent.map((r) => r.payload.text)).toEqual(["Zaki belum jawab check-in."])
    expect(sent[0]!.source).toMatch(/^outbound:obm_/)
    expect(h.db.select().from(outboundMessages).get()).toMatchObject({ origin: "flow", approvedBy: "flow_version", contactId: zaki.id })
  })

  it("refuses unknown contacts, templates, and to plus contact", () => {
    const h = flowHarness()
    addContact(h.db, { agentId: getDefaultAgent(h.db).id, name: "Zaki", phone: "082200001001" }, h.now())
    expect(() => h.create({ name: "A", steps: [{ id: "m", kind: "message", text: "x", contact: "Budi" }] })).toThrow(/No saved contact Budi/)
    expect(() => h.create({ name: "B", steps: [{ id: "m", kind: "message", text: "x", contact: "{{vars.who}}" }] })).toThrow(/message.contact is a literal/)
    expect(() => h.create({ name: "C", steps: [{ id: "m", kind: "message", text: "x", contact: "Zaki", to: h.otherChatId }] })).toThrow(/not both/)
  })

  it("fails the step when the contact was removed or the kill switch is off", async () => {
    const h = flowHarness()
    const zaki = addContact(h.db, { agentId: getDefaultAgent(h.db).id, name: "Zaki", phone: "082200001001" }, h.now())
    const saved = h.create({ name: "Kabar", steps: [{ id: "m", kind: "message", text: "halo", contact: "Zaki" }] })
    decideVersion(h.db, saved.version.id, true, "test", h.now())
    setOutboundEnabled(h.db, false, h.now())
    h.engine.startRun({ flowId: saved.flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(stepLog(h.db, runsOf(h.db, saved.flow.id)[0]!.id)[0]!.error).toMatch(/turned off/)
    setOutboundEnabled(h.db, true, h.now())
    removeContact(h.db, zaki.id, h.now())
    h.engine.startRun({ flowId: saved.flow.id, triggerKind: "manual", runKey: "m2", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(stepLog(h.db, runsOf(h.db, saved.flow.id)[1]!.id)[0]!.error).toMatch(/removed/)
    expect(h.db.select().from(outbox).where(eq(outbox.chatId, zaki.chatId)).all()).toHaveLength(0)
  })
})
