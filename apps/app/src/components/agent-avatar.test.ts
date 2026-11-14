import { describe, expect, it } from "vitest"
import { AGENT_COLORS, AGENT_ICONS } from "@/shared/constants"
import { AVATAR_COLORS, AVATAR_ICONS } from "./agent-avatar"

describe("agent avatar choices", () => {
  it("offers exactly the icons and colors core accepts", () => {
    expect(Object.keys(AVATAR_ICONS)).toEqual([...AGENT_ICONS])
    expect([...AVATAR_COLORS]).toEqual([...AGENT_COLORS])
  })
})
