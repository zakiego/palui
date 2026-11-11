// Flow tools for the model (RFC flows 8). The agent builds a flow from chat:
// it finds tools with palui__search_tools, checks a draft with check_flow, dry-runs it
// with test_flow (read tools run for real, everything else is stubbed), shows the
// owner the triggers and write calls, then saves with create_flow. Every tool works
// on the turn's chat only, and the tools are only offered in the owner's own
// chats (DMs and the web chat), never in shared or guest turns, jobs or hooks.
import { tool } from "ai"
import { randomUUID } from "node:crypto"
import { z } from "zod"
import { hooks, type DB } from "@palui/db"
import { isSharedChat } from "@palui/core/agents"
import { getChat } from "@palui/core/chats"
import { defineRuntime, type PaluiTool, type TurnInfo } from "@palui/core/connectors/define"
import type { FlowToolCaller } from "@palui/core/flows/call"
import { dryRunFlow } from "@palui/core/flows/dryrun"
import { createFlowEngine, type FlowEngine } from "@palui/core/flows/engine"
import { answerFlowCheckin } from "@palui/core/flows/checkin"
import { diagnoseFlow } from "@palui/core/flows/diagnose"
import { flowDetailView, flowRecap, flowSummaryView, listRuns, runView, summarizeRuns } from "@palui/core/flows/history"
import {
  chatAccess,
  createFlow,
  definitionOf,
  deleteFlow,
  FlowError,
  getRun,
  listFlows,
  previewTimes,
  resolveFlow,
  setFlowPaused,
  updateFlow,
  currentVersion,
  type FlowRow,
  type SaveResult,
} from "@palui/core/flows/service"
import { resolverFrom, validateDefinition, describeWriteCall, type ValidateContext } from "@palui/core/flows/validate"
import { silentLogger } from "@palui/core/log"
import { formatWhen, localToUtc, TimeError } from "@palui/core/reminders/time"
import { getSetting } from "@palui/core/settings"
import { flowRuns } from "@palui/db"
import { and, eq, inArray } from "drizzle-orm"
import manifest from "./manifest"

type Fail = { ok: false; error: string; problems?: { path: string; message: string }[] }

function fail(e: unknown): Fail {
  if (e instanceof FlowError) return { ok: false, error: e.message, ...(e.problems.length ? { problems: e.problems } : {}) }
  if (e instanceof TimeError) return { ok: false, error: e.message }
  throw e
}

const localDateTime = z.string().regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/, "Use YYYY-MM-DDTHH:mm with no offset")

export const FLOW_HELP = `A flow definition is JSON: { name, description?, tz?, triggers: [...], steps: [...], limits?: { runsPerHour?, dailyTokens? } }.
Triggers (each has id, kind, start = first step id):
- schedule: { source: <time source: {kind:"times", times:[{time:"06:00", label}]} | {kind:"rrule", rrule, start:"YYYY-MM-DDTHH:mm"} | {kind:"tool", connectorId, tool, args, refreshAt, items|list}>, days?: ["MO".."SU"], lateGraceMin? }. trigger.* = at, time, date, label, key, nextAt (the next occurrence, epoch ms).
- webhook: { hookId } (an existing hook; its mode becomes flow). trigger.payload is untrusted.
- chat: { command: "/beres" } or { phrases: ["udah bayar"] }, the owner's messages in this chat. trigger.text, trigger.args.
- poll: { tool, connectorId?, args, everyMin: 5..1440, select: "path.in.result", fire: "change"|"truthy" }. trigger.value, trigger.previous.
- event: { event: "checkin.answered"|"checkin.missed"|"run.failed"|"run.done", flow: "<flow name>"|"self" }.
- Every flow can also run manually (run_flow); trigger.args are its args.
Names: trigger ids, step ids and set value names are lowercase letters, digits and _, starting with a letter, max 32 ("ask_1", "last_at"; not "L" or "Ask1"). Answer ids are lowercase letters, digits and _, max 20.
Steps (each has id, kind, optional next (step id or null to end; default the next in the array), name, onError: "fail"|"continue"|<step id>, timeoutSec):
- tool: { tool: "<connectorId>__<tool>" (as palui__search_tools shows), args, parse?: {field:"text", as:"json"}, retry?: {max, backoffSec} }. Output steps.<id>.data (or .error with onError).
- if: { when: "<expression>", then: <id|null>, else: <id|null> }.
- message: { text, to?: <another chat id the owner allowed> }.
- checkin: { text, answers: [{id, label, words?, closes}], followUp: {everyMin, max, text?}, deadline, missedText?, on?: {<answer id>: <step id>}, onMissed? }. Output answer, label, missed (skipped too when the deadline had already passed).
  deadline (default next, max 7 days): {kind:"next", plusMin?} the schedule's next occurrence (plusMin:-10 = 10 min before it) | {kind:"after", minutes: N or "{{template}}"} | {kind:"at", at: "<template or ISO>", plusMin?} a computed time, e.g. {kind:"at", at:"{{steps.nextp.data.next.timestamp}}", plusMin:-10}. A deadline already past skips the ask silently and goes onMissed. Re-asks stop at the deadline.
- wait: { minutes } or { until: "<template>", plusMin? } (max 7 days).
- prompt: { prompt: "<literal instruction, no templates>", input: { name: "{{...}}" }, tools: [], send: false, model: "main"|"fast" }. Output text, silent.
- set: { scope: "run"|"flow", values: { name: "<expression or template>" } } -> vars.<name> or flow.<name> (flow ones persist). Times in expressions are epoch ms: "trigger.nextAt - 10 * 60000" is 10 minutes before the next occurrence.
- each: { items: "<path to a list>", where?: "<expression on item>", key?: "{{item.id}}", max: 20, body: <step id> } runs body once per item, deduped by key across runs.
Templates "{{path | filter}}" go only in tool args, message/check-in text, prompt input, set values, wait.until, checkin deadline.at and deadline.minutes, each.key. Paths start with trigger, steps, vars, flow, item, index, now (now.date, now.time, now.weekday), run (run.followUps: the re-ask number, for check-in followUp.text). Filters: time, date, json, default("x"), join(", "), lines("field"), upper, lower, round(n), truncate(n).
Expressions (if.when, set, each.where): and or not, == != < <= > >=, + - * /, functions exists empty contains startsWith len lower any count number round min max choose(cond, a, b) date daysUntil hoursUntil minutesSince weekday today.
Destructive tools are never allowed. Write tools need the owner's approval once per version.`

const definitionInput = z.record(z.string(), z.unknown()).describe("The flow definition (see create_flow).")

// The flow id a draft is checked under, before it has one.
export const DRAFT_FLOW_ID = "draft"

export type FlowToolDeps = { clock?: () => number; tools?: FlowToolCaller; engine?: FlowEngine }

function zoneFor(db: DB, turn: TurnInfo): string {
  return turn.tz || getSetting(db, "timezone")
}

export function flowTools(db: DB, turn: TurnInfo, deps: FlowToolDeps = {}): Record<string, PaluiTool> {
  const chatId = turn.chatId
  const clock = deps.clock ?? (() => Date.now())
  const engine = deps.engine ?? createFlowEngine({ db, log: silentLogger, now: clock, tools: deps.tools })

  async function validateCtx(): Promise<ValidateContext> {
    // A tool step's view: a flow's own tools (flows__recap) only come with a flow id.
    const list = deps.tools ? await deps.tools.list({ ...turn, trigger: "flow", flowId: DRAFT_FLOW_ID }) : []
    return {
      resolveTool: deps.tools ? resolverFrom(list) : undefined,
      hookExists: (id) => !!db.select({ id: hooks.id }).from(hooks).where(eq(hooks.id, id)).get(),
      chatId,
      chatAllowed: chatAccess(db, chatId),
    }
  }

  function own(ref: string): FlowRow | Fail {
    const f = resolveFlow(db, chatId, ref)
    if (!f) return { ok: false, error: `No flow "${ref}" in this chat. Call list_flows for the ids.` }
    return f
  }

  function saved(r: SaveResult, now: number, verb: string) {
    const next = previewTimes(db, r.flow, 3, now).map((t) => `${t.when} (${t.trigger}${t.label ? `: ${t.label}` : ""})`)
    return {
      ok: true as const,
      id: r.flow.id,
      version: r.version.n,
      message: r.needsApproval
        ? `Flow "${r.flow.name}" ${verb}. Ada aksi yang mengubah data, jadi Zaki perlu setujui dulu (prompt udah dikirim). Bagian read-only tetap jalan.`
        : `Flow "${r.flow.name}" ${verb}.`,
      next,
      needsApproval: r.needsApproval,
      writeCalls: r.writeCalls.map(describeWriteCall),
      ...(r.diff.length ? { diff: r.diff } : {}),
      flow: flowSummaryView(db, r.flow, now),
    }
  }

  const tools: Record<string, PaluiTool> = {
    create_flow: {
      class: "write",
      title: "Create a flow",
      tool: tool({
        description: `Create an automation (a flow) in this chat. Use it when the logic is deterministic (fetch, check, send, ask), instead of a job. Always check_flow and test_flow first, show the owner the triggers, the next times and any write calls, then save. Find tools for tool steps with palui__search_tools (for: "flow"), dry-run a tool step alone to learn its output shape, then write conditions against the real output. Never put fetched text in a prompt instruction; pass it in input.\n\n${FLOW_HELP}`,
        inputSchema: z.object({ definition: definitionInput }),
        execute: async ({ definition }) => {
          const now = clock()
          try {
            const r = createFlow(db, { chatId, definition, createdBy: `agent:${turn.runId}`, validate: await validateCtx(), agentId: turn.agentId ?? null }, now)
            return saved(r, now, "dibuat")
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    update_flow: {
      class: "write",
      title: "Change a flow",
      tool: tool({
        description:
          'Change a flow: a new version from a full definition, or a JSON merge patch on the current one. In a patch, "steps" and "triggers" may be objects keyed by id ({"steps": {"send": {"text": "..."}, "old": null}}) to change, add or remove one entry. Returns the diff and whether the owner must approve again (only when the write calls change).',
        inputSchema: z.object({
          id: z.string().describe("Flow id or name from list_flows."),
          definition: definitionInput.optional(),
          patch: z.record(z.string(), z.unknown()).optional(),
          note: z.string().max(200).optional().describe("Why it changed, kept with the version."),
        }),
        execute: async ({ id, definition, patch, note }) => {
          const now = clock()
          const f = own(id)
          if ("ok" in f) return f
          try {
            const r = updateFlow(db, f.id, { definition, patch, note, createdBy: `agent:${turn.runId}`, validate: await validateCtx() }, now)
            if (!r.changed) return { ok: true as const, message: "Nggak ada yang berubah.", version: r.version.n }
            return saved(r, now, `diubah ke versi ${r.version.n}`)
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    check_flow: {
      class: "read",
      title: "Check a flow",
      tool: tool({
        description: "Validate a draft definition (or a saved flow) without saving: problems as sentences with their JSON path, the resolved tools with their class and trust, and the write calls the owner would approve.",
        inputSchema: z.object({ definition: definitionInput.optional(), id: z.string().optional() }),
        execute: async ({ definition, id }) => {
          let def: unknown = definition
          if (!def && id) {
            const f = own(id)
            if ("ok" in f) return f
            def = definitionOf(currentVersion(db, f))
          }
          if (!def) return { ok: false as const, error: "Pass a definition or an id." }
          const r = validateDefinition(def, await validateCtx())
          return {
            ok: r.ok,
            problems: r.problems,
            tools: Object.fromEntries(Object.entries(r.tools).map(([k, t]) => [k, { tool: t.name, class: t.class, untrusted: t.untrusted, flowApproval: t.flowApproval }])),
            writeCalls: r.writeCalls.map(describeWriteCall),
            needsApproval: r.writeCalls.length > 0,
          }
        },
      }),
    },

    test_flow: {
      class: "read",
      // Read tools run for real, so their output is outside data.
      untrusted: true,
      title: "Dry-run a flow",
      tool: tool({
        description:
          'Dry-run a flow (saved, by id) or a draft definition: read tools are called for real so you see their output, while write tools, messages, check-ins and prompts are stubbed and show what they would do. Waits are skipped, an each runs 3 items. Override a stub to walk a branch: stubs: {"ask": {"answer": "done"}, "cheer": "teks"}. trigger is a fake trigger payload (e.g. {"payload": {...}} for a webhook).',
        inputSchema: z.object({
          id: z.string().optional(),
          definition: definitionInput.optional(),
          trigger: z.record(z.string(), z.unknown()).optional(),
          stubs: z.record(z.string(), z.unknown()).optional(),
          start: z.string().optional().describe("A trigger id to start from (default the first)."),
          step: z.string().optional().describe("Run from this step instead (to try one tool step alone)."),
        }),
        execute: async ({ id, definition, trigger, stubs, start, step }) => {
          const now = clock()
          try {
            let flow: FlowRow | undefined
            let def
            let writeCalls
            if (definition) {
              const v = validateDefinition(definition, await validateCtx())
              if (!v.ok || !v.definition) return { ok: false as const, error: "The draft has problems; fix them first (check_flow).", problems: v.problems }
              def = v.definition
              writeCalls = v.writeCalls
            } else if (id) {
              const f = own(id)
              if ("ok" in f) return f
              flow = f
            } else return { ok: false as const, error: "Pass an id or a definition." }
            const res = await dryRunFlow(db, engine, { chatId, flow, definition: def, writeCalls, triggerId: start ?? null, trigger, stubs, start: step, tz: zoneFor(db, turn), now })
            return { ok: true as const, ...res }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    list_flows: {
      class: "read",
      title: "List flows",
      tool: tool({
        description: "This chat's flows with their triggers, next times, last run, approval state and pause state.",
        inputSchema: z.object({ filter: z.enum(["all", "active", "paused", "failing"]).default("all") }),
        execute: async ({ filter }) => {
          const now = clock()
          let rows = listFlows(db, chatId)
          if (filter === "active") rows = rows.filter((f) => f.enabled)
          if (filter === "paused") rows = rows.filter((f) => !f.enabled)
          if (filter === "failing") rows = rows.filter((f) => f.consecutiveFailures > 0 || !!f.lastError)
          return { ok: true as const, count: rows.length, flows: rows.map((f) => flowSummaryView(db, f, now)), ...(rows.length ? {} : { message: "Belum ada flow." }) }
        },
      }),
    },

    get_flow: {
      class: "read",
      title: "Show a flow",
      tool: tool({
        description: "One flow in full: its definition (current or an older version), versions, approval state, trigger state and flow variables.",
        inputSchema: z.object({ id: z.string(), version: z.number().int().min(1).optional() }),
        execute: async ({ id, version }) => {
          const f = own(id)
          if ("ok" in f) return f
          const view = flowDetailView(db, f, clock(), version)
          if (!view) return { ok: false as const, error: `No version ${version}.` }
          return { ok: true as const, flow: view }
        },
      }),
    },

    pause_flow: {
      class: "write",
      title: "Pause or resume a flow",
      tool: tool({
        description: "Pause a flow (optionally until a local date-time, then it resumes by itself) or resume it. Resuming starts from now: what was due while paused doesn't run.",
        inputSchema: z.object({ id: z.string(), paused: z.boolean(), until: localDateTime.optional() }),
        execute: async ({ id, paused, until }) => {
          const now = clock()
          const f = own(id)
          if ("ok" in f) return f
          try {
            const r = setFlowPaused(db, f.id, paused, paused && until ? localToUtc(until, f.tz) : null, now)
            const msg = !paused ? `Flow "${r.name}" jalan lagi.` : r.pausedUntil ? `Flow "${r.name}" dijeda sampai ${formatWhen(r.pausedUntil, r.tz, now)}.` : `Flow "${r.name}" dijeda.`
            return { ok: true as const, message: msg }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    delete_flow: {
      class: "destructive",
      title: "Delete a flow",
      tool: tool({
        description: "Delete a flow with its versions and run log. To stop it for a while, use pause_flow instead.",
        inputSchema: z.object({ id: z.string() }),
        execute: async ({ id }) => {
          const f = own(id)
          if ("ok" in f) return f
          deleteFlow(db, f.id)
          return { ok: true as const, message: `Flow "${f.name}" dihapus.` }
        },
      }),
    },

    run_flow: {
      class: "write",
      title: "Run a flow now",
      tool: tool({
        description: "Run a flow now, for real (messages go out, approved writes run). trigger picks which trigger's start step to use; args become trigger.args. To try it without side effects, use test_flow.",
        inputSchema: z.object({ id: z.string(), trigger: z.string().optional(), args: z.record(z.string(), z.unknown()).optional() }),
        execute: async ({ id, trigger, args }) => {
          const now = clock()
          const f = own(id)
          if ("ok" in f) return f
          const def = definitionOf(currentVersion(db, f))
          const t = trigger ? def.triggers.find((x) => x.id === trigger) : undefined
          if (trigger && !t) return { ok: false as const, error: `No trigger "${trigger}". Triggers: ${def.triggers.map((x) => x.id).join(", ") || "none"}.` }
          const r = engine.startRun({ flowId: f.id, triggerId: t?.id ?? null, triggerKind: "manual", runKey: `manual:${randomUUID()}`, payload: { kind: "manual", args: args ?? {} }, now, start: t?.start })
          if (!r.run || r.skipped) return { ok: false as const, error: r.skipped === "paused" ? `Flow "${f.name}" lagi dijeda.` : `Run skipped: ${r.skipped}.` }
          return { ok: true as const, runId: r.run.id, message: `Flow "${f.name}" dijalankan. Cek hasilnya dengan get_run.` }
        },
      }),
    },

    flow_history: {
      class: "read",
      title: "Flow history",
      tool: tool({
        description: 'Runs of a flow (or every flow in this chat): status, skip reason, the check-in answer, and counts by status and answer. Use it for "berapa kali aku lupa minggu ini?" or to find a run id.',
        inputSchema: z.object({
          id: z.string().optional(),
          days: z.number().int().min(1).max(180).default(7),
          status: z.enum(["queued", "running", "waiting", "awaiting_approval", "done", "failed", "skipped", "cancelled"]).optional(),
          limit: z.number().int().min(1).max(200).default(30),
        }),
        execute: async ({ id, days, status, limit }) => {
          const now = clock()
          let flowId: string | undefined
          let tz = zoneFor(db, turn)
          if (id) {
            const f = own(id)
            if ("ok" in f) return f
            flowId = f.id
            tz = f.tz
          }
          const visible = listFlows(db, chatId)
          const names = new Map(visible.map((f) => [f.id, f.name]))
          const rows = listRuns(db, { chatId, flowId, since: now - (days ?? 7) * 24 * 3600_000, status: status ? [status] : undefined, limit: 2000 }).filter((r) => names.has(r.flowId))
          return { ok: true as const, summary: summarizeRuns(rows), runs: rows.slice(0, limit ?? 30).map((r) => ({ flow: names.get(r.flowId)!, ...runView(db, r, tz, now) })) }
        },
      }),
    },

    get_run: {
      class: "read",
      title: "Show a flow run",
      tool: tool({
        description: "The step log of one flow run: each step's inputs, outputs, the branch it took (with the values an if compared), errors and timing.",
        inputSchema: z.object({ runId: z.string() }),
        execute: async ({ runId }) => {
          const r = getRun(db, runId)
          const f = r ? resolveFlow(db, chatId, r.flowId) : undefined
          if (!r || r.chatId !== chatId) return { ok: false as const, error: `No run ${runId} in this chat.` }
          return { ok: true as const, flow: f?.name ?? null, run: runView(db, r, f?.tz ?? zoneFor(db, turn), clock(), true) }
        },
      }),
    },

    cancel_run: {
      class: "write",
      title: "Cancel a flow run",
      tool: tool({
        description: "Cancel a queued or waiting flow run (a check-in that should stop asking, a long wait).",
        inputSchema: z.object({ runId: z.string() }),
        execute: async ({ runId }) => {
          const now = clock()
          const r = getRun(db, runId)
          if (!r || r.chatId !== chatId) return { ok: false as const, error: `No run ${runId} in this chat.` }
          const changed = db
            .update(flowRuns)
            .set({ status: "cancelled", error: "Cancelled from chat.", wait: null, wakeAt: null, endedAt: now, updatedAt: now })
            .where(and(eq(flowRuns.id, runId), inArray(flowRuns.status, ["queued", "waiting", "awaiting_approval"])))
            .run().changes
          return changed ? { ok: true as const, message: "Run dibatalkan." } : { ok: false as const, error: `That run is ${r.status}; only queued or waiting runs can be cancelled.` }
        },
      }),
    },

    answer_checkin: {
      class: "write",
      title: "Record a check-in answer",
      tool: tool({
        description:
          'Record the owner\'s answer to a check-in. Use it when their message answers one ("udah", "belum", "skip hari ini"). Without runId it answers the newest open check-in in this chat; pass the run id from the system prompt or flow_history for another one, or to correct an earlier answer. answer is an answer id or its label.',
        inputSchema: z.object({
          answer: z.string().min(1).max(40),
          runId: z.string().optional().describe("A flow run id (flr_...)."),
          note: z.string().max(200).optional().describe("The owner's words, kept in the log."),
        }),
        execute: async ({ answer, runId, note }) => {
          const now = clock()
          try {
            const res = answerFlowCheckin(db, { chatId, runId, answer, text: note }, now)
            return { ok: true as const, message: res.reply, runId: res.run.id, flow: res.flow.name, closed: res.closed }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },

    explain_flow: {
      class: "read",
      title: "Why didn't a flow run",
      tool: tool({
        description:
          'Diagnose "why didn\'t this flow fire / send / ask?" for a moment (default now): whether it is paused, the nearest run with its status, skip reason, the step it ended at and the values each if compared, or, with no run, what the trigger says (the computed times, source errors, the hook or poll state) and whether Palui was down. Phrase the verdict for the owner; don\'t guess beyond it.',
        inputSchema: z.object({ id: z.string(), at: localDateTime.optional().describe("Local date-time to ask about (YYYY-MM-DDTHH:mm). Default now.") }),
        execute: async ({ id, at }) => {
          const now = clock()
          const f = own(id)
          if ("ok" in f) return f
          try {
            const when = at ? localToUtc(at, f.tz) : now
            return { ok: true as const, flow: f.name, at: formatWhen(when, f.tz, now), ...diagnoseFlow(db, f, when, now) }
          } catch (e) {
            return fail(e)
          }
        },
      }),
    },
  }
  return tools
}

// flows__recap: a recap of a flow's log as data. Callable by the agent and,
// as a tool step, by a flow (where "self" or no flow means the calling flow).
export function recapTool(db: DB, turn: TurnInfo, clock: () => number = () => Date.now()): PaluiTool {
  return {
    class: "read",
    title: "Flow recap",
    tool: tool({
      description:
        'A recap of a flow\'s runs over the last N days: total, counts by check-in answer, missed count and labels, open check-ins, counts by status, and `text` (the ready-to-send recap). flow is a name or id; "self" or nothing means the flow calling it.',
      inputSchema: z.object({
        flow: z.string().max(80).optional(),
        days: z.number().int().min(1).max(90).default(7),
        trigger: z.string().max(32).optional().describe("Only runs started by this trigger (e.g. main)."),
      }),
      execute: async ({ flow, days, trigger }) => {
        const ref = !flow || flow === "self" ? turn.flowId : flow
        if (!ref) return { ok: false as const, error: 'Name a flow ("self" only works inside a flow).' }
        const f = resolveFlow(db, turn.chatId, ref)
        if (!f) return { ok: false as const, error: `No flow "${ref}" in this chat.` }
        return { ok: true as const, ...flowRecap(db, f, { days: days ?? 7, triggerId: trigger ?? null }, clock()) }
      },
    }),
  }
}

// Flow tools are the owner's, in the owner's own chats (RFC 12.9): never in a
// shared chat or a guest turn, and not in unattended turns (jobs, hooks, flow
// prompt steps), where a flow could otherwise install another flow.
export function offerFlowTools(db: DB, turn: TurnInfo): boolean {
  if (turn.trigger !== "message" && turn.trigger !== "approval") return false
  const chat = getChat(db, turn.chatId)
  return !!chat && !isSharedChat(db, chat)
}

export default defineRuntime(manifest, {
  test: async (ctx) => {
    const n = listFlows(ctx.db).length
    return { ok: true, detail: n ? `${n} flow${n === 1 ? "" : "s"}` : "Ready, no flows yet" }
  },
  start: async (ctx) => {
    let engine: FlowEngine | undefined
    return {
      stop: async () => {},
      tools: async (turn): Promise<Record<string, PaluiTool>> => {
        // A flow's own tool steps get the recap; prompt steps and jobs get nothing.
        if (turn.trigger === "flow" && turn.flowId) return { recap: recapTool(ctx.db, turn) }
        if (!offerFlowTools(ctx.db, turn)) return {}
        engine ??= createFlowEngine({ db: ctx.db, log: ctx.log, tools: ctx.services.flowTools })
        return { ...flowTools(ctx.db, turn, { tools: ctx.services.flowTools, engine }), recap: recapTool(ctx.db, turn) }
      },
    }
  },
})
