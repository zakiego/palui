// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"

vi.mock("@tanstack/react-router", () => ({ Link: (props: Record<string, unknown>) => <a href="#">{props.children as never}</a> }))

import { GroupActions, type ToolItem, type ToolsViewProps } from "./shared"

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
