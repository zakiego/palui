import { describe, expect, it } from "vitest"
import Database from "better-sqlite3"
import { decrypt, decryptJson, encrypt, encryptJson, maskSecret, MasterKeyError } from "./crypto"
import { memoryDb, migrate, migrations, newId, schemaState } from "./index"

const KEY = "test-master-key-test-master-key-1234"

describe("crypto", () => {
  it("round-trips and uses a fresh IV each time", () => {
    const a = encrypt("hello", KEY)
    const b = encrypt("hello", KEY)
    expect(a).not.toBe(b)
    expect(decrypt(a, KEY)).toBe("hello")
  })

  it("fails with the wrong key or a tampered blob", () => {
    const blob = encrypt("secret", KEY)
    expect(() => decrypt(blob, `${KEY}x`)).toThrow()
    const parts = blob.split(".")
    parts[3] = Buffer.from("tampered").toString("base64url")
    expect(() => decrypt(parts.join("."), KEY)).toThrow()
  })

  it("rejects a short master key", () => {
    expect(() => encrypt("x", "short")).toThrow(MasterKeyError)
  })

  it("encrypts JSON and masks secrets", () => {
    expect(decryptJson(encryptJson({ apiKey: "rcl_abcdef123456a91f" }, KEY), KEY)).toEqual({ apiKey: "rcl_abcdef123456a91f" })
    expect(decryptJson(null, KEY)).toEqual({})
    expect(maskSecret("rcl_abcdef123456a91f")).toBe("rcl_••••a91f")
    expect(maskSecret("1234567:abcdefgh")).toBe("••••efgh")
  })

  it("shows no tail for secrets of 12 characters or fewer", () => {
    expect(maskSecret("")).toBe("")
    expect(maskSecret("abcd")).toBe("••••")
    expect(maskSecret("1234:abcd")).toBe("••••")
    expect(maskSecret("abcdefghijkl")).toBe("••••")
    expect(maskSecret("abcdefghijklm")).toBe("••••jklm")
    expect(maskSecret("sk-abc")).toBe("sk-••••")
    for (const short of ["a", "ab", "abc", "abcd", "abcdefghijkl"]) expect(maskSecret(short)).not.toContain(short.slice(-1))
  })
})

describe("schema", () => {
  it("applies every migration to a fresh database", () => {
    const db = memoryDb()
    expect(schemaState(db.$client)).toEqual({ ok: true, version: migrations.at(-1)!.name })
  })

  it("reports a database migrated by a newer build", () => {
    const sqlite = new Database(":memory:")
    migrate(sqlite)
    sqlite.prepare("INSERT INTO _migrations (name, applied_at) VALUES (?, ?)").run("9999_future", Date.now())
    expect(schemaState(sqlite)).toMatchObject({ ok: false, reason: "newer", unknown: ["9999_future"] })
  })

  it("reports a database behind the code", () => {
    const sqlite = new Database(":memory:")
    expect(schemaState(sqlite)).toMatchObject({ ok: false, reason: "behind" })
  })

  it("makes sortable ids", async () => {
    const a = newId("msg")
    await new Promise((r) => setTimeout(r, 2))
    const b = newId("msg")
    expect(a < b).toBe(true)
    expect(a).toMatch(/^msg_[0-9a-z]{19}$/)
  })
})
