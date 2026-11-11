// SSE streams outlive the request that opened them. The session is checked
// when a stream opens, then again every `checkMs`, so a session that's revoked
// (a recovery invite deletes them all) or expired stops receiving turns and
// events within one interval instead of for as long as the tab stays open.
import { schema, type DB } from "@palui/db"
import { and, eq, gt } from "drizzle-orm"

export const SESSION_RECHECK_MS = 20_000

// True while the Better Auth session row exists and hasn't expired.
export function sessionAlive(db: DB, sessionId: string, now = Date.now()): boolean {
  const row = db
    .select({ id: schema.session.id })
    .from(schema.session)
    .where(and(eq(schema.session.id, sessionId), gt(schema.session.expiresAt, new Date(now))))
    .get()
  return !!row
}

export type GuardOptions = {
  checkMs?: number
  signal?: AbortSignal
}

const enc = new TextEncoder()
// Sent once before closing, so the client can tell a revoked session from a
// dropped connection. EventSource then reconnects, gets a 401 and stops.
export const UNAUTHORIZED_FRAME = "event: unauthorized\ndata: {}\n\n"

// Passes `source` through unchanged until `isAlive()` returns false (or
// throws), then sends one `unauthorized` frame, cancels the source and closes.
export function guardStream(
  source: ReadableStream<Uint8Array>,
  isAlive: () => boolean | Promise<boolean>,
  opts: GuardOptions = {},
): ReadableStream<Uint8Array> {
  const checkMs = opts.checkMs ?? SESSION_RECHECK_MS
  const reader = source.getReader()
  let timer: ReturnType<typeof setInterval> | undefined
  let closed = false
  const stop = () => {
    clearInterval(timer)
    reader.cancel().catch(() => {})
  }
  return new ReadableStream<Uint8Array>({
    start(controller) {
      const finish = (revoked: boolean) => {
        if (closed) return
        closed = true
        stop()
        try {
          if (revoked) controller.enqueue(enc.encode(UNAUTHORIZED_FRAME))
          controller.close()
        } catch {
          // already closed
        }
      }
      timer = setInterval(async () => {
        let alive: boolean
        try {
          alive = await isAlive()
        } catch {
          alive = false
        }
        if (!alive) finish(true)
      }, checkMs)
      opts.signal?.addEventListener("abort", () => finish(false), { once: true })
      // Pump the source into this stream. Errors end the stream quietly.
      void (async () => {
        try {
          for (;;) {
            const { done, value } = await reader.read()
            if (closed) return
            if (done) break
            controller.enqueue(value)
          }
        } catch {
          // source failed or was cancelled
        }
        finish(false)
      })()
    },
    cancel() {
      closed = true
      stop()
    },
  })
}
