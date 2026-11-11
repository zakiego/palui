import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { getSetting, setSetting } from "./settings"

const NOW = Date.parse("2026-11-10T02:00:00Z")

describe("setSetting", () => {
  it("clears a nullable key back to its default", () => {
    const db = memoryDb()
    setSetting(db, "turnsPausedUntil", NOW + 1000, NOW)
    expect(getSetting(db, "turnsPausedUntil")).toBe(NOW + 1000)
    setSetting(db, "turnsPausedUntil", null, NOW)
    expect(getSetting(db, "turnsPausedUntil")).toBeNull()
    setSetting(db, "turnsPausedReason", null, NOW)
    expect(getSetting(db, "turnsPausedReason")).toBeNull()
  })
})
