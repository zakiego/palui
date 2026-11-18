// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import type { ChatRules } from "@/lib/api/agents"

const api = vi.hoisted(() => ({
  getChatRulesFn: vi.fn(),
  deleteChatRuleFn: vi.fn(),
  undoChatRuleChangeFn: vi.fn(),
}))
vi.mock("@/lib/api/agents", () => api)
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { AgentChatRules } from "./agent-chat-rules"

const agent = { id: "agt_arisan", isDefault: false }
const now = Date.now()

const filled: ChatRules = {
  rules: [{ id: "cru_1", chatId: "chat_g", chatTitle: "Grup Arisan", title: "Tanggal", body: "Jangan bahas tanggal lagi.", by: "Bu Sari", fromOwner: false, createdAt: now }],
  changes: [
    { id: "crc_2", chatId: "chat_g", chatTitle: "", action: "remove", title: "Bahasa", body: "Pakai bahasa Inggris.", by: "Bu Sari", fromOwner: false, createdAt: now, undoneAt: null },
    { id: "crc_1", chatId: "chat_g", chatTitle: "Grup Arisan", action: "add", title: "Tanggal", body: "Jangan bahas tanggal lagi.", by: "Owner", fromOwner: true, createdAt: now, undoneAt: now },
  ],
}

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset()
})
afterEach(cleanup)

test("lists rules with who added them, and changes with undo", async () => {
  api.getChatRulesFn.mockResolvedValue(filled)
  render(<AgentChatRules agent={agent} />)
  expect(await screen.findByText("Jangan bahas tanggal lagi.", { selector: "p.text-sm" })).toBeTruthy()
  expect(screen.getByText(/Grup Arisan · by Bu Sari/)).toBeTruthy()
  expect(screen.getByText("Bu Sari removed in Untitled chat:")).toBeTruthy()
  expect(screen.getByText("you added in Grup Arisan:")).toBeTruthy()
  expect(screen.getByText("Undone")).toBeTruthy()
  expect(screen.getAllByRole("button", { name: /Undo the change/ })).toHaveLength(1)
})

test("undo and remove call the server and reload", async () => {
  api.getChatRulesFn.mockResolvedValue(filled)
  api.undoChatRuleChangeFn.mockResolvedValue({ ok: true })
  api.deleteChatRuleFn.mockResolvedValue({ ok: true })
  const user = userEvent.setup()
  render(<AgentChatRules agent={agent} />)
  await user.click(await screen.findByRole("button", { name: "Undo the change to Bahasa" }))
  await waitFor(() => expect(api.undoChatRuleChangeFn).toHaveBeenCalledWith({ data: { agentId: "agt_arisan", id: "crc_2" } }))
  await user.click(await screen.findByRole("button", { name: "Remove Tanggal" }))
  await waitFor(() => expect(api.deleteChatRuleFn).toHaveBeenCalledWith({ data: { agentId: "agt_arisan", id: "cru_1" } }))
  await waitFor(() => expect(api.getChatRulesFn).toHaveBeenCalledTimes(3))
})

test("shows the empty state", async () => {
  api.getChatRulesFn.mockResolvedValue({ rules: [], changes: [] })
  render(<AgentChatRules agent={agent} />)
  expect(await screen.findByText("No chat rules yet")).toBeTruthy()
})

test("renders nothing for the default agent", () => {
  const { container } = render(<AgentChatRules agent={{ ...agent, isDefault: true }} />)
  expect(container.innerHTML).toBe("")
  expect(api.getChatRulesFn).not.toHaveBeenCalled()
})
