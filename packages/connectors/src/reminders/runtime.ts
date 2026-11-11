// Reminder tools for the model (PLAN 6). Every tool works on the turn's chat only;
// results carry the Indonesian echo so the model can repeat the time back.
import { tool } from "ai"
import { z } from "zod"
import type { DB } from "@palui/db"
import { defineRuntime, type PaluiTool, type TurnInfo } from "@palui/core/saklar/define"
import {
  cancelReminder,
  createReminder,
  getReminder,
  listReminders,
  ReminderError,
  reminderEcho,
  reminderNext,
  snoozeReminder,
  updateReminder,
  type ReminderRow,
} from "@palui/core/reminders/service"
import { isValidZone, localToUtc, TimeError } from "@palui/core/reminders/time"
import { getSetting } from "@palui/core/settings"
import manifest from "./manifest"

type Fail = { ok: false; error: string }

function fail(e: unknown): Fail {
  if (e instanceof ReminderError || e instanceof TimeError) return { ok: false, error: e.message }
  throw e
}

function view(r: ReminderRow, now: number) {
  return {
    id: r.id,
    text: r.text,
    status: r.status,
    recurring: !!r.rrule,
    rrule: r.rrule,
    tz: r.tz,
    when: reminderEcho(r, now),
    next: r.status === "pending" || r.status === "firing" ? reminderNext(r, now) : null,
    smart: !!r.smartPrompt,
  }
}

function zoneFor(db: DB, turn: TurnInfo, tz?: string | null): string {
  if (tz) return tz
  if (turn.tz && isValidZone(turn.tz)) return turn.tz
  return getSetting(db, "timezone")
}

function ownReminder(db: DB, id: string, chatId: string): ReminderRow | Fail {
  const r = getReminder(db, id)
  if (!r || r.chatId !== chatId) return { ok: false, error: `No reminder ${id} in this chat. Call daftar_pengingat to get the ids.` }
  return r
}

const localDateTime = z
  .string()
  .regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/, "Use YYYY-MM-DDTHH:mm with no offset")
  .describe("Wall-clock date and time in the reminder's zone, ISO 8601 without offset (YYYY-MM-DDTHH:mm). For a repeating reminder, the first occurrence.")

const rrule = z
  .string()
  .nullable()
  .optional()
  .describe(
    'RFC 5545 RRULE body without DTSTART for repeating reminders: "FREQ=DAILY", "FREQ=WEEKLY;BYDAY=MO", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "FREQ=MONTHLY;BYMONTHDAY=1", "FREQ=MONTHLY;BYDAY=-1FR" (last Friday). Omit or null for a one-off.',
  )

const tz = z.string().optional().describe("IANA time zone, e.g. Asia/Makassar. Omit to use the owner's zone.")

export function reminderTools(db: DB, turn: TurnInfo, clock: () => number = () => Date.now()): Record<string, PaluiTool> {
  const chatId = turn.chatId
  return {
    ingatkan: {
      class: "write",
      title: "Set a reminder",
      tool: tool({
        description:
          "Create a reminder in this chat. Work out the local date and time yourself from the current time in the system prompt (a date with no time means 09:00). Repeat the returned `echo` to the user so a wrong time shows right away.",
        inputSchema: z.object({
          text: z.string().min(1).max(500).describe("What to remind about, short, in the user's language."),
          localDateTime,
          rrule,
          tz,
        }),
        execute: async (input) => {
          const now = clock()
          try {
            const r = createReminder(db, { chatId, text: input.text, localStart: input.localDateTime, tz: zoneFor(db, turn, input.tz), rrule: input.rrule ?? null }, now)
            const echo = reminderEcho(r, now)
            const first = r.rrule ? ` Pertama: ${reminderNext(r, now)}.` : ""
            return { ok: true as const, id: r.id, echo, message: `Oke, aku ingetin "${r.text}": ${echo}.${first}`, reminder: view(r, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    daftar_pengingat: {
      class: "read",
      title: "List reminders",
      tool: tool({
        description: "List this chat's reminders. upcoming = pending by next fire time, recurring = repeating ones, done = finished or cancelled (latest first).",
        inputSchema: z.object({
          filter: z.enum(["upcoming", "recurring", "done"]).default("upcoming"),
          limit: z.number().int().min(1).max(50).default(20),
        }),
        execute: async ({ filter, limit }) => {
          const now = clock()
          const rows = listReminders(db, { filter, chatId, limit })
          return {
            ok: true as const,
            count: rows.length,
            reminders: rows.map((r) => view(r, now)),
            message: rows.length ? undefined : filter === "done" ? "Belum ada pengingat yang selesai." : "Nggak ada pengingat aktif.",
          }
        },
      }),
    },

    ubah_pengingat: {
      class: "write",
      title: "Change a reminder",
      tool: tool({
        description: "Change a reminder's text, time, repeat rule or zone. Changing the time reschedules it. Pass rrule null to make it a one-off.",
        inputSchema: z.object({
          id: z.string().describe("Reminder id from daftar_pengingat."),
          text: z.string().min(1).max(500).optional(),
          localDateTime: localDateTime.optional(),
          rrule,
          tz,
        }),
        execute: async (input) => {
          const now = clock()
          const own = ownReminder(db, input.id, chatId)
          if ("ok" in own) return own
          try {
            const r = updateReminder(
              db,
              input.id,
              {
                text: input.text,
                localStart: input.localDateTime,
                rrule: input.rrule,
                tz: input.tz,
              },
              now,
            )
            const echo = reminderEcho(r, now)
            return { ok: true as const, echo, message: `Sip, pengingat "${r.text}" sekarang ${echo}.`, reminder: view(r, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    batal_pengingat: {
      class: "write",
      title: "Cancel a reminder",
      tool: tool({
        description: "Cancel a reminder (a repeating one stops for good). It stays in the done list.",
        inputSchema: z.object({ id: z.string().describe("Reminder id from daftar_pengingat.") }),
        execute: async ({ id }) => {
          const now = clock()
          const own = ownReminder(db, id, chatId)
          if ("ok" in own) return own
          if (own.status === "cancelled") return { ok: true as const, message: `Pengingat "${own.text}" memang udah dibatalin.` }
          const r = cancelReminder(db, id, now)
          return { ok: true as const, message: `Oke, pengingat "${r.text}" dibatalin.`, reminder: view(r, now) }
        },
      }),
    },

    tunda_pengingat: {
      class: "write",
      title: "Snooze a reminder",
      tool: tool({
        description:
          "Snooze a reminder: preset 10m, 1h or tomorrow (tomorrow 09:00 in the reminder's zone), or an exact localDateTime. A repeating reminder keeps its rule afterwards.",
        inputSchema: z.object({
          id: z.string().describe("Reminder id from daftar_pengingat."),
          preset: z.enum(["10m", "1h", "tomorrow"]).optional(),
          localDateTime: localDateTime.optional().describe("Snooze until this wall-clock time instead of a preset."),
        }),
        execute: async ({ id, preset, localDateTime: until }) => {
          const now = clock()
          const own = ownReminder(db, id, chatId)
          if ("ok" in own) return own
          if (!preset && !until) return { ok: false as const, error: "Give a preset (10m, 1h, tomorrow) or a localDateTime." }
          try {
            const r = snoozeReminder(db, id, until ? { until: localToUtc(until, own.tz) } : (preset as "10m" | "1h" | "tomorrow"), now)
            const when = reminderNext(r, now).replace(/^Hari ini, /, "")
            return { ok: true as const, message: `Oke, ditunda sampai ${when}.`, reminder: view(r, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },
  }
}

export default defineRuntime(manifest, {
  test: async (ctx) => {
    const n = listReminders(ctx.db, { filter: "upcoming", limit: 500 }).length
    return { ok: true, detail: n ? `${n} active reminder${n === 1 ? "" : "s"}` : "Ready, no active reminders" }
  },
  start: async (ctx) => ({
    stop: async () => {},
    tools: async (turn) => reminderTools(ctx.db, turn),
  }),
})
