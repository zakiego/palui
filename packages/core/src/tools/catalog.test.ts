import { describe, expect, it } from "vitest"
import { silentLogger } from "../log"
import { fnTool, harness, toolsRuntime } from "../chat/testing"
import { setConnectorOwner } from "../connectors/store"
import { connectorFlowCaller } from "../flows/call"
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

  it("gives an agent's own connector's tools only to that agent", async () => {
    const h = await harness({
      registry: {
        web: toolsRuntime("web", () => ({ fetch: { tool: fnTool("fetch", () => "page"), class: "read" } })),
        crm: toolsRuntime("crm", () => ({ deals: { tool: fnTool("deals", () => "[]"), class: "read" } })),
      },
      rows: { web: "tools", crm: "tools" },
    })
    setConnectorOwner(h.db, "crm", "agt_sales")
    const names = async (agentId: string) => (await toolCatalog(h.sup.running(), { agentId, chatId: "chat_x", channel: "web", tz: "Asia/Makassar", now: h.clock.now, log: silentLogger })).map((t) => t.name).sort()
    expect(await names("agt_sales")).toEqual(["crm__deals", "web__fetch"])
    expect(await names("agt_other")).toEqual(["web__fetch"])
    setConnectorOwner(h.db, "crm", null)
    expect(await names("agt_other")).toEqual(["crm__deals", "web__fetch"])
  })

  it("keeps flow tool steps to the tools the flow's agent may use", async () => {
    const h = await harness({
      registry: { crm: toolsRuntime("crm", () => ({ deals: { tool: fnTool("deals", () => "[]"), class: "read" } })) },
      rows: { crm: "tools" },
    })
    setConnectorOwner(h.db, "crm", "agt_sales")
    const caller = connectorFlowCaller(() => h.sup.running())
    const turn = (agentId: string) => ({ runId: "r", chatId: "c", channel: null, trigger: "flow" as const, tz: "UTC", now: h.clock.now, agentId })
    expect((await caller.list(turn("agt_sales"))).map((t) => t.name)).toEqual(["crm__deals"])
    expect(await caller.list(turn("agt_other"))).toEqual([])
    expect(await caller.resolve({ tool: "crm__deals" }, turn("agt_other"))).toBeNull()
  })
})
