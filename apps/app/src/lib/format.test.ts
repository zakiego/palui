import { describe, expect, it } from "vitest"
import { clockTime, noBreak } from "./format"

describe("clockTime", () => {
  it("joins the time and AM or PM with a non-breaking space", () => {
    // A local timestamp, so the output is the same in every zone.
    const at = new Date(2026, 10, 11, 3, 42).getTime()
    expect(clockTime(at)).toBe("3:42\u00A0AM")
  })
})

describe("noBreak", () => {
  it("swaps a plain space for a non-breaking one", () => {
    expect(noBreak("3:42\u0020AM")).toBe("3:42\u00A0AM")
  })

  it("swaps a narrow no-break space for a non-breaking one", () => {
    expect(noBreak("3:42\u202FAM")).toBe("3:42\u00A0AM")
  })
})
