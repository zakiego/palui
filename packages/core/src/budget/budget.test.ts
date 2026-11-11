import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { addUsage, jobOverCap, usageKeys } from "./index"

const NOW = Date.parse("2026-11-10T02:00:00Z")
const TZ = "Asia/Makassar"

describe("jobOverCap", () => {
  it("treats a cap of 0 as unlimited, like the job runner", () => {
    const db = memoryDb()
    expect(jobOverCap(db, "job_1", 0, NOW, TZ)).toBe(false)
    addUsage(db, usageKeys.job("job_1", NOW, TZ), 50_000, NOW)
    expect(jobOverCap(db, "job_1", 0, NOW, TZ)).toBe(false)
  })

  it("is over once the day's usage reaches a positive cap", () => {
    const db = memoryDb()
    expect(jobOverCap(db, "job_1", 1000, NOW, TZ)).toBe(false)
    addUsage(db, usageKeys.job("job_1", NOW, TZ), 1000, NOW)
    expect(jobOverCap(db, "job_1", 1000, NOW, TZ)).toBe(true)
  })
})
