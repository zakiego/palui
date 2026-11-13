import { describe, expect, it } from "vitest"
import { parseAckEmoji } from "./ack"

describe("parseAckEmoji", () => {
  it("takes one emoji, including skin tones, ZWJ sequences and flags", () => {
    expect(parseAckEmoji("👀")).toBe("👀")
    expect(parseAckEmoji(" ✍️ ")).toBe("✍️")
    expect(parseAckEmoji("👍🏽")).toBe("👍🏽")
    expect(parseAckEmoji("👨‍👩‍👧‍👦")).toBe("👨‍👩‍👧‍👦")
    expect(parseAckEmoji("🇮🇩")).toBe("🇮🇩")
  })

  it("rejects text, several emoji and nothing", () => {
    expect(parseAckEmoji("")).toBeNull()
    expect(parseAckEmoji("ok")).toBeNull()
    expect(parseAckEmoji("a")).toBeNull()
    expect(parseAckEmoji("👀👀")).toBeNull()
    expect(parseAckEmoji("👀 ok")).toBeNull()
  })
})
