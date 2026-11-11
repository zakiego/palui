// Inbound webhooks (PLAN 5.5, 10). Each hook has a random URL slug and its own
// HMAC secret. The app handles `POST /api/hooks/:slug` in this order: refuse a
// Content-Length over 256 KB, look the slug up (lookupHook), read the body as bytes
// with a hard cap (readHookBody), then verify the signature over the raw bytes
// (verifyHookSignature). ingestHook then writes a `pending` message into the hook's
// chat whose content is `{ text, hook: { id, name, mode } }` and pokes the runtime.
// The engine treats it as untrusted: `forward` sends the text as is, `summarize`
// runs with zero tools. A hook in mode `flow` skips the message and starts flow
// runs instead (flows/entry.ts ingestFlowHook).
//
// Signing: x-palui-timestamp is the unix time in seconds, and x-palui-signature is
// `sha256=<hex HMAC-SHA256 of "<timestamp>.<raw body>">`. Timestamps more than 5
// minutes off are refused, and a signature is accepted once.
import { createHmac } from "node:crypto"
import { desc, eq } from "drizzle-orm"
import { chats, connectors, hooks, messages, newId, randomToken, type DB } from "@palui/db"
import { decrypt, encrypt, safeEqual } from "@palui/db/crypto"

// flow: the body starts the flows whose webhook trigger names this hook (RFC flows 4.1).
export type HookMode = "forward" | "summarize" | "flow"
export type HookRow = typeof hooks.$inferSelect
// What the dashboard gets: never the secret, not even encrypted.
export type HookView = Omit<HookRow, "secret">
export type MessageRow = typeof messages.$inferSelect

export const HOOK_SIGNATURE_HEADER = "x-palui-signature"
export const HOOK_TIMESTAMP_HEADER = "x-palui-timestamp"
// How far a request's timestamp may be from now, either way.
export const HOOK_MAX_SKEW_MS = 5 * 60_000
// The hooks skill connector (manifest id "hooks"). Pausing it makes every hook answer 503.
export const HOOKS_CONNECTOR_ID = "hooks"
export const MAX_HOOK_BODY_BYTES = 256 * 1024
export const MAX_HOOK_TEXT_CHARS = 8000

function view(row: HookRow): HookView {
  const { secret: _secret, ...rest } = row
  return rest
}

function newSecret(): string {
  return `phk_${randomToken(32)}`
}

export function createHook(
  db: DB,
  input: { name: string; mode: HookMode; chatId: string },
  masterKey: string,
  now = Date.now(),
): { hook: HookView; secret: string } {
  const name = input.name.trim()
  if (!name) throw new Error("Hook name is required")
  if (!db.select({ id: chats.id }).from(chats).where(eq(chats.id, input.chatId)).get()) throw new Error(`No chat ${input.chatId}`)
  const secret = newSecret()
  const row = db
    .insert(hooks)
    .values({
      id: newId("hook"),
      // 16 url-safe characters, 96 bits: unguessable, and short enough to paste.
      slug: randomToken(12),
      name,
      secret: encrypt(secret, masterKey),
      mode: input.mode,
      chatId: input.chatId,
      enabled: true,
      createdAt: now,
    })
    .returning()
    .get()
  return { hook: view(row), secret }
}

export function rotateHookSecret(db: DB, id: string, masterKey: string): { hook: HookView; secret: string } {
  const secret = newSecret()
  const row = db.update(hooks).set({ secret: encrypt(secret, masterKey) }).where(eq(hooks.id, id)).returning().get()
  if (!row) throw new Error(`No hook ${id}`)
  return { hook: view(row), secret }
}

export function listHooks(db: DB): HookView[] {
  return db.select().from(hooks).orderBy(desc(hooks.createdAt)).all().map(view)
}

export function getHookBySlug(db: DB, slug: string): HookRow | undefined {
  return db.select().from(hooks).where(eq(hooks.slug, slug)).get()
}

export function setHookEnabled(db: DB, id: string, enabled: boolean): HookView | undefined {
  const row = db.update(hooks).set({ enabled }).where(eq(hooks.id, id)).returning().get()
  return row ? view(row) : undefined
}

export function deleteHook(db: DB, id: string): boolean {
  return db.delete(hooks).where(eq(hooks.id, id)).run().changes > 0
}

export type HookBody = Uint8Array | string

function bytesOf(body: HookBody): Buffer {
  return typeof body === "string" ? Buffer.from(body, "utf8") : Buffer.from(body.buffer, body.byteOffset, body.byteLength)
}

// The x-palui-signature value for a request: `sha256=<hex HMAC-SHA256>` over the
// timestamp (unix seconds, as sent in x-palui-timestamp), a ".", and the raw body
// bytes exactly as sent.
export function signHookBody(secret: string, timestamp: number | string, rawBody: HookBody): string {
  return `sha256=${createHmac("sha256", secret).update(`${timestamp}.`, "utf8").update(bytesOf(rawBody)).digest("hex")}`
}

export type HookFailure = { ok: false; status: number; error: string }
export type HookLookupResult = { ok: true; hook: HookRow } | HookFailure
export type HookVerifyResult = { ok: true; hook: HookRow } | HookFailure
export type HookHeaders = { signature: string | null; timestamp: string | null }

// Step 1, before the body is read: the hook exists and is on. A paused hooks
// connector answers 503 for every hook.
export function lookupHook(db: DB, slug: string): HookLookupResult {
  const hook = getHookBySlug(db, slug)
  if (!hook) return { ok: false, status: 404, error: "No such hook" }
  const skill = db.select({ enabled: connectors.enabled }).from(connectors).where(eq(connectors.id, HOOKS_CONNECTOR_ID)).get()
  if (skill && !skill.enabled) return { ok: false, status: 503, error: "Hooks are paused" }
  if (!hook.enabled) return { ok: false, status: 503, error: "This hook is turned off" }
  return { ok: true, hook }
}

// Step 2: read the body as bytes and stop as soon as it passes `max`, so a huge
// upload is never buffered whole.
export async function readHookBody(request: Request, max = MAX_HOOK_BODY_BYTES): Promise<{ ok: true; body: Uint8Array } | HookFailure> {
  const tooLarge: HookFailure = { ok: false, status: 413, error: "Body is too large" }
  const length = request.headers.get("content-length")
  if (length !== null && (!/^\d+$/.test(length.trim()) || Number(length) > max)) return tooLarge
  if (!request.body) return { ok: true, body: new Uint8Array(0) }
  const reader = request.body.getReader()
  const parts: Uint8Array[] = []
  let total = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    total += value.byteLength
    if (total > max) {
      await reader.cancel().catch(() => {})
      return tooLarge
    }
    parts.push(value)
  }
  const out = new Uint8Array(total)
  let at = 0
  for (const p of parts) {
    out.set(p, at)
    at += p.byteLength
  }
  return { ok: true, body: out }
}

// The body as text for ingestHook (invalid UTF-8 becomes U+FFFD).
export function hookBodyText(body: Uint8Array): string {
  return new TextDecoder("utf-8").decode(body)
}

// Signatures accepted in the last few minutes, per hook: a captured request can't be
// replayed while its timestamp is still inside the window. The app is one process,
// so memory is enough; after a restart the timestamp window still applies.
const seen = new Map<string, number>()

function claimSignature(key: string, now: number): boolean {
  if (seen.size > 1000) {
    for (const [k, exp] of seen) if (exp <= now) seen.delete(k)
    if (seen.size > 5000) seen.clear()
  }
  const exp = seen.get(key)
  if (exp !== undefined && exp > now) return false
  seen.set(key, now + 2 * HOOK_MAX_SKEW_MS)
  return true
}

// Step 3: size, timestamp and signature over the raw bytes. Every failure is a
// sentence the app can return as is. The signature check is timing safe. A request
// that passes is recorded, so the same signature is refused if it comes again.
export function verifyHookSignature(hook: HookRow, rawBody: HookBody, headers: HookHeaders, masterKey: string, now = Date.now()): HookVerifyResult {
  const body = bytesOf(rawBody)
  if (body.byteLength > MAX_HOOK_BODY_BYTES) return { ok: false, status: 413, error: "Body is too large" }
  if (!headers.timestamp) return { ok: false, status: 401, error: `Missing ${HOOK_TIMESTAMP_HEADER} header (unix seconds)` }
  const ts = headers.timestamp.trim()
  if (!/^\d{1,12}$/.test(ts)) return { ok: false, status: 401, error: `${HOOK_TIMESTAMP_HEADER} must be unix time in seconds` }
  if (Math.abs(now - Number(ts) * 1000) > HOOK_MAX_SKEW_MS) {
    return { ok: false, status: 401, error: `${HOOK_TIMESTAMP_HEADER} is more than 5 minutes off. Sign each request when you send it.` }
  }
  if (!headers.signature) return { ok: false, status: 401, error: `Missing ${HOOK_SIGNATURE_HEADER} header` }
  let secret: string
  try {
    secret = decrypt(hook.secret, masterKey)
  } catch {
    return { ok: false, status: 500, error: "Hook secret can't be read (MASTER_KEY changed?)" }
  }
  const expected = signHookBody(secret, ts, body)
  const got = headers.signature.trim().toLowerCase()
  if (!safeEqual(got, expected)) return { ok: false, status: 401, error: "Bad signature" }
  if (!claimSignature(`${hook.id}:${expected}`, now)) return { ok: false, status: 409, error: "This request was already received" }
  return { ok: true, hook }
}

// Steps 1 and 3 together, for callers that already hold the whole body.
export function verifyHookRequest(db: DB, slug: string, rawBody: HookBody, headers: HookHeaders, masterKey: string, now = Date.now()): HookVerifyResult {
  const found = lookupHook(db, slug)
  if (!found.ok) return found
  return verifyHookSignature(found.hook, rawBody, headers, masterKey, now)
}

// --- Payload to text -------------------------------------------------------------------

const MAX_DEPTH = 6

function scalar(v: unknown): string {
  if (v === null) return "null"
  if (typeof v === "string") return v.replace(/\s+/g, " ").trim()
  return String(v)
}

function isScalar(v: unknown): boolean {
  return v === null || ["string", "number", "boolean"].includes(typeof v)
}

function render(value: unknown, indent: string, depth: number, out: string[]) {
  if (depth > MAX_DEPTH) {
    out.push(`${indent}...`)
    return
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      if (isScalar(item)) out.push(`${indent}- ${scalar(item)}`)
      else {
        out.push(`${indent}-`)
        render(item, `${indent}  `, depth + 1, out)
      }
    }
    return
  }
  if (value && typeof value === "object") {
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      if (isScalar(v)) out.push(`${indent}${k}: ${scalar(v)}`)
      else if (Array.isArray(v) && v.every(isScalar)) out.push(`${indent}${k}: ${v.map(scalar).join(", ")}`)
      else {
        out.push(`${indent}${k}:`)
        render(v, `${indent}  `, depth + 1, out)
      }
    }
    return
  }
  out.push(`${indent}${scalar(value)}`)
}

// A readable rendering of a hook payload: JSON becomes indented `key: value`
// lines, anything else is passed through. Capped at MAX_HOOK_TEXT_CHARS.
export function summarizeHookPayload(name: string, rawBody: string): string {
  let body: string
  try {
    const parsed: unknown = JSON.parse(rawBody)
    const lines: string[] = []
    render(parsed, "", 0, lines)
    body = lines.join("\n")
  } catch {
    body = rawBody.trim()
  }
  const text = `Webhook ${name}:\n${body || "(empty body)"}`
  if (text.length <= MAX_HOOK_TEXT_CHARS) return text
  const marker = "\n[truncated]"
  return text.slice(0, MAX_HOOK_TEXT_CHARS - marker.length) + marker
}

// Insert the event as a pending inbound message in the hook's chat.
export function ingestHook(db: DB, hook: HookRow, rawBody: string, now = Date.now()): MessageRow {
  const text = summarizeHookPayload(hook.name, rawBody)
  return db.transaction((tx) => {
    const chat = tx.select().from(chats).where(eq(chats.id, hook.chatId)).get()
    if (!chat) throw new Error(`Hook ${hook.id} points at a missing chat ${hook.chatId}`)
    const row = tx
      .insert(messages)
      .values({
        id: newId("msg"),
        chatId: hook.chatId,
        sessionId: chat.sessionId,
        role: "user",
        content: { text, hook: { id: hook.id, name: hook.name, mode: hook.mode } },
        text,
        senderId: `hook:${hook.slug}`,
        status: "pending",
        createdAt: now,
      })
      .returning()
      .get()
    tx.update(hooks).set({ lastAt: now }).where(eq(hooks.id, hook.id)).run()
    return row
  })
}
