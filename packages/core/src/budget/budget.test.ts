import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { getSetting, setSetting } from "../settings"
import { activePause, addUsage, agentTokensToday, budgetTokens, checkGlobalBudget, jobOverCap, recordAgentTokens, recordRunTokens, tokensToday, usageKeys } from "./index"

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

describe("budgetTokens: what a run counts against the daily caps", () => {
  it("counts cache reads at a tenth, and fresh input, cache writes and output in full", () => {
    // 52,000 in: 40,000 read from the cache, 2,000 written to it, 10,000 fresh. 800 out.
    expect(budgetTokens({ tokensIn: 52_000, tokensOut: 800, tokensCached: 40_000 })).toBe(12_000 + 4_000 + 800)
    // Nothing cached: every token counts, as before.
    expect(budgetTokens({ tokensIn: 52_000, tokensOut: 800, tokensCached: 0 })).toBe(52_800)
    expect(budgetTokens({ tokensIn: 52_000, tokensOut: 800 })).toBe(52_800)
    // Everything cached still costs its tenth.
    expect(budgetTokens({ tokensIn: 50_000, tokensOut: 0, tokensCached: 50_000 })).toBe(5_000)
  })

  it("rounds the cached share and never trusts a cached count above the input", () => {
    expect(budgetTokens({ tokensIn: 15, tokensOut: 0, tokensCached: 15 })).toBe(2)
    expect(budgetTokens({ tokensIn: 100, tokensOut: 5, tokensCached: 900 })).toBe(15)
    expect(budgetTokens({ tokensIn: 100, tokensOut: 5, tokensCached: -3 })).toBe(105)
    expect(budgetTokens({ tokensIn: 100, tokensOut: 5, tokensCached: null })).toBe(105)
  })

  it("is what the 2026-11-11 measurement would have counted: about 813k, not 1.96M", () => {
    // 64 runs, 1,958,748 input tokens of which 1,275,047 were cache reads.
    const counted = budgetTokens({ tokensIn: 1_958_748, tokensOut: 0, tokensCached: 1_275_047 })
    expect(counted).toBe(683_701 + 127_505)
    const db = memoryDb()
    setSetting(db, "timezone", TZ, NOW)
    recordRunTokens(db, counted, undefined, NOW)
    recordAgentTokens(db, "agent_1", counted, NOW)
    expect(tokensToday(db, NOW)).toBe(811_206)
    expect(agentTokensToday(db, "agent_1", NOW)).toBe(811_206)
    expect(checkGlobalBudget(db, NOW)).toEqual({ ok: true })
  })
})
