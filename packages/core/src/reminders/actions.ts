// Reminder actions the router handles before the model (PLAN 6, 8.2):
//   fromCallback  Telegram buttons: "rem:done:<id>", "rem:snooze:<id>:<10m|1h|tomorrow>"
//   fromText      WhatsApp replies that quote a reminder: "done", "tunda 1j"...
// A non-null result is the reply to send; null means "not mine, carry on".
import { and, eq } from "drizzle-orm"
import { outbox, type DB } from "@palui/db"
import { getReminder, markDone, snoozeReminder, ReminderError, type ReminderRow, type SnoozePreset } from "./service"
import { formatWhen } from "./time"

type Preset = "10m" | "1h" | "tomorrow"

const CALLBACK_RE = /^rem:(done|snooze):([A-Za-z0-9_-]+)(?::(10m|1h|tomorrow))?$/

const DONE_WORDS = new Set([
  "done",
  "selesai",
  "udah",
  "udh",
  "sudah",
  "dah",
  "beres",
  "ok done",
  "oke done",
  "sip",
  "siap",
  "ok",
  "oke",
  "okay",
  "✅",
  "👍",
  "kelar",
])

// Text after "tunda"/"snooze" -> preset. Accepts "10m", "10 menit", "1j", "1 jam",
// "1h", "1 hour", "besok", "tomorrow".
function parsePreset(rest: string): Preset | null {
  const r = rest.replace(/\s+/g, " ").trim()
  if (!r || /^(10 ?(m|mnt|menit|min|mins|minutes?))$/.test(r)) return "10m"
  if (/^(1 ?(j|jam|h|hr|hour)|sejam|satu jam|an hour|one hour)$/.test(r)) return "1h"
  if (/^(besok|bsk|besok pagi|tomorrow|tmr|tmrw)( (jam )?(09|9)([.:]00)?)?$/.test(r)) return "tomorrow"
  return null
}

type TextAction = { kind: "done" } | { kind: "snooze"; preset: Preset }

export function parseActionText(text: string): TextAction | null {
  const t = text
    .toLowerCase()
    .replace(/[!.,?]+$/g, "")
    .replace(/\s+/g, " ")
    .trim()
  if (!t) return null
  if (DONE_WORDS.has(t) || /^(udah|sudah|dah|done|selesai|beres|kelar)( (ya|yaa|kok|deh|dong|bro|kak|nih))?$/.test(t)) return { kind: "done" }
  const m = /^(tunda|snooze|nanti|ingetin lagi|remind me again|later)(?: (?:in|dalam|lagi|sampai|ke))? ?(.*)$/.exec(t)
  if (m) {
    const preset = parsePreset((m[2] ?? "").replace(/ lagi$/, ""))
    if (preset) return { kind: "snooze", preset }
  }
  return null
}

function doneReply(r: ReminderRow, now: number): string {
  if (!r.rrule) return "Sip, ditandai selesai."
  return `Sip, ditandai selesai. Berikutnya ${formatWhen(r.fireAt, r.tz, now)}.`
}

function snoozeReply(r: ReminderRow, now: number): string {
  const when = formatWhen(r.fireAt, r.tz, now).replace(/^Hari ini, /, "")
  return `Oke, ditunda sampai ${when}.`
}

// The owner may act from the reminder's own chat, or from the chat a copy of it was
// actually delivered to: after 2 failed attempts the outbox row moves to the fallback
// channel's chat, and the Done/Snooze buttons there carry the same reminder id.
function deliveredTo(db: DB, id: string, chatId: string): boolean {
  return db
    .select({ id: outbox.id })
    .from(outbox)
    .where(and(eq(outbox.source, `reminder:${id}`), eq(outbox.chatId, chatId)))
    .limit(1)
    .all().length > 0
}

function apply(db: DB, id: string, action: TextAction, chatId: string, now: number): string {
  const r = getReminder(db, id)
  if (!r || (r.chatId !== chatId && !deliveredTo(db, id, chatId))) return "Pengingat itu nggak ketemu di chat ini."
  if (r.status === "cancelled") return "Pengingat itu udah dibatalin."
  try {
    if (action.kind === "done") return doneReply(markDone(db, id, now), now)
    return snoozeReply(snoozeReminder(db, id, action.preset as SnoozePreset, now), now)
  } catch (e) {
    if (e instanceof ReminderError) return `Gagal: ${e.message}`
    throw e
  }
}

export const reminderActions = {
  async fromCallback(db: DB, data: string, chatId: string, now: number = Date.now()): Promise<string | null> {
    const m = CALLBACK_RE.exec(data)
    if (!m) return null
    const [, verb, id, preset] = m as unknown as [string, "done" | "snooze", string, Preset | undefined]
    if (verb === "snooze" && !preset) return null
    return apply(db, id, verb === "done" ? { kind: "done" } : { kind: "snooze", preset: preset as Preset }, chatId, now)
  },

  async fromText(db: DB, text: string, replyToChannelMsgId: string | undefined, chatId: string, now: number = Date.now()): Promise<string | null> {
    if (!replyToChannelMsgId) return null
    const action = parseActionText(text)
    if (!action) return null
    const row = db.select({ source: outbox.source, chatId: outbox.chatId }).from(outbox).where(eq(outbox.channelMsgId, replyToChannelMsgId)).all()
    // channel_msg_id is unique per channel chat, not globally: prefer this chat's row.
    const hit = row.find((o) => o.chatId === chatId && o.source.startsWith("reminder:")) ?? row.find((o) => o.source.startsWith("reminder:"))
    if (!hit) return null
    return apply(db, hit.source.slice("reminder:".length), action, chatId, now)
  },
}
