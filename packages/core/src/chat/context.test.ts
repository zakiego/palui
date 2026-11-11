import { describe, expect, it } from "vitest"
import { toModelMessages, type HistoryRow, type ToolCallRow } from "./context"

let seq = 0
function row(role: "user" | "assistant", text: string, runId: string | null): HistoryRow {
  return { id: `msg_${++seq}`, role, text, content: { text }, runId, status: "handled", createdAt: seq } as HistoryRow
}

function call(runId: string, toolCallId: string, extra: Partial<ToolCallRow> = {}): ToolCallRow {
  return { id: `${runId}:${toolCallId}`, runId, connectorId: "reminders", tool: "reminders__ingatkan", class: "write", args: { text: "bayar pajak" }, resultPreview: '{"ok":true}', status: "ok", ms: 3, createdAt: ++seq, ...extra } as ToolCallRow
}

const none = new Set<string>()

describe("toModelMessages with past tool calls", () => {
  it("replays a run's calls as tool parts right before its reply", () => {
    const rows = [row("user", "ingetin bayar pajak", "run_1"), row("assistant", "Beres, udah aku set.", "run_1"), row("user", "cek disk", null)]
    expect(toModelMessages(rows, none, [call("run_1", "toolu_1")])).toEqual([
      { role: "user", content: [{ type: "text", text: "ingetin bayar pajak" }] },
      { role: "assistant", content: [{ type: "tool-call", toolCallId: "toolu_1", toolName: "reminders__ingatkan", input: { text: "bayar pajak" } }] },
      { role: "tool", content: [{ type: "tool-result", toolCallId: "toolu_1", toolName: "reminders__ingatkan", output: { type: "text", value: '{"ok":true}' } }] },
      { role: "assistant", content: "Beres, udah aku set." },
      { role: "user", content: [{ type: "text", text: "cek disk" }] },
    ])
  })

  it("is unchanged when no calls are passed", () => {
    const rows = [row("user", "halo", "run_1"), row("assistant", "hai", "run_1")]
    expect(toModelMessages(rows, none)).toEqual([
      { role: "user", content: [{ type: "text", text: "halo" }] },
      { role: "assistant", content: "hai" },
    ])
  })

  it("shows a run that asked for approval and never replied before the next owner message", () => {
    const rows = [row("user", "cek disk", "run_1"), row("user", "cek disk", null)]
    const out = toModelMessages(rows, none, [call("run_1", "toolu_1", { tool: "host__disk_free", args: {}, status: "pending", resultPreview: null })])
    expect(out.map((m) => m.role)).toEqual(["user", "assistant", "tool", "user"])
    expect(out[2]).toEqual({ role: "tool", content: [{ type: "tool-result", toolCallId: "toolu_1", toolName: "host__disk_free", output: { type: "text", value: "Not run yet: still waiting for the owner's approval." } }] })
  })

  it("puts an approved call before the reply of the run that resumed it", () => {
    // run_1 asked, the owner said ya, run_2 (the resume) replied.
    const rows = [row("user", "hapus temp", "run_1"), row("assistant", "udah aku hapus", "run_2"), row("user", "makasih", null)]
    const out = toModelMessages(rows, none, [call("run_1", "toolu_1", { tool: "alpha__nuke", resultPreview: '{"deleted":true}' })])
    expect(out.map((m) => m.role)).toEqual(["user", "assistant", "tool", "assistant", "user"])
    expect(JSON.stringify(out[2])).toContain('{\\"deleted\\":true}')
  })

  it("says why a call has no result: denied, failed, or cut short", () => {
    const rows = [row("user", "kerjain", "run_1"), row("assistant", "selesai", "run_1")]
    const calls = [
      call("run_1", "a", { status: "denied", resultPreview: "denied" }),
      call("run_1", "b", { status: "denied", resultPreview: "dropped: the same call was made again before the owner answered" }),
      call("run_1", "c", { status: "error", resultPreview: "boom" }),
      call("run_1", "d", { resultPreview: `${"x".repeat(499)}…` }),
    ]
    const tool = toModelMessages(rows, none, calls)[2] as { content: { output: unknown }[] }
    expect(tool.content.map((p) => p.output)).toEqual([
      { type: "execution-denied", reason: "Not run: the owner did not approve it." },
      { type: "execution-denied", reason: "dropped: the same call was made again before the owner answered" },
      { type: "error-text", value: "boom" },
      { type: "text", value: `${"x".repeat(499)}…\n[result cut short in the history]` },
    ])
  })

  it("replays a call once when a resumed run recorded it twice, with an id every provider accepts", () => {
    const rows = [row("user", "hapus", "run_1"), row("assistant", "udah", "run_2")]
    const out = toModelMessages(rows, none, [call("run_1", "call.1"), call("run_2", "call.1")])
    expect(out.map((m) => m.role)).toEqual(["user", "assistant", "tool", "assistant"])
    expect((out[1] as { content: { toolCallId: string }[] }).content).toEqual([expect.objectContaining({ toolCallId: "call_1" })])
  })

  it("drops calls left without an owner message at the start of the window", () => {
    const rows = [row("assistant", "udah aku set", "run_0"), row("user", "oke", "run_1"), row("assistant", "sip", "run_1")]
    const out = toModelMessages(rows, none, [call("run_0", "toolu_0")])
    expect(out.map((m) => m.role)).toEqual(["user", "assistant"])
  })
})
