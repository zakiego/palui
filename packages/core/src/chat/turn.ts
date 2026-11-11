// The agent turn (PLAN 8.4 to 8.8): context, tools, AI SDK loop within the
// budget, approvals, reply, bookkeeping.
import { and, asc, eq, inArray, like } from "drizzle-orm"
import { isStepCount, streamText, type ModelMessage, type SystemModelMessage, type ToolApprovalResponse } from "ai"
import { events, jobs, messages, newId, pendingApprovals, runs, sessions, toolCalls, type DB, type RunStatus, type RunTrigger } from "@palui/db"
import { agentForChat, getAgent, isSharedChat, type AgentRow } from "../agents"
import { approvalsForRun, createApproval, preview, supersedeSameCall, type ApprovalRow, type ResumeContext, type TurnMode } from "../approvals/store"
import { agentOverCap, budgetTokens, checkGlobalBudget, firstAgentNoticeToday, jobOverCap, pausedNotice, recordAgentTokens, recordRunStart, recordRunTokens } from "../budget"
import { getChat, getSession, type ChatRow } from "../chats"
import { ownerChat } from "../connectors/rules"
import { logEvent } from "../events"
import { redact, type Logger } from "../log"
import { autoRecall, findMemory } from "../memory"
import { failoverModel, resolveChain } from "../models/failover"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import type { TurnInfo } from "../connectors/define"
import type { Supervisor } from "../connectors/supervisor"
import { notesPrompt } from "../notes/service"
import { flowCheckinsPrompt } from "../flows/checkin"
import { checkinsPrompt } from "../routines/service"
import { getAllSettings, type ModelRole } from "../settings"
import { collectTools, emptyTools, SEARCH_TOOL, STICKY_MAX, type CollectedTools } from "../tools/collect"
import type { TurnBus } from "./bus"
import { limitNotice, shouldWrapUp, stepClock, WRAP_UP_NOTE, type RanTool, type WrapUpReason } from "./turn-limits"
import { buildSystem, buildTurnContext, hasImages, loadHistory, loadToolCalls, markStaleReads, quotesOutsider, systemBlocks, toModelMessages, withTurnContext, type StoredContent } from "./context"
import type { EngineDeps } from "./deps"

export const MODEL_OFF_TEXT = "Model lagi off. Nyalakan: `/connectors on cliproxy`"
// What a shared chat hears instead of admin details (budgets, connectors, errors).
export const SHARED_BUSY_TEXT = "Maaf, lagi gak bisa jawab sekarang. Coba lagi nanti ya."
export const REPLY_TTL_MS = 24 * 3600_000
// A run cut short by a shutdown stays `running` with this error, so the next
// start recovers it the same way as a crash (recoverInterruptedRuns).
export const INTERRUPTED_ERROR = "interrupted by a restart"
export const INTERRUPTED_TEXT = "Turn tadi kepotong restart di tengah jalan, dan sempat jalanin aksi yang mengubah data. Cek dulu hasilnya ya, terus kirim ulang kalau masih perlu."

// What the model is told about each request when a run resumes.
const APPROVAL_REASON: Partial<Record<ApprovalRow["status"], string>> = {
  approved: "Approved by the owner",
  expired: "No answer from the owner within 10 minutes",
  superseded: "Dropped: the same call was made again later, so this older request no longer counts",
}

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
  // Tests only: overrides settings.budgets.turnTimeoutMs.
  turnTimeoutMs?: number
  // Queue the resume of a run whose approval requests are all decided.
  resumeRun: (runId: string) => void
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
  // Flow prompt steps (RFC flows 5.6): a run id chosen before the call, the
  // agent whose persona answers, and namespaced tools allowed ("*": all).
  runId?: string
  agentId?: string | null
  toolNames?: string[] | null
}

export type TurnResult = { runId: string; text: string; status: string; tainted?: boolean }

export type ExecInput = TurnOptions & {
  batchIds?: string[]
  resume?: { previousRunId: string; approvals: ApprovalRow[]; context: ResumeContext; messages: ModelMessage[] }
}

// Enqueue a message to a chat's channel.
export function sendToChat(ctx: EngineCtx, chat: ChatRow, text: string, source: string, extra: { actions?: { id: string; label: string }[]; format?: "plain" | "markdown" } = {}) {
  const now = ctx.clock()
  const row = enqueueOutbox(ctx.db, { chatId: chat.id, channel: chat.channel, connectorId: chat.connectorId, payload: { text, ...extra }, source, expiresAt: now + REPLY_TTL_MS }, now)
  notifyOutbox()
  return row
}

// One line per enabled skill or tool connector. With tool search on, a connector
// with tools still to load names them, so the model knows they exist.
function capabilityList(ctx: EngineCtx, tools: CollectedTools): string[] {
  const out: string[] = []
  const active = new Set(tools.active)
  for (const e of ctx.host.running()) {
    if (e.row.kind === "model" || e.row.kind === "channel") continue
    const metas = Object.values(tools.meta).filter((m) => m.connectorId === e.id)
    const loaded = metas.filter((m) => active.has(m.name)).length
    const waiting = metas.filter((m) => !active.has(m.name)).map((m) => m.tool)
    if (!waiting.length) out.push(loaded ? `${e.row.label} (${loaded} tools)` : e.row.label)
    else {
      const shown = waiting.slice(0, 8).join(", ") + (waiting.length > 8 ? `, +${waiting.length - 8} more` : "")
      out.push(`${e.row.label} (${loaded ? `${loaded} tools loaded, ` : ""}${waiting.length} to load with ${SEARCH_TOOL}: ${shown})`)
    }
  }
  return out
}

// Keep tools loaded by search for the rest of the session, most recent first.
function rememberLoaded(db: DB, sessionId: string, names: string[]) {
  const row = db.select({ loadedTools: sessions.loadedTools }).from(sessions).where(eq(sessions.id, sessionId)).get()
  const next = [...new Set([...names, ...(row?.loadedTools ?? [])])].slice(0, STICKY_MAX)
  db.update(sessions).set({ loadedTools: next }).where(eq(sessions.id, sessionId)).run()
}

export async function executeTurn(ctx: EngineCtx, input: ExecInput): Promise<TurnResult> {
  const { db, log } = ctx
  const startedAt = ctx.clock()
  const chat = getChat(db, input.chatId)
  if (!chat) throw new Error(`No chat ${input.chatId}`)
  const settings = getAllSettings(db)
  // The agent and whether this is a shared chat (PLAN 19). The default agent
  // in a group the owner never gave an agent keeps the old owner-only rules.
  const agentId = input.resume?.context.agentId ?? input.agentId
  const agent: AgentRow = (agentId ? getAgent(db, agentId) : undefined) ?? agentForChat(db, chat)
  const shared = !agent.isDefault && isSharedChat(db, chat)
  const deliver = input.resume ? input.resume.context.deliver : (input.deliver ?? true)
  const origin: ResumeContext["originTrigger"] = input.resume ? input.resume.context.originTrigger : input.trigger === "approval" ? "message" : input.trigger
  const tlog = log.child({ chat_id: chat.id })

  // Budgets: a hit cap pauses turns and tells the owner (PLAN 5.2).
  const budget = checkGlobalBudget(db, startedAt)
  if (!budget.ok) {
    if (shared) {
      if (budget.fresh) sendToOwner(ctx, chat, pausedNotice(db, budget))
      if (input.trigger === "message") sendToChat(ctx, chat, SHARED_BUSY_TEXT, "system")
    } else if (budget.fresh || input.trigger === "message") sendToChat(ctx, chat, pausedNotice(db, budget), "system")
    if (budget.fresh) logEvent(db, { kind: "budget.pause", level: "warn", message: `Turns paused: ${budget.reason}` }, startedAt)
    markBatch(db, input.batchIds, null)
    return { runId: "", text: "", status: "budget" }
  }
  if (agentOverCap(db, agent, startedAt)) {
    if (firstAgentNoticeToday(db, agent.id, startedAt)) {
      logEvent(db, { kind: "budget.agent", level: "warn", message: `Agent ${agent.name} hit its daily token cap (${agent.dailyTokenCap}); it answers again tomorrow` }, startedAt)
      if (input.trigger === "message") sendToChat(ctx, chat, `${agent.name} lagi istirahat, jatah hari ini udah habis. Besok lagi ya.`, "system")
    }
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
  // In the order loadHistory returns them on the next turn, so the prompt repeats.
  if (batch.size && !history.some((h) => batch.has(h.id))) {
    history = [...history, ...db.select().from(messages).where(inArray(messages.id, [...batch])).orderBy(asc(messages.createdAt), asc(messages.id)).all()]
  }
  const role: ModelRole = input.resume?.context.modelRole ?? input.modelRole ?? (hasImages(history, batch) ? "vision" : "default")
  const guestInBatch = history.some((h) => batch.has(h.id) && h.role === "user" && !h.fromOwner)
  const mode: TurnMode = input.resume?.context.mode ?? (!shared ? "owner" : guestInBatch || agent.ownerPowers === "off" ? "guest" : "sharedOwner")

  const chain = resolveChain(db, ctx.host, role)
  if (!chain.length) {
    if (input.trigger === "message") sendToChat(ctx, chat, shared ? SHARED_BUSY_TEXT : MODEL_OFF_TEXT, "system")
    markBatch(db, input.batchIds, null)
    tlog.warn("no model connector running", { role })
    return { runId: "", text: "", status: "no_model" }
  }

  const runId = input.runId ?? newId("run")
  const rlog = tlog.child({ run_id: runId })
  db.insert(runs).values({ id: runId, chatId: chat.id, agentId: agent.id, jobId: jobId ?? null, trigger: input.trigger, status: "running", startedAt }).run()
  recordRunStart(db, startedAt)
  markBatch(db, input.batchIds, runId)

  const controller = new AbortController()
  const active: ActiveTurn = { runId, controller, connectorIds: new Set() }
  ctx.active.set(chat.id, active)
  const state = { tainted: !!(input.untrustedInput || input.resume?.context.tainted || quotesOutsider(history, batch)) }
  const toolStarts = new Map<string, number>()
  const channel = chat.channel === "web" ? undefined : chat.connectorId ? ctx.host.get(chat.connectorId)?.handle?.channel : ctx.host.channel(chat.channel)
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
  let tokensCacheWrite = 0
  let steps = 0
  const approvalRequests: { approvalId: string; toolCallId: string; toolName: string; input: unknown }[] = []
  let model: ReturnType<typeof failoverModel> | undefined
  let interrupted = false
  const timeoutMs = ctx.turnTimeoutMs ?? settings.budgets.turnTimeoutMs
  // Tools that finished this turn, for the limit notice.
  const ran: RanTool[] = []
  // Set once a step was told to answer with no tools (turn-limits.ts).
  let wrapUp: WrapUpReason | null = null
  // That step called a tool anyway, which the SDK turns into an error.
  let toolChoiceViolated = false
  const failNotice = () => {
    if (shared) return sendToChat(ctx, chat, SHARED_BUSY_TEXT, "system")
    if (toolChoiceViolated) return deliverReply(ctx, chat, runId, limitNotice(wrapUp === "steps" ? { maxSteps: settings.budgets.maxSteps } : { timeoutMs }, ran))
    sendToChat(ctx, chat, `Waduh, gagal jawab: ${shortError(error)}. Coba lagi bentar ya.`, "system")
  }

  try {
    const batchRunId = input.resume ? (input.resume.context.batchRunId ?? input.resume.previousRunId) : runId
    const turnInfo: TurnInfo = { runId, chatId: chat.id, channel: chat.channel, trigger: input.trigger, tz: settings.timezone, now: startedAt, jobId, batchRunId, agentId: agent.id }
    const guest = mode === "guest"
    const noTools = input.noTools || origin === "hook" || (guest && agent.guestTools === "none")
    const scope = input.resume ? input.resume.context.toolScope : input.toolScope
    const toolNames = input.resume ? input.resume.context.toolNames : input.toolNames
    const tools: CollectedTools = noTools
      ? emptyTools()
      : await collectTools(ctx.host.running(), {
          turn: turnInfo,
          maxTools: settings.budgets.maxTools,
          scope,
          only: toolNames,
          excludeDestructive: unattended(origin),
          sticky: session?.loadedTools,
          onLoad: (names) => {
            if (chat.sessionId) rememberLoaded(db, chat.sessionId, names)
          },
          guestOnly: guest,
          log: rlog,
          onUntrustedResult: (m) => {
            if (!state.tainted) rlog.info("turn tainted by an untrusted tool result", { tool: m.name })
            state.tainted = true
          },
          onToolStart: (id) => toolStarts.set(id, ctx.clock()),
        })
    for (const id of tools.connectorIds) active.connectorIds.add(id)
    if (tools.deferred.length) rlog.info("more tools than maxTools, the rest load through tool search", { max_tools: settings.budgets.maxTools, active: tools.active.length, deferred: tools.deferred.length })

    // Messages and system prompt.
    let system: SystemModelMessage[]
    let modelMessages: ModelMessage[]
    if (input.resume) {
      // Approvals asked before the prompt was split into blocks stored one string.
      const stored = input.resume.context.system
      system = typeof stored === "string" ? [{ role: "system", content: stored }] : stored
      const responses: ToolApprovalResponse[] = input.resume.approvals.map((a) => ({
        type: "tool-approval-response",
        approvalId: a.approvalId,
        approved: a.status === "approved",
        reason: APPROVAL_REASON[a.status] ?? "Denied by the owner",
      }))
      // Reads that ran next to an approved call are older than what it changed.
      const approved = new Set(input.resume.approvals.flatMap((a) => (a.status === "approved" && a.toolCallId ? [a.toolCallId] : [])))
      modelMessages = [...markStaleReads(input.resume.messages, approved), { role: "tool", content: responses }]
    } else {
      const batchText = history
        .filter((h) => batch.has(h.id))
        .map((h) => h.text)
        .join("\n")
      const recall = input.trigger === "message" ? await autoRecall(db, findMemory(ctx.host.running()), batchText, rlog, startedAt, agent) : null
      // Stable first, volatile last (see ./context): the system blocks and the
      // history repeat from turn to turn, the clock, check-ins and recall ride
      // at the end of the newest message.
      system = systemBlocks(
        buildSystem({
          persona: agent.persona,
          channel: chat.channel,
          chat: { id: chat.id, externalId: chat.externalId, kind: chat.kind, sessionId: chat.sessionId },
          capabilities: capabilityList(ctx, tools),
          toolSearch: tools.deferred.length > 0,
          notes: notesPrompt(db, agent),
          trigger: origin,
          shared: shared ? { kind: chat.kind, guest } : null,
        }),
        input.trigger === "message" ? session?.summary : null,
      )
      const turnContext = buildTurnContext({
        now: startedAt,
        tz: settings.timezone,
        // Routine check-ins are the owner's; a shared chat never sees them.
        checkins: input.trigger === "message" && !shared ? [checkinsPrompt(db, chat.id, startedAt), flowCheckinsPrompt(db, chat.id, startedAt)].filter(Boolean).join("\n\n") || null : null,
        recall,
        recallAbout: agent.isDefault ? "owner" : "chats",
      })
      // Past tool calls are replayed only next to a tool list: a request with tool parts and no tools is rejected by some providers.
      const pastCalls = Object.keys(tools.tools).length ? loadToolCalls(db, history) : []
      const base: ModelMessage[] = input.trigger === "message" ? toModelMessages(history, batch, pastCalls, { labelSenders: shared }) : [{ role: "user", content: input.prompt ?? "" }]
      // Only a chat's history comes back on the next turn; a job prompt stands alone.
      modelMessages = withTurnContext(base, turnContext, { markTurnStart: input.trigger === "message" })
    }

    model = failoverModel(chain, (from, e) => rlog.warn("model failed, trying the next one in the chain", { connector_id: from.connectorId, model: from.model, error: (e as Error)?.message }))
    // Shared chats (PLAN 19.4): every write needs approval, like jobs.
    const strict = unattended(origin) || origin === "hook" || mode !== "owner"
    void typing(true)
    ctx.bus.publish({ type: "turn-start", chatId: chat.id, runId })
    const clock = stepClock(performance.now())
    const hasTools = Object.keys(tools.tools).length > 0

    const result = streamText({
      model,
      system,
      messages: modelMessages,
      tools: tools.tools,
      // With tool search on, only the active names reach the model; a search
      // appends to tools.active, and the next step offers the loaded tools.
      ...(tools.deferred.length ? { activeTools: [...tools.active] } : {}),
      // Near the step or time limit, the step answers with no tools, so the
      // turn ends with a reply instead of a tool call nobody reads (turn-limits.ts).
      prepareStep: ({ stepNumber, messages: stepMessages }) => {
        const active = tools.deferred.length ? { activeTools: [...tools.active] } : {}
        const reason = hasTools ? shouldWrapUp(clock, { stepNumber, maxSteps: settings.budgets.maxSteps, timeoutMs, now: performance.now() }) : null
        if (!reason) return active
        wrapUp = reason
        rlog.info("turn near its limit, asking for the reply now", { reason, step: stepNumber, elapsed_ms: Math.round(performance.now() - clock.startedAt) })
        return { ...active, toolChoice: "none" as const, messages: [...stepMessages, { role: "user" as const, content: WRAP_UP_NOTE }] }
      },
      maxRetries: 1,
      stopWhen: [isStepCount(settings.budgets.maxSteps), () => ctx.draining],
      abortSignal: AbortSignal.any([controller.signal, AbortSignal.timeout(timeoutMs)]),
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

    // A call made again replaces an older request for the same call that still waits.
    const supersede = (toolName: string, args: unknown) => {
      const r = supersedeSameCall(db, { chatId: chat.id, runId, tool: toolName, args }, ctx.clock())
      for (const a of r.superseded) logEvent(db, { kind: "approval.superseded", message: `${a.tool} was called again; the older request was dropped`, data: { approvalId: a.id, runId: a.runId } }, ctx.clock())
      for (const id of r.resume) ctx.resumeRun(id)
    }

    const noteRan = (toolName: string, ok: boolean) => {
      const meta = tools.meta[toolName]
      ran.push({ name: meta?.tool ?? toolName.split("__").pop() ?? toolName, write: (meta?.class ?? "destructive") !== "read", ok })
    }
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
          supersede(p.toolName, p.input)
          noteRan(p.toolName, true)
          finishToolCall(ctx, chat.id, runId, input.resume?.previousRunId, p.toolCallId, p.toolName, "ok", redact(preview(p.output, 500)), toolStarts)
          break
        case "tool-error":
          supersede(p.toolName, p.input)
          noteRan(p.toolName, false)
          finishToolCall(ctx, chat.id, runId, input.resume?.previousRunId, p.toolCallId, p.toolName, "error", redact(preview((p.error as Error)?.message ?? p.error, 500)), toolStarts)
          break
        case "tool-output-denied":
          finishToolCall(ctx, chat.id, runId, input.resume?.previousRunId, p.toolCallId, p.toolName, "denied", "denied", toolStarts)
          break
        case "error":
          status = "error"
          error = (p.error as Error)?.message ?? String(p.error)
          if (isToolChoiceViolation(p.error)) toolChoiceViolated = true
          break
        case "abort":
          status = "aborted"
          break
        case "finish":
          tokensIn = p.totalUsage.inputTokens ?? 0
          tokensOut = p.totalUsage.outputTokens ?? 0
          // inputTokens is all input; the details say how much of it the cache served or stored.
          tokensCached = p.totalUsage.inputTokenDetails?.cacheReadTokens ?? 0
          tokensCacheWrite = p.totalUsage.inputTokenDetails?.cacheWriteTokens ?? 0
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
        toolNames: toolNames ?? null,
        modelRole: role,
        deliver,
        silentToken: input.resume?.context.silentToken ?? input.silentToken,
        tainted: state.tainted,
        sessionId: chat.sessionId,
        system,
        batchRunId: turnInfo.batchRunId,
        agentId: agent.id,
        mode,
      }
      if (text && deliver && !silent) deliverReply(ctx, chat, runId, text)
      // A shared chat never sees the prompt: it goes to the owner's DM (PLAN 19.4).
      const askChat = shared ? (ownerChat(db, chat.channel) ?? ownerChat(db, "web") ?? chat) : chat
      for (const r of approvalRequests) {
        const meta = tools.meta[r.toolName]
        supersede(r.toolName, r.input)
        const row = createApproval(
          db,
          {
            runId,
            chatId: chat.id,
            askChatId: askChat.id === chat.id ? null : askChat.id,
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
        sendApprovalPrompt(ctx, askChat, row, meta?.title, askChat.id === chat.id ? undefined : chat)
      }
      status = "awaiting_approval"
    } else if (status === "ok") {
      if (text && deliver && !silent) deliverReply(ctx, chat, runId, text)
    } else if (status === "error" && (input.trigger === "message" || input.trigger === "approval")) {
      failNotice()
    }
  } catch (e) {
    const aborted = controller.signal.aborted || (e as Error)?.name === "AbortError" || (e as Error)?.name === "TimeoutError"
    status = aborted ? "aborted" : "error"
    error = (e as Error)?.message ?? String(e)
    if (isToolChoiceViolation(e)) toolChoiceViolated = true
    if (aborted && ctx.draining) interrupted = true
    else if (!aborted) rlog.error("turn failed", { error: e })
    if (!interrupted && status === "error" && (input.trigger === "message" || input.trigger === "approval")) failNotice()
  } finally {
    void typing(false)
    if (ctx.active.get(chat.id) === active) ctx.active.delete(chat.id)
    ctx.host.release()
  }

  if (interrupted) {
    status = "running"
    error = INTERRUPTED_ERROR
  } else if (status === "aborted") {
    if (controller.signal.aborted) error ??= "stopped by the owner"
    else {
      error = "turn timed out"
      // A timeout used to end the turn in silence; the owner gets told, and the
      // notice is saved as the reply so the next turn knows why it stopped.
      if (input.trigger === "message" || input.trigger === "approval") {
        if (shared) sendToChat(ctx, chat, SHARED_BUSY_TEXT, "system")
        else deliverReply(ctx, chat, runId, limitNotice({ timeoutMs }, ran))
      }
    }
  }
  const ms = ctx.clock() - startedAt
  const served = model?.served()
  db.update(runs)
    .set({
      status,
      error: error ? redact(error) : null,
      tokensIn,
      tokensOut,
      tokensCached,
      tokensCacheWrite,
      steps,
      tainted: state.tainted,
      model: served?.model ?? chain[0]!.model,
      modelConnectorId: served?.connectorId ?? chain[0]!.connectorId,
      resultPreview: preview(text, 300),
      ms,
    })
    .where(eq(runs.id, runId))
    .run()
  // Cache reads count for a tenth against the caps (budgetTokens).
  const spent = budgetTokens({ tokensIn, tokensOut, tokensCached })
  recordRunTokens(db, spent, jobId, ctx.clock())
  recordAgentTokens(db, agent.id, spent, ctx.clock())
  const after = checkGlobalBudget(db, ctx.clock())
  if (!after.ok && after.fresh) {
    if (shared) sendToOwner(ctx, chat, pausedNotice(db, after))
    else sendToChat(ctx, chat, pausedNotice(db, after), "system")
    logEvent(db, { kind: "budget.pause", level: "warn", message: `Turns paused: ${after.reason}` }, ctx.clock())
  }
  ctx.bus.publish({ type: "turn-end", chatId: chat.id, runId, status: interrupted ? "aborted" : status, error })
  rlog.info("turn finished", { status: interrupted ? "interrupted" : status, steps, tokens_in: tokensIn, tokens_out: tokensOut, tokens_cached: tokensCached, tokens_cache_write: tokensCacheWrite, ms, tainted: state.tainted, connector_id: served?.connectorId })
  return { runId, text: interrupted ? "" : text, status: interrupted ? "interrupted" : status, tainted: state.tainted }
}

function isToolChoiceViolation(e: unknown): boolean {
  return (e as Error | undefined)?.name === "AI_ToolChoiceViolationError"
}

// Jobs, smart reminders and flow prompt steps run with nobody watching (PLAN 7):
// no destructive tools, and every write tool needs approval.
function unattended(origin: ResumeContext["originTrigger"]): boolean {
  return origin === "schedule" || origin === "reminder" || origin === "flow"
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

// Admin notices from a shared chat go to the owner's DM instead (PLAN 19.4).
function sendToOwner(ctx: EngineCtx, from: ChatRow, text: string) {
  const to = ownerChat(ctx.db, from.channel) ?? ownerChat(ctx.db, "web")
  if (to) sendToChat(ctx, to, text, "system")
}

// `from`: the shared chat the request came from, when the prompt is asked elsewhere.
export function sendApprovalPrompt(ctx: EngineCtx, chat: ChatRow, row: ApprovalRow, title?: string, from?: ChatRow) {
  const name = title ?? row.tool.replace("__", " › ")
  const kind = row.toolClass === "destructive" ? "aksi destruktif" : "aksi yang mengubah data"
  const where = from ? ` di ${from.kind === "group" ? "grup" : "chat"} ${from.title.trim() ? `"${from.title.trim().slice(0, 60)}"` : from.externalId}` : ""
  const text = `Boleh aku jalanin ${name}${where}? (${kind})\n${row.argsPreview ?? ""}\n\nBalas ya atau batal. Kalau 10 menit gak dijawab, aku anggap batal.`
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
