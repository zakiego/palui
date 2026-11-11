import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { outbox, pendingApprovals } from "@palui/db"
import { decide, expireDue } from "../approvals/store"
import { MIN } from "../reminders/test-helpers"
import { fakePrayerTool } from "./test-helpers"
import { flowActions } from "./actions"
import { answerFlowCheckin, flowCheckinsPrompt, openFlowCheckins } from "./checkin"
import { decideVersion, getRun, wakeFlowRun } from "./service"
import { at, exampleConnectors, flowHarness, runsOf, sentTexts, stepLog } from "./test-harness"
import { PR_MERGED, SHOLAT } from "./test-helpers"

const MEDICINE = {
  name: "Minum obat",
  triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "08:00", label: "pagi" }, { time: "20:00", label: "malam" }] } }],
  steps: [
    {
      id: "ask",
      kind: "checkin",
      text: "💊 Obat {{trigger.label}} udah diminum?",
      answers: [
        { id: "taken", label: "Udah minum", words: ["udah", "sudah"], closes: true, reply: "Mantap, obat {{trigger.label}} tercatat 👍" },
        { id: "skip", label: "Skip", closes: true },
        { id: "later", label: "Nanti", closes: false },
      ],
      followUp: { everyMin: 15, max: 2, text: "Pengingat ke-{{run.followUps}}: obat {{trigger.label}} jangan lupa ya." },
      deadline: { kind: "after", minutes: 120 },
      missedText: "Obat {{trigger.label}} kelewat, dicatat ya.",
      on: { skip: "why" },
      next: null,
    },
    { id: "why", kind: "message", text: "Oke, skip dulu." },
  ],
}

describe("check-in steps", () => {
  it("asks with fl: buttons, re-asks, then logs a miss at the deadline", async () => {
    const h = flowHarness()
    h.setNow(at("2026-11-11T07:00"))
    const { flow } = h.create(MEDICINE)
    await h.run(at("2026-11-11T08:00"))
    const [run] = runsOf(h.db, flow.id)
    expect(run).toMatchObject({ status: "waiting", wakeAt: at("2026-11-11T08:15") })
    expect(sentTexts(h.db)).toEqual([{ source: `flow:${run!.id}`, text: "💊 Obat pagi udah diminum?", actions: [`fl:${run!.id}:taken`, `fl:${run!.id}:skip`, `fl:${run!.id}:later`] }])

    // A restart while it waits changes nothing.
    h.engine.recover(at("2026-11-11T08:05"))
    await h.run(at("2026-11-11T08:05"))
    expect(sentTexts(h.db)).toHaveLength(1)

    await h.run(at("2026-11-11T08:15"))
    await h.run(at("2026-11-11T08:30"))
    await h.run(at("2026-11-11T08:45"))
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(["💊 Obat pagi udah diminum?", "Pengingat ke-1: obat pagi jangan lupa ya.", "Pengingat ke-2: obat pagi jangan lupa ya."])
    expect(getRun(h.db, run!.id)!.wakeAt).toBe(at("2026-11-11T10:00"))

    await h.run(at("2026-11-11T10:00"))
    const done = getRun(h.db, run!.id)!
    expect(done.status).toBe("done")
    expect((done.vars as { steps: Record<string, unknown> }).steps.ask).toMatchObject({ missed: true, followUps: 2, answer: null })
    expect(sentTexts(h.db).at(-1)!.text).toBe("Obat pagi kelewat, dicatat ya.")
    expect(stepLog(h.db, run!.id)).toEqual([{ step: "ask", status: "ok", branch: "missed", error: null }])
  })

  it("takes a button answer, replies, and continues the run", async () => {
    const h = flowHarness()
    h.setNow(at("2026-11-11T07:00"))
    const { flow } = h.create(MEDICINE)
    await h.run(at("2026-11-11T08:00"))
    const [run] = runsOf(h.db, flow.id)
    const reply = await flowActions.fromCallback(h.db, `fl:${run!.id}:taken`, h.chatId, at("2026-11-11T08:03"))
    expect(reply).toBe("Mantap, obat pagi tercatat 👍")
    await h.run(at("2026-11-11T08:03"))
    expect(getRun(h.db, run!.id)!.status).toBe("done")
    // The answer ends the flow (ask -> why only on skip): no more messages.
    expect(sentTexts(h.db)).toHaveLength(1)
    expect(await flowActions.fromCallback(h.db, `fl:${run!.id}:taken`, h.chatId, at("2026-11-11T08:04"))).toBe("Oke, jawaban diganti: Udah minum.")
  })

  it("branches on a closing answer, and a non-closing one re-asks from now", async () => {
    const h = flowHarness()
    h.setNow(at("2026-11-11T07:00"))
    const { flow } = h.create(MEDICINE)
    await h.run(at("2026-11-11T08:00"))
    const [run] = runsOf(h.db, flow.id)
    const later = answerFlowCheckin(h.db, { chatId: h.chatId, answer: "nanti" }, at("2026-11-11T08:10"))
    expect(later.reply).toBe("Oke, dicatat: Nanti. Aku tanya lagi jam 08.25.")
    expect(getRun(h.db, run!.id)!.wakeAt).toBe(at("2026-11-11T08:25"))
    answerFlowCheckin(h.db, { chatId: h.chatId, runId: run!.id, answer: "skip" }, at("2026-11-11T08:12"))
    await h.run(at("2026-11-11T08:12"))
    expect(getRun(h.db, run!.id)!.status).toBe("done")
    expect(stepLog(h.db, run!.id).map((s) => `${s.step}:${s.branch}`)).toEqual(["ask:on.skip", "why:null"])
    expect(sentTexts(h.db).at(-1)!.text).toBe("Oke, skip dulu.")
  })

  it("matches a quoted reply exactly and leaves anything else to the model", async () => {
    const h = flowHarness()
    h.setNow(at("2026-11-11T07:00"))
    const { flow } = h.create(MEDICINE)
    await h.run(at("2026-11-11T08:00"))
    h.db.update(outbox).set({ channelMsgId: "777" }).run()
    expect(await flowActions.fromText(h.db, "udah tapi telat", "777", h.chatId, at("2026-11-11T08:05"))).toBeNull()
    expect(await flowActions.fromText(h.db, "Udah!", "777", h.chatId, at("2026-11-11T08:05"))).toBe("Mantap, obat pagi tercatat 👍")
    expect(runsOf(h.db, flow.id)[0]!.status).toBe("queued")
  })

  it("lists open check-ins for the system prompt", async () => {
    const h = flowHarness()
    h.setNow(at("2026-11-11T07:00"))
    h.create(MEDICINE)
    await h.run(at("2026-11-11T08:00"))
    const [run] = openFlowCheckins(h.db, h.chatId)
    expect(flowCheckinsPrompt(h.db, h.chatId, at("2026-11-11T08:01"))).toContain(`- ${run!.id}: flow "Minum obat", item "pagi", asked Rabu 11 Nov 08.00; answers: taken, skip, later (keeps asking)`)
  })

  it("9e: deadline next is the next prayer time; the recap trigger reads the log", async () => {
    const prayer = fakePrayerTool()
    const h = flowHarness({ engine: { readSource: prayer }, connectors: exampleConnectors({ flows__recap: () => ({ total: 3, text: "📊 Rekap" }) }) })
    const { flow } = h.create(SHOLAT)
    await h.run(at("2026-11-10T10:00"))
    expect(prayer.calls.map((c) => c.args.date)).toEqual(["2026-11-10", "2026-11-11"])
    await h.run(at("2026-11-10T12:00"))
    const [run] = runsOf(h.db, flow.id)
    expect(run).toMatchObject({ status: "waiting", runKey: "2026-11-10:times.dhuhr" })
    expect(sentTexts(h.db)[0]!.text).toBe("Udah sholat Zuhur (12.00)?")
    const wait = run!.wait as { deadlineAt: number }
    expect(wait.deadlineAt).toBe(at("2026-11-10T15:25"))
  })

  it("deadline next with plusMin stops 10 minutes before the next prayer", async () => {
    const prayer = fakePrayerTool()
    const h = flowHarness({ engine: { readSource: prayer } })
    const def = { ...SHOLAT, steps: SHOLAT.steps.map((s) => (s.id === "ask" ? { ...s, deadline: { kind: "next", plusMin: -10 } } : s)) }
    const { flow } = h.create(def)
    await h.run(at("2026-11-10T10:00"))
    await h.run(at("2026-11-10T12:00"))
    const [run] = runsOf(h.db, flow.id)
    expect((run!.wait as { deadlineAt: number }).deadlineAt).toBe(at("2026-11-10T15:15"))
  })
})

// The owner's rule from rpt_0mwcdzchvhscqv9o16o: the last reminder lands 10
// minutes before the next prayer, whatever the gap between the two prayers is.
describe("computed check-in deadlines", () => {
  const ask = (deadline: Record<string, unknown>) => ({
    id: "ask",
    kind: "checkin",
    text: "Udah sholat?",
    answers: [{ id: "done", label: "Sudah", closes: true }],
    followUp: { everyMin: 30, max: 5 },
    deadline,
    missedText: "Kelewat.",
    onMissed: "last",
    next: null,
  })
  const last = { id: "last", kind: "message", text: "Pengingat terakhir", next: null }

  it("at: a template time with plusMin, and re-asks stop before it", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Sholat", tz: "Asia/Makassar", steps: [ask({ kind: "at", at: "{{trigger.args.next}}", plusMin: -10 }), last] })
    const t0 = at("2026-11-10T18:16")
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "a", payload: { args: { next: "2026-11-10T19:29" } }, now: t0 })
    await h.run(t0)
    const [run] = runsOf(h.db, flow.id)
    expect((run!.wait as { deadlineAt: number }).deadlineAt).toBe(at("2026-11-10T19:19"))
    await h.run(at("2026-11-10T18:46"))
    await h.run(at("2026-11-10T19:16"))
    // The next re-ask (19.46) would be past the deadline, so none is planned.
    expect(sentTexts(h.db).map((x) => x.text)).toEqual(["Udah sholat?", "🔁 Udah sholat?", "🔁 Udah sholat?"])
    expect(runsOf(h.db, flow.id)[0]!.wakeAt).toBe(at("2026-11-10T19:19"))
    await h.run(at("2026-11-10T19:19"))
    expect(sentTexts(h.db).map((x) => x.text).slice(3)).toEqual(["Kelewat.", "Pengingat terakhir"])
  })

  it("at: a time already past skips the ask silently and goes onMissed", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Sholat", tz: "Asia/Makassar", steps: [ask({ kind: "at", at: "{{trigger.args.next}}", plusMin: -10 }), last] })
    const t0 = at("2026-11-10T19:25")
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "a", payload: { args: { next: "2026-11-10T19:29" } }, now: t0 })
    await h.run(t0)
    const [run] = runsOf(h.db, flow.id)
    expect(run!.status).toBe("done")
    expect((run!.vars as { steps: Record<string, unknown> }).steps.ask).toMatchObject({ missed: true, skipped: true })
    expect(sentTexts(h.db).map((x) => x.text)).toEqual(["Pengingat terakhir"])
  })

  it("after: minutes from a template", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Sholat", steps: [ask({ kind: "after", minutes: "{{trigger.args.left}}" }), last] })
    const t0 = h.now()
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "a", payload: { args: { left: 63 } }, now: t0 })
    await h.run(t0)
    expect((runsOf(h.db, flow.id)[0]!.wait as { deadlineAt: number }).deadlineAt).toBe(t0 + 63 * MIN)
  })

  it("a value that isn't a time fails the step through onError", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Sholat", steps: [ask({ kind: "at", at: "{{trigger.args.next}}" }), last] })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "a", payload: { args: { next: "besok" } }, now: h.now() })
    await h.run(h.now())
    const [run] = runsOf(h.db, flow.id)
    expect(run!.status).toBe("failed")
    expect(run!.error).toContain("deadline.at")
    expect(sentTexts(h.db).map((x) => x.text)).toEqual(['⚠️ Flow "Sholat" gagal: Step ask: deadline.at ("besok") isn\'t a date or time.'])
  })
})

describe("wait steps (RFC 9b, entered directly)", () => {
  const payload = { payload: { action: "closed", pull_request: { merged: true, number: 46, title: "feat: flows", merge_commit_sha: "abc123", html_url: "https://github.com/zakiego/palui/pull/46" } } }

  it("sleeps in the row across a restart, then checks CI and reports", async () => {
    const h = flowHarness({ connectors: exampleConnectors({ "mcp-github__get_commit_status": () => ({ state: "success" }) }) })
    const { flow } = h.create(PR_MERGED)
    const t0 = h.now()
    h.engine.startRun({ flowId: flow.id, triggerId: "gh", triggerKind: "webhook", runKey: "d-1", payload, taint: ["trigger"], now: t0 })
    await h.run(t0)
    let [run] = runsOf(h.db, flow.id)
    expect(run).toMatchObject({ status: "waiting", wakeAt: t0 + 5 * MIN })
    h.engine.recover(t0 + MIN)
    await h.run(t0 + MIN)
    expect(runsOf(h.db, flow.id)[0]!.status).toBe("waiting")
    await h.run(t0 + 5 * MIN)
    ;[run] = runsOf(h.db, flow.id)
    expect(run!.status).toBe("done")
    expect(h.calls.find((c) => c.tool === "get_commit_status")!.args).toEqual({ owner: "zakiego", repo: "palui", ref: "abc123" })
    expect(sentTexts(h.db).map((s) => s.text)).toEqual(['✅ PR #46 "feat: flows" merged, CI hijau.'])
    expect(stepLog(h.db, run!.id).map((s) => `${s.step}:${s.status}`)).toEqual(["is_merge:ok", "settle:ok", "ci:ok", "green:ok", "ok:ok"])
  })

  it("an until template wakes at that time; a past one continues at once", async () => {
    const h = flowHarness()
    const { flow } = h.create({ name: "Tunggu", steps: [{ id: "w", kind: "wait", until: "{{trigger.args.at}}", plusMin: 10 }, { id: "m", kind: "message", text: "bangun" }] })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "a", payload: { args: { at: "2026-11-10T12:00" } }, now: h.now() })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "b", payload: { args: { at: "2026-11-01" } }, now: h.now() })
    await h.run(h.now())
    const a = runsOf(h.db, flow.id).find((r) => r.runKey === "a")
    const b = runsOf(h.db, flow.id).find((r) => r.runKey === "b")
    expect(a).toMatchObject({ status: "waiting", wakeAt: at("2026-11-10T12:10") })
    expect(b!.status).toBe("done")
  })
})

describe("write steps: per-run approval (RFC 7.2)", () => {
  const def = {
    name: "Komentar",
    steps: [
      { id: "note", kind: "tool", tool: "notes__save_note", args: { text: "{{trigger.args.text}}" } },
      { id: "done", kind: "message", text: "Tersimpan." },
    ],
  }

  async function approved(h: ReturnType<typeof flowHarness>, d: unknown = def) {
    const saved = h.create(d)
    decideVersion(h.db, saved.version.id, true, "test", h.now())
    return saved.flow
  }

  it("runs approved writes with clean args without asking", async () => {
    const h = flowHarness()
    const flow = await approved(h)
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "m1", payload: { args: { text: "halo" } }, now: h.now() })
    await h.run(h.now())
    expect(h.calls.filter((c) => c.tool === "save_note").map((c) => c.args)).toEqual([{ text: "halo" }])
    expect(h.db.select().from(pendingApprovals).all()).toEqual([])
  })

  it("asks when an argument is tainted, then runs exactly what was approved", async () => {
    const h = flowHarness()
    const flow = await approved(h)
    const { run } = h.engine.startRun({ flowId: flow.id, triggerKind: "webhook", runKey: "w1", payload: { args: { text: "dari web" } }, taint: ["trigger"], now: h.now() })
    await h.run(h.now())
    expect(getRun(h.db, run!.id)!.status).toBe("waiting")
    const [ap] = h.db.select().from(pendingApprovals).all()
    expect(ap).toMatchObject({ runId: run!.id, tool: "notes__save_note", toolClass: "write", argsPreview: '{"text":"dari web"}' })
    expect(sentTexts(h.db).at(-1)).toMatchObject({ source: `approval:${ap!.id}`, actions: [`approve:${ap!.id}`, `deny:${ap!.id}`] })
    expect(sentTexts(h.db).at(-1)!.text).toContain("argumen dari data luar (text)")
    expect(h.calls).toEqual([])

    decide(h.db, ap!.id, true, h.now())
    wakeFlowRun(h.db, run!.id, h.now())
    await h.run(h.now())
    expect(getRun(h.db, run!.id)!.status).toBe("done")
    expect(h.calls.map((c) => c.args)).toEqual([{ text: "dari web" }])
    expect(stepLog(h.db, run!.id).map((s) => `${s.step}:${s.status}`)).toEqual(["note:ok", "done:ok"])
  })

  it("an expired or denied ask fails the step and takes onError", async () => {
    const h = flowHarness()
    const flow = await approved(h, { ...def, steps: [{ ...def.steps[0], onError: "continue" }, def.steps[1]] })
    const { run } = h.engine.startRun({ flowId: flow.id, triggerKind: "webhook", runKey: "w1", payload: { args: { text: "x" } }, taint: ["trigger"], now: h.now() })
    await h.run(h.now())
    const later = h.now() + 11 * MIN
    for (const id of expireDue(h.db, later)) wakeFlowRun(h.db, id, later)
    await h.run(later)
    expect(getRun(h.db, run!.id)!.status).toBe("done")
    expect(stepLog(h.db, run!.id)[0]).toMatchObject({ step: "note", status: "error", branch: "continue", error: "no answer from the owner within 10 minutes" })
    expect(h.calls).toEqual([])
  })

  it("asks every run for a tool set to every_run", async () => {
    const h = flowHarness()
    const flow = await approved(h, { name: "Komen", steps: [{ id: "c", kind: "tool", tool: "mcp-github__add_comment", args: { body: "ok" } }] })
    h.engine.startRun({ flowId: flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h.now() })
    await h.run(h.now())
    expect(h.db.select().from(pendingApprovals).all()[0]!.tool).toBe("mcp-github__add_comment")
    expect(sentTexts(h.db).at(-1)!.text).toContain("tool ini selalu minta izin")
  })

  it("asks when a tool approved as read became write since", async () => {
    // Saved while the tool was read: no write calls, no version approval.
    const h = flowHarness({ connectors: exampleConnectors() })
    const saved = h.create({ name: "Daftar", steps: [{ id: "r", kind: "tool", tool: "reminders__list_reminders" }] })
    expect(saved.needsApproval).toBe(false)
    // The owner reclassified it on the Connectors page.
    const h2 = flowHarness({ connectors: exampleConnectors().map((c) => (c.id === "reminders" ? { ...c, tools: { ...c.tools, list_reminders: { ...c.tools.list_reminders!, class: "write" as const } } } : c)) })
    const saved2 = h2.create({ name: "Daftar", steps: [{ id: "r", kind: "tool", tool: "reminders__list_reminders" }] })
    h2.engine.startRun({ flowId: saved2.flow.id, triggerKind: "manual", runKey: "m1", payload: {}, now: h2.now() })
    await h2.run(h2.now())
    expect(sentTexts(h2.db).at(-1)!.text).toContain("kelas tool berubah sejak disetujui")
    expect(h2.db.select().from(pendingApprovals).where(eq(pendingApprovals.tool, "reminders__list_reminders")).all()).toHaveLength(1)
  })
})

// The owner's full rule from rpt_0mwcj91x26l822n6pa5: ask when the prayer
// starts, remind at +10, +30, +70 and +150 minutes, skip a reminder within 10
// minutes of the last one, the last one always 10 minutes before the next
// prayer (Subuh tomorrow for Isya), and close 1 minute before the next prayer.
const SHOLAT_GROWING = (() => {
  const answers = [
    { id: "done", label: "Sudah", words: ["udah", "sudah"], closes: true, reply: "Alhamdulillah 🤲" },
    { id: "not_yet", label: "Belum", words: ["belum"], closes: false },
  ]
  const ask = (id: string, text: string, until: string, onMissed: string) => ({ id, kind: "checkin", text, answers, followUp: { everyMin: 5, max: 0 }, deadline: { kind: "at", at: `{{${until}}}` }, onMissed, next: null })
  // A reminder planned within 10 minutes of the last one moves onto it, so its ask is skipped.
  const at = (min: number) => `choose(trigger.at + ${min} * 60000 <= vars.last - 10 * 60000, trigger.at + ${min} * 60000, vars.last)`
  return {
    name: "Sholat 5 waktu",
    tz: "Asia/Makassar",
    triggers: [{ id: "main", kind: "schedule", start: "plan", lateGraceMin: 60, source: SHOLAT.triggers[0]!.source }],
    steps: [
      { id: "plan", kind: "set", values: { last: "trigger.nextAt - 10 * 60000" } },
      { id: "plan_times", kind: "set", values: { r1: at(10), r2: at(30), r3: at(70), r4: at(150) } },
      ask("ask_0", "🕌 Waktu {{trigger.label}} masuk ({{trigger.time}}). Udah sholat?", "vars.r1", "ask_1"),
      ask("ask_1", "Udah sholat {{trigger.label}}?", "vars.r2", "ask_2"),
      ask("ask_2", "Udah sholat {{trigger.label}}?", "vars.r3", "ask_3"),
      ask("ask_3", "Udah sholat {{trigger.label}}?", "vars.r4", "ask_4"),
      ask("ask_4", "Udah sholat {{trigger.label}}?", "vars.last", "final"),
      {
        id: "final",
        kind: "checkin",
        text: "⏰ Terakhir: udah sholat {{trigger.label}}? 10 menit lagi masuk waktu berikutnya",
        answers,
        followUp: { everyMin: 5, max: 0 },
        deadline: { kind: "next", plusMin: -1 },
        missedText: "{{trigger.label}} kelewat, dicatat ya",
        next: null,
      },
    ],
  }
})()

describe("growing reminders up to 10 minutes before the next prayer", () => {
  // Fake times on 2026-11-10: Subuh 04.34, Zuhur 12.00, Asar 15.25, Magrib 18.11, Isya 19.23; Subuh 04.35 on the 11th.
  const texts = (h: ReturnType<typeof flowHarness>) => sentTexts(h.db).map((x) => x.text)

  it("Zuhur: every reminder fits, the last one at 15.15, closed at 15.24", async () => {
    const h = flowHarness({ engine: { readSource: fakePrayerTool() } })
    const { flow } = h.create(SHOLAT_GROWING)
    await h.run(at("2026-11-10T10:00"))
    for (const t of ["12:00", "12:10", "12:30", "13:10", "14:30", "15:15"]) await h.run(at(`2026-11-10T${t}`))
    expect(texts(h)).toEqual([
      "🕌 Waktu Zuhur masuk (12.00). Udah sholat?",
      "Udah sholat Zuhur?",
      "Udah sholat Zuhur?",
      "Udah sholat Zuhur?",
      "Udah sholat Zuhur?",
      "⏰ Terakhir: udah sholat Zuhur? 10 menit lagi masuk waktu berikutnya",
    ])
    const run = runsOf(h.db, flow.id).find((r) => r.runKey?.endsWith("dhuhr"))!
    expect((run.wait as { deadlineAt: number }).deadlineAt).toBe(at("2026-11-10T15:24"))
    await h.run(at("2026-11-10T15:24"))
    expect(texts(h).at(-1)).toBe("Zuhur kelewat, dicatat ya")
  })

  it("Magrib: reminders past 19.03 are skipped, the last one lands at 19.13", async () => {
    const h = flowHarness({ engine: { readSource: fakePrayerTool() } })
    h.create(SHOLAT_GROWING)
    await h.run(at("2026-11-10T17:00"))
    for (const t of ["18:11", "18:21", "18:41", "19:13"]) await h.run(at(`2026-11-10T${t}`))
    expect(texts(h)).toEqual([
      "🕌 Waktu Magrib masuk (18.11). Udah sholat?",
      "Udah sholat Magrib?",
      "Udah sholat Magrib?",
      "⏰ Terakhir: udah sholat Magrib? 10 menit lagi masuk waktu berikutnya",
    ])
  })

  it("Isya: the last reminder is 10 minutes before Subuh tomorrow", async () => {
    const h = flowHarness({ engine: { readSource: fakePrayerTool() } })
    const { flow } = h.create(SHOLAT_GROWING)
    await h.run(at("2026-11-10T19:00"))
    for (const t of ["2026-11-10T19:23", "2026-11-10T19:33", "2026-11-10T19:53", "2026-11-10T20:33", "2026-11-10T21:53", "2026-11-11T04:25"]) await h.run(at(t))
    expect(texts(h).at(-1)).toBe("⏰ Terakhir: udah sholat Isya? 10 menit lagi masuk waktu berikutnya")
    // Magrib (18.11) was still running when the test started and catches up first.
    expect(texts(h).filter((x) => x.includes("Isya"))).toHaveLength(6)
    const run = runsOf(h.db, flow.id).find((r) => r.runKey?.endsWith("isha"))!
    expect((run.wait as { deadlineAt: number }).deadlineAt).toBe(at("2026-11-11T04:34"))
  })

  it("an answer stops the chain", async () => {
    const h = flowHarness({ engine: { readSource: fakePrayerTool() } })
    const { flow } = h.create(SHOLAT_GROWING)
    await h.run(at("2026-11-10T11:00"))
    await h.run(at("2026-11-10T12:00"))
    const run = runsOf(h.db, flow.id).find((r) => r.runKey?.endsWith("dhuhr"))!
    answerFlowCheckin(h.db, { chatId: h.chatId, runId: run.id, answer: "done" }, at("2026-11-10T12:05"))
    await h.run(at("2026-11-10T15:20"))
    expect(texts(h)).toEqual(["🕌 Waktu Zuhur masuk (12.00). Udah sholat?"])
    expect(runsOf(h.db, flow.id).find((r) => r.id === run.id)!.status).toBe("done")
  })
})
