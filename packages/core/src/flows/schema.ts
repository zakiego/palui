// The flow definition (RFC flows section 4): a small JSON document with
// triggers[] and steps[]. Validated by zod here and by the graph and data checks
// in validate.ts. Step and trigger kinds are discriminated unions on `kind`, so a
// new kind needs no migration. `schema` versions the format:
// upgradeDefinition() turns an older stored definition into the current shape.
import { z } from "zod"
import { answerSchema, daysSchema, DEFAULT_ANSWERS, sourceSchema } from "./source-config"

export const DEFINITION_SCHEMA = 1

export const ID_RE = /^[a-z][a-z0-9_]{0,31}$/
export const MAX_STEPS = 40
export const MAX_TRIGGERS = 8
export const MAX_DEFINITION_BYTES = 32 * 1024
// onError values that aren't step ids.
export const RESERVED_IDS = ["fail", "continue"]
export const MAX_EACH = 50
export const DEFAULT_EACH = 20
export const MAX_WAIT_MIN = 7 * 24 * 60

const id = z.string().regex(ID_RE, "Ids use lowercase letters, digits and _, starting with a letter (max 32), like ask_1")
const target = id.nullable()
const text = (max: number) => z.string().trim().min(1).max(max)

// --- Triggers -----------------------------------------------------------------------------

const triggerBase = {
  id,
  // The first step this trigger runs.
  start: id,
  // A schedule run more than this many minutes late (after downtime) is skipped.
  lateGraceMin: z.number().int().min(1).max(1440).optional(),
}

export const scheduleTriggerSchema = z.object({ ...triggerBase, kind: z.literal("schedule"), source: sourceSchema, days: daysSchema.optional() })

export const webhookTriggerSchema = z.object({ ...triggerBase, kind: z.literal("webhook"), hookId: text(80) })

export const CHAT_COMMAND_RE = /^\/[a-z0-9_]{1,32}$/
export const chatTriggerSchema = z.object({
  ...triggerBase,
  kind: z.literal("chat"),
  command: z.string().trim().regex(CHAT_COMMAND_RE, 'A command looks like "/beres" (lowercase, max 32)').optional(),
  phrases: z.array(text(60)).min(1).max(10).optional(),
})

export const pollTriggerSchema = z.object({
  ...triggerBase,
  kind: z.literal("poll"),
  tool: text(130),
  connectorId: text(80).optional(),
  args: z.record(z.string(), z.unknown()).default({}),
  everyMin: z.number().int().min(5).max(1440),
  // A dotted path into the tool's result ("state", "items.0.due"); empty means the whole result.
  select: z
    .string()
    .trim()
    .max(200)
    .regex(/^[A-Za-z0-9_.-]*$/, "Use a dotted path like items.0.state")
    .default(""),
  fire: z.enum(["change", "truthy"]).default("change"),
})

export const FLOW_EVENTS = ["checkin.answered", "checkin.missed", "run.failed", "run.done"] as const
export type FlowEventName = (typeof FLOW_EVENTS)[number]
export const eventTriggerSchema = z.object({ ...triggerBase, kind: z.literal("event"), event: z.enum(FLOW_EVENTS), flow: text(80).default("self") })

export const manualTriggerSchema = z.object({ ...triggerBase, kind: z.literal("manual") })

export const triggerSchema = z.discriminatedUnion("kind", [scheduleTriggerSchema, webhookTriggerSchema, chatTriggerSchema, pollTriggerSchema, eventTriggerSchema, manualTriggerSchema])
export type FlowTrigger = z.infer<typeof triggerSchema>
export type TriggerKind = FlowTrigger["kind"]
export type ScheduleTrigger = z.infer<typeof scheduleTriggerSchema>
export type PollTrigger = z.infer<typeof pollTriggerSchema>
export type ChatTrigger = z.infer<typeof chatTriggerSchema>
export type EventTrigger = z.infer<typeof eventTriggerSchema>
export type WebhookTrigger = z.infer<typeof webhookTriggerSchema>

// --- Steps --------------------------------------------------------------------------------

const stepBase = {
  id,
  // The step after this one; omitted means the next one in the array, null ends the run.
  next: target.optional(),
  name: text(80).optional(),
  // "fail" (default), "continue" (output { ok: false, error } and go on) or a step id.
  onError: z.union([z.enum(["fail", "continue"]), id]).optional(),
  timeoutSec: z.number().int().min(1).max(60).optional(),
}

export const toolStepSchema = z.object({
  ...stepBase,
  kind: z.literal("tool"),
  // "<connectorId>__<tool>" as cari_tool returns it, or the bare tool name with connectorId.
  tool: text(130),
  connectorId: text(80).optional(),
  args: z.record(z.string(), z.unknown()).default({}),
  parse: z.object({ field: z.string().trim().max(200).default("text"), as: z.literal("json") }).optional(),
  retry: z.object({ max: z.number().int().min(0).max(5), backoffSec: z.number().int().min(5).max(3600).default(30) }).optional(),
  // A write tool that is safe to repeat after a crash mid-call.
  idempotent: z.boolean().optional(),
})

export const ifStepSchema = z.object({ ...stepBase, kind: z.literal("if"), when: text(2000), then: target, else: target.default(null) })

export const messageStepSchema = z.object({
  ...stepBase,
  kind: z.literal("message"),
  text: text(4000),
  // A literal chat id of another chat the owner owns or allowed. Never a template.
  to: text(80).optional(),
})

// When a check-in stops waiting, up to a week out:
// - next: the trigger's next occurrence (schedule paths only), shifted by plusMin
//   ({kind:"next", plusMin:-10} is 10 minutes before it).
// - after: N minutes, a number or a template that renders to one ("{{vars.left}}").
// - at: a time from a template or an ISO date/datetime, like wait.until, shifted by
//   plusMin ({kind:"at", at:"{{steps.nextp.data.next.timestamp}}", plusMin:-10}).
// A deadline already past when the check-in is reached skips the ask and goes the
// missed way without sending anything.
export const deadlineSchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("next"), plusMin: z.number().int().min(-1440).max(1440).optional() }),
  z.object({ kind: z.literal("after"), minutes: z.union([z.number().int().min(1).max(MAX_WAIT_MIN), text(300)]) }),
  z.object({ kind: z.literal("at"), at: text(300), plusMin: z.number().int().min(-MAX_WAIT_MIN).max(MAX_WAIT_MIN).optional() }),
])
export type CheckinDeadline = z.infer<typeof deadlineSchema>

export const checkinStepSchema = z.object({
  ...stepBase,
  kind: z.literal("checkin"),
  text: text(1000),
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
      max: z.number().int().min(0).max(20).default(3),
      text: text(1000).optional(),
    })
    .default({ everyMin: 30, max: 3 }),
  deadline: deadlineSchema.default({ kind: "next" }),
  missedText: text(1000).optional(),
  // Per closing answer id, the step to go to.
  on: z.record(z.string(), target).optional(),
  onMissed: target.optional(),
})

export const waitStepSchema = z.object({
  ...stepBase,
  kind: z.literal("wait"),
  minutes: z.number().int().min(1).max(MAX_WAIT_MIN).optional(),
  until: text(300).optional(),
  plusMin: z.number().int().min(-MAX_WAIT_MIN).max(MAX_WAIT_MIN).optional(),
})

export const promptStepSchema = z.object({
  ...stepBase,
  kind: z.literal("prompt"),
  // A literal instruction. Data goes in `input`, never here.
  prompt: text(4000),
  input: z.record(z.string().regex(/^[A-Za-z][A-Za-z0-9_]{0,31}$/), z.string().max(2000)).default({}),
  // Namespaced tools the turn may use. Default none.
  tools: z.array(text(130)).max(20).default([]),
  // true: the reply goes to the chat directly.
  send: z.boolean().default(false),
  model: z.enum(["main", "fast"]).default("main"),
})

export const setStepSchema = z.object({
  ...stepBase,
  kind: z.literal("set"),
  scope: z.enum(["run", "flow"]).default("run"),
  // A value with "{{" is a template, anything else an expression.
  values: z
    .record(z.string().regex(ID_RE, "Variable names use lowercase letters, digits and _, starting with a letter (max 32), like last_at"), text(2000))
    .refine((v) => Object.keys(v).length >= 1 && Object.keys(v).length <= 20, "Set 1 to 20 values"),
})

export const eachStepSchema = z.object({
  ...stepBase,
  kind: z.literal("each"),
  items: text(300),
  where: text(2000).optional(),
  // A template: the dedupe key per item, across runs.
  key: text(300).optional(),
  max: z.number().int().min(1).max(MAX_EACH).default(DEFAULT_EACH),
  body: id,
})

export const stepSchema = z.discriminatedUnion("kind", [toolStepSchema, ifStepSchema, messageStepSchema, checkinStepSchema, waitStepSchema, promptStepSchema, setStepSchema, eachStepSchema])
export type FlowStep = z.infer<typeof stepSchema>
export type StepKind = FlowStep["kind"]
export type ToolStep = z.infer<typeof toolStepSchema>
export type IfStep = z.infer<typeof ifStepSchema>
export type MessageStep = z.infer<typeof messageStepSchema>
export type CheckinStep = z.infer<typeof checkinStepSchema>
export type WaitStep = z.infer<typeof waitStepSchema>
export type PromptStep = z.infer<typeof promptStepSchema>
export type SetStep = z.infer<typeof setStepSchema>
export type EachStep = z.infer<typeof eachStepSchema>

// --- Definition ---------------------------------------------------------------------------

export const definitionSchema = z.object({
  schema: z.literal(DEFINITION_SCHEMA).default(DEFINITION_SCHEMA),
  name: text(80),
  description: text(200).optional(),
  tz: z.string().trim().min(1).max(64).optional(),
  triggers: z.array(triggerSchema).max(MAX_TRIGGERS).default([]),
  steps: z.array(stepSchema).min(1).max(MAX_STEPS),
  limits: z
    .object({
      runsPerHour: z.number().int().min(1).max(30).optional(),
      dailyTokens: z.number().int().min(1000).max(500_000).optional(),
    })
    .optional(),
})
export type FlowDefinition = z.infer<typeof definitionSchema>
export type FlowDefinitionInput = z.input<typeof definitionSchema>

// Older stored definitions -> the current shape. Version 1 is the first, so this
// is the identity today; a format change adds a case here instead of a data migration.
export function upgradeDefinition(raw: unknown): unknown {
  if (!raw || typeof raw !== "object") return raw
  const v = (raw as { schema?: unknown }).schema ?? DEFINITION_SCHEMA
  if (v === DEFINITION_SCHEMA) return raw
  return raw
}

// Sorted-key JSON: equal definitions hash equal whatever order the keys came in.
export function canonicalJson(v: unknown): string {
  return (
    JSON.stringify(v, (_k, x: unknown) => (x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x as Record<string, unknown>).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) : x)) ?? ""
  )
}

// `input` is the parsed value: an id or key that breaks the naming rule is quoted
// in the message, so "steps[1].id" says which name to fix and how.
export function zodProblems(e: z.ZodError, input?: unknown): { path: string; message: string }[] {
  return e.issues.slice(0, 12).map((i) => ({ path: formatPath(i.path), message: issueMessage(i, input) }))
}

function issueMessage(i: z.core.$ZodIssue, input: unknown): string {
  // A record key that fails its schema: zod only says "Invalid key in record".
  if (i.code === "invalid_key") {
    const key = String(i.path.at(-1) ?? "")
    const inner = i.issues[0]?.message
    return `Key "${key}": ${inner && inner !== "Invalid input" ? inner : "not allowed here"}.`
  }
  if (i.code === "invalid_format" && i.format === "regex") {
    const v = valueAt(input, i.path)
    if (typeof v === "string") return `"${v}": ${i.message}.`
  }
  return i.message
}

function valueAt(v: unknown, path: readonly PropertyKey[]): unknown {
  let cur = v
  for (const p of path) {
    if (!cur || typeof cur !== "object") return undefined
    cur = (cur as Record<PropertyKey, unknown>)[p]
  }
  return cur
}

export function formatPath(path: readonly PropertyKey[]): string {
  let out = ""
  for (const p of path) {
    if (typeof p === "number") out += `[${p}]`
    else out += out ? `.${String(p)}` : String(p)
  }
  return out || "(root)"
}
