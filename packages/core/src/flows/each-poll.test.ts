import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { flowRuns } from "@palui/db"
import { MIN } from "../reminders/test-helpers"
import { answerFlowCheckin } from "./checkin"
import { dryRunFlow } from "./dryrun"
import { getFlow, triggersOf } from "./service"
import { at, exampleConnectors, flowHarness, runsOf, sentTexts, stepLog } from "./test-harness"
import { BILLS, EXAMPLE_TOOLS } from "./test-helpers"
import { resolverFrom, validateDefinition } from "./validate"

const bills = (items: { id: string; name: string; amount: number; due: string }[]) => () => ({ items })

describe("each with cross-run keys (RFC 9c)", () => {
  const LIST = [
    { id: "pln", name: "Listrik", amount: 350000, due: "2026-11-12" },
    { id: "net", name: "Internet", amount: 400000, due: "2026-11-25" },
    { id: "bpjs", name: "BPJS", amount: 150000, due: "2026-11-11" },
  ]

  it("asks once per bill due within 3 days, never twice for the same bill and due date", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-keuangan__list_bills": bills(LIST) }) })
    const { flow } = h.create(BILLS)
    await h.run(at("2026-11-11T09:00"))
    const runs = runsOf(h.db, flow.id)
    const parent = runs.find((r) => !r.parentRunId)!
    expect(parent.status).toBe("done")
    expect((parent.vars as { steps: Record<string, unknown> }).steps.per_bill).toEqual({ count: 2, started: 2, deduped: 0, dropped: 0 })
    const children = runs.filter((r) => r.parentRunId === parent.id)
    expect(children.map((r) => r.runKey).sort()).toEqual(["each:per_bill:bpjs:2026-11-11", "each:per_bill:pln:2026-11-12"])
    expect(children.every((r) => r.status === "waiting")).toBe(true)
    expect(sentTexts(h.db).map((s) => s.text).sort()).toEqual([
      "Tagihan BPJS Rp150000 jatuh tempo Rabu 11 Nov. Udah bayar?",
      "Tagihan Listrik Rp350000 jatuh tempo Kamis 12 Nov. Udah bayar?",
    ])

    // The next day the same bills come back: nothing new is asked.
    await h.run(at("2026-11-12T09:00"))
    const next = runsOf(h.db, flow.id).filter((r) => !r.parentRunId).at(-1)!
    expect((next.vars as { steps: Record<string, unknown> }).steps.per_bill).toEqual({ count: 1, started: 0, deduped: 1, dropped: 0 })

    // Paying one runs its own branch and sets a flow variable.
    const pln = children.find((r) => r.runKey.includes("pln"))!
    answerFlowCheckin(h.db, { chatId: h.chatId, runId: pln.id, answer: "lunas" }, at("2026-11-12T09:30"))
    await h.run(at("2026-11-12T09:30"))
    expect(h.db.select().from(flowRuns).where(eq(flowRuns.id, pln.id)).get()!.status).toBe("done")
    expect(stepLog(h.db, pln.id).map((s) => `${s.step}:${s.branch}`)).toEqual(["ask:on.paid", "mark:null"])
    expect(getFlow(h.db, flow.id)!.state.values).toEqual({ last_paid: "Listrik 2026-11-12" })
  })

  it("drops items over max and says so", async () => {
    const many = Array.from({ length: 5 }, (_, i) => ({ id: `b${i}`, name: `B${i}`, amount: 1, due: "2026-11-12" }))
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-keuangan__list_bills": bills(many) }) })
    const { flow } = h.create({ ...BILLS, steps: BILLS.steps.map((s) => (s.id === "per_bill" ? { ...s, max: 2 } : s)) })
    await h.run(at("2026-11-11T09:00"))
    const parent = runsOf(h.db, flow.id).find((r) => !r.parentRunId)!
    expect((parent.vars as { steps: Record<string, unknown> }).steps.per_bill).toEqual({ count: 5, started: 2, deduped: 0, dropped: 3 })
    expect(stepLog(h.db, parent.id).find((s) => s.step === "per_bill")!.error).toBe("3 item(s) over max 2 were dropped.")
  })

  it("dry-runs the first 3 items inline with stubbed check-ins", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-keuangan__list_bills": bills(LIST) }) })
    const v = validateDefinition(BILLS, { resolveTool: resolverFrom(EXAMPLE_TOOLS) })
    const res = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, stubs: { ask: { answer: "paid" } }, tz: "Asia/Makassar", now: at("2026-11-11T09:00") })
    expect(res.steps.map((s) => `${s.step}:${s.status}`)).toEqual(["bills:ok", "per_bill:ok"])
    expect(res.children.map((c) => c.steps.map((s) => `${s.step}:${s.status}:${s.branch}`))).toEqual([
      ["ask:stubbed:on.paid", "mark:ok:null"],
      ["ask:stubbed:on.paid", "mark:ok:null"],
    ])
    expect(sentTexts(h.db)).toEqual([])
  })
})

describe("poll triggers", () => {
  const POLL = {
    name: "CI",
    triggers: [{ id: "ci", kind: "poll", tool: "mcp-github__get_commit_status", args: { ref: "main" }, everyMin: 5, select: "state", start: "say" }],
    steps: [{ id: "say", kind: "message", text: "CI: {{trigger.previous}} -> {{trigger.value}}" }],
  }

  it("seeds on the first poll, fires on a change, and stays quiet otherwise", async () => {
    let state = "pending"
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-github__get_commit_status": () => ({ state }) }) })
    const { flow } = h.create(POLL)
    const t0 = h.now()
    await h.run(t0)
    expect(runsOf(h.db, flow.id)).toEqual([])
    expect(triggersOf(h.db, flow.id)[0]).toMatchObject({ nextFireAt: t0 + 5 * MIN, pollState: { seeded: true, lastValuePreview: '"pending"' } })
    await h.run(t0 + 5 * MIN)
    expect(runsOf(h.db, flow.id)).toEqual([])
    state = "success"
    await h.run(t0 + 10 * MIN)
    const [run] = runsOf(h.db, flow.id)
    expect(run).toMatchObject({ triggerKind: "poll", status: "done", taint: ["trigger"], trigger: { value: "success", previous: "pending" } })
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(["CI: pending -> success"])
    // Back and forth fires again (the key includes the previous value).
    state = "pending"
    await h.run(t0 + 15 * MIN)
    state = "success"
    await h.run(t0 + 20 * MIN)
    expect(runsOf(h.db, flow.id)).toHaveLength(3)
  })

  it("backs off on failures and tells the owner after three", async () => {
    const h = flowHarness({
      connectors: exampleConnectors({
        "mcp-github__get_commit_status": () => {
          throw new Error("503")
        },
      }),
    })
    const { flow } = h.create(POLL)
    let t = h.now()
    for (let i = 0; i < 3; i++) {
      await h.run(t)
      t = triggersOf(h.db, flow.id)[0]!.nextFireAt!
    }
    expect(triggersOf(h.db, flow.id)[0]!.pollState).toMatchObject({ failures: 3, lastError: "503" })
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(['⚠️ Flow "CI" gagal ngecek mcp-github__get_commit_status 3 kali berturut-turut: 503. Aku coba lagi otomatis.'])
  })
})
