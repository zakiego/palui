// Pending tool approvals (PLAN 8.6). A turn that ends with tool-approval-request
// parts is saved here with everything needed to resume it, so it survives a
// restart. All requests from one run are answered together in one resume call.
import { and, asc, eq, inArray, lte } from "drizzle-orm"
import type { ModelMessage } from "ai"
import { newId, pendingApprovals, type DB, type ToolClass } from "@palui/db"

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

// Runs whose requests are all decided but not resumed yet (after a restart).
// A resumed row keeps its decision status and gets context.resumedAt.
export function readyToResume(db: DB): string[] {
  const rows = db
    .select()
    .from(pendingApprovals)
    .where(inArray(pendingApprovals.status, ["approved", "denied", "expired", "pending"]))
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
