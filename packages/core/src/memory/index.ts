// Memory (PLAN 5.3): auto-recall before a turn and auto-save at compaction and
// on /new, through the first running memory connector.
import { and, asc, eq, gt, inArray, isNotNull, or } from "drizzle-orm"
import { chats, messages, runs, sessions, type DB } from "@palui/db"
import { agentContainer, getAgent, getDefaultAgent, type AgentRow } from "../agents"
import type { Logger } from "../log"
import type { MemoryHandle } from "../connectors/define"
import { getConnector, patchConnectorConfig } from "../connectors/store"
import type { RunningEntry } from "../connectors/supervisor"

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

// `agent`: whose memory to read (PLAN 19.5). The default agent reads the owner's.
export async function autoRecall(db: DB, entry: MemoryEntry | undefined, text: string, log: Logger, now = Date.now(), agent?: AgentRow): Promise<string | null> {
  if (!entry || !entry.memory.autoRecall || !recallApplies(text)) return null
  const container = agent ? agentContainer(agent) : undefined
  const opts = { timeoutMs: RECALL_TIMEOUT_MS, ...(container ? { container } : {}), ...(agent && !agent.isDefault ? { about: "the people in your chats" } : {}) }
  let timer: NodeJS.Timeout | undefined
  try {
    const profile = await Promise.race([
      entry.memory.recall(text, opts),
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

// Auto-save (PLAN 5.3, 10, 19.5): owner messages, guest messages labeled with
// who said them and where, and final replies. Never tool output, never runs
// tainted by an untrusted tool result. Each run's agent decides the container
// (a chat can switch agents mid-session). sourceId
// `<sessionId>:<firstMsgId>-<lastMsgId>` keeps it idempotent. Returns the number
// of messages saved.
export async function autoSave(db: DB, entry: MemoryEntry | undefined, sessionId: string, log: Logger, until?: number): Promise<number> {
  if (!entry || !entry.memory.autoSave) return 0
  const session = db.select().from(sessions).where(eq(sessions.id, sessionId)).get()
  if (!session) return 0
  const chat = db.select().from(chats).where(eq(chats.id, session.chatId)).get()
  const after = session.savedToRecallAt ?? 0
  const rows = db
    .select({
      id: messages.id,
      role: messages.role,
      text: messages.text,
      runId: messages.runId,
      createdAt: messages.createdAt,
      fromOwner: messages.fromOwner,
      senderName: messages.senderName,
      senderId: messages.senderId,
    })
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
  const runRows = db
    .select({ id: runs.id, tainted: runs.tainted, agentId: runs.agentId })
    .from(runs)
    .where(inArray(runs.id, runIds))
    .all()
  const tainted = new Set(runRows.filter((r) => r.tainted).map((r) => r.id))
  const agentOf = new Map(runRows.map((r) => [r.id, r.agentId]))
  const keep = rows.filter((r) => !tainted.has(r.runId!) && r.text.trim())
  const last = rows.at(-1)!
  // Group by agent, in order of first appearance.
  const groups = new Map<string, typeof keep>()
  const fallback = getDefaultAgent(db)
  for (const r of keep) {
    const id = agentOf.get(r.runId!) ?? fallback.id
    groups.set(id, [...(groups.get(id) ?? []), r])
  }
  const where = chat?.title?.trim() ? chat.title.trim().slice(0, 80) : "a chat"
  for (const [agentId, group] of groups) {
    const agent = getAgent(db, agentId) ?? fallback
    const label = (m: (typeof keep)[number]) => {
      if (m.role === "assistant") return agent.isDefault ? "Palui" : agent.name
      if (m.fromOwner) return "Owner"
      return `${(m.senderName ?? m.senderId ?? "Someone").replace(/\s+/g, " ").slice(0, 60)} (in ${where})`
    }
    const content = group.map((m) => `${label(m)}: ${m.text}`).join("\n")
    const base = `${sessionId}:${group[0]!.id}-${group.at(-1)!.id}`
    const sourceId = groups.size > 1 ? `${base}:${agent.slug}` : base
    const container = agentContainer(agent)
    try {
      await entry.memory.save({ sourceId, content, title: agent.isDefault ? "Palui chat" : `${agent.name} chat`, ...(container ? { container } : {}) })
    } catch (e) {
      log.warn("auto-save failed", { connector_id: entry.id, error: e })
      return 0
    }
  }
  db.update(sessions).set({ savedToRecallAt: last.createdAt }).where(eq(sessions.id, sessionId)).run()
  return keep.length
}
