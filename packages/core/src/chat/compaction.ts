// Compaction (PLAN 8.9): past about 40 messages, the fast model folds the older
// ones into sessions.summary and they leave the context (they stay in the DB).
// Auto-save to the memory connector runs here too.
import { and, asc, eq, gt, isNotNull, or } from "drizzle-orm"
import { generateText } from "ai"
import { messages, sessions } from "@palui/db"
import { getSession } from "../chats"
import { autoSave, findMemory } from "../memory"
import { failoverModel, resolveChain } from "../models/failover"
import { meterToBudget } from "../models/meter"
import type { EngineCtx } from "./turn"

export const COMPACT_AFTER = 40
export const KEEP_AFTER_COMPACTION = 20
// `/compact`: fold everything but the last few messages, however short the session.
export const KEEP_AFTER_MANUAL = 6

// `short`: nothing old enough to fold. `no_model`: no fast model is running.
export type CompactResult = { status: "ok"; folded: number } | { status: "short" | "no_model" | "failed" | "no_session" }

// After a turn: compact once the session is past COMPACT_AFTER messages.
export async function maybeCompact(ctx: EngineCtx, sessionId: string): Promise<boolean> {
  return (await compactSession(ctx, sessionId, { after: COMPACT_AFTER, keep: KEEP_AFTER_COMPACTION })).status === "ok"
}

// Fold the session's messages into its summary when there are more than
// `after`, keeping the newest `keep` in context.
export async function compactSession(ctx: EngineCtx, sessionId: string, opts: { after: number; keep: number }): Promise<CompactResult> {
  const { db, log } = ctx
  const session = getSession(db, sessionId)
  if (!session) return { status: "no_session" }
  const rows = db
    .select()
    .from(messages)
    .where(
      and(
        eq(messages.sessionId, sessionId),
        gt(messages.createdAt, session.summarizedUntil ?? 0),
        eq(messages.status, "handled"),
        isNotNull(messages.runId),
        or(eq(messages.role, "user"), eq(messages.role, "assistant")),
      ),
    )
    .orderBy(asc(messages.createdAt), asc(messages.id))
    .all()
  if (rows.length <= opts.after) return { status: "short" }
  const fold = rows.slice(0, rows.length - opts.keep)
  const until = fold.at(-1)!.createdAt
  const chain = resolveChain(db, ctx.host, "fast")
  if (!chain.length) {
    log.warn("compaction skipped: no fast model running", { session_id: sessionId })
    return { status: "no_model" }
  }
  const transcript = fold.map((m) => `${m.role === "user" ? "Owner" : "Palui"}: ${m.text}`).join("\n")
  try {
    const r = await generateText({
      model: meterToBudget(db, failoverModel(chain), ctx.clock),
      system:
        "You compress a chat between the owner and their assistant Palui into a short running summary for Palui's context. Keep facts, decisions, open tasks, names, dates and preferences. Drop small talk. Write in the conversation's language, at most 250 words. Output only the summary.",
      prompt: `${session.summary ? `Earlier summary:\n${session.summary}\n\n` : ""}New messages:\n${transcript}`,
      maxRetries: 1,
      abortSignal: AbortSignal.timeout(60_000),
    })
    const summary = r.text.trim()
    if (!summary) return { status: "failed" }
    db.update(sessions).set({ summary, summarizedUntil: until }).where(eq(sessions.id, sessionId)).run()
  } catch (e) {
    log.warn("compaction failed", { session_id: sessionId, error: e })
    return { status: "failed" }
  }
  await autoSave(db, findMemory(ctx.host.running()), sessionId, log, until)
  return { status: "ok", folded: fold.length }
}
