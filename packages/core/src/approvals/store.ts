// Pending tool approvals (PLAN 8.6). A turn that ends with tool-approval-request
// parts is saved here with everything needed to resume it, so it survives a
// restart. All requests from one run are answered together in one resume call.
import { and, asc, eq, inArray, lte } from "drizzle-orm"
import type { ModelMessage } from "ai"
import { newId, pendingApprovals, runs, toolCalls, type DB, type ToolClass } from "@palui/db"

export const APPROVAL_TTL_MS = 10 * 60_000

export type ApprovalRow = typeof pendingApprovals.$inferSelect

// What a resume needs besides the messages (stored in `context`).
export type ResumeContext = {
  trigger: "message" | "schedule" | "hook" | "approval" | "reminder"
  // The trigger of the turn that asked; schedule and hook keep their stricter policy on resume.
  originTrigger: "message" | "schedule" | "hook" | "reminder"
  jobId?: string
  toolScope?: string[] | null
  modelRole: "default" | "fast" | "vision"
  deliver: boolean
  silentToken?: string
  tainted: boolean
  sessionId: string | null
  system: string
  // Set by markResumed once the run is claimed for resuming.
  resumedAt?: number
}

export function createApproval(
  db: DB,
  input: {
    runId: string
    chatId: string
    approvalId: string
    toolCallId: string
    tool: string
    toolClass: ToolClass
    argsPreview: string
    messages: ModelMessage[]
    context: ResumeContext
  },
  now = Date.now(),
  ttlMs = APPROVAL_TTL_MS,
): ApprovalRow {
  return db
    .insert(pendingApprovals)
    .values({
      id: newId("apr"),
      runId: input.runId,
      chatId: input.chatId,
      approvalId: input.approvalId,
      toolCallId: input.toolCallId,
      tool: input.tool,
      toolClass: input.toolClass,
      argsPreview: input.argsPreview,
      messages: input.messages as unknown[],
      context: input.context as unknown as Record<string, unknown>,
      expiresAt: now + ttlMs,
      status: "pending",
      createdAt: now,
    })
    .returning()
    .get()
}

export function getApproval(db: DB, id: string): ApprovalRow | undefined {
  return db.select().from(pendingApprovals).where(eq(pendingApprovals.id, id)).get()
}

export function pendingForChat(db: DB, chatId: string): ApprovalRow[] {
  return db
    .select()
    .from(pendingApprovals)
    .where(and(eq(pendingApprovals.chatId, chatId), eq(pendingApprovals.status, "pending")))
    .orderBy(asc(pendingApprovals.createdAt))
    .all()
}

export function approvalsForRun(db: DB, runId: string): ApprovalRow[] {
  return db.select().from(pendingApprovals).where(eq(pendingApprovals.runId, runId)).orderBy(asc(pendingApprovals.createdAt)).all()
}

// Record a decision on a pending row. False when it was already decided or expired.
export function decide(db: DB, id: string, approved: boolean, now = Date.now()): boolean {
  return (
    db
      .update(pendingApprovals)
      .set({ status: approved ? "approved" : "denied", decidedAt: now })
      .where(and(eq(pendingApprovals.id, id), eq(pendingApprovals.status, "pending")))
      .run().changes > 0
  )
}

// Expire due rows; returns the run ids that now have every request decided.
export function expireDue(db: DB, now = Date.now()): string[] {
  const rows = db
    .update(pendingApprovals)
    .set({ status: "expired", decidedAt: now })
    .where(and(eq(pendingApprovals.status, "pending"), lte(pendingApprovals.expiresAt, now)))
    .returning()
    .all()
  return [...new Set(rows.map((r) => r.runId))]
}

export const SUPERSEDED_PREVIEW = "dropped: the same call was made again before the owner answered"

// Sorted-key JSON, so the same arguments compare equal in whatever order the model wrote them.
function canonical(v: unknown): string {
  return JSON.stringify(v, (_k, x: unknown) => (x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => (a < b ? -1 : 1))) : x)) ?? ""
}

// The model made a call again (same tool, same arguments) while an older request
// for it still waits in this chat: the older one is moot. It's marked superseded
// and its call denied, so the owner gets no late "that didn't run" follow-up
// about something already asked again. A run left with only superseded requests
// is closed here. `resume` lists the runs that had another request answered and
// are now fully decided: they still owe a resume.
export function supersedeSameCall(db: DB, input: { chatId: string; runId: string; tool: string; args: unknown }, now = Date.now()): { superseded: ApprovalRow[]; resume: string[] } {
  const superseded: ApprovalRow[] = []
  for (const r of pendingForChat(db, input.chatId)) {
    if (r.tool !== input.tool || r.runId === input.runId) continue
    const callId = `${r.runId}:${r.toolCallId}`
    const prior = db.select({ args: toolCalls.args }).from(toolCalls).where(eq(toolCalls.id, callId)).get()
    if (!prior || canonical(prior.args) !== canonical(input.args)) continue
    db.transaction((tx) => {
      tx.update(pendingApprovals).set({ status: "superseded", decidedAt: now }).where(eq(pendingApprovals.id, r.id)).run()
      tx.update(toolCalls).set({ status: "denied", resultPreview: SUPERSEDED_PREVIEW }).where(eq(toolCalls.id, callId)).run()
    })
    superseded.push(r)
  }
  const resume: string[] = []
  for (const runId of new Set(superseded.map((r) => r.runId))) {
    const rows = approvalsForRun(db, runId)
    if (rows.some((r) => r.status === "pending")) continue
    if (rows.some((r) => r.status !== "superseded")) resume.push(runId)
    else if (markResumed(db, runId, now)) db.update(runs).set({ status: "ok" }).where(eq(runs.id, runId)).run()
  }
  return { superseded, resume }
}

// Runs whose requests are all decided but not resumed yet (after a restart).
// A resumed row keeps its decision status and gets context.resumedAt.
export function readyToResume(db: DB): string[] {
  const rows = db
    .select()
    .from(pendingApprovals)
    .where(inArray(pendingApprovals.status, ["approved", "denied", "expired", "superseded", "pending"]))
    .all()
  const byRun = new Map<string, ApprovalRow[]>()
  for (const r of rows) byRun.set(r.runId, [...(byRun.get(r.runId) ?? []), r])
  return [...byRun.entries()].filter(([, rs]) => rs.every((r) => r.status !== "pending" && !r.context.resumedAt)).map(([id]) => id)
}

// Claim a run for resuming. False when another caller already resumed it.
export function markResumed(db: DB, runId: string, now = Date.now()): boolean {
  return db.transaction((tx) => {
    const rows = tx.select().from(pendingApprovals).where(eq(pendingApprovals.runId, runId)).all()
    if (!rows.length || rows.some((r) => r.status === "pending" || r.context.resumedAt)) return false
    for (const r of rows) {
      tx.update(pendingApprovals)
        .set({ context: { ...r.context, resumedAt: now } })
        .where(eq(pendingApprovals.id, r.id))
        .run()
    }
    return true
  })
}

export function preview(value: unknown, max = 300): string {
  let s: string
  try {
    s = typeof value === "string" ? value : JSON.stringify(value)
  } catch {
    s = String(value)
  }
  if (s === undefined) s = ""
  return s.length > max ? `${s.slice(0, max - 1)}…` : s
}
