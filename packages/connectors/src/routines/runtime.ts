// Routine tools for the model. They are generic on purpose: the model composes a
// feature (medicine check-ins, a habit tracker, a weekly review, times pulled
// from a schedule server) out of one source, one action and a recap. Every tool
// works on the turn's chat only.
import { tool } from "ai"
import { z } from "zod"
import type { DB } from "@palui/db"
import { defineRuntime, type PaluiTool, type TurnInfo } from "@palui/core/connectors/define"
import { formatWhen, isValidZone, localToUtc, TimeError } from "@palui/core/reminders/time"
import { actionSchema, daysSchema, recapSchema, sourceSchema, zodProblem, type RoutineSource } from "@palui/core/routines/config"
import {
  answerCheckin,
  configOf,
  createRoutine,
  deleteRoutine,
  describeSchedule,
  getRoutine,
  listRoutines,
  occurrencesSince,
  parseConfig,
  previewOccurrences,
  RoutineError,
  setPaused,
  summarize,
  updateRoutine,
  type OccurrenceRow,
  type Prefetched,
  type RoutineRow,
} from "@palui/core/routines/service"
import { nextLocalTime, refreshToolSource, upcoming, type Occurrence, type ToolCaller } from "@palui/core/routines/sources"
import { getSetting } from "@palui/core/settings"
import { routineMode } from "@palui/core/flows/migrate"
import { flowBackedRoutineTools } from "./flows"
import manifest from "./manifest"

type Fail = { ok: false; error: string }

function fail(e: unknown): Fail {
  if (e instanceof RoutineError || e instanceof TimeError) return { ok: false, error: e.message }
  throw e
}

const localDateTime = z.string().regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/, "Use YYYY-MM-DDTHH:mm with no offset")

const SOURCE_HELP = `WHEN the routine fires. One of:
- {kind:"times", times:[{time:"08:00", label:"Pagi"}, {time:"20:00", label:"Malam"}]}: fixed local times every active day.
- {kind:"rrule", rrule:"FREQ=WEEKLY;BYDAY=FR", start:"2026-11-13T16:00", label:"Review"}: one repeat rule (RFC 5545 body without DTSTART). start is the first local occurrence.
- {kind:"tool", connectorId:"<connector id>", tool:"<tool name without namespace>", args:{...}, refreshAt:"00:05", items:[{field:"path.to.time", label:"Name", offsetMin:0}]}: times read from a read-only connector tool every day. The tool is called for today and tomorrow; "{{date}}" in args becomes YYYY-MM-DD ({{month}}, {{year}}, {{tz}} too). Each item's field is a dotted path to a value like "04:39" or an ISO timestamp; offsetMin shifts it (-10 = ten minutes before).
- Or {kind:"tool", ..., list:{path:"items", at:"due_date", label:"name", time:"09:00", offsetMin:0}}: one call returning an array; each element's date or datetime becomes an occurrence.
Run pratinjau_rutinitas on a tool source first to check the field paths.`

const ACTION_HELP = `WHAT happens. One of:
- {kind:"message", text:"..."}: send a text.
- {kind:"checkin", text:"Udah {{label}}?", answers:[{id:"done", label:"Sudah", words:["udah"], closes:true}, {id:"not_yet", label:"Belum", closes:false}], followUp:{everyMin:30, max:3, text?:"..."}, deadline:{kind:"next"} or {kind:"after", minutes:120}, missedText?:"..."}: ask and log the answer. A closing answer stops the re-asks; others keep asking every everyMin up to max times, until the deadline (next = until the next occurrence). Answers default to Sudah/Belum.
- {kind:"prompt", prompt:"..."}: run the prompt as an unattended turn (it can use read tools and send its answer).
Templates: {{name}} {{label}} {{time}} {{date}} {{answers}} {{n}} (re-ask number).`

function zoneFor(db: DB, turn: TurnInfo, tz?: string | null): string {
  if (tz) return tz
  if (turn.tz && isValidZone(turn.tz)) return turn.tz
  return getSetting(db, "timezone")
}

function occView(o: Occurrence, tz: string, now: number) {
  return { when: formatWhen(o.at, tz, now), label: o.label, ...(o.fallback ? { fallback: true } : {}) }
}

function view(r: RoutineRow, now: number) {
  const cfg = configOf(r)
  return {
    id: r.id,
    name: r.name,
    active: r.enabled,
    pausedUntil: !r.enabled && r.pausedUntil ? formatWhen(r.pausedUntil, r.tz, now) : null,
    schedule: describeSchedule(r, cfg),
    action: cfg.action.kind,
    next: r.enabled && r.nextOcc ? occView(r.nextOcc, r.tz, now) : null,
    lastError: r.lastError,
    config: { tz: r.tz, days: cfg.days, source: cfg.source, action: cfg.action, recap: cfg.recap, lateGraceMin: cfg.lateGraceMin },
  }
}

function occRowView(o: OccurrenceRow, tz: string, now: number) {
  return {
    id: o.id,
    label: o.label,
    scheduled: formatWhen(o.scheduledAt, tz, now),
    status: o.status,
    answer: o.answer,
    reAsks: o.followUps,
    ...(o.fallback ? { fallback: true } : {}),
  }
}

function ownRoutine(db: DB, id: string, chatId: string): RoutineRow | Fail {
  const r = getRoutine(db, id)
  if (!r || r.chatId !== chatId) return { ok: false, error: `No routine ${id} in this chat. Call daftar_rutinitas to get the ids.` }
  return r
}

export type RoutineToolDeps = { callTool?: ToolCaller; clock?: () => number }

// Reads a tool source once, so creating or changing it fails fast on a bad
// connector, tool, args or field path.
async function prefetch(source: RoutineSource, tz: string, deps: RoutineToolDeps, now: number): Promise<Prefetched | Fail> {
  if (source.kind !== "tool") return { state: {}, nextRefreshAt: now }
  if (!deps.callTool) return { ok: false, error: "Tool sources aren't available in this process." }
  const res = await refreshToolSource(source, tz, {}, deps.callTool, now)
  if (!(res.state.entries ?? []).length) return { ok: false, error: `Couldn't read times from ${source.connectorId}.${source.tool}: ${res.error ?? "no entries"}` }
  // Partial success (tomorrow failed, say) still creates; the engine retries soon.
  return { state: res.state, nextRefreshAt: res.error ? now + 60_000 : nextLocalTime(source.refreshAt, tz, now) }
}

export function routineTools(db: DB, turn: TurnInfo, deps: RoutineToolDeps = {}): Record<string, PaluiTool> {
  const chatId = turn.chatId
  const clock = deps.clock ?? (() => Date.now())

  const configInput = {
    name: z.string().min(1).max(80).describe("Short name the owner will recognize, e.g. \"Minum obat\"."),
    tz: z.string().optional().describe("IANA time zone. Omit for the owner's zone."),
    days: daysSchema.optional().describe("Active weekdays (MO..SU). Omit or null for every day."),
    source: sourceSchema.describe(SOURCE_HELP),
    action: actionSchema.describe(ACTION_HELP),
    recap: recapSchema.optional().describe('Summary of the log: {kind:"off"} (default), {kind:"daily", time:"21:00"} or {kind:"weekly", day:"SU", time:"20:00"}.'),
    lateGraceMin: z.number().int().min(1).max(1440).optional().describe("Message and prompt actions more than this many minutes late (after downtime) are skipped. Default 60."),
  }

  return {
    buat_rutinitas: {
      class: "write",
      title: "Create a routine",
      tool: tool({
        description:
          "Create an automation in this chat: a time source (fixed times, a repeat rule, or times read daily from a connector tool) plus an action (message, check-in with answer options and re-asks, or a prompt). Use it for anything recurring the owner wants to be reminded about, asked about or tracked: taking medicine, drinking water, a habit, a weekly review, a bill whose due date comes from a tool, times that change daily from a schedule server. For a one-off reminder use the reminder tools instead. Confirm the returned `next` times with the owner.",
        inputSchema: z.object(configInput),
        execute: async (input) => {
          const now = clock()
          try {
            const tz = zoneFor(db, turn, input.tz)
            const cfg = parseConfig({ ...input, tz })
            const pre = await prefetch(cfg.source, tz, deps, now)
            if ("ok" in pre) return pre
            const r = createRoutine(db, chatId, { ...input, tz }, now, pre)
            const next = previewOccurrences(r, 5, now).map((o) => occView(o, r.tz, now))
            return { ok: true as const, id: r.id, message: `Oke, rutinitas "${r.name}" aktif.`, next, routine: view(r, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    daftar_rutinitas: {
      class: "read",
      title: "List routines",
      tool: tool({
        description: "List this chat's routines with their full config (to edit one), schedule, next occurrence, pause state and last source error.",
        inputSchema: z.object({}),
        execute: async () => {
          const now = clock()
          const rows = listRoutines(db, chatId)
          return { ok: true as const, count: rows.length, routines: rows.map((r) => view(r, now)), message: rows.length ? undefined : "Belum ada rutinitas." }
        },
      }),
    },

    ubah_rutinitas: {
      class: "write",
      title: "Change a routine",
      tool: tool({
        description:
          "Change a routine. Pass only what changes; source, action and recap are replaced whole, so start from the config daftar_rutinitas returned. Changes apply from now on.",
        inputSchema: z.object({
          id: z.string().describe("Routine id from daftar_rutinitas."),
          name: configInput.name.optional(),
          tz: configInput.tz,
          days: configInput.days,
          source: sourceSchema.optional().describe(SOURCE_HELP),
          action: actionSchema.optional().describe(ACTION_HELP),
          recap: configInput.recap,
          lateGraceMin: configInput.lateGraceMin,
        }),
        execute: async ({ id, ...patch }) => {
          const now = clock()
          const own = ownRoutine(db, id, chatId)
          if ("ok" in own) return own
          try {
            const merged = parseConfig({ ...configOf(own), ...Object.fromEntries(Object.entries(patch).filter(([, v]) => v !== undefined)) })
            let pre: Prefetched | undefined
            if (patch.source || patch.tz) {
              const p = await prefetch(merged.source, merged.tz, deps, now)
              if ("ok" in p) return p
              pre = p
            }
            const r = updateRoutine(db, id, patch, now, pre)
            return { ok: true as const, message: `Sip, rutinitas "${r.name}" diubah.`, next: previewOccurrences(r, 5, now).map((o) => occView(o, r.tz, now)), routine: view(r, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    jeda_rutinitas: {
      class: "write",
      title: "Pause or resume a routine",
      tool: tool({
        description: "Pause a routine (optionally until a local date-time, after which it resumes by itself) or resume it. While paused nothing fires and open check-ins stop re-asking. Resuming starts from now.",
        inputSchema: z.object({
          id: z.string().describe("Routine id from daftar_rutinitas."),
          paused: z.boolean(),
          until: localDateTime.optional().describe("Resume automatically at this wall-clock time in the routine's zone (YYYY-MM-DDTHH:mm)."),
        }),
        execute: async ({ id, paused, until }) => {
          const now = clock()
          const own = ownRoutine(db, id, chatId)
          if ("ok" in own) return own
          try {
            const r = setPaused(db, id, paused, paused && until ? localToUtc(until, own.tz) : null, now)
            const msg = !paused ? `Rutinitas "${r.name}" jalan lagi.` : r.pausedUntil ? `Rutinitas "${r.name}" dijeda sampai ${formatWhen(r.pausedUntil, r.tz, now)}.` : `Rutinitas "${r.name}" dijeda.`
            return { ok: true as const, message: msg, routine: view(r, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    hapus_rutinitas: {
      class: "destructive",
      title: "Delete a routine",
      tool: tool({
        description: "Delete a routine and its whole log. To stop it for a while, use jeda_rutinitas instead.",
        inputSchema: z.object({ id: z.string().describe("Routine id from daftar_rutinitas.") }),
        execute: async ({ id }) => {
          const own = ownRoutine(db, id, chatId)
          if ("ok" in own) return own
          deleteRoutine(db, id)
          return { ok: true as const, message: `Rutinitas "${own.name}" dihapus.` }
        },
      }),
    },

    pratinjau_rutinitas: {
      class: "read",
      // A tool source's preview reads outside data.
      untrusted: true,
      title: "Preview routine times",
      tool: tool({
        description:
          "Show the next occurrences of a routine (by id) or of a draft source before creating it. For a tool source this calls the tool now and reports which field paths didn't parse, so you can fix the mapping. Read-only.",
        inputSchema: z.object({
          id: z.string().optional().describe("Routine id from daftar_rutinitas."),
          source: sourceSchema.optional().describe("A draft source to try instead of an id."),
          tz: z.string().optional(),
          days: daysSchema.optional(),
          count: z.number().int().min(1).max(20).default(8),
        }),
        execute: async ({ id, source: draft, tz, days, count }) => {
          let source = draft
          const now = clock()
          if (id) {
            const own = ownRoutine(db, id, chatId)
            if ("ok" in own) return own
            return { ok: true as const, next: previewOccurrences(own, count, now).map((o) => occView(o, own.tz, now)), lastError: own.lastError }
          }
          if (!source) return { ok: false as const, error: "Pass an id or a draft source." }
          const parsed = sourceSchema.safeParse(source)
          if (!parsed.success) return { ok: false as const, error: `source: ${zodProblem(parsed.error)}` }
          source = parsed.data
          const zone = zoneFor(db, turn, tz)
          if (!isValidZone(zone)) return { ok: false as const, error: `Unknown time zone "${zone}".` }
          let state = {}
          let error: string | null = null
          if (source.kind === "tool") {
            if (!deps.callTool) return { ok: false as const, error: "Tool sources aren't available in this process." }
            const res = await refreshToolSource(source, zone, {}, deps.callTool, now)
            state = res.state
            error = res.error
          }
          try {
            const next = upcoming({ source, tz: zone, days: days ?? null, state }, { at: now, key: "" }, count)
            return { ok: true as const, next: next.map((o) => occView(o, zone, now)), ...(error ? { sourceError: error } : {}) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    jawab_checkin: {
      class: "write",
      title: "Record a check-in answer",
      tool: tool({
        description:
          "Record the owner's answer to a routine check-in. Use it when their message answers one (\"udah\", \"belum\", \"skip hari ini\"). Without occurrenceId it answers the newest open check-in in this chat; pass the id from the system prompt or riwayat_rutinitas for another one (also to correct an earlier answer). answer is an answer id or its label.",
        inputSchema: z.object({
          answer: z.string().min(1).max(40),
          occurrenceId: z.string().optional(),
          note: z.string().max(200).optional().describe("The owner's words, kept in the log."),
        }),
        execute: async ({ answer, occurrenceId, note }) => {
          try {
            const res = answerCheckin(db, { chatId, occurrenceId, answer, text: note }, clock())
            return { ok: true as const, message: res.reply, occurrence: occRowView(res.occurrence, res.routine.tz, clock()), routine: res.routine.name }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    riwayat_rutinitas: {
      class: "read",
      title: "Routine history",
      tool: tool({
        description: "The log of a routine (or all routines in this chat): each occurrence with its status (open, done, missed, sent, skipped, failed) and answer, plus counts. Use it for \"berapa kali aku lupa minggu ini?\" or to find an occurrence id.",
        inputSchema: z.object({
          id: z.string().optional().describe("Routine id; omit for every routine in this chat."),
          days: z.number().int().min(1).max(90).default(7),
          limit: z.number().int().min(1).max(200).default(50),
        }),
        execute: async ({ id, days, limit }) => {
          const now = clock()
          let tz = zoneFor(db, turn)
          if (id) {
            const own = ownRoutine(db, id, chatId)
            if ("ok" in own) return own
            tz = own.tz
          }
          const rows = occurrencesSince(db, { chatId, routineId: id, since: now - days * 24 * 3600_000, limit: 2000 })
          const names = new Map(listRoutines(db, chatId).map((r) => [r.id, r.name]))
          return {
            ok: true as const,
            summary: summarize(rows),
            occurrences: rows.slice(0, limit).map((o) => ({ routine: names.get(o.routineId) ?? o.routineId, ...occRowView(o, tz, now) })),
          }
        },
      }),
    },
  }
}

export default defineRuntime(manifest, {
  test: async (ctx) => {
    const n = listRoutines(ctx.db).length
    return { ok: true, detail: n ? `${n} routine${n === 1 ? "" : "s"}` : "Ready, no routines yet" }
  },
  start: async (ctx) => ({
    stop: async () => {},
    tools: async (turn) => {
      const tools = routineTools(ctx.db, turn, { callTool: ctx.services.callTool?.bind(ctx.services) })
      if (routineMode(ctx.db) !== "flows") return tools
      // After the handover the routine tools edit flows; the preview stays as it is.
      return { ...flowBackedRoutineTools(ctx.db, turn), pratinjau_rutinitas: tools.pratinjau_rutinitas! }
    },
  }),
})
