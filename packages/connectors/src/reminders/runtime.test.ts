import { describe, expect, it } from "vitest"
import { memoryDb, type DB } from "@palui/db"
import { ensureChat } from "@palui/core/chats"
import { createJob } from "@palui/core/jobs/service"
import { dailyBriefing } from "@palui/core/jobs/presets"
import { silentLogger } from "@palui/core/log"
import { getReminder } from "@palui/core/reminders/service"
import { utcToLocal } from "@palui/core/reminders/time"
import type { ConnectorContext, PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import jobsRuntime, { jobTools } from "../jobs/runtime"
import jobsManifest from "../jobs/manifest"
import manifest from "./manifest"
import runtime, { reminderTools } from "./runtime"

// Tuesday 2026-11-10 10:00 WITA.
const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"

function setup() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const other = ensureChat(db, { channel: "web", externalId: "web" }, NOW)
  const turn: TurnInfo = { runId: "run_1", chatId: chat.id, channel: "telegram", trigger: "message", tz: WITA, now: NOW }
  const tools = reminderTools(db, turn, () => NOW)
  return { db, chat, other, turn, tools }
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

function ctx(db: DB): ConnectorContext<Record<string, never>, Record<string, never>> {
  return {
    id: "reminders",
    type: "reminders",
    config: {},
    secrets: {},
    trust: "own",
    db,
    log: silentLogger,
    dataDir: "/tmp",
    signal: new AbortController().signal,
    services: { inbound: async () => {}, claimOwner: async () => false, isOwner: () => true },
    setStatus() {},
    event() {},
    patchConfig() {},
  }
}

describe("reminders manifest", () => {
  it("is a builtin skill with the pause note", () => {
    expect(manifest).toMatchObject({ id: "reminders", kind: "skill", builtin: true, icon: "alarm-clock" })
    expect(manifest.pauseNote).toBe("Pausing hides the reminder tools. Reminders already set still fire.")
    expect(jobsManifest).toMatchObject({ id: "jobs", kind: "skill", builtin: true, icon: "calendar-clock" })
  })
})

describe("reminder tools", () => {
  it("exposes the five tools with classes", () => {
    const { tools } = setup()
    expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, v.class]))).toEqual({
      set_reminder: "write",
      list_reminders: "read",
      update_reminder: "write",
      cancel_reminder: "write",
      snooze_reminder: "write",
    })
  })

  it("set_reminder creates in the turn's chat and echoes the time", async () => {
    const { db, chat, tools } = setup()
    const res = await call(tools.set_reminder, { text: "bayar listrik", localDateTime: "2026-11-11T09:00" })
    expect(res.ok).toBe(true)
    expect(res.echo).toBe("Besok, Rabu 11 Nov, 09.00 WITA")
    expect(res.message).toBe('Oke, aku ingetin "bayar listrik": Besok, Rabu 11 Nov, 09.00 WITA.')
    expect(getReminder(db, res.id)?.chatId).toBe(chat.id)
    const weekly = await call(tools.set_reminder, { text: "laporan", localDateTime: "2026-11-16T08:00", rrule: "FREQ=WEEKLY;BYDAY=MO" })
    expect(weekly.message).toBe('Oke, aku ingetin "laporan": Tiap Senin, 08.00 WITA. Pertama: Senin 16 Nov, 08.00 WITA.')
  })

  it("set_reminder returns errors instead of throwing", async () => {
    const { tools } = setup()
    expect((await call(tools.set_reminder, { text: "x", localDateTime: "2026-11-09T09:00" })).ok).toBe(false)
    expect((await call(tools.set_reminder, { text: "x", localDateTime: "2026-11-11T09:00", tz: "Mars/Base" })).ok).toBe(false)
    expect((await call(tools.set_reminder, { text: "x", localDateTime: "2026-11-11T09:00", rrule: "FREQ=OFTEN" })).ok).toBe(false)
  })

  it("uses an explicit zone", async () => {
    const { db, tools } = setup()
    const res = await call(tools.set_reminder, { text: "call", localDateTime: "2026-11-11T09:00", tz: "Asia/Jakarta" })
    expect(res.echo).toBe("Besok, Rabu 11 Nov, 09.00 WIB")
    expect(utcToLocal(getReminder(db, res.id)?.fireAt as number, "Asia/Jakarta")).toBe("2026-11-11T09:00:00")
  })

  it("lists, changes, snoozes and cancels only this chat's reminders", async () => {
    const { db, other, turn, tools } = setup()
    const a = await call(tools.set_reminder, { text: "a", localDateTime: "2026-11-11T09:00" })
    const otherTools = reminderTools(db, { ...turn, chatId: other.id }, () => NOW)
    const b = await call(otherTools.set_reminder, { text: "b", localDateTime: "2026-11-11T10:00" })

    const list = await call(tools.list_reminders, { filter: "upcoming", limit: 20 })
    expect(list.reminders.map((r: { id: string }) => r.id)).toEqual([a.id])

    const changed = await call(tools.update_reminder, { id: a.id, localDateTime: "2026-11-12T07:30" })
    expect(changed.message).toBe('Sip, pengingat "a" sekarang Kamis 12 Nov, 07.30 WITA.')

    const snoozed = await call(tools.snooze_reminder, { id: a.id, preset: "1h" })
    expect(snoozed.message).toBe("Oke, ditunda sampai 11.00 WITA.")
    const exact = await call(tools.snooze_reminder, { id: a.id, localDateTime: "2026-11-10T15:00" })
    expect(exact.message).toBe("Oke, ditunda sampai 15.00 WITA.")
    expect((await call(tools.snooze_reminder, { id: a.id })).ok).toBe(false)

    expect((await call(tools.cancel_reminder, { id: b.id })).ok).toBe(false)
    expect((await call(tools.update_reminder, { id: b.id, text: "hijack" })).ok).toBe(false)
    expect(getReminder(db, b.id)?.text).toBe("b")

    const cancelled = await call(tools.cancel_reminder, { id: a.id })
    expect(cancelled.message).toBe('Oke, pengingat "a" dibatalin.')
    const done = await call(tools.list_reminders, { filter: "done", limit: 20 })
    expect(done.reminders[0].status).toBe("cancelled")
    expect((await call(tools.list_reminders, { filter: "upcoming", limit: 20 })).message).toBe("Nggak ada pengingat aktif.")
  })

  it("test() and start() work", async () => {
    const { db, turn } = setup()
    expect(await runtime.test(ctx(db))).toEqual({ ok: true, detail: "Ready, no active reminders" })
    const running = await runtime.start(ctx(db))
    const tools = await running.tools?.(turn)
    expect(Object.keys(tools ?? {})).toContain("set_reminder")
    await running.stop()
  })
})

describe("job tools", () => {
  it("lists jobs with the schedule and next run", async () => {
    const { db, chat } = setup()
    createJob(db, dailyBriefing(chat.id, WITA, NOW), NOW)
    const res = await call(jobTools(db, () => NOW).list_jobs, { includePaused: true })
    expect(res.jobs[0]).toMatchObject({ name: "Briefing pagi", schedule: "Tiap hari, 07.00 WITA", next: "Besok, Rabu 11 Nov, 07.00 WITA", enabled: true })
    expect(jobTools(db).list_jobs?.class).toBe("read")
    expect(await jobsRuntime.test(ctx(db))).toEqual({ ok: true, detail: "1 active job" })
  })
})
