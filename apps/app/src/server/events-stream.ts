// Live status for the dashboard (PLAN 11.5): new `events` rows streamed over SSE.
// The runtime writes events from another process, so this polls by id every
// second instead of listening in-process.
import { listConnectors } from "@palui/core/saklar/store"
import { events, type DB } from "@palui/db"
import { asc, desc, gt } from "drizzle-orm"

export type EventRow = typeof events.$inferSelect

export function latestEventId(db: DB): number {
  return db.select({ id: events.id }).from(events).orderBy(desc(events.id)).limit(1).get()?.id ?? 0
}

// Rows after `afterId`, oldest first, and the cursor to poll from next.
export function nextEvents(db: DB, afterId: number, limit = 200): { rows: EventRow[]; cursor: number } {
  const rows = db.select().from(events).where(gt(events.id, afterId)).orderBy(asc(events.id)).limit(limit).all()
  return { rows, cursor: rows.at(-1)?.id ?? afterId }
}

export type StatusSummary = {
  total: number
  on: number
  attention: { id: string; label: string; status: string }[]
}

// "All on" or "2 need attention". Paused is a choice, so it isn't attention;
// an error, or something enabled that isn't running, is.
export function statusSummary(db: DB): StatusSummary {
  const rows = listConnectors(db).filter((c) => c.status !== "not_set_up")
  const attention = rows
    .filter((c) => c.status === "error" || (c.enabled && c.status === "paused"))
    .map((c) => ({ id: c.id, label: c.label, status: c.status }))
  return { total: rows.length, on: rows.filter((c) => c.status === "on").length, attention }
}

const enc = new TextEncoder()
// `id:` carries the event cursor, so a reconnecting EventSource sends it back
// as Last-Event-ID and resumes without missing rows.
const frame = (event: string, data: unknown, id?: number) => enc.encode(`${id !== undefined ? `id: ${id}\n` : ""}event: ${event}\ndata: ${JSON.stringify(data)}\n\n`)

export function sseHeaders(): HeadersInit {
  return {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache, no-transform",
    connection: "keep-alive",
    // Cloudflare and nginx buffer responses unless told not to.
    "x-accel-buffering": "no",
  }
}

export type EventStreamOptions = {
  afterId?: number
  intervalMs?: number
  keepaliveMs?: number
  signal?: AbortSignal
}

// `summary` goes out first and again whenever a batch touched a connector, then
// every new row as an `events` frame.
export function eventStream(db: DB, opts: EventStreamOptions = {}): ReadableStream<Uint8Array> {
  const intervalMs = opts.intervalMs ?? 1_000
  const keepaliveMs = opts.keepaliveMs ?? 15_000
  let cursor = opts.afterId ?? latestEventId(db)
  let timer: ReturnType<typeof setInterval> | undefined
  let lastWrite = Date.now()
  return new ReadableStream<Uint8Array>({
    start(controller) {
      const close = () => {
        clearInterval(timer)
        try {
          controller.close()
        } catch {
          // already closed
        }
      }
      controller.enqueue(frame("summary", statusSummary(db)))
      timer = setInterval(() => {
        try {
          const { rows, cursor: next } = nextEvents(db, cursor)
          cursor = next
          if (rows.length) {
            controller.enqueue(frame("events", rows, cursor))
            if (rows.some((r) => r.connectorId)) controller.enqueue(frame("summary", statusSummary(db)))
            lastWrite = Date.now()
          } else if (Date.now() - lastWrite >= keepaliveMs) {
            controller.enqueue(enc.encode(": keepalive\n\n"))
            lastWrite = Date.now()
          }
        } catch {
          close()
        }
      }, intervalMs)
      opts.signal?.addEventListener("abort", close, { once: true })
    },
    cancel() {
      clearInterval(timer)
    },
  })
}
