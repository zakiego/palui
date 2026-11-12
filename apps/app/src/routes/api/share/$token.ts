// GET /api/share/:token: a temporary link to one saved attachment (see
// @palui/core/attachments/shares). No session check for "public" links; the
// token is the auth. "private" links also need a dashboard sign-in. Previewable
// files open in the browser, the rest download; ?download=1 always downloads.
import { readAttachment } from "@palui/core/attachments/service"
import { recordOpen, resolveShare } from "@palui/core/attachments/shares"
import { masterKey } from "@palui/core/config"
import type { DB } from "@palui/db"
import { createFileRoute } from "@tanstack/react-router"
import { getDb } from "@/server/db"
import { fileResponse } from "@/server/file-response"
import { requireUserFromRequest } from "@/server/session"

export type ShareRouteDeps = {
  db: () => DB
  signedIn: (request: Request) => Promise<boolean>
  masterKey: () => string
  now: () => number
}

const defaultDeps: ShareRouteDeps = {
  db: getDb,
  signedIn: async (request) => !((await requireUserFromRequest(request)) instanceof Response),
  masterKey: () => masterKey(),
  now: Date.now,
}

// Plain text so nothing on this public path can render as a page.
function notice(status: number, text: string): Response {
  return new Response(`${text}\n`, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8", "x-content-type-options": "nosniff", "cache-control": "no-store", "referrer-policy": "no-referrer" },
  })
}

export async function handleShareRequest(request: Request, token: string, deps: ShareRouteDeps = defaultDeps): Promise<Response> {
  const db = deps.db()
  const now = deps.now()
  const found = resolveShare(db, token, now)
  if (!found.ok) {
    if (found.reason === "expired") return notice(410, "This link has expired. Ask for a new one.")
    if (found.reason === "revoked") return notice(410, "This link was turned off. Ask for a new one.")
    return notice(404, "This link doesn't exist.")
  }
  if (found.share.visibility === "private" && !(await deps.signedIn(request))) {
    return notice(401, "This link is private. Sign in to the dashboard first, then open the link again.")
  }
  const file = readAttachment(db, found.attachment.id, deps.masterKey())
  if (!file) return notice(404, "This link doesn't exist.")
  recordOpen(db, found.share.id, now)
  const download = new URL(request.url).searchParams.get("download") === "1"
  return fileResponse(file.meta, file.bytes, { inline: !download })
}

export const Route = createFileRoute("/api/share/$token")({
  server: {
    handlers: {
      GET: async ({ request, params }: { request: Request; params: { token: string } }) => handleShareRequest(request, params.token),
    },
  },
})
