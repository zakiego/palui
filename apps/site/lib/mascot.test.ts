import { describe, expect, it } from "vitest"
import { KEEPS, STAGE_BEATS, TIMING, TYPE_LEAD, TYPE_MS, typeTime } from "./mascot"

describe("KEEPS", () => {
  it("is the three things on the slab, left to right", () => {
    expect(KEEPS.map((keep) => keep.id)).toEqual(["notes", "clock", "toolbox"])
  })

  it("keeps every gaze inside the face", () => {
    for (const keep of KEEPS) {
      for (const n of keep.gaze) expect(Math.abs(n)).toBeLessThanOrEqual(1)
    }
  })

  it("gives every thing an ask short enough for the box on a phone, and an answer", () => {
    for (const keep of KEEPS) {
      expect(keep.ask.length).toBeGreaterThan(0)
      expect(keep.ask.length).toBeLessThanOrEqual(36)
      expect(keep.reply.length).toBeGreaterThan(0)
    }
  })
})

describe("typeTime", () => {
  it("waits, then takes one beat a character", () => {
    expect(typeTime("")).toBe(TYPE_LEAD)
    expect(typeTime("abcd")).toBe(TYPE_LEAD + 4 * TYPE_MS)
  })
})

describe("STAGE_BEATS", () => {
  it("opens on the first ask being typed, never on a finished picture", () => {
    expect(STAGE_BEATS[0]).toMatchObject({ keep: 0, phase: "type" })
  })

  it("types, acts, answers and clears for each thing in order", () => {
    expect(STAGE_BEATS.map((beat) => beat.keep)).toEqual([0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2])
    for (let i = 0; i < STAGE_BEATS.length; i += 4) {
      expect(STAGE_BEATS.slice(i, i + 4).map((beat) => beat.phase)).toEqual(["type", "act", "reply", "clear"])
    }
  })

  it("holds the typing beat until the whole line is written", () => {
    for (const beat of STAGE_BEATS) {
      if (beat.phase === "type") expect(beat.wait).toBe(typeTime(KEEPS[beat.keep].ask) + TIMING.settle)
    }
  })

  it("leaves the longest act (the alarm, 1400 ms) room to finish", () => {
    expect(TIMING.act).toBeGreaterThanOrEqual(1400)
  })

  it("does not nag: a thing comes back after more than ten seconds", () => {
    const round = STAGE_BEATS.reduce((sum, beat) => sum + beat.wait, 0)
    expect(round).toBeGreaterThan(10_000)
  })
})
