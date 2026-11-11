import { describe, expect, it } from "vitest"
import { beatAt, KEEPS, TIMING } from "./mascot"

describe("KEEPS", () => {
  it("is the three things on the slab, left to right", () => {
    expect(KEEPS.map((keep) => keep.id)).toEqual(["notes", "clock", "toolbox"])
  })

  it("keeps every gaze inside the face", () => {
    for (const keep of KEEPS) {
      for (const n of keep.gaze) expect(Math.abs(n)).toBeLessThanOrEqual(1)
    }
  })
})

describe("beatAt", () => {
  it("opens on a rest, so the first paint is the still picture", () => {
    expect(beatAt(0)).toEqual({ focus: null, wait: TIMING.first })
  })

  it("gives each thing the stage in order, then starts over", () => {
    const looks = [1, 3, 5, 7, 9].map((n) => beatAt(n).focus)
    expect(looks).toEqual([0, 1, 2, 0, 1])
  })

  it("rests between two acts", () => {
    for (let n = 2; n < 20; n += 2) expect(beatAt(n)).toEqual({ focus: null, wait: TIMING.rest })
    for (let n = 1; n < 20; n += 2) expect(beatAt(n).wait).toBe(TIMING.look)
  })

  it("keeps moving: the first act starts within two seconds and no rest outlasts an act", () => {
    expect(TIMING.first).toBeLessThanOrEqual(2000)
    expect(TIMING.rest).toBeLessThan(TIMING.look)
  })

  it("does not nag: a thing comes back after more than ten seconds", () => {
    const round = KEEPS.length * (TIMING.look + TIMING.rest)
    expect(round).toBeGreaterThan(10_000)
  })
})
