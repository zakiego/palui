import { describe, expect, it } from "vitest"
import { memoryDb, outbox } from "@palui/db"
import { ensureChat, ensureWebChat } from "@palui/core/chats"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { connectorFlowCaller } from "@palui/core/flows/call"
import { getFlow, listFlows, listVersions } from "@palui/core/flows/service"
import { fakeEntries } from "@palui/core/flows/test-entries"
import { exampleConnectors } from "@palui/core/flows/test-harness"
import { BRIEFING } from "@palui/core/flows/test-helpers"
import manifest from "./manifest"
import runtime, { flowTools, offerFlowTools } from "./runtime"
import { fakeCtx } from "../mcp/test-helpers"
import type { RunningEntry } from "@palui/core/connectors/supervisor"

// Tuesday 2026-11-10 10:00 WITA.
const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"

const prayer = () => ({ content: [{ type: "text", text: JSON.stringify({ times: { fajr: "04:39", dhuhr: "12:01", asr: "15:26", maghrib: "18:12", isha: "19:24" } }) }] })

function setup() {
  const db = memoryDb()
  const chat = ensureWebChat(db, NOW)
  const turn: TurnInfo = { runId: "run_1", chatId: chat.id, channel: "web", trigger: "message", tz: WITA, now: NOW }
  const entries = fakeEntries(exampleConnectors({ "mcp-jadwal-sholat__get_prayer_times": prayer, reminders__list_reminders: () => ({ reminders: [] }) }))
  const tools = flowTools(db, turn, { clock: () => NOW, tools: connectorFlowCaller(() => entries) })
  return { db, chat, turn, tools }
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

describe("flows manifest", () => {
  it("is a builtin skill", () => {
    expect(manifest).toMatchObject({ id: "flows", kind: "skill", builtin: true, icon: "workflow" })
  })
})

describe("flow tools", () => {
  it("are classed like the RFC table", () => {
    const { tools } = setup()
    expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.class]))).toEqual({
      create_flow: "write",
      update_flow: "write",
      check_flow: "read",
      test_flow: "read",
      list_flows: "read",
      get_flow: "read",
      pause_flow: "write",
      delete_flow: "destructive",
      run_flow: "write",
      flow_history: "read",
      get_run: "read",
      cancel_run: "write",
      answer_checkin: "write",
      explain_flow: "read",
    })
    expect(tools.test_flow!.untrusted).toBe(true)
  })

  it("are only offered in the owner's own chats and owner turns", () => {
    const { db, chat } = setup()
    const group = ensureChat(db, { channel: "telegram", externalId: "-100", kind: "group", allowed: true }, NOW)
    const base: TurnInfo = { runId: "r", chatId: chat.id, channel: "web", trigger: "message", tz: WITA, now: NOW }
    expect(offerFlowTools(db, base)).toBe(true)
    expect(offerFlowTools(db, { ...base, trigger: "schedule" })).toBe(false)
    expect(offerFlowTools(db, { ...base, trigger: "flow" })).toBe(false)
    expect(offerFlowTools(db, { ...base, trigger: "hook" })).toBe(false)
    expect(offerFlowTools(db, { ...base, chatId: group.id, channel: "telegram" })).toBe(false)
  })

  it("checks, dry-runs, then creates a flow and shows its next times", async () => {
    const { db, tools } = setup()
    const checked = await call(tools.check_flow, { definition: BRIEFING })
    expect(checked).toMatchObject({ ok: true, problems: [], needsApproval: false })
    expect(checked.tools.weather).toEqual({ tool: "webfetch__fetch_page", class: "read", untrusted: true, flowApproval: "version" })

    const dry = await call(tools.test_flow, { definition: BRIEFING })
    expect(dry.ok).toBe(true)
    expect(dry.steps.map((s: { step: string; status: string }) => `${s.step}:${s.status}`)).toEqual(["prayer:ok", "weather:ok", "reminders:ok", "today:ok", "send:stubbed"])

    const made = await call(tools.create_flow, { definition: BRIEFING })
    expect(made).toMatchObject({ ok: true, version: 1, needsApproval: false, message: 'Flow "Briefing pagi" dibuat.' })
    expect(made.next[0]).toBe("Besok, Rabu 11 Nov, 06.00 WITA (main: Briefing)")
    expect(getFlow(db, made.id)!.name).toBe("Briefing pagi")

    const dup = await call(tools.create_flow, { definition: BRIEFING })
    expect(dup).toMatchObject({ ok: false })
    expect(dup.error).toMatch(/already exists/)
  })

  it("finds flows__recap for a draft's tool step", async () => {
    const ctx = fakeCtx({}, {}, { id: "flows" })
    const chat = ensureWebChat(ctx.db, NOW)
    const turn: TurnInfo = { runId: "run_1", chatId: chat.id, channel: "web", trigger: "message", tz: WITA, now: NOW }
    const running = await runtime.start(ctx as never)
    const entries: RunningEntry[] = [{ id: "flows", row: { id: "flows", type: "flows", kind: "skill", label: "flows", trust: "own" } as RunningEntry["row"], handle: running }]
    const tools = flowTools(ctx.db, turn, { clock: () => NOW, tools: connectorFlowCaller(() => entries) })
    const recap = {
      name: "Rekap",
      triggers: [{ id: "main", kind: "schedule", start: "recap", source: { kind: "times", times: [{ time: "20:00" }] } }],
      steps: [
        { id: "recap", kind: "tool", tool: "flows__recap", args: { flow: "self", days: 7 } },
        { id: "send", kind: "message", text: "{{steps.recap.data.text}}" },
      ],
    }
    expect(await call(tools.check_flow, { definition: recap })).toMatchObject({ ok: true, problems: [], tools: { recap: { tool: "flows__recap", class: "read" } } })
    await running.stop()
  })

  it("points {{n}} in a re-ask to run.followUps", async () => {
    const { tools } = setup()
    const ask = { id: "ask", kind: "checkin", text: "Udah?", deadline: { kind: "after", minutes: 60 }, followUp: { everyMin: 20, max: 2, text: "Pengingat ke-{{n}}" } }
    const bad = await call(tools.check_flow, { definition: { name: "Air", triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "11:00" }] } }], steps: [ask] } })
    expect(bad.problems[0].message).toContain("run.followUps")
    const good = await call(tools.check_flow, { definition: { name: "Air", triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "11:00" }] } }], steps: [{ ...ask, followUp: { ...ask.followUp, text: "Pengingat ke-{{run.followUps}}" } }] } })
    expect(good).toMatchObject({ ok: true, problems: [] })
  })

  it("refuses a destructive tool with the problem and its path", async () => {
    const { tools } = setup()
    const r = await call(tools.create_flow, { definition: { name: "Hapus", steps: [{ id: "x", kind: "tool", tool: "reminders__cancel_reminder" }] } })
    expect(r.ok).toBe(false)
    expect(r.problems[0]).toEqual({ path: "steps[0].tool", message: expect.stringMatching(/destructive. Flows never run destructive tools/) })
  })

  it("patches one step by id into a new version, and a no-op edit changes nothing", async () => {
    const { db, tools } = setup()
    const made = await call(tools.create_flow, { definition: BRIEFING })
    const r = await call(tools.update_flow, { id: "briefing pagi", patch: { steps: { send: { text: "Pagi! {{now.date}}" } } }, note: "shorter" })
    expect(r).toMatchObject({ ok: true, version: 2, diff: ["~ step send"] })
    expect(listVersions(db, made.id).map((v) => [v.n, v.note])).toEqual([
      [2, "shorter"],
      [1, null],
    ])
    expect(await call(tools.update_flow, { id: made.id, patch: { steps: { send: { text: "Pagi! {{now.date}}" } } } })).toMatchObject({ ok: true, message: "Nggak ada yang berubah." })
  })

  it("asks for approval when a write tool is added", async () => {
    const { db, tools } = setup()
    const r = await call(tools.create_flow, { definition: { name: "Catat", steps: [{ id: "n", kind: "tool", tool: "notes__save_note", args: { text: "{{now.date}}" } }] } })
    expect(r).toMatchObject({ ok: true, needsApproval: true, writeCalls: ["notes.save_note(text: {{now.date}})"] })
    expect(db.select().from(outbox).all()[0]!.payload.actions!.map((a) => a.id)[0]).toMatch(/^flowver:approve:flv_/)
  })

  it("runs, lists, shows, pauses and deletes", async () => {
    const { db, tools } = setup()
    const made = await call(tools.create_flow, { definition: BRIEFING })
    const ran = await call(tools.run_flow, { id: made.id, args: { x: 1 } })
    expect(ran).toMatchObject({ ok: true, runId: expect.stringMatching(/^flr_/) })
    expect((await call(tools.list_flows, {})).flows[0]).toMatchObject({ name: "Briefing pagi", active: true, approval: "not_needed", lastRun: { status: "queued" } })
    expect((await call(tools.get_flow, { id: made.id })).flow).toMatchObject({ shownVersion: 1, versions: [{ n: 1, current: true }] })
    const hist = await call(tools.flow_history, {})
    expect(hist.summary, JSON.stringify(hist)).toMatchObject({ total: 1, byStatus: { queued: 1 } })
    expect((await call(tools.get_run, { runId: ran.runId })).run).toMatchObject({ id: ran.runId, status: "queued", triggerPayload: { kind: "manual", args: { x: 1 } } })
    expect(await call(tools.cancel_run, { runId: ran.runId })).toMatchObject({ ok: true })
    expect(await call(tools.pause_flow, { id: made.id, paused: true, until: "2026-11-12T08:00" })).toMatchObject({ ok: true, message: 'Flow "Briefing pagi" dijeda sampai Kamis 12 Nov, 08.00 WITA.' })
    expect(await call(tools.run_flow, { id: made.id })).toMatchObject({ ok: false, error: 'Flow "Briefing pagi" lagi dijeda.' })
    expect(await call(tools.delete_flow, { id: made.id })).toMatchObject({ ok: true })
    expect(listFlows(db)).toEqual([])
  })
})

describe("check-ins, recap and diagnosis tools", () => {
  const CHECKIN = {
    name: "Air",
    triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "11:00", label: "siang" }] } }],
    steps: [{ id: "ask", kind: "checkin", text: "Udah minum air {{trigger.label}}?", deadline: { kind: "after", minutes: 60 }, followUp: { everyMin: 20, max: 1 } }],
  }

  async function withRun() {
    const s = setup()
    const made = await call(s.tools.create_flow, { definition: CHECKIN })
    const { createFlowEngine } = await import("@palui/core/flows/engine")
    const { silentLogger } = await import("@palui/core/log")
    const engine = createFlowEngine({ db: s.db, log: silentLogger, now: () => at11 })
    engine.sweep(at11)
    await engine.idle()
    return { ...s, made, engine }
  }
  const at11 = Date.parse("2026-11-10T03:00:00Z")

  it("answer_checkin answers the newest open flow check-in", async () => {
    const { tools, made } = await withRun()
    const r = await call(tools.answer_checkin, { answer: "udah" })
    expect(r).toMatchObject({ ok: true, flow: "Air", closed: true, message: "Sip, dicatat: Sudah (siang)." })
    const hist = await call(tools.flow_history, { id: made.id })
    expect(hist.summary.total).toBe(1)
  })

  it("flows__recap returns counts and the recap text", async () => {
    const { db, turn, made, tools } = await withRun()
    await call(tools.answer_checkin, { answer: "done" })
    const { recapTool } = await import("./runtime")
    const r = await call(recapTool(db, turn, () => at11 + 60_000), { flow: made.id, days: 7 })
    expect(r).toMatchObject({ ok: true, flow: "Air", total: 1, byAnswer: { done: 1 }, missed: 0 })
    expect(r.text).toBe("📊 Rekap Air, 7 hari terakhir:\n- Sudah: 1\n- Terlewat: 0\nSelesai 1/1.")
    expect(await call(recapTool(db, turn), {})).toMatchObject({ ok: false })
    // Inside a flow, "self" is the calling flow.
    expect(await call(recapTool(db, { ...turn, trigger: "flow", flowId: made.id }), { flow: "self" })).toMatchObject({ ok: true, flow: "Air" })
  })

  it("explain_flow explains a run and a quiet moment", async () => {
    const { tools, made } = await withRun()
    const r = await call(tools.explain_flow, { id: made.id, at: "2026-11-10T11:00" })
    expect(r.verdict).toMatch(/^It ran at Hari ini, 11\.00 WITA and is waiting \(checkin\) at step ask until Hari ini, 11\.20 WITA\.$/)
    const quiet = await call(tools.explain_flow, { id: made.id, at: "2026-11-10T03:00" })
    expect(quiet.facts.join(" ")).toMatch(/Trigger main occurrences around then: .*11\.00 WITA \(siang\)/)
    await call(tools.pause_flow, { id: made.id, paused: true })
    expect((await call(tools.explain_flow, { id: made.id })).verdict).toBe('Flow "Air" is paused.')
  })
})
