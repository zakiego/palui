import { describe, expect, it, vi } from "vitest"
import { createViewerCache, FRESH_MS, MAX_AGE_MS } from "./viewer-cache"

type Viewer = { id: string }

const T0 = 1_000_000

function setup(answers: (Viewer | null | Error)[]) {
  const load = vi.fn(async () => {
    const next = answers.shift()
    if (next instanceof Error) throw next
    return next ?? null
  })
  return { load, cache: createViewerCache<Viewer>(load) }
}

// Lets a background recheck settle.
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

describe("createViewerCache", () => {
  it("asks the server once, then answers from memory while fresh", async () => {
    const { load, cache } = setup([{ id: "a" }])
    expect(await cache.get(T0)).toEqual({ id: "a" })
    expect(await cache.get(T0 + FRESH_MS - 1)).toEqual({ id: "a" })
    expect(load).toHaveBeenCalledTimes(1)
  })

  it("shares one request between callers that arrive together", async () => {
    const { load, cache } = setup([{ id: "a" }])
    const all = await Promise.all([cache.get(T0), cache.get(T0), cache.get(T0)])
    expect(all).toEqual([{ id: "a" }, { id: "a" }, { id: "a" }])
    expect(load).toHaveBeenCalledTimes(1)
  })

  it("answers from memory and rechecks behind the scenes once it is no longer fresh", async () => {
    const { load, cache } = setup([{ id: "a" }, { id: "b" }])
    await cache.get(T0)
    expect(await cache.get(T0 + FRESH_MS)).toEqual({ id: "a" })
    expect(load).toHaveBeenCalledTimes(2)
    await settle()
    expect(await cache.get(T0 + FRESH_MS + 1)).toEqual({ id: "b" })
    expect(load).toHaveBeenCalledTimes(2)
  })

  it("waits for the server once the entry is too old to trust", async () => {
    const { load, cache } = setup([{ id: "a" }, null])
    await cache.get(T0)
    expect(await cache.get(T0 + MAX_AGE_MS)).toBeNull()
    expect(load).toHaveBeenCalledTimes(2)
  })

  it("never remembers a signed-out answer", async () => {
    const { load, cache } = setup([null, { id: "a" }])
    expect(await cache.get(T0)).toBeNull()
    expect(await cache.get(T0 + 1)).toEqual({ id: "a" })
    expect(load).toHaveBeenCalledTimes(2)
  })

  it("drops the entry and tells listeners when a recheck finds the session gone", async () => {
    const { load, cache } = setup([{ id: "a" }, null, null])
    const lost = vi.fn()
    cache.onLost(lost)
    await cache.get(T0)
    await cache.get(T0 + FRESH_MS)
    await settle()
    expect(lost).toHaveBeenCalledTimes(1)
    expect(await cache.get(T0 + FRESH_MS + 1)).toBeNull()
    expect(load).toHaveBeenCalledTimes(3)
    // Signed out to begin with is not a session that went away.
    expect(lost).toHaveBeenCalledTimes(1)
  })

  it("keeps the entry when a recheck fails", async () => {
    const { cache } = setup([{ id: "a" }, new Error("offline"), { id: "a" }])
    await cache.get(T0)
    expect(await cache.get(T0 + FRESH_MS)).toEqual({ id: "a" })
    await settle()
    expect(await cache.get(T0 + FRESH_MS + 1)).toEqual({ id: "a" })
  })

  it("seeds only an empty cache", async () => {
    const { load, cache } = setup([])
    cache.seed({ id: "a" }, T0)
    cache.seed({ id: "b" }, T0 + 1)
    expect(await cache.get(T0 + 2)).toEqual({ id: "a" })
    expect(load).not.toHaveBeenCalled()
  })

  it("forgets on clear, and an answer already on its way can't come back", async () => {
    let release: (viewer: Viewer) => void = () => {}
    const load = vi
      .fn<() => Promise<Viewer | null>>()
      .mockImplementationOnce(() => new Promise((resolve) => (release = resolve)))
      .mockResolvedValueOnce({ id: "new" })
    const cache = createViewerCache<Viewer>(load)
    const first = cache.get(T0)
    cache.clear()
    release({ id: "old" })
    await first
    expect(await cache.get(T0 + 1)).toEqual({ id: "new" })
    expect(load).toHaveBeenCalledTimes(2)
  })
})
