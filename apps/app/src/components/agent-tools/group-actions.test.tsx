// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"

vi.mock("@tanstack/react-router", () => ({ Link: (props: Record<string, unknown>) => <a href="#">{props.children as never}</a> }))

import { GroupActions, GroupMeta, PermissionTree, type ToolItem, type ToolsViewProps } from "./shared"

afterEach(cleanup)

const item = (connectorId: string, connectorType: string): ToolItem => ({
  name: `${connectorId}__lookup`,
  tool: "lookup",
  connectorId,
  connectorLabel: connectorId,
  connectorType,
  class: "read",
  title: null,
  untrusted: false,
  guestSafe: false,
})

function props(over: Partial<ToolsViewProps> = {}): ToolsViewProps {
  return {
    agent: { id: "agt_a", isDefault: false, guestTools: "safe" },
    data: { tools: [], error: null, used: {}, scopes: {} },
    tools: [],
    groups: [],
    query: "",
    agentsOf: () => [],
    setScope: vi.fn(),
    noPrivate: () => false,
    setNoPrivate: vi.fn(),
    applySuggested: vi.fn(async () => {}),
    permissions: { allowAll: false, tools: {} },
    setPermission: vi.fn(),
    ...over,
  }
}

test("an MCP server gets the no-private-data switch", async () => {
  const p = props()
  render(<GroupActions head={item("mcp-times", "mcp")} props={p} />)
  const sw = screen.getByRole("switch", { name: "Holds no private data" })
  expect(sw.getAttribute("aria-checked")).toBe("false")
  expect(screen.getByText(/Applies to every agent/)).toBeTruthy()
  await userEvent.click(sw)
  expect(p.setNoPrivate).toHaveBeenCalledWith("mcp-times", true)
})

test("a server only this agent has shows its scope and doesn't say it applies to every agent", () => {
  render(<GroupActions head={item("mcp-times", "mcp")} props={props({ agentsOf: () => ["agt_a"], noPrivate: () => true, data: { tools: [], error: null, used: {}, scopes: { "mcp-times": ["agt_a"] } } })} />)
  expect(screen.getByText("Only this agent")).toBeTruthy()
  expect(screen.getByRole("switch", { name: "Holds no private data" }).getAttribute("aria-checked")).toBe("true")
  expect(screen.queryByText(/Applies to every agent/)).toBeNull()
})

test("a server shared with other agents says how many", () => {
  render(<GroupActions head={item("mcp-times", "mcp")} props={props({ agentsOf: () => ["agt_b", "agt_a", "agt_c"], data: { tools: [], error: null, used: {}, scopes: { "mcp-times": ["agt_b", "agt_a", "agt_c"] } } })} />)
  expect(screen.getByText("This agent and 2 more")).toBeTruthy()
  expect(screen.getByText(/Applies to every agent/)).toBeTruthy()
})

test("a built-in connector has no switch", () => {
  render(<GroupActions head={item("reminders", "reminders")} props={props()} />)
  expect(screen.queryByRole("switch")).toBeNull()
})

// An MCP tool the owner hasn't classified: destructive at runtime, with the
// class the server's annotations suggest.
const hub = (tool: string, cls: ToolItem["class"], suggestedClass?: ToolItem["class"]): ToolItem => ({
  ...item("mcp-hub", "mcp"),
  name: `mcp-hub__${tool}`,
  tool,
  connectorLabel: "MCP Hub",
  class: cls,
  ...(suggestedClass ? { suggestedClass } : {}),
})

const hubTools = [
  hub("list_todos", "destructive", "read"),
  hub("search", "destructive", "read"),
  hub("add_todo", "destructive", "write"),
  hub("mystery", "destructive", "destructive"),
  hub("drop_all", "destructive"),
  hub("get_time", "read"),
]

test("an MCP server with unclassified tools offers the server's suggestions in one click", async () => {
  const p = props({ tools: [...hubTools, item("reminders", "reminders")] })
  render(<GroupActions head={hubTools[0]!} props={p} />)
  expect(screen.getByText("4 tools aren't classified yet")).toBeTruthy()
  expect(screen.getByText(/The server suggests 2 read, 1 write and 1 destructive\./)).toBeTruthy()
  await userEvent.click(screen.getByRole("button", { name: "Use suggested classes" }))
  expect(p.applySuggested).toHaveBeenCalledWith("mcp-hub")
})

test("no suggestions once every tool has a class", () => {
  render(<GroupActions head={hubTools[4]!} props={props({ tools: [hubTools[4]!, hubTools[5]!] })} />)
  expect(screen.queryByRole("button", { name: "Use suggested classes" })).toBeNull()
})

test("the group counts saved destructive tools apart from unclassified ones", () => {
  render(<GroupMeta list={hubTools} scope="shared" />)
  expect(screen.getByText("1 destructive")).toBeTruthy()
  expect(screen.getByText("4 not classified")).toBeTruthy()
})

test("an unclassified tool says what the server suggests", () => {
  render(<PermissionTree head={hubTools[0]!} list={hubTools} props={props({ tools: hubTools })} />)
  expect(screen.getAllByText("Not classified, suggests read")).toHaveLength(2)
  expect(screen.getByText("Not classified, suggests write")).toBeTruthy()
  expect(screen.queryAllByText(/^Not classified/)).toHaveLength(4)
})
