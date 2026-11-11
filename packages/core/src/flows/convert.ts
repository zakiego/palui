// routineToFlow (RFC flows 3.6, 9e): a routine as the flow of the canonical
// shape. The routine's source becomes the `main` schedule trigger unchanged, its
// action becomes one step, and a recap becomes a second schedule trigger with a
// built-in read step (flows__ringkasan) guarded by "anything logged?", which
// keeps today's recap text and its silence on an empty period.
//
// Routine message templates ({{label}} {{time}} {{date}} {{name}} {{answers}}
// {{n}}) become flow templates. {{date}} inside a tool source's args stays: it's
// a source-arg template ("the local date being fetched"), separate from steps.
import type { DayCode } from "../routines/config"
import type { ParsedConfig } from "../routines/service"
import type { FlowDefinition, FlowDefinitionInput } from "./schema"

// A routine's recap reads the log of the `main` trigger.
export const MAIN_TRIGGER = "main"
export const RECAP_TRIGGER = "recap"
// Monday 2026-01-05 + n days: a fixed first occurrence for a weekly recap rule.
const WEEKDAY_ANCHOR: Record<DayCode, string> = { MO: "2026-01-05", TU: "2026-01-06", WE: "2026-01-07", TH: "2026-01-08", FR: "2026-01-09", SA: "2026-01-10", SU: "2026-01-11" }
// Routine recaps more than 2 hours late are skipped (routines/engine RECAP_LATE_MS).
const RECAP_GRACE_MIN = 120

export function convertTemplate(t: string, answers: string): string {
  const map: Record<string, string> = {
    name: "{{flow.name}}",
    label: "{{trigger.label}}",
    time: "{{trigger.time}}",
    date: "{{trigger.date}}",
    answers,
    n: "{{run.followUps}}",
  }
  return t.replace(/\{\{\s*([^}]*?)\s*\}\}/g, (m, k: string) => map[k] ?? m.replace("{{", "{").replace("}}", "}"))
}

export function routineToFlow(r: ParsedConfig): FlowDefinitionInput {
  const a = r.action
  const answers = a.kind === "checkin" ? a.answers.map((x) => x.label).join(" / ") : ""
  const conv = (t: string) => convertTemplate(t, answers)
  const steps: Record<string, unknown>[] = []
  if (a.kind === "message") steps.push({ id: "send", kind: "message", text: conv(a.text), next: null })
  else if (a.kind === "checkin") {
    steps.push({
      id: "ask",
      kind: "checkin",
      text: conv(a.text),
      answers: a.answers.map((x) => ({ ...x, ...(x.reply ? { reply: conv(x.reply) } : {}) })),
      followUp: { everyMin: a.followUp.everyMin, max: a.followUp.max, ...(a.followUp.text ? { text: conv(a.followUp.text) } : {}) },
      deadline: a.deadline,
      ...(a.missedText ? { missedText: conv(a.missedText) } : {}),
      next: null,
    })
  } else {
    // The routine ran its prompt as an unattended turn that could use tools and
    // sent the answer; the occurrence moves into the data block.
    steps.push({
      id: "run",
      kind: "prompt",
      prompt: a.prompt.replace(/\{\{\s*(name|label|time|date|answers|n)\s*\}\}/g, (_m, k: string) => `[${k}]`),
      input: { name: "{{flow.name}}", label: "{{trigger.label}}", time: "{{trigger.time}}", date: "{{trigger.date}}" },
      tools: ["*"],
      send: true,
      next: null,
    })
  }
  const triggers: Record<string, unknown>[] = [
    { id: MAIN_TRIGGER, kind: "schedule", start: steps[0]!.id, lateGraceMin: r.lateGraceMin, source: r.source, ...(r.days ? { days: r.days } : {}) },
  ]
  if (r.recap.kind !== "off") {
    const source =
      r.recap.kind === "daily"
        ? { kind: "times", times: [{ time: r.recap.time, label: "Rekap" }] }
        : { kind: "rrule", rrule: `FREQ=WEEKLY;BYDAY=${r.recap.day}`, start: `${WEEKDAY_ANCHOR[r.recap.day]}T${r.recap.time}`, label: "Rekap" }
    triggers.push({ id: RECAP_TRIGGER, kind: "schedule", start: "recap_read", lateGraceMin: RECAP_GRACE_MIN, source })
    steps.push(
      { id: "recap_read", kind: "tool", tool: "flows__ringkasan", args: { flow: "self", days: r.recap.kind === "daily" ? 1 : 7, trigger: MAIN_TRIGGER } },
      { id: "recap_any", kind: "if", when: "steps.recap_read.data.total > 0", then: "recap_send", else: null },
      { id: "recap_send", kind: "message", text: "{{steps.recap_read.data.text}}" },
    )
  }
  return { schema: 1, name: r.name, tz: r.tz, triggers, steps } as unknown as FlowDefinitionInput
}

// The routine config of a flow of the canonical shape (what routineToFlow makes),
// or null when the flow was edited into something a routine can't express. The
// routine tools use it as thin wrappers over flows.
export function flowToRoutine(def: FlowDefinition): ParsedConfig | null {
  const main = def.triggers.find((t) => t.id === MAIN_TRIGGER)
  if (main?.kind !== "schedule") return null
  const first = def.steps.find((s) => s.id === main.start)
  if (!first) return null
  const back = (t: string) =>
    t
      .replaceAll("{{flow.name}}", "{{name}}")
      .replaceAll("{{trigger.label}}", "{{label}}")
      .replaceAll("{{trigger.time}}", "{{time}}")
      .replaceAll("{{trigger.date}}", "{{date}}")
      .replaceAll("{{run.followUps}}", "{{n}}")
  let action: ParsedConfig["action"]
  if (first.kind === "message") action = { kind: "message", text: back(first.text) }
  else if (first.kind === "checkin") {
    // A routine only knows "next" and a fixed "after" up to two days.
    const d = first.deadline
    if (d.kind === "at" || (d.kind === "next" && d.plusMin) || (d.kind === "after" && (typeof d.minutes !== "number" || d.minutes > 2880))) return null
    const deadline = d.kind === "after" ? { kind: "after" as const, minutes: d.minutes as number } : { kind: "next" as const }
    const labels = first.answers.map((a) => a.label).join(" / ")
    const backC = (t: string) => back(t).replaceAll(labels, "{{answers}}")
    action = {
      kind: "checkin",
      text: backC(first.text),
      answers: first.answers.map((a) => ({ ...a, ...(a.reply ? { reply: back(a.reply) } : {}) })),
      followUp: { everyMin: first.followUp.everyMin, max: first.followUp.max, ...(first.followUp.text ? { text: backC(first.followUp.text) } : {}) },
      deadline,
      ...(first.missedText ? { missedText: backC(first.missedText) } : {}),
    }
  } else if (first.kind === "prompt") action = { kind: "prompt", prompt: first.prompt.replace(/\[(name|label|time|date|answers|n)\]/g, "{{$1}}") }
  else return null
  if ((first.next ?? null) !== null) return null
  const recapT = def.triggers.find((t) => t.id === RECAP_TRIGGER)
  let recap: ParsedConfig["recap"] = { kind: "off" }
  if (recapT?.kind === "schedule") {
    const src = recapT.source
    if (src.kind === "times" && src.times.length === 1) recap = { kind: "daily", time: src.times[0]!.time }
    else if (src.kind === "rrule") {
      const day = /BYDAY=(MO|TU|WE|TH|FR|SA|SU)$/.exec(src.rrule)?.[1] as DayCode | undefined
      if (!day) return null
      recap = { kind: "weekly", day, time: src.start.slice(11, 16) }
    } else return null
  }
  if (def.triggers.some((t) => t.id !== MAIN_TRIGGER && t.id !== RECAP_TRIGGER)) return null
  return { name: def.name, tz: def.tz ?? "Asia/Makassar", days: (main.days ?? null) as DayCode[] | null, source: main.source, action, recap, lateGraceMin: main.lateGraceMin ?? 60 }
}
