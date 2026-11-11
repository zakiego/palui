import { chats, memoryDb, messages, runs, toolCalls } from "@palui/db"
import { describe, expect, it } from "vitest"
import { runDetail } from "./queries"

const NOW = 1_762_800_000_000

function seed() {
  const db = memoryDb()
  db.insert(chats).values({ id: "chat_1", channel: "web", externalId: "web", kind: "dm", title: "Palui (web)", createdAt: NOW }).run()
  db.insert(runs).values({ id: "run_1", chatId: "chat_1", trigger: "message", model: "m", tokensIn: 1000, tokensOut: 20, tokensCached: 800, tokensCacheWrite: 150, steps: 2, status: "ok", startedAt: NOW, ms: 900 }).run()
  db.insert(messages)
    .values([
      { id: "msg_1", chatId: "chat_1", role: "user", content: { text: "ingatkan besok" }, text: "ingatkan besok", runId: "run_1", createdAt: NOW },
      { id: "msg_2", chatId: "chat_1", role: "assistant", content: [], text: "Siap, besok 09.00", runId: "run_1", createdAt: NOW + 2 },
      { id: "msg_3", chatId: "chat_1", role: "assistant", content: [], text: "", runId: "run_1", createdAt: NOW + 3 },
      { id: "msg_4", chatId: "chat_1", role: "user", content: { text: "other" }, text: "other", runId: "run_2", createdAt: NOW + 4 },
    ])
    .run()
  db.insert(toolCalls)
    .values([
      { id: "tc_2", runId: "run_1", tool: "reminders__set_reminder", class: "write", args: { when: "besok" }, resultPreview: "ok", status: "ok", ms: 80, createdAt: NOW + 1 },
      { id: "tc_1", runId: "run_1", tool: "recall__cari", class: "read", args: null, resultPreview: null, status: "error", ms: null, createdAt: NOW },
    ])
    .run()
  return db
}

describe("runDetail", () => {
  it("returns the run with its prompt, reply and tool calls in order", () => {
    const run = runDetail(seed(), "run_1")
    expect(run).toMatchObject({ id: "run_1", chat: "Palui (web)", chatId: "chat_1", chatGone: false, tokensCached: 800, tokensCacheWrite: 150, steps: 2 })
    // 50 fresh + 150 written + 80 for the 800 read + 20 out.
    expect(run?.tokensCounted).toBe(300)
    expect(run?.prompt.map((m) => m.text)).toEqual(["ingatkan besok"])
    // Empty assistant messages (a turn that only called tools) are left out.
    expect(run?.reply.map((m) => m.text)).toEqual(["Siap, besok 09.00"])
    expect(run?.tools.map((x) => x.tool)).toEqual(["recall__cari", "reminders__set_reminder"])
    expect(run?.tools[0]?.args).toBe("")
    expect(run?.tools[1]?.args).toContain('"when": "besok"')
  })

  it("says the chat is gone instead of linking to it", () => {
    const db = seed()
    db.insert(runs).values({ id: "run_3", chatId: "chat_gone", trigger: "message", status: "error", startedAt: NOW }).run()
    expect(runDetail(db, "run_3")).toMatchObject({ chatId: null, chat: null, chatGone: true, tools: [], prompt: [], reply: [] })
  })

  it("returns null for an unknown id", () => {
    expect(runDetail(seed(), "run_nope")).toBeNull()
  })
})
