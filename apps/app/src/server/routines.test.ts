import { toolSourceSchema } from "@palui/core/routines/config"
import { createRoutine, getRoutine } from "@palui/core/routines/service"
import { refreshToolSource } from "@palui/core/routines/sources"
import { fakePrayerTool, MEDICINE_ROUTINE, PRAYER_ROUTINE } from "@palui/core/routines/test-helpers"
import { chats, connectors, memoryDb, newId, routineOccurrences, routines, type DB, type RoutineOccurrenceStatus } from "@palui/db"
import { eq } from "drizzle-orm"
import { afterEach, describe, expect, it } from "vitest"
import { labelTally, logWindow, pauseRoutine, removeRoutine, resumeRoutine, routineDetail, routineList, RoutineNotFoundError } from "./routines"
import { setRuntime } from "./runtime"
import { upRuntime } from "./test-helpers"

// Wed 11 Nov 2026, 12:00 in Asia/Makassar (UTC+8).
const NOW = Date.UTC(2026, 10, 11, 4, 0)
const HOUR = 3600_000
const TZ = "Asia/Makassar"

afterEach(() => setRuntime(undefined))

async function seed() {
  const db = memoryDb()
  db.insert(chats)
    .values([
      { id: "chat_tg", channel: "telegram", externalId: "123", kind: "dm", title: "Zaki", createdAt: NOW },
      { id: "chat_wa", channel: "whatsapp", externalId: "628", kind: "dm", title: "", createdAt: NOW },
    ])
    .run()
  const medicine = createRoutine(db, "chat_wa", MEDICINE_ROUTINE, NOW)
  const tool = fakePrayerTool()
  const pre = await refreshToolSource(toolSourceSchema.parse(PRAYER_ROUTINE.source), TZ, {}, tool, NOW)
  const prayer = createRoutine(db, "chat_tg", { ...PRAYER_ROUTINE, recap: { kind: "weekly", day: "SU", time: "20:00" } }, NOW, { state: pre.state, nextRefreshAt: NOW + 12 * HOUR })
  return { db, medicine, prayer }
}

function occ(db: DB, routineId: string, chatId: string, label: string, at: number, status: RoutineOccurrenceStatus, extra: Partial<typeof routineOccurrences.$inferInsert> = {}) {
  db.insert(routineOccurrences)
    .values({ id: newId("rocc"), routineId, chatId, key: `${at}:${label}`, label, scheduledAt: at, firedAt: at, status, createdAt: at, updatedAt: at, ...extra })
    .run()
}

describe("routineList", () => {
  it("lists every routine across chats with the chat, schedule text and next times", async () => {
    const { db } = await seed()
    const list = routineList(db, NOW)
    expect(list.map((r) => r.name)).toEqual(["Minum obat", "Sholat"])
    const [medicine, prayer] = list
    expect(medicine).toMatchObject({ chat: { id: "chat_wa", title: "628", channel: "whatsapp" }, active: true, sourceKind: "times", actionKind: "checkin", connector: null })
    // The same sentence daftar_rutinitas gives the model.
    expect(medicine?.schedule).toBe("Tiap hari: pagi 08.00, malam 20.00")
    expect(medicine?.next.map((o) => o.label)).toEqual(["malam", "pagi"])
    expect(prayer).toMatchObject({ chat: { title: "Zaki", channel: "telegram" }, sourceKind: "tool", recap: { kind: "weekly", day: "SU", time: "20:00" } })
    expect(prayer?.schedule).toMatch(/^Dari mcp-jadwal-sholat\.get_prayer_times/)
  })

  it("flags a tool source whose connector is missing, off or failing", async () => {
    const { db } = await seed()
    expect(routineList(db, NOW)[1]?.connector).toMatchObject({ id: "mcp-jadwal-sholat", state: "missing" })
    db.insert(connectors).values({ id: "mcp-jadwal-sholat", type: "mcp", kind: "tools", label: "Jadwal Sholat", enabled: false, status: "paused", createdAt: NOW, updatedAt: NOW }).run()
    expect(routineList(db, NOW)[1]?.connector).toMatchObject({ state: "disabled", label: "Jadwal Sholat" })
    db.update(connectors).set({ enabled: true, status: "error", statusDetail: "fetch failed" }).run()
    expect(routineList(db, NOW)[1]?.connector).toMatchObject({ state: "error", detail: "fetch failed" })
    db.update(connectors).set({ status: "on", statusDetail: null }).run()
    expect(routineList(db, NOW)[1]?.connector).toMatchObject({ state: "ok" })
  })

  it("counts open check-ins and keeps the last source error", async () => {
    const { db, prayer } = await seed()
    occ(db, prayer.id, "chat_tg", "Dzuhur", NOW - HOUR, "open")
    db.update(routines).set({ lastError: "fetch failed: ECONNRESET" }).run()
    const row = routineList(db, NOW).find((r) => r.id === prayer.id)
    expect(row).toMatchObject({ openCheckins: 1, lastError: "fetch failed: ECONNRESET" })
  })

  it("still lists a routine whose stored config no longer parses", async () => {
    const { db, medicine } = await seed()
    db.update(routines).set({ action: { kind: "teleport" } }).where(eq(routines.id, medicine.id)).run()
    const row = routineList(db, NOW).find((r) => r.id === medicine.id)
    expect(row).toMatchObject({ schedule: null, configProblem: "invalid", actionKind: "teleport", next: [] })
  })
})

describe("routineDetail", () => {
  it("returns null for an unknown id", async () => {
    const { db } = await seed()
    expect(routineDetail(db, "rtn_nope", {}, NOW)).toBeNull()
  })

  it("shows the config, the next 8 times and the last 7 days of log with counts", async () => {
    const { db, prayer } = await seed()
    const day = 24 * HOUR
    occ(db, prayer.id, "chat_tg", "Subuh", NOW - 2 * day, "done", { answer: "done", answeredAt: NOW - 2 * day + 60_000, answers: [{ answer: "done", text: "alhamdulillah udah", at: NOW - 2 * day + 60_000 }] })
    occ(db, prayer.id, "chat_tg", "Subuh", NOW - day, "missed")
    occ(db, prayer.id, "chat_tg", "Dzuhur", NOW - HOUR, "open", { followUps: 1, nextFollowUpAt: NOW + 15 * 60_000 })
    occ(db, prayer.id, "chat_tg", "Subuh", NOW - 10 * day, "done")
    const d = routineDetail(db, prayer.id, {}, NOW)!
    expect(d.config?.source).toMatchObject({ kind: "tool", connectorId: "mcp-jadwal-sholat", tool: "get_prayer_times" })
    expect(JSON.parse(d.rawJson)).toMatchObject({ id: prayer.id, source: { kind: "tool", args: { city: "banjarmasin", date: "{{date}}" } } })
    expect(d.preview).toHaveLength(8)
    expect(d.preview.every((o, i) => i === 0 || o.at > d.preview[i - 1]!.at)).toBe(true)
    expect(d.range).toEqual({ from: "2026-11-05", to: "2026-11-11" })
    expect(d.log.map((o) => o.status)).toEqual(["open", "missed", "done"])
    expect(d.log[2]).toMatchObject({ answer: "done", answerLabel: "Sudah", note: "alhamdulillah udah" })
    expect(d.counts).toEqual({ open: 1, done: 1, missed: 1, sent: 0, skipped: 0, failed: 0 })
    // The delete dialog counts the whole log, not just this range.
    expect(d.logTotal).toBe(4)
    // Weekly on Sunday 20:00 Makassar: Sun 15 Nov, 12:00 UTC.
    expect(d.nextRecapAt).toBe(Date.UTC(2026, 10, 15, 12, 0))
    expect(d.tally).toEqual([{ label: "Subuh", done: 1, counted: 2 }])
    expect(d.open).toEqual([expect.objectContaining({ label: "Dzuhur", reAsks: 1, nextFollowUpAt: NOW + 15 * 60_000 })])
  })

  it("filters the log by a date range in the routine's zone", async () => {
    const { db, prayer } = await seed()
    // 23:30 local on 1 Nov is still 1 Nov in Makassar, though it's 15:30 UTC.
    occ(db, prayer.id, "chat_tg", "Isya", Date.UTC(2026, 10, 1, 15, 30), "done")
    occ(db, prayer.id, "chat_tg", "Subuh", Date.UTC(2026, 10, 1, 16, 30), "done")
    const d = routineDetail(db, prayer.id, { from: "2026-11-01", to: "2026-11-01" }, NOW)!
    expect(d.log.map((o) => o.label)).toEqual(["Isya"])
  })

  it("lists no upcoming times while paused", async () => {
    const { db, medicine } = await seed()
    pauseRoutine(db, medicine.id, null, NOW)
    expect(routineDetail(db, medicine.id, {}, NOW)?.preview).toEqual([])
  })
})

describe("logWindow", () => {
  it("defaults to the last 7 days, orders swapped ends and caps the span at a year", () => {
    expect(logWindow(TZ, {}, NOW)).toMatchObject({ from: "2026-11-05", to: "2026-11-11" })
    expect(logWindow(TZ, { from: "2026-11-10", to: "2026-11-01" }, NOW)).toMatchObject({ from: "2026-11-01", to: "2026-11-10" })
    expect(logWindow(TZ, { from: "2020-01-01", to: "2026-11-11" }, NOW).from).toBe("2025-11-10")
    expect(logWindow(TZ, { from: "nope" }, NOW).from).toBe("2026-11-05")
  })
})

describe("labelTally", () => {
  it("counts closed items per label, leaving out open and skipped ones", () => {
    const row = (label: string, status: RoutineOccurrenceStatus, at: number) => ({ label, status, scheduledAt: at }) as Parameters<typeof labelTally>[0][number]
    expect(labelTally([row("Isya", "done", 5), row("Subuh", "missed", 2), row("Subuh", "done", 1), row("Subuh", "open", 3), row("Isya", "skipped", 4)])).toEqual([
      { label: "Subuh", done: 1, counted: 2 },
      { label: "Isya", done: 1, counted: 1 },
    ])
  })
})

describe("routine writes", () => {
  it("pause, pause until a local time, and resume go through the service and poke the runtime", async () => {
    const { db, medicine } = await seed()
    const rt = upRuntime()
    setRuntime(rt)
    occ(db, medicine.id, "chat_wa", "pagi", NOW - HOUR, "open", { nextFollowUpAt: NOW + 60_000 })

    pauseRoutine(db, medicine.id, null, NOW)
    expect(getRoutine(db, medicine.id)).toMatchObject({ enabled: false, pausedUntil: null, nextFireAt: null })
    // Paused check-ins stop asking (service rule, not repeated here).
    expect(db.select().from(routineOccurrences).get()?.nextFollowUpAt).toBeNull()

    pauseRoutine(db, medicine.id, "2026-11-12T07:00", NOW)
    expect(getRoutine(db, medicine.id)?.pausedUntil).toBe(Date.UTC(2026, 10, 11, 23, 0))
    expect(routineList(db, NOW)[0]).toMatchObject({ active: false, pausedUntil: Date.UTC(2026, 10, 11, 23, 0) })

    resumeRoutine(db, medicine.id, NOW)
    expect(getRoutine(db, medicine.id)).toMatchObject({ enabled: true, pausedUntil: null })
    expect(getRoutine(db, medicine.id)?.nextFireAt).toBeGreaterThan(NOW)
    expect(rt.reconcile).toHaveBeenCalledTimes(3)
  })

  it("refuses a pause end in the past with the service's sentence", async () => {
    const { db, medicine } = await seed()
    setRuntime(upRuntime())
    expect(() => pauseRoutine(db, medicine.id, "2026-11-10T07:00", NOW)).toThrow("The pause end must be in the future.")
    expect(getRoutine(db, medicine.id)?.enabled).toBe(true)
  })

  it("delete removes the routine and its whole log, and leaves other routines alone", async () => {
    const { db, medicine, prayer } = await seed()
    setRuntime(upRuntime())
    occ(db, medicine.id, "chat_wa", "pagi", NOW - HOUR, "done")
    occ(db, prayer.id, "chat_tg", "Subuh", NOW - HOUR, "done")
    removeRoutine(db, medicine.id)
    expect(getRoutine(db, medicine.id)).toBeUndefined()
    expect(db.select().from(routineOccurrences).all().map((o) => o.routineId)).toEqual([prayer.id])
  })

  it("throws a not-found error for an unknown id", async () => {
    const { db } = await seed()
    const rt = upRuntime()
    setRuntime(rt)
    expect(() => pauseRoutine(db, "rtn_nope", null, NOW)).toThrow(RoutineNotFoundError)
    expect(() => resumeRoutine(db, "rtn_nope", NOW)).toThrow(RoutineNotFoundError)
    expect(() => removeRoutine(db, "rtn_nope")).toThrow(RoutineNotFoundError)
    expect(rt.reconcile).not.toHaveBeenCalled()
  })
})
