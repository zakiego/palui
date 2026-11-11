import { describe, expect, it } from "vitest"
import type { TurnResult } from "@palui/core/chat/turn"
import { runFlowPrompt, runSmartReminder } from "./deps"

const r = { chatId: "chat_1", text: "bayar listrik", smartPrompt: "ingetin bayar listrik, cek tagihan" }
const turn = (res: TurnResult) => async () => res

describe("runSmartReminder", () => {
  it("resolves when the turn sent a reply or an approval prompt", async () => {
    await expect(runSmartReminder(turn({ runId: "r", text: "Jangan lupa bayar listrik", status: "ok" }), r)).resolves.toBeUndefined()
    await expect(runSmartReminder(turn({ runId: "r", text: "", status: "awaiting_approval" }), r)).resolves.toBeUndefined()
  })

  it("throws when nothing was sent, so the scheduler falls back to the plain text", async () => {
    for (const status of ["draining", "no_model", "budget", "error", "aborted", "interrupted"]) {
      await expect(runSmartReminder(turn({ runId: "", text: "", status }), r)).rejects.toThrow(status)
    }
    await expect(runSmartReminder(turn({ runId: "r", text: "  ", status: "ok" }), r)).rejects.toThrow("status ok")
  })

  it("runs the smart prompt as a reminder turn", async () => {
    let seen: unknown
    await runSmartReminder(async (o) => ((seen = o), { runId: "r", text: "x", status: "ok" }), r)
    expect(seen).toEqual({ chatId: "chat_1", trigger: "reminder", prompt: r.smartPrompt })
  })
})

describe("runFlowPrompt", () => {
  it("runs a flow prompt step as an unattended flow turn with its own run id and tools", async () => {
    let seen: unknown
    const res = await runFlowPrompt(async (o) => ((seen = o), { runId: "run_x", text: "Semangat!", status: "ok", tainted: true }), {
      chatId: "chat_1",
      agentId: null,
      flowId: "flw_1",
      flowName: "Rekap",
      stepId: "cheer",
      turnRunId: "run_x",
      prompt: "p",
      tools: [],
      send: false,
      modelRole: "fast",
      tainted: true,
    })
    expect(res).toEqual({ status: "ok", text: "Semangat!", runId: "run_x", tainted: true })
    expect(seen).toEqual({ chatId: "chat_1", trigger: "flow", prompt: "p", runId: "run_x", agentId: null, toolNames: [], noTools: true, deliver: false, silentToken: "NOTHING", untrustedInput: true, modelRole: "fast" })
  })
})
