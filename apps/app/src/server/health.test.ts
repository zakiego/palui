import { upsertConnector } from "@palui/core/saklar/store"
import { memoryDb } from "@palui/db"
import { describe, expect, it } from "vitest"
import { health } from "./health"
import { TEST_MASTER_KEY } from "./test-helpers"

describe("/health", () => {
  it("is 200 with the runtime and connector state in the body", async () => {
    const db = memoryDb()
    upsertConnector(db, { id: "telegram", manifest: { id: "telegram", kind: "channel", name: "Telegram" } }, TEST_MASTER_KEY)
    const res = await health(db, async () => ({ ok: true, schema: "0000" }))
    expect(res.status).toBe(200)
    expect(await res.json()).toMatchObject({
      ok: true,
      schema: expect.any(String),
      runtime: { ok: true, schema: "0000" },
      connectors: [{ id: "telegram", status: "not_set_up" }],
    })
  })

  it("stays 200 when the runtime is down", async () => {
    const db = memoryDb()
    const res = await health(db, async () => {
      throw new Error("ECONNREFUSED")
    })
    expect(res.status).toBe(200)
    expect(await res.json()).toMatchObject({ ok: true, runtime: "unreachable", connectors: [] })
  })

  it("answers 503 when a newer build migrated the database", async () => {
    const db = memoryDb()
    db.$client.prepare("INSERT INTO _migrations (name, applied_at) VALUES ('9999_from_the_future', 1)").run()
    const res = await health(db, async () => ({ ok: true, schema: "x" }))
    expect(res.status).toBe(503)
    const body = await res.json()
    expect(body).toMatchObject({ ok: false, error: "db_newer_than_app", unknownMigrations: ["9999_from_the_future"] })
    expect(body.message).toMatch(/newer version migrated it/)
  })
})
