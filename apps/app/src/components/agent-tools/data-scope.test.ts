import { describe, expect, it } from "vitest"

import { audienceOf, dataOf, groupAudience, groupData, inMemberChats, inSharedChats, type ToolItem } from "./shared"

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

describe("members' chats", () => {
  const safe = { ...tool("notes", "agent"), memberSafe: true }
  const none = { allowAll: false, tools: {} }

  it("gets a tool the server marks member-safe", () => {
    expect(inMemberChats(safe, none)).toBe(true)
  })

  it("leaves out a tool set to ask first or off", () => {
    expect(inMemberChats(safe, { allowAll: false, tools: { [safe.name]: "ask" } })).toBe(false)
    expect(inMemberChats(safe, { allowAll: false, tools: { [safe.name]: "deny" } })).toBe(false)
  })

  it("leaves out a tool when the server sends no memberSafe", () => {
    expect(inMemberChats(tool("x", "agent"), none)).toBe(false)
  })
})

describe("audience", () => {
  const agent = { id: "a1", isDefault: false, guestTools: "safe" }
  const palui = { ...agent, isDefault: true }
  const none = { allowAll: false, tools: {} }
  const guest = { ...tool("set", "chat"), guestSafe: true, memberSafe: true }
  const member = { ...tool("notes", "agent"), memberSafe: true }
  const wipe = { ...tool("wipe", "agent"), class: "destructive" as const }

  it("reaches everyone for a guest-safe tool", () => {
    expect(audienceOf(guest, agent, none)).toBe("everyone")
  })

  it("reaches members for a member-safe tool", () => {
    expect(audienceOf(member, agent, none)).toBe("members")
  })

  it("stays with the owner on the default agent, which has no members", () => {
    expect(audienceOf(member, palui, none)).toBe("owner")
  })

  it("stays with the owner for ask first and destructive tools", () => {
    expect(audienceOf(guest, agent, { allowAll: false, tools: { [guest.name]: "ask" } })).toBe("owner")
    expect(audienceOf(wipe, agent, none)).toBe("owner")
  })

  it("says off for a tool set to off", () => {
    expect(audienceOf(guest, agent, { allowAll: false, tools: { [guest.name]: "deny" } })).toBe("off")
  })

  it("falls back to members when the agent offers shared chats nothing", () => {
    expect(audienceOf(guest, { ...agent, guestTools: "none" }, none)).toBe("members")
  })

  it("labels a server with its widest audience", () => {
    expect(groupAudience([guest, { ...guest, name: "mcp-crm__b" }], agent, none)).toEqual({ audience: "everyone", n: 2, all: true })
    expect(groupAudience([guest, member, wipe], agent, none)).toEqual({ audience: "everyone", n: 1, all: false })
    expect(groupAudience([wipe], agent, none)).toEqual({ audience: "owner", n: 1, all: true })
  })

  it("has nothing to say when every tool is off", () => {
    const off = { allowAll: false, tools: { [guest.name]: "deny" as const, [wipe.name]: "deny" as const } }
    expect(groupAudience([guest, wipe], agent, off)).toBeNull()
  })
})
