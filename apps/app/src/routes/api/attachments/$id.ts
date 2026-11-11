// GET /api/attachments/:id (auth): download a saved attachment. Always served
// as a download (Content-Disposition: attachment) with nosniff and a sandbox
// CSP, so a file can never render as a page on the dashboard's origin.
import { masterKey } from "@palui/core/config"
import { downloadName, readAttachment } from "@palui/core/attachments/service"
import type { DB } from "@palui/db"
import { createFileRoute } from "@tanstack/react-router"
import { requireUserFromRequest } from "@/server/session"

export type AttachmentRouteDeps = {
  // The signed-in DB, or a 401 Response.
  auth: (request: Request) => Promise<{ db: DB } | Response>
  masterKey: () => string
}

const defaultDeps: AttachmentRouteDeps = { auth: requireUserFromRequest, masterKey: () => masterKey() }

export async function handleAttachmentRequest(request: Request, id: string, deps: AttachmentRouteDeps = defaultDeps): Promise<Response> {
  const auth = await deps.auth(request)
  if (auth instanceof Response) return auth
  const found = /^att_[a-z0-9]+$/i.test(id) ? readAttachment(auth.db, id, deps.masterKey()) : undefined
  if (!found) return Response.json({ error: "NOT_FOUND" }, { status: 404 })
  const name = downloadName(found.meta)
  const ascii = name.replace(/[^\x20-\x7e]/g, "_")
  return new Response(new Uint8Array(found.bytes), {
    headers: {
      "content-type": found.meta.mimeType,
      "content-length": String(found.bytes.length),
      "content-disposition": `attachment; filename="${ascii}"; filename*=UTF-8''${encodeURIComponent(name)}`,
      "x-content-type-options": "nosniff",
      "content-security-policy": "default-src 'none'; sandbox",
      "cache-control": "private, no-store",
    },
  })
}

export const Route = createFileRoute("/api/attachments/$id")({
  server: {
    handlers: {
      GET: async ({ request, params }: { request: Request; params: { id: string } }) => handleAttachmentRequest(request, params.id),
    },
  },
})
