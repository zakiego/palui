// Temporary links to a saved attachment. The owner makes them from the
// Attachments page, the agent with share_attachment. A link is
// `${APP_URL}/api/share/<token>`: "public" opens for anyone with it, "private"
// also needs a dashboard sign-in. Links always expire (7 days at most) and can be
// revoked. Only the token's sha256 is stored, like owner invites.
import { createHash } from "node:crypto"
import { and, count, desc, eq, gt, isNull, sql } from "drizzle-orm"
import { attachmentShares, newId, randomToken, savedAttachments, type DB, type ShareVisibility } from "@palui/db"
import { getAttachment, type AttachmentMeta } from "./service"

export const SHARE_DURATIONS = { "1h": 60 * 60 * 1000, "24h": 24 * 60 * 60 * 1000, "7d": 7 * 24 * 60 * 60 * 1000 } as const
export type ShareDuration = keyof typeof SHARE_DURATIONS
export const SHARE_DURATION_KEYS = Object.keys(SHARE_DURATIONS) as [ShareDuration, ...ShareDuration[]]

export type ShareRow = typeof attachmentShares.$inferSelect
export type ShareView = Omit<ShareRow, "tokenHash">

const TOKEN_RE = /^[A-Za-z0-9_-]{20,64}$/

function hashToken(token: string): string {
  return createHash("sha256").update(token).digest("hex")
}

function toView({ tokenHash: _hash, ...rest }: ShareRow): ShareView {
  return rest
}

export function shareUrl(appUrl: string, token: string): string {
  return `${appUrl}/api/share/${token}`
}

export function createShare(
  db: DB,
  input: { attachmentId: string; visibility: ShareVisibility; duration: ShareDuration; createdBy: string },
  now = Date.now(),
): { share: ShareView; token: string } | undefined {
  if (!getAttachment(db, input.attachmentId)) return undefined
  const token = randomToken(24)
  const row: ShareRow = {
    id: newId("shr"),
    attachmentId: input.attachmentId,
    tokenHash: hashToken(token),
    visibility: input.visibility,
    createdBy: input.createdBy,
    expiresAt: now + SHARE_DURATIONS[input.duration],
    revokedAt: null,
    openCount: 0,
    lastOpenedAt: null,
    createdAt: now,
  }
  db.insert(attachmentShares).values(row).run()
  return { share: toView(row), token }
}

export type ResolvedShare = { ok: true; share: ShareView; attachment: AttachmentMeta } | { ok: false; reason: "not_found" | "expired" | "revoked" }

// Looks a token up. Doesn't count the open; the route calls recordOpen once it serves the file.
export function resolveShare(db: DB, token: string, now = Date.now()): ResolvedShare {
  if (!TOKEN_RE.test(token)) return { ok: false, reason: "not_found" }
  const row = db.select().from(attachmentShares).where(eq(attachmentShares.tokenHash, hashToken(token))).get()
  if (!row) return { ok: false, reason: "not_found" }
  if (row.revokedAt !== null) return { ok: false, reason: "revoked" }
  if (row.expiresAt <= now) return { ok: false, reason: "expired" }
  const attachment = getAttachment(db, row.attachmentId)
  if (!attachment) return { ok: false, reason: "not_found" }
  return { ok: true, share: toView(row), attachment }
}

export function recordOpen(db: DB, shareId: string, now = Date.now()): void {
  db.update(attachmentShares)
    .set({ openCount: sql`${attachmentShares.openCount} + 1`, lastOpenedAt: now })
    .where(eq(attachmentShares.id, shareId))
    .run()
}

const live = (now: number) => and(isNull(attachmentShares.revokedAt), gt(attachmentShares.expiresAt, now))

// Links that still open, newest first.
export function listActiveShares(db: DB, attachmentId: string, now = Date.now()): ShareView[] {
  return db
    .select()
    .from(attachmentShares)
    .where(and(eq(attachmentShares.attachmentId, attachmentId), live(now)))
    .orderBy(desc(attachmentShares.createdAt), desc(attachmentShares.id))
    .all()
    .map(toView)
}

// Live links per attachment, for the Attachments table.
export function activeShareCounts(db: DB, now = Date.now()): Map<string, number> {
  const rows = db
    .select({ id: attachmentShares.attachmentId, n: count() })
    .from(attachmentShares)
    .innerJoin(savedAttachments, eq(savedAttachments.id, attachmentShares.attachmentId))
    .where(live(now))
    .groupBy(attachmentShares.attachmentId)
    .all()
  return new Map(rows.map((r) => [r.id, r.n]))
}

export function revokeShare(db: DB, shareId: string, now = Date.now()): boolean {
  return db.update(attachmentShares).set({ revokedAt: now }).where(and(eq(attachmentShares.id, shareId), isNull(attachmentShares.revokedAt))).run().changes > 0
}

// Revokes every live link to one attachment. Returns how many.
export function revokeShares(db: DB, attachmentId: string, now = Date.now()): number {
  return db
    .update(attachmentShares)
    .set({ revokedAt: now })
    .where(and(eq(attachmentShares.attachmentId, attachmentId), live(now)))
    .run().changes
}
