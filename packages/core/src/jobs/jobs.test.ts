import { describe, expect, it, vi } from "vitest"
import { newId, outbox, runs } from "@palui/db"
import { silentLogger } from "../log"
import { createScheduler } from "../reminders/scheduler"
import { fakeClock, HOUR, MIN, NOW, setup, WITA } from "../reminders/test-helpers"
import { localToUtc, occurrencesAfter, utcToLocal } from "../reminders/time"
import { dailyBriefing, HEARTBEAT_RRULE, heartbeat, SILENT_TOKEN } from "./presets"
import { addUsage, createJobRunner, getUsage, jobUsageKey, type RunTurn } from "./runner"
import { createJob, deleteJob, describeJobSchedule, describeNextRun, getJob, JobError, listJobs, setJobEnabled, updateJob } from "./service"

const local = (t: number) => utcToLocal(t, WITA).slice(0, 16)

describe("job service", () => {
  it("computes next_run_at in wall-clock time", () => {
    const { db, chatId } = setup()
    const j = createJob(db, dailyBriefing(chatId, WITA, NOW), NOW)
    expect(local(j.nextRunAt as number)).toBe("2026-11-11T07:00")
    expect(describeJobSchedule(j, NOW)).toBe("Tiap hari, 07.00 WITA")
    expect(describeNextRun(j, NOW)).toBe("Besok, Rabu 11 Nov, 07.00 WITA")
  })

  it("runs the heartbeat every 30 minutes from 08:00 to 22:00 inclusive", () => {
    const { db, chatId } = setup()
    const j = createJob(db, heartbeat(chatId, WITA, NOW), NOW)
    expect(local(j.nextRunAt as number)).toBe("2026-11-10T10:30")
    const s = { rrule: j.rrule, tz: WITA, localStart: j.localStart }
    const day = occurrencesAfter(s, localToUtc("2026-11-11T00:00", WITA), 40)
      .filter((t) => local(t).startsWith("2026-11-11"))
      .map((t) => local(t).slice(11))
    expect(day).toHaveLength(29)
    expect(day[0]).toBe("08:00")
    expect(day.at(-1)).toBe("22:00")
    expect(day).not.toContain("22:30")
    expect(day).not.toContain("07:30")
    // After 22:00 the next run is 08:00 the next morning.
    expect(local(occurrencesAfter(s, localToUtc("2026-11-11T22:00", WITA), 1)[0] as number)).toBe("2026-11-12T08:00")
    expect(describeJobSchedule(j, NOW)).toBe("Tiap hari, tiap 30 menit 08.00 sampai 22.00 WITA")
    expect(HEARTBEAT_RRULE).toContain("BYSETPOS=1,2,")
  })

  it("pauses, resumes from now, edits and deletes", () => {
    const { db, chatId } = setup()
    const j = createJob(db, { name: "Cek", rrule: "FREQ=WEEKLY;BYDAY=MO", tz: WITA, localStart: "2026-11-16T09:00", prompt: "cek", chatId }, NOW)
    expect(local(j.nextRunAt as number)).toBe("2026-11-16T09:00")
    expect(setJobEnabled(db, j.id, false, NOW).nextRunAt).toBeNull()
    // Resumed after Monday: the missed run is skipped.
    const resumed = setJobEnabled(db, j.id, true, localToUtc("2026-11-17T09:00", WITA))
    expect(local(resumed.nextRunAt as number)).toBe("2026-11-23T09:00")
    const edited = updateJob(db, j.id, { rrule: "FREQ=DAILY", localStart: "2026-11-10T18:00" }, NOW)
    expect(local(edited.nextRunAt as number)).toBe("2026-11-10T18:00")
    expect(() => updateJob(db, j.id, { rrule: "FREQ=NEVER" }, NOW)).toThrow(JobError)
    expect(() => updateJob(db, j.id, { prompt: " " }, NOW)).toThrow(JobError)
    expect(deleteJob(db, j.id)).toBe(true)
    expect(getJob(db, j.id)).toBeUndefined()
  })

  it("lists by next run with paused jobs last", () => {
    const { db, chatId } = setup()
    const a = createJob(db, dailyBriefing(chatId, WITA, NOW), NOW)
    const b = createJob(db, heartbeat(chatId, WITA, NOW), NOW)
    const c = createJob(db, { ...dailyBriefing(chatId, WITA, NOW), name: "Paused", enabled: false }, NOW)
    expect(listJobs(db).map((j) => j.id)).toEqual([b.id, a.id, c.id])
    expect(listJobs(db, { enabled: true }).map((j) => j.id)).toEqual([b.id, a.id])
  })

  it("rejects bad input", () => {
    const { db, chatId } = setup()
    expect(() => createJob(db, { name: "x", rrule: "FREQ=DAILY", tz: "Bad/Zone", prompt: "p", chatId }, NOW)).toThrow(JobError)
    expect(() => createJob(db, { name: "x", rrule: "FREQ=DAILY", tz: WITA, prompt: "p", chatId: "nope" }, NOW)).toThrow(JobError)
    expect(() => createJob(db, { name: "x", rrule: "FREQ=DAILY", tz: WITA, prompt: "p", chatId, dailyTokenCap: -1 }, NOW)).toThrow(JobError)
  })
})

function fakeTurn(db: ReturnType<typeof setup>["db"], text: string, tokens = 1000, tokensCached = 0) {
  const calls: Parameters<RunTurn>[0][] = []
  const runTurn: RunTurn = async (o) => {
    calls.push(o)
    const id = newId("run")
    db.insert(runs).values({ id, chatId: o.chatId, jobId: o.jobId, trigger: "schedule", tokensIn: tokens, tokensOut: 0, tokensCached, status: "ok", startedAt: NOW }).run()
    return { runId: id, text, status: "ok" }
  }
  return { runTurn, calls }
}

describe("job runner", () => {
  it("passes the engine options through and records the result", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, heartbeat(chatId, WITA, NOW), NOW)
    const { runTurn, calls } = fakeTurn(db, SILENT_TOKEN, 1200)
    const runner = createJobRunner({ db, log: silentLogger, runTurn, now: () => NOW })
    const res = await runner.runNow(j.id)
    expect(res.ok).toBe(true)
    expect(calls[0]).toEqual({
      chatId,
      trigger: "schedule",
      prompt: j.prompt,
      jobId: j.id,
      toolScope: null,
      modelRole: "fast",
      silentToken: "NOTHING",
    })
    const after = getJob(db, j.id)
    expect(after?.status).toBe("idle")
    expect(after?.lastStatus).toBe("ok")
    expect(after?.lastResult).toBe("NOTHING (nothing sent)")
    expect(after?.lastRunAt).toBe(NOW)
    // Run now leaves the schedule alone, and the runner never writes the outbox.
    expect(after?.nextRunAt).toBe(j.nextRunAt)
    expect(db.select().from(outbox).all()).toHaveLength(0)
    expect(getUsage(db, jobUsageKey(j, NOW))).toBe(1200)
    expect(jobUsageKey(j, NOW)).toBe(`job:${j.id}:2026-11-10`)
  })

  it("counts a run's cache reads at a tenth against the job's daily cap", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, { ...heartbeat(chatId, WITA, NOW), dailyTokenCap: 5000 }, NOW)
    // 20,000 tokens in, 18,000 of them read from the cache: 2,000 + 1,800.
    const { runTurn, calls } = fakeTurn(db, SILENT_TOKEN, 20_000, 18_000)
    const runner = createJobRunner({ db, log: silentLogger, runTurn, now: () => NOW })
    await runner.runNow(j.id)
    expect(getUsage(db, jobUsageKey(j, NOW))).toBe(3800)
    // Still under the 5,000 cap, where the raw 20,000 would have stopped the job for the day.
    expect((await runner.runNow(j.id)).ok).toBe(true)
    expect(calls).toHaveLength(2)
    expect(getUsage(db, jobUsageKey(j, NOW))).toBe(7600)
    expect((await runner.runNow(j.id)).ok).toBe(false)
    expect(getJob(db, j.id)?.lastStatus).toBe("cap")
    expect(calls).toHaveLength(2)
  })

  it("skips with status cap once the daily cap is used up, and resets the next day", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, { ...dailyBriefing(chatId, WITA, NOW), dailyTokenCap: 5000 }, NOW)
    addUsage(db, jobUsageKey(j, NOW), 5000, NOW)
    const { runTurn, calls } = fakeTurn(db, "Pagi!")
    let now = NOW
    const runner = createJobRunner({ db, log: silentLogger, runTurn, now: () => now })
    const res = await runner.runNow(j.id)
    expect(res.ok).toBe(false)
    expect(calls).toHaveLength(0)
    expect(getJob(db, j.id)?.lastStatus).toBe("cap")
    expect(getJob(db, j.id)?.status).toBe("idle")
    now = localToUtc("2026-11-11T07:00", WITA)
    expect((await runner.runNow(j.id)).ok).toBe(true)
    expect(getJob(db, j.id)?.lastResult).toBe("Pagi!")
  })

  it("truncates long results and records errors", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, dailyBriefing(chatId, WITA, NOW), NOW)
    const long = createJobRunner({ db, log: silentLogger, runTurn: fakeTurn(db, "x".repeat(2000)).runTurn, now: () => NOW })
    await long.runNow(j.id)
    expect(getJob(db, j.id)?.lastResult?.length).toBe(500)
    const failing = createJobRunner({
      db,
      log: silentLogger,
      now: () => NOW,
      runTurn: async () => {
        throw new Error("model off")
      },
    })
    const res = await failing.runNow(j.id)
    expect(res).toMatchObject({ ok: false, detail: "model off" })
    expect(getJob(db, j.id)).toMatchObject({ lastStatus: "error", lastResult: "model off", status: "idle" })
  })

  it("refuses Run now while the job is running", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, dailyBriefing(chatId, WITA, NOW), NOW)
    let release: () => void = () => {}
    const runner = createJobRunner({
      db,
      log: silentLogger,
      now: () => NOW,
      runTurn: () =>
        new Promise((resolve) => {
          release = () => resolve({ runId: "run_x", text: "ok", status: "ok" })
        }),
    })
    const first = runner.runNow(j.id)
    expect(await runner.runNow(j.id)).toEqual({ ok: false, detail: "This job is already running." })
    release()
    expect((await first).ok).toBe(true)
  })
})

describe("scheduler with jobs", () => {
  it("claims a due job once, advances next_run_at and hands it to the runner", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, heartbeat(chatId, WITA, NOW), NOW)
    const clock = fakeClock()
    const { runTurn, calls } = fakeTurn(db, SILENT_TOKEN)
    const runner = createJobRunner({ db, log: silentLogger, runTurn, now: clock.now })
    const onJobDue = vi.fn(async (job: Parameters<typeof runner.run>[0]) => {
      await runner.run(job)
    })
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers, onJobDue })
    await s.start()
    expect(onJobDue).not.toHaveBeenCalled()
    await clock.advance(30 * MIN, () => s.idle())
    expect(onJobDue).toHaveBeenCalledTimes(1)
    expect(calls).toHaveLength(1)
    const after = getJob(db, j.id)
    expect(local(after?.nextRunAt as number)).toBe("2026-11-10T11:00")
    expect(after?.status).toBe("idle")
    s.stop()
  })

  it("runs a job that missed several runs during downtime once", async () => {
    const { db, chatId } = setup()
    const j = createJob(db, heartbeat(chatId, WITA, NOW - 5 * HOUR), NOW - 5 * HOUR)
    const onJobDue = vi.fn(async () => {})
    const s = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers, onJobDue })
    await s.start()
    await s.idle()
    await s.sweep()
    expect(onJobDue).toHaveBeenCalledTimes(1)
    expect(local(getJob(db, j.id)?.nextRunAt as number)).toBe("2026-11-10T10:30")
    s.stop()
  })

  it("doesn't claim paused jobs", async () => {
    const { db, chatId } = setup()
    createJob(db, { ...heartbeat(chatId, WITA, NOW - HOUR), enabled: false }, NOW - HOUR)
    const onJobDue = vi.fn(async () => {})
    const s = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers, onJobDue })
    await s.start()
    expect(onJobDue).not.toHaveBeenCalled()
    s.stop()
  })
})
