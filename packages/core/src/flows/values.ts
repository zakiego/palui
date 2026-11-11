// Value helpers shared by expressions and templates: paths into data, dates in a
// zone, truthiness. Everything here is pure and never throws on odd data: a value
// that doesn't fit is null.
import { DateTime } from "luxon"
import { DAY_CODES, type DayCode } from "../routines/config"
import { localToUtc, utcToLocal } from "../reminders/time"

export type Scope = Record<string, unknown>

// A path segment: an object key or an array index.
export type Segment = string | number

export function getIn(value: unknown, segs: Segment[]): unknown {
  let cur = value
  for (const s of segs) {
    if (cur === null || cur === undefined) return null
    if (Array.isArray(cur)) {
      const i = typeof s === "number" ? s : /^\d+$/.test(s) ? Number(s) : Number.NaN
      cur = Number.isInteger(i) ? cur[i] : null
    } else if (typeof cur === "object") {
      const key = String(s)
      cur = Object.hasOwn(cur, key) ? (cur as Record<string, unknown>)[key] : null
    } else return null
  }
  return cur === undefined ? null : cur
}

export function isEmpty(v: unknown): boolean {
  if (v === null || v === undefined || v === "") return true
  if (Array.isArray(v)) return v.length === 0
  if (typeof v === "object") return Object.keys(v as object).length === 0
  return false
}

export function truthy(v: unknown): boolean {
  if (v === false || v === 0 || Number.isNaN(v)) return false
  return !isEmpty(v)
}

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/
const CLOCK_RE = /^(\d{1,2})[:.](\d{2})(?::\d{2})?$/
const LOCAL_DT_RE = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/
const ABS_DT_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})$/

// An ISO date ("2026-11-13", start of that day in tz), a local or absolute
// datetime, a clock time ("04:39", today in tz) or epoch ms -> UTC ms. Else null.
export function toMs(v: unknown, tz: string, now: number): number | null {
  if (typeof v === "number" && Number.isFinite(v)) {
    if (v > 1e11) return Math.round(v)
    if (v > 1e9) return Math.round(v * 1000)
    return null
  }
  if (typeof v !== "string") return null
  const s = v.trim()
  try {
    if (DATE_RE.test(s)) return localToUtc(`${s}T00:00`, tz)
    const clock = CLOCK_RE.exec(s)
    if (clock) {
      const h = Number(clock[1])
      const m = Number(clock[2])
      if (h > 23 || m > 59) return null
      return localToUtc(`${localDateOf(now, tz)}T${String(h).padStart(2, "0")}:${clock[2]}`, tz)
    }
    if (ABS_DT_RE.test(s)) {
      const ms = Date.parse(s)
      return Number.isFinite(ms) ? ms : null
    }
    if (LOCAL_DT_RE.test(s)) return localToUtc(s.replace(" ", "T").slice(0, 16), tz)
  } catch {
    return null
  }
  return null
}

export function localDateOf(ms: number, tz: string): string {
  return utcToLocal(ms, tz).slice(0, 10)
}

export function localTimeOf(ms: number, tz: string): string {
  return utcToLocal(ms, tz).slice(11, 16)
}

export function weekdayOfMs(ms: number, tz: string): DayCode {
  return DAY_CODES[DateTime.fromMillis(ms, { zone: tz }).weekday - 1]!
}

// Whole calendar days from today (in tz) to the date of `ms`.
export function calendarDaysBetween(from: number, to: number, tz: string): number {
  const a = DateTime.fromMillis(from, { zone: tz }).startOf("day")
  const b = DateTime.fromMillis(to, { zone: tz }).startOf("day")
  return Math.round(b.diff(a, "days").days)
}

// `now.*` as templates and expressions see it.
export function nowScope(now: number, tz: string): { at: number; date: string; time: string; weekday: DayCode } {
  return { at: now, date: localDateOf(now, tz), time: localTimeOf(now, tz), weekday: weekdayOfMs(now, tz) }
}

export function numeric(v: unknown): number | null {
  if (typeof v === "number") return Number.isFinite(v) ? v : null
  if (typeof v === "string" && v.trim() !== "" && /^-?\d+(\.\d+)?$/.test(v.trim())) return Number(v.trim())
  return null
}

// Compact JSON that never throws (cycles and BigInt come out as a marker).
export function compactJson(v: unknown): string {
  try {
    return JSON.stringify(v) ?? "null"
  } catch {
    return '"[unserializable]"'
  }
}
