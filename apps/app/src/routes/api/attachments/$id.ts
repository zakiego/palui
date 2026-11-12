// GET /api/attachments/:id (auth): a saved attachment. A download by default;
// ?inline=1 shows previewable types in the browser. Headers: fileResponse.
import { masterKey } from "@palui/core/config"
import { readAttachment } from "@palui/core/attachments/service"
import type { DB } from "@palui/db"
import { createFileRoute } from "@tanstack/react-router"
import { fileResponse } from "@/server/file-response"
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
  // ?inline=1 opens it in the browser (the preview and "Open in new tab"); anything else downloads.
  const inline = new URL(request.url).searchParams.get("inline") === "1"
  return fileResponse(found.meta, found.bytes, { inline })
}

export const Route = createFileRoute("/api/attachments/$id")({
  server: {
    handlers: {
      GET: async ({ request, params }: { request: Request; params: { id: string } }) => handleAttachmentRequest(request, params.id),
    },
  },
})
