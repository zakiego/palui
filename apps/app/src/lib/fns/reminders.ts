// Reminders page. Reads the table directly; writes go through the bridge.
import { getOwner } from "@palui/core/owner"
import { findChat } from "@palui/core/chats"
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { DateTime } from "luxon"
import { z } from "zod"
import { cancelReminder, createReminder, snoozeReminder } from "@/server/reminders-bridge"
import { chatOptions, reminderLists } from "@/server/queries"
import { callRuntime } from "@/server/runtime"
import { requireUser, requireWriter } from "@/server/session"

export const getReminders = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const chats = chatOptions(db)
  // Default target: the owner's Telegram DM, then WhatsApp, then the web chat.
  const preferred = (["telegram", "whatsapp"] as const)
    .map((ch) => {
      const owner = getOwner(db, ch)
      return owner ? findChat(db, ch, owner.externalId) : undefined
    })
    .find(Boolean)
  const defaultChatId = preferred?.id ?? chats.find((c) => c.channel === "web")?.id ?? chats[0]?.id ?? null
  return { ...reminderLists(db), chats, defaultChatId, timezone: getSetting(db, "timezone") }
})

export const parseReminder = createServerFn({ method: "POST" })
  .validator(z.object({ text: z.string().trim().min(1).max(500), tz: z.string().max(64).optional() }))
  .handler(async ({ data }) => {
    // A POST that spends model tokens: same-origin check, like every write.
    await requireWriter()
    return callRuntime((rt) => rt.parseReminder(data.text, data.tz))
  })

export const createReminderFn = createServerFn({ method: "POST" })
  .validator(
    z.object({
      chatId: z.string().min(1).max(100),
      text: z.string().trim().min(1).max(500),
      fireAtIso: z.string().min(10).max(40),
      tz: z
        .string()
        .min(1)
        .max(64)
        .refine((tz) => DateTime.local().setZone(tz).isValid, "Use an IANA zone like Asia/Makassar."),
      rrule: z.string().max(300).nullable().optional(),
    }),
  )
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const local = DateTime.fromISO(data.fireAtIso, { setZone: true }).setZone(data.tz)
    if (!local.isValid) throw new Error("That time couldn't be read. Try rephrasing it.")
    const localStart = local.toFormat("yyyy-LL-dd'T'HH:mm:ss")
    const row = createReminder(db, { chatId: data.chatId, text: data.text, localStart, tz: data.tz, rrule: data.rrule ?? null })
    return { id: row.id }
  })

export const cancelReminderFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!cancelReminder(db, data.id)) throw new Error("That reminder doesn't exist anymore.")
    return { ok: true }
  })

export const snoozeReminderFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100), preset: z.enum(["10m", "1h", "tomorrow"]) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const row = snoozeReminder(db, data.id, data.preset)
    if (!row) throw new Error("That reminder doesn't exist anymore.")
    return { fireAt: row.fireAt }
  })
