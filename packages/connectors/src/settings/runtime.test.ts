import { describe, expect, it } from "vitest"
import { memoryDb, type DB } from "@palui/db"
import type { PaluiTool } from "@palui/core/connectors/define"
import { createAgent } from "@palui/core/agents"
import { upsertConnector } from "@palui/core/connectors/store"
import { listEvents } from "@palui/core/events"
import { DEFAULT_SETTINGS, getSetting } from "@palui/core/settings"
import cliproxyManifest from "../cliproxy/manifest"
import { settingsTools } from "./runtime"

const KEY = "0".repeat(64)

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

function setup(): DB {
  const db = memoryDb()
  upsertConnector(db, { id: "cliproxy", manifest: cliproxyManifest, config: { baseUrl: "http://127.0.0.1:8317/v1" }, secrets: { apiKey: "sk-very-secret" } }, KEY)
  return db
}

describe("settings tools", () => {
  it("classes: reading is read, changing is destructive", () => {
    const tools = settingsTools(memoryDb())
    expect(Object.fromEntries(Object.entries(tools).map(([k, t]) => [k, t.class]))).toEqual({ lihat_setelan: "read", ubah_setelan: "destructive" })
  })

  it("reads the setup without any secret", async () => {
    const db = setup()
    const out = await call(settingsTools(db).lihat_setelan, {})
    expect(out.ok).toBe(true)
    expect(out.settings.timezone).toBe(DEFAULT_SETTINGS.timezone)
    expect(out.settings.budgets).toEqual(DEFAULT_SETTINGS.budgets)
    expect(out.models.map((m: { id: string }) => m.id)).toEqual(["cliproxy"])
    expect(out.usage).toMatchObject({ tokensToday: 0, pausedUntil: null })
    expect(typeof out.version).toBe("string")
    expect(JSON.stringify(out)).not.toContain("sk-very-secret")
    expect(out.settings).not.toHaveProperty("persona")
  })

  it("changes only the keys given and merges partial budgets", async () => {
    const db = setup()
    const out = await call(settingsTools(db, () => 1000).ubah_setelan, { timezone: "Asia/Jakarta", budgets: { dailyTokenCap: 3_000_000 } })
    expect(out.ok).toBe(true)
    expect(Object.keys(out.changed).sort()).toEqual(["budgets", "timezone"])
    expect(getSetting(db, "timezone")).toBe("Asia/Jakarta")
    expect(getSetting(db, "budgets")).toEqual({ ...DEFAULT_SETTINGS.budgets, dailyTokenCap: 3_000_000 })
    expect(getSetting(db, "language")).toBe(DEFAULT_SETTINGS.language)
    expect(listEvents(db, { limit: 5 }).some((e) => e.kind === "settings.update")).toBe(true)
  })

  it("replaces one model role and keeps the others", async () => {
    const db = setup()
    const chain = [{ connectorId: "cliproxy", model: "claude-haiku-4-5" }]
    const out = await call(settingsTools(db).ubah_setelan, { modelRoles: { fast: chain } })
    expect(out.ok).toBe(true)
    const roles = getSetting(db, "modelRoles")
    expect(roles.fast).toEqual(chain)
    expect(roles.default).toEqual(DEFAULT_SETTINGS.modelRoles.default)
  })

  it("refuses an unknown model connector, a bad timezone and an out-of-range budget", async () => {
    const db = setup()
    const tools = settingsTools(db)
    const model = await call(tools.ubah_setelan, { modelRoles: { fast: [{ connectorId: "openai", model: "gpt" }] } })
    expect(model.ok).toBe(false)
    expect(model.error).toContain("cliproxy")
    const tz = await call(tools.ubah_setelan, { timezone: "Mars/Olympus" })
    expect(tz.ok).toBe(false)
    const budget = await call(tools.ubah_setelan, { budgets: { maxSteps: 500 } })
    expect(budget.ok).toBe(false)
    expect(budget.error).toContain("budgets.maxSteps")
    expect(getSetting(db, "timezone")).toBe(DEFAULT_SETTINGS.timezone)
  })

  it("says so when nothing changes", async () => {
    const db = setup()
    const out = await call(settingsTools(db).ubah_setelan, { timezone: DEFAULT_SETTINGS.timezone })
    expect(out).toMatchObject({ ok: true, changed: {} })
    expect(listEvents(db, { limit: 5 }).some((e) => e.kind === "settings.update")).toBe(false)
  })

  it("gives other agents the read tool only", () => {
    const db = memoryDb()
    const agent = createAgent(db, { slug: "kerja", name: "Kerja", persona: "Asisten kerja." })
    expect(Object.keys(settingsTools(db, undefined, agent.id))).toEqual(["lihat_setelan"])
  })
})
