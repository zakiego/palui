import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { flowRuns, flowStepRuns, toolCalls } from "@palui/db"
import { MIN } from "../reminders/test-helpers"
import { decideVersion, getFlow, getRun, triggersOf } from "./service"
import { at, exampleConnectors, flowHarness, NOW, runsOf, sentTexts, stepLog } from "./test-harness"
import { BRIEFING, RECAP } from "./test-helpers"

const prayer = () => ({ content: [{ type: "text", text: JSON.stringify({ times: { fajr: "04:39", dhuhr: "12:01", asr: "15:26", maghrib: "18:12", isha: "19:24" } }) }] })
const weather = () => ({ text: JSON.stringify({ current_condition: [{ temp_C: "27", weatherDesc: [{ value: "Hujan ringan" }] }] }) })
const reminders = () => ({ reminders: [{ text: "Bayar listrik", date: "2026-11-11" }, { text: "Beli obat", date: "2026-11-12" }] })

function briefingHarness(over: Parameters<typeof exampleConnectors>[0] = {}) {
  return flowHarness({
    connectors: exampleConnectors({ "mcp-jadwal-sholat__get_prayer_times": prayer, webfetch__fetch_page: weather, reminders__list_reminders: reminders, ...over }),
  })
}

describe("schedule trigger + read tools + message (RFC 9a, end to end)", () => {
  it("runs the morning briefing once per occurrence with the flow's chat in the tool context", async () => {
    const h = briefingHarness()
    const { flow } = h.create(BRIEFING)
    const [trig] = triggersOf(h.db, flow.id)
    expect(trig!.nextFireAt).toBe(at("2026-11-11T06:00"))

    await h.run(at("2026-11-11T06:00"))
    const [run] = runsOf(h.db, flow.id)
    expect(run).toMatchObject({ status: "done", runKey: "2026-11-11:06:00", triggerKind: "schedule" })
    expect(stepLog(h.db, run!.id).map((s) => `${s.step}:${s.status}`)).toEqual(["prayer:ok", "weather:ok", "reminders:ok", "today:ok", "send:ok"])
    expect(sentTexts(h.db)).toEqual([
      {
        source: `flow:${run!.id}`,
        text: "Selamat pagi! Rabu 11 Nov\n\nSholat: Subuh 04:39, Zuhur 12:01, Asar 15:26, Magrib 18:12, Isya 19:24\nCuaca: 27°C, Hujan ringan\n\nPengingat hari ini (1):\n- Bayar listrik\n- Beli obat",
        actions: [],
      },
    ])
    // Web fetch and the external MCP server taint their steps; reminders don't.
    expect(run!.taint).toEqual(["steps.prayer", "steps.weather"])
    expect(h.calls.every((c) => c.turn.chatId === h.chatId && c.turn.flowId === flow.id && c.turn.trigger === "flow")).toBe(true)
    // Tool steps leave tool_calls rows like turns do.
    expect(h.db.select().from(toolCalls).all().map((t) => t.tool)).toEqual(["mcp-jadwal-sholat__get_prayer_times", "webfetch__fetch_page", "reminders__list_reminders"])

    // The same sweep again, and a restart in between: still one run, one message.
    h.engine.recover(at("2026-11-11T06:00"))
    await h.run(at("2026-11-11T06:00"))
    expect(runsOf(h.db, flow.id)).toHaveLength(1)
    expect(sentTexts(h.db)).toHaveLength(1)
    expect(triggersOf(h.db, flow.id)[0]!.nextFireAt).toBe(at("2026-11-12T06:00"))
  })

  it("a failing source with onError continue still sends the briefing", async () => {
    const h = briefingHarness({
      "mcp-jadwal-sholat__get_prayer_times": () => {
        throw new Error("ECONNRESET")
      },
    })
    const { flow } = h.create(BRIEFING)
    await h.run(at("2026-11-11T06:00"))
    // Read tools retry twice (30 s, then 2 min) before onError.
    let [run] = runsOf(h.db, flow.id)
    expect(run).toMatchObject({ status: "waiting", wakeAt: at("2026-11-11T06:00") + 30_000 })
    await h.run(at("2026-11-11T06:00") + 30_000)
    await h.run(at("2026-11-11T06:00") + 30_000 + 120_000)
    ;[run] = runsOf(h.db, flow.id)
    expect(run!.status).toBe("done")
    expect(stepLog(h.db, run!.id).filter((s) => s.step === "prayer").map((s) => s.status)).toEqual(["error", "error", "error"])
    expect(stepLog(h.db, run!.id).at(-1)).toMatchObject({ step: "send", status: "ok" })
    expect(sentTexts(h.db)[0]!.text).toContain("Sholat: Subuh -, Zuhur -")
  })

  it("skips an occurrence more than lateGraceMin late after downtime", async () => {
    const h = briefingHarness()
    const { flow } = h.create(BRIEFING)
    await h.run(at("2026-11-11T08:30"))
    expect(runsOf(h.db, flow.id)).toEqual([expect.objectContaining({ status: "skipped", skipReason: "late", runKey: "2026-11-11:06:00" })])
    expect(sentTexts(h.db)).toEqual([])
    expect(triggersOf(h.db, flow.id)[0]!.nextFireAt).toBe(at("2026-11-12T06:00"))
  })

  it("adds the late note when a run is a few minutes late", async () => {
    const h = briefingHarness()
    const { flow } = h.create(BRIEFING)
    await h.run(at("2026-11-11T06:10"))
    expect(runsOf(h.db, flow.id)[0]!.status).toBe("done")
    expect(sentTexts(h.db)[0]!.text).toMatch(/\(telat, harusnya 06\.00\)$/)
  })
})

describe("manual runs, branches and limits", () => {
  const branchy = {
    name: "Cabang",
    steps: [
      { id: "pick", kind: "if", when: 'trigger.args.mode == "a"', then: "a", else: "b" },
      { id: "a", kind: "set", values: { n: "flow.n + 1", label: "{{trigger.args.mode | upper}}" }, scope: "flow" },
      { id: "say_a", kind: "message", text: "A ke-{{flow.n}} ({{flow.label}})", next: null },
      { id: "b", kind: "message", text: "B" },
    ],
  }

  it("runs a manual start through the scheduler, with branches and flow variables", async () => {
    const h = flowHarness()
    const { flow } = h.create(branchy)
    for (const mode of ["a", "a", "b"]) {
      const { run } = h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: `manual:${mode}:${h.now()}`, payload: { args: { mode } }, now: h.now() })
      expect(run!.status).toBe("queued")
      await h.run(h.now())
      h.setNow(h.now() + 1000)
    }
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(["A ke-1 (A)", "A ke-2 (A)", "B"])
    expect(getFlow(h.db, flow.id)!.state).toEqual({ values: { n: 2, label: "A" }, tainted: [] })
    const last = runsOf(h.db, flow.id).at(-1)!
    expect(stepLog(h.db, last.id)).toEqual([
      { step: "pick", status: "ok", branch: "else", error: null },
      { step: "b", status: "ok", branch: null, error: null },
    ])
  })

  it("caps messages per run and fails the run with a sentence", async () => {
    const h = flowHarness()
    const steps = Array.from({ length: 11 }, (_, i) => ({ id: `m${i}`, kind: "message", text: `${i}` }))
    const { flow } = h.create({ name: "Spam", steps })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "manual:1", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(sentTexts(h.db).filter((s) => s.source.startsWith("flow:"))).toHaveLength(10)
    expect(runsOf(h.db, flow.id)[0]).toMatchObject({ status: "failed", error: "Step m10: More than 10 messages in one run." })
  })

  it("skips runs past the per-flow hourly limit", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Sering", limits: { runsPerHour: 2 }, steps: [{ id: "m", kind: "message", text: "x" }] })
    const res = [1, 2, 3].map((i) => h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: `manual:${i}`, payload: {}, now: h.now() }))
    expect(res.map((r) => r.skipped ?? r.run!.status)).toEqual(["queued", "queued", "rate_limited"])
  })

  it("dedupes a run key", () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Sekali", steps: [{ id: "m", kind: "message", text: "x" }] })
    expect(h.engine.startRun({ flowId: flow.id, triggerKind: "webhook", runKey: "d1", payload: {}, now: h.now() }).run).not.toBeNull()
    expect(h.engine.startRun({ flowId: flow.id, triggerKind: "webhook", runKey: "d1", payload: {}, now: h.now() })).toEqual({ run: null, skipped: "duplicate" })
  })

  it("pauses a flow after five failed runs in a row and says so once", async () => {
    // The tool always fails.
    const failing = flowHarness({ connectors: exampleConnectors({ reminders__list_reminders: () => { throw new Error("boom") } }) })
    const f2 = failing.create({ name: "Rusak", steps: [{ id: "t", kind: "tool", tool: "reminders__list_reminders", retry: { max: 0, backoffSec: 5 } }] }).flow
    for (let i = 0; i < 5; i++) {
      failing.engine.startRun({ flowId: f2.id, triggerKind: "manual", runKey: `m${i}`, payload: {}, now: failing.now() })
      await failing.run(failing.now())
      failing.setNow(failing.now() + MIN)
    }
    expect(getFlow(failing.db, f2.id)).toMatchObject({ enabled: false, consecutiveFailures: 5 })
    const notices = sentTexts(failing.db).map((s) => s.text)
    expect(notices[0]).toMatch(/^⚠️ Flow "Rusak" gagal: Step t: boom/)
    expect(notices.at(-1)).toMatch(/^⏸️ Flow "Rusak" aku jeda: 5 kali gagal/)
    expect(notices).toHaveLength(2)
  })
})

describe("write steps and the version approval", () => {
  it("stops at a write step until the version is approved, then continues", async () => {
    const h = flowHarness()
    const def = {
      name: "Catat",
      steps: [
        { id: "hi", kind: "message", text: "Mulai" },
        { id: "note", kind: "tool", tool: "notes__save_note", args: { text: "{{now.date}}" } },
        { id: "done", kind: "message", text: "Beres" },
      ],
    }
    const saved = h.create(def)
    expect(saved.needsApproval).toBe(true)
    expect(sentTexts(h.db)[0]!.actions).toEqual([`flowver:approve:${saved.version.id}`, `flowver:deny:${saved.version.id}`])
    h.engine.startRun({ flowId: saved.flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    let [run] = runsOf(h.db, saved.flow.id)
    expect(run).toMatchObject({ status: "awaiting_approval", skipReason: "not_approved", cursor: "note" })
    expect(h.calls.filter((c) => c.tool === "save_note")).toHaveLength(0)

    expect(decideVersion(h.db, saved.version.id, true, "test", h.now()).ok).toBe(true)
    await h.run(h.now())
    ;[run] = runsOf(h.db, saved.flow.id)
    expect(run!.status).toBe("done")
    expect(h.calls.filter((c) => c.tool === "save_note")).toHaveLength(1)
    expect(sentTexts(h.db).slice(1).map((s) => s.text)).toEqual(["Mulai", "Beres"])
  })
})

describe("restart recovery", () => {
  it("runs a read tool again when a crash cut it off mid-call", async () => {
    const h = briefingHarness()
    const { flow } = h.create(BRIEFING)
    const { run } = h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    // A crash right after the step row went `running`.
    h.db.update(flowRuns).set({ status: "running", leaseUntil: h.now() + 60_000 }).where(eq(flowRuns.id, run!.id)).run()
    h.db.insert(flowStepRuns).values({ id: "fls_x", runId: run!.id, seq: 1, stepId: "prayer", kind: "tool", status: "running", startedAt: h.now() }).run()
    h.engine.recover(h.now())
    expect(getRun(h.db, run!.id)!.status).toBe("queued")
    await h.run(h.now())
    expect(getRun(h.db, run!.id)!.status).toBe("done")
    expect(stepLog(h.db, run!.id).slice(0, 2)).toEqual([
      { step: "prayer", status: "error", branch: null, error: "interrupted by a restart; running it again" },
      { step: "prayer", status: "ok", branch: null, error: null },
    ])
  })

  it("never repeats a write tool cut off mid-call; it tells the owner and takes onError", async () => {
    const h = flowHarness()
    const saved = h.create({ name: "Tulis", steps: [{ id: "note", kind: "tool", tool: "notes__save_note", args: { text: "x" } }] })
    decideVersion(h.db, saved.version.id, true, "test", h.now())
    const { run } = h.engine.startRun({ flowId: saved.flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    h.db.update(flowRuns).set({ status: "running" }).where(eq(flowRuns.id, run!.id)).run()
    h.db.insert(flowStepRuns).values({ id: "fls_y", runId: run!.id, seq: 1, stepId: "note", kind: "tool", status: "running", startedAt: h.now() }).run()
    h.engine.recover(h.now())
    await h.run(h.now())
    expect(h.calls.filter((c) => c.tool === "save_note")).toHaveLength(0)
    expect(getRun(h.db, run!.id)).toMatchObject({ status: "failed", error: "Step note: unknown outcome after restart" })
    expect(sentTexts(h.db).some((s) => s.text.includes("kepotong restart"))).toBe(true)
  })

  it("takes a finished prompt turn's output instead of running it again", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: () => ({ total: 10, missed: 2, byAnswer: {}, text: "rekap" }) }) })
    const { flow } = h.create(RECAP)
    const { run } = h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now(), start: "cheer" })
    h.db.update(flowRuns).set({ status: "running", vars: { steps: { recap: { ok: true, data: { total: 10 } } }, vars: {} } }).where(eq(flowRuns.id, run!.id)).run()
    h.db.insert(flowStepRuns).values({ id: "fls_z", runId: run!.id, seq: 1, stepId: "cheer", kind: "prompt", status: "running", turnRunId: "run_done", startedAt: h.now() }).run()
    const { runs } = await import("@palui/db")
    h.db.insert(runs).values({ id: "run_done", chatId: h.chatId, trigger: "flow", status: "ok", resultPreview: "Mantap minggu ini!", startedAt: h.now() }).run()
    h.engine.recover(h.now())
    await h.run(h.now())
    expect(h.prompts).toHaveLength(0)
    expect(sentTexts(h.db).at(-1)!.text).toBe("Mantap minggu ini!")
  })
})

describe("prompt steps (RFC 9d)", () => {
  const recapTool = (missed: number) => () => ({ total: 35, missed, byAnswer: { done: 35 - missed }, text: `📊 Rekap: terlewat ${missed}` })

  it("puts data in blocks, never in the instruction, and sends the model's text", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: recapTool(2) }) })
    const { flow } = h.create(RECAP)
    await h.run(at("2026-11-15T20:00"))
    const [run] = runsOf(h.db, flow.id)
    expect(run!.status).toBe("done")
    expect(h.prompts).toHaveLength(1)
    const p = h.prompts[0]!
    expect(p).toMatchObject({ chatId: h.chatId, modelRole: "fast", tools: [], send: false, tainted: false })
    expect(p.prompt).toContain('<data name="recap">\n{"total":35,"missed":2')
    expect(p.prompt.indexOf("Tulis pesan pendek")).toBeLessThan(p.prompt.indexOf("<data"))
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(["Semangat!"])
    expect(stepLog(h.db, run!.id).map((s) => s.step)).toEqual(["recap", "any_missed", "cheer", "send_cheer"])
  })

  it("falls back to the plain recap when the model fails", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: recapTool(1) }), prompt: () => ({ status: "error", text: "", runId: "" }) })
    const { flow } = h.create(RECAP)
    await h.run(at("2026-11-15T20:00"))
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(["📊 Rekap: terlewat 1"])
    expect(stepLog(h.db, runsOf(h.db, flow.id)[0]!.id).find((s) => s.step === "cheer")).toMatchObject({ status: "error", branch: "onError:plain" })
  })

  it("takes the clean branch with no model call when nothing was missed", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: recapTool(0) }) })
    h.create(RECAP)
    await h.run(at("2026-11-15T20:00"))
    expect(h.prompts).toHaveLength(0)
    expect(sentTexts(h.db)[0]!.text).toBe("Minggu ini 35 sholat, semuanya kecatat sudah. Mantap, pertahankan! 🤲")
  })

  it("respects the flow's daily token cap", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: recapTool(2) }) })
    const { flow } = h.create({ ...RECAP, limits: { dailyTokens: 1000 } })
    const { addUsage, dayKey } = await import("../budget")
    addUsage(h.db, `flow:${flow.id}:${dayKey(at("2026-11-15T20:00"), "Asia/Makassar")}`, 1000)
    await h.run(at("2026-11-15T20:00"))
    expect(runsOf(h.db, flow.id)[0]).toMatchObject({ status: "skipped", skipReason: "budget" })
    expect(h.prompts).toHaveLength(0)
  })

  it("counts a prompt step's cache reads at a tenth against the flow's daily cap", async () => {
    const { runs } = await import("@palui/db")
    const h = flowHarness({
      connectors: exampleConnectors({ flows__recap: recapTool(2) }),
      // 20,000 tokens in, 18,000 of them read from the cache, 100 out.
      prompt: (r) => {
        h.db.insert(runs).values({ id: r.turnRunId, chatId: h.chatId, trigger: "flow", status: "ok", tokensIn: 20_000, tokensOut: 100, tokensCached: 18_000, startedAt: h.now() }).run()
        return { status: "ok", text: "Semangat!", runId: r.turnRunId }
      },
    })
    const { flow } = h.create(RECAP)
    const { getUsage, dayKey } = await import("../budget")
    await h.run(at("2026-11-15T20:00"))
    expect(h.prompts).toHaveLength(1)
    expect(getUsage(h.db, `flow:${flow.id}:${dayKey(at("2026-11-15T20:00"), "Asia/Makassar")}`)).toBe(2_000 + 1_800 + 100)
  })
})

it("NOW is a Tuesday morning", () => {
  expect(NOW).toBe(at("2026-11-10T10:00"))
})

describe("retention", () => {
  it("clears step inputs after 30 days and drops old runs and dry runs", async () => {
    const { pruneFlows } = await import("./history")
    const h = flowHarness()
    const { flow } = h.create({ name: "Lama", steps: [{ id: "m", kind: "message", text: "x" }] })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    const day = 24 * 3600_000
    expect(pruneFlows(h.db, h.now() + 31 * day)).toEqual({ io: 1, runs: 0, dry: 0 })
    expect(h.db.select().from(flowStepRuns).get()).toMatchObject({ status: "ok", output: null })
    expect(pruneFlows(h.db, h.now() + 181 * day)).toMatchObject({ runs: 1 })
    expect(runsOf(h.db, flow.id)).toEqual([])
  })
})
