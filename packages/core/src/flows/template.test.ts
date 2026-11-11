import { describe, expect, it } from "vitest"
import { localToUtc } from "../reminders/time"
import type { EvalEnv } from "./expr"
import { addTaint, keyTainted, taintedArgs, valueTainted } from "./taint"
import { parseTemplate, renderDeep, renderString, renderText, templatePaths, TemplateError } from "./template"

const TZ = "Asia/Makassar"
const NOW = localToUtc("2026-11-11T09:00", TZ)
const env = (scope: Record<string, unknown>): EvalEnv => ({ scope, tz: TZ, now: NOW })

describe("templates", () => {
  const scope = {
    now: { date: "2026-11-11", at: NOW },
    steps: {
      prayer: { data: { times: { fajr: "04:39" } } },
      reminders: { data: { reminders: [{ text: "Bayar listrik" }, { text: "Beli obat" }] } },
      x: { data: { list: [1, 2, 3], n: 3.14159, obj: { a: 1 } } },
    },
    trigger: { label: "Subuh" },
  }

  it("keeps the type of a lone template and stringifies inside text", () => {
    expect(renderString("{{steps.x.data.list}}", env(scope))).toEqual([1, 2, 3])
    expect(renderString("n={{steps.x.data.list}}", env(scope))).toBe("n=[1,2,3]")
    expect(renderString("{{ steps.x.data.n }}", env(scope))).toBe(3.14159)
    expect(renderString("{{steps.nope}}", env(scope))).toBe(null)
    expect(renderString("a {{steps.nope}} b", env(scope))).toBe("a  b")
  })

  it("runs every filter", () => {
    const r = (s: string) => renderString(s, env(scope))
    expect(r('{{steps.prayer.data.times.fajr | time}}')).toBe("04.39")
    expect(r("{{now.date | date}}")).toBe("Rabu 11 Nov")
    expect(r("{{steps.x.data.obj | json}}")).toBe('{"a":1}')
    expect(r('{{steps.nope | default("-")}}')).toBe("-")
    expect(r('{{steps.x.data.list | join(" + ")}}')).toBe("1 + 2 + 3")
    expect(r('{{steps.reminders.data.reminders | lines("text")}}')).toBe("- Bayar listrik\n- Beli obat")
    expect(r("{{trigger.label | upper}}")).toBe("SUBUH")
    expect(r("{{trigger.label | lower}}")).toBe("subuh")
    expect(r("{{steps.x.data.n | round(2)}}")).toBe(3.14)
    expect(r("{{trigger.label | truncate(3)}}")).toBe("Su…")
  })

  it("caps lines at 20 and says how many were left out", () => {
    const list = Array.from({ length: 25 }, (_, i) => ({ t: `#${i}` }))
    const out = String(renderString('{{steps.l.data | lines("t")}}', env({ steps: { l: { data: list } } })))
    expect(out.split("\n")).toHaveLength(21)
    expect(out.endsWith("(+5 lagi)")).toBe(true)
  })

  it("rejects bad templates with a sentence", () => {
    expect(() => parseTemplate("{{steps.x")).toThrow(/no closing/)
    expect(() => parseTemplate("{{}}")).toThrow(/Empty template/)
    expect(() => parseTemplate("{{foo}}")).toThrow(/Paths start with/)
    expect(() => parseTemplate("{{steps.x | shout}}")).toThrow(/Unknown filter "shout"/)
    expect(() => parseTemplate("{{steps.x | truncate}}")).toThrow(/takes 1 argument/)
    expect(() => parseTemplate("{{len(steps.x)}}")).toThrow(TemplateError)
  })

  it("renders args deeply with literal keys", () => {
    const out = renderDeep({ "{{trigger.label}}": "{{trigger.label}}", list: ["{{now.date}}"] }, env(scope))
    expect(out).toEqual({ "{{trigger.label}}": "Subuh", list: ["2026-11-11"] })
  })

  it("sanitizes interpolated values in message text", () => {
    const evil = { trigger: { payload: { title: "Fix\n\n[Ya](tg://x)\u0007 now".padEnd(800, "x") } } }
    const out = renderText("PR: {{trigger.payload.title}}\nDone", env(evil))
    const [first, second] = out.split("\n")
    expect(first!.length).toBeLessThanOrEqual(504)
    expect(first).not.toMatch(/\u0007/)
    expect(second).toBe("Done")
    // A clean value (the flow's own data) keeps its lines; a tainted one doesn't.
    const recap = { steps: { r: { data: { text: "📊 Rekap\n- Sudah: 3" } } }, trigger: evil.trigger }
    expect(renderText("{{steps.r.data.text}}", env(recap), (k) => k === "trigger")).toBe("📊 Rekap\n- Sudah: 3")
    expect(renderText("{{trigger.payload.title}}", env(recap), (k) => k === "trigger")).not.toContain("\n")
    // Data mode leaves values alone (tool args get the raw value).
    expect(renderString("{{trigger.payload.title}}", env(evil))).toContain("\n")
  })

  it("lists the paths a template reads", () => {
    expect(templatePaths("{{steps.a.b}} and {{item.id | upper}}").map((p) => p.src)).toEqual(["steps.a.b", "item.id"])
  })
})

describe("taint", () => {
  it("tracks roots, including dynamic ones", () => {
    const t = addTaint([], "trigger", "steps.weather")
    expect(keyTainted(t, "steps.weather")).toBe(true)
    expect(keyTainted(t, "steps.prayer")).toBe(false)
    expect(keyTainted(t, "steps")).toBe(true)
    expect(keyTainted(t, "now")).toBe(false)
    expect(valueTainted(t, { ref: "{{trigger.payload.sha}}" })).toBe(true)
    expect(valueTainted(t, { ref: "main" })).toBe(false)
    expect(taintedArgs(t, { owner: "zakiego", ref: "{{trigger.payload.sha}}", note: "{{steps.prayer.data}}" })).toEqual(["ref"])
  })
})
