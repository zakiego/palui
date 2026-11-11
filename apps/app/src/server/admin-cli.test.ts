import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { ensureWebChat } from "@palui/core/chats"
import { getConnector, upsertConnector } from "@palui/core/connectors/store"
import { hooks, memoryDb, openDb, type DB } from "@palui/db"
import { decrypt } from "@palui/db/crypto"
import { describe, expect, it } from "vitest"
import { runCli, type CliIo } from "./admin-cli"
import { inspectInvite } from "./owner-invite"
import { downRuntime, TEST_MASTER_KEY, upRuntime } from "./test-helpers"

function harness(opts: { db?: DB; rt?: CliIo["rt"]; key?: string; dbFile?: string; stdin?: string } = {}) {
  const db = opts.db ?? memoryDb()
  const out: string[] = []
  const err: string[] = []
  const rt = upRuntime()
  const io: CliIo = {
    db: () => db,
    rt: opts.rt ?? (() => rt),
    appUrl: () => "https://palui.example.com",
    masterKey: () => opts.key ?? TEST_MASTER_KEY,
    dbFile: () => opts.dbFile ?? ":memory:",
    out: (l) => out.push(l),
    err: (l) => err.push(l),
    readStdin: async () => opts.stdin ?? "",
  }
  return { db, out, err, rt, run: (...argv: string[]) => runCli(argv, io) }
}

const telegram = { id: "telegram", kind: "channel" as const, name: "Telegram" }

describe("admin cli", () => {
  it("prints usage with no command and fails on an unknown one", async () => {
    const h = harness()
    expect(await h.run()).toBe(0)
    expect(h.out[0]).toMatch(/owner invite/)
    expect(await h.run("frobnicate")).toBe(1)
    expect(await h.run("connectors", "--nope")).toBe(1)
  })

  it("owner invite prints a working single-use link", async () => {
    const h = harness()
    expect(await h.run("owner", "invite")).toBe(0)
    const url = h.out[0]!
    expect(url).toMatch(/^https:\/\/palui\.example\.com\/app\/invite\/[\w-]+$/)
    expect(inspectInvite(h.db, url.split("/").at(-1)!)).toMatchObject({ ok: true })
    expect(h.err[0]).toMatch(/15 minutes/)
    expect(await h.run("owner", "invite", "--json")).toBe(0)
    expect(JSON.parse(h.out[1]!)).toMatchObject({ url: expect.any(String), recovery: false })
  })

  it("connectors list, off and on", async () => {
    const h = harness()
    expect(await h.run("connectors", "list")).toBe(0)
    expect(h.out[0]).toMatch(/no connectors yet/)
    upsertConnector(h.db, { id: "telegram", manifest: telegram, enabled: true }, TEST_MASTER_KEY)
    h.db.$client.prepare("UPDATE connectors SET status = 'on'").run()

    expect(await h.run("connectors", "off", "telegram")).toBe(0)
    expect(h.out.at(-1)).toBe("telegram paused")
    expect(getConnector(h.db, "telegram")!.status).toBe("paused")
    expect(h.rt.reconcile).toHaveBeenCalled()

    expect(await h.run("connectors", "on", "telegram", "--json")).toBe(0)
    expect(JSON.parse(h.out.at(-1)!)).toEqual({ id: "telegram", state: "resumed", runtimeReachable: true })

    expect(await h.run("connectors", "list", "--json")).toBe(0)
    expect(JSON.parse(h.out.at(-1)!)).toEqual([{ id: "telegram", type: "telegram", kind: "channel", enabled: true, status: "connecting", detail: null }])

    expect(await h.run("connectors", "off", "nope")).toBe(1)
    expect(h.err.at(-1)).toMatch(/No connector "nope"/)
    expect(await h.run("connectors", "off")).toBe(1)
  })

  it("connectors off still works with the runtime down, and says so", async () => {
    const h = harness({ rt: downRuntime })
    upsertConnector(h.db, { id: "telegram", manifest: telegram, enabled: true }, TEST_MASTER_KEY)
    h.db.$client.prepare("UPDATE connectors SET status = 'on'").run()
    expect(await h.run("connectors", "off", "telegram")).toBe(0)
    expect(getConnector(h.db, "telegram")!.status).toBe("paused")
    expect(h.err.at(-1)).toMatch(/runtime isn't reachable/)
  })

  it("secrets-check prints counts and fails on the wrong key", async () => {
    const h = harness()
    upsertConnector(h.db, { id: "telegram", manifest: telegram, secrets: { botToken: "a", other: "b" } }, TEST_MASTER_KEY)
    expect(await h.run("secrets-check")).toBe(0)
    expect(h.out.at(-1)).toBe("decrypted 2 secrets in 1 connectors and 0 hook secrets")
    expect(h.out.join("\n")).not.toContain("botToken")

    const wrong = harness({ db: h.db, key: "another-master-key-0123456789-abcdefghijkl" })
    expect(await wrong.run("secrets-check", "--json")).toBe(1)
    expect(JSON.parse(wrong.out[0]!)).toMatchObject({ ok: false, failed: ["connector telegram"] })
  })

  it("secrets-check --require-some fails when there's nothing to decrypt", async () => {
    const h = harness()
    expect(await h.run("secrets-check")).toBe(0)
    expect(await h.run("secrets-check", "--require-some", "--json")).toBe(1)
    expect(JSON.parse(h.out.at(-1)!)).toMatchObject({ ok: false, secrets: 0, hooks: 0, failed: [] })
    expect(await h.run("secrets-check", "--require-some")).toBe(1)
    expect(h.err.at(-1)).toMatch(/nothing to decrypt/)
    upsertConnector(h.db, { id: "telegram", manifest: telegram, secrets: { botToken: "a" } }, TEST_MASTER_KEY)
    expect(await h.run("secrets-check", "--require-some")).toBe(0)
  })

  it("hook create prints the URL and the secret once", async () => {
    const h = harness()
    const chat = ensureWebChat(h.db)
    expect(await h.run("hook", "create", "GitHub", "deploys", "--chat", chat.id, "--mode", "summarize")).toBe(0)
    expect(h.out[0]).toMatch(/^https:\/\/palui\.example\.com\/api\/hooks\/[A-Za-z0-9_-]{6,}$/)
    const secret = h.out[1]!
    const row = h.db.select().from(hooks).get()!
    expect(row).toMatchObject({ name: "GitHub deploys", mode: "summarize", chatId: chat.id })
    expect(row.secret).not.toContain(secret)
    expect(decrypt(row.secret, TEST_MASTER_KEY)).toBe(secret)

    expect(await h.run("hook", "create", "x", "--chat", "chat_missing")).toBe(1)
    expect(await h.run("hook", "create", "x", "--chat", chat.id, "--mode", "loud")).toBe(1)
    expect(await h.run("hook", "create", "--chat", chat.id)).toBe(1)
  })

  it("backup writes a gzipped snapshot that passes quick_check", async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "palui-backup-"))
    try {
      const file = path.join(dir, "palui.db")
      const db = openDb(file)
      upsertConnector(db, { id: "telegram", manifest: telegram }, TEST_MASTER_KEY)
      const h = harness({ db, dbFile: file })
      const dest = path.join(dir, "backups", "palui.db.gz")
      expect(await h.run("backup", dest)).toBe(0)
      expect(h.out[0]).toMatch(/^backup .*palui\.db\.gz \d+ bytes/)
      const head = fs.readFileSync(dest).subarray(0, 2)
      expect([...head]).toEqual([0x1f, 0x8b])
      db.$client.close()
    } finally {
      fs.rmSync(dir, { recursive: true, force: true })
    }
  })

  it("connectors connect saves the secret from stdin and switches the connector on", async () => {
    const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw0"
    const h = harness({ stdin: `${token}\n` })
    expect(await h.run("connectors", "connect", "telegram", "--secret-stdin", "botToken")).toBe(0)
    const row = getConnector(h.db, "telegram")!
    expect(row).toMatchObject({ enabled: true, status: "connecting", type: "telegram" })
    expect(row.secrets).not.toContain(token)
    expect(h.out.join("\n")).not.toContain(token)
  })

  it("connectors connect refuses a bad secret and names the field", async () => {
    const h = harness({ stdin: "not-a-token" })
    expect(await h.run("connectors", "connect", "telegram", "--secret-stdin", "botToken")).toBe(1)
    expect(h.err.join("\n")).toMatch(/botToken/)
  })
})
