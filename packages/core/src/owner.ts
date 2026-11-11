// Channel ownership (PLAN 5.1, 10). Only the owner gets answers; everyone else
// is dropped silently with the reason logged.
//
// Telegram: claimed with a single-use nonce link (t.me/<bot>?start=<nonce>, 10 min).
// WhatsApp: the owner's phone number is entered in the wizard; the LID is learned
// from the first DM whose alternate id matches that number, then both are stored.
import { and, eq, gt, isNull, or } from "drizzle-orm"
import { claims, identities, newId, randomToken, type Channel, type DB } from "@palui/db"

export const CLAIM_TTL_MS = 10 * 60_000

export type IdentityRow = typeof identities.$inferSelect

export function createClaim(db: DB, channel: Channel, now = Date.now()): { nonce: string; expiresAt: number } {
  const nonce = randomToken(12)
  const expiresAt = now + CLAIM_TTL_MS
  db.insert(claims).values({ nonce, channel, expiresAt, createdAt: now }).run()
  return { nonce, expiresAt }
}

// Single use, unexpired, right channel. Marks the claim used and stores the owner
// identity in one transaction. A new claim replaces any previous owner on that
// channel (it's the recovery path when the owner changes accounts).
export function claimOwner(db: DB, input: { channel: Channel; nonce: string; externalId: string; displayName?: string }, now = Date.now()): boolean {
  return db.transaction((tx) => {
    const used = tx
      .update(claims)
      .set({ usedAt: now, usedBy: input.externalId })
      .where(and(eq(claims.nonce, input.nonce), eq(claims.channel, input.channel), isNull(claims.usedAt), gt(claims.expiresAt, now)))
      .run().changes
    if (!used) return false
    tx.delete(identities).where(eq(identities.channel, input.channel)).run()
    tx.insert(identities)
      .values({ id: newId("idn"), channel: input.channel, externalId: input.externalId, displayName: input.displayName ?? null, role: "owner", createdAt: now })
      .run()
    return true
  })
}

// Normalize a phone number to a WhatsApp phone JID: digits only, leading 0 -> 62.
export function phoneToJid(phone: string): string {
  let digits = phone.replace(/\D/g, "")
  if (digits.startsWith("0")) digits = `62${digits.slice(1)}`
  return `${digits}@s.whatsapp.net`
}

export function setWhatsAppOwner(db: DB, phone: string, now = Date.now()): IdentityRow {
  const jid = phoneToJid(phone)
  return db.transaction((tx) => {
    tx.delete(identities).where(eq(identities.channel, "whatsapp")).run()
    return tx.insert(identities).values({ id: newId("idn"), channel: "whatsapp", externalId: jid, role: "owner", createdAt: now }).returning().get()
  })
}

export function getOwner(db: DB, channel: Channel): IdentityRow | undefined {
  return db.select().from(identities).where(eq(identities.channel, channel)).get()
}

// Whether the sender is the owner. For WhatsApp, a sender may show up as the
// phone JID or as the LID; when the alt id matches the stored number, the LID is
// learned and stored so later LID-only messages still match.
export function isOwner(db: DB, input: { channel: Channel; externalId: string; altId?: string }): boolean {
  if (input.channel === "web") return true // the dashboard is behind passkey auth
  const ids = [input.externalId, input.altId].filter((x): x is string => !!x)
  if (!ids.length) return false
  const owner = db
    .select()
    .from(identities)
    .where(and(eq(identities.channel, input.channel), or(...ids.flatMap((id) => [eq(identities.externalId, id), eq(identities.altId, id)]))))
    .get()
  if (!owner) return false
  if (input.channel === "whatsapp" && !owner.altId) {
    const lid = ids.find((id) => id.endsWith("@lid"))
    if (lid) db.update(identities).set({ altId: lid }).where(eq(identities.id, owner.id)).run()
  }
  return true
}
