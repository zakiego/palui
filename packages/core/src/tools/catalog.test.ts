import { describe, expect, it } from "vitest"
import { silentLogger } from "../log"
import { fnTool, harness, toolsRuntime } from "../chat/testing"
import { toolCatalog } from "./catalog"

describe("toolCatalog", () => {
  it("lists every running tool with its connector, class and whether guests get it", async () => {
    const h = await harness({
      registry: {
        web: toolsRuntime("web", () => ({ fetch: { tool: fnTool("fetch", () => "page"), class: "read", title: "Fetch a page", untrusted: true, guestSafe: true } })),
        alpha: toolsRuntime("alpha", () => ({ wipe: { tool: fnTool("wipe", () => "ok"), class: "destructive" }, note: { tool: fnTool("note", () => "ok"), class: "write" } })),
      },
      rows: { web: "tools", alpha: "tools" },
    })
    const tools = await toolCatalog(h.sup.running(), { agentId: "agt_x", chatId: "chat_x", channel: "web", tz: "Asia/Makassar", now: h.clock.now, log: silentLogger })
    const byName = Object.fromEntries(tools.map((t) => [t.name, t]))
    expect(Object.keys(byName).sort()).toEqual(["alpha__note", "alpha__wipe", "web__fetch"])
    expect(byName["web__fetch"]).toMatchObject({ tool: "fetch", connectorId: "web", class: "read", title: "Fetch a page", untrusted: true, guestSafe: true })
    expect(byName["alpha__wipe"]).toMatchObject({ class: "destructive", guestSafe: false, title: null })
    expect(byName["alpha__note"]).toMatchObject({ class: "write", guestSafe: false })
  })
})
