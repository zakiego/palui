import { describe, expect, it } from "vitest"
import type { ToolCallPart } from "ai"
import { MAX_REPLAYED_ARGS, toModelMessages, type HistoryRow, type ToolCallRow } from "./context"

const NOW = 1_700_000_000_000

function row(id: string, role: "user" | "assistant", text: string, runId: string, at: number): HistoryRow {
  return { id, chatId: "chat_1", sessionId: "ses_1", role, content: { text }, text, status: "handled", runId, createdAt: at, fromOwner: true } as HistoryRow
}

function call(runId: string, callId: string, tool: string, args: unknown, at: number): ToolCallRow {
  return { id: `${runId}:${callId}`, runId, connectorId: "flows", tool, class: "read", args, resultPreview: '{"ok":true}', status: "ok", ms: 5, createdAt: at } as ToolCallRow
}

function inputs(msgs: ReturnType<typeof toModelMessages>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const m of msgs) {
    if (m.role !== "assistant" || typeof m.content === "string") continue
    for (const p of m.content) if (p.type === "tool-call") out[(p as ToolCallPart).toolCallId] = (p as ToolCallPart).input
  }
  return out
}

describe("replayed tool call arguments", () => {
  const big = (tag: string) => ({ definition: { name: tag, steps: [{ id: "a", kind: "message", text: "x".repeat(MAX_REPLAYED_ARGS) }] } })

  it("keeps the newest large draft of a tool and leaves out older ones", () => {
    const rows = [row("m1", "user", "buat flow", "run_a", NOW), row("m2", "assistant", "draft 1", "run_a", NOW + 10), row("m3", "user", "lanjut", "run_b", NOW + 20), row("m4", "assistant", "draft 2", "run_b", NOW + 30)]
    const calls = [call("run_a", "c1", "flows__test_flow", big("old"), NOW + 5), call("run_b", "c2", "flows__test_flow", big("new"), NOW + 25)]
    const got = inputs(toModelMessages(rows, new Set(), calls))
    expect(got.c1).toEqual({ note: expect.stringMatching(/^arguments left out of the history/) })
    expect(got.c2).toEqual(big("new"))
  })

  it("still leaves out the newest call when it is huge", () => {
    const rows = [row("m1", "user", "tulis", "run_a", NOW), row("m2", "assistant", "ok", "run_a", NOW + 10)]
    const calls = [call("run_a", "c1", "notes__tulis", { body: "y".repeat(40_000) }, NOW + 5)]
    expect(inputs(toModelMessages(rows, new Set(), calls)).c1).toEqual({ note: expect.stringMatching(/^arguments left out/) })
  })
})
