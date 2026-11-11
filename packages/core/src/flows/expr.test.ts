import { describe, expect, it } from "vitest"
import { localToUtc } from "../reminders/time"
import { evalCondition, evaluate, ExprError, MAX_NODES, parseExpr, parsePath, pathsOf, rootKey, type EvalEnv } from "./expr"

const TZ = "Asia/Makassar"
const NOW = localToUtc("2026-11-11T09:00", TZ)

function env(scope: Record<string, unknown> = {}): EvalEnv {
  return { scope, tz: TZ, now: NOW }
}

const run = (src: string, scope: Record<string, unknown> = {}) => evaluate(parseExpr(src), env(scope)).value

describe("parser", () => {
  it("parses precedence: not > cmp > and > or, * before +", () => {
    expect(run("1 + 2 * 3")).toBe(7)
    expect(run("(1 + 2) * 3")).toBe(9)
    expect(run("not 1 == 2 and 3 > 2")).toBe(true)
    expect(run("false or true and false")).toBe(false)
    expect(run("-2 * -3")).toBe(6)
  })

  it("reads paths with dots, numeric segments and brackets", () => {
    const scope = { steps: { bills: { data: { items: [{ due: "2026-11-13", id: "a" }] } } }, flow: { paid: { a: true } }, item: { id: "a" } }
    expect(run("steps.bills.data.items.0.due", scope)).toBe("2026-11-13")
    expect(run('steps.bills.data.items[0]["due"]', scope)).toBe("2026-11-13")
    expect(run("flow.paid[item.id]", scope)).toBe(true)
    expect(run("flow.paid[item.nope]", scope)).toBe(null)
  })

  it("rejects unknown roots, functions, = and && with a sentence", () => {
    expect(() => parseExpr("foo.bar")).toThrow(/Paths start with/)
    expect(() => parseExpr("eval(1)")).toThrow(/Unknown function "eval"/)
    expect(() => parseExpr("vars.a = 1")).toThrow(/"=="/)
    expect(() => parseExpr("true && false")).toThrow(/"and"/)
    expect(() => parseExpr("!true")).toThrow(/"not"/)
    expect(() => parseExpr("1 < 2 < 3")).toThrow(/don't chain/)
    expect(() => parseExpr("len(1, 2)")).toThrow(/takes 1 argument/)
    expect(() => parseExpr('"open')).toThrow(/closing quote/)
    expect(() => parseExpr("(1 + 2")).toThrow(ExprError)
    expect(() => parseExpr("1 2")).toThrow(/after the end/)
    expect(() => parseExpr("")).toThrow(/empty/)
  })

  it("caps the AST size at save time", () => {
    const big = Array.from({ length: MAX_NODES }, () => "1").join(" + ")
    expect(() => parseExpr(big)).toThrow(/more than 200 parts/)
  })

  it("parsePath only takes a plain path", () => {
    expect(parsePath("steps.x.data.items").root).toBe("steps")
    expect(() => parsePath("len(steps.x)")).toThrow(/plain path/)
  })

  it("lists the paths read and their taint roots", () => {
    const ast = parseExpr('daysUntil(item.due) < 3 and not flow.paid[item.id] or steps.ci.data.state == "x" or vars.n > 1')
    expect([...new Set(pathsOf(ast).map(rootKey))].sort()).toEqual(["flow.paid", "item", "steps.ci", "vars.n"])
  })
})

describe("semantics", () => {
  it("== is strict except numbers against numeric strings", () => {
    expect(run('3 == "3"')).toBe(true)
    expect(run('"3" == "3.0"')).toBe(false)
    expect(run('true == "true"')).toBe(false)
    expect(run("null == null")).toBe(true)
    expect(run("trigger.missing == null")).toBe(true)
    expect(run("trigger.missing != null")).toBe(false)
  })

  it("ordering with null is false, and odd types warn and give false", () => {
    expect(run("trigger.missing < 3")).toBe(false)
    expect(run("trigger.missing >= 3")).toBe(false)
    const r = evaluate(parseExpr("trigger.list > 1"), env({ trigger: { list: [1, 2] } }))
    expect(r.value).toBe(false)
    expect(r.warnings[0]).toMatch(/can't compare a list/)
  })

  it("compares numeric strings as numbers and dates as text", () => {
    expect(run('"10" > "9"')).toBe(true)
    expect(run('"2026-11-13" > "2026-11-12"')).toBe(true)
  })

  it("short-circuits and/or", () => {
    // The right side would blow the step budget if it ran.
    expect(run("false and len(steps.x) > 0")).toBe(false)
    expect(run("true or len(steps.x) > 0")).toBe(true)
  })

  it("division by zero is null; null counts as 0 in + and -", () => {
    expect(run("1 / 0")).toBe(null)
    expect(run("flow.streak + 1", { flow: {} })).toBe(1)
    expect(run("flow.streak - 1", { flow: { streak: 3 } })).toBe(2)
    expect(run('"a" + "b"')).toBe("ab")
    expect(run('"3" + 1')).toBe(4)
  })

  it("runs every function", () => {
    const scope = { trigger: { list: [{ s: "a" }, { s: "b" }, { s: "a" }], text: "Halo Dunia" } }
    expect(run("exists(trigger.text)", scope)).toBe(true)
    expect(run("empty(trigger.nope)", scope)).toBe(true)
    expect(run('empty("")')).toBe(true)
    expect(run('contains(trigger.text, "Dunia")', scope)).toBe(true)
    expect(run('contains(trigger.list, "a")', scope)).toBe(false)
    expect(run('startsWith(trigger.text, "Ha")', scope)).toBe(true)
    expect(run("len(trigger.list)", scope)).toBe(3)
    expect(run("lower(trigger.text)", scope)).toBe("halo dunia")
    expect(run('any(trigger.list, "s", "b")', scope)).toBe(true)
    expect(run('count(trigger.list, "s", "a")', scope)).toBe(2)
    expect(run('number("12.5")')).toBe(12.5)
    expect(run('number("x")')).toBe(null)
    expect(run("round(2.456, 2)")).toBe(2.46)
    expect(run("min(3, 5)")).toBe(3)
    expect(run("max(3, 5)")).toBe(5)
    expect(run('date("2026-11-11")')).toBe(localToUtc("2026-11-11T00:00", TZ))
    expect(run('daysUntil("2026-11-13")')).toBe(2)
    expect(run('daysUntil("2026-11-11T23:59")')).toBe(0)
    expect(run('hoursUntil("2026-11-11T12:00")')).toBe(3)
    expect(run('minutesSince("08:30")')).toBe(30)
    expect(run('weekday("2026-11-15")')).toBe("SU")
    expect(run("weekday()")).toBe("WE")
    expect(run("today()")).toBe("2026-11-11")
  })

  it("logs the operands a condition compared", () => {
    const r = evalCondition(parseExpr("daysUntil(item.due) < 3"), env({ item: { due: "2026-11-13" } }))
    expect(r.result).toBe(true)
    expect(r.trace).toEqual([
      { expr: "item.due", value: "2026-11-13" },
      { expr: "daysUntil(item.due)", value: 2 },
    ])
  })

  it("evaluates a large expression within the step budget", () => {
    // 60 nested len() over a long list is fine; the step cap is per evaluation.
    const list = Array.from({ length: 5 }, (_, i) => i)
    const src = Array.from({ length: 60 }, () => "len(trigger.list) + ").join("") + "0"
    const r = evaluate(parseExpr(src), env({ trigger: { list } }))
    expect(r.value).toBe(300)
  })

  it("never runs code from data", () => {
    const r = evaluate(parseExpr("trigger.payload.cmd == \"x\""), env({ trigger: { payload: { cmd: "process.exit(1)" } } }))
    expect(r.value).toBe(false)
  })
})
