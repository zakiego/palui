import { generateText, streamText } from "ai"
import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { tokensToday } from "../budget"
import { say, scriptedModel } from "../chat/testing"
import { meteredModel, meterToBudget } from "./meter"

describe("meteredModel", () => {
  it("reports tokens from generate and stream calls", async () => {
    const seen: number[] = []
    const m = scriptedModel([say("a"), say("b")])
    const model = meteredModel(m.model, (n) => seen.push(n))
    await generateText({ model, prompt: "x" })
    const s = streamText({ model, prompt: "y" })
    for await (const _ of s.fullStream) {
      // drain
    }
    // scriptedModel reports 10 input + 5 output tokens per call.
    expect(seen).toEqual([15, 15])
  })

  it("adds parse and compaction tokens to the daily cap counter", async () => {
    const db = memoryDb()
    const now = Date.UTC(2026, 10, 10, 6)
    const m = scriptedModel([say("summary")])
    await generateText({ model: meterToBudget(db, m.model, () => now), prompt: "x" })
    expect(tokensToday(db, now)).toBe(15)
  })
})
