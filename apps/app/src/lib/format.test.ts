import { describe, expect, it } from "vitest"
import { clip, clockTime, duration, inOtherZone, inZone, noBreak } from "./format"

describe("clockTime", () => {
  it("joins the time and AM or PM with a non-breaking space", () => {
    // A local timestamp, so the output is the same in every zone.
    const at = new Date(2026, 10, 11, 3, 42).getTime()
    expect(clockTime(at)).toBe("3:42\u00A0AM")
  })
})

describe("noBreak", () => {
  it("swaps a plain space for a non-breaking one", () => {
    expect(noBreak("3:42\u0020AM")).toBe("3:42\u00A0AM")
  })

  it("swaps a narrow no-break space for a non-breaking one", () => {
    expect(noBreak("3:42\u202FAM")).toBe("3:42\u00A0AM")
  })
})

describe("inZone", () => {
  const now = Date.UTC(2026, 10, 11, 4, 0)

  it("leaves the year off inside the current year", () => {
    expect(inZone(Date.UTC(2026, 10, 11, 1, 0), "Asia/Makassar", undefined, now)).toBe("Wed 11 Nov, 09:00")
  })

  it("adds the year when the date is in another one", () => {
    expect(inZone(Date.UTC(2030, 0, 1, 7, 30), "America/Los_Angeles", undefined, now)).toBe("Mon, 31 Dec 2029, 23:30")
    expect(inZone(0, "UTC", undefined, now)).toBe("Thu, 1 Jan 1970, 00:00")
  })

  it("adds the year to a custom date format too, but not to a time-only one", () => {
    expect(inZone(0, "UTC", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }, now)).toBe("1 Jan 1970, 00:00")
    expect(inZone(0, "UTC", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }, now)).toBe("00:00")
  })

  it("falls back to the local zone for an unknown zone, and to nothing for an impossible time", () => {
    expect(inZone(now, "Not/AZone", undefined, now)).not.toBe("")
    expect(inZone(Number.NaN, "UTC", undefined, now)).toBe("")
    expect(inZone(9e15, "UTC", undefined, now)).toBe("")
  })
})

describe("inOtherZone", () => {
  const at = Date.UTC(2026, 10, 11, 4, 0)

  it("is null when the row is in the owner's zone, or in one with the same clock", () => {
    expect(inOtherZone(at, "Asia/Makassar", "Asia/Makassar", at)).toBeNull()
    expect(inOtherZone(at, "Asia/Singapore", "Asia/Makassar", at)).toBeNull()
  })

  it("gives the row's own clock time and zone when they differ", () => {
    expect(inOtherZone(at, "America/Los_Angeles", "Asia/Makassar", at)).toBe("20:00 America/Los_Angeles")
  })
})

describe("duration", () => {
  it("grows the unit with the value", () => {
    expect(duration(null)).toBe("")
    expect(duration(0)).toBe("0 ms")
    expect(duration(999)).toBe("999 ms")
    expect(duration(8342)).toBe("8.3 s")
    expect(duration(42_000)).toBe("42 s")
    expect(duration(59_999)).toBe("1 min")
    expect(duration(15 * 60_000)).toBe("15 min")
    expect(duration(3_723_000)).toBe("1 h 2 min")
    expect(duration(2 * 3600_000)).toBe("2 h")
    expect(duration(86_400_000)).toBe("1 d")
    expect(duration(3 * 86_400_000 + 4 * 3600_000)).toBe("3 d 4 h")
  })
})

// Half of a surrogate pair: what a plain slice() leaves when it cuts an emoji.
const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/

describe("clip", () => {
  it("returns a short text as is", () => {
    expect(clip("ok sip", 140)).toBe("ok sip")
  })

  it("never cuts through an emoji", () => {
    const text = `Wkwkwk ${"\u{1F602}".repeat(80)}`
    const cut = clip(text, 140)
    expect(cut.length).toBeLessThanOrEqual(140)
    expect(LONE_SURROGATE.test(cut)).toBe(false)
    expect(LONE_SURROGATE.test(text.slice(0, 140))).toBe(true)
  })

  it("keeps a joined emoji whole or drops it", () => {
    expect(clip("ab\u{1F469}\u{1F3FD}\u200D\u{1F4BB}cd", 4)).toBe("ab")
  })
})
