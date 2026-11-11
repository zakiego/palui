import { createHmac } from "node:crypto"
import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { hooks, memoryDb } from "@palui/db"
import { ensureChat } from "../chats"
import { upsertConnector } from "../saklar/store"
import {
  createHook,
  deleteHook,
  hookBodyText,
  ingestHook,
  listHooks,
  lookupHook,
  MAX_HOOK_TEXT_CHARS,
  readHookBody,
  rotateHookSecret,
  setHookEnabled,
  signHookBody,
  summarizeHookPayload,
  verifyHookRequest,
} from "./index"

const KEY = "test-master-key-0123456789-abcdefghijklmnop"

function setup() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, 1000)
  const { hook, secret } = createHook(db, { name: "GitHub", mode: "forward", chatId: chat.id }, KEY, 2000)
  return { db, chat, hook, secret }
}

describe("createHook", () => {
  it("returns the plaintext secret once and stores it encrypted", () => {
    const { db, hook, secret } = setup()
    expect(secret).toMatch(/^phk_/)
    expect(hook.slug).toMatch(/^[A-Za-z0-9_-]{16}$/)
    expect("secret" in hook).toBe(false)
    const stored = db.select().from(hooks).where(eq(hooks.id, hook.id)).get()
    expect(stored?.secret).not.toContain(secret)
    expect(stored?.secret.startsWith("v1.")).toBe(true)
    expect(listHooks(db).map((h) => h.id)).toEqual([hook.id])
    expect(listHooks(db)[0]).not.toHaveProperty("secret")
  })

  it("refuses an unknown chat", () => {
    const db = memoryDb()
    expect(() => createHook(db, { name: "x", mode: "forward", chatId: "chat_nope" }, KEY)).toThrow(/No chat/)
  })
})

describe("verifyHookRequest", () => {
  const body = JSON.stringify({ action: "opened", number: 7 })
  const NOW = Date.parse("2026-11-10T02:00:00Z")
  const TS = String(Math.floor(NOW / 1000))
  const signed = (secret: string, raw = body, ts = TS) => ({ timestamp: ts, signature: signHookBody(secret, ts, raw) })

  it("accepts a valid signature over the timestamp and the raw body", () => {
    const { db, hook, secret } = setup()
    expect(signHookBody(secret, TS, body)).toBe(`sha256=${createHmac("sha256", secret).update(`${TS}.${body}`).digest("hex")}`)
    const res = verifyHookRequest(db, hook.slug, body, signed(secret), KEY, NOW)
    expect(res.ok).toBe(true)
  })

  it("rejects missing, wrong and rotated signatures", () => {
    const { db, hook, secret } = setup()
    expect(verifyHookRequest(db, hook.slug, body, { timestamp: TS, signature: null }, KEY, NOW)).toMatchObject({ ok: false, status: 401 })
    expect(verifyHookRequest(db, hook.slug, body, { timestamp: TS, signature: "sha256=deadbeef" }, KEY, NOW)).toMatchObject({ ok: false, status: 401 })
    expect(verifyHookRequest(db, hook.slug, `${body} `, signed(secret), KEY, NOW)).toMatchObject({ ok: false, status: 401 })
    // The legacy body-only signature is not accepted.
    const legacy = `sha256=${createHmac("sha256", secret).update(body).digest("hex")}`
    expect(verifyHookRequest(db, hook.slug, body, { timestamp: TS, signature: legacy }, KEY, NOW)).toMatchObject({ ok: false, status: 401 })
    const rotated = rotateHookSecret(db, hook.id, KEY)
    expect(rotated.secret).not.toBe(secret)
    expect(verifyHookRequest(db, hook.slug, body, signed(secret), KEY, NOW)).toMatchObject({ ok: false, status: 401 })
    expect(verifyHookRequest(db, hook.slug, body, signed(rotated.secret), KEY, NOW).ok).toBe(true)
  })

  it("requires a timestamp within 5 minutes", () => {
    const { db, hook, secret } = setup()
    expect(verifyHookRequest(db, hook.slug, body, { timestamp: null, signature: signHookBody(secret, TS, body) }, KEY, NOW)).toMatchObject({ status: 401, error: /x-palui-timestamp/ })
    expect(verifyHookRequest(db, hook.slug, body, signed(secret, body, "soon"), KEY, NOW)).toMatchObject({ status: 401 })
    expect(verifyHookRequest(db, hook.slug, body, signed(secret, body, String(Number(TS) - 301)), KEY, NOW)).toMatchObject({ status: 401, error: /5 minutes/ })
    expect(verifyHookRequest(db, hook.slug, body, signed(secret, body, String(Number(TS) + 301)), KEY, NOW)).toMatchObject({ status: 401, error: /5 minutes/ })
    expect(verifyHookRequest(db, hook.slug, body, signed(secret, body, String(Number(TS) - 299)), KEY, NOW).ok).toBe(true)
  })

  it("refuses a replay of an accepted request", () => {
    const { db, hook, secret } = setup()
    const headers = signed(secret)
    expect(verifyHookRequest(db, hook.slug, body, headers, KEY, NOW).ok).toBe(true)
    expect(verifyHookRequest(db, hook.slug, body, headers, KEY, NOW + 1000)).toMatchObject({ ok: false, status: 409 })
  })

  it("signs and verifies raw bytes", () => {
    const { db, hook, secret } = setup()
    const raw = new Uint8Array([0x22, 0xff, 0x22])
    expect(verifyHookRequest(db, hook.slug, raw, signed(secret, raw as unknown as string), KEY, NOW).ok).toBe(true)
    expect(hookBodyText(raw)).toBe('"\uFFFD"')
  })

  it("404s an unknown slug and 503s a disabled hook or a paused hooks skill", () => {
    const { db, hook, secret } = setup()
    expect(lookupHook(db, "nope")).toMatchObject({ ok: false, status: 404 })
    expect(verifyHookRequest(db, "nope", body, signed(secret), KEY, NOW)).toMatchObject({ ok: false, status: 404 })
    setHookEnabled(db, hook.id, false)
    expect(verifyHookRequest(db, hook.slug, body, signed(secret), KEY, NOW)).toMatchObject({ ok: false, status: 503 })
    setHookEnabled(db, hook.id, true)
    upsertConnector(db, { id: "hooks", manifest: { id: "hooks", kind: "skill", name: "Webhooks" }, enabled: false }, KEY)
    expect(verifyHookRequest(db, hook.slug, body, signed(secret), KEY, NOW)).toMatchObject({ ok: false, status: 503, error: "Hooks are paused" })
    upsertConnector(db, { id: "hooks", manifest: { id: "hooks", kind: "skill", name: "Webhooks" }, enabled: true }, KEY)
    expect(verifyHookRequest(db, hook.slug, body, signed(secret), KEY, NOW).ok).toBe(true)
  })

  it("rejects oversized bodies", () => {
    const { db, hook, secret } = setup()
    const big = "x".repeat(300 * 1024)
    expect(verifyHookRequest(db, hook.slug, big, signed(secret, big), KEY, NOW)).toMatchObject({ ok: false, status: 413 })
  })

  it("is gone after delete", () => {
    const { db, hook, secret } = setup()
    expect(deleteHook(db, hook.id)).toBe(true)
    expect(verifyHookRequest(db, hook.slug, body, signed(secret), KEY, NOW)).toMatchObject({ status: 404 })
  })
})

describe("readHookBody", () => {
  it("reads bytes and stops past the cap", async () => {
    const ok = await readHookBody(new Request("http://x/", { method: "POST", body: "hello" }), 10)
    expect(ok.ok && hookBodyText(ok.body)).toBe("hello")
    expect(await readHookBody(new Request("http://x/", { method: "POST", body: "x".repeat(11) }), 10)).toMatchObject({ ok: false, status: 413 })
    expect(await readHookBody(new Request("http://x/", { method: "POST", body: "x", headers: { "content-length": "999" } }), 10)).toMatchObject({ ok: false, status: 413 })
  })
})

describe("ingestHook", () => {
  it("inserts a pending user message and updates lastAt", () => {
    const { db, chat, hook, secret } = setup()
    const body = JSON.stringify({ repo: { name: "palui" }, action: "opened", labels: ["bug", "p1"] })
    const now = Date.now()
    const ts = String(Math.floor(now / 1000))
    const res = verifyHookRequest(db, hook.slug, body, { timestamp: ts, signature: signHookBody(secret, ts, body) }, KEY, now)
    if (!res.ok) throw new Error("verify failed")
    const msg = ingestHook(db, res.hook, body, 5000)
    expect(msg.status).toBe("pending")
    expect(msg.role).toBe("user")
    expect(msg.chatId).toBe(chat.id)
    expect(msg.sessionId).toBe(chat.sessionId)
    expect(msg.senderId).toBe(`hook:${hook.slug}`)
    expect(msg.content).toEqual({ text: msg.text, hook: { id: hook.id, name: "GitHub", mode: "forward" } })
    expect(msg.text).toBe("Webhook GitHub:\nrepo:\n  name: palui\naction: opened\nlabels: bug, p1")
    expect(listHooks(db)[0]?.lastAt).toBe(5000)
  })
})

describe("summarizeHookPayload", () => {
  it("passes non-JSON through and caps the length", () => {
    expect(summarizeHookPayload("Uptime", "  server down  ")).toBe("Webhook Uptime:\nserver down")
    const long = summarizeHookPayload("Big", JSON.stringify({ items: Array.from({ length: 2000 }, (_, i) => ({ i, note: "lorem ipsum" })) }))
    expect(long.length).toBe(MAX_HOOK_TEXT_CHARS)
    expect(long.endsWith("[truncated]")).toBe(true)
  })

  it("renders arrays of objects", () => {
    expect(summarizeHookPayload("x", JSON.stringify([{ a: 1 }, 2]))).toBe("Webhook x:\n-\n  a: 1\n- 2")
  })
})
