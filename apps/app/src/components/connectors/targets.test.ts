import { describe, expect, it } from "vitest"

import { manifestFor } from "@/lib/manifests"
import { presetFor } from "@/lib/presets"
import { draftTarget, isDraftId, newConnectorId } from "./targets"

const mcp = manifestFor("mcp")!
const telegram = manifestFor("telegram")!
const http = manifestFor("http")!

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

describe("newConnectorId from a name", () => {
  it("builds the id from the name for a connector without presets", () => {
    expect(newConnectorId(http, [], undefined, "ElevenLabs")).toBe("http-elevenlabs")
    expect(newConnectorId(http, [{ id: "http-elevenlabs" }], undefined, "ElevenLabs")).toBe("http-elevenlabs-2")
    expect(newConnectorId(http, [], undefined, "☃☃")).toBe("http-item")
  })

  it("keeps a long name's id within 36 characters and a valid draft id", () => {
    const id = newConnectorId(http, [], undefined, "a very long service name ".repeat(3).slice(0, 60))
    expect(id.length).toBeLessThanOrEqual(36)
    expect(id.endsWith("-")).toBe(false)
    expect(isDraftId(http, id)).toBe(true)
    const numbered = newConnectorId(http, [{ id }], undefined, "a very long service name ".repeat(3).slice(0, 60))
    expect(numbered).toBe(`${id}-2`)
    expect(isDraftId(http, numbered)).toBe(true)
  })

  it("leaves a preset's id alone", () => {
    expect(newConnectorId(mcp, [], presetFor(mcp, "linear"), "Something else")).toBe("mcp-linear")
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

  it("takes the name asked for before the page opened", () => {
    expect(draftTarget(http, "http-elevenlabs", undefined, undefined, "ElevenLabs").label).toBe("ElevenLabs")
    expect(draftTarget(http, "http-server", undefined, undefined, "  ").label).toBe("New server")
  })
})
