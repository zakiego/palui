// Schedule sources and check-in answers, shared by the flow schema, the
// scheduler and the check-in code. Sources are a discriminated union on `kind`:
// adding a kind means one more schema here and one more entry in sources.ts,
// never a migration.
//
//   times   fixed local times every (active) day
//   rrule   one RFC 5545 rule with a start (weekly review, monthly bill)
//   tool    times read from a connector tool's result (any MCP server),
//           refreshed daily; `items` picks fields, `list` walks an array
//
// Tool args are templates: {{date}} (YYYY-MM-DD) {{month}} (YYYY-MM) {{year}} {{tz}}.
import { z } from "zod"

export const DAY_CODES = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"] as const
export type DayCode = (typeof DAY_CODES)[number]

export const MAX_ITEMS = 24

const hhmm = z.string().regex(/^([01]\d|2[0-3]):[0-5]\d$/, "Use HH:mm (24h, two-digit hour)")

const label = z.string().trim().min(1).max(60)
// Up to a week either way (a bill reminder a day before its due date, say).
const offsetMin = z.number().int().min(-10080).max(10080).default(0)
const fieldPath = z.string().trim().min(1).max(200)

export const timesSourceSchema = z.object({
  kind: z.literal("times"),
  times: z
    .array(z.object({ time: hhmm, label: label.optional() }))
    .min(1)
    .max(MAX_ITEMS),
})

export const rruleSourceSchema = z.object({
  kind: z.literal("rrule"),
  rrule: z.string().trim().min(1).max(300),
  // Local datetime of the first occurrence (also the time of day), no offset.
  start: z.string().regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/, "Use YYYY-MM-DDTHH:mm"),
  label: label.optional(),
})

export const toolSourceSchema = z.object({
  kind: z.literal("tool"),
  // Connector instance id (e.g. "mcp-weather") and the tool name without namespace.
  connectorId: z.string().trim().min(1).max(80),
  tool: z.string().trim().min(1).max(120),
  args: z.record(z.string(), z.unknown()).default({}),
  // Local time the source is re-read every day.
  refreshAt: hhmm.default("00:05"),
  // Per-day fields: the tool is called for today and tomorrow ({{date}} in args)
  // and each field's value ("HH:mm" or an ISO timestamp) becomes one occurrence.
  items: z
    .array(z.object({ field: fieldPath, label: label.optional(), offsetMin }))
    .min(1)
    .max(MAX_ITEMS)
    .optional(),
  // Or one call that returns an array: each element's `at` field (ISO date or
  // datetime) becomes an occurrence, labeled by its `label` field.
  list: z
    .object({
      path: z.string().trim().max(200).default(""),
      at: fieldPath,
      label: fieldPath.optional(),
      // Time of day for date-only values.
      time: hhmm.default("09:00"),
      offsetMin,
    })
    .optional(),
})

export const sourceSchema = z.discriminatedUnion("kind", [timesSourceSchema, rruleSourceSchema, toolSourceSchema])
export type TimesSource = z.infer<typeof timesSourceSchema>
export type RruleSource = z.infer<typeof rruleSourceSchema>
export type ToolSource = z.infer<typeof toolSourceSchema>
export type ScheduleSource = z.infer<typeof sourceSchema>

export const answerSchema = z.object({
  // Stable id stored in the log ("done", "not_yet", "skip").
  id: z.string().regex(/^[a-z0-9_]{1,20}$/, "Use lowercase letters, digits and _ (max 20)"),
  label: z.string().trim().min(1).max(30),
  // Extra words that pick this answer in a reply ("udah", "done"). The id and
  // label always match.
  words: z.array(z.string().trim().min(1).max(40)).max(12).default([]),
  // Whether this answer closes the check-in (no more re-asks).
  closes: z.boolean().default(true),
  // Reply sent when the answer is recorded from a button or quoted reply.
  reply: z.string().trim().max(300).optional(),
})
export type AnswerOption = z.infer<typeof answerSchema>

export const DEFAULT_ANSWERS: AnswerOption[] = [
  { id: "done", label: "Sudah", words: ["udah", "udh", "dah", "done", "selesai", "beres"], closes: true },
  { id: "not_yet", label: "Belum", words: ["blm", "not yet", "nanti"], closes: false },
]

export const daysSchema = z.array(z.enum(DAY_CODES)).min(1).max(7).nullable()

export function normalizeAnswer(text: string): string {
  return text
    .toLowerCase()
    .replace(/[!.,?]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
}

// The answer option a reply picks: its id, its label or one of its words, exactly
// (case and trailing punctuation ignored). Null when nothing matches.
export function matchAnswer(options: AnswerOption[], text: string): AnswerOption | null {
  const t = normalizeAnswer(text)
  if (!t) return null
  for (const o of options) {
    const words = [o.id, o.id.replace(/_/g, " "), o.label, ...o.words].map(normalizeAnswer)
    if (words.includes(t)) return o
  }
  return null
}

// One line for a schedule, in the bot's language.
export function describeSource(source: ScheduleSource, days: readonly string[] | null): string {
  const d = days ? ` (${days.join(",")})` : ""
  switch (source.kind) {
    case "times":
      return `Tiap hari${d}: ${source.times.map((t) => `${t.label ? `${t.label} ` : ""}${t.time.replace(":", ".")}`).join(", ")}`
    case "rrule":
      return `RRULE ${source.rrule} mulai ${source.start}${d}`
    case "tool": {
      const what = source.items ? source.items.map((i) => `${i.label ?? i.field}${i.offsetMin ? ` ${i.offsetMin > 0 ? "+" : ""}${i.offsetMin}m` : ""}`).join(", ") : `list ${source.list?.path || "(result)"}`
      return `Dari ${source.connectorId}.${source.tool} (refresh ${source.refreshAt})${d}: ${what}`
    }
  }
}
