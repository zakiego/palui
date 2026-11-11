import { describe, expect, it } from "vitest"
import { computeNext, createRoutine, parseConfig, type RoutineInput } from "../routines/service"
import { refreshToolSource, upcoming } from "../routines/sources"
import { fakePrayerTool, MEDICINE_ROUTINE, PRAYER_ROUTINE } from "../routines/test-helpers"
import { setup, NOW } from "../reminders/test-helpers"
import { convertTemplate, flowToRoutine, routineToFlow } from "./convert"
import { nextScheduled, scheduleInput } from "./service"
import type { ScheduleTrigger } from "./schema"
import { EXAMPLE_TOOLS, SHOLAT } from "./test-helpers"
import { resolverFrom, validateDefinition } from "./validate"

const WITA = "Asia/Makassar"

// Every routine shape the routine tests use, plus days and a list source.
const SHAPES: Record<string, RoutineInput> = {
  prayer: PRAYER_ROUTINE,
  medicine: MEDICINE_ROUTINE,
  weekly: { name: "Review mingguan", tz: WITA, source: { kind: "rrule", rrule: "FREQ=WEEKLY;BYDAY=FR", start: "2026-11-13T16:00", label: "Jumat" }, action: { kind: "message", text: "📝 {{name}}: waktunya review minggu ini ({{date}})." }, recap: { kind: "weekly", day: "SU", time: "20:00" } },
  weekdays: { name: "Air", tz: WITA, days: ["TU", "TH"], source: { kind: "times", times: [{ time: "11:00" }, { time: "15:30", label: "sore" }] }, action: { kind: "prompt", prompt: "Ingetin minum air jam {{time}} dengan gaya lucu." } },
  bills: {
    name: "Tagihan",
    tz: WITA,
    source: { kind: "tool", connectorId: "mcp-keuangan", tool: "list_bills", args: {}, list: { path: "bills", at: "due", label: "name", time: "09:00", offsetMin: -1440 } },
    action: { kind: "message", text: "Besok jatuh tempo: {{label}}" },
    recap: { kind: "daily", time: "21:00" },
  },
  jakarta: { name: "Jakarta", tz: "Asia/Jakarta", source: { kind: "times", times: [{ time: "07:00", label: "pagi" }] }, action: { kind: "message", text: "Pagi!" } },
}

const bills = () => ({ bills: [{ name: "Listrik", due: "2026-11-12" }, { name: "Internet", due: "2026-11-20" }, { name: "Air", due: "2026-12-01T10:00" }] })

async function stateFor(input: RoutineInput, now: number) {
  const cfg = parseConfig(input)
  if (cfg.source.kind !== "tool") return {}
  const call = cfg.source.connectorId === "mcp-keuangan" ? async () => bills() : fakePrayerTool()
  return (await refreshToolSource(cfg.source, cfg.tz, {}, call, now)).state
}

describe("routineToFlow", () => {
  for (const [name, input] of Object.entries(SHAPES)) {
    it(`${name}: validates and round-trips the next 50 occurrences`, async () => {
      const cfg = parseConfig(input)
      const def = routineToFlow(cfg)
      const v = validateDefinition(def, { resolveTool: resolverFrom(EXAMPLE_TOOLS) })
      expect(v.problems).toEqual([])
      expect(v.writeCalls).toEqual([])
      const main = v.definition!.triggers.find((t) => t.id === "main") as ScheduleTrigger
      expect(main.source).toEqual(cfg.source)

      // The old engine's walk (computeNext from the routine row) against the new
      // one's (nextScheduled from the trigger row), cursor by cursor.
      const { db, chatId } = setup()
      const state = await stateFor(input, NOW)
      const routine = { ...createRoutine(db, chatId, input, NOW), sourceState: state }
      let rc = { ...routine }
      let tc = { cursor: routine.cursor, sourceState: state }
      const oldTimes: string[] = []
      const newTimes: string[] = []
      for (let i = 0; i < 50; i++) {
        const a = computeNext(rc, NOW)
        const b = nextScheduled(main, v.definition!.tz ?? WITA, tc, NOW)
        if (a) oldTimes.push(`${a.key}@${a.at}:${a.label}`)
        if (b) newTimes.push(`${b.key}@${b.at}:${b.label}`)
        if (!a || !b) break
        rc = { ...rc, cursor: { at: a.at, key: a.key } }
        tc = { ...tc, cursor: { at: b.at, key: b.key } }
      }
      expect(newTimes).toEqual(oldTimes)
      expect(oldTimes.length).toBeGreaterThan(0)
      // And the bulk view the tools show.
      expect(upcoming(scheduleInput(main, cfg.tz, { sourceState: state }), { at: NOW, key: "" }, 50)).toEqual(upcoming({ source: cfg.source, tz: cfg.tz, days: cfg.days, state }, { at: NOW, key: "" }, 50))
    })
  }

  it("flowToRoutine inverts routineToFlow for every shape", () => {
    for (const input of Object.values(SHAPES)) {
      const cfg = parseConfig(input)
      const def = validateDefinition(routineToFlow(cfg), { resolveTool: resolverFrom(EXAMPLE_TOOLS) }).definition!
      expect(flowToRoutine(def)).toEqual(cfg)
    }
    const edited = validateDefinition({ ...routineToFlow(parseConfig(MEDICINE_ROUTINE)), steps: [{ id: "ask", kind: "message", text: "x", next: "more" }, { id: "more", kind: "message", text: "y" }] }).definition!
    expect(flowToRoutine(edited)).toBeNull()
  })

  it("produces the 9e shape for Sholat 5 waktu", () => {
    const cfg = parseConfig({
      name: "Sholat 5 waktu",
      tz: WITA,
      source: SHOLAT.triggers[0]!.source as RoutineInput["source"],
      action: { kind: "checkin", text: "Udah sholat {{label}} ({{time}})?", answers: SHOLAT.steps[0]!.answers, followUp: { everyMin: 15, max: 4 }, deadline: { kind: "next" } },
      recap: { kind: "weekly", day: "SU", time: "20:00" },
    })
    const def = routineToFlow(cfg) as unknown as typeof SHOLAT
    expect(def.triggers[0]).toEqual({ ...SHOLAT.triggers[0], start: "ask", days: undefined } as never)
    expect(def.triggers[1]).toMatchObject({ id: "recap", kind: "schedule", start: "recap_read", source: { kind: "rrule", rrule: "FREQ=WEEKLY;BYDAY=SU", start: "2026-01-11T20:00" } })
    expect(def.steps.map((s) => s.id)).toEqual(["ask", "recap_read", "recap_any", "recap_send"])
    expect(def.steps[0]).toMatchObject({ kind: "checkin", text: "Udah sholat {{trigger.label}} ({{trigger.time}})?", deadline: { kind: "next" }, followUp: { everyMin: 15, max: 4 } })
    expect(def.steps[1]).toMatchObject({ tool: "flows__ringkasan", args: { flow: "self", days: 7, trigger: "main" } })
  })

  it("converts every routine template name, and keeps unknown braces literal", () => {
    expect(convertTemplate("{{name}} {{ label }} {{time}} {{date}} {{answers}} ke-{{n}} {{other}}", "Sudah / Belum")).toBe(
      "{{flow.name}} {{trigger.label}} {{trigger.time}} {{trigger.date}} Sudah / Belum ke-{{run.followUps}} {other}",
    )
  })

  it("moves a prompt's templates into its data block and keeps tool access", () => {
    const def = routineToFlow(parseConfig(SHAPES.weekdays!)) as unknown as { steps: { prompt: string; tools: string[]; send: boolean; input: Record<string, string> }[] }
    expect(def.steps[0]).toMatchObject({ prompt: "Ingetin minum air jam [time] dengan gaya lucu.", tools: ["*"], send: true, input: { time: "{{trigger.time}}" } })
  })
})
