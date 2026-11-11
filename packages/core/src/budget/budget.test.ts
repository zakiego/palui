import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { getSetting, setSetting } from "../settings"
import { activePause, addUsage, checkGlobalBudget, jobOverCap, usageKeys } from "./index"

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

describe("checkGlobalBudget", () => {
  const budgets = (db: ReturnType<typeof memoryDb>, dailyTokenCap: number) =>
    setSetting(db, "budgets", { ...getSetting(db, "budgets"), dailyTokenCap }, NOW)

  it("pauses once when the daily cap is hit, then stays quiet", () => {
    const db = memoryDb()
    setSetting(db, "timezone", TZ, NOW)
    budgets(db, 2_000_000)
    addUsage(db, usageKeys.tokens(NOW, TZ), 2_029_956, NOW)
    expect(checkGlobalBudget(db, NOW)).toMatchObject({ ok: false, fresh: true })
    expect(checkGlobalBudget(db, NOW + 1000)).toMatchObject({ ok: false, fresh: false })
  })

  it("lifts the pause as soon as the cap is raised above today's usage", () => {
    const db = memoryDb()
    setSetting(db, "timezone", TZ, NOW)
    budgets(db, 2_000_000)
    addUsage(db, usageKeys.tokens(NOW, TZ), 2_029_956, NOW)
    expect(checkGlobalBudget(db, NOW).ok).toBe(false)
    budgets(db, 10_000_000)
    expect(activePause(db, NOW)).toBeNull()
    expect(checkGlobalBudget(db, NOW).ok).toBe(true)
    expect(getSetting(db, "turnsPausedUntil")).toBeNull()
    expect(getSetting(db, "turnsPausedReason")).toBeNull()
  })

  it("names the new cap when it's lowered under a pause", () => {
    const db = memoryDb()
    setSetting(db, "timezone", TZ, NOW)
    budgets(db, 2_000_000)
    addUsage(db, usageKeys.tokens(NOW, TZ), 2_029_956, NOW)
    checkGlobalBudget(db, NOW)
    budgets(db, 1_000_000)
    const check = checkGlobalBudget(db, NOW)
    expect(check).toMatchObject({ ok: false, fresh: true })
    expect(getSetting(db, "turnsPausedReason")).toContain("1.000.000")
  })
})

describe("checkGlobalBudget at the day rollover", () => {
  it("clears the token pause once the next day starts", () => {
    const db = memoryDb()
    setSetting(db, "timezone", TZ, NOW)
    addUsage(db, usageKeys.tokens(NOW, TZ), getSetting(db, "budgets").dailyTokenCap, NOW)
    const paused = checkGlobalBudget(db, NOW)
    if (paused.ok) throw new Error("expected a pause")
    expect(checkGlobalBudget(db, paused.until).ok).toBe(true)
    expect(getSetting(db, "turnsPausedUntil")).toBeNull()
  })
})

describe("checkGlobalBudget daily token cap", () => {
  const withCap = (cap: number) => {
    const db = memoryDb()
    setSetting(db, "timezone", TZ, NOW)
    setSetting(db, "budgets", { ...getSetting(db, "budgets"), dailyTokenCap: cap }, NOW)
    return db
  }

  it("never pauses turns when the cap is 0, however many tokens were used", () => {
    const db = withCap(0)
    addUsage(db, usageKeys.tokens(NOW, TZ), 500_000_000, NOW)
    expect(checkGlobalBudget(db, NOW)).toEqual({ ok: true })
    expect(getSetting(db, "turnsPausedUntil")).toBeNull()
  })

  it("pauses turns until tomorrow once a positive cap is reached", () => {
    const db = withCap(10_000)
    addUsage(db, usageKeys.tokens(NOW, TZ), 9_999, NOW)
    expect(checkGlobalBudget(db, NOW)).toEqual({ ok: true })
    addUsage(db, usageKeys.tokens(NOW, TZ), 1, NOW)
    const check = checkGlobalBudget(db, NOW)
    expect(check).toMatchObject({ ok: false, fresh: true, until: Date.parse("2026-11-10T16:00:00Z") })
    expect(check.ok ? "" : check.reason).toContain("token per hari")
  })
})
