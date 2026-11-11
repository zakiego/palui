import { describe, expect, it } from "vitest"
import type { TurnResult } from "@palui/core/chat/turn"
import { runSmartReminder } from "./deps"

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
