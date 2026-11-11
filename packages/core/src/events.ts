// The `events` table is the single source for status: the dashboard (SSE), /health
// and chat all read it (PLAN 4.4). Writers also notify in-process listeners.
import { and, desc, eq, gt, lt, type SQL } from "drizzle-orm"
import { events, type DB, type EventLevel } from "@palui/db"
import { redact, redactDeep } from "./log"

export type EventInput = {
  level?: EventLevel
  connectorId?: string | null
  kind: string
  message: string
  data?: Record<string, unknown>
}

export type EventRow = typeof events.$inferSelect

type Listener = (e: EventRow) => void
const listeners = new Set<Listener>()

export function onEvent(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function logEvent(db: DB, e: EventInput, now = Date.now()): EventRow {
  const row = db
    .insert(events)
    .values({ at: now, level: e.level ?? "info", connectorId: e.connectorId ?? null, kind: e.kind, message: redact(e.message), data: e.data ? redactDeep(e.data) : null })
    .returning()
    .get()
  for (const l of listeners) {
    try {
      l(row)
    } catch {
      // A listener must never break the writer.
    }
  }
  return row
}

export function listEvents(db: DB, opts: { afterId?: number; connectorId?: string; level?: EventLevel; limit?: number } = {}): EventRow[] {
  const where: SQL[] = []
  if (opts.afterId !== undefined) where.push(gt(events.id, opts.afterId))
  if (opts.connectorId) where.push(eq(events.connectorId, opts.connectorId))
  if (opts.level) where.push(eq(events.level, opts.level))
  return db
    .select()
    .from(events)
    .where(where.length ? and(...where) : undefined)
    .orderBy(desc(events.id))
    .limit(opts.limit ?? 100)
    .all()
}

export function pruneEvents(db: DB, olderThanMs = 90 * 24 * 3600_000, now = Date.now()): number {
  return db.delete(events).where(lt(events.at, now - olderThanMs)).run().changes
}
