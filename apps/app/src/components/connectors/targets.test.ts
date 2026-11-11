import { describe, expect, it } from "vitest"

import { manifestFor } from "@/lib/manifests"
import { presetFor } from "@/lib/presets"
import { draftTarget, isDraftId, newConnectorId } from "./targets"

const mcp = manifestFor("mcp")!
const telegram = manifestFor("telegram")!

describe("newConnectorId", () => {
  it("uses the manifest id for a single connector", () => {
    expect(newConnectorId(telegram, [])).toBe("telegram")
  })

  it("numbers past ids already taken", () => {
    const linear = presetFor(mcp, "linear")
    expect(newConnectorId(mcp, [], linear)).toBe("mcp-linear")
    expect(newConnectorId(mcp, [{ id: "mcp-linear" }, { id: "mcp-linear-2" }], linear)).toBe("mcp-linear-3")
    expect(newConnectorId(mcp, [])).toBe("mcp-server")
  })
})

describe("isDraftId", () => {
  it("accepts only ids the catalog could have made", () => {
    expect(isDraftId(telegram, "telegram")).toBe(true)
    expect(isDraftId(telegram, "telegram-2")).toBe(false)
    expect(isDraftId(mcp, "mcp-linear-2")).toBe(true)
    expect(isDraftId(mcp, "mcp")).toBe(false)
    expect(isDraftId(mcp, "mcp-Bad Id")).toBe(false)
  })
})

describe("draftTarget", () => {
  it("fills a preset's config, label and trust", () => {
    const t = draftTarget(mcp, "mcp-linear", presetFor(mcp, "linear"))
    expect(t).toMatchObject({ id: "mcp-linear", type: "mcp", label: "Linear", isNew: true, status: "not_set_up" })
    expect(t.config.url).toBe("https://mcp.linear.app/mcp")
    expect(draftTarget(mcp, "mcp-host", presetFor(mcp, "host")).trust).toBe("own")
    expect(draftTarget(mcp, "mcp-server").label).toBe("New server")
  })
})
