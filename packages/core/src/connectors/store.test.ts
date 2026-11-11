import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { listEvents } from "../events"
import { getSetting, setSetting, DEFAULT_SETTINGS } from "../settings"
import { redact } from "../log"
import {
  connectConnector,
  deleteConnector,
  getConnector,
  getSecrets,
  maskedSecrets,
  patchConnectorSecrets,
  pauseConnector,
  resumeConnector,
  setConnectorStatus,
  upsertConnector,
} from "./store"

const KEY = "test-master-key-test-master-key-1234"
const manifest = { id: "telegram", kind: "channel" as const, name: "Telegram" }

describe("connector store", () => {
  it("creates a draft, merges secrets, and changes the hash only when config or secrets change", () => {
    const db = memoryDb()
    const a = upsertConnector(db, { id: "telegram", manifest, secrets: { botToken: "123:abc" } }, KEY)
    expect(a.status).toBe("not_set_up")
    expect(a.enabled).toBe(false)
    expect(a.secrets).not.toContain("123:abc")
    const b = upsertConnector(db, { id: "telegram", manifest, config: { x: 1 } }, KEY)
    expect(b.configHash).not.toBe(a.configHash)
    expect(getSecrets(b, KEY)).toEqual({ botToken: "123:abc" })
    const c = upsertConnector(db, { id: "telegram", manifest, secrets: { botToken: "" } }, KEY)
    expect(c.secrets).toBeNull()
    expect(maskedSecrets(c, KEY)).toEqual({})
  })

  it("patches runtime-learned secrets without changing the hash", () => {
    const db = memoryDb()
    const a = upsertConnector(db, { id: "telegram", manifest, secrets: { botToken: "123:abc" } }, KEY)
    patchConnectorSecrets(db, "telegram", { token: "t3-token" }, KEY)
    const b = getConnector(db, "telegram")!
    expect(getSecrets(b, KEY)).toEqual({ botToken: "123:abc", token: "t3-token" })
    expect(b.secrets).not.toContain("t3-token")
    expect(b.configHash).toBe(a.configHash)
    patchConnectorSecrets(db, "telegram", { token: "" }, KEY)
    expect(getSecrets(getConnector(db, "telegram")!, KEY)).toEqual({ botToken: "123:abc" })
  })

  it("moves through connect, pause, resume, remove with events", () => {
    const db = memoryDb()
    upsertConnector(db, { id: "telegram", manifest }, KEY)
    connectConnector(db, "telegram")
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: true, status: "connecting" })
    setConnectorStatus(db, "telegram", "on", "@palui_bot")
    pauseConnector(db, "telegram", "chat")
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: false, status: "paused" })
    resumeConnector(db, "telegram")
    expect(getConnector(db, "telegram")).toMatchObject({ enabled: true, status: "connecting" })
    deleteConnector(db, "telegram")
    expect(getConnector(db, "telegram")).toBeUndefined()
    const kinds = listEvents(db).map((e) => e.kind)
    expect(kinds).toEqual(["remove", "resume", "pause", "status", "connect"])
  })

  it("records errorSince once and clears it on recovery", () => {
    const db = memoryDb()
    upsertConnector(db, { id: "telegram", manifest }, KEY)
    setConnectorStatus(db, "telegram", "error", "boom", 1000)
    setConnectorStatus(db, "telegram", "error", "boom again", 5000)
    expect(getConnector(db, "telegram")?.errorSince).toBe(1000)
    setConnectorStatus(db, "telegram", "on", null, 6000)
    expect(getConnector(db, "telegram")?.errorSince).toBeNull()
  })
})

describe("settings", () => {
  it("returns defaults and merges new default fields into stored objects", () => {
    const db = memoryDb()
    expect(getSetting(db, "timezone")).toBe("Asia/Makassar")
    setSetting(db, "budgets", { ...DEFAULT_SETTINGS.budgets, maxSteps: 5 })
    expect(getSetting(db, "budgets").maxSteps).toBe(5)
    expect(getSetting(db, "budgets").maxRunsPerHour).toBe(60)
  })
})

describe("redact", () => {
  it("hides known secret shapes", () => {
    const s = redact("key rcl_abcdefgh123 and zkg_abcdefgh and sk-abcdefghijk and 123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw0 Bearer abcdefghijklmnop")
    expect(s).not.toMatch(/abcdefgh123|AAHdqTcv|abcdefghijklmnop/)
  })
})
