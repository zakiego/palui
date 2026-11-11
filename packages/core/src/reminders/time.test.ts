import { describe, expect, it } from "vitest"
import { RRule } from "./rrule"
import {
  describeRecurrence,
  formatEcho,
  formatWhen,
  localToUtc,
  nextOccurrence,
  normalizeRrule,
  occurrencesAfter,
  parseLocal,
  TimeError,
  tomorrowAt,
  utcToIso,
  utcToLocal,
  zoneLabel,
} from "./time"

const NY = "America/New_York"
const WITA = "Asia/Makassar"
// Tuesday 2026-11-10 10:00 WITA.
const NOW = Date.parse("2026-11-10T02:00:00Z")

describe("localToUtc", () => {
  it("shifts a wall time in the DST gap forward", () => {
    // 2026-03-08 02:30 doesn't exist in New York; clocks jump 02:00 -> 03:00.
    expect(utcToIso(localToUtc("2026-03-08T02:30", NY), NY)).toBe("2026-03-08T03:30:00.000-04:00")
  })

  it("picks the earlier instant for a wall time in the DST overlap", () => {
    // 2026-11-01 01:30 happens twice; the first one is EDT (-04:00).
    expect(new Date(localToUtc("2026-11-01T01:30", NY)).toISOString()).toBe("2026-11-01T05:30:00.000Z")
  })

  it("handles fixed zones", () => {
    expect(new Date(localToUtc("2026-11-11T09:00", WITA)).toISOString()).toBe("2026-11-11T01:00:00.000Z")
    expect(utcToLocal(Date.parse("2026-11-11T01:00:00Z"), WITA)).toBe("2026-11-11T09:00:00")
  })

  it("rejects offsets, bad dates and bad zones", () => {
    expect(() => parseLocal("2026-11-11T09:00+08:00")).toThrow(TimeError)
    expect(() => parseLocal("2026-11-31T09:00")).toThrow(TimeError)
    expect(() => localToUtc("2026-11-11T09:00", "Mars/Olympus")).toThrow(TimeError)
  })
})

describe("recurrence in wall-clock time", () => {
  it("keeps a daily 02:30 New York reminder on the wall clock across spring-forward", () => {
    const s = { rrule: "FREQ=DAILY", tz: NY, localStart: "2026-03-06T02:30" }
    const after = localToUtc("2026-03-06T12:00", NY)
    const isos = occurrencesAfter(s, after, 3).map((t) => utcToIso(t, NY))
    expect(isos).toEqual(["2026-03-07T02:30:00.000-05:00", "2026-03-08T03:30:00.000-04:00", "2026-03-09T02:30:00.000-04:00"])
  })

  it("fires a daily 01:30 New York reminder once on fall-back day, at the first 01:30", () => {
    const s = { rrule: "FREQ=DAILY", tz: NY, localStart: "2026-10-30T01:30" }
    const after = localToUtc("2026-10-31T12:00", NY)
    const isos = occurrencesAfter(s, after, 2).map((t) => utcToIso(t, NY))
    expect(isos).toEqual(["2026-11-01T01:30:00.000-04:00", "2026-11-02T01:30:00.000-05:00"])
  })

  it("keeps 08:00 local after the DST change for a weekly rule", () => {
    const s = { rrule: "FREQ=WEEKLY;BYDAY=MO", tz: NY, localStart: "2026-10-26T08:00" }
    const isos = occurrencesAfter(s, localToUtc("2026-10-26T09:00", NY), 2).map((t) => utcToIso(t, NY))
    expect(isos).toEqual(["2026-11-02T08:00:00.000-05:00", "2026-11-09T08:00:00.000-05:00"])
  })

  it("finds the last Friday of the month", () => {
    const s = { rrule: "FREQ=MONTHLY;BYDAY=-1FR", tz: WITA, localStart: "2026-11-10T17:00" }
    const locals = occurrencesAfter(s, NOW, 4).map((t) => utcToLocal(t, WITA))
    expect(locals).toEqual(["2026-11-27T17:00:00", "2026-12-25T17:00:00", "2027-01-29T17:00:00", "2027-02-26T17:00:00"])
  })

  it("skips months without a 31st (RFC 5545 / rrule semantics, not clamped to the 30th)", () => {
    const s = { rrule: "FREQ=MONTHLY;BYMONTHDAY=31", tz: WITA, localStart: "2026-10-31T09:00" }
    const locals = occurrencesAfter(s, NOW, 3).map((t) => utcToLocal(t, WITA))
    // November (30 days) and February are skipped.
    expect(locals).toEqual(["2026-12-31T09:00:00", "2027-01-31T09:00:00", "2027-03-31T09:00:00"])
  })

  it("uses BYMONTHDAY=-1 for the last day of every month", () => {
    const s = { rrule: "FREQ=MONTHLY;BYMONTHDAY=-1", tz: WITA, localStart: "2026-11-10T09:00" }
    const locals = occurrencesAfter(s, NOW, 3).map((t) => utcToLocal(t, WITA))
    expect(locals).toEqual(["2026-11-30T09:00:00", "2026-12-31T09:00:00", "2027-01-31T09:00:00"])
  })

  it("doesn't count the anchor unless it matches the rule", () => {
    // Anchor on a Tuesday, rule says Mondays.
    const s = { rrule: "FREQ=WEEKLY;BYDAY=MO", tz: WITA, localStart: "2026-11-10T08:00" }
    expect(utcToLocal(nextOccurrence(s, NOW) as number, WITA)).toBe("2026-11-16T08:00:00")
  })

  it("ends a series at COUNT and UNTIL", () => {
    expect(nextOccurrence({ rrule: "FREQ=DAILY;COUNT=2", tz: WITA, localStart: "2026-11-01T09:00" }, NOW)).toBeNull()
    expect(nextOccurrence({ rrule: "FREQ=DAILY;UNTIL=20261109T000000Z", tz: WITA, localStart: "2026-11-01T09:00" }, NOW)).toBeNull()
  })

  it("returns a one-off only when it's still ahead", () => {
    expect(nextOccurrence({ rrule: null, tz: WITA, localStart: "2026-11-10T09:00" }, NOW)).toBeNull()
    expect(nextOccurrence({ rrule: null, tz: WITA, localStart: "2026-11-10T11:00" }, NOW)).toBe(NOW + 3600_000)
  })

  it("gives the same occurrences as plain rrule when fast-forwarding an old anchor", () => {
    const cases: [string, string][] = [
      ["FREQ=HOURLY;INTERVAL=7", "2016-01-01T00:00:00"],
      ["FREQ=MINUTELY;INTERVAL=45", "2024-03-01T00:10:00"],
      ["FREQ=WEEKLY;INTERVAL=3;BYDAY=SA,TU", "2019-05-04T09:00:00"],
      ["FREQ=DAILY;INTERVAL=5;BYHOUR=8,20;BYMINUTE=0", "2021-02-03T08:00:00"],
    ]
    for (const [rrule, localStart] of cases) {
      const ours = occurrencesAfter({ rrule, tz: WITA, localStart }, NOW, 5).map((t) => utcToLocal(t, WITA))
      const plain = new RRule({ ...RRule.parseString(rrule), dtstart: new Date(`${localStart}Z`) })
        .between(new Date("2026-11-10T10:00:00Z"), new Date("2027-12-31T00:00:00Z"), false)
        .slice(0, 5)
        .map((d) => d.toISOString().slice(0, 19))
      expect(ours).toEqual(plain)
    }
  })

  it("stays fast for a dense rule anchored long ago", () => {
    const s = { rrule: "FREQ=MINUTELY;INTERVAL=5", tz: WITA, localStart: "2025-01-01T00:00" }
    const t0 = performance.now()
    expect(nextOccurrence(s, NOW)).toBe(NOW + 5 * 60_000)
    expect(performance.now() - t0).toBeLessThan(3000)
  })
})

describe("normalizeRrule", () => {
  it("sorts keys and list values and drops defaults", () => {
    expect(normalizeRrule("RRULE:byday=we,mo;freq=weekly;interval=1")).toBe("FREQ=WEEKLY;BYDAY=MO,WE")
    expect(normalizeRrule("BYDAY=+1MO;FREQ=MONTHLY;WKST=MO")).toBe("FREQ=MONTHLY;BYDAY=1MO")
  })

  it("rejects junk, DTSTART and too-frequent rules", () => {
    expect(() => normalizeRrule("FREQ=BOGUS")).toThrow(TimeError)
    expect(() => normalizeRrule("DTSTART:20261110T090000\nRRULE:FREQ=DAILY")).toThrow(TimeError)
    expect(() => normalizeRrule("FREQ=SECONDLY")).toThrow(TimeError)
    expect(() => normalizeRrule("FREQ=MINUTELY")).toThrow(TimeError)
    expect(() => normalizeRrule("FREQ=DAILY;FOO=1")).toThrow(TimeError)
    expect(() => normalizeRrule("")).toThrow(TimeError)
  })

  it("rejects rules whose BY parts fire more often than every 5 minutes", () => {
    expect(() => normalizeRrule("FREQ=DAILY;BYSECOND=0,1,2,3,4,5,6,7,8,9,10")).toThrow(/every 5 minutes/)
    const everyMinute = Array.from({ length: 60 }, (_, i) => i).join(",")
    expect(() => normalizeRrule(`FREQ=HOURLY;BYMINUTE=${everyMinute}`)).toThrow(/every 5 minutes/)
    expect(() => normalizeRrule("FREQ=DAILY;BYHOUR=8;BYMINUTE=0,2")).toThrow(/every 5 minutes/)
    // Exactly 5 minutes apart is fine.
    expect(normalizeRrule("FREQ=DAILY;BYHOUR=8;BYMINUTE=0,5")).toBe("FREQ=DAILY;BYHOUR=8;BYMINUTE=0,5")
    expect(normalizeRrule("FREQ=MINUTELY;INTERVAL=5")).toBe("FREQ=MINUTELY;INTERVAL=5")
  })

  it("rejects a rule that never fires, quickly", () => {
    const t0 = performance.now()
    expect(() => normalizeRrule("FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30")).toThrow(/never fires/)
    expect(() => normalizeRrule("FREQ=MINUTELY;INTERVAL=5;BYMONTH=4;BYMONTHDAY=31")).toThrow(/never fires/)
    expect(performance.now() - t0).toBeLessThan(500)
  })

  it("keeps sparse but real rules, and finds their next run fast", () => {
    expect(normalizeRrule("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29")).toBe("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29")
    const s = { rrule: "FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=29", tz: WITA, localStart: "2026-11-10T00:00" }
    const t0 = performance.now()
    expect(utcToLocal(nextOccurrence(s, NOW) as number, WITA)).toBe("2028-02-29T00:00:00")
    expect(performance.now() - t0).toBeLessThan(500)
  })

  it("still lets a finished series (UNTIL in the past) be read", () => {
    const s = { rrule: "FREQ=DAILY;UNTIL=20200101T000000", tz: WITA, localStart: "2019-12-30T08:00" }
    expect(nextOccurrence(s, NOW)).toBeNull()
  })
})

describe("echo formatting", () => {
  it("formats one-offs relative to now", () => {
    expect(formatWhen(localToUtc("2026-11-10T14:30", WITA), WITA, NOW)).toBe("Hari ini, 14.30 WITA")
    expect(formatWhen(localToUtc("2026-11-11T09:00", WITA), WITA, NOW)).toBe("Besok, Rabu 11 Nov, 09.00 WITA")
    expect(formatWhen(localToUtc("2026-11-16T08:00", WITA), WITA, NOW)).toBe("Senin 16 Nov, 08.00 WITA")
    expect(formatWhen(localToUtc("2027-01-01T09:00", WITA), WITA, NOW)).toBe("Jumat 1 Jan 2027, 09.00 WITA")
  })

  it("labels Indonesian zones and falls back to Luxon's short name", () => {
    expect(zoneLabel("Asia/Jakarta", NOW)).toBe("WIB")
    expect(zoneLabel("Asia/Makassar", NOW)).toBe("WITA")
    expect(zoneLabel("Asia/Jayapura", NOW)).toBe("WIT")
    // NOW is Monday 21:00 in New York, so Tuesday is "Besok" there.
    expect(formatWhen(localToUtc("2026-11-10T09:00", NY), NY, NOW)).toBe("Besok, Selasa 10 Nov, 09.00 EST")
  })

  it("describes recurring rules", () => {
    const d = (rrule: string, localStart = "2026-11-16T08:00") => describeRecurrence({ rrule, tz: WITA, localStart }, NOW)
    expect(d("FREQ=WEEKLY;BYDAY=MO")).toBe("Tiap Senin, 08.00 WITA")
    expect(d("FREQ=WEEKLY")).toBe("Tiap Senin, 08.00 WITA")
    expect(d("FREQ=DAILY", "2026-11-11T07:00")).toBe("Tiap hari, 07.00 WITA")
    expect(d("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR")).toBe("Tiap hari kerja, 08.00 WITA")
    expect(d("FREQ=WEEKLY;BYDAY=MO,WE")).toBe("Tiap Senin dan Rabu, 08.00 WITA")
    expect(d("FREQ=WEEKLY;INTERVAL=2;BYDAY=FR")).toBe("Tiap 2 minggu, Jumat, 08.00 WITA")
    expect(d("FREQ=MONTHLY;BYDAY=-1FR", "2026-11-27T17:00")).toBe("Tiap Jumat terakhir, 17.00 WITA")
    expect(d("FREQ=MONTHLY;BYMONTHDAY=1", "2026-12-01T09:00")).toBe("Tiap tanggal 1, 09.00 WITA")
    expect(d("FREQ=MONTHLY;BYMONTHDAY=-1", "2026-11-30T09:00")).toBe("Tiap akhir bulan, 09.00 WITA")
    expect(d("FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17", "2027-08-17T09:00")).toBe("Tiap 17 Agu, 09.00 WITA")
    expect(d("FREQ=HOURLY;INTERVAL=2")).toBe("Tiap 2 jam")
    expect(d("FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0")).toBe("Tiap hari, 08.00 dan 20.00 WITA")
  })

  it("uses the rule echo for recurring and the date echo for one-offs", () => {
    const at = localToUtc("2026-11-11T09:00", WITA)
    expect(formatEcho({ rrule: null, tz: WITA, localStart: "2026-11-11T09:00" }, at, NOW)).toBe("Besok, Rabu 11 Nov, 09.00 WITA")
    expect(formatEcho({ rrule: "FREQ=DAILY", tz: WITA, localStart: "2026-11-11T09:00" }, at, NOW)).toBe("Tiap hari, 09.00 WITA")
  })
})

describe("tomorrowAt", () => {
  it("is tomorrow 09:00 in the zone, even late at night", () => {
    // 23:30 WITA on Tuesday -> Wednesday 09:00 WITA.
    const lateNight = localToUtc("2026-11-10T23:30", WITA)
    expect(utcToLocal(tomorrowAt(lateNight, WITA), WITA)).toBe("2026-11-11T09:00:00")
    // 00:30 WITA Wednesday is still "Tuesday night" in UTC; tomorrow is Thursday.
    expect(utcToLocal(tomorrowAt(localToUtc("2026-11-11T00:30", WITA), WITA), WITA)).toBe("2026-11-12T09:00:00")
  })
})
