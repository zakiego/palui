import { describe, expect, it } from "vitest"
import { createLogger, redact, redactDeep, silentLogger } from "./log.ts"

const BOT = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ"

describe("redact", () => {
  it("covers rcl_ keys with - and _, and bot tokens inside URLs", () => {
    expect(redact("key rcl_ab-cd_ef1234 end")).toBe("key rcl_•••• end")
    expect(redact(`GET https://api.telegram.org/bot${BOT}/getMe`)).toBe("GET https://api.telegram.org/bot1234••••/getMe")
    expect(redact(`token ${BOT}`)).toBe("token 1234••••")
    expect(redact("sk-abcdefgh1234 and zkg_abc-123_xyz")).toBe("sk-a•••• and zkg_••••")
    expect(redact("Authorization: Bearer abcdefghijklmnop")).toBe("Authorization: Bear••••")
  })

  it("leaves ordinary text alone", () => {
    expect(redact("run 12:30 at 2026-11-10")).toBe("run 12:30 at 2026-11-10")
  })

  it("walks nested event data", () => {
    expect(redactDeep({ a: [`x rcl_secret-key_1`], b: { c: 1, d: null } })).toEqual({ a: ["x rcl_••••"], b: { c: 1, d: null } })
  })
})

describe("createLogger", () => {
  const capture = (min: "debug" | "info" | "warn" | "error" = "info") => {
    const lines: string[] = []
    return { lines, log: createLogger({ app: "bridge" }, (l) => lines.push(l), min) }
  }

  it("writes one JSON line per call with the base and call fields", () => {
    const { lines, log } = capture()
    log.info("started", { port: 4893 })
    expect(lines).toHaveLength(1)
    const rec = JSON.parse(lines[0]!)
    expect(rec).toMatchObject({ level: "info", msg: "started", app: "bridge", port: 4893 })
    expect(typeof rec.t).toBe("string")
  })

  it("drops lines below the minimum level", () => {
    const { lines, log } = capture("warn")
    log.debug("d")
    log.info("i")
    log.warn("w")
    log.error("e")
    expect(lines.map((l) => JSON.parse(l).level)).toEqual(["warn", "error"])
  })

  it("redacts the whole line, fields included", () => {
    const { lines, log } = capture()
    log.error(`call failed with ${BOT}`, { url: `/bot${BOT}/x`, key: "rcl_abc-def_123" })
    expect(lines[0]).not.toContain(BOT)
    expect(lines[0]).not.toContain("rcl_abc-def_123")
  })

  it("serializes errors and child fields", () => {
    const { lines, log } = capture()
    log.child({ command: "gh" }).warn("failed", { err: new Error("boom") })
    const rec = JSON.parse(lines[0]!)
    expect(rec.command).toBe("gh")
    expect(rec.err).toMatchObject({ name: "Error", message: "boom" })
  })

  it("survives fields that can't be serialized", () => {
    const { lines, log } = capture()
    const loop: Record<string, unknown> = {}
    loop.self = loop
    log.info("x", { loop })
    expect(JSON.parse(lines[0]!)).toMatchObject({ level: "info", msg: "x", note: "unserializable fields" })
  })

  it("silentLogger writes nothing", () => {
    expect(() => silentLogger.child({}).error("x")).not.toThrow()
  })
})
