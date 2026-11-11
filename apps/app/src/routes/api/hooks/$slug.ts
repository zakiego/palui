// POST /api/hooks/:slug: inbound webhooks (PLAN 5.5, AGENTS.md "Inbound hooks").
// No session: the HMAC signature is the auth. It covers x-palui-timestamp and the
// raw body bytes, and old timestamps and repeated signatures are refused. The
// cheap checks run before the body is read: Content-Length, then the slug. A
// verified event becomes a `pending` message in the hook's chat, and the runtime
// is poked so it handles it now instead of on its next sweep.
import { createFileRoute } from "@tanstack/react-router"
import type { DB } from "@palui/db"
import { masterKey } from "@palui/core/config"
import {
  HOOK_SIGNATURE_HEADER,
  HOOK_TIMESTAMP_HEADER,
  hookBodyText,
  ingestHook,
  lookupHook,
  MAX_HOOK_BODY_BYTES,
  readHookBody,
  verifyHookSignature,
} from "@palui/core/hooks/index"
import { HOOK_HEADERS, ingestFlowHook } from "@palui/core/flows/entry"
import { getDb } from "@/server/db"
import { runtime } from "@/server/runtime"

export type HookRouteDeps = { db: () => DB; masterKey: () => string; poke: () => void; now: () => number }

const defaultDeps: HookRouteDeps = {
  db: getDb,
  masterKey,
  poke: () => {
    runtime()
      .reconcile()
      .catch(() => {})
  },
  now: Date.now,
}

const fail = (status: number, error: string) => Response.json({ ok: false, error }, { status })

export async function handleHookRequest(request: Request, slug: string, deps: HookRouteDeps = defaultDeps): Promise<Response> {
  // 1. Refuse an announced oversized body before touching the database.
  const length = request.headers.get("content-length")
  if (length !== null && (!/^\d+$/.test(length.trim()) || Number(length) > MAX_HOOK_BODY_BYTES)) return fail(413, "Body is too large")
  // 2. The slug, before reading anything.
  const db = deps.db()
  const found = lookupHook(db, slug)
  if (!found.ok) return fail(found.status, found.error)
  // 3. The body as bytes, cut off at the cap even without a Content-Length.
  const read = await readHookBody(request)
  if (!read.ok) return fail(read.status, read.error)
  // 4. Timestamp and signature over the raw bytes.
  const headers = { signature: request.headers.get(HOOK_SIGNATURE_HEADER), timestamp: request.headers.get(HOOK_TIMESTAMP_HEADER) }
  const result = verifyHookSignature(found.hook, read.body, headers, deps.masterKey(), deps.now())
  if (!result.ok) return fail(result.status, result.error)
  // Mode flow (RFC flows 4.1): the body starts the flows listening to this hook.
  if (result.hook.mode === "flow") {
    const raw = hookBodyText(read.body)
    const headerMap = Object.fromEntries(HOOK_HEADERS.map((h) => [h, request.headers.get(h)]))
    const r = ingestFlowHook(db, result.hook, raw, headerMap, deps.now())
    deps.poke()
    return Response.json({ ok: true, runs: r.started }, { status: 202 })
  }
  const msg = ingestHook(db, result.hook, hookBodyText(read.body), deps.now())
  deps.poke()
  return Response.json({ ok: true, id: msg.id }, { status: 202 })
}

export const Route = createFileRoute("/api/hooks/$slug")({
  server: {
    handlers: {
      POST: ({ request, params }: { request: Request; params: { slug: string } }) => handleHookRequest(request, params.slug),
    },
  },
})
