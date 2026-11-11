// The routine config model. A routine is: WHEN (a time source) + WHAT (an action)
// + optional recap, plus a zone, active days and pause state. Sources and actions
// are discriminated unions on `kind`: adding a kind means one more schema here and
// one more entry in sources.ts / the engine's action switch, never a migration.
//
//   source  times     fixed local times every (active) day
//           rrule     one RFC 5545 rule with a start (weekly review, monthly bill)
//           tool      times read from a connector tool's result (any MCP server),
//                     refreshed daily; `items` picks fields, `list` walks an array
//   action  message   send a text
//           checkin   ask a question with answer options, re-ask until a closing
//                     answer, the max number of re-asks or the deadline
//           prompt    run a prompt as an unattended turn
//
// Message text is a template: {{name}} {{label}} {{time}} {{date}} {{answers}} {{n}}.
// Tool args are templates too: {{date}} (YYYY-MM-DD) {{month}} (YYYY-MM) {{year}} {{tz}}.
import { z } from "zod"

export const DAY_CODES = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"] as const
export type DayCode = (typeof DAY_CODES)[number]

export const MAX_ITEMS = 24

const hhmm = z.string().regex(/^([01]\d|2[0-3]):[0-5]\d$/, "Use HH:mm (24h, two-digit hour)")

const label = z.string().trim().min(1).max(60)
const template = z.string().trim().min(1).max(1000)
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
export type RoutineSource = z.infer<typeof sourceSchema>

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

export const deadlineSchema = z.discriminatedUnion("kind", [
  // Until the routine's next occurrence (or 24 h when there is none).
  z.object({ kind: z.literal("next") }),
  z.object({ kind: z.literal("after"), minutes: z.number().int().min(1).max(2880) }),
])

export const messageActionSchema = z.object({ kind: z.literal("message"), text: template })

export const checkinActionSchema = z.object({
  kind: z.literal("checkin"),
  text: template,
  answers: z
    .array(answerSchema)
    .min(1)
    .max(6)
    .default(DEFAULT_ANSWERS)
    .refine((a) => new Set(a.map((x) => x.id)).size === a.length, "Answer ids must be unique")
    .refine((a) => a.some((x) => x.closes), "At least one answer must close the check-in"),
  followUp: z
    .object({
      everyMin: z.number().int().min(5).max(1440).default(30),
      // 0 means ask once.
      max: z.number().int().min(0).max(20).default(3),
      text: template.optional(),
    })
    .default({ everyMin: 30, max: 3 }),
  deadline: deadlineSchema.default({ kind: "next" }),
  // Sent when the deadline passes with no closing answer. Omit to stay quiet.
  missedText: template.optional(),
})

export const promptActionSchema = z.object({ kind: z.literal("prompt"), prompt: z.string().trim().min(1).max(2000) })

export const actionSchema = z.discriminatedUnion("kind", [messageActionSchema, checkinActionSchema, promptActionSchema])
export type MessageAction = z.infer<typeof messageActionSchema>
export type CheckinAction = z.infer<typeof checkinActionSchema>
export type PromptAction = z.infer<typeof promptActionSchema>
export type RoutineAction = z.infer<typeof actionSchema>

export const recapSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("off") }),
  z.object({ kind: z.literal("daily"), time: hhmm }),
  z.object({ kind: z.literal("weekly"), day: z.enum(DAY_CODES), time: hhmm }),
])
export type RoutineRecap = z.infer<typeof recapSchema>

export const daysSchema = z.array(z.enum(DAY_CODES)).min(1).max(7).nullable()

// Checks a schema can't express. Returns the first problem as a sentence.
export function configProblem(source: RoutineSource, action: RoutineAction): string | null {
  if (source.kind === "tool") {
    if (!!source.items === !!source.list) return 'A tool source needs exactly one of "items" (fields per day) or "list" (an array of dated entries).'
  }
  if (action.kind === "checkin" && action.deadline.kind === "after" && action.followUp.max > 0) {
    if (action.followUp.everyMin >= action.deadline.minutes) return "The re-ask interval is not shorter than the deadline, so no re-ask would ever go out. Shorten followUp.everyMin or set followUp.max to 0."
  }
  return null
}

// zod error -> one readable line for the model.
export function zodProblem(e: z.ZodError): string {
  return e.issues
    .slice(0, 4)
    .map((i) => `${i.path.join(".") || "(root)"}: ${i.message}`)
    .join("; ")
}
