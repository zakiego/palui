import { connectConnector, getConnector, getSecrets, upsertConnector } from "@palui/core/saklar/store"
import { events, memoryDb } from "@palui/db"
import { describe, expect, it } from "vitest"
import { manifestFor } from "@/lib/manifests"
import { connectSaklar, describeField, listSaklar, pauseSaklar, removeSaklar, resumeSaklar, SaklarError, saveDraft } from "./saklar"
import { downRuntime, TEST_MASTER_KEY, upRuntime } from "./test-helpers"

const TOKEN = "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"

function setup() {
  const db = memoryDb()
  upsertConnector(db, { id: "telegram", manifest: manifestFor("telegram")!, secrets: { botToken: TOKEN } }, TEST_MASTER_KEY)
  return db
}

describe("saklar actions", () => {
  it("pause writes the store and pokes the runtime", async () => {
    const db = setup()
    connectConnector(db, "telegram")
    const rt = upRuntime()
    expect(await pauseSaklar({ db, rt, masterKey: TEST_MASTER_KEY }, "telegram")).toEqual({ ok: true, runtimeReachable: true })
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: false, status: "paused" })
    expect(rt.reconcile).toHaveBeenCalledOnce()
    expect(db.select().from(events).all().map((e) => e.kind)).toContain("pause")
  })

  it("pause and resume still apply when the runtime is down", async () => {
    const db = setup()
    connectConnector(db, "telegram")
    const deps = { db, rt: downRuntime(), masterKey: TEST_MASTER_KEY }
    expect(await pauseSaklar(deps, "telegram")).toEqual({ ok: true, runtimeReachable: false })
    expect(getConnector(db, "telegram")!.status).toBe("paused")
    expect(await resumeSaklar(deps, "telegram")).toEqual({ ok: true, runtimeReachable: false })
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: true, status: "connecting" })
  })

  it("pause and resume refuse a draft that was never connected", async () => {
    const db = setup()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    await expect(resumeSaklar(deps, "telegram")).rejects.toThrow(/isn't set up yet/)
    await expect(pauseSaklar(deps, "telegram")).rejects.toThrow(/isn't set up yet/)
    expect(getConnector(db, "telegram")!.status).toBe("not_set_up")
  })

  it("an unknown id is a clear error", async () => {
    const db = setup()
    await expect(pauseSaklar({ db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }, "nope")).rejects.toBeInstanceOf(SaklarError)
  })

  it("remove goes through the runtime when it's up", async () => {
    const db = setup()
    const rt = upRuntime()
    expect(await removeSaklar({ db, rt, masterKey: TEST_MASTER_KEY }, "telegram")).toMatchObject({ ok: true, runtimeReachable: true })
    expect(rt.remove).toHaveBeenCalledWith("telegram")
    expect(getConnector(db, "telegram")).toBeUndefined()
  })

  it("remove deletes credentials locally when the runtime is down, and says so", async () => {
    const db = setup()
    const res = await removeSaklar({ db, rt: downRuntime(), masterKey: TEST_MASTER_KEY }, "telegram")
    expect(res).toMatchObject({ ok: true, runtimeReachable: false })
    expect(res.detail).toMatch(/runtime was down/)
    expect(getConnector(db, "telegram")).toBeUndefined()
  })

  it("remove surfaces a runtime refusal instead of deleting", async () => {
    const db = setup()
    const rt = upRuntime({ remove: async () => ({ ok: false, detail: "device busy" }) })
    await expect(removeSaklar({ db, rt, masterKey: TEST_MASTER_KEY }, "telegram")).rejects.toThrow("device busy")
    expect(getConnector(db, "telegram")).toBeDefined()
  })
})

describe("saklar wizard", () => {
  it("saves a draft field by field and points at the bad one", () => {
    const db = memoryDb()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    try {
      saveDraft(deps, { id: "telegram", type: "telegram", secrets: { botToken: "nope" } })
      expect.unreachable()
    } catch (err) {
      expect(err).toBeInstanceOf(SaklarError)
      expect((err as SaklarError).fields.botToken).toMatch(/bot token/)
    }
    const row = saveDraft(deps, { id: "telegram", type: "telegram", secrets: { botToken: TOKEN } })
    expect(row.status).toBe("not_set_up")
    expect(getSecrets(row, TEST_MASTER_KEY)).toEqual({ botToken: TOKEN })
    expect(listSaklar(deps)[0]!.secrets.botToken).toBe("••••ppqq")
  })

  it("connect needs the required fields, then marks it connecting", async () => {
    const db = memoryDb()
    const deps = { db, rt: upRuntime(), masterKey: TEST_MASTER_KEY }
    saveDraft(deps, { id: "cliproxy", type: "cliproxy", config: { baseUrl: "http://localhost:8317" } })
    await expect(connectSaklar(deps, "cliproxy")).rejects.toMatchObject({ fields: { apiKey: "Required" } })
    saveDraft(deps, { id: "cliproxy", type: "cliproxy", secrets: { apiKey: "sk-123456789" } })
    expect(await connectSaklar(deps, "cliproxy")).toEqual({ ok: true, runtimeReachable: true })
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
