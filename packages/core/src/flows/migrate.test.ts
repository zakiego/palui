import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { flowRuns, flows, outbox, routineOccurrences } from "@palui/db"
import { silentLogger } from "../log"
import { MIN } from "../reminders/test-helpers"
import { routineActions } from "../routines/actions"
import { createRoutineEngine } from "../routines/engine"
import { createRoutine, deleteRoutine, setPaused, updateRoutine } from "../routines/service"
import { fakePrayerTool, MEDICINE_ROUTINE, PRAYER_ROUTINE } from "../routines/test-helpers"
import { getSetting } from "../settings"
import { flowForRoutine, followRoutine, runForLegacyOccurrence, setRoutineMode, shadowCompare, shadowReport, syncRoutineFlows } from "./migrate"
import { listVersions, triggersOf } from "./service"
import { at, exampleConnectors, flowHarness, NOW, runsOf, sentTexts } from "./test-harness"

function both() {
  const prayer = fakePrayerTool()
  const h = flowHarness({ engine: { readSource: prayer }, connectors: exampleConnectors() })
  const routines = createRoutineEngine({ db: h.db, log: silentLogger, callTool: prayer, now: h.now })
  // The scheduler runs routines first, then flows, on the same timer.
  const tick = async (t: number) => {
    h.setNow(t)
    routines.sweep(t)
    await routines.idle()
    await h.run(t)
  }
  return { ...h, prayer, routines, tick }
}

describe("routine data migration (RFC 3.6)", () => {
  it("defaults to shadow mode and creates one shadow flow per routine, idempotently", () => {
    const h = both()
    expect(getSetting(h.db, "routineMode")).toBe("shadow")
    const med = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    expect(syncRoutineFlows(h.db, NOW)).toMatchObject({ created: 1, updated: 0 })
    expect(syncRoutineFlows(h.db, NOW)).toMatchObject({ created: 0, updated: 0 })
    const flow = flowForRoutine(h.db, med.id)!
    expect(flow).toMatchObject({ name: "Minum obat", shadow: true, legacyRoutineId: med.id })
    expect(listVersions(h.db, flow.id)[0]!.createdBy).toBe("migration")
    // The schedule state is the routine's.
    const main = triggersOf(h.db, flow.id).find((t) => t.triggerId === "main")!
    expect(main).toMatchObject({ nextFireAt: med.nextFireAt, cursor: med.cursor })

    updateRoutine(h.db, med.id, { action: { ...MEDICINE_ROUTINE.action, text: "Obat {{label}}?" } }, NOW + MIN)
    expect(syncRoutineFlows(h.db, NOW + 2 * MIN)).toMatchObject({ updated: 1 })
    expect(listVersions(h.db, flow.id)).toHaveLength(2)
    deleteRoutine(h.db, med.id)
    expect(syncRoutineFlows(h.db, NOW + 3 * MIN)).toMatchObject({ removed: 1 })
    expect(h.db.select().from(flows).all()).toEqual([])
  })

  it("a paused routine pauses its shadow flow, and deleting it removes the flow", () => {
    const h = both()
    const med = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    syncRoutineFlows(h.db, NOW)
    setPaused(h.db, med.id, true, NOW + 2 * 24 * 3600_000, NOW + MIN)
    followRoutine(h.db, med.id, NOW + MIN)
    expect(flowForRoutine(h.db, med.id)).toMatchObject({ enabled: false, pausedUntil: NOW + 2 * 24 * 3600_000 })
    expect(triggersOf(h.db, flowForRoutine(h.db, med.id)!.id).every((t) => t.nextFireAt === null)).toBe(true)
    // Resumed from chat: the next sync catches it too.
    setPaused(h.db, med.id, false, null, NOW + 2 * MIN)
    expect(syncRoutineFlows(h.db, NOW + 3 * MIN).updated).toBe(1)
    expect(flowForRoutine(h.db, med.id)!.enabled).toBe(true)
    deleteRoutine(h.db, med.id)
    followRoutine(h.db, med.id, NOW + 4 * MIN)
    expect(flowForRoutine(h.db, med.id)).toBeUndefined()
  })

  it("legacy mode migrates nothing", () => {
    const h = both()
    setRoutineMode(h.db, "legacy", NOW)
    createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    expect(syncRoutineFlows(h.db, NOW)).toEqual({ created: 0, updated: 0, removed: 0, failed: 0 })
  })
})

describe("shadow week (RFC 12.6)", () => {
  it("computes the same occurrences as the routine engine, sends nothing, and reports no difference", async () => {
    const h = both()
    createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    createRoutine(h.db, h.chatId, PRAYER_ROUTINE, NOW)
    syncRoutineFlows(h.db, NOW)
    for (let t = NOW; t <= at("2026-11-11T22:00"); t += 5 * MIN) await h.tick(t)
    shadowCompare(h.db, at("2026-11-11T23:00"))
    // Only the routine engine sent anything.
    expect(sentTexts(h.db).every((s) => s.source.startsWith("routine"))).toBe(true)
    const report = shadowReport(h.db, NOW)
    expect(report.diffs).toEqual([])
    expect(report.compared).toBe(h.db.select().from(routineOccurrences).all().length)
    expect(report.matches).toBe(report.compared)
    expect(report.compared).toBeGreaterThan(8)
  })

  it("logs a difference when the routine fired something the flow didn't", async () => {
    const h = both()
    const med = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    syncRoutineFlows(h.db, NOW)
    // A routine occurrence the shadow flow never saw.
    h.db.insert(routineOccurrences).values({ id: "rto_x", routineId: med.id, chatId: h.chatId, key: "2026-11-10:13:00", label: "x", scheduledAt: at("2026-11-10T13:00"), firedAt: at("2026-11-10T13:00"), status: "sent", createdAt: NOW, updatedAt: NOW }).run()
    shadowCompare(h.db, at("2026-11-10T14:00"))
    expect(shadowReport(h.db, NOW).diffs).toEqual([{ flow: "Minum obat", key: "2026-11-10:13:00", result: "missing_in_flow", at: at("2026-11-10T13:00") }])
  })
})

describe("handover to flows", () => {
  it("takes over the open check-in and the schedule without firing twice", async () => {
    const h = both()
    const med = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    syncRoutineFlows(h.db, NOW)
    // The routine asks at 20:00 and the owner hasn't answered.
    await h.tick(at("2026-11-10T20:00"))
    const [occ] = h.db.select().from(routineOccurrences).all()
    expect(occ).toMatchObject({ status: "open", key: "2026-11-10:20:00" })
    const before = sentTexts(h.db).length

    setRoutineMode(h.db, "flows", at("2026-11-10T20:05"))
    await h.tick(at("2026-11-10T20:05"))
    const flow = flowForRoutine(h.db, med.id)!
    expect(flow.shadow).toBe(false)
    const run = runForLegacyOccurrence(h.db, occ!.id)!
    expect(run).toMatchObject({ status: "waiting", runKey: "2026-11-10:20:00", cursor: "ask" })
    expect(sentTexts(h.db)).toHaveLength(before)

    // The re-ask now comes from the flow, with fl: buttons; the routine stays quiet.
    await h.tick(at("2026-11-10T20:15"))
    const reask = sentTexts(h.db).at(-1)!
    expect(reask).toMatchObject({ source: `flow:${run.id}`, text: "Pengingat ke-1: obat malam jangan lupa ya." })
    expect(h.db.select().from(routineOccurrences).where(eq(routineOccurrences.id, occ!.id)).get()!.followUps).toBe(0)

    // An old routine button answers the flow run.
    expect(await routineActions.fromCallback(h.db, `rt:${occ!.id}:taken`, h.chatId, at("2026-11-10T20:20"))).toBe("Mantap, obat malam tercatat 👍")
    await h.tick(at("2026-11-10T20:20"))
    expect(h.db.select().from(flowRuns).where(eq(flowRuns.id, run.id)).get()!.status).toBe("done")

    // Next morning the flow fires 08:00 once; the routine doesn't.
    await h.tick(at("2026-11-11T08:00"))
    const mains = runsOf(h.db, flow.id).filter((r) => !r.skipReason)
    expect(mains.map((r) => r.runKey)).toEqual(["2026-11-10:20:00", "2026-11-11:08:00"])
    expect(h.db.select().from(routineOccurrences).all()).toHaveLength(1)
    expect(h.db.select().from(outbox).all().filter((o) => o.source.startsWith("routine:"))).toHaveLength(1)
  })

  it("moves closed occurrences into the log the recap reads", async () => {
    const h = both()
    const med = createRoutine(h.db, h.chatId, MEDICINE_ROUTINE, NOW)
    syncRoutineFlows(h.db, NOW)
    await h.tick(at("2026-11-10T20:00"))
    await h.tick(at("2026-11-10T22:00"))
    setRoutineMode(h.db, "flows", at("2026-11-10T22:05"))
    await h.tick(at("2026-11-10T22:05"))
    const run = runsOf(h.db, flowForRoutine(h.db, med.id)!.id).find((r) => r.legacyOccurrenceId)!
    expect(run).toMatchObject({ status: "done", vars: { steps: { ask: { missed: true } } } })
  })
})
