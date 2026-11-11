// Calendar API v3 over plain REST (no googleapis dependency), plus the date
// range parsing for lihat_kalender.
import { DateTime } from "luxon"
import { CALENDAR_API, GoogleError, refreshAccessToken } from "./oauth"

export type CalendarEvent = {
  id: string
  calendarId: string
  title: string
  // RFC 3339 for timed events, YYYY-MM-DD for all-day events.
  start: string
  end: string
  allDay: boolean
  location?: string
  status?: string
  link?: string
}

type RawEvent = {
  id: string
  status?: string
  summary?: string
  location?: string
  htmlLink?: string
  start?: { date?: string; dateTime?: string }
  end?: { date?: string; dateTime?: string }
}

export async function listEvents(
  input: { accessToken: string; calendarId: string; timeMin: string; timeMax: string; maxResults?: number },
  fetchImpl: typeof fetch = fetch,
): Promise<CalendarEvent[]> {
  const params = new URLSearchParams({
    timeMin: input.timeMin,
    timeMax: input.timeMax,
    singleEvents: "true",
    orderBy: "startTime",
    maxResults: String(input.maxResults ?? 50),
  })
  const res = await fetchImpl(`${CALENDAR_API}/calendars/${encodeURIComponent(input.calendarId)}/events?${params}`, {
    headers: { authorization: `Bearer ${input.accessToken}`, accept: "application/json" },
    signal: AbortSignal.timeout(15_000),
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: { message?: string; status?: string } }
    const msg = body.error?.message ?? res.statusText
    if (res.status === 404) throw new GoogleError(404, "not_found", `Calendar ${input.calendarId} wasn't found`)
    throw new GoogleError(res.status, body.error?.status ?? `http_${res.status}`, `Google Calendar answered ${res.status}: ${msg}`)
  }
  const body = (await res.json()) as { items?: RawEvent[] }
  return (body.items ?? [])
    .filter((e) => e.status !== "cancelled")
    .map((e) => ({
      id: e.id,
      calendarId: input.calendarId,
      title: e.summary?.trim() || "(no title)",
      start: e.start?.dateTime ?? e.start?.date ?? "",
      end: e.end?.dateTime ?? e.end?.date ?? "",
      allDay: !e.start?.dateTime,
      ...(e.location ? { location: e.location } : {}),
      ...(e.status ? { status: e.status } : {}),
      ...(e.htmlLink ? { link: e.htmlLink } : {}),
    }))
}

// Caches the access token until a minute before it expires.
export class GoogleTokens {
  private cached: { token: string; expiresAt: number } | null = null

  constructor(
    private readonly creds: { clientId: string; clientSecret: string; refreshToken: string },
    private readonly fetchImpl: typeof fetch = fetch,
  ) {}

  async get(now = Date.now()): Promise<string> {
    if (this.cached && this.cached.expiresAt - 60_000 > now) return this.cached.token
    const { accessToken, expiresAt } = await refreshAccessToken(this.creds, this.fetchImpl, now)
    this.cached = { token: accessToken, expiresAt }
    return accessToken
  }

  clear() {
    this.cached = null
  }
}

export const MAX_RANGE_DAYS = 31

const KEYWORDS: Record<string, (today: DateTime) => [DateTime, DateTime]> = {
  today: (d) => [d, d.plus({ days: 1 })],
  hari_ini: (d) => [d, d.plus({ days: 1 })],
  tomorrow: (d) => [d.plus({ days: 1 }), d.plus({ days: 2 })],
  besok: (d) => [d.plus({ days: 1 }), d.plus({ days: 2 })],
  week: (d) => [d, d.plus({ days: 7 })],
  minggu_ini: (d) => [d, d.plus({ days: 7 })],
}

function parsePoint(raw: string, tz: string): { dt: DateTime; dateOnly: boolean } | null {
  const s = raw.trim()
  if (/^\d{4}-\d{2}-\d{2}$/.test(s)) {
    const dt = DateTime.fromISO(s, { zone: tz })
    return dt.isValid ? { dt, dateOnly: true } : null
  }
  // ISO with an offset keeps it; without one it's wall time in tz.
  const dt = DateTime.fromISO(s, { zone: tz, setZone: false })
  return dt.isValid ? { dt: dt.setZone(tz), dateOnly: false } : null
}

// from/to: ISO dates or datetimes, or "today" / "tomorrow" / "week" (Indonesian
// hari ini / besok / minggu ini work too). Missing from means today; a date
// without `to` covers that whole day.
export function resolveRange(from: string | undefined, to: string | undefined, tz: string, now: number): { timeMin: DateTime; timeMax: DateTime } {
  const today = DateTime.fromMillis(now, { zone: tz }).startOf("day")
  const key = (from ?? "today").trim().toLowerCase().replace(/\s+/g, "_")
  let start: DateTime
  let end: DateTime
  const kw = KEYWORDS[key]
  if (kw) {
    ;[start, end] = kw(today)
  } else {
    const p = parsePoint(from as string, tz)
    if (!p) throw new Error(`Can't read the date "${from}". Use YYYY-MM-DD, an ISO time, today, tomorrow or week.`)
    start = p.dt
    end = p.dt.plus({ days: 1 })
  }
  if (to?.trim()) {
    const q = parsePoint(to, tz)
    if (!q) throw new Error(`Can't read the date "${to}".`)
    // A date-only `to` is inclusive: through the end of that day.
    end = q.dateOnly ? q.dt.plus({ days: 1 }) : q.dt
  }
  if (end <= start) throw new Error("The end of the range must be after the start")
  if (end.diff(start, "days").days > MAX_RANGE_DAYS) end = start.plus({ days: MAX_RANGE_DAYS })
  return { timeMin: start, timeMax: end }
}

// One event as the model sees it, in the turn's timezone.
export function presentEvent(e: CalendarEvent, tz: string) {
  if (e.allDay) {
    const start = DateTime.fromISO(e.start, { zone: tz })
    const lastDay = DateTime.fromISO(e.end, { zone: tz }).minus({ days: 1 })
    const days = lastDay > start ? `${start.toFormat("ccc d LLL")} to ${lastDay.toFormat("ccc d LLL")}` : start.toFormat("ccc d LLL")
    return { title: e.title, when: `${days} (all day)`, start: e.start, end: e.end, allDay: true, ...(e.location ? { location: e.location } : {}), calendar: e.calendarId }
  }
  const s = DateTime.fromISO(e.start).setZone(tz)
  const en = DateTime.fromISO(e.end).setZone(tz)
  const sameDay = s.hasSame(en, "day")
  const when = sameDay ? `${s.toFormat("ccc d LLL HH:mm")}-${en.toFormat("HH:mm")}` : `${s.toFormat("ccc d LLL HH:mm")} to ${en.toFormat("ccc d LLL HH:mm")}`
  return {
    title: e.title,
    when,
    start: s.toISO({ suppressMilliseconds: true }),
    end: en.toISO({ suppressMilliseconds: true }),
    allDay: false,
    ...(e.location ? { location: e.location } : {}),
    calendar: e.calendarId,
  }
}

// All-day events sort at the start of their day in tz.
export function sortEvents(events: CalendarEvent[], tz: string): CalendarEvent[] {
  const key = (e: CalendarEvent) => (e.allDay ? DateTime.fromISO(e.start, { zone: tz }).toMillis() : DateTime.fromISO(e.start).toMillis())
  return [...events].sort((a, b) => key(a) - key(b))
}
