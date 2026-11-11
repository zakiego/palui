// Natural-language reminder parsing (PLAN 6) with the `fast` model. The model only
// reads the phrase into { text, localDateTime, rrule }; everything after that (the
// UTC instant, the first occurrence of a series, the echo) is computed here with
// time.ts, so the model never does offset math.
import { generateText, Output, type LanguageModel } from "ai"
import { DateTime } from "luxon"
import { z } from "zod"
import type { ParsedReminder } from "../control"
import {
  assertZone,
  formatEcho,
  formatSeriesStart,
  normalizeLocal,
  normalizeRrule,
  occurrencesAfter,
  resolveFirstFire,
  TimeError,
  utcToIso,
  utcToLocal,
  WEEKDAYS_ID,
  zoneLabel,
  type Schedule,
} from "./time"

export const parsedReminderSchema = z.object({
  text: z.string().describe("What to remind about, in the user's language, without the time words or the 'remind me' part."),
  localDateTime: z
    .string()
    .describe("Wall-clock date and time in the user's zone, ISO 8601 without offset: YYYY-MM-DDTHH:mm. For a repeating reminder, its first occurrence."),
  rrule: z
    .string()
    .nullable()
    .describe('RRULE body without DTSTART for repeating reminders, e.g. "FREQ=WEEKLY;BYDAY=MO". null for a one-off.'),
})

export type ModelReminder = z.infer<typeof parsedReminderSchema>

const EN_DAYS = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]

// A two-week calendar so the model never has to do weekday arithmetic.
function calendar(now: DateTime): string {
  const lines: string[] = []
  const upcoming: string[] = []
  for (let i = 1; i <= 7; i++) {
    const d = now.plus({ days: i })
    upcoming.push(`${EN_DAYS[d.weekday - 1]}/${WEEKDAYS_ID[d.weekday - 1]} = ${d.toFormat("yyyy-MM-dd")}`)
  }
  for (let i = 0; i < 15; i++) {
    const d = now.plus({ days: i })
    const tag = i === 0 ? " (today)" : i === 1 ? " (tomorrow, besok)" : i === 2 ? " (day after tomorrow, lusa)" : ""
    lines.push(`- ${d.toFormat("yyyy-MM-dd")} ${EN_DAYS[d.weekday - 1]} / ${WEEKDAYS_ID[d.weekday - 1]}${tag}`)
  }
  return `${lines.join("\n")}\n\nA bare weekday name means the next one after today: ${upcoming.join(", ")}.`
}

export function buildSystemPrompt(tz: string, now: number): string {
  const d = DateTime.fromMillis(now, { zone: tz })
  const local = d.toFormat("yyyy-MM-dd'T'HH:mm")
  const endOfMonth = d.endOf("month").toFormat("yyyy-MM-dd")
  const nextMonday = d.plus({ days: ((8 - d.weekday) % 7) || 7 }).toFormat("yyyy-MM-dd")
  return `You turn a reminder request (Indonesian or English, often casual) into JSON: { text, localDateTime, rrule }.

Current time: ${local} (${EN_DAYS[d.weekday - 1]} / ${WEEKDAYS_ID[d.weekday - 1]}), zone ${tz} (${zoneLabel(tz, now)}, UTC${d.toFormat("ZZ")}).
This month ends on ${endOfMonth}. Next week starts on Monday ${nextMonday}.

Calendar:
${calendar(d)}

Output
- localDateTime: wall-clock time in ${tz}, "YYYY-MM-DDTHH:mm", no offset, no "Z". It must be after the current time.
- rrule: an RFC 5545 RRULE body without DTSTART, or null for a one-off. Use FREQ, INTERVAL, BYDAY, BYMONTHDAY, BYMONTH, BYHOUR, BYMINUTE, BYSETPOS, COUNT, UNTIL only.
- For a repeating reminder, localDateTime is the FIRST occurrence after the current time (it carries the time of day).
- text: the thing to remember, in the user's language, short. Drop "ingetin/ingatkan/remind me (to)", the time and the repeat words. Keep names and details.

Times
- "jam 9", "at 9", "9", "pukul 9" with no other hint: hours 7 to 11 mean morning (07:00 to 11:00), 12 means 12:00 noon, hours 1 to 6 mean afternoon (13:00 to 18:00).
- Explicit parts of day win: pagi/morning = AM; siang = around midday (jam 1 siang = 13:00); sore/afternoon = PM (jam 4 sore = 16:00); malam/evening/tonight/night = PM (jam 8 malam = 20:00, jam 9 malam = 21:00). "am"/"pm" win over everything. "jam 12 malam"/midnight = 00:00 of the next day.
- Indonesian "9.30" or "09.30" means 09:30. "setengah 10" means 09:30 (half before 10). "jam 10 lewat 15" means 10:15.
- A part of day with no hour: pagi/morning = 08:00, siang/noon = 12:00, sore/afternoon = 16:00, malam/evening/tonight = 20:00.
- A date with no time at all (besok, lusa, a weekday, a date, akhir bulan) means 09:00.
- If only a time is given (no day) and that time today has already passed, use tomorrow. If it's still ahead, use today. At ${local}, "jam 9" alone is tomorrow 09:00, "jam 3" alone is today 15:00.
- Relative times: "in 2 hours", "2 jam lagi", "30 menit lagi", "setengah jam lagi" (30 minutes), "1 jam 15 menit lagi", "in 90 minutes": add exactly that to the current time, keep the minutes exact.
- "in 3 days at 6pm"/"3 hari lagi jam 6 sore": that day at that time. "in 3 days" alone: that day at 09:00.

Days
- besok/tomorrow = +1 day, lusa/the day after tomorrow = +2 days. "nanti", "nanti sore", "nanti malam", "tonight", "this afternoon", "hari ini", "today" = today.
- A weekday name ("Jumat", "on Friday", "hari Kamis", "Jumat ini", "this Friday") means the next such day after today; if it's today's weekday, it means 7 days from today unless "hari ini"/"today" is said. "Senin depan", "next Monday", "minggu depan hari Senin" = the Monday of next week (${nextMonday}). "Thursday next week", "Kamis minggu depan" = that weekday in next week (the week starting ${nextMonday}).
- "tanggal 25"/"on the 25th" = the 25th of this month if still ahead, else next month. "25 Desember", "December 1st" = that date this year if still ahead, else next year. "akhir bulan"/"end of the month"/"the last day of the month" = the last day of this month. New Year's Eve = Dec 31; "tahun baru"/New Year's Day = Jan 1.

Repeats (rrule)
- "tiap/setiap/every day", "tiap hari", "every morning/evening at X" = FREQ=DAILY.
- "tiap Senin", "every Monday" = FREQ=WEEKLY;BYDAY=MO. Several days: "tiap Selasa dan Kamis" = FREQ=WEEKLY;BYDAY=TU,TH. Weekend = BYDAY=SA,SU.
- "hari kerja", "every weekday", "Senin sampai Jumat" = FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR.
- "tiap 2 minggu (sekali) hari Sabtu", "every 2 weeks on Thursday", "every other Thursday" = FREQ=WEEKLY;INTERVAL=2;BYDAY=SA (or TH); the first occurrence is the next such day.
- "tiap tanggal 1", "every month on the 1st" = FREQ=MONTHLY;BYMONTHDAY=1. "the 31st" stays BYMONTHDAY=31 (months without it are skipped; the first occurrence is the next month that has a 31st).
- "tiap akhir bulan", "every last day of the month" = FREQ=MONTHLY;BYMONTHDAY=-1.
- "Jumat terakhir tiap bulan", "last Friday of the month" = FREQ=MONTHLY;BYDAY=-1FR. "Senin pertama tiap bulan", "first Monday of the month" = FREQ=MONTHLY;BYDAY=1MO.
- "tiap tahun 17 Agustus", "every year on March 3" = FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17.
- Several times a day: "tiap hari jam 8 pagi dan jam 8 malam" = FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0, localDateTime = the next of those times.
- "tiap 3 jam", "every 3 hours" = FREQ=HOURLY;INTERVAL=3, first occurrence = now + 3 hours. "tiap 30 menit" = FREQ=MINUTELY;INTERVAL=30.
- "sampai 31 Desember"/"until Dec 31" adds UNTIL=YYYYMMDDT235959Z with that date; "5 kali"/"5 times" adds COUNT=5.
- A one-off (no repeat words) has rrule null. Only tiap/setiap/every/each/"sekali"/daily/weekly/monthly make a repeat. "Hari Jumat", "Jumat ini", "akhir bulan", "on the last day of the month", "on the 15th" alone are one-offs.
- The first occurrence of a repeat can be today when its time is still ahead: "every Monday and Wednesday at 7pm" said on a Monday at 10:00 starts that same Monday at 19:00.

Examples (current time ${local}):
- "ingetin bayar listrik besok jam 9" -> {"text":"bayar listrik","localDateTime":"${d.plus({ days: 1 }).toFormat("yyyy-MM-dd")}T09:00","rrule":null}
- "remind me every Monday 8am to send the report" -> {"text":"send the report","localDateTime":"${nextMonday}T08:00","rrule":"FREQ=WEEKLY;BYDAY=MO"}
- "2 jam lagi angkat jemuran" -> {"text":"angkat jemuran","localDateTime":"${d.plus({ hours: 2 }).toFormat("yyyy-MM-dd'T'HH:mm")}","rrule":null}`
}

function stripOffset(s: string): string {
  return s.trim().replace(/(?:Z|[+-]\d{2}:?\d{2})$/i, "")
}

// Models sometimes start a series at the next matching day and skip a run still
// ahead today ("every weekday at 19:00" said at 10:00 comes back starting tomorrow).
// The start moves to today only when the model's first fire is exactly the next
// matching day after today. A later first fire is a start the user asked for
// ("mulai Senin depan, tiap hari kerja jam 8") and is kept.
function todayInstead(rrule: string, localStart: string, fireAt: number, now: number, tz: string): Schedule | null {
  if (!/^FREQ=(DAILY|WEEKLY|MONTHLY)(;|$)/.test(rrule) || /INTERVAL=|COUNT=|BYHOUR=|BYMINUTE=|BYSETPOS=/.test(rrule)) return null
  // Without BY* days the anchor itself picks the weekday or day of month; moving it
  // would change the rule.
  if (rrule.startsWith("FREQ=WEEKLY") && !rrule.includes("BYDAY=")) return null
  if (rrule.startsWith("FREQ=MONTHLY") && !/BYDAY=|BYMONTHDAY=/.test(rrule)) return null
  const todayDate = utcToLocal(now, tz).slice(0, 10)
  const today: Schedule = { rrule, tz, localStart: `${todayDate}${localStart.slice(10)}` }
  const [first, second] = occurrencesAfter(today, now - 1, 2)
  if (first === undefined || second === undefined) return null
  if (utcToLocal(first, tz).slice(0, 10) !== todayDate || second !== fireAt) return null
  return today
}

// The deterministic half: validate the model's fields and compute the instant and
// echo. Exported for tests and for callers that already have the fields.
export function finishParse(m: ModelReminder, fallbackText: string, tz: string, now: number): ParsedReminder {
  const text = m.text?.trim() || fallbackText.trim()
  let rrule: string | null = null
  try {
    assertZone(tz)
    const localStart = normalizeLocal(stripOffset(m.localDateTime ?? ""))
    rrule = m.rrule?.trim() ? normalizeRrule(m.rrule) : null
    // A one-off earlier today rolls to tomorrow (the echo says "Besok"); a past or
    // far-off time is refused. Same rules as createReminder.
    let { schedule: s, fireAt } = resolveFirstFire({ rrule, tz, localStart }, now)
    if (rrule) {
      const today = todayInstead(rrule, localStart, fireAt, now, tz)
      if (today) {
        s = today
        fireAt = occurrencesAfter(today, now - 1, 1)[0] as number
      }
    }
    const echo = s.rrule ? `${formatEcho(s, fireAt, now)}, ${formatSeriesStart({ ...s, rrule: s.rrule }, fireAt, now)}` : formatEcho(s, fireAt, now)
    return { ok: true, text, fireAt, fireAtIso: utcToIso(fireAt, tz), rrule, tz, echo }
  } catch (e) {
    if (e instanceof TimeError) return { ok: false, text, ...(rrule ? { rrule } : {}), tz, error: e.message }
    throw e
  }
}

export type ParseOptions = { timeoutMs?: number; maxRetries?: number }

export async function parseReminder(text: string, tz: string, now: number, model: LanguageModel, opts: ParseOptions = {}): Promise<ParsedReminder> {
  const input = text.trim()
  if (!input) return { ok: false, text: "", tz, error: "Nothing to parse." }
  try {
    assertZone(tz)
  } catch (e) {
    return { ok: false, text: input, tz, error: (e as Error).message }
  }
  try {
    const { output } = await generateText({
      model,
      system: buildSystemPrompt(tz, now),
      prompt: input,
      output: Output.object({ schema: parsedReminderSchema }),
      temperature: 0,
      maxOutputTokens: 400,
      maxRetries: opts.maxRetries ?? 1,
      abortSignal: AbortSignal.timeout(opts.timeoutMs ?? 15_000),
    })
    return finishParse(output, input, tz, now)
  } catch (e) {
    return { ok: false, text: input, tz, error: `Couldn't read that reminder: ${(e as Error)?.message ?? String(e)}` }
  }
}

// The localStart to store when creating a reminder from a parse result.
export function parsedLocalStart(p: ParsedReminder): string | null {
  return p.ok && p.fireAt !== undefined ? utcToLocal(p.fireAt, p.tz) : null
}
