// Outbound page (docs/design/outbound-messages.md): the kill switch and limits,
// contacts, scheduled messages and the send log. Reads and writes the shared
// database directly; the runtime's scheduler and outbox worker pick changes up
// on their next sweep, and check the kill switch again when they send.
import { and, desc, eq, inArray, like, sql } from "drizzle-orm"
import { chats, outbox, outboundMessages } from "@palui/db"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { outboundSettingsSchema } from "@palui/core/settings-schema"
import { getSetting } from "@palui/core/settings"
import { getOwner } from "@palui/core/owner"
import { getAgent, getDefaultAgent, listAgents } from "@palui/core/agents"
import { formatPhone } from "@palui/core/outbound/phone"
import { addContact, cancelOutbound, getContact, listContacts, OutboundError, removeContact, saveOutboundSettings, setOutboundEnabled, updateContact, usage } from "@palui/core/outbound/service"
import { requireUser, requireWriter } from "@/server/session"

export const LOG_LIMIT = 100

function friendly<T>(fn: () => T): T {
  try {
    return fn()
  } catch (e) {
    if (e instanceof OutboundError) throw new Error(e.message)
    throw e
  }
}

export const getOutbound = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const now = Date.now()
  const settings = getSetting(db, "outbound")
  const contactRows = listContacts(db)
  const byId = new Map(contactRows.map((c) => [c.id, c]))
  // Every agent has its own contacts (PLAN 19); null rows belong to the default agent.
  const agentList = listAgents(db).map((a) => ({ id: a.id, name: a.name, isDefault: a.isDefault }))
  const defaultAgentId = getDefaultAgent(db).id
  const agentOf = (id: string | null) => id ?? defaultAgentId
  const contactsOut = contactRows.map((c) => ({
    id: c.id,
    agentId: agentOf(c.agentId),
    name: c.name,
    channel: c.channel,
    address: c.channel === "whatsapp" ? formatPhone(c.address) : c.address,
    policy: c.policy,
    note: c.note,
    sentToday: usage(db, now, c).contact ?? 0,
  }))
  const scheduled = db
    .select()
    .from(outboundMessages)
    .where(inArray(outboundMessages.status, ["scheduled", "firing"]))
    .orderBy(outboundMessages.fireAt)
    .limit(200)
    .all()
    .map((r) => ({ id: r.id, agentId: agentOf(r.agentId), contact: byId.get(r.contactId)?.name ?? "Removed contact", text: r.text, fireAt: r.fireAt, tz: r.tz, rrule: r.rrule, origin: r.origin }))
  // The log: every send is an outbox row with source "outbound:<id>".
  const sends = db
    .select({ o: outbox, m: outboundMessages })
    .from(outbox)
    .leftJoin(outboundMessages, eq(outboundMessages.id, sql`substr(${outbox.source}, ${"outbound:".length + 1})`))
    .where(like(outbox.source, "outbound:%"))
    .orderBy(desc(outbox.createdAt))
    .limit(LOG_LIMIT)
    .all()
  // Removed contacts keep their name in the log.
  const nameOf = (id: string) => byId.get(id)?.name ?? getContact(db, id)?.name ?? "Removed contact"
  const log = sends.map(({ o, m }) => ({
    id: o.id,
    agentId: m ? agentOf(m.agentId) : null,
    contact: m ? nameOf(m.contactId) : "Unknown",
    text: o.payload.text,
    status: o.status,
    error: o.error,
    origin: m?.origin ?? null,
    approvedBy: m?.approvedBy ?? null,
    createdAt: o.createdAt,
    sendAt: o.nextAttemptAt,
    sentAt: o.sentAt,
    outboundId: m?.id ?? null,
  }))
  const ownerTg = getOwner(db, "telegram")?.externalId
  const dms = db
    .select({ id: chats.id, title: chats.title, channel: chats.channel, externalId: chats.externalId })
    .from(chats)
    .where(and(eq(chats.kind, "dm"), eq(chats.channel, "telegram")))
    .all()
    .filter((c) => c.externalId !== ownerTg && !contactRows.some((k) => k.chatId === c.id))
  return { agents: agentList, defaultAgentId, settings, usage: usage(db, now).total, contacts: contactsOut, scheduled, log, telegramChats: dms, timezone: getSetting(db, "timezone") }
})

export const setOutboundSwitch = createServerFn({ method: "POST" })
  .validator(z.object({ enabled: z.boolean() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    return setOutboundEnabled(db, data.enabled)
  })

export const saveOutboundLimits = createServerFn({ method: "POST" })
  .validator(outboundSettingsSchema.omit({ enabled: true }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    friendly(() => saveOutboundSettings(db, data))
    return { ok: true }
  })

export const addContactFn = createServerFn({ method: "POST" })
  .validator(
    z.object({
      agentId: z.string().min(1).max(100),
      name: z.string().trim().min(1).max(60),
      phone: z.string().trim().max(30).optional(),
      chatId: z.string().max(100).optional(),
      policy: z.enum(["ask", "allow"]).default("ask"),
      note: z.string().trim().max(200).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!getAgent(db, data.agentId)) throw new Error("That agent doesn't exist anymore.")
    const row = friendly(() => addContact(db, { agentId: data.agentId, name: data.name, phone: data.phone || undefined, chatId: data.chatId || undefined, policy: data.policy, note: data.note }))
    return { id: row.id }
  })

export const setContactPolicy = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100), policy: z.enum(["ask", "allow"]) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    friendly(() => updateContact(db, data.id, { policy: data.policy }))
    return { ok: true }
  })

export const removeContactFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    return friendly(() => removeContact(db, data.id))
  })

export const cancelOutboundFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const r = cancelOutbound(db, data.id)
    if (!r.ok) throw new Error(r.message)
    return { ok: true }
  })
