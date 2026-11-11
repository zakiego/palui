// Wall-clock recurrence (PLAN 6). A schedule is { rrule, tz, localStart }:
//   rrule       RRULE body without DTSTART ("FREQ=WEEKLY;BYDAY=MO"), or null for one-off
//   tz          IANA zone
//   localStart  ISO local datetime without offset: the series anchor and time of day
//
// rrule runs in floating mode: local wall time is treated as if it were UTC, so
// "every Monday 08:00" stays 08:00 across DST changes. Each floating result is then
// placed in the zone here, with Luxon, never with rrule's TZID support.
//   DST gap (02:30 that doesn't exist): shifted forward, Luxon's default (03:30).
//   DST overlap (01:30 that happens twice): the earlier instant (the first offset).
import { DateTime, IANAZone } from "luxon"
import { RRule } from "./rrule"

export type Schedule = { rrule: string | null; tz: string; localStart: string }

export type LocalParts = { year: number; month: number; day: number; hour: number; minute: number; second: number }

export class TimeError extends Error {}

const HOUR = 3600_000
const DAY = 24 * HOUR
// Most occurrences rrule may produce per query window. Together with the minimum
// frequency check in normalizeRrule this bounds every loop here.
const MAX_RESULTS = 2000

export const WEEKDAYS_ID = ["Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu", "Minggu"] as const
export const MONTHS_ID = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"] as const
const RRULE_DAYS = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"] as const

// --- Zones and local datetimes ---------------------------------------------------------

export function isValidZone(tz: string): boolean {
  return IANAZone.isValidZone(tz)
}

export function assertZone(tz: string): void {
  if (!isValidZone(tz)) throw new TimeError(`Unknown time zone "${tz}". Use an IANA name like Asia/Makassar.`)
}

const LOCAL_RE = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.\d+)?)?)?$/

// Parses "2026-11-11T09:00", "2026-11-11T09:00:00" or a bare date (09:00 is NOT
// assumed here; a bare date means midnight). Offsets and "Z" are rejected: a
// reminder time is always wall-clock time in its own zone.
export function parseLocal(iso: string): LocalParts {
  const m = LOCAL_RE.exec(iso.trim())
  if (!m) throw new TimeError(`"${iso}" isn't a local date-time. Use YYYY-MM-DDTHH:mm without an offset.`)
  const p: LocalParts = {
    year: Number(m[1]),
    month: Number(m[2]),
    day: Number(m[3]),
    hour: Number(m[4] ?? 0),
    minute: Number(m[5] ?? 0),
    second: Number(m[6] ?? 0),
  }
  const check = DateTime.fromObject(p, { zone: "utc" })
  if (!check.isValid || check.day !== p.day) throw new TimeError(`"${iso}" isn't a real date.`)
  return p
}

const pad = (n: number, w = 2) => String(n).padStart(w, "0")

export function formatLocal(p: LocalParts): string {
  return `${pad(p.year, 4)}-${pad(p.month)}-${pad(p.day)}T${pad(p.hour)}:${pad(p.minute)}:${pad(p.second)}`
}

export function normalizeLocal(iso: string): string {
  return formatLocal(parseLocal(iso))
}

function toFloating(p: LocalParts): Date {
  return new Date(Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second))
}

function fromFloating(d: Date): LocalParts {
  return {
    year: d.getUTCFullYear(),
    month: d.getUTCMonth() + 1,
    day: d.getUTCDate(),
    hour: d.getUTCHours(),
    minute: d.getUTCMinutes(),
    second: d.getUTCSeconds(),
  }
}

// Wall time in `tz` -> UTC ms. See the header for the gap and overlap rules.
export function localToUtc(local: string | LocalParts, tz: string): number {
  assertZone(tz)
  const p = typeof local === "string" ? parseLocal(local) : local
  const asUtc = Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second)
  const zone = IANAZone.create(tz)
  // Every offset in effect within a day of this wall time is a candidate; keep the
  // ones that really map back to this wall time and take the earliest instant.
  let best: number | null = null
  for (const probe of [asUtc - DAY, asUtc, asUtc + DAY]) {
    const off = zone.offset(probe)
    const t = asUtc - off * 60_000
    if (zone.offset(t) === off && (best === null || t < best)) best = t
  }
  if (best !== null) return best
  // The wall time falls in a DST gap: Luxon shifts it forward by the gap.
  return DateTime.fromObject(p, { zone: tz }).toMillis()
}

export function utcToLocalParts(ms: number, tz: string): LocalParts {
  const d = DateTime.fromMillis(ms, { zone: tz })
  return { year: d.year, month: d.month, day: d.day, hour: d.hour, minute: d.minute, second: d.second }
}

export function utcToLocal(ms: number, tz: string): string {
  return formatLocal(utcToLocalParts(ms, tz))
}

// ISO with the zone's offset, e.g. "2026-11-11T09:00:00.000+08:00".
export function utcToIso(ms: number, tz: string): string {
  return DateTime.fromMillis(ms, { zone: tz }).toISO() ?? new Date(ms).toISOString()
}

// --- RRULE ---------------------------------------------------------------------------

function splitRrule(body: string): [string, string][] {
  return body
    .split(";")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((part) => {
      const i = part.indexOf("=")
      if (i < 1) throw new TimeError(`Bad RRULE part "${part}".`)
      return [part.slice(0, i).toUpperCase(), part.slice(i + 1).toUpperCase()] as [string, string]
    })
}

const LIST_KEYS = new Set(["BYDAY", "BYMONTH", "BYMONTHDAY", "BYHOUR", "BYMINUTE", "BYSECOND", "BYSETPOS", "BYYEARDAY", "BYWEEKNO"])

function sortListValue(key: string, value: string): string {
  if (!LIST_KEYS.has(key)) return value
  const items = value.split(",").filter(Boolean)
  if (key === "BYDAY") {
    const rank = (v: string) => {
      const m = /^([+-]?\d+)?([A-Z]{2})$/.exec(v)
      const day = RRULE_DAYS.indexOf((m?.[2] ?? "MO") as (typeof RRULE_DAYS)[number])
      return day * 1000 + Number(m?.[1] ?? 0)
    }
    return [...new Set(items.map((v) => v.replace(/^\+/, "")))].sort((a, b) => rank(a) - rank(b)).join(",")
  }
  return [...new Set(items.map(Number))].sort((a, b) => a - b).join(",")
}

const KEY_ORDER = ["FREQ", "INTERVAL", "BYMONTH", "BYWEEKNO", "BYYEARDAY", "BYMONTHDAY", "BYDAY", "BYHOUR", "BYMINUTE", "BYSECOND", "BYSETPOS", "COUNT", "UNTIL", "WKST"]

const MIN_GAP_MS = 5 * 60_000
// Occurrences the probe looks at for the minimum gap.
const PROBE_COUNT = 50
// rrule only gives up on a rule that never matches (FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30)
// when it reaches year 9999, which takes seconds and blocks the event loop. The probe
// starts 12 years before that end, so such a rule is found out in a few milliseconds.
const PROBE_START = Date.UTC(9988, 0, 1)

// Rejects rules that fire more often than every 5 minutes (FREQ=DAILY;BYSECOND=0,30
// passes the FREQ/INTERVAL check but fires twice a minute) and rules that never fire
// (or fire less than once in 12 years). COUNT and UNTIL are ignored here: they only
// end a series, and the real anchor isn't known yet.
function probeRrule(opts: ReturnType<typeof RRule.parseString>, s: string): void {
  const { count: _count, until: _until, ...rest } = opts
  const probe = new RRule({ ...rest, dtstart: new Date(PROBE_START), tzid: null })
  const dates = probe.all((_d, i) => i < PROBE_COUNT)
  if (dates.length === 0) throw new TimeError(`RRULE "${s}" never fires.`)
  for (let i = 1; i < dates.length; i++) {
    if ((dates[i] as Date).getTime() - (dates[i - 1] as Date).getTime() < MIN_GAP_MS) {
      throw new TimeError("Reminders can repeat at most every 5 minutes.")
    }
  }
}

// normalizeRrule runs on every occurrence query, and the probe isn't free.
const normalized = new Map<string, string>()

// Validates an RRULE body and returns its canonical form: known keys only, keys in
// a fixed order, list values sorted, INTERVAL=1 and WKST=MO dropped. Throws a
// TimeError with a readable message when the rule is unusable.
export function normalizeRrule(body: string): string {
  const hit = normalized.get(body)
  if (hit !== undefined) return hit
  const out = normalizeRruleUncached(body)
  if (normalized.size >= 1000) normalized.clear()
  normalized.set(body, out)
  return out
}

function normalizeRruleUncached(body: string): string {
  const s = body.trim().replace(/^RRULE:/i, "")
  if (!s) throw new TimeError("Empty RRULE.")
  if (/DTSTART|TZID|\n/i.test(s)) throw new TimeError("Give the RRULE body only, without DTSTART or TZID.")
  const parts = splitRrule(s)
  const keys = new Set<string>()
  for (const [k] of parts) {
    if (!KEY_ORDER.includes(k)) throw new TimeError(`Unsupported RRULE key ${k}.`)
    if (keys.has(k)) throw new TimeError(`RRULE key ${k} appears twice.`)
    keys.add(k)
  }
  let opts: ReturnType<typeof RRule.parseString>
  try {
    opts = RRule.parseString(s)
  } catch (e) {
    throw new TimeError(`Bad RRULE "${s}": ${(e as Error).message}`)
  }
  if (opts.freq === undefined || opts.freq === null) throw new TimeError(`RRULE "${s}" needs a valid FREQ.`)
  if (opts.freq === RRule.SECONDLY) throw new TimeError("Reminders can't repeat every few seconds.")
  if (opts.freq === RRule.MINUTELY && (opts.interval ?? 1) < 5) throw new TimeError("Reminders can repeat at most every 5 minutes.")
  if (opts.interval !== undefined && opts.interval !== null && opts.interval < 1) throw new TimeError("INTERVAL must be at least 1.")
  const kept = parts
    .filter(([k, v]) => !(k === "INTERVAL" && Number(v) === 1) && !(k === "WKST" && v === "MO"))
    .map(([k, v]) => [k, sortListValue(k, v)] as [string, string])
    .sort((a, b) => KEY_ORDER.indexOf(a[0]) - KEY_ORDER.indexOf(b[0]))
  const canonical = kept.map(([k, v]) => `${k}=${v}`).join(";")
  probeRrule(RRule.parseString(canonical), s)
  return canonical
}

function rruleParts(body: string): Record<string, string> {
  return Object.fromEntries(splitRrule(body))
}

// rrule iterates from DTSTART on every query, so a minutely or hourly series
// anchored long ago gets slower every day. For rules whose period is a fixed span
// of floating time (MINUTELY, HOURLY, DAILY, WEEKLY times INTERVAL) and that have
// no COUNT, moving DTSTART forward by whole periods yields the same occurrences.
const FIXED_PERIOD_MS: Partial<Record<number, number>> = {
  [RRule.MINUTELY]: 60_000,
  [RRule.HOURLY]: HOUR,
  [RRule.DAILY]: DAY,
  [RRule.WEEKLY]: 7 * DAY,
}

function ruleFor(s: Schedule & { rrule: string }, windowStart?: number): RRule {
  const opts = RRule.parseString(normalizeRrule(s.rrule))
  let dtstart = toFloating(parseLocal(s.localStart)).getTime()
  const unit = opts.freq !== undefined ? FIXED_PERIOD_MS[opts.freq] : undefined
  if (windowStart !== undefined && unit && !opts.count) {
    const period = unit * (opts.interval ?? 1)
    const target = windowStart - 2 * DAY - period
    if (target > dtstart) dtstart += Math.floor((target - dtstart) / period) * period
  }
  // UNTIL is read as floating local time too (it went through the same parser).
  return new RRule({ ...opts, dtstart: new Date(dtstart), tzid: null })
}

// The next `n` fire instants strictly after `afterMs`, in order.
export function occurrencesAfter(s: Schedule, afterMs: number, n: number): number[] {
  assertZone(s.tz)
  if (!s.rrule) {
    const t = localToUtc(s.localStart, s.tz)
    return t > afterMs && n > 0 ? [t] : []
  }
  const out: number[] = []
  // Start a little before `after` in wall time: around DST changes, a wall time
  // before the local "now" may still be in the future.
  let from = toFloating(utcToLocalParts(afterMs, s.tz)).getTime() - 26 * HOUR
  const rule = ruleFor({ ...s, rrule: s.rrule }, from)
  // Rules that never fire are rejected by normalizeRrule (inside ruleFor), so the long
  // spans only matter for sparse rules, and the iterator stops as soon as `n`
  // occurrences past `afterMs` are in hand.
  for (const span of [3 * DAY, 400 * DAY, 40 * 366 * DAY]) {
    const to = from + span
    let seen = 0
    rule.between(new Date(from), new Date(to), true, (d) => {
      if (++seen > MAX_RESULTS) return false
      const t = localToUtc(fromFloating(d), s.tz)
      if (t > afterMs && (out.length === 0 || t > (out.at(-1) as number))) out.push(t)
      return out.length < n
    })
    if (out.length >= n || n <= 0) return out.slice(0, Math.max(n, 0))
    from = to
  }
  return out
}

// The next fire instant strictly after `afterMs`, or null when the series is over.
export function nextOccurrence(s: Schedule, afterMs: number): number | null {
  return occurrencesAfter(s, afterMs, 1)[0] ?? null
}

// The latest occurrence in [fromMs, atMs], scanning forward from fromMs (which is
// itself an occurrence). Bounded: after 10 000 steps it returns the last one seen.
export function latestAtOrBefore(s: Schedule, fromMs: number, atMs: number): number {
  let last = fromMs
  for (let i = 0; i < 100; i++) {
    const batch = occurrencesAfter(s, last, 100)
    for (const t of batch) {
      if (t > atMs) return last
      last = t
    }
    if (batch.length < 100) return last
  }
  return last
}

// --- Formatting (Indonesian echo) ------------------------------------------------------

const ZONE_LABELS: Record<string, string> = {
  "Asia/Jakarta": "WIB",
  "Asia/Pontianak": "WIB",
  "Asia/Makassar": "WITA",
  "Asia/Jayapura": "WIT",
}

export function zoneLabel(tz: string, ms: number): string {
  return ZONE_LABELS[tz] ?? DateTime.fromMillis(ms, { zone: tz }).offsetNameShort ?? tz
}

// "09.00"
export function formatClock(ms: number, tz: string): string {
  return DateTime.fromMillis(ms, { zone: tz }).toFormat("HH.mm")
}

function dayDiff(ms: number, tz: string, now: number): number {
  const a = DateTime.fromMillis(ms, { zone: tz }).startOf("day")
  const b = DateTime.fromMillis(now, { zone: tz }).startOf("day")
  return Math.round(a.diff(b, "days").days)
}

// "Senin 16 Nov" (with the year when it isn't this year).
export function formatDate(ms: number, tz: string, now: number): string {
  const d = DateTime.fromMillis(ms, { zone: tz })
  const year = d.year !== DateTime.fromMillis(now, { zone: tz }).year ? ` ${d.year}` : ""
  return `${WEEKDAYS_ID[d.weekday - 1]} ${d.day} ${MONTHS_ID[d.month - 1]}${year}`
}

// "Hari ini, 14.30 WITA" / "Besok, Rabu 11 Nov, 09.00 WITA" / "Senin 16 Nov, 08.00 WITA"
export function formatWhen(ms: number, tz: string, now: number): string {
  const clock = `${formatClock(ms, tz)} ${zoneLabel(tz, ms)}`
  const diff = dayDiff(ms, tz, now)
  if (diff === 0) return `Hari ini, ${clock}`
  if (diff === 1) return `Besok, ${formatDate(ms, tz, now)}, ${clock}`
  return `${formatDate(ms, tz, now)}, ${clock}`
}

function joinId(items: string[]): string {
  if (items.length <= 1) return items.join("")
  return `${items.slice(0, -1).join(", ")} dan ${items.at(-1)}`
}

const ORDINALS_ID: Record<string, string> = { "1": "pertama", "2": "kedua", "3": "ketiga", "4": "keempat", "-1": "terakhir", "-2": "kedua terakhir" }

function dayName(code: string): string {
  return WEEKDAYS_ID[RRULE_DAYS.indexOf(code as (typeof RRULE_DAYS)[number])] ?? code
}

// Wall-clock times the series fires on the local day of `dayMs` ("08.00", "20.00").
function timesOnDay(s: Schedule & { rrule: string }, dayMs: number): string[] {
  const p = utcToLocalParts(dayMs, s.tz)
  const start = Date.UTC(p.year, p.month - 1, p.day)
  const dates = ruleFor(s, start).between(new Date(start), new Date(start + DAY - 1), true, (_d, i) => i < 500)
  return dates.map((d) => `${pad(d.getUTCHours())}.${pad(d.getUTCMinutes())}`)
}

// "Tiap Senin, 08.00 WITA", "Tiap hari, 07.00 WITA", "Tiap Jumat terakhir, 17.00 WITA".
export function describeRecurrence(s: Schedule & { rrule: string }, now: number): string {
  const p = rruleParts(normalizeRrule(s.rrule))
  const interval = Number(p.INTERVAL ?? 1)
  const start = parseLocal(s.localStart)
  const startDt = DateTime.fromObject(start, { zone: "utc" })
  const next = nextOccurrence(s, now) ?? localToUtc(start, s.tz)
  const days = p.BYDAY ? p.BYDAY.split(",") : []
  const plainDays = days.filter((d) => /^[A-Z]{2}$/.test(d))
  const isWeekdays = plainDays.length === 5 && ["MO", "TU", "WE", "TH", "FR"].every((d) => plainDays.includes(d))
  const isWeekend = plainDays.length === 2 && plainDays.includes("SA") && plainDays.includes("SU")
  const namedDays = isWeekdays ? "hari kerja" : isWeekend ? "akhir pekan" : joinId(plainDays.map(dayName))

  let head: string
  let withTime = true
  switch (p.FREQ) {
    case "DAILY":
      head = days.length ? `Tiap ${namedDays}` : interval > 1 ? `Tiap ${interval} hari` : "Tiap hari"
      break
    case "WEEKLY": {
      const list = days.length ? namedDays : WEEKDAYS_ID[startDt.weekday - 1]
      head = interval > 1 ? `Tiap ${interval} minggu, ${list}` : `Tiap ${list}`
      break
    }
    case "MONTHLY": {
      const every = interval > 1 ? ` tiap ${interval} bulan` : ""
      const ord = days.length === 1 ? /^([+-]?\d+)([A-Z]{2})$/.exec(days[0] as string) : null
      const setpos = p.BYSETPOS && days.length === 1 && !ord ? p.BYSETPOS : null
      if (ord && ORDINALS_ID[String(Number(ord[1]))]) head = `Tiap ${dayName(ord[2] as string)} ${ORDINALS_ID[String(Number(ord[1]))]}${every}`
      else if (setpos && ORDINALS_ID[setpos]) head = `Tiap ${dayName(days[0] as string)} ${ORDINALS_ID[setpos]}${every}`
      else if (p.BYMONTHDAY === "-1") head = `Tiap akhir bulan${every}`
      else if (p.BYMONTHDAY) head = `Tiap tanggal ${joinId(p.BYMONTHDAY.split(","))}${every}`
      else if (days.length) head = `Tiap ${namedDays}${every}`
      else head = `Tiap tanggal ${start.day}${every}`
      break
    }
    case "YEARLY": {
      const month = p.BYMONTH ? Number(p.BYMONTH.split(",")[0]) : start.month
      const day = p.BYMONTHDAY ? p.BYMONTHDAY.split(",")[0] : String(start.day)
      head = `Tiap ${day} ${MONTHS_ID[month - 1]}${interval > 1 ? ` (tiap ${interval} tahun)` : ""}`
      break
    }
    case "HOURLY":
      head = interval > 1 ? `Tiap ${interval} jam` : "Tiap jam"
      withTime = false
      break
    case "MINUTELY":
      head = `Tiap ${interval} menit`
      withTime = false
      break
    default:
      head = "Berulang"
  }

  let tail = ""
  if (p.COUNT) tail += `, ${p.COUNT} kali`
  if (p.UNTIL) {
    const u = /^(\d{4})(\d{2})(\d{2})/.exec(p.UNTIL)
    if (u) tail += `, sampai ${Number(u[3])} ${MONTHS_ID[Number(u[2]) - 1]} ${u[1]}`
  }
  if (!withTime) return `${head}${tail}`

  const times = timesOnDay({ ...s, rrule: s.rrule }, next)
  let clock: string
  if (times.length === 0) clock = `${pad(start.hour)}.${pad(start.minute)}`
  else if (times.length <= 3) clock = joinId(times)
  else {
    const mins = times.map((t) => Number(t.slice(0, 2)) * 60 + Number(t.slice(3)))
    const gap = (mins[1] as number) - (mins[0] as number)
    const even = mins.every((m, i) => i === 0 || m - (mins[i - 1] as number) === gap)
    clock = `${even ? `tiap ${gap >= 60 && gap % 60 === 0 ? `${gap / 60} jam` : `${gap} menit`} ` : ""}${times[0]} sampai ${times.at(-1)}`
  }
  return `${head}, ${clock} ${zoneLabel(s.tz, next)}${tail}`
}

// The echo shown after a reminder is set: recurring rules are described, one-offs
// get their date and time relative to `now`.
export function formatEcho(s: Schedule, fireAt: number, now: number): string {
  if (s.rrule) return describeRecurrence({ ...s, rrule: s.rrule }, now)
  return formatWhen(fireAt, s.tz, now)
}

// ", mulai Senin 16 Nov" appended to a series echo so the first run is visible:
// "Tiap hari kerja, 08.00 WITA, mulai Senin 16 Nov". Hourly and minutely series
// carry the clock too, since their head has no time of day.
export function formatSeriesStart(s: Schedule & { rrule: string }, fireAt: number, now: number): string {
  const freq = rruleParts(normalizeRrule(s.rrule)).FREQ
  const clock = freq === "HOURLY" || freq === "MINUTELY" ? ` ${formatClock(fireAt, s.tz)} ${zoneLabel(s.tz, fireAt)}` : ""
  const diff = dayDiff(fireAt, s.tz, now)
  if (diff === 0) return `mulai hari ini${clock}`
  if (diff === 1) return `mulai besok, ${formatDate(fireAt, s.tz, now)}${clock}`
  return `mulai ${formatDate(fireAt, s.tz, now)}${clock}`
}

// --- First fire (shared by the parser and the service, so the set_reminder tool too) -------

// A first fire further out than this is refused as a likely misparse.
export const MAX_LEAD_MS = 731 * DAY

export type FirstFire = { schedule: Schedule; fireAt: number; rolled: boolean }

// The first fire of a new or edited schedule (localStart already normalized):
//   - a one-off earlier today ("jam 9" said at 10:00) moves to the same time
//     tomorrow (`rolled`); the echo then says "Besok", so a wrong guess shows
//   - a one-off in the past, or a series with no run from now on, is refused
//   - a first fire more than about 2 years away is refused
export function resolveFirstFire(s: Schedule, now: number): FirstFire {
  let schedule = s
  let fireAt = nextOccurrence(s, now - 1)
  let rolled = false
  if (fireAt === null && !s.rrule && s.localStart.slice(0, 10) === utcToLocal(now, s.tz).slice(0, 10)) {
    const d = DateTime.fromObject(parseLocal(s.localStart), { zone: "utc" }).plus({ days: 1 })
    schedule = { ...s, localStart: formatLocal({ year: d.year, month: d.month, day: d.day, hour: d.hour, minute: d.minute, second: d.second }) }
    fireAt = nextOccurrence(schedule, now - 1)
    rolled = true
  }
  if (fireAt === null) {
    if (s.rrule) throw new TimeError("That repeat rule has no occurrence from now on.")
    throw new TimeError(`That time has already passed (${formatWhen(localToUtc(s.localStart, s.tz), s.tz, now)}). Pick a time in the future.`)
  }
  if (fireAt - now > MAX_LEAD_MS) {
    throw new TimeError(`That's more than 2 years away (${formatWhen(fireAt, s.tz, now)}). Pick a nearer date.`)
  }
  return { schedule, fireAt, rolled }
}

// Tomorrow 09:00 wall time in `tz` (the "Besok 09.00" snooze).
export function tomorrowAt(now: number, tz: string, hour = 9, minute = 0): number {
  const d = DateTime.fromMillis(now, { zone: tz }).plus({ days: 1 })
  return localToUtc({ year: d.year, month: d.month, day: d.day, hour, minute, second: 0 }, tz)
}
