import { describe, expect, it } from "vitest"
import { createIdGenerator, newId } from "./ids"

const FORMAT = /^msg_[0-9a-z]{19}$/

describe("newId", () => {
  it("makes strictly increasing ids in a tight loop", () => {
    const ids = Array.from({ length: 5000 }, () => newId("msg"))
    for (let i = 1; i < ids.length; i++) {
      if (!(ids[i - 1]! < ids[i]!)) throw new Error(`id ${i} (${ids[i]}) does not sort after id ${i - 1} (${ids[i - 1]})`)
    }
    for (const id of ids) expect(id).toMatch(FORMAT)
  })
})

describe("createIdGenerator", () => {
  const T = Date.UTC(2026, 10, 12)
  const stamp = (ms: number) => ms.toString(36).padStart(9, "0")
  const digits = (text: string) => [...text].map((c) => Number.parseInt(c, 36))

  it("draws a fresh random part when the millisecond moves forward", () => {
    const clock = { now: T }
    const draws = [digits("aaaaaaaaaa"), digits("0000000000")]
    const next = createIdGenerator({ now: () => clock.now, random: () => draws.shift()! })
    expect(next("msg")).toBe(`msg_${stamp(T)}aaaaaaaaaa`)
    clock.now = T + 1
    expect(next("msg")).toBe(`msg_${stamp(T + 1)}0000000000`)
  })

  it("counts up from the last id within one millisecond", () => {
    const next = createIdGenerator({ now: () => T, random: () => digits("000000000y") })
    expect(next("msg")).toBe(`msg_${stamp(T)}000000000y`)
    expect(next("msg")).toBe(`msg_${stamp(T)}000000000z`)
    expect(next("msg")).toBe(`msg_${stamp(T)}0000000010`)
  })

  it("carries across several places", () => {
    const next = createIdGenerator({ now: () => T, random: () => digits("a9zzzzzzzz") })
    next("msg")
    expect(next("msg")).toBe(`msg_${stamp(T)}aa00000000`)
  })

  it("keeps counting when the clock steps back", () => {
    const clock = { now: T }
    const next = createIdGenerator({ now: () => clock.now, random: () => digits("5555555555") })
    const a = next("msg")
    clock.now = T - 60_000
    const b = next("msg")
    expect(b).toBe(`msg_${stamp(T)}5555555556`)
    expect(a < b).toBe(true)
  })

  it("moves to the next millisecond when the random part overflows", () => {
    const draws = [digits("zzzzzzzzzz"), digits("3333333333")]
    const next = createIdGenerator({ now: () => T, random: () => draws.shift()! })
    const a = next("msg")
    const b = next("msg")
    const c = next("msg")
    expect(a).toBe(`msg_${stamp(T)}zzzzzzzzzz`)
    expect(b).toBe(`msg_${stamp(T + 1)}3333333333`)
    expect(c).toBe(`msg_${stamp(T + 1)}3333333334`)
    expect(a < b && b < c).toBe(true)
  })

  it("orders ids across prefixes made by one generator", () => {
    const next = createIdGenerator({ now: () => T, random: () => digits("0000000000") })
    const a = next("msg").slice(4)
    const b = next("run").slice(4)
    expect(a < b).toBe(true)
  })
})
