// Messages to contacts on the owner's behalf (docs/design/outbound-messages.md).
// Every guard that must hold whatever the model does lives here: the kill switch,
// the allowlist (contacts), and the rate limits. Callers add the origin checks.
import { and, asc, desc, eq, gt, inArray, isNull, like, ne, sql } from "drizzle-orm"
import { chats, contacts, newId, outbox, outboundMessages, type ContactPolicy, type DB, type OutboxPayload } from "@palui/db"
import { findChat, getChat, ensureChat, type ChatRow } from "../chats"
import { logEvent } from "../events"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import { DEFAULT_OUTBOUND_SIGNATURE, getSetting, LEGACY_OUTBOUND_SIGNATURES, setSetting, type OutboundSettings } from "../settings"
import { assertZone, formatWhen, normalizeLocal, normalizeRrule, resolveFirstFire, TimeError } from "../reminders/time"
import { notifyScheduleChanged } from "../reminders/signal"
import { formatPhone, normalizePhone, phonesIn, waJid } from "./phone"

export type ContactRow = typeof contacts.$inferSelect
export type OutboundRow = typeof outboundMessages.$inferSelect

export class OutboundError extends Error {}

export const MAX_OUTBOUND_TEXT = 2000
export const MAX_CONTACT_NAME = 60
// A "now" send that couldn't go out within this is dropped (a stale "otw" is worse than none).
export const OUTBOUND_TTL_MS = 60 * 60_000
// A scheduled send this late (downtime) is skipped and the owner is told.
export const OUTBOUND_LATE_SKIP_MS = 30 * 60_000
export const DAY_MS = 24 * 3600_000

export const sourceOf = (outboundId: string) => `outbound:${outboundId}`
export const noticeSourceOf = (outboundId: string) => `outbound-notice:${outboundId}`
export const cancelAction = (outboundId: string) => `ob:cancel:${outboundId}`

export function outboundSettings(db: DB): OutboundSettings {
  return getSetting(db, "outbound")
}

// --- Contacts ------------------------------------------------------------------------

export function contactLabel(c: Pick<ContactRow, "name" | "channel" | "address">): string {
  return c.channel === "whatsapp" ? `${c.name} (${formatPhone(c.address)})` : `${c.name} (Telegram)`
}

export function getContact(db: DB, id: string): ContactRow | undefined {
  return db.select().from(contacts).where(eq(contacts.id, id)).get()
}

// An agent's contacts (PLAN 19: each agent has its own list). Omit the agent for
// every agent's (the dashboard).
export function listContacts(db: DB, agentId?: string): ContactRow[] {
  const where = agentId === undefined ? isNull(contacts.removedAt) : and(isNull(contacts.removedAt), eq(contacts.agentId, agentId))
  return db.select().from(contacts).where(where).orderBy(asc(contacts.name)).all()
}

function sameName(a: string, b: string) {
  return a.trim().toLowerCase() === b.trim().toLowerCase()
}

// One of the agent's saved contacts by id, name (case-insensitive) or WhatsApp number.
export function findContact(db: DB, agentId: string, ref: string): ContactRow | undefined {
  const r = ref.trim()
  if (!r) return undefined
  const all = listContacts(db, agentId)
  const byId = all.find((c) => c.id === r)
  if (byId) return byId
  const byName = all.find((c) => sameName(c.name, r))
  if (byName) return byName
  const phone = normalizePhone(r)
  return phone ? all.find((c) => c.channel === "whatsapp" && c.address === phone) : undefined
}

function cleanName(name: string): string {
  const n = name.replace(/\s+/g, " ").trim()
  if (!n) throw new OutboundError("A contact needs a name.")
  if (n.length > MAX_CONTACT_NAME) throw new OutboundError(`Keep the name under ${MAX_CONTACT_NAME} characters.`)
  return n
}

export type AddContactInput = { agentId: string; name: string; phone?: string; chatId?: string; policy?: ContactPolicy; note?: string | null }

// A contact from a WhatsApp number, or from an existing Telegram/WhatsApp DM.
export function addContact(db: DB, input: AddContactInput, now = Date.now()): ContactRow {
  const name = cleanName(input.name)
  const active = listContacts(db, input.agentId)
  if (active.some((c) => sameName(c.name, name))) throw new OutboundError(`There's already a contact named ${name}.`)
  let channel: "whatsapp" | "telegram"
  let address: string
  let chat: ChatRow
  if (input.phone) {
    const digits = normalizePhone(input.phone)
    if (!digits) throw new OutboundError(`${input.phone} isn't a phone number. Use 08xx or +<country code>.`)
    channel = "whatsapp"
    address = digits
    // A new number gets a chat row Palui won't answer (allowed=false); an existing chat keeps its settings.
    chat = findChat(db, "whatsapp", waJid(digits)) ?? ensureChat(db, { channel: "whatsapp", externalId: waJid(digits), title: name, allowed: false }, now)
  } else if (input.chatId) {
    const found = getChat(db, input.chatId)
    if (!found) throw new OutboundError(`No chat ${input.chatId}.`)
    if (found.channel === "web") throw new OutboundError("The web chat can't be a contact.")
    if (found.kind !== "dm") throw new OutboundError("Only direct chats can be contacts, not groups.")
    chat = found
    channel = found.channel
    address = found.channel === "whatsapp" ? found.externalId.split("@")[0]!.replace(/\D/g, "") : found.externalId
  } else {
    throw new OutboundError("Give a phone number or a chat.")
  }
  const dup = active.find((c) => c.chatId === chat.id || (c.channel === channel && c.address === address))
  if (dup) throw new OutboundError(`That's already saved as ${dup.name}.`)
  const row = db
    .insert(contacts)
    .values({ id: newId("con"), agentId: input.agentId, name, channel, address, chatId: chat.id, policy: input.policy ?? "ask", note: input.note?.trim() || null, createdAt: now, updatedAt: now })
    .returning()
    .get()
  logEvent(db, { kind: "outbound.contact", message: `Contact ${contactLabel(row)} added (${row.policy === "allow" ? "always allow" : "always ask"})`, data: { contactId: row.id } }, now)
  return row
}

export function updateContact(db: DB, id: string, patch: { name?: string; policy?: ContactPolicy; note?: string | null }, now = Date.now()): ContactRow {
  const c = getContact(db, id)
  if (!c || c.removedAt) throw new OutboundError(`No contact ${id}.`)
  const set: Partial<ContactRow> = { updatedAt: now }
  if (patch.name !== undefined) {
    const name = cleanName(patch.name)
    if (listContacts(db, c.agentId ?? undefined).some((o) => o.id !== id && sameName(o.name, name))) throw new OutboundError(`There's already a contact named ${name}.`)
    set.name = name
  }
  if (patch.policy !== undefined) set.policy = patch.policy
  if (patch.note !== undefined) set.note = patch.note?.trim() || null
  const row = db.update(contacts).set(set).where(eq(contacts.id, id)).returning().get()
  if (patch.policy !== undefined && patch.policy !== c.policy) {
    logEvent(db, { kind: "outbound.policy", level: "warn", message: `${row.name} is now ${row.policy === "allow" ? "always allow" : "always ask"}`, data: { contactId: id } }, now)
  }
  return row
}

// Removing a contact stops everything still waiting for it.
export function removeContact(db: DB, id: string, now = Date.now()): { cancelled: number } {
  const c = getContact(db, id)
  if (!c || c.removedAt) throw new OutboundError(`No contact ${id}.`)
  let cancelled = 0
  db.transaction(() => {
    db.update(contacts).set({ removedAt: now, updatedAt: now }).where(eq(contacts.id, id)).run()
    const rows = db.select({ id: outboundMessages.id }).from(outboundMessages).where(eq(outboundMessages.contactId, id)).all()
    for (const r of rows) cancelled += cancelOutboundRow(db, r.id, now)
  })
  logEvent(db, { kind: "outbound.contact", message: `Contact ${contactLabel(c)} removed; ${cancelled} waiting message(s) cancelled`, data: { contactId: id } }, now)
  notifyScheduleChanged()
  return { cancelled }
}

// Whether the owner's own words name this contact (its name or its number).
export function mentionsContact(c: Pick<ContactRow, "name" | "channel" | "address">, text: string | undefined | null): boolean {
  if (!text) return false
  const hay = text.toLowerCase()
  const name = c.name.toLowerCase()
  const at = hay.indexOf(name)
  if (at !== -1) {
    const before = at === 0 ? "" : hay[at - 1]!
    const after = hay[at + name.length] ?? ""
    const word = (ch: string) => /[\p{L}\p{N}]/u.test(ch)
    if (!word(before) && !word(after)) return true
  }
  return c.channel === "whatsapp" && phonesIn(text).includes(c.address)
}

// --- Limits and the kill switch --------------------------------------------------------

function sentSince(db: DB, since: number, chatId?: string): number {
  const where = [like(outbox.source, "outbound:%"), gt(outbox.createdAt, since), ne(outbox.status, "cancelled")]
  if (chatId) where.push(eq(outbox.chatId, chatId))
  return db.select({ n: sql<number>`count(*)` }).from(outbox).where(and(...where)).get()?.n ?? 0
}

export function usage(db: DB, now = Date.now(), contact?: ContactRow): { total: number; contact: number | null } {
  return { total: sentSince(db, now - DAY_MS), contact: contact ? sentSince(db, now - DAY_MS, contact.chatId) : null }
}

// Throws when a send must not happen now.
export function assertCanSend(db: DB, given: ContactRow, now = Date.now()) {
  const s = outboundSettings(db)
  if (!s.enabled) throw new OutboundError("Sending messages to contacts is turned off (kill switch on the Outbound page).")
  // Read it again: the caller's copy may be older than a removal.
  const contact = getContact(db, given.id) ?? given
  if (contact.removedAt) throw new OutboundError(`${contact.name} was removed from contacts.`)
  const u = usage(db, now, contact)
  if (u.contact! >= s.perContactPerDay) throw new OutboundError(`Limit reached: ${u.contact} messages to ${contact.name} in the last 24 hours (max ${s.perContactPerDay}).`)
  if (u.total >= s.perDay) throw new OutboundError(`Limit reached: ${u.total} messages to contacts in the last 24 hours (max ${s.perDay}).`)
}

// --- Signature -----------------------------------------------------------------------

// Compared loosely: case, whitespace and the WhatsApp markup (_ * ~) don't count,
// so a signature the agent typed by hand still matches.
function looseText(s: string): string {
  return s.replace(/[_*~]/g, "").replace(/\s+/g, " ").trim().toLowerCase()
}

// Every signature text Palui knows: the current one, the owner's earlier ones and
// the earlier defaults.
export function knownSignatures(s: OutboundSettings): string[] {
  return [s.signature, ...s.pastSignatures, DEFAULT_OUTBOUND_SIGNATURE, ...LEGACY_OUTBOUND_SIGNATURES].filter((x) => x.trim())
}

// Whether the text already ends with one of these signatures, or with a
// signature's last line (the agent may have left out the dash line).
export function endsWithSignature(text: string, signatures: string[]): boolean {
  const body = looseText(text)
  return signatures.some((sig) => {
    const full = looseText(sig)
    if (full && body.endsWith(full)) return true
    const last = looseText(sig.trim().split("\n").at(-1) ?? "")
    return last.length >= 20 && body.endsWith(last)
  })
}

// The body with the signature after one blank line, unless it's off, opted out,
// or already there.
export function withSignature(text: string, s: OutboundSettings, opts: { signature?: boolean } = {}): string {
  const t = text.trim()
  const sig = s.signature.trim()
  if (!s.signatureOn || !sig || opts.signature === false) return t
  if (endsWithSignature(t, knownSignatures(s))) return t
  return `${t}\n\n${sig}`
}

// The exact text a contact gets. Throws when it's empty or too long.
export function finalText(db: DB, text: string, opts: { signature?: boolean } = {}): string {
  const t = text.trim()
  if (!t) throw new OutboundError("The message is empty.")
  const full = withSignature(t, outboundSettings(db), opts)
  if (full.length > MAX_OUTBOUND_TEXT) throw new OutboundError(`Keep the message under ${MAX_OUTBOUND_TEXT} characters, signature included (this one is ${full.length}).`)
  return full
}

// Saves the limits and the signature from the Outbound page. An edited signature
// text is remembered, so messages already carrying it aren't signed twice.
export function saveOutboundSettings(db: DB, patch: Omit<OutboundSettings, "enabled" | "pastSignatures">, now = Date.now()): OutboundSettings {
  const cur = outboundSettings(db)
  const signature = patch.signature.replace(/\r\n?/g, "\n").trim()
  if (patch.signatureOn && !signature) throw new OutboundError("Write the signature, or turn it off.")
  let pastSignatures = cur.pastSignatures
  if (cur.signature.trim() && cur.signature.trim() !== signature) {
    pastSignatures = [cur.signature.trim(), ...pastSignatures.filter((x) => x !== cur.signature.trim() && x !== signature)].slice(0, 10)
  }
  const next: OutboundSettings = { ...cur, ...patch, signature, pastSignatures }
  setSetting(db, "outbound", next, now)
  if (cur.signatureOn !== next.signatureOn || cur.signature.trim() !== signature) {
    logEvent(db, { kind: "outbound.signature", message: next.signatureOn ? "Signature for messages to contacts changed" : "Signature for messages to contacts turned off" }, now)
  }
  return next
}

// The kill switch. Off cancels every send still waiting in the outbox.
export function setOutboundEnabled(db: DB, enabled: boolean, now = Date.now()): { cancelled: number } {
  setSetting(db, "outbound", { ...outboundSettings(db), enabled }, now)
  let cancelled = 0
  if (!enabled) {
    cancelled = db
      .update(outbox)
      .set({ status: "cancelled", error: "outbound turned off", claimedAt: null })
      .where(and(like(outbox.source, "outbound:%"), eq(outbox.status, "pending")))
      .run().changes
  }
  logEvent(db, { kind: "outbound.switch", level: enabled ? "info" : "warn", message: enabled ? "Messages to contacts turned on" : `Messages to contacts turned off; ${cancelled} waiting message(s) cancelled` }, now)
  notifyScheduleChanged()
  return { cancelled }
}

// --- Sending -------------------------------------------------------------------------

export type SendMeta = {
  origin: "chat" | "flow"
  originRef?: string | null
  approvedBy: "owner" | "policy" | "flow_version"
  // Where notices about this send go.
  ownerChatId?: string | null
  // false: no signature on this one (the owner asked for that).
  signature?: boolean
}

function enqueueTo(db: DB, contact: ContactRow, outboundId: string, text: string, now: number, notBefore: number): string {
  const chat = getChat(db, contact.chatId)
  if (!chat) throw new OutboundError(`The chat for ${contact.name} is gone; add the contact again.`)
  const row = enqueueOutbox(
    db,
    { chatId: chat.id, channel: chat.channel, connectorId: chat.connectorId || null, payload: { text, format: "plain" }, source: sourceOf(outboundId), notBefore, expiresAt: notBefore + OUTBOUND_TTL_MS },
    now,
  )
  return row.id
}

// Send now. `holdMs` (the undo window) delays the outbox row; 0 sends at once.
export function sendNow(db: DB, contact: ContactRow, text: string, meta: SendMeta & { holdMs: number }, now = Date.now()): { row: OutboundRow; outboxId: string; sendAt: number } {
  assertCanSend(db, contact, now)
  const full = finalText(db, text, { signature: meta.signature })
  const sendAt = now + Math.max(0, meta.holdMs)
  const tz = getSetting(db, "timezone")
  const result = db.transaction(() => {
    const id = newId("obm")
    const outboxId = enqueueTo(db, contact, id, full, now, sendAt)
    const row = db
      .insert(outboundMessages)
      .values({
        id,
        agentId: contact.agentId,
        contactId: contact.id,
        text: full,
        origin: meta.origin,
        originRef: meta.originRef ?? null,
        approvedBy: meta.approvedBy,
        ownerChatId: meta.ownerChatId ?? null,
        rrule: null,
        tz,
        localStart: new Date(sendAt).toISOString().slice(0, 16),
        fireAt: sendAt,
        status: "done",
        lastOutboxId: outboxId,
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
    return { row, outboxId, sendAt }
  })
  notifyOutbox()
  return result
}

export type ScheduleInput = { localStart: string; tz: string; rrule?: string | null }

// Send at a wall-clock time, or on a repeat. Guards run again at each fire.
export function scheduleSend(db: DB, contact: ContactRow, text: string, when: ScheduleInput, meta: SendMeta, now = Date.now()): OutboundRow {
  const s = outboundSettings(db)
  if (!s.enabled) throw new OutboundError("Sending messages to contacts is turned off (kill switch on the Outbound page).")
  if ((getContact(db, contact.id) ?? contact).removedAt) throw new OutboundError(`${contact.name} was removed from contacts.`)
  // Signed now, so the stored text is the one the owner approved; the scheduler
  // sends it as is at every fire.
  const full = finalText(db, text, { signature: meta.signature })
  let first
  try {
    assertZone(when.tz)
    const localStart = normalizeLocal(when.localStart)
    const rrule = when.rrule ? normalizeRrule(when.rrule) : null
    first = resolveFirstFire({ rrule, tz: when.tz, localStart }, now)
    // A one-off time already past is refused instead of rolling to tomorrow:
    // "jam 8" said at 21:00 is a mistake worth asking about, not a send tomorrow.
    if (first.rolled) throw new OutboundError(`${localStart.slice(11, 16)} today has already passed. Pick a time in the future.`)
  } catch (e) {
    if (e instanceof TimeError) throw new OutboundError(e.message)
    throw e
  }
  const row = db
    .insert(outboundMessages)
    .values({
      id: newId("obm"),
      agentId: contact.agentId,
      contactId: contact.id,
      text: full,
      origin: meta.origin,
      originRef: meta.originRef ?? null,
      approvedBy: meta.approvedBy,
      ownerChatId: meta.ownerChatId ?? null,
      rrule: first.schedule.rrule,
      tz: first.schedule.tz,
      localStart: first.schedule.localStart,
      fireAt: first.fireAt,
      status: "scheduled",
      createdAt: now,
      updatedAt: now,
    })
    .returning()
    .get()
  notifyScheduleChanged()
  return row
}

export function getOutbound(db: DB, id: string): OutboundRow | undefined {
  return db.select().from(outboundMessages).where(eq(outboundMessages.id, id)).get()
}

// Cancels a scheduled request and its sends still waiting in the outbox. Returns how many things it stopped.
function cancelOutboundRow(db: DB, id: string, now: number): number {
  let n = db
    .update(outboundMessages)
    .set({ status: "cancelled", updatedAt: now })
    .where(and(eq(outboundMessages.id, id), inArray(outboundMessages.status, ["scheduled", "firing"])))
    .run().changes
  n += db
    .update(outbox)
    .set({ status: "cancelled", error: "cancelled by the owner", claimedAt: null })
    .where(and(eq(outbox.source, sourceOf(id)), eq(outbox.status, "pending")))
    .run().changes
  return n
}

// "batal" on a send: stops it if it hasn't gone out. Says what happened.
export function cancelOutbound(db: DB, id: string, now = Date.now()): { ok: boolean; message: string; row?: OutboundRow } {
  const row = getOutbound(db, id)
  if (!row) return { ok: false, message: `Nggak ada pesan ${id}.` }
  const contact = getContact(db, row.contactId)
  const who = contact?.name ?? "kontak"
  const n = cancelOutboundRow(db, id, now)
  notifyScheduleChanged()
  if (n) {
    logEvent(db, { kind: "outbound.cancel", message: `Message to ${who} cancelled`, data: { outboundId: id } }, now)
    return { ok: true, message: `Oke, pesan ke ${who} dibatalin.`, row: getOutbound(db, id) }
  }
  const sent = row.lastOutboxId ? db.select({ status: outbox.status }).from(outbox).where(eq(outbox.id, row.lastOutboxId)).get() : undefined
  if (sent?.status === "sent" || sent?.status === "sending") return { ok: false, message: `Telat, pesan ke ${who} udah kekirim.`, row }
  return { ok: false, message: `Pesan ke ${who} udah nggak aktif.`, row }
}

// A bare "batal": the newest send still in its undo window, asked from this chat.
export function newestHeld(db: DB, ownerChatId: string, now = Date.now()): OutboundRow | undefined {
  return db
    .select({ o: outboundMessages })
    .from(outboundMessages)
    .innerJoin(outbox, eq(outbox.id, outboundMessages.lastOutboxId))
    .where(and(eq(outboundMessages.ownerChatId, ownerChatId), eq(outbox.status, "pending"), gt(outbox.nextAttemptAt, now)))
    .orderBy(desc(outboundMessages.createdAt))
    .get()?.o
}

// The send a notice was about, from the notice's channel message id (a reply quoting it).
export function heldFromNotice(db: DB, chatId: string, channelMsgId: string): string | undefined {
  const row = db
    .select({ source: outbox.source })
    .from(outbox)
    .where(and(eq(outbox.chatId, chatId), eq(outbox.channelMsgId, channelMsgId), like(outbox.source, "outbound-notice:%")))
    .get()
  return row?.source.slice("outbound-notice:".length)
}

// --- Notices to the owner --------------------------------------------------------------

export function noticeToOwner(db: DB, chatId: string | null | undefined, text: string, source = "system", payload: Partial<OutboxPayload> = {}, now = Date.now()): void {
  if (!chatId) return
  const chat = db.select().from(chats).where(eq(chats.id, chatId)).get()
  if (!chat) return
  enqueueOutbox(db, { chatId: chat.id, channel: chat.channel, connectorId: chat.connectorId || null, payload: { text, format: "plain", ...payload }, source }, now)
  notifyOutbox()
}

// The "kekirim 15 detik lagi" notice with a cancel button.
export function holdNotice(db: DB, row: OutboundRow, contact: ContactRow, sendAt: number, now = Date.now()): void {
  const secs = Math.max(0, Math.round((sendAt - now) / 1000))
  const text = secs ? `Pesan ke ${contactLabel(contact)} kekirim ${secs} detik lagi:\n\n${row.text}` : `Pesan ke ${contactLabel(contact)} dikirim:\n\n${row.text}`
  noticeToOwner(db, row.ownerChatId, text, noticeSourceOf(row.id), secs ? { actions: [{ id: cancelAction(row.id), label: "Batalin" }] } : {}, now)
}

// The outbox gave up on a send (called by the outbox worker).
export function outboundFailed(db: DB, outboxRow: { source: string; error: string | null }, now = Date.now()): void {
  if (!outboxRow.source.startsWith("outbound:")) return
  const row = getOutbound(db, outboxRow.source.slice("outbound:".length))
  if (!row) return
  const contact = getContact(db, row.contactId)
  noticeToOwner(db, row.ownerChatId, `Pesan ke ${contact?.name ?? "kontak"} gagal terkirim${outboxRow.error ? ` (${outboxRow.error.slice(0, 120)})` : ""}.`, "system", {}, now)
}

// --- Listing -------------------------------------------------------------------------

export type OutboundView = {
  id: string
  contact: string
  text: string
  status: string
  recurring: boolean
  rrule: string | null
  next: string | null
  delivery: string | null
}

export function viewOutbound(db: DB, r: OutboundRow, now = Date.now()): OutboundView {
  const c = getContact(db, r.contactId)
  const sent = r.lastOutboxId ? db.select({ status: outbox.status }).from(outbox).where(eq(outbox.id, r.lastOutboxId)).get() : undefined
  return {
    id: r.id,
    contact: c ? contactLabel(c) : r.contactId,
    text: r.text,
    status: r.status,
    recurring: !!r.rrule,
    rrule: r.rrule,
    next: r.status === "scheduled" ? formatWhen(r.fireAt, r.tz, now) : null,
    delivery: sent?.status ?? null,
  }
}

export function listOutbound(db: DB, agentId: string, filter: "scheduled" | "recent", limit = 20): OutboundRow[] {
  const mine = eq(outboundMessages.agentId, agentId)
  if (filter === "scheduled") {
    return db.select().from(outboundMessages).where(and(mine, inArray(outboundMessages.status, ["scheduled", "firing"]))).orderBy(asc(outboundMessages.fireAt)).limit(limit).all()
  }
  return db.select().from(outboundMessages).where(mine).orderBy(desc(outboundMessages.updatedAt)).limit(limit).all()
}
