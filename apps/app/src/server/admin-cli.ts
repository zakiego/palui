// Admin commands shared by `pnpm cli` (tsx, dev checkout) and the bundled
// .output/server/cli.mjs (production image, `docker compose exec app palui ...`).
// Keep stdout clean: only the command's result goes there, so --json output can
// be piped.
import type { ControlClient } from "@palui/core/control"
import { getChat } from "@palui/core/chats"
import { getSecrets, listConnectors } from "@palui/core/connectors/store"
import type { DB } from "@palui/db"
import { decrypt } from "@palui/db/crypto"
import { hooks } from "@palui/db"
import { backupDb } from "./backup"
import { createHook } from "./hooks-bridge"
import { createOwnerInvite, INVITE_TTL_MS, ownerExists } from "./owner-invite"
import { connectConnectorAction, pauseConnectorAction, resumeConnectorAction, ConnectorError, saveDraft } from "./connectors"

export const USAGE = `usage: palui <command> [--json]
  owner invite                  print a single-use owner link (15 min). Also the
                                recovery path for a lost passkey
  connectors list               every connector with its state
  connectors on <id>            resume a connector
  connectors off <id>           pause a connector (keeps its credentials)
  connectors connect <type> [--id <id>] [--set key=value ...] [--secret-stdin <field>]
                                save and switch on a connector without the dashboard.
                                The secret is read from stdin so it stays out of shell history:
                                palui connectors connect telegram --secret-stdin botToken < token.txt
  backup <dest-path>            gzipped online snapshot of the database, safe while running
  secrets-check [--require-some]
                                decrypt every stored secret with MASTER_KEY, print counts only.
                                --require-some also fails when there was nothing to decrypt
  hook create <name> --chat <chatId> [--mode forward|summarize]
                                create an inbound webhook and print its URL and secret once`

export type CliIo = {
  db: () => DB
  rt: () => ControlClient
  appUrl: () => string
  masterKey: () => string
  dbFile: () => string
  out: (line: string) => void
  err: (line: string) => void
  now?: () => number
  // All of stdin, for --secret-stdin.
  readStdin?: () => Promise<string>
}

const BOOL_FLAGS = ["--json", "--help", "-h", "--require-some"]
const VALUE_FLAGS = ["--chat", "--mode", "--id", "--secret-stdin"]
const MULTI_FLAGS = ["--set"]

function parse(argv: string[]) {
  const positionals: string[] = []
  const bools = new Set<string>()
  const values: Record<string, string> = {}
  const multi: Record<string, string[]> = {}
  const unknown: string[] = []
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!
    const [name, inline] = a.startsWith("--") && a.includes("=") ? [a.slice(0, a.indexOf("=")), a.slice(a.indexOf("=") + 1)] : [a, undefined]
    if (MULTI_FLAGS.includes(name)) {
      const v = inline ?? argv[++i]
      if (v === undefined) unknown.push(`${name} needs a value`)
      else (multi[name] ??= []).push(v)
    } else if (VALUE_FLAGS.includes(name)) {
      const v = inline ?? argv[++i]
      if (v === undefined) unknown.push(`${name} needs a value`)
      else values[name] = v
    } else if (BOOL_FLAGS.includes(a)) bools.add(a)
    else if (a.startsWith("-")) unknown.push(a)
    else positionals.push(a)
  }
  return { positionals, bools, values, multi, unknown }
}

export async function runCli(argv: string[], io: CliIo): Promise<number> {
  const { positionals, bools, values, multi, unknown } = parse(argv)
  const [cmd, sub, ...rest] = positionals
  const json = bools.has("--json")
  const now = io.now ?? Date.now
  if (unknown.length) {
    io.err(`unknown flag: ${unknown.join(" ")}\n${USAGE}`)
    return 1
  }
  if (!cmd || bools.has("--help") || bools.has("-h")) {
    io.out(USAGE)
    return 0
  }

  try {
    if (cmd === "owner" && sub === "invite") {
      const db = io.db()
      const recovery = ownerExists(db)
      const inv = createOwnerInvite(db, io.appUrl(), now())
      if (json) io.out(JSON.stringify({ url: inv.url, expiresAt: inv.expiresAt, recovery }))
      else {
        io.out(inv.url)
        io.err(
          `${recovery ? "Recovery link: it removes the old passkeys and signs out other sessions." : "Owner link."} Works once, for ${INVITE_TTL_MS / 60_000} minutes.`,
        )
      }
      return 0
    }

    if (cmd === "connectors") {
      const db = io.db()
      if (sub === "list") {
        const rows = listConnectors(db)
        if (json) io.out(JSON.stringify(rows.map((r) => ({ id: r.id, type: r.type, kind: r.kind, enabled: r.enabled, status: r.status, detail: r.statusDetail }))))
        else if (!rows.length) io.out("no connectors yet: add one in the dashboard (Connectors)")
        else for (const r of rows) io.out(`${r.id.padEnd(20)} ${r.kind.padEnd(8)} ${r.status}${r.statusDetail ? ` (${r.statusDetail})` : ""}`)
        return 0
      }
      if (sub === "on" || sub === "off") {
        const id = rest[0]
        if (!id) {
          io.err(`usage: palui connectors ${sub} <id>`)
          return 1
        }
        const deps = { db, rt: io.rt(), masterKey: "" }
        const res = sub === "on" ? await resumeConnectorAction(deps, id, "cli", now()) : await pauseConnectorAction(deps, id, "cli", now())
        if (json) io.out(JSON.stringify({ id, state: sub === "on" ? "resumed" : "paused", runtimeReachable: res.runtimeReachable }))
        else io.out(`${id} ${sub === "on" ? "resumed" : "paused"}`)
        if (!res.runtimeReachable) io.err("The runtime isn't reachable, so it picks this up when it starts. Check docker compose logs runtime.")
        return 0
      }
      if (sub === "connect") {
        const type = rest[0]
        if (!type) {
          io.err("usage: palui connectors connect <type> [--id <id>] [--set key=value ...] [--secret-stdin <field>]")
          return 1
        }
        const config: Record<string, unknown> = {}
        for (const pair of multi["--set"] ?? []) {
          const eq = pair.indexOf("=")
          if (eq < 1) {
            io.err(`--set needs key=value, got "${pair}"`)
            return 1
          }
          config[pair.slice(0, eq)] = pair.slice(eq + 1)
        }
        const secrets: Record<string, string> = {}
        const field = values["--secret-stdin"]
        if (field) {
          if (!io.readStdin) throw new ConnectorError("This build can't read stdin.")
          const secret = (await io.readStdin()).trim()
          if (!secret) {
            io.err(`nothing on stdin for ${field}`)
            return 1
          }
          secrets[field] = secret
        }
        const id = values["--id"] ?? type
        const deps = { db, rt: io.rt(), masterKey: io.masterKey() }
        saveDraft(deps, { id, type, config, secrets }, now())
        const res = await connectConnectorAction(deps, id, now())
        if (json) io.out(JSON.stringify({ id, type, state: "connecting", runtimeReachable: res.runtimeReachable }))
        else io.out(`${id} saved and switched on`)
        if (!res.runtimeReachable) io.err("The runtime isn't reachable, so it picks this up when it starts. Check docker compose logs runtime.")
        return 0
      }
      io.err(USAGE)
      return 1
    }

    if (cmd === "backup") {
      const dest = sub
      if (!dest) {
        io.err("usage: palui backup <dest-path>   (e.g. /data/backups/palui.db.gz)")
        return 1
      }
      const res = await backupDb(io.dbFile(), dest)
      io.out(json ? JSON.stringify(res) : `backup ${res.dest} ${res.bytes} bytes (db ${res.dbBytes} bytes)`)
      return 0
    }

    if (cmd === "secrets-check") {
      const db = io.db()
      const key = io.masterKey()
      let connectors = 0
      let secrets = 0
      let hookSecrets = 0
      const failed: string[] = []
      for (const row of listConnectors(db)) {
        if (!row.secrets) continue
        try {
          secrets += Object.keys(getSecrets(row, key)).length
          connectors++
        } catch {
          failed.push(`connector ${row.id}`)
        }
      }
      for (const h of db.select({ slug: hooks.slug, secret: hooks.secret }).from(hooks).all()) {
        try {
          decrypt(h.secret, key)
          hookSecrets++
        } catch {
          failed.push(`hook ${h.slug}`)
        }
      }
      // An empty database decrypts nothing and fails nothing. The restore drill
      // passes --require-some so that can't count as proof the key fits.
      const empty = bools.has("--require-some") && secrets + hookSecrets === 0
      const res = { ok: failed.length === 0 && !empty, connectors, secrets, hooks: hookSecrets, failed }
      if (json) io.out(JSON.stringify(res))
      else {
        io.out(`decrypted ${secrets} secrets in ${connectors} connectors and ${hookSecrets} hook secrets`)
        if (failed.length) io.err(`failed to decrypt: ${failed.join(", ")}. Is MASTER_KEY the one this database was written with?`)
        if (empty) io.err("nothing to decrypt: no connector or hook secrets in this database, so it proves nothing about MASTER_KEY")
      }
      return res.ok ? 0 : 1
    }

    if (cmd === "hook" && sub === "create") {
      const name = rest.join(" ").trim()
      const chatId = values["--chat"]
      const mode = (values["--mode"] ?? "forward") as "forward" | "summarize"
      if (!name || !chatId) {
        io.err("usage: palui hook create <name> --chat <chatId> [--mode forward|summarize]")
        return 1
      }
      if (mode !== "forward" && mode !== "summarize") {
        io.err("--mode must be forward or summarize")
        return 1
      }
      const db = io.db()
      if (!getChat(db, chatId)) {
        io.err(`no chat "${chatId}". Chat ids are on the Chats page.`)
        return 1
      }
      const { hook, secret } = createHook(db, { name, chatId, mode }, io.masterKey(), now())
      const url = `${io.appUrl()}/api/hooks/${hook.slug}`
      if (json) io.out(JSON.stringify({ id: hook.id, slug: hook.slug, url, secret, mode }))
      else {
        io.out(url)
        io.out(secret)
        io.err("That secret is shown once. Send x-palui-timestamp (unix seconds) and x-palui-signature: sha256=<hex HMAC-SHA256 of '<timestamp>.<raw body>'> with it.")
      }
      return 0
    }

    io.err(`unknown command: ${[cmd, sub].filter(Boolean).join(" ")}\n${USAGE}`)
    return 1
  } catch (err) {
    if (err instanceof ConnectorError) {
      const fields = Object.entries(err.fields ?? {}).map(([k, v]) => `${k}: ${v}`)
      io.err(fields.length ? `${err.message} ${fields.join("; ")}` : err.message)
      return 1
    }
    throw err
  }
}
