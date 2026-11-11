import { describe, expect, it } from "vitest"
import { translate, type Key } from "@/lib/i18n"
import type { Config } from "./parts"
import { daysText, reAskText, triggerText } from "./summary"

const t = (key: Key, vars?: Record<string, string | number>) => translate("en", key, vars)
const id = (key: Key, vars?: Record<string, string | number>) => translate("id", key, vars)

const base = { tz: "Asia/Makassar", days: null, recap: { kind: "off" }, lateGraceMin: 60, action: { kind: "message", text: "x" } } as unknown as Config
const rrule = (rule: string, start = "2026-10-02T16:00") => ({ ...base, source: { kind: "rrule", rrule: rule, start } }) as unknown as Config

describe("routine summary sentences", () => {
  it("reads common repeat rules, with BYHOUR and BYMINUTE over the start time", () => {
    expect(triggerText(t, rrule("FREQ=WEEKLY;BYDAY=FR"))).toBe("Every Fri at 16:00")
    expect(triggerText(t, rrule("FREQ=DAILY;BYHOUR=7,19;BYMINUTE=0"))).toBe("Every day at 07:00, 19:00")
    expect(triggerText(t, rrule("FREQ=MONTHLY;BYMONTHDAY=1", "2026-11-01T09:00"))).toBe("Every month on day 1 at 09:00")
    expect(triggerText(t, rrule("FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH"))).toBe("Every 2 weeks on Mon, Thu at 16:00")
    expect(triggerText(t, rrule("FREQ=MINUTELY;INTERVAL=30"))).toBe("Repeats")
    expect(triggerText(id, rrule("FREQ=WEEKLY;BYDAY=FR"))).toBe("Setiap Jum jam 16:00")
  })

  it("reads fixed times, active days and tool sources", () => {
    expect(triggerText(t, { ...base, days: ["MO", "TU", "WE", "TH", "FR"], source: { kind: "times", times: [{ time: "09:00" }, { time: "14:00" }] } } as unknown as Config)).toBe("Weekdays at 09:00, 14:00")
    expect(daysText(t, ["SA", "SU"])).toBe("Weekends")
    expect(daysText(t, ["MO", "WE"])).toBe("Mon, Wed")
    const tool = { ...base, source: { kind: "tool", connectorId: "mcp-jadwal-sholat", tool: "get", args: {}, refreshAt: "00:05", items: [{ field: "times.fajr", label: "Subuh", offsetMin: 0 }, { field: "times.isha", offsetMin: 0 }] } } as unknown as Config
    expect(triggerText(t, tool)).toBe("Every day, at the times mcp-jadwal-sholat gives for Subuh, times.isha")
  })

  it("describes re-asks, including asking once", () => {
    const checkin = (followUp: { everyMin: number; max: number }, deadline: { kind: string; minutes?: number }) => ({ ...base, action: { kind: "checkin", text: "x", answers: [], followUp, deadline } }) as unknown as Config
    expect(reAskText(t, checkin({ everyMin: 15, max: 4 }, { kind: "next" }))).toBe("Asks again every 15 min, up to 4 times, until the next one")
    expect(reAskText(t, checkin({ everyMin: 15, max: 0 }, { kind: "after", minutes: 120 }))).toBe("Asks once, waits for 120 min")
    expect(reAskText(t, base)).toBeNull()
  })
})
