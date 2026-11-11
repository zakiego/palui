import { createAgent, getDefaultAgent } from "@palui/core/agents"
import { ensureChat } from "@palui/core/chats"
import { upsertConnector } from "@palui/core/connectors/store"
import { memoryDb, runs, toolCalls } from "@palui/db"
import { afterEach, describe, expect, it } from "vitest"
import { manifestFor } from "@/lib/manifests"
import { agentMemory, agentTools } from "./agents"
import { setRuntime } from "./runtime"
import { downRuntime, TEST_MASTER_KEY, upRuntime } from "./test-helpers"

const NOW = Date.UTC(2026, 10, 12, 6, 0, 0)
const DAY = 24 * 60 * 60 * 1000

afterEach(() => setRuntime(undefined))

function withRecall(container = "main") {
  const db = memoryDb()
  upsertConnector(db, { id: "recall", manifest: manifestFor("recall")!, config: { container }, secrets: { apiKey: "rcl_test" }, enabled: true }, TEST_MASTER_KEY)
  return db
}

describe("agentMemory", () => {
  it("says there's no memory connector, with the container it would use", async () => {
    const db = memoryDb()
    const chef = createAgent(db, { slug: "chef", name: "Chef", persona: "p" })
    expect(await agentMemory(db, chef)).toEqual({ connector: null, container: "agent-chef", stats: null, error: null })
  })

  it("reads the agent's container from recall; Palui uses the connector's", async () => {
    const db = withRecall("main")
    const chef = createAgent(db, { slug: "chef", name: "Chef", persona: "p" })
    const rt = upRuntime({ action: async () => ({ containers: [{ container: "agent-chef", memories: 12, documents: 0, lastActivity: NOW }] }) })
    setRuntime(rt)
    const own = await agentMemory(db, chef)
    expect(own).toMatchObject({ container: "agent-chef", stats: { memories: 12, lastActivity: NOW }, error: null })
    expect(own.connector).toMatchObject({ id: "recall", enabled: true })
    // A container recall doesn't know yet is empty, not an error.
    expect(await agentMemory(db, getDefaultAgent(db))).toMatchObject({ container: "main", stats: { container: "main", memories: 0, lastActivity: null } })
  })

  it("reports a down runtime without failing", async () => {
    const db = withRecall()
    setRuntime(downRuntime())
    const r = await agentMemory(db, createAgent(db, { slug: "chef", name: "Chef", persona: "p" }))
    expect(r.stats).toBeNull()
    expect(r.error).toContain("isn't reachable")
  })
})

describe("agentTools", () => {
  it("counts this agent's successful calls from the last 7 days", async () => {
    const db = memoryDb()
    const chef = createAgent(db, { slug: "chef", name: "Chef", persona: "p" })
    const other = createAgent(db, { slug: "cs", name: "CS", persona: "p" })
    const chat = ensureChat(db, { channel: "web", externalId: "web" })
    const run = (id: string, agentId: string) => db.insert(runs).values({ id, chatId: chat.id, agentId, trigger: "message", status: "ok", startedAt: NOW }).run()
    run("run_a", chef.id)
    run("run_b", other.id)
    const call = (id: string, runId: string, status: "ok" | "error" | "denied", at: number) =>
      db.insert(toolCalls).values({ id, runId, connectorId: "web", tool: "web__fetch", class: "read", status, createdAt: at }).run()
    call("run_a:1", "run_a", "ok", NOW - DAY)
    call("run_a:2", "run_a", "ok", NOW - 2 * DAY)
    call("run_a:3", "run_a", "denied", NOW - DAY)
    call("run_a:4", "run_a", "ok", NOW - 8 * DAY)
    call("run_b:1", "run_b", "ok", NOW - DAY)
    setRuntime(upRuntime({ agentTools: async () => ({ tools: [] }) }))
    expect(await agentTools(db, chef, NOW)).toEqual({ tools: [], error: null, used: { web__fetch: 2 }, scopes: {} })
  })

  it("still shows usage when the runtime is down", async () => {
    const db = memoryDb()
    setRuntime(downRuntime())
    const r = await agentTools(db, createAgent(db, { slug: "chef", name: "Chef", persona: "p" }), NOW)
    expect(r).toMatchObject({ tools: null, used: {} })
    expect(r.error).toContain("isn't reachable")
  })
})
