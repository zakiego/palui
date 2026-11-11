// Memory (PLAN 5.3): auto-recall before a turn and auto-save at compaction and
// on /new, through the first running memory connector.
import { and, asc, eq, gt, inArray, isNotNull, or } from "drizzle-orm"
import { messages, runs, sessions, type DB } from "@palui/db"
import type { Logger } from "../log"
import type { MemoryHandle } from "../saklar/define"
import { getConnector, patchConnectorConfig } from "../saklar/store"
import type { RunningEntry } from "../saklar/supervisor"

export const RECALL_TIMEOUT_MS = 1_500
export const RECALL_MIN_WORDS = 4
const RECALL_WINDOW = 20

export type MemoryEntry = { id: string; memory: MemoryHandle }

export function findMemory(running: RunningEntry[]): MemoryEntry | undefined {
  const e = running.find((x) => x.handle.memory)
  return e ? { id: e.id, memory: e.handle.memory! } : undefined
}

export type RecallStats = { recent: boolean[]; skipped: number; total: number }

// Whether auto-recall even applies (commands and messages under 4 words don't).
export function recallApplies(text: string): boolean {
  const t = text.trim()
  if (!t || t.startsWith("/")) return false
  return t.split(/\s+/).filter(Boolean).length >= RECALL_MIN_WORDS
}

// Keep the last 20 outcomes in the connector config so the card can show
// "skipped 4 of last 20" (a skip is a timeout, an error or no profile).
export function recordRecall(db: DB, connectorId: string, skipped: boolean, now = Date.now()) {
  const row = getConnector(db, connectorId)
  if (!row) return
  const prev = (row.config.autoRecallStats as RecallStats | undefined)?.recent ?? []
  const recent = [...prev, skipped].slice(-RECALL_WINDOW)
  const stats: RecallStats = { recent, skipped: recent.filter(Boolean).length, total: recent.length }
  patchConnectorConfig(db, connectorId, { autoRecallStats: stats }, now)
}

export async function autoRecall(db: DB, entry: MemoryEntry | undefined, text: string, log: Logger, now = Date.now()): Promise<string | null> {
  if (!entry || !entry.memory.autoRecall || !recallApplies(text)) return null
  let timer: NodeJS.Timeout | undefined
  try {
    const profile = await Promise.race([
      entry.memory.recall(text, { timeoutMs: RECALL_TIMEOUT_MS }),
      new Promise<null>((resolve) => {
        timer = setTimeout(() => resolve(null), RECALL_TIMEOUT_MS + 50)
      }),
    ])
    recordRecall(db, entry.id, !profile, now)
    return profile || null
  } catch (e) {
    log.warn("auto-recall failed, going on without memory", { connector_id: entry.id, error: e })
    recordRecall(db, entry.id, true, now)
    return null
  } finally {
    if (timer) clearTimeout(timer)
  }
}

// Auto-save (PLAN 5.3, 10): only the owner's messages and final replies, never
// tool output, never tainted runs. sourceId `<sessionId>:<firstMsgId>-<lastMsgId>`
// keeps it idempotent. Returns the number of messages saved.
export async function autoSave(db: DB, entry: MemoryEntry | undefined, sessionId: string, log: Logger, until?: number): Promise<number> {
  if (!entry || !entry.memory.autoSave) return 0
  const session = db.select().from(sessions).where(eq(sessions.id, sessionId)).get()
  if (!session) return 0
  const after = session.savedToRecallAt ?? 0
  const rows = db
    .select({ id: messages.id, role: messages.role, text: messages.text, runId: messages.runId, createdAt: messages.createdAt })
    .from(messages)
    .where(
      and(
        eq(messages.sessionId, sessionId),
        gt(messages.createdAt, after),
        isNotNull(messages.runId),
        or(eq(messages.role, "user"), eq(messages.role, "assistant")),
        eq(messages.status, "handled"),
      ),
    )
    .orderBy(asc(messages.createdAt), asc(messages.id))
    .all()
    .filter((m) => until === undefined || m.createdAt <= until)
  if (!rows.length) return 0
  const runIds = [...new Set(rows.map((r) => r.runId!))]
  const tainted = new Set(
    db
      .select({ id: runs.id })
      .from(runs)
      .where(and(inArray(runs.id, runIds), eq(runs.tainted, true)))
      .all()
      .map((r) => r.id),
  )
  const keep = rows.filter((r) => !tainted.has(r.runId!) && r.text.trim())
  const last = rows.at(-1)!
  if (keep.length) {
    const content = keep.map((m) => `${m.role === "user" ? "Owner" : "Palui"}: ${m.text}`).join("\n")
    const sourceId = `${sessionId}:${keep[0]!.id}-${keep.at(-1)!.id}`
    try {
      await entry.memory.save({ sourceId, content, title: "Palui chat" })
    } catch (e) {
      log.warn("auto-save failed", { connector_id: entry.id, error: e })
      return 0
    }
  }
  db.update(sessions).set({ savedToRecallAt: last.createdAt }).where(eq(sessions.id, sessionId)).run()
  return keep.length
}
