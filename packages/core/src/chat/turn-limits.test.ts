import { describe, expect, it } from "vitest"
import { formatLimit, limitNotice, shouldWrapUp, stepClock } from "./turn-limits"

describe("shouldWrapUp", () => {
  const opts = { maxSteps: 12, timeoutMs: 120_000 }

  it("never wraps up the first step", () => {
    expect(shouldWrapUp(stepClock(0), { ...opts, stepNumber: 0, now: 0 })).toBeNull()
    expect(shouldWrapUp(stepClock(0), { ...opts, maxSteps: 1, stepNumber: 0, now: 0 })).toBeNull()
  })

  it("wraps up the last allowed step", () => {
    const c = stepClock(0)
    expect(shouldWrapUp(c, { ...opts, maxSteps: 3, stepNumber: 1, now: 1000 })).toBeNull()
    expect(shouldWrapUp(c, { ...opts, maxSteps: 3, stepNumber: 2, now: 2000 })).toBe("steps")
  })

  // The incident in rpt_0mwcdzchvhscqv9o16o: steps of 10s, 47s, 44s, 4s, then a
  // timeout at 120s with no reply. The third step should have answered.
  it("wraps up when the slowest step so far wouldn't fit in the time left", () => {
    const c = stepClock(0)
    expect(shouldWrapUp(c, { ...opts, stepNumber: 0, now: 0 })).toBeNull()
    expect(shouldWrapUp(c, { ...opts, stepNumber: 1, now: 10_500 })).toBeNull()
    expect(shouldWrapUp(c, { ...opts, stepNumber: 2, now: 57_300 })).toBe("time")
  })

  it("keeps going while fast steps leave plenty of time", () => {
    const c = stepClock(0)
    for (let i = 0; i < 8; i++) expect(shouldWrapUp(c, { ...opts, stepNumber: i, now: i * 3000 })).toBeNull()
  })
})

describe("limitNotice", () => {
  it("lists what ran, counts repeats and says nothing changed", () => {
    const text = limitNotice({ timeoutMs: 120_000 }, [
      { name: "lihat_flow", write: false, ok: true },
      { name: "uji_flow", write: false, ok: true },
      { name: "uji_flow", write: false, ok: true },
    ])
    expect(text).toContain("batas 2 menit")
    expect(text).toContain("Yang sempat jalan: lihat_flow, uji_flow (2x).")
    expect(text).toContain("Belum ada yang diubah.")
    expect(text).toContain('Bilang "lanjut"')
  })

  it("names the writes that went through", () => {
    const text = limitNotice({ timeoutMs: 90_000 }, [
      { name: "buat_flow", write: true, ok: true },
      { name: "hapus_flow", write: true, ok: false },
    ])
    expect(text).toContain("batas 90 detik")
    expect(text).toContain("Yang udah ngubah sesuatu: buat_flow.")
    expect(text).not.toContain("Belum ada yang diubah")
  })

  it("skips the tool line when nothing ran", () => {
    expect(limitNotice({ timeoutMs: 60_000 }, [])).not.toContain("Yang sempat jalan")
    expect(limitNotice({ maxSteps: 12 }, [])).toContain("batas 12 langkah")
  })

  it("formats the limit", () => {
    expect(formatLimit(600_000)).toBe("10 menit")
    expect(formatLimit(45_000)).toBe("45 detik")
  })
})
