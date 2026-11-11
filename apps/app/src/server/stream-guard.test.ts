import { logEvent } from "@palui/core/events"
import { memoryDb, schema } from "@palui/db"
import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { eventStream } from "./events-stream"
import { guardStream, sessionAlive, UNAUTHORIZED_FRAME } from "./stream-guard"

const dec = new TextDecoder()
const enc = new TextEncoder()

function seedSession(db: ReturnType<typeof memoryDb>, expiresAt: number) {
  const now = new Date(1_700_000_000_000)
  db.insert(schema.user).values({ id: "u1", name: "Owner", email: "owner@palui.local", emailVerified: true, createdAt: now, updatedAt: now }).run()
  db.insert(schema.session)
    .values({ id: "s1", token: "tok", userId: "u1", expiresAt: new Date(expiresAt), createdAt: now, updatedAt: now })
    .run()
}

describe("sessionAlive", () => {
  it("is true for a live session and false once it's deleted or expired", () => {
    const db = memoryDb()
    seedSession(db, 2_000)
    expect(sessionAlive(db, "s1", 1_000)).toBe(true)
    expect(sessionAlive(db, "s1", 2_000)).toBe(false)
    expect(sessionAlive(db, "nope", 1_000)).toBe(false)
    db.delete(schema.session).where(eq(schema.session.userId, "u1")).run()
    expect(sessionAlive(db, "s1", 1_000)).toBe(false)
  })
})

describe("guardStream", () => {
  it("passes data through while alive, then sends unauthorized, closes and cancels the source", async () => {
    let alive = true
    let cancelled = false
    let push: ((s: string) => void) | undefined
    const source = new ReadableStream<Uint8Array>({
      start(c) {
        push = (s) => c.enqueue(enc.encode(s))
      },
      cancel() {
        cancelled = true
      },
    })
    const reader = guardStream(source, () => alive, { checkMs: 10 }).getReader()
    push?.("event: a\n\n")
    expect(dec.decode((await reader.read()).value)).toBe("event: a\n\n")
    alive = false
    expect(dec.decode((await reader.read()).value)).toBe(UNAUTHORIZED_FRAME)
    expect((await reader.read()).done).toBe(true)
    expect(cancelled).toBe(true)
  })

  it("treats a failing check as revoked", async () => {
    const source = new ReadableStream<Uint8Array>()
    const reader = guardStream(source, () => Promise.reject(new Error("db gone")), { checkMs: 10 }).getReader()
    expect(dec.decode((await reader.read()).value)).toBe(UNAUTHORIZED_FRAME)
    expect((await reader.read()).done).toBe(true)
  })

  it("closes without the unauthorized frame when the request aborts", async () => {
    const ac = new AbortController()
    const reader = guardStream(new ReadableStream<Uint8Array>(), () => true, { checkMs: 10, signal: ac.signal }).getReader()
    ac.abort()
    expect((await reader.read()).done).toBe(true)
  })

  it("stops the events stream once the recovery invite deletes the session", async () => {
    const db = memoryDb()
    seedSession(db, Date.now() + 60_000)
    const ac = new AbortController()
    const events = eventStream(db, { intervalMs: 5, signal: ac.signal })
    const reader = guardStream(events, () => sessionAlive(db, "s1"), { checkMs: 15 }).getReader()
    expect(dec.decode((await reader.read()).value)).toMatch(/^event: summary/)
    logEvent(db, { kind: "a", message: "before" })
    expect(dec.decode((await reader.read()).value)).toMatch(/event: events/)
    db.delete(schema.session).where(eq(schema.session.userId, "u1")).run()
    const rest: string[] = []
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      rest.push(dec.decode(value))
    }
    expect(rest.at(-1)).toBe(UNAUTHORIZED_FRAME)
    logEvent(db, { kind: "b", message: "after" })
    expect(rest.join("")).not.toContain("after")
    ac.abort()
  })
})
