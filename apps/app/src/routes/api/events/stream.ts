// GET /api/events/stream (auth): new `events` rows over SSE, polled by id every
// second, plus a status summary whenever a connector changed. Drives the header
// status and live cards. The session is re-checked while the stream is open and
// the stream closes once it's revoked or expired.
import { createFileRoute } from "@tanstack/react-router"
import { eventStream, sseHeaders } from "@/server/events-stream"
import { requireUserFromRequest } from "@/server/session"
import { guardStream, sessionAlive } from "@/server/stream-guard"

export const Route = createFileRoute("/api/events/stream")({
  server: {
    handlers: {
      GET: async ({ request }: { request: Request }) => {
        const auth = await requireUserFromRequest(request)
        if (auth instanceof Response) return auth
        // EventSource resends the last `id:` it saw after a reconnect.
        const after = Number(request.headers.get("last-event-id") ?? new URL(request.url).searchParams.get("after") ?? Number.NaN)
        const stream = eventStream(auth.db, { afterId: Number.isFinite(after) ? after : undefined, signal: request.signal })
        const body = guardStream(stream, () => sessionAlive(auth.db, auth.viewer.sessionId), { signal: request.signal })
        return new Response(body, { headers: sseHeaders() })
      },
    },
  },
})
