import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { ensureChat } from "@palui/core/chats"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { getRoutine } from "@palui/core/routines/service"
import { fakePrayerTool, MEDICINE_ROUTINE, PRAYER_ROUTINE } from "@palui/core/routines/test-helpers"
import { fakeCtx } from "../mcp/test-helpers"
import manifest from "./manifest"
import runtime, { routineTools } from "./runtime"

// Tuesday 2026-11-10 10:00 WITA.
const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"

function setup() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "whatsapp", externalId: "628@s.whatsapp.net" }, NOW)
  const other = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const turn: TurnInfo = { runId: "run_1", chatId: chat.id, channel: "whatsapp", trigger: "message", tz: WITA, now: NOW }
  const callTool = fakePrayerTool()
  const tools = routineTools(db, turn, { callTool, clock: () => NOW })
  return { db, chat, other, turn, tools, callTool }
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

describe("routines manifest", () => {
  it("is a builtin skill", () => {
    expect(manifest).toMatchObject({ id: "routines", kind: "skill", builtin: true, icon: "repeat" })
  })
})

describe("routine tools", () => {
  it("exposes generic tools with classes", () => {
    const { tools } = setup()
    expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.class]))).toEqual({
      buat_rutinitas: "write",
      daftar_rutinitas: "read",
      ubah_rutinitas: "write",
      jeda_rutinitas: "write",
      hapus_rutinitas: "destructive",
      pratinjau_rutinitas: "read",
      jawab_checkin: "write",
      riwayat_rutinitas: "read",
    })
    expect(tools.pratinjau_rutinitas?.untrusted).toBe(true)
  })

  it("creates the prayer-time routine from plain config, reading the tool once", async () => {
    const { db, tools, callTool, chat } = setup()
    const res = await call(tools.buat_rutinitas, PRAYER_ROUTINE)
    expect(res.ok).toBe(true)
    expect(callTool.calls).toHaveLength(2)
    expect(res.next.map((o: { when: string; label: string }) => `${o.label} ${o.when}`)).toEqual([
      "Dzuhur Hari ini, 12.00 WITA",
      "Ashar Hari ini, 15.25 WITA",
      "Maghrib Hari ini, 18.11 WITA",
      "Isya Hari ini, 19.23 WITA",
      "Subuh Besok, Rabu 11 Nov, 04.35 WITA",
    ])
    const row = getRoutine(db, res.id)!
    expect(row.chatId).toBe(chat.id)
    expect(row.nextFireAt).toBe(Date.parse("2026-11-10T04:00:00Z"))
    expect(res.routine.schedule).toBe("Dari mcp-jadwal-sholat.get_prayer_times (refresh 00:05): Subuh, Dzuhur, Ashar, Maghrib, Isya")
  })

  it("refuses a tool source it can't read, and a broken config, with a usable error", async () => {
    const { tools, callTool } = setup()
    callTool.fail = true
    const bad = await call(tools.buat_rutinitas, PRAYER_ROUTINE)
    expect(bad).toEqual({ ok: false, error: expect.stringMatching(/^Couldn't read times from mcp-jadwal-sholat.get_prayer_times: .*ECONNRESET/) })
    const both = await call(tools.buat_rutinitas, { ...PRAYER_ROUTINE, source: { ...PRAYER_ROUTINE.source, list: { at: "x" } } })
    expect(both.error).toMatch(/exactly one of "items"/)
  })

  it("creates a fixed-time medicine check-in and lists, edits, pauses and deletes it", async () => {
    const { tools, db, turn, other } = setup()
    const made = await call(tools.buat_rutinitas, MEDICINE_ROUTINE)
    expect(made.next[0]).toEqual({ when: "Hari ini, 20.00 WITA", label: "malam" })

    const list = await call(tools.daftar_rutinitas, {})
    expect(list.routines[0]).toMatchObject({ name: "Minum obat", active: true, action: "checkin", schedule: "Tiap hari: pagi 08.00, malam 20.00" })

    const edited = await call(tools.ubah_rutinitas, { id: made.id, days: ["MO", "WE", "FR"] })
    expect(edited.next[0]).toEqual({ when: "Besok, Rabu 11 Nov, 08.00 WITA", label: "pagi" })

    const paused = await call(tools.jeda_rutinitas, { id: made.id, paused: true, until: "2026-11-14T00:00" })
    expect(paused.message).toBe('Rutinitas "Minum obat" dijeda sampai Sabtu 14 Nov, 00.00 WITA.')
    expect((await call(tools.jeda_rutinitas, { id: made.id, paused: false })).message).toBe('Rutinitas "Minum obat" jalan lagi.')

    const otherTools = routineTools(db, { ...turn, chatId: other.id }, { clock: () => NOW })
    expect((await call(otherTools.hapus_rutinitas, { id: made.id })).ok).toBe(false)
    expect((await call(tools.hapus_rutinitas, { id: made.id })).message).toBe('Rutinitas "Minum obat" dihapus.')
    expect((await call(tools.daftar_rutinitas, {})).message).toBe("Belum ada rutinitas.")
  })

  it("previews a draft source, reporting fields that don't parse", async () => {
    const { tools } = setup()
    const res = await call(tools.pratinjau_rutinitas, { source: { ...PRAYER_ROUTINE.source, items: [{ field: "times.isha", label: "Isya" }, { field: "times.typo" }] }, count: 2 })
    expect(res.next).toEqual([
      { when: "Hari ini, 19.23 WITA", label: "Isya" },
      { when: "Besok, Rabu 11 Nov, 19.24 WITA", label: "Isya" },
    ])
    const water = await call(tools.pratinjau_rutinitas, { source: { kind: "times", times: [{ time: "09:00" }, { time: "13:00" }] }, days: ["SA"], count: 2 })
    expect(water.next.map((o: { when: string }) => o.when)).toEqual(["Sabtu 14 Nov, 09.00 WITA", "Sabtu 14 Nov, 13.00 WITA"])
  })

  it("records answers and shows the history", async () => {
    const { tools } = setup()
    expect((await call(tools.jawab_checkin, { answer: "udah" })).error).toMatch(/No open check-in/)
    const res = await call(tools.riwayat_rutinitas, { days: 7, limit: 10 })
    expect(res.summary).toMatchObject({ total: 0, missed: 0 })
  })

  it("test() and start() work", async () => {
    const ctx = fakeCtx({}, {}, { id: "routines" })
    expect(await runtime.test(ctx as never)).toEqual({ ok: true, detail: "Ready, no routines yet" })
    const running = await runtime.start(ctx as never)
    const tools = await running.tools?.({ runId: "r", chatId: "c", channel: null, trigger: "message", tz: WITA, now: NOW })
    expect(Object.keys(tools ?? {})).toContain("buat_rutinitas")
    await running.stop()
  })
})

describe("routine tools after the handover to flows", () => {
  it("create and edit flows of the routine shape, and find migrated routines by their old id", async () => {
    const { setRoutineMode, syncRoutineFlows, flowForRoutine, handover } = await import("@palui/core/flows/migrate")
    const { createRoutine } = await import("@palui/core/routines/service")
    const { listFlows } = await import("@palui/core/flows/service")
    const { flowBackedRoutineTools } = await import("./flows")
    const { db, turn } = setup()
    const old = createRoutine(db, turn.chatId, MEDICINE_ROUTINE, NOW)
    syncRoutineFlows(db, NOW)
    setRoutineMode(db, "flows", NOW)
    expect(handover(db, NOW)).toBe(1)
    const tools = flowBackedRoutineTools(db, turn, () => NOW)
    expect(Object.keys(tools)).not.toContain("jawab_checkin")

    const made = await call(tools.buat_rutinitas, { name: "Jalan pagi", source: { kind: "times", times: [{ time: "06:30" }] }, action: { kind: "message", text: "Ayo jalan pagi" } })
    expect(made).toMatchObject({ ok: true, message: 'Oke, rutinitas "Jalan pagi" aktif.' })
    expect(listFlows(db, turn.chatId).map((f) => f.name).sort()).toEqual(["Jalan pagi", "Minum obat"])

    const edited = await call(tools.ubah_rutinitas, { id: old.id, days: ["MO", "TU"] })
    expect(edited).toMatchObject({ ok: true, routine: { id: flowForRoutine(db, old.id)!.id, config: { days: ["MO", "TU"] } } })
    expect((await call(tools.daftar_rutinitas, {})).count).toBe(2)
    expect(await call(tools.jeda_rutinitas, { id: made.id, paused: true })).toMatchObject({ ok: true, message: 'Rutinitas "Jalan pagi" dijeda.' })
  })
})
