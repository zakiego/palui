import { describe, expect, it } from "vitest"
import { HOUR, MIN, NOW, setup, WITA } from "./test-helpers"
import {
  cancelReminder,
  createReminder,
  getReminder,
  listReminders,
  markDone,
  nextReminders,
  ReminderError,
  reminderEcho,
  snoozeReminder,
  updateReminder,
} from "./service"
import { utcToLocal } from "./time"

describe("createReminder", () => {
  it("computes fire_at in the zone and echoes it", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "bayar listrik", localStart: "2026-11-11T09:00", tz: WITA }, NOW)
    expect(new Date(r.fireAt).toISOString()).toBe("2026-11-11T01:00:00.000Z")
    expect(r.scheduledFor).toBe(r.fireAt)
    expect(r.localStart).toBe("2026-11-11T09:00:00")
    expect(r.status).toBe("pending")
    expect(reminderEcho(r, NOW)).toBe("Besok, Rabu 11 Nov, 09.00 WITA")
  })

  it("normalizes the rrule and starts a series at its first matching occurrence", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "kirim laporan", localStart: "2026-11-10T08:00", tz: WITA, rrule: "BYDAY=MO;FREQ=WEEKLY" }, NOW)
    expect(r.rrule).toBe("FREQ=WEEKLY;BYDAY=MO")
    expect(utcToLocal(r.fireAt, WITA)).toBe("2026-11-16T08:00:00")
    expect(reminderEcho(r, NOW)).toBe("Tiap Senin, 08.00 WITA")
  })

  it("rejects a one-off in the past, bad zones, bad rules and empty text", () => {
    const { db, chatId } = setup()
    expect(() => createReminder(db, { chatId, text: "x", localStart: "2026-11-09T09:00", tz: WITA }, NOW)).toThrow(/already passed/)
    expect(() => createReminder(db, { chatId, text: "x", localStart: "2030-01-01T09:00", tz: WITA }, NOW)).toThrow(/2 years/)
    expect(() => createReminder(db, { chatId, text: "x", localStart: "2026-11-11T09:00", tz: "Nowhere/City" }, NOW)).toThrow(ReminderError)
    expect(() => createReminder(db, { chatId, text: "x", localStart: "2026-11-11T09:00", tz: WITA, rrule: "FREQ=NOPE" }, NOW)).toThrow(ReminderError)
    expect(() => createReminder(db, { chatId, text: "  ", localStart: "2026-11-11T09:00", tz: WITA }, NOW)).toThrow(ReminderError)
    expect(() => createReminder(db, { chatId: "chat_missing", text: "x", localStart: "2026-11-11T09:00", tz: WITA }, NOW)).toThrow(ReminderError)
  })

  it("rolls a one-off earlier today to tomorrow, like the parser (the set_reminder tool path)", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "cek email", localStart: "2026-11-10T09:00", tz: WITA }, NOW)
    expect(r.localStart).toBe("2026-11-11T09:00:00")
    expect(utcToLocal(r.fireAt, WITA)).toBe("2026-11-11T09:00:00")
    expect(reminderEcho(r, NOW)).toBe("Besok, Rabu 11 Nov, 09.00 WITA")
  })

  it("accepts a one-off due right now", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "x", localStart: "2026-11-10T10:00", tz: WITA }, NOW)
    expect(r.fireAt).toBe(NOW)
  })
})

describe("list and next", () => {
  it("filters upcoming, recurring and done, per chat", () => {
    const { db, chatId, otherChatId } = setup()
    const a = createReminder(db, { chatId, text: "a", localStart: "2026-11-12T09:00", tz: WITA }, NOW)
    const b = createReminder(db, { chatId, text: "b", localStart: "2026-11-11T09:00", tz: WITA, rrule: "FREQ=DAILY" }, NOW)
    const c = createReminder(db, { chatId: otherChatId, text: "c", localStart: "2026-11-10T11:00", tz: WITA }, NOW)
    const d = createReminder(db, { chatId, text: "d", localStart: "2026-11-13T09:00", tz: WITA }, NOW)
    cancelReminder(db, d.id, NOW + 1)
    expect(listReminders(db, { filter: "upcoming", chatId }).map((r) => r.id)).toEqual([b.id, a.id])
    expect(listReminders(db, { filter: "upcoming" }).map((r) => r.id)).toEqual([c.id, b.id, a.id])
    expect(listReminders(db, { filter: "recurring" }).map((r) => r.id)).toEqual([b.id])
    expect(listReminders(db, { filter: "done", chatId }).map((r) => r.id)).toEqual([d.id])
    expect(nextReminders(db, 2, NOW).map((r) => r.id)).toEqual([c.id, b.id])
  })
})

describe("update", () => {
  it("reschedules on a time change and revives a done one-off", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "a", localStart: "2026-11-10T11:00", tz: WITA }, NOW)
    markDone(db, r.id, NOW)
    const u = updateReminder(db, r.id, { localStart: "2026-11-12T07:30", text: "b" }, NOW)
    expect(u.status).toBe("pending")
    expect(u.text).toBe("b")
    expect(utcToLocal(u.fireAt, WITA)).toBe("2026-11-12T07:30:00")
    const t = updateReminder(db, r.id, { tz: "Asia/Jakarta" }, NOW)
    expect(utcToLocal(t.fireAt, "Asia/Jakarta")).toBe("2026-11-12T07:30:00")
    expect(t.fireAt - u.fireAt).toBe(HOUR)
  })

  it("leaves the schedule alone on a text-only edit and refuses cancelled ones", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "a", localStart: "2026-11-11T09:00", tz: WITA }, NOW)
    expect(updateReminder(db, r.id, { text: "b" }, NOW).fireAt).toBe(r.fireAt)
    cancelReminder(db, r.id, NOW)
    expect(() => updateReminder(db, r.id, { text: "c" }, NOW)).toThrow(ReminderError)
  })
})

describe("snooze", () => {
  it("supports 10m, 1h and an explicit time", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "a", localStart: "2026-11-10T10:00", tz: WITA }, NOW)
    expect(snoozeReminder(db, r.id, "10m", NOW).fireAt).toBe(NOW + 10 * MIN)
    expect(snoozeReminder(db, r.id, "1h", NOW).fireAt).toBe(NOW + HOUR)
    expect(snoozeReminder(db, r.id, { until: NOW + 5 * MIN }, NOW).fireAt).toBe(NOW + 5 * MIN)
    expect(() => snoozeReminder(db, r.id, { until: NOW - 1 }, NOW)).toThrow(ReminderError)
  })

  it("snoozes to tomorrow 09:00 in the reminder's own zone", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "a", localStart: "2026-11-10T22:00", tz: "America/New_York" }, NOW)
    const s = snoozeReminder(db, r.id, "tomorrow", NOW)
    // NOW is Monday 21:00 in New York, so "tomorrow" is Tuesday there.
    expect(utcToLocal(s.fireAt, "America/New_York")).toBe("2026-11-10T09:00:00")
    const w = createReminder(db, { chatId, text: "b", localStart: "2026-11-10T22:00", tz: WITA }, NOW)
    expect(utcToLocal(snoozeReminder(db, w.id, "tomorrow", NOW).fireAt, WITA)).toBe("2026-11-11T09:00:00")
  })

  it("revives a done one-off and keeps a series' rule", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "a", localStart: "2026-11-10T11:00", tz: WITA }, NOW)
    markDone(db, r.id, NOW)
    expect(snoozeReminder(db, r.id, "10m", NOW).status).toBe("pending")
    const s = createReminder(db, { chatId, text: "s", localStart: "2026-11-11T09:00", tz: WITA, rrule: "FREQ=DAILY" }, NOW)
    const snoozed = snoozeReminder(db, s.id, "1h", NOW)
    expect(snoozed.rrule).toBe("FREQ=DAILY")
    expect(snoozed.snoozedUntil).toBe(NOW + HOUR)
  })
})

describe("markDone", () => {
  it("finishes a one-off", () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "a", localStart: "2026-11-11T09:00", tz: WITA }, NOW)
    expect(markDone(db, r.id, NOW).status).toBe("done")
  })

  it("only acknowledges a series, and drops a pending snooze", () => {
    const { db, chatId } = setup()
    const s = createReminder(db, { chatId, text: "s", localStart: "2026-11-11T09:00", tz: WITA, rrule: "FREQ=DAILY" }, NOW)
    const acked = markDone(db, s.id, NOW)
    expect(acked.status).toBe("pending")
    expect(acked.fireAt).toBe(s.fireAt)
    snoozeReminder(db, s.id, "10m", NOW)
    const back = markDone(db, s.id, NOW + MIN)
    expect(back.snoozedUntil).toBeNull()
    expect(utcToLocal(back.fireAt, WITA)).toBe("2026-11-11T09:00:00")
    expect(getReminder(db, s.id)?.status).toBe("pending")
  })
})
