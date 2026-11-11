import { describe, expect, it } from "vitest"
import { z } from "zod"
import type { TurnInfo } from "../connectors/define"
import { connectorFlowCaller, stepData } from "./call"
import { fakeEntries, type CallLog } from "./test-entries"

const turn: TurnInfo = { runId: "flr_1", chatId: "chat_owner", channel: "telegram", trigger: "flow", tz: "Asia/Makassar", now: 0, flowId: "flw_1" }

describe("connectorFlowCaller", () => {
  const log: CallLog = []
  const entries = fakeEntries(
    [
      {
        id: "reminders",
        tools: {
          list_reminders: { class: "read", run: (_a, t) => ({ chat: t.chatId }) },
          set_reminder: { class: "write", input: z.object({ text: z.string() }), run: (a) => ({ ok: true, text: a.text }) },
          cancel_reminder: { class: "destructive", run: () => ({ ok: true }) },
        },
      },
      { id: "mcp-x", trust: "external", tools: { get: { class: "read", run: () => ({ content: [{ type: "text", text: '{"a":1}' }] }) } } },
    ],
    log,
  )
  const caller = connectorFlowCaller(() => entries)

  it("resolves the class and trust before any call", async () => {
    const info = await caller.resolve({ tool: "reminders__cancel_reminder" }, turn)
    expect(info).toMatchObject({ class: "destructive", connectorId: "reminders", tool: "cancel_reminder" })
    expect(log).toHaveLength(0)
    expect(await caller.resolve({ tool: "get", connectorId: "mcp-x" }, turn)).toMatchObject({ untrusted: true, connectorType: "mcp-x" })
    expect(await caller.resolve({ tool: "nope__x" }, turn)).toBeNull()
  })

  it("refuses a destructive tool without running it", async () => {
    const info = (await caller.resolve({ tool: "reminders__cancel_reminder" }, turn))!
    await expect(caller.call(info, {}, turn)).rejects.toThrow(/destructive/)
    expect(log.filter((l) => l.tool === "cancel_reminder")).toHaveLength(0)
  })

  it("refuses when the class changed since it was resolved", async () => {
    const info = { ...(await caller.resolve({ tool: "reminders__set_reminder" }, turn))!, class: "read" as const }
    await expect(caller.call(info, { text: "x" }, turn)).rejects.toThrow(/now classified write/)
  })

  it("passes the flow's chat and id in the tool context", async () => {
    const info = (await caller.resolve({ tool: "reminders__list_reminders" }, turn))!
    expect(await caller.call(info, {}, turn)).toEqual({ chat: "chat_owner" })
    expect(log.at(-1)!.turn).toMatchObject({ chatId: "chat_owner", flowId: "flw_1", trigger: "flow" })
  })

  it("validates args against the input schema", async () => {
    const info = (await caller.resolve({ tool: "reminders__set_reminder" }, turn))!
    await expect(caller.call(info, { text: 5 }, turn)).rejects.toThrow(/don't match the tool's input/)
    expect(await caller.call(info, { text: "beli obat" }, turn)).toEqual({ ok: true, text: "beli obat" })
  })

  it("stepData reads MCP results and keeps plain text", () => {
    expect(stepData({ content: [{ type: "text", text: '{"a":1}' }] })).toEqual({ a: 1 })
    expect(stepData({ content: [{ type: "text", text: "hello" }] })).toBe("hello")
    expect(stepData({ structuredContent: { b: 2 }, content: [] })).toEqual({ b: 2 })
    expect(() => stepData({ isError: true, content: [{ type: "text", text: "rate limited" }] })).toThrow(/rate limited/)
  })
})
