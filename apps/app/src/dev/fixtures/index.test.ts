import { upsertConnector } from "@palui/core/connectors/store"
import { memoryDb, schema, type DB } from "@palui/db"
import { sql } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { TEST_MASTER_KEY } from "@/server/test-helpers"
import { applyFixture, currentFixture, FIXTURE_NAMES, holdsRealData } from "./index"
import { HUGE } from "./huge"

const count = (db: DB, table: Parameters<DB["delete"]>[0]) => Number(db.select({ n: sql<number>`count(*)` }).from(table).get()?.n ?? 0)

function addOwner(db: DB) {
  const now = new Date()
  db.insert(schema.user).values({ id: "user_owner", name: "Owner", email: "owner@palui.invalid", username: "owner", createdAt: now, updatedAt: now }).run()
  db.insert(schema.passkey).values({ id: "pk_real", name: "Real key", publicKey: "x", userId: "user_owner", credentialID: "cred_real", counter: 0, deviceType: "multiDevice", backedUp: true }).run()
}

describe("dev fixtures", () => {
  // Applying is the test: `worst` throws if one of its values is over the limit
  // the dashboard's own forms accept.
  it.each(FIXTURE_NAMES)("applies %s and records it", (name) => {
    const db = memoryDb()
    addOwner(db)
    applyFixture(db, name)
    expect(currentFixture(db)).toBe(name)
  })

  it("replaces the previous fixture instead of adding to it", () => {
    const db = memoryDb()
    applyFixture(db, "demo")
    expect(count(db, schema.connectors)).toBeGreaterThan(0)
    applyFixture(db, "empty")
    expect(count(db, schema.connectors)).toBe(0)
    expect(count(db, schema.messages)).toBe(0)
    expect(count(db, schema.events)).toBe(0)
  })

  it("keeps the owner, and only removes the passkeys it added", () => {
    const db = memoryDb()
    addOwner(db)
    applyFixture(db, "worst")
    expect(count(db, schema.passkey)).toBeGreaterThan(1)
    applyFixture(db, "empty")
    expect(count(db, schema.user)).toBe(1)
    expect(db.select({ id: schema.passkey.id }).from(schema.passkey).all()).toEqual([{ id: "pk_real" }])
  })

  it("refuses a database with data that no fixture put there", () => {
    const db = memoryDb()
    upsertConnector(db, { id: "telegram", manifest: { id: "telegram", kind: "channel", name: "Telegram" } }, TEST_MASTER_KEY)
    expect(holdsRealData(db)).toBe(true)
    expect(() => applyFixture(db, "worst")).toThrow(/didn't come from a fixture/)
    expect(count(db, schema.connectors)).toBe(1)
    applyFixture(db, "worst", { force: true })
    expect(currentFixture(db)).toBe("worst")
  })

  it("fills the huge fixture past every list limit", () => {
    const db = memoryDb()
    applyFixture(db, "huge")
    expect(count(db, schema.chats)).toBe(HUGE.chats + 1)
    expect(count(db, schema.reminders)).toBe(HUGE.upcoming + HUGE.recurring + HUGE.done)
    expect(count(db, schema.events)).toBeGreaterThanOrEqual(HUGE.events)
  })
})
