// Owner invite links (PLAN 10). `palui owner invite` makes a single-use token,
// valid for 15 minutes; only its sha256 is stored. Accepting it creates the one
// owner user on first use, or resets that user's internal password when it
// already exists (the recovery path for a lost passkey). The password is random
// and never shown: the invite page signs in with it server side and then asks
// for a passkey right away.
import { randomBytes } from "node:crypto"
import { newId, ownerInvites, randomToken, schema, type DB } from "@palui/db"
import { sha256 } from "@palui/db/crypto"
import { hashPassword } from "better-auth/crypto"
import { and, asc, eq, gt, isNull } from "drizzle-orm"

export const INVITE_TTL_MS = 15 * 60_000
export const OWNER_USERNAME = "owner"
const OWNER_EMAIL = "owner@palui.invalid"

export class InviteError extends Error {}

export function inviteUrl(appUrl: string, token: string): string {
  return `${appUrl.replace(/\/+$/, "")}/app/invite/${token}`
}

export function createOwnerInvite(db: DB, appUrl: string, now = Date.now()): { token: string; url: string; expiresAt: number } {
  const token = randomToken(24)
  const expiresAt = now + INVITE_TTL_MS
  db.insert(ownerInvites).values({ id: newId("inv"), tokenHash: sha256(token), expiresAt, createdAt: now }).run()
  return { token, url: inviteUrl(appUrl, token), expiresAt }
}

export type InviteState = { ok: true; expiresAt: number; recovery: boolean } | { ok: false; problem: string }

export function inspectInvite(db: DB, token: string, now = Date.now()): InviteState {
  const row = db.select().from(ownerInvites).where(eq(ownerInvites.tokenHash, sha256(token))).get()
  if (!row) return { ok: false, problem: "This link isn't valid." }
  if (row.usedAt) return { ok: false, problem: "This link was already used." }
  if (row.expiresAt <= now) return { ok: false, problem: "This link expired." }
  return { ok: true, expiresAt: row.expiresAt, recovery: !!findOwner(db) }
}

export function findOwner(db: DB) {
  return db.select().from(schema.user).orderBy(asc(schema.user.createdAt)).get()
}

export function ownerExists(db: DB): boolean {
  return !!findOwner(db)
}

// Marks the invite used and creates or resets the owner, in one transaction.
// Returns the fresh internal password so the caller can sign in with it.
export async function acceptOwnerInvite(
  db: DB,
  token: string,
  now = Date.now(),
): Promise<{ userId: string; username: string; password: string; created: boolean }> {
  // Hashing is async, so it happens before the synchronous transaction.
  const password = randomBytes(32).toString("base64url")
  const hash = await hashPassword(password)
  return db.transaction((tx) => {
    const used = tx
      .update(ownerInvites)
      .set({ usedAt: now })
      .where(and(eq(ownerInvites.tokenHash, sha256(token)), isNull(ownerInvites.usedAt), gt(ownerInvites.expiresAt, now)))
      .run().changes
    if (!used) throw new InviteError("This link is no longer valid. Run `palui owner invite` for a new one.")

    const at = new Date(now)
    const existing = tx.select().from(schema.user).orderBy(asc(schema.user.createdAt)).get()
    if (!existing) {
      const userId = newId("usr")
      tx.insert(schema.user)
        .values({
          id: userId,
          name: "Owner",
          email: OWNER_EMAIL,
          emailVerified: false,
          username: OWNER_USERNAME,
          displayUsername: OWNER_USERNAME,
          createdAt: at,
          updatedAt: at,
        })
        .run()
      tx.insert(schema.account)
        .values({ id: newId("acc"), accountId: userId, providerId: "credential", userId, password: hash, createdAt: at, updatedAt: at })
        .run()
      return { userId, username: OWNER_USERNAME, password, created: true }
    }

    const credential = tx
      .select()
      .from(schema.account)
      .where(and(eq(schema.account.userId, existing.id), eq(schema.account.providerId, "credential")))
      .get()
    if (credential) {
      tx.update(schema.account).set({ password: hash, updatedAt: at }).where(eq(schema.account.id, credential.id)).run()
    } else {
      tx.insert(schema.account)
        .values({ id: newId("acc"), accountId: existing.id, providerId: "credential", userId: existing.id, password: hash, createdAt: at, updatedAt: at })
        .run()
    }
    // Recovery: whoever holds an old session or passkey (a lost phone) is shut
    // out. The invite page asks for a new passkey right after.
    tx.delete(schema.session).where(eq(schema.session.userId, existing.id)).run()
    tx.delete(schema.passkey).where(eq(schema.passkey.userId, existing.id)).run()
    return { userId: existing.id, username: existing.username ?? OWNER_USERNAME, password, created: false }
  })
}
