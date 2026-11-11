import { ensureChat } from "@palui/core/chats"
import { createHook, HOOK_SIGNATURE_HEADER, HOOK_TIMESTAMP_HEADER, signHookBody } from "@palui/core/hooks/index"
import { memoryDb, messages } from "@palui/db"
import { eq } from "drizzle-orm"
import { describe, expect, it, vi } from "vitest"
import { handleHookRequest, type HookRouteDeps } from "@/routes/api/hooks/$slug"
import { TEST_MASTER_KEY } from "./test-helpers"

const NOW = Date.parse("2026-11-10T02:00:00Z")
const TS = String(Math.floor(NOW / 1000))

function setup() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, 1000)
  const { hook, secret } = createHook(db, { name: "CI", mode: "forward", chatId: chat.id }, TEST_MASTER_KEY, 2000)
  const poke = vi.fn()
  const deps: HookRouteDeps = { db: () => db, masterKey: () => TEST_MASTER_KEY, poke, now: () => NOW }
  return { db, hook, secret, deps, poke }
}

function post(slug: string, body: BodyInit | null, headers: Record<string, string> = {}) {
  return new Request(`https://palui.example.com/api/hooks/${slug}`, { method: "POST", body, headers, ...(body instanceof ReadableStream ? { duplex: "half" } : {}) } as RequestInit)
}

describe("POST /api/hooks/:slug", () => {
  const body = JSON.stringify({ event: "deploy", status: "ok" })

  it("accepts a request signed over the timestamp and raw body, once", async () => {
    const { db, hook, secret, deps, poke } = setup()
    const headers = { [HOOK_TIMESTAMP_HEADER]: TS, [HOOK_SIGNATURE_HEADER]: signHookBody(secret, TS, body) }
    const res = await handleHookRequest(post(hook.slug, body, headers), hook.slug, deps)
    expect(res.status).toBe(202)
    const { id } = (await res.json()) as { id: string }
    expect(db.select().from(messages).where(eq(messages.id, id)).get()?.text).toContain("event: deploy")
    expect(poke).toHaveBeenCalledOnce()
    // The same request again is a replay.
    const again = await handleHookRequest(post(hook.slug, body, headers), hook.slug, deps)
    expect(again.status).toBe(409)
  })

  it("refuses a missing or stale timestamp and a body-only signature", async () => {
    const { hook, secret, deps } = setup()
    const sig = signHookBody(secret, TS, body)
    expect((await handleHookRequest(post(hook.slug, body, { [HOOK_SIGNATURE_HEADER]: sig }), hook.slug, deps)).status).toBe(401)
    const old = String(Number(TS) - 6 * 60)
    const stale = await handleHookRequest(post(hook.slug, body, { [HOOK_TIMESTAMP_HEADER]: old, [HOOK_SIGNATURE_HEADER]: signHookBody(secret, old, body) }), hook.slug, deps)
    expect(stale.status).toBe(401)
    expect(((await stale.json()) as { error: string }).error).toMatch(/5 minutes/)
    // Moving a captured signature to a fresh timestamp breaks it.
    const moved = await handleHookRequest(post(hook.slug, body, { [HOOK_TIMESTAMP_HEADER]: String(Number(TS) + 1), [HOOK_SIGNATURE_HEADER]: sig }), hook.slug, deps)
    expect(moved.status).toBe(401)
  })

  it("verifies over the raw bytes, not decoded text", async () => {
    const { hook, secret, deps } = setup()
    // Invalid UTF-8: decoding and re-encoding would change these bytes.
    const raw = new Uint8Array([0x7b, 0x22, 0x61, 0x22, 0x3a, 0x22, 0xff, 0xfe, 0x22, 0x7d])
    const headers = { [HOOK_TIMESTAMP_HEADER]: TS, [HOOK_SIGNATURE_HEADER]: signHookBody(secret, TS, raw) }
    expect((await handleHookRequest(post(hook.slug, raw, headers), hook.slug, deps)).status).toBe(202)
  })

  it("refuses an oversized Content-Length before looking up the slug", async () => {
    const { deps } = setup()
    const db = vi.fn(deps.db)
    const res = await handleHookRequest(post("nope", "x", { "content-length": String(300 * 1024) }), "nope", { ...deps, db })
    expect(res.status).toBe(413)
    expect(db).not.toHaveBeenCalled()
  })

  it("looks up the slug before reading the body", async () => {
    const { deps } = setup()
    let pulled = false
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        pulled = true
        c.enqueue(new TextEncoder().encode("x"))
        c.close()
      },
    }, { highWaterMark: 0 })
    const res = await handleHookRequest(post("nope", stream), "nope", deps)
    expect(res.status).toBe(404)
    expect(pulled).toBe(false)
  })

  it("cuts off a streamed body over 256 KB without a Content-Length", async () => {
    const { hook, deps } = setup()
    const chunk = new Uint8Array(64 * 1024)
    let sent = 0
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        sent++
        c.enqueue(chunk)
        if (sent > 100) c.close()
      },
    })
    const res = await handleHookRequest(post(hook.slug, stream), hook.slug, deps)
    expect(res.status).toBe(413)
    expect(sent).toBeLessThan(10)
  })
})
