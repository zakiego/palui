import { describe, expect, it } from "vitest"

import { dataOf, groupData, inSharedChats, type ToolItem } from "./shared"

const tool = (name: string, data?: ToolItem["data"]): ToolItem => ({
  name: `mcp-crm__${name}`,
  tool: name,
  connectorId: "mcp-crm",
  connectorLabel: "CRM",
  connectorType: "mcp",
  class: "read",
  title: null,
  untrusted: false,
  guestSafe: false,
  data,
})

describe("data scope", () => {
  it("follows the server when the tool doesn't say", () => {
    expect(dataOf(tool("x"), true)).toBe("agent")
    expect(dataOf(tool("x"), false)).toBe("shared")
  })

  it("keeps a declared scope whoever owns the server", () => {
    expect(dataOf(tool("x", "chat"), false)).toBe("chat")
    expect(dataOf(tool("x", "shared"), true)).toBe("shared")
  })

  it("labels a server with its widest scope", () => {
    expect(groupData([tool("a", "none"), tool("b", "chat")], false)).toBe("chat")
    expect(groupData([tool("a", "chat"), tool("b")], true)).toBe("agent")
    expect(groupData([tool("a", "agent"), tool("b"), tool("c", "none")], false)).toBe("shared")
    expect(groupData([tool("a", "none")], false)).toBe("none")
  })
})

describe("shared chats", () => {
  const safe = { ...tool("set", "chat"), guestSafe: true }
  const none = { allowAll: false, tools: {} }

  it("gets a tool the server lets through", () => {
    expect(inSharedChats(safe, "safe", none)).toBe(true)
    expect(inSharedChats(tool("x"), "safe", none)).toBe(false)
  })

  it("gets nothing when the agent offers no tools there", () => {
    expect(inSharedChats(safe, "none", none)).toBe(false)
  })

  it("leaves out a tool set to ask first or off", () => {
    expect(inSharedChats(safe, "safe", { allowAll: false, tools: { [safe.name]: "ask" } })).toBe(false)
    expect(inSharedChats(safe, "safe", { allowAll: false, tools: { [safe.name]: "deny" } })).toBe(false)
    expect(inSharedChats(safe, "safe", { allowAll: true, tools: { [safe.name]: "allow" } })).toBe(true)
  })
})
