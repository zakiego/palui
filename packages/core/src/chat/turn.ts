// The agent turn (PLAN 8.4 to 8.8): context, tools, AI SDK loop within the
// budget, approvals, reply, bookkeeping.
import { and, eq, inArray, like } from "drizzle-orm"
import { isStepCount, streamText, type ModelMessage, type ToolApprovalResponse } from "ai"
import { events, jobs, messages, newId, pendingApprovals, runs, toolCalls, type DB, type RunStatus, type RunTrigger } from "@palui/db"
import { approvalsForRun, createApproval, preview, type ApprovalRow, type ResumeContext } from "../approvals/store"
import { checkGlobalBudget, jobOverCap, pausedNotice, recordRunStart, recordRunTokens } from "../budget"
import { getChat, getSession, type ChatRow } from "../chats"
import { logEvent } from "../events"
import { redact, type Logger } from "../log"
import { autoRecall, findMemory } from "../memory"
import { failoverModel, resolveChain } from "../models/failover"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import type { TurnInfo } from "../connectors/define"
import type { Supervisor } from "../connectors/supervisor"
import { getAllSettings, type ModelRole } from "../settings"
import { collectTools, type CollectedTools } from "../tools/collect"
import type { TurnBus } from "./bus"
import { buildSystem, hasImages, loadHistory, loadToolCalls, toModelMessages, type StoredContent } from "./context"
import type { EngineDeps } from "./deps"

export const MODEL_OFF_TEXT = "Model lagi off. Nyalakan: `/connectors on cliproxy`"
export const REPLY_TTL_MS = 24 * 3600_000
// A run cut short by a shutdown stays `running` with this error, so the next
// start recovers it the same way as a crash (recoverInterruptedRuns).
export const INTERRUPTED_ERROR = "interrupted by a restart"
export const INTERRUPTED_TEXT = "Turn tadi kepotong restart di tengah jalan, dan sempat jalanin aksi yang mengubah data. Cek dulu hasilnya ya, terus kirim ulang kalau masih perlu."

export type EngineHost = Pick<Supervisor, "running" | "get" | "channel" | "status" | "reconcile" | "release">

export type ActiveTurn = { runId: string; controller: AbortController; connectorIds: Set<string> }

export type EngineCtx = {
  db: DB
  host: EngineHost
  log: Logger
  clock: () => number
  bus: TurnBus
  deps: EngineDeps
  active: Map<string, ActiveTurn>
  draining: boolean
  approvalTtlMs: number
}

export type TurnOptions = {
  chatId: string
  trigger: RunTrigger
  prompt?: string
  jobId?: string
  toolScope?: string[] | null
  modelRole?: ModelRole
  deliver?: boolean
  silentToken?: string
  noTools?: boolean
  untrustedInput?: boolean
}

export type TurnResult = { runId: string; text: string; status: string }

export type ExecInput = TurnOptions & {
  batchIds?: string[]
  resume?: { previousRunId: string; approvals: ApprovalRow[]; context: ResumeContext; messages: ModelMessage[] }
}

// Enqueue a message to a chat's channel.
export function sendToChat(ctx: EngineCtx, chat: ChatRow, text: string, source: string, extra: { actions?: { id: string; label: string }[]; format?: "plain" | "markdown" } = {}) {
  const now = ctx.clock()
  const row = enqueueOutbox(ctx.db, { chatId: chat.id, channel: chat.channel, payload: { text, ...extra }, source, expiresAt: now + REPLY_TTL_MS }, now)
  notifyOutbox()
  return row
}

function capabilityList(ctx: EngineCtx, tools: CollectedTools): string[] {
  const out: string[] = []
  for (const e of ctx.host.running()) {
    if (e.row.kind === "model" || e.row.kind === "channel") continue
    const n = Object.values(tools.meta).filter((m) => m.connectorId === e.id).length
    out.push(n ? `${e.row.label} (${n} tools)` : e.row.label)
  }
  return out
}

export async function executeTurn(ctx: EngineCtx, input: ExecInput): Promise<TurnResult> {
  const { db, log } = ctx
  const startedAt = ctx.clock()
  const chat = getChat(db, input.chatId)
  if (!chat) throw new Error(`No chat ${input.chatId}`)
  const settings = getAllSettings(db)
  const deliver = input.resume ? input.resume.context.deliver : (input.deliver ?? true)
  const origin: ResumeContext["originTrigger"] = input.resume ? input.resume.context.originTrigger : input.trigger === "approval" ? "message" : input.trigger
  const tlog = log.child({ chat_id: chat.id })

  // Budgets: a hit cap pauses turns and tells the owner (PLAN 5.2).
  const budget = checkGlobalBudget(db, startedAt)
  if (!budget.ok) {
    if (budget.fresh || input.trigger === "message") sendToChat(ctx, chat, pausedNotice(db, budget), "system")
    if (budget.fresh) logEvent(db, { kind: "budget.pause", level: "warn", message: `Turns paused: ${budget.reason}` }, startedAt)
    markBatch(db, input.batchIds, null)
    return { runId: "", text: "", status: "budget" }
  }
  const jobId = input.resume?.context.jobId ?? input.jobId
  if (jobId) {
    const job = db.select().from(jobs).where(eq(jobs.id, jobId)).get()
    if (job && jobOverCap(db, jobId, job.dailyTokenCap, startedAt, job.tz)) {
      logEvent(db, { kind: "budget.job", level: "warn", message: `Job ${job.name} hit its daily token cap (${job.dailyTokenCap}); run skipped` }, startedAt)
      return { runId: "", text: "", status: "budget" }
    }
  }

  // Context pieces that decide the model role.
  const session = chat.sessionId ? getSession(db, chat.sessionId) : undefined
  const batch = new Set(input.batchIds ?? [])
  let history = input.trigger === "message" && session ? loadHistory(db, session.id, session.summarizedUntil ?? null) : []
  // Messages of this batch always make it in, even past the 30-message window.
  if (batch.size && !history.some((h) => batch.has(h.id))) {
    history = [...history, ...db.select().from(messages).where(inArray(messages.id, [...batch])).all()]
  }
  const role: ModelRole = input.resume?.context.modelRole ?? input.modelRole ?? (hasImages(history, batch) ? "vision" : "default")

  const chain = resolveChain(db, ctx.host, role)
  if (!chain.length) {
    if (input.trigger === "message") sendToChat(ctx, chat, MODEL_OFF_TEXT, "system")
    markBatch(db, input.batchIds, null)
    tlog.warn("no model connector running", { role })
    return { runId: "", text: "", status: "no_model" }
  }

  const runId = newId("run")
  const rlog = tlog.child({ run_id: runId })
  db.insert(runs).values({ id: runId, chatId: chat.id, jobId: jobId ?? null, trigger: input.trigger, status: "running", startedAt }).run()
  recordRunStart(db, startedAt)
  markBatch(db, input.batchIds, runId)

  const controller = new AbortController()
  const active: ActiveTurn = { runId, controller, connectorIds: new Set() }
  ctx.active.set(chat.id, active)
  const state = { tainted: !!(input.untrustedInput || input.resume?.context.tainted) }
  const toolStarts = new Map<string, number>()
  const channel = chat.channel === "web" ? undefined : ctx.host.channel(chat.channel)
  const typing = (on: boolean) => channel?.typing?.(chat.externalId, on).catch(() => {})

  let status: RunStatus = "ok"
  let error: string | undefined
  let text = ""
  const texts: string[] = []
  // The text of the last step only: the silent token is judged on it (PLAN 7 heartbeat).
  let lastStepText = ""
  let lastFinishReason: string | undefined
  let tokensIn = 0
  let tokensOut = 0
  let tokensCached = 0
  let steps = 0
  const approvalRequests: { approvalId: string; toolCallId: string; toolName: string; input: unknown }[] = []
  let model: ReturnType<typeof failoverModel> | undefined
  let interrupted = false

  try {
    const turnInfo: TurnInfo = { runId, chatId: chat.id, channel: chat.channel, trigger: input.trigger, tz: settings.timezone, now: startedAt, jobId }
    const noTools = input.noTools || origin === "hook"
    const scope = input.resume ? input.resume.context.toolScope : input.toolScope
    const tools: CollectedTools = noTools
      ? { tools: {}, meta: {}, connectorIds: [] }
      : await collectTools(ctx.host.running(), {
          turn: turnInfo,
          maxTools: settings.budgets.maxTools,
          scope,
          excludeDestructive: unattended(origin),
          log: rlog,
          onUntrustedResult: (m) => {
            if (!state.tainted) rlog.info("turn tainted by an untrusted tool result", { tool: m.name })
            state.tainted = true
          },
          onToolStart: (id) => toolStarts.set(id, ctx.clock()),
        })
    for (const id of tools.connectorIds) active.connectorIds.add(id)

    // Messages and system prompt.
    let system: string
    let modelMessages: ModelMessage[]
    if (input.resume) {
      system = input.resume.context.system
      const responses: ToolApprovalResponse[] = input.resume.approvals.map((a) => ({
        type: "tool-approval-response",
        approvalId: a.approvalId,
        approved: a.status === "approved",
        reason: a.status === "approved" ? "Approved by the owner" : a.status === "expired" ? "No answer from the owner within 10 minutes" : "Denied by the owner",
      }))
      modelMessages = [...input.resume.messages, { role: "tool", content: responses }]
    } else {
      const batchText = history
        .filter((h) => batch.has(h.id))
        .map((h) => h.text)
        .join("\n")
      const recall = input.trigger === "message" ? await autoRecall(db, findMemory(ctx.host.running()), batchText, rlog, startedAt) : null
      system = buildSystem({
        persona: settings.persona,
        now: startedAt,
        tz: settings.timezone,
        channel: chat.channel,
        capabilities: capabilityList(ctx, tools),
        recall,
        summary: input.trigger === "message" ? session?.summary : null,
        trigger: origin,
      })
      // Past tool calls are replayed only next to a tool list: a request with tool parts and no tools is rejected by some providers.
      const pastCalls = Object.keys(tools.tools).length ? loadToolCalls(db, history) : []
      modelMessages = input.trigger === "message" ? toModelMessages(history, batch, pastCalls) : [{ role: "user", content: input.prompt ?? "" }]
    }

    model = failoverModel(chain, (from, e) => rlog.warn("model failed, trying the next one in the chain", { connector_id: from.connectorId, model: from.model, error: (e as Error)?.message }))
    const strict = unattended(origin) || origin === "hook"
    void typing(true)
    ctx.bus.publish({ type: "turn-start", chatId: chat.id, runId })

    const result = streamText({
      model,
      system,
      messages: modelMessages,
      tools: tools.tools,
      maxRetries: 1,
      stopWhen: [isStepCount(settings.budgets.maxSteps), () => ctx.draining],
      abortSignal: AbortSignal.any([controller.signal, AbortSignal.timeout(settings.budgets.turnTimeoutMs)]),
      toolApproval: ({ toolCall }) => {
        const meta = tools.meta[toolCall.toolName]
        // Taint at call time, not only when the result comes back: the SDK decides
        // approvals for every call in a step before running any of them, so a write
        // called next to an untrusted read in the same step must already see it.
        if (meta?.untrusted && !state.tainted) {
          rlog.info("turn tainted by an untrusted tool call", { tool: meta.name })
          state.tainted = true
        }
        const cls = meta?.class ?? "destructive"
        if (cls === "destructive") return "user-approval"
        if (cls === "write" && (state.tainted || strict)) return "user-approval"
        return undefined
      },
      onError: () => {},
    })

    let stepText = ""
    for await (const p of result.fullStream) {
      switch (p.type) {
        case "start-step":
          stepText = ""
          break
        case "text-delta":
          stepText += p.text
          ctx.bus.publish({ type: "text-delta", chatId: chat.id, runId, delta: p.text })
          break
        case "finish-step":
          steps++
          lastFinishReason = p.finishReason
          lastStepText = stepText.trim()
          if (lastStepText) texts.push(lastStepText)
          stepText = ""
          break
        case "tool-call": {
          const meta = tools.meta[p.toolName]
          db.insert(toolCalls)
            .values({
              id: `${runId}:${p.toolCallId}`,
              runId,
              connectorId: meta?.connectorId ?? null,
              tool: p.toolName,
              class: meta?.class ?? "destructive",
              args: p.input as unknown,
              status: "pending",
              createdAt: ctx.clock(),
            })
            .onConflictDoNothing()
            .run()
          ctx.bus.publish({ type: "tool-call", chatId: chat.id, runId, toolCallId: p.toolCallId, tool: p.toolName, class: meta?.class ?? "destructive", args: p.input })
          break
        }
        case "tool-approval-request":
          if (!(p as { isAutomatic?: boolean }).isAutomatic) {
            approvalRequests.push({ approvalId: p.approvalId, toolCallId: p.toolCall.toolCallId, toolName: p.toolCall.toolName, input: p.toolCall.input })
          }
          break
        case "tool-result":
          if ((p as { preliminary?: boolean }).preliminary) break
          finishToolCall(ctx, chat.id, runId, input.resume?.previousRunId, p.toolCallId, p.toolName, "ok", redact(preview(p.output, 500)), toolStarts)
          break
        case "tool-error":
          finishToolCall(ctx, chat.id, runId, input.resume?.previousRunId, p.toolCallId, p.toolName, "error", redact(preview((p.error as Error)?.message ?? p.error, 500)), toolStarts)
          break
        case "tool-output-denied":
          finishToolCall(ctx, chat.id, runId, input.resume?.previousRunId, p.toolCallId, p.toolName, "denied", "denied", toolStarts)
          break
        case "error":
          status = "error"
          error = (p.error as Error)?.message ?? String(p.error)
          break
        case "abort":
          status = "aborted"
          break
        case "finish":
          tokensIn = p.totalUsage.inputTokens ?? 0
          tokensOut = p.totalUsage.outputTokens ?? 0
          tokensCached = p.totalUsage.inputTokenDetails?.cacheReadTokens ?? 0
          break
      }
    }
    if (stepText.trim()) {
      lastStepText = stepText.trim()
      texts.push(lastStepText)
    }
    text = texts.join("\n\n")
    const silent = isSilent(lastStepText, input.resume?.context.silentToken ?? input.silentToken)
    // stopWhen ends the loop on a drain right after a tool step: the answer never came.
    if (ctx.draining && !approvalRequests.length && (status === "aborted" || (status === "ok" && lastFinishReason === "tool-calls"))) interrupted = true

    if (interrupted) {
      rlog.warn("turn interrupted by a shutdown; it is recovered on the next start")
    } else if (approvalRequests.length && status !== "error" && status !== "aborted") {
      const response = (await result.responseMessages) as ModelMessage[]
      const context: ResumeContext = {
        trigger: input.trigger,
        originTrigger: origin,
        jobId,
        toolScope: scope ?? null,
        modelRole: role,
        deliver,
        silentToken: input.resume?.context.silentToken ?? input.silentToken,
        tainted: state.tainted,
        sessionId: chat.sessionId,
        system,
      }
      if (text && deliver && !silent) deliverReply(ctx, chat, runId, text)
      for (const r of approvalRequests) {
        const meta = tools.meta[r.toolName]
        const row = createApproval(
          db,
          {
            runId,
            chatId: chat.id,
            approvalId: r.approvalId,
            toolCallId: r.toolCallId,
            tool: r.toolName,
            toolClass: meta?.class ?? "destructive",
            argsPreview: preview(r.input, 300),
            messages: [...modelMessages, ...response],
            context,
          },
          ctx.clock(),
          ctx.approvalTtlMs,
        )
        sendApprovalPrompt(ctx, chat, row, meta?.title)
      }
      status = "awaiting_approval"
    } else if (status === "ok") {
      if (text && deliver && !silent) deliverReply(ctx, chat, runId, text)
    } else if (status === "error" && (input.trigger === "message" || input.trigger === "approval")) {
      sendToChat(ctx, chat, `Waduh, gagal jawab: ${shortError(error)}. Coba lagi bentar ya.`, "system")
    }
  } catch (e) {
    const aborted = controller.signal.aborted || (e as Error)?.name === "AbortError" || (e as Error)?.name === "TimeoutError"
    status = aborted ? "aborted" : "error"
    error = (e as Error)?.message ?? String(e)
    if (aborted && ctx.draining) interrupted = true
    else if (!aborted) rlog.error("turn failed", { error: e })
    if (!interrupted && status === "error" && (input.trigger === "message" || input.trigger === "approval")) sendToChat(ctx, chat, `Waduh, gagal jawab: ${shortError(error)}. Coba lagi bentar ya.`, "system")
  } finally {
    void typing(false)
    if (ctx.active.get(chat.id) === active) ctx.active.delete(chat.id)
    ctx.host.release()
  }

  if (interrupted) {
    status = "running"
    error = INTERRUPTED_ERROR
  } else if (status === "aborted" && !error) error = controller.signal.aborted ? "stopped by the owner" : "turn timed out"
  const ms = ctx.clock() - startedAt
  const served = model?.served()
  db.update(runs)
    .set({
      status,
      error: error ? redact(error) : null,
      tokensIn,
      tokensOut,
      tokensCached,
      steps,
      tainted: state.tainted,
      model: served?.model ?? chain[0]!.model,
      modelConnectorId: served?.connectorId ?? chain[0]!.connectorId,
      resultPreview: preview(text, 300),
      ms,
    })
    .where(eq(runs.id, runId))
    .run()
  recordRunTokens(db, tokensIn + tokensOut, jobId, ctx.clock())
  const after = checkGlobalBudget(db, ctx.clock())
  if (!after.ok && after.fresh) {
    sendToChat(ctx, chat, pausedNotice(db, after), "system")
    logEvent(db, { kind: "budget.pause", level: "warn", message: `Turns paused: ${after.reason}` }, ctx.clock())
  }
  ctx.bus.publish({ type: "turn-end", chatId: chat.id, runId, status: interrupted ? "aborted" : status, error })
  rlog.info("turn finished", { status: interrupted ? "interrupted" : status, steps, tokens_in: tokensIn, tokens_out: tokensOut, ms, tainted: state.tainted, connector_id: served?.connectorId })
  return { runId, text: interrupted ? "" : text, status: interrupted ? "interrupted" : status }
}

// Jobs and smart reminders run with nobody watching (PLAN 7): no destructive
// tools, and every write tool needs approval.
function unattended(origin: ResumeContext["originTrigger"]): boolean {
  return origin === "schedule" || origin === "reminder"
}

// The heartbeat answers its silent token ("NOTHING") when nothing matters. Only
// the final step counts, so "Aku cek dulu" + a tool call + "NOTHING" stays silent.
export function isSilent(finalStepText: string, token: string | undefined): boolean {
  const t = token?.trim().toLowerCase()
  return !!t && finalStepText.trim().toLowerCase() === t
}

function shortError(e: string | undefined): string {
  return redact(e ?? "error").replace(/\s+/g, " ").slice(0, 160)
}

function markBatch(db: DB, ids: string[] | undefined, runId: string | null) {
  if (!ids?.length) return
  db.update(messages).set({ status: "handled", runId }).where(inArray(messages.id, ids)).run()
}

function finishToolCall(ctx: EngineCtx, chatId: string, runId: string, previousRunId: string | undefined, toolCallId: string, tool: string, status: "ok" | "error" | "denied", resultPreview: string, starts: Map<string, number>) {
  const started = starts.get(toolCallId)
  const ms = started !== undefined ? ctx.clock() - started : null
  const ids = [`${runId}:${toolCallId}`, ...(previousRunId ? [`${previousRunId}:${toolCallId}`] : [])]
  ctx.db.update(toolCalls).set({ status, resultPreview, ms }).where(inArray(toolCalls.id, ids)).run()
  ctx.bus.publish({ type: "tool-result", chatId, runId, toolCallId, tool, preview: resultPreview, ms: ms ?? 0, status })
}

// The final reply: stored in the session (context and auto-save) and sent.
export function deliverReply(ctx: EngineCtx, chat: ChatRow, runId: string, text: string) {
  const now = ctx.clock()
  ctx.db
    .insert(messages)
    .values({ id: newId("msg"), chatId: chat.id, sessionId: chat.sessionId, role: "assistant", content: { text } satisfies StoredContent, text, status: "handled", runId, createdAt: now })
    .run()
  sendToChat(ctx, chat, text, `reply:${runId}`, { format: "markdown" })
}

export function sendApprovalPrompt(ctx: EngineCtx, chat: ChatRow, row: ApprovalRow, title?: string) {
  const name = title ?? row.tool.replace("__", " › ")
  const kind = row.toolClass === "destructive" ? "aksi destruktif" : "aksi yang mengubah data"
  const text = `Boleh aku jalanin ${name}? (${kind})\n${row.argsPreview ?? ""}\n\nBalas ya atau batal. Kalau 10 menit gak dijawab, aku anggap batal.`
  sendToChat(ctx, chat, text, `approval:${row.id}`, { actions: [{ id: `approve:${row.id}`, label: "Ya" }, { id: `deny:${row.id}`, label: "Batal" }] })
  ctx.bus.publish({ type: "approval", chatId: chat.id, runId: row.runId, approvalId: row.id, tool: row.tool, class: row.toolClass, argsPreview: row.argsPreview ?? "" })
}

// Why a resume can't start right now (budget pause, job cap, no model), as text
// for the owner; null when it can. Checked before the run is claimed, so an
// approved action isn't marked resumed and then dropped.
export function resumeBlocker(ctx: EngineCtx, context: ResumeContext): string | null {
  const now = ctx.clock()
  const budget = checkGlobalBudget(ctx.db, now)
  if (!budget.ok) {
    if (budget.fresh) logEvent(ctx.db, { kind: "budget.pause", level: "warn", message: `Turns paused: ${budget.reason}` }, now)
    return pausedNotice(ctx.db, budget)
  }
  if (context.jobId) {
    const job = ctx.db.select().from(jobs).where(eq(jobs.id, context.jobId)).get()
    if (job && jobOverCap(ctx.db, job.id, job.dailyTokenCap, now, job.tz)) return `Job ${job.name} udah kena batas token hariannya.`
  }
  if (!resolveChain(ctx.db, ctx.host, context.modelRole).length) return MODEL_OFF_TEXT
  return null
}

// Resume a run whose approvals are all decided.
export function resumeInput(db: DB, runId: string): ExecInput | null {
  const rows = approvalsForRun(db, runId)
  if (!rows.length) return null
  const first = rows[0]!
  const context = first.context as unknown as ResumeContext
  return {
    chatId: first.chatId,
    trigger: "approval",
    resume: { previousRunId: runId, approvals: rows, context, messages: first.messages as ModelMessage[] },
  }
}

export function finishResumedRun(db: DB, runId: string) {
  db.update(runs).set({ status: "ok" }).where(eq(runs.id, runId)).run()
}

// Runs still `running` when the engine starts were cut off by a crash, an OOM or
// a drain (PLAN 8.1: a message sent mid-deploy must still get its answer). A
// message turn that changed nothing is run again: its batch goes back to
// pending for replayPending. Anything that may have changed data (a write or
// destructive tool call, or an approved action being resumed) is marked
// `error` and the owner is told in that chat instead of guessing.
export const MAX_RECOVERIES = 2
const POISON_TEXT = "Pesan tadi bikin Palui keputus berkali-kali, jadi aku lewati dulu. Coba kirim ulang dengan versi yang lebih pendek atau tanpa lampiran."

// How many times any of these messages was already re-queued after a crash.
function countRecoveries(db: EngineCtx["db"], messageIds: string[]): number {
  let max = 0
  for (const id of messageIds) {
    const n = db
      .select({ id: events.id })
      .from(events)
      .where(and(eq(events.kind, "run.recovered"), like(events.data, `%"${id}"%`)))
      .all().length
    max = Math.max(max, n)
  }
  return max
}

export function recoverInterruptedRuns(ctx: EngineCtx): { requeued: number; failed: number } {
  const { db } = ctx
  const now = ctx.clock()
  let requeued = 0
  let failed = 0
  for (const run of db.select().from(runs).where(eq(runs.status, "running")).all()) {
    // It got as far as asking for approval: it's waiting, not lost.
    if (db.select({ id: pendingApprovals.id }).from(pendingApprovals).where(eq(pendingApprovals.runId, run.id)).get()) {
      db.update(runs).set({ status: "awaiting_approval", error: null }).where(eq(runs.id, run.id)).run()
      continue
    }
    // The reply already went out: running it again would answer twice.
    const replied = !!db
      .select({ id: messages.id })
      .from(messages)
      .where(and(eq(messages.runId, run.id), eq(messages.role, "assistant")))
      .get()
    const changed = !!db
      .select({ id: toolCalls.id })
      .from(toolCalls)
      // Only calls that ran: rows are written when the model emits the call, before approval or execution.
      .where(and(eq(toolCalls.runId, run.id), inArray(toolCalls.class, ["write", "destructive"]), inArray(toolCalls.status, ["ok", "error"])))
      .get()
    const batch =
      run.trigger === "message"
        ? db
            .select({ id: messages.id })
            .from(messages)
            .where(and(eq(messages.runId, run.id), eq(messages.role, "user")))
            .all()
        : []
    // A batch that already crashed the process twice is a poison message: skip it
    // instead of crash-looping, and tell the owner.
    const recoveries = batch.length ? countRecoveries(db, batch.map((b) => b.id)) : 0
    if (!replied && !changed && run.trigger !== "approval" && batch.length && recoveries >= MAX_RECOVERIES) {
      db.update(runs).set({ status: "error", error: `${INTERRUPTED_ERROR}; skipped after ${recoveries} recoveries` }).where(eq(runs.id, run.id)).run()
      logEvent(db, { kind: "run.poison", level: "error", message: `Run ${run.id} was cut off ${recoveries + 1} times; its messages were skipped`, data: { runId: run.id, messageIds: batch.map((b) => b.id) } }, now)
      const chat = run.chatId ? getChat(db, run.chatId) : undefined
      if (chat) sendToChat(ctx, chat, POISON_TEXT, "system")
      failed++
      continue
    }
    if (!replied && !changed && run.trigger !== "approval" && batch.length) {
      db.transaction((tx) => {
        tx.update(messages)
          .set({ status: "pending", runId: null })
          .where(and(eq(messages.runId, run.id), eq(messages.role, "user")))
          .run()
        tx.update(runs).set({ status: "error", error: `${INTERRUPTED_ERROR}; its messages were queued again` }).where(eq(runs.id, run.id)).run()
      })
      logEvent(db, { kind: "run.recovered", level: "warn", message: `Run ${run.id} was cut off by a restart; ${batch.length} message(s) queued again`, data: { runId: run.id, messageIds: batch.map((b) => b.id) } }, now)
      requeued++
      continue
    }
    db.update(runs).set({ status: "error", error: INTERRUPTED_ERROR }).where(eq(runs.id, run.id)).run()
    failed++
    const unsafe = !replied && (changed || run.trigger === "approval")
    logEvent(
      db,
      {
        kind: "run.interrupted",
        level: "warn",
        message: `Run ${run.id} (${run.trigger}) was cut off by a restart${unsafe ? " after it may have changed data; the owner was told" : ""}`,
        data: { runId: run.id },
      },
      now,
    )
    const chat = unsafe && run.chatId ? getChat(db, run.chatId) : undefined
    if (chat) sendToChat(ctx, chat, INTERRUPTED_TEXT, "system")
  }
  return { requeued, failed }
}
