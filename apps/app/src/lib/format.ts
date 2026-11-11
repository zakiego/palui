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

export function plural(n: number, one: string, many = `${one}s`) {
  return `${n.toLocaleString("en")} ${n === 1 ? one : many}`
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

// A time in a given IANA zone: "Wed 11 Nov, 09:00".
export function inZone(ms: number, tz: string, opts: Intl.DateTimeFormatOptions = { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }): string {
  try {
    return new Intl.DateTimeFormat("en-GB", { ...opts, timeZone: tz }).format(ms)
  } catch {
    return new Intl.DateTimeFormat("en-GB", opts).format(ms)
  }
}

const compactFmt = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })
export function compact(n: number): string {
  return compactFmt.format(n)
}

export function duration(ms: number | null | undefined): string {
  if (ms == null) return ""
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`
  return `${Math.round(ms / 60_000)} min`
}
