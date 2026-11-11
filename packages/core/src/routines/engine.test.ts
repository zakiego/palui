import { eq } from "drizzle-orm"
import { describe, expect, it, vi } from "vitest"
import { outbox, routineOccurrences, routines, type DB } from "@palui/db"
import { silentLogger } from "../log"
import { HOUR, MIN, NOW, setup, WITA } from "../reminders/test-helpers"
import { localToUtc } from "../reminders/time"
import { routineActions } from "./actions"
import { createRoutineEngine, NOTICE_AFTER_FAILURES, type RoutineEngineOptions } from "./engine"
import { answerCheckin, checkinsPrompt, createRoutine, getRoutine, openCheckins, setPaused, updateRoutine } from "./service"
import { fakePrayerTool, MEDICINE_ROUTINE, PRAYER_ROUTINE } from "./test-helpers"

const at = (local: string) => localToUtc(local, WITA)

function harness(extra: Partial<RoutineEngineOptions> = {}) {
  const { db, chatId, otherChatId } = setup()
  const call = fakePrayerTool()
  const engine = createRoutineEngine({ db, log: silentLogger, callTool: call, ...extra })
  const run = async (t: number) => {
    const n = engine.sweep(t)
    await engine.idle()
    return n
  }
  // Sweep every minute from `from` to `to`, like the scheduler's timer would.
  const runUntil = async (from: number, to: number, step = MIN) => {
    for (let t = from; t <= to; t += step) await run(t)
  }
  return { db, chatId, otherChatId, call, engine, run, runUntil }
}

function sent(db: DB) {
  return db
    .select()
    .from(outbox)
    .all()
    .map((r) => ({ id: r.id, source: r.source, text: r.payload.text, actions: r.payload.actions?.map((a) => a.id) ?? [] }))
}

function occs(db: DB) {
  return db.select().from(routineOccurrences).orderBy(routineOccurrences.scheduledAt).all()
}

describe("a dynamic-source check-in routine (prayer times from a tool)", () => {
  it("reads the source server-side, then asks at each time with answer buttons", async () => {
    const h = harness()
    const r = createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    expect(r.nextFireAt).toBeNull()
    await h.run(NOW)
    expect(h.call.calls.map((c) => [c.connectorId, c.tool, c.args.date])).toEqual([
      ["mcp-jadwal-sholat", "get_prayer_times", "2026-11-10"],
      ["mcp-jadwal-sholat", "get_prayer_times", "2026-11-11"],
    ])
    const fresh = getRoutine(h.db, r.id)!
    expect(fresh.nextFireAt).toBe(at("2026-11-10T12:00"))
    expect(fresh.nextRefreshAt).toBe(at("2026-11-11T00:05"))

    await h.run(at("2026-11-10T12:00"))
    const [occ] = occs(h.db)
    expect(occ).toMatchObject({ label: "Dzuhur", status: "open", key: "2026-11-10:times.dhuhr", deadlineAt: at("2026-11-10T15:25"), nextFollowUpAt: at("2026-11-10T12:20") })
    expect(sent(h.db)).toEqual([{ id: expect.any(String), source: `routine:${occ!.id}`, text: "Udah sholat Dzuhur? (12.00)", actions: [`rt:${occ!.id}:done`, `rt:${occ!.id}:not_yet`] }])
    expect(getRoutine(h.db, r.id)!.nextFireAt).toBe(at("2026-11-10T15:25"))
  })

  it("re-asks until a closing answer, records 'not yet', and stops on 'done'", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    await h.run(NOW)
    await h.runUntil(at("2026-11-10T12:00"), at("2026-11-10T12:20"))
    expect(sent(h.db).map((s) => s.text)).toEqual(["Udah sholat Dzuhur? (12.00)", "🔁 Udah sholat Dzuhur? (12.00)"])

    const occId = occs(h.db)[0]!.id
    const notYet = answerCheckin(h.db, { chatId: h.chatId, answer: "belum" }, at("2026-11-10T12:25"))
    expect(notYet.occurrence).toMatchObject({ status: "open", answer: "not_yet", nextFollowUpAt: at("2026-11-10T12:45") })
    expect(notYet.reply).toBe("Oke, dicatat: Belum. Aku tanya lagi jam 12.45.")

    const reply = await routineActions.fromCallback(h.db, `rt:${occId}:done`, h.chatId, at("2026-11-10T12:30"))
    expect(reply).toBe("Sip, dicatat: Sudah (Dzuhur).")
    await h.runUntil(at("2026-11-10T12:30"), at("2026-11-10T15:00"), 5 * MIN)
    expect(sent(h.db)).toHaveLength(2)
    expect(occs(h.db)[0]).toMatchObject({ status: "done", answer: "done", nextFollowUpAt: null })
    expect(occs(h.db)[0]!.answers.map((a) => a.answer)).toEqual(["not_yet", "done"])
  })

  it("stops after max re-asks and marks the check-in missed at the next occurrence", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    await h.run(NOW)
    await h.runUntil(at("2026-11-10T15:25"), at("2026-11-10T18:11"))
    const ashar = occs(h.db).find((o) => o.label === "Ashar")!
    expect(ashar).toMatchObject({ status: "missed", followUps: 3 })
    const texts = sent(h.db).map((s) => s.text)
    // Dzuhur (12.00) was before the window, so it never fired.
    expect(texts.filter((t) => t.includes("Ashar"))).toHaveLength(4)
    expect(texts.at(-1)).toBe("Udah sholat Maghrib? (18.11)")
  })

  it("never sends an occurrence twice, across overlapping sweeps and a crash mid-fire", async () => {
    const h = harness()
    const r = createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    await h.run(NOW)
    const t = at("2026-11-10T12:00")
    await Promise.all([h.run(t), h.run(t)])
    expect(sent(h.db)).toHaveLength(1)
    // A crash left the routine mid-fire, pointing at the same occurrence again.
    const fired = occs(h.db)[0]!
    h.db.update(routines).set({ status: "firing", nextFireAt: t, nextOcc: { at: t, key: fired.key, label: fired.label }, cursor: { at: NOW, key: "" } }).where(eq(routines.id, r.id)).run()
    const restarted = createRoutineEngine({ db: h.db, log: silentLogger, callTool: h.call })
    restarted.recover(t + MIN)
    restarted.sweep(t + MIN)
    expect(sent(h.db)).toHaveLength(1)
    expect(getRoutine(h.db, r.id)).toMatchObject({ status: "idle", nextFireAt: at("2026-11-10T15:25") })
  })

  it("after a long downtime skips stale check-ins and asks only the current one, marked late", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    await h.run(NOW)
    // Down from Tue 10:00 to Thu 12:30.
    const back = at("2026-11-12T12:30")
    for (let i = 0; i < 20; i++) await h.run(back)
    const texts = sent(h.db).map((s) => s.text)
    expect(texts).toEqual(["Udah sholat Dzuhur? (12.02) (telat, harusnya 12.02)"])
    expect(occs(h.db).filter((o) => o.status === "skipped").length).toBeGreaterThan(0)
  })

  it("falls back to the last good times when the source fails, and tells the owner once a day", async () => {
    const h = harness()
    const r = createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    await h.run(NOW)
    h.call.fail = true
    // Next day: the 00:05 refresh fails and retries with backoff (1, 5, 15 min...).
    await h.runUntil(at("2026-11-11T00:05"), at("2026-11-11T01:30"))
    const row = getRoutine(h.db, r.id)!
    expect(row.sourceState.failures).toBeGreaterThanOrEqual(NOTICE_AFTER_FAILURES)
    expect(row.lastError).toMatch(/ECONNRESET/)
    const notices = sent(h.db).filter((s) => s.source === `routine-notice:${r.id}`)
    expect(notices).toHaveLength(1)
    expect(notices[0]!.text).toContain("Sementara pakai jadwal dari data terakhir")
    // The 12th was never fetched: its Subuh uses the 11th's times and says so.
    await h.runUntil(at("2026-11-12T04:30"), at("2026-11-12T04:40"))
    const subuh = sent(h.db).find((s) => s.text.includes("Subuh") && occs(h.db).find((o) => o.key === "2026-11-12:times.fajr" && s.source === `routine:${o.id}`))
    expect(subuh?.text).toBe("Udah sholat Subuh? (04.35)\n(Jadwal pakai data terakhir yang berhasil diambil, sumbernya lagi error.)")
  })

  it("sends a daily recap of the log", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    await h.run(NOW)
    await h.run(at("2026-11-10T12:00"))
    answerCheckin(h.db, { chatId: h.chatId, answer: "udah" }, at("2026-11-10T12:05"))
    await h.run(at("2026-11-10T15:25"))
    await h.run(at("2026-11-10T18:11"))
    answerCheckin(h.db, { chatId: h.chatId, answer: "done" }, at("2026-11-10T18:15"))
    await h.run(at("2026-11-10T19:23"))
    await h.run(at("2026-11-10T21:30"))
    const recap = sent(h.db).find((s) => s.source.startsWith("routine-recap:"))
    expect(recap?.text).toBe(["📊 Rekap Sholat, 24 jam terakhir:", "- Sudah: 2", "- Terlewat: 1 (Ashar)", "- Masih nunggu jawaban: 1", "Selesai 2/3."].join("\n"))
  })
})

describe("a fixed-time check-in with custom answers (medicine)", () => {
  it("asks with its own answers, takes a quoted reply, and logs the custom reply", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    await h.run(at("2026-11-10T20:00"))
    const occ = occs(h.db)[0]!
    expect(sent(h.db)[0]).toMatchObject({ text: "💊 Obat malam udah diminum? Jawab: Udah minum / Skip / Nanti", actions: [`rt:${occ.id}:taken`, `rt:${occ.id}:skip`, `rt:${occ.id}:later`] })
    h.db.update(outbox).set({ channelMsgId: "wa-1" }).run()
    // Not an exact answer: left to the model.
    expect(await routineActions.fromText(h.db, "udah tapi telat 1 jam", "wa-1", h.chatId, at("2026-11-10T20:05"))).toBeNull()
    expect(await routineActions.fromText(h.db, "Udah!", "wa-1", h.chatId, at("2026-11-10T20:05"))).toBe("Mantap, obat malam tercatat 👍")
    expect(occs(h.db)[0]).toMatchObject({ status: "done", answer: "taken" })
  })

  it("uses its own re-ask text, then a fixed deadline with a missed note", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    await h.run(at("2026-11-11T08:00"))
    answerCheckin(h.db, { chatId: h.chatId, answer: "later", text: "bentar lagi" }, at("2026-11-11T08:01"))
    await h.runUntil(at("2026-11-11T08:01"), at("2026-11-11T10:00"))
    expect(sent(h.db).map((s) => s.text)).toEqual([
      "💊 Obat pagi udah diminum? Jawab: Udah minum / Skip / Nanti",
      "Pengingat ke-1: obat pagi jangan lupa ya.",
      "Pengingat ke-2: obat pagi jangan lupa ya.",
      "Obat pagi (08.00) kelewat, dicatat ya.",
    ])
    const pagi = occs(h.db).find((o) => o.label === "pagi")!
    expect(pagi).toMatchObject({ status: "missed", answer: "later", followUps: 2 })
    expect(pagi.answers[0]).toMatchObject({ answer: "later", text: "bentar lagi" })
    // Tuesday night was already past its deadline when this sweep ran: logged, not sent.
    expect(occs(h.db).find((o) => o.label === "malam")!.status).toBe("skipped")
  })

  it("only answers in its own chat (or where it was delivered)", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    await h.run(at("2026-11-10T20:00"))
    const occ = occs(h.db)[0]!
    expect(await routineActions.fromCallback(h.db, `rt:${occ.id}:taken`, h.otherChatId, NOW)).toBe("Check-in itu nggak ketemu di chat ini.")
    expect(() => answerCheckin(h.db, { chatId: h.otherChatId, answer: "taken" }, NOW)).toThrow(/No open check-in/)
  })

  it("puts open check-ins in the prompt so a bare reply can be resolved", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    expect(checkinsPrompt(h.db, h.chatId, NOW)).toBeNull()
    await h.run(at("2026-11-10T20:00"))
    const p = checkinsPrompt(h.db, h.chatId, at("2026-11-10T20:01"))!
    expect(p).toContain(`${occs(h.db)[0]!.id}: routine "Minum obat", item "malam", asked Selasa 10 Nov 20.00; answers: taken, skip, later (keeps asking)`)
    expect(openCheckins(h.db, h.otherChatId)).toEqual([])
  })
})

describe("pause and resume", () => {
  it("fires nothing while paused, stops re-asks, and resumes by itself at the end date", async () => {
    const h = harness()
    const r = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    await h.run(at("2026-11-10T20:00"))
    setPaused(h.db, r.id, true, at("2026-11-12T00:00"), at("2026-11-10T20:05"))
    await h.runUntil(at("2026-11-10T20:05"), at("2026-11-11T21:00"), 15 * MIN)
    expect(sent(h.db)).toHaveLength(1)
    await h.run(at("2026-11-12T00:00"))
    expect(getRoutine(h.db, r.id)).toMatchObject({ enabled: true, pausedUntil: null, nextFireAt: at("2026-11-12T08:00") })
    await h.run(at("2026-11-12T08:00"))
    expect(sent(h.db).at(-1)!.text).toContain("Obat pagi")
  })
})

describe("message and prompt actions", () => {
  it("sends a message on an RRULE, and skips one that is too late after downtime", async () => {
    const h = harness()
    createRoutine(h.db, h.chatId, { name: "Review mingguan", tz: WITA, source: { kind: "rrule", rrule: "FREQ=WEEKLY;BYDAY=FR", start: "2026-11-13T16:00", label: "Jumat" }, action: { kind: "message", text: "📝 {{name}}: waktunya review minggu ini ({{date}})." } }, NOW)
    await h.run(at("2026-11-13T16:00"))
    expect(sent(h.db).map((s) => s.text)).toEqual(["📝 Review mingguan: waktunya review minggu ini (Jumat 13 Nov)."])
    await h.run(at("2026-11-20T18:00"))
    expect(sent(h.db)).toHaveLength(1)
    expect(occs(h.db).map((o) => o.status)).toEqual(["sent", "skipped"])
  })

  it("runs a prompt action through the injected runner and logs a failure", async () => {
    const onPrompt = vi.fn(async () => {})
    const h = harness({ onPrompt })
    createRoutine(h.db, h.chatId, { name: "Air", tz: WITA, days: ["TU"], source: { kind: "times", times: [{ time: "11:00" }] }, action: { kind: "prompt", prompt: "Ingetin minum air jam {{time}} dengan gaya lucu." } }, NOW)
    await h.run(at("2026-11-10T11:00"))
    expect(onPrompt).toHaveBeenCalledWith(expect.objectContaining({ prompt: "Ingetin minum air jam 11.00 dengan gaya lucu.", untrusted: false }))
    onPrompt.mockRejectedValueOnce(new Error("no model"))
    await h.run(at("2026-11-17T11:00"))
    expect(occs(h.db).map((o) => o.status)).toEqual(["sent", "failed"])
  })

  it("applies an edit from now on", async () => {
    const h = harness()
    const r = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    const edited = updateRoutine(h.db, r.id, { source: { kind: "times", times: [{ time: "21:00", label: "malam" }] } }, NOW + HOUR)
    expect(edited.nextFireAt).toBe(at("2026-11-10T21:00"))
  })
})
