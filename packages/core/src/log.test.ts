import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { listEvents, logEvent } from "./events"
import { redact, redactDeep } from "./log"
import { seedConnector } from "./chat/testing"
import { getConnector, setConnectorStatus } from "./connectors/store"

const BOT = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ"

describe("redact", () => {
  it("covers rcl_ keys with - and _, and bot tokens inside URLs", () => {
    expect(redact("key rcl_ab-cd_ef1234 end")).toBe("key rcl_•••• end")
    expect(redact(`GET https://api.telegram.org/bot${BOT}/getMe`)).toBe("GET https://api.telegram.org/bot1234••••/getMe")
    expect(redact(`token ${BOT}`)).toBe("token 1234••••")
    expect(redact("sk-abcdefgh1234 and zkg_abc-123_xyz")).toBe("sk-a•••• and zkg_••••")
  })

  it("leaves ordinary text alone", () => {
    expect(redact("run 12:30 at 2026-11-10")).toBe("run 12:30 at 2026-11-10")
  })

  it("walks nested event data", () => {
    expect(redactDeep({ a: [`x rcl_secret-key_1`], b: { c: 1, d: null } })).toEqual({ a: ["x rcl_••••"], b: { c: 1, d: null } })
  })
})

describe("stored text is redacted", () => {
  it("logEvent redacts the message and the data", () => {
    const db = memoryDb()
    logEvent(db, { kind: "x", message: `failed with rcl_abc-def_123`, data: { url: `/bot${BOT}/x` } })
    const e = listEvents(db)[0]!
    expect(e.message).toBe("failed with rcl_••••")
    expect(JSON.stringify(e.data)).not.toContain(BOT)
  })

  it("setConnectorStatus redacts the detail", () => {
    const db = memoryDb()
    seedConnector(db, "telegram", "channel")
    setConnectorStatus(db, "telegram", "error", `409 at https://api.telegram.org/bot${BOT}/getUpdates`)
    expect(getConnector(db, "telegram")!.statusDetail).not.toContain(BOT)
    expect(listEvents(db)[0]!.message).not.toContain(BOT)
  })
})
