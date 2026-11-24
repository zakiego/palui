// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"

import type { ProactiveView } from "@/lib/api/proactive"

const api = vi.hoisted(() => ({
  getProactiveFn: vi.fn(),
  saveProactiveFn: vi.fn(),
  unmuteProactiveFn: vi.fn(),
  checkProactiveNowFn: vi.fn(),
}))
vi.mock("@/lib/api/proactive", () => api)
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), promise: vi.fn() }))
vi.mock("sonner", () => ({ toast }))

import { AgentProactive } from "./agent-proactive"

const NOW = 1_794_621_600_000

function view(over: Partial<ProactiveView> = {}): ProactiveView {
  return {
    agentId: "agt_palui",
    enabled: false,
    chatId: null,
    pausedReason: null,
    lastTickAt: null,
    nextTickAt: null,
    config: {
      schema: 1,
      checkEveryMin: 30,
      maxPerHour: 4,
      quietHours: { start: "22:00", end: "07:00", urgentPasses: true },
      morning: { on: true, at: "07:00" },
      evening: { on: true, at: "20:00" },
      calendar: null,
      whatsapp: null,
      contactsAuto: true,
      contacts: [],
      modelRole: "fast",
    },
    defaults: {
      calendar: { connectorId: "", departLeadMin: 30, onlineLeadMin: 10, notifyChanges: true },
      whatsapp: { connectorId: "", account: "personal", waitHours: 2, escalateHours: 4, maxAgeDays: 3, skipGroups: true },
    },
    chats: [{ id: "chat_web", title: "Palui (web)", channel: "web" }],
    connectors: { calendar: [{ id: "gcal", label: "Google Calendar", status: "on" }], whatsapp: [{ id: "mcp-wa", label: "wa-hub", status: "on" }] },
    mutes: [],
    activity: [],
    ticksToday: 0,
    sentToday: 0,
    tokensToday: 0,
    tokenCap: 0,
    lastErrors: {},
    ...over,
  }
}

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset()
  for (const fn of Object.values(toast)) fn.mockReset()
})
afterEach(cleanup)

test("off: only the switch, the target and the sources show", async () => {
  api.getProactiveFn.mockResolvedValue(view())
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByRole("switch", { name: "Message me first" })).toBeTruthy()
  expect(screen.getByRole("heading", { name: "Sources" })).toBeTruthy()
  expect(screen.queryByRole("heading", { name: "Rhythm" })).toBeNull()
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

test("turning on saves, and the rest of the settings appear", async () => {
  api.getProactiveFn.mockResolvedValue(view({ chatId: "chat_web", config: { ...view().config, calendar: { connectorId: "gcal", departLeadMin: 30, onlineLeadMin: 10, notifyChanges: true } } }))
  api.saveProactiveFn.mockImplementation(async ({ data }: { data: { enabled?: boolean } }) => view({ enabled: !!data.enabled, chatId: "chat_web" }))
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  await user.click(await screen.findByRole("switch", { name: "Message me first" }))
  expect(api.saveProactiveFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", enabled: true } })
  expect(await screen.findByRole("heading", { name: "Rhythm" })).toBeTruthy()
})

test("a failed save puts the control back and says why", async () => {
  api.getProactiveFn.mockResolvedValue(view({ enabled: true, chatId: "chat_web", config: { ...view().config, calendar: { connectorId: "gcal", departLeadMin: 30, onlineLeadMin: 10, notifyChanges: true } } }))
  api.saveProactiveFn.mockRejectedValue(new Error("Add a calendar or WhatsApp source first"))
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  const sw = await screen.findByRole("switch", { name: "Tell me when an event changes" })
  expect(sw.getAttribute("aria-checked")).toBe("true")
  await user.click(sw)
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Add a calendar or WhatsApp source first"))
  expect(screen.getByRole("switch", { name: "Tell me when an event changes" }).getAttribute("aria-checked")).toBe("true")
})

test("a load error offers to try again", async () => {
  api.getProactiveFn.mockRejectedValueOnce(new Error("Runtime is down")).mockResolvedValue(view())
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  await user.click(await screen.findByRole("button", { name: "Try again" }))
  expect(await screen.findByRole("switch", { name: "Message me first" })).toBeTruthy()
  expect(api.getProactiveFn).toHaveBeenCalledTimes(2)
})

test("activity lists what happened today, starring a chat adds a contact, a mute can be undone", async () => {
  const on = view({
    enabled: true,
    chatId: "chat_web",
    lastTickAt: NOW,
    mutes: [{ agentId: "agt_palui", subject: "wa:chat:9", label: "Grup Alumni", createdAt: NOW }],
    activity: [{ id: "ndg_1", kind: "wa_waiting", subject: "wa:chat:7", status: "sent", label: "Pak Budi", reason: null, sentAt: NOW, escalatedAt: null, wakeAt: null, updatedAt: NOW }],
  })
  api.getProactiveFn.mockResolvedValue(on)
  api.saveProactiveFn.mockResolvedValue(on)
  api.unmuteProactiveFn.mockResolvedValue({ ...on, mutes: [] })
  const user = userEvent.setup()
  render(<AgentProactive agentId="agt_palui" />)
  expect(await screen.findByText("Pak Budi")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Mark Pak Budi important" }))
  expect(api.saveProactiveFn).toHaveBeenCalledWith({ data: { agentId: "agt_palui", config: { contacts: [{ subject: "wa:chat:7", label: "Pak Budi" }] } } })
  await user.click(screen.getByRole("button", { name: "Unmute" }))
  await waitFor(() => expect(screen.queryByText("Grup Alumni")).toBeNull())
})

test("saves run in order, and two failures go back to what the server last confirmed", async () => {
  const base = view({
    enabled: true,
    chatId: "chat_web",
    config: {
      ...view().config,
      calendar: { connectorId: "gcal", departLeadMin: 30, onlineLeadMin: 10, notifyChanges: true },
      whatsapp: { connectorId: "mcp-wa", account: "personal", waitHours: 2, escalateHours: 4, maxAgeDays: 3, skipGroups: true },
    },
  })
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
  const changes = await screen.findByRole("switch", { name: "Tell me when an event changes" })
  const groups = screen.getByRole("switch", { name: "Skip groups" })
  await user.click(changes)
  await user.click(groups)
  expect(api.saveProactiveFn).toHaveBeenCalledTimes(1)
  releaseFirst()
  await waitFor(() => expect(order).toEqual(["first", "second"]))
  await waitFor(() => expect(screen.getByRole("switch", { name: "Skip groups" }).getAttribute("aria-checked")).toBe("true"))
  expect(screen.getByRole("switch", { name: "Tell me when an event changes" }).getAttribute("aria-checked")).toBe("true")
})
