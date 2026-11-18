const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" })
const dateFmt = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" })

export function ago(ms: number, now = Date.now()): string {
  const s = Math.round((ms - now) / 1000)
  const abs = Math.abs(s)
  if (abs < 45) return "just now"
  if (abs < 3600) return rtf.format(Math.round(s / 60), "minute")
  if (abs < 86_400) return rtf.format(Math.round(s / 3600), "hour")
  if (abs < 30 * 86_400) return rtf.format(Math.round(s / 86_400), "day")
  return dateFmt.format(ms)
}

export function fullDate(ms: number): string {
  return dateFmt.format(ms)
}

const timeFmt = new Intl.DateTimeFormat("en", { timeStyle: "short" })

// Intl puts a breakable space before AM or PM: U+0020, or U+202F in WebKit. Swap either for U+00A0.
export function noBreak(text: string): string {
  return text.replace(/[\u0020\u202F]/g, "\u00A0")
}

// A clock time that never wraps, like "11:42 AM" with a non-breaking space.
export function clockTime(ms: number): string {
  return noBreak(timeFmt.format(ms))
}

export function plural(n: number, one: string, many = `${one}s`) {
  return `${n.toLocaleString("en")} ${n === 1 ? one : many}`
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

const ZONED: Intl.DateTimeFormatOptions = { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }

// A time in a given IANA zone: "Wed 11 Nov, 09:00". A date outside the current
// year carries its year ("Mon, 31 Dec 2029, 23:30"), so a far-off reminder or an
// old run never reads as this year's. A timestamp Intl can't format gives "".
export function inZone(ms: number, tz: string, opts: Intl.DateTimeFormatOptions = ZONED, now = Date.now()): string {
  if (!Number.isFinite(ms) || Math.abs(ms) > 8.64e15) return ""
  const format = (zone: string | undefined) => {
    const year = new Intl.DateTimeFormat("en-GB", { year: "numeric", timeZone: zone })
    const full = opts.year === undefined && opts.day !== undefined && year.format(ms) !== year.format(now) ? { ...opts, year: "numeric" as const } : opts
    return new Intl.DateTimeFormat("en-GB", { ...full, timeZone: zone }).format(ms)
  }
  try {
    return format(tz)
  } catch {
    return format(undefined)
  }
}

// The clock time in a row's own zone when it isn't the owner's: "23:30
// America/Los_Angeles". Null when both zones show the same wall time, so the
// note only appears where the two would disagree.
export function inOtherZone(ms: number, tz: string, ownerTz: string, now = Date.now()): string | null {
  if (tz === ownerTz) return null
  const own = inZone(ms, tz, ZONED, now)
  if (!own || own === inZone(ms, ownerTz, ZONED, now)) return null
  return `${inZone(ms, tz, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }, now)} ${tz}`
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" })

// The first `max` UTF-16 units of a text, cut between characters as a reader
// sees them. A plain slice() can split an emoji and leave half of it behind.
export function clip(text: string, max: number): string {
  if (text.length <= max) return text
  let out = ""
  for (const { segment } of graphemes.segment(text)) {
    if (out.length + segment.length > max) break
    out += segment
  }
  return out
}

const compactFmt = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })
export function compact(n: number): string {
  return compactFmt.format(n)
}

// "850 ms", "8.3 s", "42 s", "5 min", "2 h 15 min", "3 d 4 h": the unit grows
// with the value, so a day-long run doesn't read as "1440 min".
export function duration(ms: number | null | undefined): string {
  if (ms == null) return ""
  if (ms < 1000) return `${ms} ms`
  if (ms < 59_500) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`
  const minutes = Math.round(ms / 60_000)
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return minutes % 60 ? `${hours} h ${minutes % 60} min` : `${hours} h`
  const days = Math.floor(hours / 24)
  return hours % 24 ? `${days} d ${hours % 24} h` : `${days} d`
}

// A file size the way the Attachments page shows it.
export function fileSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}
