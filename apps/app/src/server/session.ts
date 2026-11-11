// Who is calling a dashboard server function or an /api route. Server-only.
import { config } from "@palui/core/config"
import type { DB } from "@palui/db"
import { getRequest } from "@tanstack/react-start/server"
import { getAuth } from "./better-auth"
import { getDb } from "./db"

export class UnauthorizedError extends Error {
  constructor() {
    super("UNAUTHORIZED")
  }
}

export class ForbiddenError extends Error {}

export type Viewer = { sessionId: string; user: { id: string; username: string; name: string } }

export async function viewerFromHeaders(db: DB, headers: Headers): Promise<Viewer | null> {
  const s = await getAuth(db).api.getSession({ headers })
  if (!s) return null
  return {
    sessionId: s.session.id,
    user: { id: s.user.id, username: (s.user as { username?: string | null }).username ?? s.user.name, name: s.user.name },
  }
}

export function getViewer() {
  return viewerFromHeaders(getDb(), getRequest().headers)
}

export async function requireUser() {
  const db = getDb()
  const viewer = await viewerFromHeaders(db, getRequest().headers)
  if (!viewer) throw new UnauthorizedError()
  return { db, viewer }
}

// For raw /api routes: a 401 Response when there's no session.
export async function requireUserFromRequest(request: Request): Promise<{ db: DB; viewer: Viewer } | Response> {
  const db = getDb()
  const viewer = await viewerFromHeaders(db, request.headers)
  if (!viewer) return Response.json({ error: "UNAUTHORIZED" }, { status: 401 })
  return { db, viewer }
}

// Writes from the dashboard must come from the dashboard's own origin.
export function checkSameOrigin(headers: Headers) {
  if (headers.get("sec-fetch-site") === "cross-site") throw new ForbiddenError("cross-origin request refused")
  const origin = headers.get("origin")
  if (!origin) return
  let originHost: string
  try {
    originHost = new URL(origin).host
  } catch {
    throw new ForbiddenError("cross-origin request refused")
  }
  if (originHost === new URL(config().appUrl).host) return
  const host = headers.get("x-forwarded-host") ?? headers.get("host")
  if (!host || originHost !== host) throw new ForbiddenError("cross-origin request refused")
}

export function requireSameOrigin() {
  checkSameOrigin(getRequest().headers)
}

// A signed-in write: same origin, then the session.
export async function requireWriter() {
  requireSameOrigin()
  return requireUser()
}
