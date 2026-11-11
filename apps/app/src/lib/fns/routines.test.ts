// The routines server functions as the dashboard calls them: no session means no
// data and no writes, and a cross-site write is refused before it touches a row.
// createServerFn and the session helpers are replaced so the handlers run in node.
import { createRoutine, getRoutine } from "@palui/core/routines/service"
import { MEDICINE_ROUTINE } from "@palui/core/routines/test-helpers"
import { chats, memoryDb, type DB } from "@palui/db"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { setRuntime } from "@/server/runtime"
import { upRuntime } from "@/server/test-helpers"

const auth = vi.hoisted(() => ({ db: null as unknown, signedIn: true, sameOrigin: true }))

vi.mock("@tanstack/react-start", () => ({
  createServerFn: () => {
    let schema: { parse: (v: unknown) => unknown } | undefined
    const builder = {
      validator(s: typeof schema) {
        schema = s
        return builder
      },
      handler(fn: (o: { data: unknown }) => unknown) {
        return (opts: { data?: unknown } = {}) => Promise.resolve().then(() => fn({ data: schema ? schema.parse(opts.data) : undefined }))
      },
    }
    return builder
  },
}))

vi.mock("@/server/session", () => {
  class UnauthorizedError extends Error {}
  const requireUser = async () => {
    if (!auth.signedIn) throw new UnauthorizedError("UNAUTHORIZED")
    return { db: auth.db, viewer: { sessionId: "s", user: { id: "u", username: "owner", name: "Owner" } } }
  }
  return {
    requireUser,
    requireWriter: async () => {
      if (!auth.sameOrigin) throw new Error("cross-origin request refused")
      return requireUser()
    },
  }
})

const { deleteRoutineFn, getRoutine: getRoutineFn, getRoutines, pauseRoutineFn, resumeRoutineFn } = await import("./routines")

const NOW = Date.now()
let db: DB
let id: string

beforeEach(() => {
  db = memoryDb()
  db.insert(chats).values({ id: "chat_1", channel: "telegram", externalId: "1", kind: "dm", title: "Owner", createdAt: NOW }).run()
  id = createRoutine(db, "chat_1", MEDICINE_ROUTINE, NOW).id
  Object.assign(auth, { db, signedIn: true, sameOrigin: true })
  setRuntime(upRuntime())
})

afterEach(() => setRuntime(undefined))

describe("routines server functions", () => {
  it("read the list and one routine when signed in", async () => {
    const list = await getRoutines()
    expect(list.routines.map((r) => r.id)).toEqual([id])
    const one = await getRoutineFn({ data: { id } })
    expect(one.detail?.routine.name).toBe("Minum obat")
    expect((await getRoutineFn({ data: { id: "rtn_nope" } })).detail).toBeNull()
  })

  it("refuse every read and write without a session", async () => {
    auth.signedIn = false
    await expect(getRoutines()).rejects.toThrow("UNAUTHORIZED")
    await expect(getRoutineFn({ data: { id } })).rejects.toThrow("UNAUTHORIZED")
    await expect(pauseRoutineFn({ data: { id } })).rejects.toThrow("UNAUTHORIZED")
    await expect(resumeRoutineFn({ data: { id } })).rejects.toThrow("UNAUTHORIZED")
    await expect(deleteRoutineFn({ data: { id } })).rejects.toThrow("UNAUTHORIZED")
    expect(getRoutine(db, id)).toMatchObject({ enabled: true })
  })

  it("refuse a cross-site write before touching the routine", async () => {
    auth.sameOrigin = false
    await expect(pauseRoutineFn({ data: { id } })).rejects.toThrow("cross-origin")
    await expect(deleteRoutineFn({ data: { id } })).rejects.toThrow("cross-origin")
    expect(getRoutine(db, id)).toMatchObject({ enabled: true })
  })

  it("pause, pause until, resume and delete", async () => {
    await pauseRoutineFn({ data: { id } })
    expect(getRoutine(db, id)).toMatchObject({ enabled: false, pausedUntil: null })
    const until = new Date(NOW + 3 * 24 * 3600_000).toISOString().slice(0, 16)
    const res = await pauseRoutineFn({ data: { id, until } })
    expect(res.pausedUntil).toBeGreaterThan(NOW)
    await resumeRoutineFn({ data: { id } })
    expect(getRoutine(db, id)).toMatchObject({ enabled: true, pausedUntil: null })
    await deleteRoutineFn({ data: { id } })
    expect(getRoutine(db, id)).toBeUndefined()
    await expect(deleteRoutineFn({ data: { id } })).rejects.toThrow("doesn't exist anymore")
  })

  it("validate the input", async () => {
    await expect(pauseRoutineFn({ data: { id, until: "tomorrow" } })).rejects.toThrow()
    await expect(getRoutineFn({ data: { id, from: "11/05/2026" } })).rejects.toThrow()
  })
})
