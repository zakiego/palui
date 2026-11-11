import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { flowRuns, outbox } from "@palui/db"
import { dryRunFlow } from "./dryrun"
import { at, exampleConnectors, flowHarness } from "./test-harness"
import { BRIEFING, EXAMPLE_TOOLS, PR_MERGED, RECAP, SHOLAT } from "./test-helpers"
import { resolverFrom, validateDefinition } from "./validate"

const prayer = () => ({ content: [{ type: "text", text: JSON.stringify({ times: { fajr: "04:39", dhuhr: "12:01", asr: "15:26", maghrib: "18:12", isha: "19:24" } }) }] })

function draft(def: unknown) {
  const r = validateDefinition(def, { resolveTool: resolverFrom(EXAMPLE_TOOLS), hookExists: () => true })
  expect(r.problems).toEqual([])
  return r
}

describe("dry runs (RFC 7.6)", () => {
  it("9a: calls read tools for real, stubs the message, writes nothing else", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-jadwal-sholat__get_prayer_times": prayer, reminders__list_reminders: () => ({ reminders: [] }) }) })
    const v = draft(BRIEFING)
    const res = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, writeCalls: v.writeCalls, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(res.status).toBe("done")
    expect(res.steps.map((s) => `${s.step}:${s.status}`)).toEqual(["prayer:ok", "weather:ok", "reminders:ok", "today:ok", "send:stubbed"])
    expect((res.steps.at(-1)!.output as { text: string }).text).toContain("Subuh 04:39")
    expect(h.calls.map((c) => c.tool)).toEqual(["get_prayer_times", "fetch_page", "list_reminders"])
    expect(h.db.select().from(outbox).all()).toEqual([])
    const rows = h.db.select().from(flowRuns).all()
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ dryRun: true, flowId: `draft_${h.chatId}` })
    // The scheduler never picks a dry run up.
    await h.run(at("2026-11-10T10:05"))
    expect(h.db.select().from(flowRuns).where(eq(flowRuns.dryRun, false)).all()).toEqual([])
  })

  it("9d: stubs the prompt with an override and walks the chosen branch", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: () => ({ total: 35, missed: 3, byAnswer: {}, text: "rekap" }) }) })
    const v = draft(RECAP)
    const res = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, stubs: { cheer: "Tetap semangat!" }, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(res.steps.map((s) => `${s.step}:${s.status}`)).toEqual(["recap:ok", "any_missed:ok", "cheer:stubbed", "send_cheer:stubbed"])
    expect(res.steps[1]!.input).toMatchObject({ explain: 'steps.recap.data.missed = 3' })
    expect((res.steps.at(-1)!.output as { text: string }).text).toBe("Tetap semangat!")
    expect(h.prompts).toEqual([])
  })

  it("a saved flow dry-runs its current version with a sample schedule payload", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: () => ({ total: 0, missed: 0, byAnswer: {}, text: "" }) }) })
    const { flow } = h.create(RECAP)
    const res = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, flow, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(res.status).toBe("done")
    expect(res.steps.map((s) => s.step)).toEqual(["recap", "any_missed", "clean"])
  })

  it("stubs write tools with the rendered args", async () => {
    const h = flowHarness()
    const v = draft({ name: "Catat", steps: [{ id: "note", kind: "tool", tool: "notes__save_note", args: { text: "Tanggal {{now.date}}" } }] })
    const res = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, writeCalls: v.writeCalls, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(res.steps[0]).toMatchObject({ status: "stubbed", input: { args: { text: "Tanggal 2026-11-10" }, class: "write" } })
    expect(h.calls).toEqual([])
  })

  it("9b: a fake webhook payload walks the merge branch; the wait is skipped and the read is real", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-github__get_commit_status": () => ({ state: "failure" }) }) })
    const v = draft(PR_MERGED)
    const payload = { payload: { action: "closed", pull_request: { merged: true, number: 7, title: "x", merge_commit_sha: "s1", html_url: "https://github.com/zakiego/palui/pull/7" } } }
    const res = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, trigger: payload, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(res.steps.map((s) => `${s.step}:${s.status}`)).toEqual(["is_merge:ok", "settle:skipped", "ci:ok", "green:ok", "not_ok:stubbed"])
    expect((res.steps.at(-1)!.output as { text: string }).text).toBe("⚠️ PR #7 merged tapi CI failure. Cek: https://github.com/zakiego/palui/pull/7")
    // Not a merge: nothing past the condition.
    const closed = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, trigger: { payload: { action: "opened" } }, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(closed.steps.map((s) => s.step)).toEqual(["is_merge"])
  })

  it("9e: the check-in is stubbed with the chosen answer, and the recap trigger can be tried alone", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ flows__recap: () => ({ total: 0, text: "" }) }) })
    const v = draft(SHOLAT)
    const ask = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, trigger: { label: "Subuh", time: "04.39" }, stubs: { ask: "not_yet" }, tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(ask.steps[0]).toMatchObject({ step: "ask", status: "stubbed", output: { answer: "not_yet", asked: "Udah sholat Subuh (04.39)?" } })
    const recap = await dryRunFlow(h.db, h.engine, { chatId: h.chatId, definition: v.definition!, triggerId: "recap", tz: "Asia/Makassar", now: at("2026-11-10T10:00") })
    expect(recap.steps.map((s) => `${s.step}:${s.branch ?? ""}`)).toEqual(["recap_read:", "recap_any:else"])
  })
})
