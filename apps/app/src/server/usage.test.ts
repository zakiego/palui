import { agents, chats, memoryDb, runs } from "@palui/db"
import { describe, expect, it } from "vitest"
import { usage } from "./queries"

const NOW = 1_762_800_000_000

describe("usage", () => {
  it("splits tokens per agent, with old runs under the default agent and deleted ones grouped", () => {
    const db = memoryDb()
    db.insert(chats).values({ id: "chat_1", channel: "web", externalId: "web", kind: "dm", title: "Web", createdAt: NOW }).run()
    db.insert(agents)
      .values([
        { id: "agt_palui", slug: "palui", name: "Palui", persona: "", isDefault: true, createdAt: NOW, updatedAt: NOW },
        { id: "agt_cs", slug: "cs", name: "CS", persona: "", createdAt: NOW, updatedAt: NOW },
      ])
      .run()
    const run = (id: string, agentId: string | null, tokensIn: number) =>
      db.insert(runs).values({ id, chatId: "chat_1", agentId, trigger: "message", model: "m", tokensIn, tokensOut: 10, status: "ok", startedAt: NOW - 1000 }).run()
    run("run_1", "agt_cs", 500)
    run("run_2", "agt_cs", 300)
    run("run_3", "agt_palui", 100)
    run("run_4", null, 40)
    run("run_5", "agt_gone", 20)
    run("run_6", "agt_gone2", 30)
    const { perAgent } = usage(db, "UTC", 14, NOW)
    expect(perAgent.total).toBe(3)
    expect(perAgent.rows).toEqual([
      { key: "CS", tokens: 820, runs: 2 },
      { key: "Palui", tokens: 160, runs: 2 },
      { key: "", tokens: 70, runs: 2 },
    ])
  })
})
