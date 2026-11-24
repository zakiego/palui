// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react"
import { afterEach, expect, test, vi } from "vitest"

vi.mock("@tanstack/react-router", () => ({ Link: (props: Record<string, unknown>) => <a href="#">{props.children as never}</a> }))

import type { ToolPermissions } from "@/lib/api/types"

import { audienceOf, lockFor, PermissionTree, permissionFor, setFor, type ToolItem, type ToolsViewProps } from "./shared"

afterEach(cleanup)

const tool = (name: string, cls: string, connectorId = "reminders"): ToolItem => ({
  name: `${connectorId}__${name}`,
  tool: name,
  connectorId,
  connectorLabel: "Reminders",
  connectorType: "reminders",
  class: cls as ToolItem["class"],
  title: null,
  untrusted: false,
  guestSafe: true,
})

const set = tool("set_reminder", "write")
const list = tool("list_reminders", "read")
const odd = tool("mystery", "unknown")

test("a higher level locks the ones under it: connector, class, tool, then allow-all", () => {
  const p: ToolPermissions = { allowAll: true, tools: { [set.name]: "allow", [list.name]: "deny" }, classes: { "reminders:write": "ask", "reminders:destructive": "allow" } }
  expect(permissionFor(set, p)).toBe("ask")
  expect(lockFor(set, p)).toBe("ask")
  expect(permissionFor(list, p)).toBe("deny")
  expect(lockFor(list, p)).toBeNull()
  // Unclassified tools count as destructive.
  expect(permissionFor(odd, p)).toBe("allow")
  const withConnector = { ...p, connectors: { reminders: "deny" as const } }
  expect(permissionFor(set, withConnector)).toBe("deny")
  expect(permissionFor(odd, withConnector)).toBe("deny")
  // Allow-all is the fallback, not something the owner set on the tool.
  expect(permissionFor(tool("x", "read", "other"), p)).toBe("allow")
  expect(setFor(tool("x", "read", "other"), p)).toBeNull()
  expect(permissionFor(tool("x", "read", "other"), { allowAll: false, tools: {} })).toBeNull()
})

test("a connector set to off turns its tools off", () => {
  const agent = { id: "agt_a", isDefault: true, guestTools: "safe" }
  expect(audienceOf(list, agent, { allowAll: false, tools: {}, connectors: { reminders: "deny" } })).toBe("off")
  expect(audienceOf(list, agent, { allowAll: false, tools: { [list.name]: "allow" }, connectors: { reminders: "deny" } })).toBe("off")
  expect(audienceOf(list, agent, { allowAll: false, tools: { [list.name]: "deny" } })).toBe("off")
  expect(audienceOf(list, agent, { allowAll: false, tools: {} })).toBe("everyone")
})

function props(permissions: ToolPermissions): ToolsViewProps {
  return {
    agent: { id: "agt_a", isDefault: false, guestTools: "safe" },
    data: { tools: [], error: null, used: {}, scopes: {} },
    tools: [set, list],
    groups: [[set, list]],
    query: "",
    agentsOf: () => [],
    setScope: vi.fn(),
    noPrivate: () => false,
    setNoPrivate: vi.fn(),
    permissions,
    setPermission: vi.fn(),
  }
}

test("tools sit under their class, in read, write, destructive order", () => {
  render(<PermissionTree head={set} list={[set, list]} props={props({ allowAll: false, tools: {} })} />)
  const sections = screen.getAllByRole("region")
  expect(sections.map((s) => s.getAttribute("aria-label"))).toEqual(["read", "write"])
  expect(within(sections[1]!).getByText("set_reminder")).toBeTruthy()
  expect(screen.queryByRole("region", { name: "destructive" })).toBeNull()
})

test("a choice above locks the selects under it", () => {
  render(<PermissionTree head={set} list={[set, list]} props={props({ allowAll: false, tools: { [list.name]: "allow" }, classes: { "reminders:write": "deny" } })} />)
  const all = screen.getByRole("combobox", { name: "Permission for every tool in Reminders" })
  expect(all.textContent).toContain("Default")
  expect((all as HTMLButtonElement).disabled).toBe(false)
  // The write class is set: its tools show its choice and are locked.
  const locked = screen.getByRole("combobox", { name: "Permission for set_reminder, locked by the level above" })
  expect(locked.textContent).toContain("Off")
  expect((locked as HTMLButtonElement).disabled).toBe(true)
  // The read class isn't: its tool keeps its own choice.
  expect(screen.getByRole("combobox", { name: "Permission for list_reminders" }).textContent).toContain("Allow")
})

test("a connector choice locks every class and tool", () => {
  render(<PermissionTree head={set} list={[set, list]} props={props({ allowAll: true, tools: { [list.name]: "allow" }, connectors: { reminders: "ask" }, classes: { "reminders:write": "deny" } })} />)
  for (const name of ["Permission for read tools in Reminders", "Permission for write tools in Reminders", "Permission for set_reminder", "Permission for list_reminders"]) {
    expect(screen.getByRole("combobox", { name: `${name}, locked by the level above` }).textContent).toContain("Ask first")
  }
})

test("Default says allow when allow-all is on", () => {
  render(<PermissionTree head={set} list={[set]} props={props({ allowAll: true, tools: {} })} />)
  expect(screen.getByRole("combobox", { name: "Permission for set_reminder" }).textContent).toContain("Default (allow)")
})
