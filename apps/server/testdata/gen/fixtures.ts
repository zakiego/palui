// Exports the dashboard's dev fixtures (apps/app/src/dev/fixtures) as goldens
// for the Go port: each fixture applied with a fixed clock to a fresh migrated
// database that has an owner and one real passkey, then every table dumped in
// rowid order into testdata/fixtures-<name>.json. `huge` keeps only the row
// count and the first and last row of each table.
//
// Also writes the two gradient PNGs the kit makes (Node's zlib output) into
// internal/fixtures/assets, which the Go kit embeds so its bytes match.
//
// Run from apps/app (its tsconfig resolves the fixture's "@/" imports):
//   cd apps/app && pnpm tsx ../server/testdata/gen/fixtures.ts
//
// Volatile values are normalized the same way by the Go test
// (internal/fixtures/golden_test.go):
// - ids (`<prefix>_` plus 19 base36 characters starting with 0) become
//   `<prefix#n>`, numbered per prefix in order of first appearance;
// - hooks.slug that is the hook id's last 16 characters becomes "<id tail>";
// - attachment_shares.token_hash becomes "<token hash>";
// - connectors.config_hash that matches its config and secrets becomes "<config hash>";
// - encrypted columns are decrypted (connectors.secrets, hooks.secret);
// - saved_attachments.data becomes {bytes, sha256} of the decrypted bytes with
//   the random `fx_...` id the kit writes into fake files masked, and
//   saved_attachments.sha256 that matches the raw bytes becomes "<sha256 of data>";
// - the default agent's created_at and updated_at become "<wall clock>" (the
//   kit creates it through getDefaultAgent without a time).
import { createHash } from "node:crypto"
import { writeFileSync } from "node:fs"
import path from "node:path"
import { memoryDb, schema, type DB } from "../../../../packages/db/src/index.ts"
import { decrypt, decryptBytes } from "../../../../packages/db/src/crypto.ts"
import { DEV_MASTER_KEY, setConfig } from "../../../../packages/core/src/config.ts"
import { applyFixture, FIXTURE_NAMES } from "../../../app/src/dev/fixtures/index.ts"
import { gradientAvatar } from "../../../app/src/dev/fixtures/kit.ts"

const KEY = DEV_MASTER_KEY
// 2026-11-14 10:00 WITA, the clock seed-db.ts uses too.
const NOW = Date.UTC(2026, 10, 14, 2, 0)

setConfig({ masterKey: KEY, mode: "dev" })

const server = path.resolve(import.meta.dirname, "../..")
const testdata = path.join(server, "testdata")
const assets = path.join(server, "internal/fixtures/assets")

for (const px of [48, 320]) writeFileSync(path.join(assets, `gradient-${px}.png`), Buffer.from(gradientAvatar(px).split(",")[1]!, "base64"))

function addOwner(db: DB) {
  const at = new Date(NOW)
  db.insert(schema.user).values({ id: "user_owner", name: "Owner", email: "owner@palui.invalid", username: "owner", createdAt: at, updatedAt: at }).run()
  db.insert(schema.passkey).values({ id: "pk_real", name: "Real key", publicKey: "x", userId: "user_owner", credentialID: "cred_real", counter: 0, deviceType: "multiDevice", backedUp: true }).run()
}

const ID_RE = /[a-z]+_0[0-9a-z]{18}[0-9a-z]*/g
const FX_RE = /fx_[0-9a-z]{1,19}/g
const sha = (b: Buffer | string) => createHash("sha256").update(b).digest("hex")

function idNormalizer() {
  const seen = new Map<string, string>()
  const counts = new Map<string, number>()
  return (s: string) =>
    s.replace(ID_RE, (m) => {
      const us = m.indexOf("_")
      if (m.length - us - 1 !== 19) return m
      let p = seen.get(m)
      if (!p) {
        const prefix = m.slice(0, us)
        const n = (counts.get(prefix) ?? 0) + 1
        counts.set(prefix, n)
        p = `<${prefix}#${n}>`
        seen.set(m, p)
      }
      return p
    })
}

type Row = Record<string, unknown>

function rowValues(table: string, columns: string[], row: Row, norm: (s: string) => string): unknown[] {
  const special: Record<string, unknown> = {}
  if (table === "connectors") {
    if (row.secrets != null) special.secrets = decrypt(String(row.secrets), KEY)
    const want = sha(`${row.config}\0${row.secrets ?? ""}`).slice(0, 16)
    if (row.config_hash === want) special.config_hash = "<config hash>"
  }
  if (table === "hooks") {
    special.secret = decrypt(String(row.secret), KEY)
    if (row.slug === String(row.id).slice(-16)) special.slug = "<id tail>"
  }
  if (table === "attachment_shares") special.token_hash = "<token hash>"
  if (table === "saved_attachments") {
    const raw = decryptBytes(row.data as Buffer, KEY)
    const masked = Buffer.from(raw.toString("latin1").replace(FX_RE, (m) => "fx_" + "#".repeat(m.length - 3)), "latin1")
    special.data = { bytes: masked.length, sha256: sha(masked) }
    if (row.sha256 === sha(raw)) special.sha256 = "<sha256 of data>"
  }
  if (table === "agents" && row.is_default === 1) {
    special.created_at = "<wall clock>"
    special.updated_at = "<wall clock>"
  }
  return columns.map((c) => {
    const v = c in special ? special[c] : row[c]
    if (typeof v === "string") return norm(v)
    if (Buffer.isBuffer(v)) return { bytes: v.length, sha256: sha(v) }
    return v ?? null
  })
}

function exportDb(db: DB, sample: boolean) {
  const raw = db.$client
  const norm = idNormalizer()
  const names = (raw.prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT IN ('_migrations', 'sqlite_sequence') ORDER BY name").all() as { name: string }[]).map((r) => r.name)
  const tables: Record<string, unknown> = {}
  for (const t of names) {
    const columns = (raw.prepare(`PRAGMA table_info("${t}")`).all() as { name: string }[]).map((c) => c.name)
    const cols = columns.map((c) => `"${c}"`).join(", ")
    if (!sample) {
      const rows = raw.prepare(`SELECT ${cols} FROM "${t}" ORDER BY rowid`).all() as Row[]
      tables[t] = { columns, rows: rows.map((r) => rowValues(t, columns, r, norm)) }
      continue
    }
    const count = (raw.prepare(`SELECT count(*) AS n FROM "${t}"`).get() as { n: number }).n
    const first = raw.prepare(`SELECT ${cols} FROM "${t}" ORDER BY rowid LIMIT 1`).get() as Row | undefined
    const last = raw.prepare(`SELECT ${cols} FROM "${t}" ORDER BY rowid DESC LIMIT 1`).get() as Row | undefined
    tables[t] = { columns, count, first: first ? rowValues(t, columns, first, norm) : null, last: last ? rowValues(t, columns, last, norm) : null }
  }
  return tables
}

// One table per line group, one row per line: small diffs, readable files.
function format(name: string, tables: Record<string, unknown>): string {
  const parts = Object.entries(tables).map(([t, v]) => {
    const { rows, ...rest } = v as { rows?: unknown[] }
    const head = Object.entries(rest).map(([k, x]) => `   ${JSON.stringify(k)}: ${JSON.stringify(x)}`)
    if (rows) head.push(`   "rows": [${rows.length ? "\n" + rows.map((r) => "    " + JSON.stringify(r)).join(",\n") + "\n   " : ""}]`)
    return `  ${JSON.stringify(t)}: {\n${head.join(",\n")}\n  }`
  })
  return `{\n "fixture": ${JSON.stringify(name)},\n "now": ${NOW},\n "tables": {\n${parts.join(",\n")}\n }\n}\n`
}

for (const name of FIXTURE_NAMES) {
  const db = memoryDb()
  addOwner(db)
  applyFixture(db, name, { now: NOW, force: true })
  const file = path.join(testdata, `fixtures-${name}.json`)
  writeFileSync(file, format(name, exportDb(db, name === "huge")))
  console.log(`wrote ${path.relative(server, file)}`)
}
