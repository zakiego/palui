// Time sources: when a flow schedule fires. Each kind answers one question, "which
// occurrences come after this cursor", from its config plus the cached state.
// Only the tool kind has a refresh step (it reads a connector tool); the others
// are pure functions of the config.
//
// An occurrence key is "<local date>:<item>" (or the local datetime for RRULE).
// The engine stores one occurrence row per key, so the same item on the same day
// never fires twice, even when a refresh nudges its time by a minute.
//
// Tool results are data, never instructions: only values that parse as times are
// used, and labels from a result are cut to one short plain line.
import { DateTime } from "luxon"
import type { SourceCursor, SourceState } from "@palui/db"
import { localToUtc, occurrencesAfter, utcToLocal } from "../reminders/time"
import { DAY_CODES, type DayCode, type ScheduleSource, type ToolSource } from "./source-config"

export type Occurrence = { at: number; key: string; label: string; fallback?: boolean }

export type SourceInput = { source: ScheduleSource; tz: string; days: string[] | null; state: SourceState }

export class SourceError extends Error {}

const HORIZON_DAYS = 8
type Entry = NonNullable<SourceState["entries"]>[number]

// --- Local dates -----------------------------------------------------------------------

export function localDate(ms: number, tz: string): string {
  return utcToLocal(ms, tz).slice(0, 10)
}

export function addDays(date: string, n: number): string {
  return DateTime.fromISO(date, { zone: "utc" }).plus({ days: n }).toISODate() as string
}

export function weekdayOf(ms: number, tz: string): DayCode {
  return DAY_CODES[DateTime.fromMillis(ms, { zone: tz }).weekday - 1]!
}

// The next instant after `after` whose local clock reads hh:mm.
export function nextLocalTime(hhmm: string, tz: string, after: number): number {
  let d = localDate(after, tz)
  for (let i = 0; i < 3; i++) {
    const at = localToUtc(`${d}T${hhmm}`, tz)
    if (at > after) return at
    d = addDays(d, 1)
  }
  return after + 24 * 3600_000
}

function isAfter(o: Occurrence, c: SourceCursor): boolean {
  return o.at > c.at || (o.at === c.at && o.key > c.key)
}

function byTime(a: Occurrence, b: Occurrence): number {
  return a.at - b.at || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0)
}

// Day-based sources: every occurrence of each local date from the cursor's date
// (minus one, for negative offsets) over the horizon.
function dayBased(cursor: SourceCursor, tz: string, perDate: (date: string) => Occurrence[]): Occurrence[] {
  const start = addDays(localDate(cursor.at, tz), -1)
  const out: Occurrence[] = []
  for (let i = 0; i <= HORIZON_DAYS; i++) out.push(...perDate(addDays(start, i)).filter((o) => isAfter(o, cursor)))
  return out.sort(byTime)
}

const clockLabel = (hhmm: string) => hhmm.replace(":", ".")

// --- Kinds -----------------------------------------------------------------------------

function timesAfter(i: SourceInput & { source: Extract<ScheduleSource, { kind: "times" }> }, cursor: SourceCursor): Occurrence[] {
  return dayBased(cursor, i.tz, (date) =>
    i.source.times.map((t) => ({ at: localToUtc(`${date}T${t.time}`, i.tz), key: `${date}:${t.time}`, label: t.label ?? clockLabel(t.time) })),
  )
}

function rruleAfter(i: SourceInput & { source: Extract<ScheduleSource, { kind: "rrule" }> }, cursor: SourceCursor, limit: number): Occurrence[] {
  const at = occurrencesAfter({ rrule: i.source.rrule, tz: i.tz, localStart: i.source.start }, cursor.at - 1, limit + 1)
  return at
    .map((ms) => {
      const local = utcToLocal(ms, i.tz)
      return { at: ms, key: local.slice(0, 16), label: i.source.label ?? clockLabel(local.slice(11, 16)) }
    })
    .filter((o) => isAfter(o, cursor))
}

// Cached entries for one date. A date the source has no data for (it failed)
// reuses the clock times of the closest fetched date, marked as fallback.
function toolEntriesFor(i: SourceInput, date: string): Occurrence[] {
  const entries = i.state.entries ?? []
  const fetched = [...(i.state.fetched ?? [])].sort()
  const own = (e: Entry) => ({ at: e.at, key: `${e.date}:${e.key}`, label: e.label })
  if (fetched.includes(date)) return entries.filter((e) => e.date === date).map(own)
  if (!fetched.length) return []
  const base = [...fetched].reverse().find((d) => d < date) ?? fetched[0]!
  return entries
    .filter((e) => e.date === base)
    .map((e) => ({ at: localToUtc(`${date}T${utcToLocal(e.at, i.tz).slice(11, 16)}`, i.tz), key: `${date}:${e.key}`, label: e.label, fallback: true }))
}

function toolAfter(i: SourceInput & { source: ToolSource }, cursor: SourceCursor): Occurrence[] {
  if (i.source.items) return dayBased(cursor, i.tz, (date) => toolEntriesFor(i, date))
  return (i.state.entries ?? [])
    .map((e) => ({ at: e.at, key: `${e.date}:${e.key}`, label: e.label }))
    .filter((o) => isAfter(o, cursor))
    .sort(byTime)
}

function rawAfter(i: SourceInput, cursor: SourceCursor, limit: number): Occurrence[] {
  switch (i.source.kind) {
    case "times":
      return timesAfter(i as SourceInput & { source: typeof i.source }, cursor)
    case "rrule":
      return rruleAfter(i as SourceInput & { source: typeof i.source }, cursor, limit)
    case "tool":
      return toolAfter(i as SourceInput & { source: typeof i.source }, cursor)
  }
}

// The next `n` occurrences strictly after the cursor that fall on an active day.
export function upcoming(i: SourceInput, cursor: SourceCursor, n: number): Occurrence[] {
  const out: Occurrence[] = []
  let c = cursor
  for (let round = 0; round < 60 && out.length < n; round++) {
    const batch = rawAfter(i, c, n * 2)
    if (!batch.length) break
    for (const o of batch) {
      if (out.length >= n) break
      if (!i.days || i.days.includes(weekdayOf(o.at, i.tz))) out.push(o)
    }
    const last = batch.at(-1)!
    c = { at: last.at, key: last.key }
  }
  return out
}

export function nextOccurrenceAfter(i: SourceInput, cursor: SourceCursor): Occurrence | null {
  return upcoming(i, cursor, 1)[0] ?? null
}

// --- Tool results ---------------------------------------------------------------------

// The JSON payload of a tool result: MCP structuredContent, the parsed text
// content, or the value itself for plain tools.
export function toolData(output: unknown): unknown {
  if (output && typeof output === "object") {
    const o = output as { isError?: boolean; structuredContent?: unknown; content?: unknown }
    const texts = Array.isArray(o.content)
      ? (o.content as { type?: string; text?: string }[])
          .filter((c) => c?.type === "text" && typeof c.text === "string")
          .map((c) => c.text as string)
          .join("\n")
      : null
    // The error text is the server's, so it isn't repeated (it could carry instructions).
    if (o.isError) throw new SourceError("The tool returned an error result. Check the args with uji_flow.")
    if (o.structuredContent && typeof o.structuredContent === "object") return o.structuredContent
    if (texts !== null) {
      try {
        return JSON.parse(texts)
      } catch {
        throw new SourceError("The tool result is not JSON, so no times can be read from it.")
      }
    }
  }
  if (typeof output === "string") {
    try {
      return JSON.parse(output)
    } catch {
      throw new SourceError("The tool result is not JSON, so no times can be read from it.")
    }
  }
  return output
}

// "times.start", "data.0.due", "" (the value itself).
export function getPath(data: unknown, path: string): unknown {
  let cur = data
  for (const part of path.split(".").filter(Boolean)) {
    if (cur === null || typeof cur !== "object") return undefined
    cur = (cur as Record<string, unknown>)[part]
  }
  return cur
}

const CLOCK_RE = /^(\d{1,2})[:.](\d{2})(?::\d{2})?$/
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/
const LOCAL_DT_RE = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/
const ABS_DT_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})$/

// A value from a tool result -> UTC ms. Clock times are placed on `date` in `tz`;
// a bare date gets `dateTime`. Anything else is null (ignored).
export function parseTimeValue(v: unknown, date: string, tz: string, dateTime = "09:00"): number | null {
  if (typeof v === "number" && Number.isFinite(v)) {
    if (v > 1e11) return Math.round(v)
    if (v > 1e9) return Math.round(v * 1000)
    return null
  }
  if (typeof v !== "string") return null
  const s = v.trim()
  const clock = CLOCK_RE.exec(s)
  if (clock) {
    const h = Number(clock[1])
    const m = Number(clock[2])
    if (h > 23 || m > 59) return null
    return localToUtc(`${date}T${String(h).padStart(2, "0")}:${clock[2]}`, tz)
  }
  if (DATE_RE.test(s)) return localToUtc(`${s}T${dateTime}`, tz)
  if (ABS_DT_RE.test(s)) {
    const ms = Date.parse(s)
    return Number.isFinite(ms) ? ms : null
  }
  if (LOCAL_DT_RE.test(s)) return localToUtc(s.replace(" ", "T").slice(0, 16), tz)
  return null
}

// One short plain line: no control characters, no markup that could pose as a
// message structure, at most 60 characters.
export function safeLabel(v: unknown): string | null {
  if (typeof v !== "string" && typeof v !== "number") return null
  const s = String(v)
    .replace(/[\u0000-\u001f\u007f`*_~<>[\]{}]/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 60)
  return s || null
}

export type Extracted = { entries: Entry[]; problems: string[] }

export function extractItems(src: ToolSource, data: unknown, date: string, tz: string): Extracted {
  const out: Extracted = { entries: [], problems: [] }
  for (const item of src.items ?? []) {
    const raw = getPath(data, item.field)
    const at = parseTimeValue(raw, date, tz)
    if (at === null) {
      out.problems.push(`${item.field}: ${raw === undefined ? "missing" : "not a time"}`)
      continue
    }
    const last = item.field.split(".").pop() ?? item.field
    out.entries.push({ date, key: item.field, label: item.label ?? last, at: at + item.offsetMin * 60_000 })
  }
  return out
}

export const MAX_LIST_ENTRIES = 200

export function extractList(src: ToolSource, data: unknown, tz: string): Extracted {
  const out: Extracted = { entries: [], problems: [] }
  const list = src.list
  if (!list) return out
  const arr = getPath(data, list.path)
  if (!Array.isArray(arr)) {
    out.problems.push(`${list.path || "(result)"}: not an array`)
    return out
  }
  const seen = new Set<string>()
  for (const [idx, el] of arr.slice(0, MAX_LIST_ENTRIES).entries()) {
    const raw = getPath(el, list.at)
    const base = parseTimeValue(raw, "1970-01-01", tz, list.time)
    if (base === null || (typeof raw === "string" && CLOCK_RE.test(raw.trim()))) {
      out.problems.push(`[${idx}].${list.at}: not a date`)
      continue
    }
    const at = base + list.offsetMin * 60_000
    const label = (list.label ? safeLabel(getPath(el, list.label)) : null) ?? `#${idx + 1}`
    let key = `${utcToLocal(at, tz).slice(11, 16)}:${label}`
    while (seen.has(`${localDate(at, tz)}:${key}`)) key += "+"
    seen.add(`${localDate(at, tz)}:${key}`)
    out.entries.push({ date: localDate(at, tz), key, label, at })
  }
  return out
}

// Tool args with the date placeholders filled in, recursively.
export function renderArgs(args: Record<string, unknown>, date: string, tz: string): Record<string, unknown> {
  const fill = (v: unknown): unknown => {
    if (typeof v === "string")
      return v
        .replaceAll("{{date}}", date)
        .replaceAll("{{month}}", date.slice(0, 7))
        .replaceAll("{{year}}", date.slice(0, 4))
        .replaceAll("{{tz}}", tz)
    if (Array.isArray(v)) return v.map(fill)
    if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, fill(x)]))
    return v
  }
  return fill(args) as Record<string, unknown>
}

export type ToolCaller = (connectorId: string, tool: string, args: Record<string, unknown>) => Promise<unknown>

export type RefreshResult = { state: SourceState; error: string | null }

// Reads a tool source: today and tomorrow for per-day items, one call for a list.
// Whatever succeeded is merged into the state even when another call failed.
export async function refreshToolSource(src: ToolSource, tz: string, prev: SourceState, call: ToolCaller, now: number): Promise<RefreshResult> {
  const today = localDate(now, tz)
  const keepFrom = addDays(today, -3)
  let entries = [...(prev.entries ?? [])]
  let fetched = [...(prev.fetched ?? [])]
  const errors: string[] = []

  const attempt = async (date: string, extract: (data: unknown) => Extracted) => {
    try {
      const data = toolData(await call(src.connectorId, src.tool, renderArgs(src.args, date, tz)))
      const got = extract(data)
      if (!got.entries.length) throw new SourceError(`No usable times in the result (${got.problems.slice(0, 3).join("; ") || "empty"}).`)
      return got.entries
    } catch (e) {
      errors.push(`${date}: ${(e as Error)?.message ?? String(e)}`.slice(0, 300))
      return null
    }
  }

  if (src.items) {
    for (const date of [today, addDays(today, 1)]) {
      const got = await attempt(date, (d) => extractItems(src, d, date, tz))
      if (!got) continue
      entries = [...entries.filter((e) => e.date !== date), ...got]
      if (!fetched.includes(date)) fetched.push(date)
    }
    // Keep a few days back, and always the latest fetched date (the fallback base).
    const latest = [...fetched].sort().at(-1)
    fetched = fetched.filter((d) => d >= keepFrom || d === latest).sort()
    entries = entries.filter((e) => fetched.includes(e.date))
  } else {
    const got = await attempt(today, (d) => extractList(src, d, tz))
    if (got) {
      entries = got.filter((e) => e.at >= now - 3 * 24 * 3600_000)
      fetched = [today]
    }
  }

  const error = errors.length ? errors.join(" | ") : null
  return {
    state: {
      ...prev,
      entries,
      fetched,
      failures: error ? (prev.failures ?? 0) + 1 : 0,
      lastError: error,
      lastOkAt: error ? (prev.lastOkAt ?? null) : now,
    },
    error,
  }
}
