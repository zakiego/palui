import { memoryDb, schema } from "@palui/db"
import { verifyPassword } from "better-auth/crypto"
import { and, eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { acceptOwnerInvite, createOwnerInvite, INVITE_TTL_MS, InviteError, inspectInvite } from "./owner-invite"

const T0 = 1_800_000_000_000
const credential = (db: ReturnType<typeof memoryDb>, userId: string) =>
  db
    .select()
    .from(schema.account)
    .where(and(eq(schema.account.userId, userId), eq(schema.account.providerId, "credential")))
    .get()

describe("owner invite", () => {
  it("stores only the token's hash and prints an /app/invite link", () => {
    const db = memoryDb()
    const inv = createOwnerInvite(db, "https://palui.example.com/", T0)
    expect(inv.url).toBe(`https://palui.example.com/app/invite/${inv.token}`)
    expect(inv.expiresAt).toBe(T0 + INVITE_TTL_MS)
    const row = db.select().from(schema.ownerInvites).get()!
    expect(row.tokenHash).not.toContain(inv.token)
    expect(row.tokenHash).toMatch(/^[0-9a-f]{64}$/)
  })

  it("creates the owner on first use and works only once", async () => {
    const db = memoryDb()
    const { token } = createOwnerInvite(db, "http://localhost:4890", T0)
    expect(inspectInvite(db, token, T0 + 1000)).toMatchObject({ ok: true, recovery: false })
    const res = await acceptOwnerInvite(db, token, T0 + 1000)
    expect(res).toMatchObject({ username: "owner", created: true })
    const users = db.select().from(schema.user).all()
    expect(users).toHaveLength(1)
    expect(users[0]!.username).toBe("owner")
    expect(await verifyPassword({ hash: credential(db, res.userId)!.password!, password: res.password })).toBe(true)

    expect(inspectInvite(db, token, T0 + 2000)).toEqual({ ok: false, problem: "This link was already used." })
    await expect(acceptOwnerInvite(db, token, T0 + 2000)).rejects.toBeInstanceOf(InviteError)
    expect(db.select().from(schema.user).all()).toHaveLength(1)
  })

  it("refuses an expired link and leaves it unused", async () => {
    const db = memoryDb()
    const { token } = createOwnerInvite(db, "http://localhost:4890", T0)
    const late = T0 + INVITE_TTL_MS
    expect(inspectInvite(db, token, late)).toEqual({ ok: false, problem: "This link expired." })
    await expect(acceptOwnerInvite(db, token, late)).rejects.toBeInstanceOf(InviteError)
    expect(db.select().from(schema.user).all()).toHaveLength(0)
    expect(db.select().from(schema.ownerInvites).get()!.usedAt).toBeNull()
  })

  it("refuses a token that was never issued", () => {
    const db = memoryDb()
    expect(inspectInvite(db, "nope", T0)).toEqual({ ok: false, problem: "This link isn't valid." })
  })

  it("recovery resets the same owner's password and shuts out old sessions and passkeys", async () => {
    const db = memoryDb()
    const first = await acceptOwnerInvite(db, createOwnerInvite(db, "http://x", T0).token, T0)
    db.insert(schema.session)
      .values({ id: "ses_old", token: "t", userId: first.userId, expiresAt: new Date(T0 + 1e9), createdAt: new Date(T0), updatedAt: new Date(T0) })
      .run()
    db.insert(schema.passkey)
      .values({ id: "pk_old", publicKey: "x", userId: first.userId, credentialID: "cred", counter: 0, deviceType: "singleDevice", backedUp: false })
      .run()

    const { token } = createOwnerInvite(db, "http://x", T0 + 5000)
    expect(inspectInvite(db, token, T0 + 5000)).toMatchObject({ ok: true, recovery: true })
    const second = await acceptOwnerInvite(db, token, T0 + 6000)
    expect(second).toMatchObject({ userId: first.userId, created: false })
    expect(second.password).not.toBe(first.password)
    expect(db.select().from(schema.user).all()).toHaveLength(1)
    const hash = credential(db, first.userId)!.password!
    expect(await verifyPassword({ hash, password: second.password })).toBe(true)
    expect(await verifyPassword({ hash, password: first.password })).toBe(false)
    expect(db.select().from(schema.session).all()).toHaveLength(0)
    expect(db.select().from(schema.passkey).all()).toHaveLength(0)
  })
})
