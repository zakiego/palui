// GET /api/chat/stream (auth): proxies the runtime's /control/stream SSE (turn
// events: text deltas, tool calls, approvals) to the browser. The control token
// never leaves the server. The session is re-checked while the stream is open
// and the stream closes once it's revoked or expired.
import { createFileRoute } from "@tanstack/react-router"
import { sseHeaders } from "@/server/events-stream"
import { RUNTIME_DOWN, runtime } from "@/server/runtime"
import { requireUserFromRequest } from "@/server/session"
import { guardStream, sessionAlive } from "@/server/stream-guard"

export const Route = createFileRoute("/api/chat/stream")({
  server: {
    handlers: {
      GET: async ({ request }: { request: Request }) => {
        const auth = await requireUserFromRequest(request)
        if (auth instanceof Response) return auth
        let upstream: Response
        try {
          upstream = await runtime().stream(request.signal)
        } catch {
          return Response.json({ error: RUNTIME_DOWN }, { status: 503 })
        }
        if (!upstream.ok || !upstream.body) return Response.json({ error: RUNTIME_DOWN }, { status: 503 })
        const { db, viewer } = auth
        const body = guardStream(upstream.body, () => sessionAlive(db, viewer.sessionId), { signal: request.signal })
        return new Response(body, { headers: sseHeaders() })
      },
    },
  },
})
