// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import type { AgentMembers as Members } from "@/lib/api/agents"
import { RpcError } from "@/lib/api/client"

const api = vi.hoisted(() => ({
  getAgentMembersFn: vi.fn(),
  saveMemberFn: vi.fn(),
  setAgentMemberFn: vi.fn(),
}))
vi.mock("@/lib/api/agents", () => api)
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { AgentMembers } from "./agent-members"

const agent = { id: "agt_arisan", name: "Arisan", isDefault: false }

const filled: Members = {
  members: [
    {
      id: "mem_1",
      name: "Bu Sari",
      accounts: [
        { id: "mac_1", channel: "whatsapp", address: "6281200000001@s.whatsapp.net", label: "+62 812-0000-0001" },
        { id: "mac_2", channel: "telegram", address: "123456789", label: "123456789" },
      ],
      agents: [
        { id: "agt_arisan", slug: "arisan", name: "Arisan" },
        { id: "agt_cs", slug: "cs", name: "CS" },
      ],
    },
  ],
  others: [],
  people: [],
}

const empty: Members = {
  members: [],
  others: [],
  people: [{ channel: "whatsapp", address: "6281200000002@s.whatsapp.net", label: "+62 812-0000-0002", name: "Pak Budi", lastAt: 1 }],
}

beforeEach(() => {
  api.getAgentMembersFn.mockReset()
  api.saveMemberFn.mockReset()
  api.setAgentMemberFn.mockReset()
})
afterEach(cleanup)

test("lists members with their accounts and other agents", async () => {
  api.getAgentMembersFn.mockResolvedValue(filled)
  render(<AgentMembers agent={agent} />)
  expect(await screen.findByText("Bu Sari")).toBeTruthy()
  expect(screen.getByText("+62 812-0000-0001")).toBeTruthy()
  expect(screen.getByText("123456789")).toBeTruthy()
  expect(screen.getByText("Also in CS")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Remove Bu Sari" })).toBeTruthy()
})

test("shows the empty state with an add button", async () => {
  api.getAgentMembersFn.mockResolvedValue(empty)
  render(<AgentMembers agent={agent} />)
  expect(await screen.findByText("No members yet")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Add member" })).toBeTruthy()
})

test("picking a person fills an account and the empty name", async () => {
  api.getAgentMembersFn.mockResolvedValue(empty)
  api.saveMemberFn.mockResolvedValue({ id: "mem_new" })
  const user = userEvent.setup()
  render(<AgentMembers agent={agent} />)
  await user.click(await screen.findByRole("button", { name: "Add member" }))
  await user.click(await screen.findByRole("button", { name: /Pak Budi/ }))
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("Pak Budi")
  expect((screen.getByLabelText("WhatsApp number") as HTMLInputElement).value).toBe("6281200000002@s.whatsapp.net")
  await user.click(screen.getByRole("button", { name: "Save" }))
  await waitFor(() =>
    expect(api.saveMemberFn).toHaveBeenCalledWith({
      data: { agentId: "agt_arisan", name: "Pak Budi", accounts: [{ channel: "whatsapp", address: "6281200000002@s.whatsapp.net" }] },
    }),
  )
})

test("a refused save shows under the accounts field", async () => {
  api.getAgentMembersFn.mockResolvedValue(empty)
  api.saveMemberFn.mockRejectedValue(new RpcError("That account already belongs to Bu Sari.", { status: 400, code: "BAD_REQUEST" }))
  const user = userEvent.setup()
  render(<AgentMembers agent={agent} />)
  await user.click(await screen.findByRole("button", { name: "Add member" }))
  await user.type(screen.getByLabelText("Name"), "Bu Sari")
  await user.type(screen.getByLabelText("WhatsApp number"), "081200000001")
  await user.click(screen.getByRole("button", { name: "Save" }))
  expect(await screen.findByText("That account already belongs to Bu Sari.")).toBeTruthy()
})

test("renders nothing for the default agent", () => {
  const { container } = render(<AgentMembers agent={{ ...agent, isDefault: true }} />)
  expect(container.innerHTML).toBe("")
  expect(api.getAgentMembersFn).not.toHaveBeenCalled()
})

const onlyHere: Members = {
  ...filled,
  members: [{ ...filled.members[0], agents: [{ id: "agt_arisan", slug: "arisan", name: "Arisan" }] }],
}

test("removing a member who is only in this agent asks first and says they are deleted", async () => {
  api.getAgentMembersFn.mockResolvedValue(onlyHere)
  api.setAgentMemberFn.mockResolvedValue(undefined)
  const user = userEvent.setup()
  render(<AgentMembers agent={agent} />)
  await user.click(await screen.findByRole("button", { name: "Remove Bu Sari" }))
  const dialog = await screen.findByRole("alertdialog")
  expect(dialog.textContent).toContain("deletes the member and every account they have")
  expect(api.setAgentMemberFn).not.toHaveBeenCalled()

  await user.click(screen.getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
  expect(api.setAgentMemberFn).not.toHaveBeenCalled()

  await user.click(screen.getByRole("button", { name: "Remove Bu Sari" }))
  await user.click(await screen.findByRole("button", { name: "Remove and delete" }))
  await waitFor(() => expect(api.setAgentMemberFn).toHaveBeenCalledWith({ data: { agentId: "agt_arisan", memberId: "mem_1", on: false } }))
})

test("removing a member who is in other agents too needs no confirm", async () => {
  api.getAgentMembersFn.mockResolvedValue(filled)
  api.setAgentMemberFn.mockResolvedValue(undefined)
  const user = userEvent.setup()
  render(<AgentMembers agent={agent} />)
  await user.click(await screen.findByRole("button", { name: "Remove Bu Sari" }))
  await waitFor(() => expect(api.setAgentMemberFn).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole("alertdialog")).toBeNull()
})
