import { logEvent } from "@palui/core/events"
import { setConnectorStatus, upsertConnector } from "@palui/core/connectors/store"
import { memoryDb } from "@palui/db"
import { describe, expect, it } from "vitest"
import { eventStream, latestEventId, nextEvents, statusSummary } from "./events-stream"
import { TEST_MASTER_KEY } from "./test-helpers"

const telegram = { id: "telegram", kind: "channel" as const, name: "Telegram" }

describe("event polling", () => {
  it("returns rows after the cursor, oldest first, and advances it", () => {
    const db = memoryDb()
    const a = logEvent(db, { kind: "a", message: "one" })
    logEvent(db, { kind: "b", message: "two" })
    logEvent(db, { kind: "c", message: "three" })
    const first = nextEvents(db, a.id)
    expect(first.rows.map((r) => r.kind)).toEqual(["b", "c"])
    expect(first.cursor).toBe(latestEventId(db))
    expect(nextEvents(db, first.cursor)).toEqual({ rows: [], cursor: first.cursor })
  })

  it("summarizes what needs attention, ignoring drafts and chosen pauses", () => {
    const db = memoryDb()
    upsertConnector(db, { id: "draft", manifest: telegram }, TEST_MASTER_KEY)
    upsertConnector(db, { id: "telegram", manifest: telegram, enabled: true }, TEST_MASTER_KEY)
    upsertConnector(db, { id: "cliproxy", manifest: { id: "cliproxy", kind: "model", name: "CLIProxyAPI" }, enabled: false }, TEST_MASTER_KEY)
    setConnectorStatus(db, "telegram", "on")
    setConnectorStatus(db, "cliproxy", "paused")
    expect(statusSummary(db)).toEqual({ total: 2, on: 1, attention: [] })
    setConnectorStatus(db, "telegram", "error", "401 from Telegram")
    expect(statusSummary(db).attention).toEqual([{ id: "telegram", label: "Telegram", status: "error" }])
  })

  it("streams a summary first, then new rows as they're written", async () => {
    const db = memoryDb()
    const ac = new AbortController()
    const reader = eventStream(db, { intervalMs: 10, signal: ac.signal }).getReader()
    const dec = new TextDecoder()
    const read = async () => dec.decode((await reader.read()).value)
    expect(await read()).toMatch(/^event: summary\ndata: \{"total":0/)
    logEvent(db, { kind: "status", message: "on", connectorId: "telegram" })
    const next = await read()
    expect(next).toMatch(/^id: 1\nevent: events\ndata: \[\{"id":1/)
    expect(await read()).toMatch(/^event: summary/)
    ac.abort()
    expect((await reader.read()).done).toBe(true)
  })
})
