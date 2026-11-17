import { describe, expect, it } from "vitest"

import { dataOf, groupData, type ToolItem } from "./shared"

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
