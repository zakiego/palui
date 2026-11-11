import { connectConnector, getConnector, getSecrets, upsertConnector } from "@palui/core/connectors/store"
import { events, memoryDb } from "@palui/db"
import { createAgent } from "@palui/core/agents"
import { describe, expect, it } from "vitest"
import { manifestFor } from "@/lib/manifests"
import { connectConnectorAction, describeField, listConnectorItems, pauseConnectorAction, removeConnectorAction, resumeConnectorAction, ConnectorError, saveDraft } from "./connectors"
import { downRuntime, TEST_MASTER_KEY, upRuntime } from "./test-helpers"

const TOKEN = "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"

function setup() {
  const db = memoryDb()
  upsertConnector(db, { id: "telegram", manifest: manifestFor("telegram")!, secrets: { botToken: TOKEN } }, TEST_MASTER_KEY)
  return db
}

describe("connector actions", () => {
  it("pause writes the store and pokes the runtime", async () => {
    const db = setup()
    connectConnector(db, "telegram")
    const rt = upRuntime()
    expect(await pauseConnectorAction({ db, rt, masterKey: TEST_MASTER_KEY }, "telegram")).toEqual({ ok: true, runtimeReachable: true })
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: false, status: "paused" })
    expect(rt.reconcile).toHaveBeenCalledOnce()
    expect(db.select().from(events).all().map((e) => e.kind)).toContain("pause")
  })

  it("pause and resume still apply when the runtime is down", async () => {
    const db = setup()
    connectConnector(db, "telegram")
    const deps = { db, rt: downRuntime(), masterKey: TEST_MASTER_KEY }
    expect(await pauseConnectorAction(deps, "telegram")).toEqual({ ok: true, runtimeReachable: false })
    expect(getConnector(db, "telegram")!.status).toBe("paused")
    expect(await resumeConnectorAction(deps, "telegram")).toEqual({ ok: true, runtimeReachable: false })
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: true, status: "connecting" })
  })

  it("pause and resume refuse a draft that was never connected", async () => {
    const db = setup()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    await expect(resumeConnectorAction(deps, "telegram")).rejects.toThrow(/isn't set up yet/)
    await expect(pauseConnectorAction(deps, "telegram")).rejects.toThrow(/isn't set up yet/)
    expect(getConnector(db, "telegram")!.status).toBe("not_set_up")
  })

  it("an unknown id is a clear error", async () => {
    const db = setup()
    await expect(pauseConnectorAction({ db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }, "nope")).rejects.toBeInstanceOf(ConnectorError)
  })

  it("remove goes through the runtime when it's up", async () => {
    const db = setup()
    const rt = upRuntime()
    expect(await removeConnectorAction({ db, rt, masterKey: TEST_MASTER_KEY }, "telegram")).toMatchObject({ ok: true, runtimeReachable: true })
    expect(rt.remove).toHaveBeenCalledWith("telegram")
    expect(getConnector(db, "telegram")).toBeUndefined()
  })

  it("remove deletes credentials locally when the runtime is down, and says so", async () => {
    const db = setup()
    const res = await removeConnectorAction({ db, rt: downRuntime(), masterKey: TEST_MASTER_KEY }, "telegram")
    expect(res).toMatchObject({ ok: true, runtimeReachable: false })
    expect(res.detail).toMatch(/runtime was down/)
    expect(getConnector(db, "telegram")).toBeUndefined()
  })

  it("remove surfaces a runtime refusal instead of deleting", async () => {
    const db = setup()
    const rt = upRuntime({ remove: async () => ({ ok: false, detail: "device busy" }) })
    await expect(removeConnectorAction({ db, rt, masterKey: TEST_MASTER_KEY }, "telegram")).rejects.toThrow("device busy")
    expect(getConnector(db, "telegram")).toBeDefined()
  })
})

describe("connector wizard", () => {
  it("saves a draft field by field and points at the bad one", () => {
    const db = memoryDb()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    try {
      saveDraft(deps, { id: "telegram", type: "telegram", secrets: { botToken: "nope" } })
      expect.unreachable()
    } catch (err) {
      expect(err).toBeInstanceOf(ConnectorError)
      expect((err as ConnectorError).fields.botToken).toMatch(/bot token/)
    }
    const row = saveDraft(deps, { id: "telegram", type: "telegram", secrets: { botToken: TOKEN } })
    expect(row.status).toBe("not_set_up")
    expect(getSecrets(row, TEST_MASTER_KEY)).toEqual({ botToken: TOKEN })
    expect(listConnectorItems(deps)[0]!.secrets.botToken).toBe("••••ppqq")
  })

  it("connect needs the required fields, then marks it connecting", async () => {
    const db = memoryDb()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    saveDraft(deps, { id: "cliproxy", type: "cliproxy", config: { baseUrl: "http://localhost:8317" } })
    await expect(connectConnectorAction(deps, "cliproxy")).rejects.toMatchObject({ fields: { apiKey: "Required" } })
    saveDraft(deps, { id: "cliproxy", type: "cliproxy", secrets: { apiKey: "sk-123456789" } })
    expect(await connectConnectorAction(deps, "cliproxy")).toEqual({ ok: true, runtimeReachable: true })
    expect(getConnector(db, "cliproxy")).toMatchObject({ enabled: true, status: "connecting" })
  })

  it("reads form fields from zod schemas", () => {
    const m = manifestFor("cliproxy")!
    const shape = (m.config as unknown as { shape: Record<string, never> }).shape
    expect(describeField(shape.baseUrl)).toEqual({ kind: "string", optional: true, defaultValue: "http://host.docker.internal:8317" })
    const mcp = (manifestFor("mcp")!.config as unknown as { shape: Record<string, never> }).shape
    expect(describeField(mcp.authMode)).toMatchObject({ kind: "enum", options: ["none", "bearer", "header"] })
  })
})

describe("agent-scoped connectors", () => {
  it("saves a tool connector's owner agent, refuses a missing one, and ignores it elsewhere", () => {
    const db = memoryDb()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    const chef = createAgent(db, { slug: "chef", name: "Chef", persona: "p" })
    saveDraft(deps, { id: "mcp-crm", type: "mcp", config: { url: "https://crm.example.com/mcp" }, agentId: chef.id })
    expect(getConnector(db, "mcp-crm")!.agentId).toBe(chef.id)
    expect(() => saveDraft(deps, { id: "mcp-crm", type: "mcp", agentId: "agt_gone" })).toThrow("doesn't exist")
    // Left out, the owner stays; null makes it global.
    saveDraft(deps, { id: "mcp-crm", type: "mcp", label: "CRM" })
    expect(getConnector(db, "mcp-crm")!.agentId).toBe(chef.id)
    saveDraft(deps, { id: "mcp-crm", type: "mcp", agentId: null })
    expect(getConnector(db, "mcp-crm")!.agentId).toBeNull()
    // A channel never gets an owner.
    saveDraft(deps, { id: "telegram", type: "telegram", secrets: { botToken: TOKEN }, agentId: chef.id })
    expect(getConnector(db, "telegram")!.agentId).toBeNull()
  })
})
