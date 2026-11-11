import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { outbox } from "@palui/db"
import { silentLogger } from "../log"
import { enqueueOutbox } from "../outbox/enqueue"
import { parseActionText, reminderActions } from "./actions"
import { createScheduler } from "./scheduler"
import { cancelReminder, createReminder, getReminder } from "./service"
import { fakeClock, HOUR, MIN, NOW, setup, WITA } from "./test-helpers"
import { utcToLocal } from "./time"

function oneOff(db: ReturnType<typeof setup>["db"], chatId: string, at = "2026-11-10T10:00") {
  return createReminder(db, { chatId, text: "bayar listrik", localStart: at, tz: WITA }, NOW)
}

describe("fromCallback", () => {
  it("marks done", async () => {
    const { db, chatId } = setup()
    const r = oneOff(db, chatId)
    expect(await reminderActions.fromCallback(db, `rem:done:${r.id}`, chatId, NOW)).toBe("Sip, ditandai selesai.")
    expect(getReminder(db, r.id)?.status).toBe("done")
  })

  it("snoozes with each preset", async () => {
    const { db, chatId } = setup()
    const r = oneOff(db, chatId)
    expect(await reminderActions.fromCallback(db, `rem:snooze:${r.id}:10m`, chatId, NOW)).toBe("Oke, ditunda sampai 10.10 WITA.")
    expect(getReminder(db, r.id)?.fireAt).toBe(NOW + 10 * MIN)
    expect(await reminderActions.fromCallback(db, `rem:snooze:${r.id}:1h`, chatId, NOW)).toBe("Oke, ditunda sampai 11.00 WITA.")
    expect(await reminderActions.fromCallback(db, `rem:snooze:${r.id}:tomorrow`, chatId, NOW)).toBe("Oke, ditunda sampai Besok, Rabu 11 Nov, 09.00 WITA.")
    expect(utcToLocal(getReminder(db, r.id)?.fireAt as number, WITA)).toBe("2026-11-11T09:00:00")
  })

  it("says when the next run is after Done on a series", async () => {
    const { db, chatId } = setup()
    const r = createReminder(db, { chatId, text: "x", localStart: "2026-11-16T08:00", tz: WITA, rrule: "FREQ=WEEKLY;BYDAY=MO" }, NOW)
    expect(await reminderActions.fromCallback(db, `rem:done:${r.id}`, chatId, NOW)).toBe("Sip, ditandai selesai. Berikutnya Senin 16 Nov, 08.00 WITA.")
  })

  it("ignores other callbacks and refuses another chat's reminder", async () => {
    const { db, chatId, otherChatId } = setup()
    const r = oneOff(db, chatId)
    expect(await reminderActions.fromCallback(db, "approve:abc", chatId, NOW)).toBeNull()
    expect(await reminderActions.fromCallback(db, `rem:snooze:${r.id}:2d`, chatId, NOW)).toBeNull()
    expect(await reminderActions.fromCallback(db, `rem:done:${r.id}`, otherChatId, NOW)).toBe("Pengingat itu nggak ketemu di chat ini.")
    expect(getReminder(db, r.id)?.status).toBe("pending")
    cancelReminder(db, r.id, NOW)
    expect(await reminderActions.fromCallback(db, `rem:done:${r.id}`, chatId, NOW)).toBe("Pengingat itu udah dibatalin.")
  })
})

describe("fromText (WhatsApp quoting)", () => {
  async function fired(chatKey: "chatId" | "otherChatId" = "otherChatId") {
    const ctx = setup()
    const chatId = ctx[chatKey]
    const r = oneOff(ctx.db, chatId)
    const s = createScheduler({ db: ctx.db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
    await s.sweep()
    // The outbox worker stores the channel's message id after the send acks.
    ctx.db.update(outbox).set({ status: "sent", channelMsgId: "WAMSG1" }).where(eq(outbox.source, `reminder:${r.id}`)).run()
    return { ...ctx, chatId, r }
  }

  it("handles done words when the reply quotes the reminder", async () => {
    for (const word of ["done", "Selesai", "udah", "udah ya", "beres!"]) {
      const { db, chatId, r } = await fired()
      expect(await reminderActions.fromText(db, word, "WAMSG1", chatId, NOW)).toBe("Sip, ditandai selesai.")
      expect(getReminder(db, r.id)?.status).toBe("done")
    }
  })

  it("handles snooze phrasings", async () => {
    const cases: [string, number][] = [
      ["tunda 10m", 10 * MIN],
      ["tunda", 10 * MIN],
      ["tunda 1j", HOUR],
      ["tunda 1 jam", HOUR],
      ["snooze 1h", HOUR],
      ["Snooze 10 min", 10 * MIN],
    ]
    for (const [text, delta] of cases) {
      const { db, chatId, r } = await fired()
      expect(await reminderActions.fromText(db, text, "WAMSG1", chatId, NOW)).toMatch(/^Oke, ditunda sampai/)
      expect(getReminder(db, r.id)?.fireAt).toBe(NOW + delta)
    }
    const { db, chatId, r } = await fired()
    expect(await reminderActions.fromText(db, "tunda besok", "WAMSG1", chatId, NOW)).toBe("Oke, ditunda sampai Besok, Rabu 11 Nov, 09.00 WITA.")
    expect(utcToLocal(getReminder(db, r.id)?.fireAt as number, WITA)).toBe("2026-11-11T09:00:00")
  })

  it("returns null without a quote, for unknown quotes and for other text", async () => {
    const { db, chatId } = await fired()
    expect(await reminderActions.fromText(db, "done", undefined, chatId, NOW)).toBeNull()
    expect(await reminderActions.fromText(db, "done", "OTHER", chatId, NOW)).toBeNull()
    expect(await reminderActions.fromText(db, "makasih, nanti aku bayar", "WAMSG1", chatId, NOW)).toBeNull()
    expect(await reminderActions.fromText(db, "tunda 3 hari", "WAMSG1", chatId, NOW)).toBeNull()
  })

  it("ignores quotes of non-reminder messages", async () => {
    const { db, chatId } = setup()
    const row = enqueueOutbox(db, { chatId, channel: "telegram", payload: { text: "hi" }, source: "reply:run_1" }, NOW)
    db.update(outbox).set({ channelMsgId: "M9" }).where(eq(outbox.id, row.id)).run()
    expect(await reminderActions.fromText(db, "done", "M9", chatId, NOW)).toBeNull()
  })

  it("refuses a reminder from another chat", async () => {
    const { db, r } = await fired("otherChatId")
    const before = getReminder(db, r.id)
    expect(await reminderActions.fromText(db, "tunda 1j", "WAMSG1", "chat_elsewhere", NOW)).toBe("Pengingat itu nggak ketemu di chat ini.")
    expect(getReminder(db, r.id)).toEqual(before)
  })
})

describe("actions on a fallback-delivered reminder", () => {
  it("accepts Done and Snooze from the chat the outbox row was moved to", async () => {
    const { db, chatId, otherChatId } = setup()
    const r = oneOff(db, chatId)
    const s = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
    await s.sweep()
    // The outbox worker moved the row to the fallback channel's chat and sent it there.
    db.update(outbox).set({ status: "sent", chatId: otherChatId, fallbackFrom: "telegram", channelMsgId: "WAFB1" }).where(eq(outbox.source, `reminder:${r.id}`)).run()
    expect(await reminderActions.fromText(db, "tunda 1j", "WAFB1", otherChatId, NOW)).toMatch(/^Oke, ditunda sampai/)
    expect(await reminderActions.fromCallback(db, `rem:done:${r.id}`, otherChatId, NOW)).toBe("Sip, ditandai selesai.")
    expect(getReminder(db, r.id)?.status).toBe("done")
  })

  it("still refuses a chat that never got the reminder", async () => {
    const { db, chatId, otherChatId } = setup()
    const r = oneOff(db, chatId)
    expect(await reminderActions.fromCallback(db, `rem:done:${r.id}`, otherChatId, NOW)).toBe("Pengingat itu nggak ketemu di chat ini.")
  })
})

describe("parseActionText", () => {
  it("parses and rejects", () => {
    expect(parseActionText("tunda 1 jam lagi")).toEqual({ kind: "snooze", preset: "1h" })
    expect(parseActionText("snooze tomorrow")).toEqual({ kind: "snooze", preset: "tomorrow" })
    expect(parseActionText("tunda sampai besok")).toEqual({ kind: "snooze", preset: "tomorrow" })
    expect(parseActionText("udah bayar listrik kemarin sih")).toBeNull()
    expect(parseActionText("")).toBeNull()
  })
})
