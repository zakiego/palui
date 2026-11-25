// @vitest-environment jsdom
import { cleanup, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import type { ProactiveTool, ProactiveView } from "@/lib/api/proactive"

const api = vi.hoisted(() => ({
  getProactiveFn: vi.fn(),
  saveProactiveFn: vi.fn(),
  unmuteProactiveFn: vi.fn(),
  checkProactiveNowFn: vi.fn(),
  testProactiveFn: vi.fn(),
  addProactiveNoteFn: vi.fn(),
  editProactiveNoteFn: vi.fn(),
  deleteProactiveNoteFn: vi.fn(),
  MAX_PROMPT_CHARS: 4000,
  MAX_MENTIONS: 16,
  MAX_NOTE_CHARS: 300,
  MAX_NOTES_CHARS: 4000,
}))
vi.mock("@/lib/api/proactive", () => api)
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), promise: vi.fn() }))
vi.mock("sonner", () => ({ toast }))

import { AgentProactive, mentionsIn } from "./agent-proactive"

const NOW = 1_794_621_600_000

const tools: ProactiveTool[] = [
  { name: "gcal__get_calendar", connectorId: "gcal", connectorLabel: "Google Calendar", tool: "get_calendar", class: "read", title: "Check the calendar", untrusted: true },
  { name: "wa-inbox__waiting_chats", connectorId: "wa-inbox", connectorLabel: "WhatsApp inbox", tool: "waiting_chats", class: "read", title: "Chats waiting for a reply", untrusted: true },
  { name: "notes__add_note", connectorId: "notes", connectorLabel: "Notes", tool: "add_note", class: "write", title: "Add a note", untrusted: false },
]

function view(over: Partial<ProactiveView> = {}): ProactiveView {
  return {
    agentId: "agt_palui",
    enabled: false,
    chatId: null,
    pausedReason: null,
    lastTickAt: null,
    nextTickAt: null,
    config: {
      schema: 2,
      prompt: "",
      checkEveryMin: 30,
      maxPerHour: 4,
      quietHours: { start: "22:00", end: "07:00", urgentPasses: true },
      morning: { on: true, at: "07:00" },
      evening: { on: true, at: "20:00" },
      modelRole: "fast",
    },
    starter: "Check my calendar (mention its tool here) for the next 36 hours.",
    chats: [{ id: "chat_web", title: "Palui (web)", channel: "web" }],
    tools,
    mentions: [],
    notes: [],
    notesChars: 0,
    mutes: [],
    activity: [],
    ticksToday: 0,
    sentToday: 0,
    tokensToday: 0,
    tokenCap: 0,
    lastRun: null,
    ...over,
  }
}

beforeEach(() => {
  for (const fn of Object.values(api)) if (typeof fn === "function") fn.mockReset()
  for (const fn of Object.values(toast)) fn.mockReset()
})
afterEach(cleanup)

test("mentions read like the server's", () => {
  expect(mentionsIn("Check @gcal__get_calendar, then @mcp-j__list_chats. Mail a@b__c or @gcal__get_calendar again. @nope @x__")).toEqual(["gcal__get_calendar", "mcp-j__list_chats"])
})

test("off: the switch, the target, the prompt and memory show; activity doesn't", async () => {
  api.getProactiveFn.mockResolvedValue(view())
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByRole("switch", { name: "Message me first" })).toBeTruthy()
  expect(screen.getByRole("heading", { name: "Prompt" })).toBeTruthy()
  expect(screen.getByRole("heading", { name: "Memory" })).toBeTruthy()
  expect(screen.queryByRole("heading", { name: "Today" })).toBeNull()
})

test("turning on without a chat asks for one and saves nothing", async () => {
  api.getProactiveFn.mockResolvedValue(view())
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  await user.click(await screen.findByRole("switch", { name: "Message me first" }))
  expect(toast.error).toHaveBeenCalledWith("Pick a chat first")
  expect(api.saveProactiveFn).not.toHaveBeenCalled()
})

test("a refused turn on says why and puts the switch back", async () => {
  api.getProactiveFn.mockResolvedValue(view({ chatId: "chat_web" }))
  api.saveProactiveFn.mockRejectedValue(new Error("Mention at least one tool in the prompt, like @gcal__get_calendar."))
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  const sw = await screen.findByRole("switch", { name: "Message me first" })
  await user.click(sw)
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Mention at least one tool in the prompt, like @gcal__get_calendar."))
  expect(screen.getByRole("switch", { name: "Message me first" }).getAttribute("aria-checked")).toBe("false")
})

test("typing @ suggests read tools; Enter inserts one and Save sends the prompt", async () => {
  api.getProactiveFn.mockResolvedValue(view())
  api.saveProactiveFn.mockImplementation(async ({ data }: { data: { config: { prompt: string } } }) => view({ config: { ...view().config, prompt: data.config.prompt } }))
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  const box = await screen.findByRole("textbox", { name: "Proactive prompt" })
  await user.type(box, "Check @cal")
  const list = await screen.findByRole("listbox")
  expect(list.textContent).toContain("@gcal__get_calendar")
  expect(within(list).getByRole("group", { name: "Google Calendar" })).toBeTruthy()
  await user.keyboard("{Enter}")
  expect((box as HTMLTextAreaElement).value).toBe("Check @gcal__get_calendar ")
  expect(screen.queryByRole("listbox")).toBeNull()
  expect(screen.getByText("1 / 16 tools")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Save prompt" }))
  expect(api.saveProactiveFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", config: { prompt: "Check @gcal__get_calendar " } } })
})

test("a write tool is listed but can't be picked, and a typed one is flagged", async () => {
  api.getProactiveFn.mockResolvedValue(view())
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  const box = await screen.findByRole("textbox", { name: "Proactive prompt" })
  await user.type(box, "@note")
  const opt = await screen.findByRole("option", { name: /Add a note/ })
  expect(opt.getAttribute("aria-disabled")).toBe("true")
  await user.keyboard("{Escape}")
  await user.clear(box)
  await user.type(box, "Use @notes__add_note and @gone__x ")
  expect(screen.getByText(/can change things/)).toBeTruthy()
  expect(screen.getByText(/isn't available/)).toBeTruthy()
  // The highlight layer marks both as problems, as code chips in the prompt.
  const chips = [...document.querySelectorAll("[data-mention]")].map((el) => `${el.textContent}:${el.getAttribute("data-mention")}`)
  expect(chips).toEqual(["@notes__add_note:problem", "@gone__x:problem"])
})

test("a usable mention is highlighted in the prompt", async () => {
  api.getProactiveFn.mockResolvedValue(view({ config: { ...view().config, prompt: "Check @gcal__get_calendar now" } }))
  render(<AgentProactive agentId="agt_palui" />)
  await screen.findByRole("textbox", { name: "Proactive prompt" })
  const chip = document.querySelector("[data-mention]")
  expect(chip?.textContent).toBe("@gcal__get_calendar")
  expect(chip?.getAttribute("data-mention")).toBe("ok")
})

test("test run shows what would be sent without saving", async () => {
  api.getProactiveFn.mockResolvedValue(view({ config: { ...view().config, prompt: "Check @gcal__get_calendar" } }))
  api.testProactiveFn.mockResolvedValue({ calls: [{ tool: "gcal__get_calendar", args: {}, hash: "x" }], items: [{ key: "cal:e1:leave", subject: "cal:e1", text: "Berangkat ke klinik.", sendAt: null, urgent: false }], closed: [], remember: ["Klinik buka jam 8"], forget: [], ignored: [], tokens: 1234 })
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  await user.click(await screen.findByRole("button", { name: "Test run" }))
  expect(await screen.findByText("Berangkat ke klinik.")).toBeTruthy()
  expect(screen.getByText("Klinik buka jam 8")).toBeTruthy()
  expect(screen.getByText("1 tool calls · 1,234 tokens")).toBeTruthy()
  expect(api.testProactiveFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", prompt: "Check @gcal__get_calendar" } })
  expect(api.saveProactiveFn).not.toHaveBeenCalled()
})

test("memory: add a note, keep the agent's, delete one", async () => {
  const note = (id: string, source: "owner" | "model", text: string) => ({ id, agentId: "agt_palui", text, source, expiresAt: source === "model" ? Date.now() + 2.5 * 86_400_000 : null, createdAt: NOW, updatedAt: NOW })
  const withNotes = view({ notes: [note("pmem_1", "owner", "Skip the bank group"), note("pmem_2", "model", "Pak Budi promised the papers tomorrow")], notesChars: 55 })
  api.getProactiveFn.mockResolvedValue(withNotes)
  api.addProactiveNoteFn.mockResolvedValue(withNotes)
  api.editProactiveNoteFn.mockResolvedValue(withNotes)
  api.deleteProactiveNoteFn.mockResolvedValue(view())
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByText("Agent · 3 days left")).toBeTruthy()
  expect(screen.getByText("You")).toBeTruthy()
  await user.type(screen.getByRole("textbox", { name: "Add note" }), "Istri always matters")
  await user.click(screen.getByRole("button", { name: "Add note" }))
  expect(api.addProactiveNoteFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", text: "Istri always matters" } })
  await user.click(screen.getByRole("button", { name: "Keep this note" }))
  expect(api.editProactiveNoteFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", noteId: "pmem_2", keep: true } })
  await user.click(screen.getAllByRole("button", { name: "Delete note" })[0])
  expect(api.deleteProactiveNoteFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", noteId: "pmem_1" } })
  await waitFor(() => expect(screen.getByText("No notes yet. Reply to a proactive message to add one.")).toBeTruthy())
})

test("a load error offers to try again", async () => {
  api.getProactiveFn.mockRejectedValueOnce(new Error("Runtime is down")).mockResolvedValue(view())
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  await user.click(await screen.findByRole("button", { name: "Try again" }))
  expect(await screen.findByRole("switch", { name: "Message me first" })).toBeTruthy()
  expect(api.getProactiveFn).toHaveBeenCalledTimes(2)
})

test("activity lists what happened today, and a mute can be undone", async () => {
  const on = view({
    enabled: true,
    chatId: "chat_web",
    lastTickAt: NOW,
    mutes: [{ agentId: "agt_palui", subject: "wa:9", label: "Grup Alumni", createdAt: NOW }],
    activity: [{ id: "ndg_1", kind: "item", subject: "wa:7", status: "sent", label: "Pak Budi nunggu balasan", reason: null, sentAt: NOW, wakeAt: null, updatedAt: NOW }],
  })
  api.getProactiveFn.mockResolvedValue(on)
  api.unmuteProactiveFn.mockResolvedValue({ ...on, mutes: [] })
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByText("Pak Budi nunggu balasan")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Unmute" }))
  await waitFor(() => expect(screen.queryByText("Grup Alumni")).toBeNull())
})

test("saves run in order, and two failures go back to what the server last confirmed", async () => {
  const base = view({ enabled: true, chatId: "chat_web" })
  api.getProactiveFn.mockResolvedValue(base)
  const order: string[] = []
  let releaseFirst: () => void = () => {}
  api.saveProactiveFn
    .mockImplementationOnce(() => new Promise((_, reject) => (releaseFirst = () => (order.push("first"), reject(new Error("first failed"))))))
    .mockImplementationOnce(async () => {
      order.push("second")
      throw new Error("second failed")
    })
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  const urgent = await screen.findByRole("switch", { name: "Urgent items still come through" })
  await user.click(urgent)
  await user.click(urgent)
  expect(api.saveProactiveFn).toHaveBeenCalledTimes(1)
  releaseFirst()
  await waitFor(() => expect(order).toEqual(["first", "second"]))
  await waitFor(() => expect(screen.getByRole("switch", { name: "Urgent items still come through" }).getAttribute("aria-checked")).toBe("true"))
})

test("counts read right at one", async () => {
  api.getProactiveFn.mockResolvedValue(view({ enabled: true, chatId: "chat_web", ticksToday: 1, sentToday: 1 }))
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByText("1 check · 1 sent")).toBeTruthy()
})

test("a failed last run shows why", async () => {
  api.getProactiveFn.mockResolvedValue(view({ enabled: true, chatId: "chat_web", lastRun: { at: NOW, full: false, skipped: "", calls: 0, error: "@gone__x isn't available" } }))
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByText("Last run failed")).toBeTruthy()
  expect(screen.getByText("@gone__x isn't available")).toBeTruthy()
})
