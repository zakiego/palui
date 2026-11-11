// The routine tools after the handover to flows (RFC flows 3.6 point 4): thin
// wrappers that create and edit a flow of the canonical shape (routineToFlow),
// kept for one release so old habits and old chats still work. New work uses the
// flow tools. jawab_checkin moved to the flows skill.
import { tool } from "ai"
import { z } from "zod"
import type { DB } from "@palui/db"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import { flowToRoutine, routineToFlow } from "@palui/core/flows/convert"
import { flowRecap, listRuns, runView, summarizeRuns } from "@palui/core/flows/history"
import { flowForRoutine } from "@palui/core/flows/migrate"
import { createFlow, currentVersion, definitionOf, deleteFlow, FlowError, getFlow, listFlows, previewTimes, setFlowPaused, updateFlow, type FlowRow } from "@palui/core/flows/service"
import { formatWhen, isValidZone, localToUtc, TimeError } from "@palui/core/reminders/time"
import { actionSchema, daysSchema, recapSchema, sourceSchema } from "@palui/core/routines/config"
import { describeSchedule, parseConfig, RoutineError, type ParsedConfig, type RoutineRow } from "@palui/core/routines/service"
import { getSetting } from "@palui/core/settings"

type Fail = { ok: false; error: string }

const NOTE = "Kept for compatibility: routines now run as flows. Prefer the flow tools (buat_flow, ubah_flow, ...) for anything new."

function fail(e: unknown): Fail {
  if (e instanceof RoutineError || e instanceof TimeError || e instanceof FlowError) return { ok: false, error: e.message }
  throw e
}

function zoneFor(db: DB, turn: TurnInfo, tz?: string | null): string {
  if (tz) return tz
  if (turn.tz && isValidZone(turn.tz)) return turn.tz
  return getSetting(db, "timezone")
}

// The flow behind a routine id (rtn_, migrated) or a flow id.
function resolve(db: DB, chatId: string, id: string): { flow: FlowRow; cfg: ParsedConfig } | Fail {
  const flow = (id.startsWith("rtn_") ? flowForRoutine(db, id) : undefined) ?? getFlow(db, id)
  if (!flow || flow.chatId !== chatId) return { ok: false, error: `No routine ${id} in this chat. Call daftar_rutinitas to get the ids.` }
  const cfg = flowToRoutine(definitionOf(currentVersion(db, flow)))
  if (!cfg) return { ok: false, error: `"${flow.name}" has steps a routine can't express; change it with ubah_flow.` }
  return { flow, cfg: { ...cfg, tz: flow.tz } }
}

function view(db: DB, flow: FlowRow, cfg: ParsedConfig, now: number) {
  return {
    id: flow.id,
    name: flow.name,
    active: flow.enabled,
    pausedUntil: !flow.enabled && flow.pausedUntil ? formatWhen(flow.pausedUntil, flow.tz, now) : null,
    schedule: describeSchedule({ tz: flow.tz } as RoutineRow, cfg),
    action: cfg.action.kind,
    next: previewTimes(db, flow, 1, now)[0]?.when ?? null,
    lastError: flow.lastError,
    config: { tz: flow.tz, days: cfg.days, source: cfg.source, action: cfg.action, recap: cfg.recap, lateGraceMin: cfg.lateGraceMin },
  }
}

export function flowBackedRoutineTools(db: DB, turn: TurnInfo, clock: () => number = () => Date.now()): Record<string, PaluiTool> {
  const chatId = turn.chatId
  const configInput = {
    name: z.string().min(1).max(80),
    tz: z.string().optional(),
    days: daysSchema.optional(),
    source: sourceSchema,
    action: actionSchema,
    recap: recapSchema.optional(),
    lateGraceMin: z.number().int().min(1).max(1440).optional(),
  }
  return {
    buat_rutinitas: {
      class: "write",
      title: "Create a routine",
      tool: tool({
        description: `Create a recurring reminder, check-in or prompt as a flow. ${NOTE}`,
        inputSchema: z.object(configInput),
        execute: async (input) => {
          const now = clock()
          try {
            const tz = zoneFor(db, turn, input.tz)
            const cfg = parseConfig({ ...input, tz })
            const r = createFlow(db, { chatId, definition: routineToFlow(cfg), createdBy: `agent:${turn.runId}`, agentId: turn.agentId ?? null }, now)
            return { ok: true as const, id: r.flow.id, message: `Oke, rutinitas "${r.flow.name}" aktif.`, next: previewTimes(db, r.flow, 5, now).map((t) => t.when), routine: view(db, r.flow, cfg, now) }
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
        description: `This chat's routines (flows of the routine shape) with their config. ${NOTE}`,
        inputSchema: z.object({}),
        execute: async () => {
          const now = clock()
          const rows = listFlows(db, chatId)
            .filter((f) => !f.shadow)
            .flatMap((f) => {
              const cfg = flowToRoutine(definitionOf(currentVersion(db, f)))
              return cfg ? [view(db, f, { ...cfg, tz: f.tz }, now)] : []
            })
          return { ok: true as const, count: rows.length, routines: rows, ...(rows.length ? {} : { message: "Belum ada rutinitas." }) }
        },
      }),
    },
    ubah_rutinitas: {
      class: "write",
      title: "Change a routine",
      tool: tool({
        description: `Change a routine. Pass only what changes; source, action and recap are replaced whole. ${NOTE}`,
        inputSchema: z.object({
          id: z.string(),
          name: configInput.name.optional(),
          tz: configInput.tz,
          days: configInput.days,
          source: sourceSchema.optional(),
          action: actionSchema.optional(),
          recap: recapSchema.optional(),
          lateGraceMin: configInput.lateGraceMin,
        }),
        execute: async ({ id, ...patch }) => {
          const now = clock()
          const own = resolve(db, chatId, id)
          if ("ok" in own) return own
          try {
            const cfg = parseConfig({ ...own.cfg, ...Object.fromEntries(Object.entries(patch).filter(([, v]) => v !== undefined)) })
            const r = updateFlow(db, own.flow.id, { definition: routineToFlow(cfg), createdBy: `agent:${turn.runId}`, note: "ubah_rutinitas" }, now)
            return { ok: true as const, message: `Sip, rutinitas "${r.flow.name}" diubah.`, next: previewTimes(db, r.flow, 5, now).map((t) => t.when), routine: view(db, r.flow, cfg, now) }
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
        description: `Pause a routine (optionally until a local date-time) or resume it. ${NOTE}`,
        inputSchema: z.object({ id: z.string(), paused: z.boolean(), until: z.string().regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/).optional() }),
        execute: async ({ id, paused, until }) => {
          const now = clock()
          const own = resolve(db, chatId, id)
          if ("ok" in own) return own
          try {
            const f = setFlowPaused(db, own.flow.id, paused, paused && until ? localToUtc(until, own.flow.tz) : null, now)
            const msg = !paused ? `Rutinitas "${f.name}" jalan lagi.` : f.pausedUntil ? `Rutinitas "${f.name}" dijeda sampai ${formatWhen(f.pausedUntil, f.tz, now)}.` : `Rutinitas "${f.name}" dijeda.`
            return { ok: true as const, message: msg }
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
        description: `Delete a routine and its log. ${NOTE}`,
        inputSchema: z.object({ id: z.string() }),
        execute: async ({ id }) => {
          const own = resolve(db, chatId, id)
          if ("ok" in own) return own
          deleteFlow(db, own.flow.id)
          return { ok: true as const, message: `Rutinitas "${own.flow.name}" dihapus.` }
        },
      }),
    },
    riwayat_rutinitas: {
      class: "read",
      title: "Routine history",
      tool: tool({
        description: `The log of a routine (or every routine in this chat) with counts. ${NOTE}`,
        inputSchema: z.object({ id: z.string().optional(), days: z.number().int().min(1).max(90).default(7), limit: z.number().int().min(1).max(200).default(50) }),
        execute: async ({ id, days, limit }) => {
          const now = clock()
          const d = days ?? 7
          if (id) {
            const own = resolve(db, chatId, id)
            if ("ok" in own) return own
            const recap = flowRecap(db, own.flow, { days: d, triggerId: "main" }, now)
            const runs = listRuns(db, { flowId: own.flow.id, since: now - d * 24 * 3600_000, triggerId: "main" }).slice(0, limit ?? 50)
            return { ok: true as const, summary: recap, occurrences: runs.map((r) => runView(db, r, own.flow.tz, now)) }
          }
          const runs = listRuns(db, { chatId, since: now - d * 24 * 3600_000 })
          return { ok: true as const, summary: summarizeRuns(runs), occurrences: runs.slice(0, limit ?? 50).map((r) => runView(db, r, getSetting(db, "timezone"), now)) }
        },
      }),
    },
  }
}
