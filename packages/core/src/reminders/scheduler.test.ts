import { eq } from "drizzle-orm"
import { describe, expect, it, vi } from "vitest"
import { outbox, reminders } from "@palui/db"
import { silentLogger } from "../log"
import { onOutboxEnqueued } from "../outbox/enqueue"
import { createScheduler, MAX_TIMER_MS, reminderMessage } from "./scheduler"
import { createReminder, getReminder, snoozeReminder } from "./service"
import { fakeClock, HOUR, MIN, NOW, setup, WITA } from "./test-helpers"
import { localToUtc, utcToLocal } from "./time"

const DAY = 24 * HOUR

function outboxRows(db: ReturnType<typeof setup>["db"]) {
  return db.select().from(outbox).all()
}

describe("firing plain reminders", () => {
  it("enqueues the text with actions, marks a one-off done and notifies the worker", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "bayar listrik", localStart: "2026-11-10T10:05", tz: WITA }, NOW)
    const clock = fakeClock()
    const notified = vi.fn()
    const off = onOutboxEnqueued(notified)
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    expect(outboxRows(db)).toHaveLength(0)
    await clock.advance(5 * MIN)
    const rows = outboxRows(db)
    expect(rows).toHaveLength(1)
    const row = rows[0] as (typeof rows)[number]
    expect(row.source).toBe(`reminder:${r.id}`)
    expect(row.channel).toBe("telegram")
    expect(row.expiresAt).toBe(NOW + 5 * MIN + DAY)
    expect(row.payload.text).toBe("⏰ bayar listrik")
    expect(row.payload.actions).toEqual([
      { id: `rem:done:${r.id}`, label: "Done" },
      { id: `rem:snooze:${r.id}:10m`, label: "10m" },
      { id: `rem:snooze:${r.id}:1h`, label: "1j" },
      { id: `rem:snooze:${r.id}:tomorrow`, label: "Besok 09.00" },
    ])
    const after = getReminder(db, r.id)
    expect(after?.status).toBe("done")
    expect(after?.lastOutboxId).toBe(row.id)
    expect(after?.lastFiredAt).toBe(NOW + 5 * MIN)
    expect(notified).toHaveBeenCalled()
    off()
    s.stop()
  })

  it("moves a series to its next occurrence", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "minum obat", localStart: "2026-11-10T10:01", tz: WITA, rrule: "FREQ=DAILY" }, NOW)
    const clock = fakeClock()
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    await clock.advance(MIN)
    const after = getReminder(db, r.id)
    expect(after?.status).toBe("pending")
    expect(utcToLocal(after?.fireAt as number, WITA)).toBe("2026-11-11T10:01:00")
    expect(after?.scheduledFor).toBe(after?.fireAt)
    s.stop()
  })

  it("uses the chat's channel", async () => {
    const { db, otherChatId } = setup()
    createReminder(db, { chatId: otherChatId, text: "x", localStart: "2026-11-10T10:00", tz: WITA }, NOW)
    const s = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
    await s.start()
    expect(outboxRows(db)[0]?.channel).toBe("whatsapp")
    s.stop()
  })
})

describe("restart with overdue reminders", () => {
  it("fires an overdue one-off once with the telat note", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "bayar listrik", localStart: "2026-11-10T09:00", tz: WITA }, NOW - 2 * HOUR)
    const clock = fakeClock()
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    await clock.advance(5 * MIN)
    const rows = outboxRows(db)
    expect(rows).toHaveLength(1)
    expect(rows[0]?.payload.text).toBe("⏰ bayar listrik (telat, harusnya 09.00)")
    expect(getReminder(db, r.id)?.status).toBe("done")
    s.stop()
  })

  it("fires a series that missed several runs once, then resumes on schedule", async () => {
    const { db, chatId } = setup()
    // Created Saturday for daily 08:00; the process was down until Tuesday 10:00.
    const r = createReminder(db, { chatId, text: "jalan pagi", localStart: "2026-11-07T08:00", tz: WITA, rrule: "FREQ=DAILY" }, localToUtc("2026-11-07T07:00", WITA))
    const clock = fakeClock()
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    await clock.advance(10 * MIN)
    const rows = outboxRows(db)
    expect(rows).toHaveLength(1)
    // The note names the latest missed run (today 08:00), not Saturday's.
    expect(rows[0]?.payload.text).toBe("⏰ jalan pagi (telat, harusnya 08.00)")
    expect(utcToLocal(getReminder(db, r.id)?.fireAt as number, WITA)).toBe("2026-11-11T08:00:00")
    s.stop()
  })

  it("shows the date when the missed run was on another day", () => {
    const fireAt = localToUtc("2026-11-08T21:00", WITA)
    expect(reminderMessage({ text: "x", fireAt, tz: WITA, rrule: null, localStart: "2026-11-08T21:00:00", snoozedUntil: null }, NOW)).toBe(
      "⏰ x (telat, harusnya Minggu 8 Nov 21.00)",
    )
  })

  it("doesn't add the note within two minutes", () => {
    expect(reminderMessage({ text: "x", fireAt: NOW - 2 * MIN, tz: WITA, rrule: null, localStart: "", snoozedUntil: null }, NOW)).toBe("⏰ x")
  })

  it("recovers rows a crash left in firing", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "x", localStart: "2026-11-10T10:00", tz: WITA }, NOW)
    db.update(reminders).set({ status: "firing" }).where(eq(reminders.id, r.id)).run()
    const s = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
    await s.start()
    expect(outboxRows(db)).toHaveLength(1)
    s.stop()
  })
})

describe("claim idempotency", () => {
  it("fires once when two sweeps race", async () => {
    const { db, chatId } = setup()
    for (let i = 0; i < 5; i++) createReminder(db, { chatId, text: `r${i}`, localStart: "2026-11-10T10:00", tz: WITA }, NOW)
    const a = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
    const b = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
    const [ra, rb] = await Promise.all([a.sweep(), b.sweep(), a.sweep()])
    expect((ra?.reminders ?? 0) + (rb?.reminders ?? 0)).toBe(5)
    expect(outboxRows(db)).toHaveLength(5)
    await a.sweep()
    expect(outboxRows(db)).toHaveLength(5)
  })
})

describe("timer arming", () => {
  it("caps the delay at 60 s for a reminder 25 days out and doesn't spin", async () => {
    const { db, chatId } = setup()
    createReminder(db, { chatId, text: "jauh", localStart: "2026-12-05T10:00", tz: WITA }, NOW)
    const clock = fakeClock()
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    expect(clock.armed.at(-1)).toBe(MAX_TIMER_MS)
    const ran = await clock.advance(10 * MIN)
    // 10 timer ticks plus 20 safety sweeps, nothing like a 1 ms loop.
    expect(ran).toBeLessThanOrEqual(31)
    expect(clock.armed.every((ms) => ms === MAX_TIMER_MS)).toBe(true)
    expect(outboxRows(db)).toHaveLength(0)
    s.stop()
  })

  it("arms for the exact delay when the next fire is closer than 60 s", async () => {
    const { db, chatId } = setup()
    createReminder(db, { chatId, text: "x", localStart: "2026-11-10T10:00:20", tz: WITA }, NOW)
    const clock = fakeClock()
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    expect(clock.armed.at(-1)).toBe(20_000)
    s.stop()
  })

  it("re-arms when a reminder is created or snoozed (poke)", async () => {
    const { db, chatId } = setup()
    const clock = fakeClock()
    const s = createScheduler({ db, log: silentLogger, now: clock.now, timers: clock.timers })
    await s.start()
    expect(clock.armed.at(-1)).toBe(MAX_TIMER_MS)
    const r = createReminder(db, { chatId, text: "x", localStart: "2026-11-10T10:00:05", tz: WITA }, NOW)
    await vi.waitFor(() => expect(clock.armed.at(-1)).toBe(5000))
    snoozeReminder(db, r.id, { until: NOW + 3000 }, NOW)
    await vi.waitFor(() => expect(clock.armed.at(-1)).toBe(3000))
    s.stop()
    expect(clock.pending.size).toBe(0)
  })
})

describe("smart reminders", () => {
  it("calls onSmartReminder instead of enqueueing text", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "cek cuaca", localStart: "2026-11-10T10:00", tz: WITA, smartPrompt: "Cek cuaca Banjarbaru" }, NOW)
    const onSmartReminder = vi.fn(async () => {})
    const s = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers, onSmartReminder })
    await s.start()
    await s.idle()
    expect(onSmartReminder).toHaveBeenCalledWith(expect.objectContaining({ id: r.id, smartPrompt: "Cek cuaca Banjarbaru" }))
    expect(outboxRows(db)).toHaveLength(0)
    expect(getReminder(db, r.id)?.status).toBe("done")
    s.stop()
  })

  it("falls back to the plain text when the smart run fails", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "cek cuaca", localStart: "2026-11-10T10:00", tz: WITA, smartPrompt: "x" }, NOW)
    const s = createScheduler({
      db,
      log: silentLogger,
      now: () => NOW,
      timers: fakeClock().timers,
      onSmartReminder: async () => {
        throw new Error("model off")
      },
    })
    await s.start()
    await s.idle()
    expect(outboxRows(db)[0]?.payload.text).toBe("⏰ cek cuaca")
    expect(getReminder(db, r.id)?.status).toBe("done")
    s.stop()
  })
})
