// Builds apps/server/testdata/seeded.db with the TS code: the dashboard's
// `worst` fixture, plus two connectors with encrypted secrets, a hook with an
// encrypted secret and a saved attachment, all under the dev master key. Writes
// testdata/seeded.json with what the Go tests should find when they decrypt.
//
// Run from apps/app (its tsconfig resolves the fixture's "@/" imports):
//   cd apps/app && pnpm tsx ../server/testdata/gen/seed-db.ts
import { createHash } from "node:crypto"
import { mkdtempSync, rmSync, statSync, writeFileSync, copyFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { openDb, schemaState, migrations } from "../../../../packages/db/src/index.ts"
import { decrypt, decryptBytes, decryptJson } from "../../../../packages/db/src/crypto.ts"
import { DEV_MASTER_KEY, setConfig } from "../../../../packages/core/src/config.ts"
import { ensureChat } from "../../../../packages/core/src/chats.ts"
import { upsertConnector } from "../../../../packages/core/src/connectors/store.ts"
import { createHook, signHookBody } from "../../../../packages/core/src/hooks/index.ts"
import { deleteAttachment, saveAttachment } from "../../../../packages/core/src/attachments/service.ts"
import { applyFixture } from "../../../app/src/dev/fixtures/index.ts"

const KEY = DEV_MASTER_KEY
// The fixture and the extra rows share one clock: 2026-11-14 10:00 WITA.
const NOW = Date.UTC(2026, 10, 14, 2, 0)

setConfig({ masterKey: KEY, mode: "dev" })

const testdata = path.resolve(import.meta.dirname, "..")
const dir = mkdtempSync(path.join(tmpdir(), "palui-golden-"))
const file = path.join(dir, "palui.db")
const db = openDb(file)

applyFixture(db, "worst", { now: NOW, force: true })

// The fixture saves 10 MB and 1 MB files to show the size limit. Too big to
// commit: drop every attachment over 256 KB (and its links). The smaller
// encrypted files stay.
const MAX_KEPT = 256 * 1024
for (const r of db.$client.prepare("SELECT id FROM saved_attachments WHERE size > ?").all(MAX_KEPT) as { id: string }[]) deleteAttachment(db, r.id)

// Two connectors with secrets, written through the store (encryptJson).
upsertConnector(
  db,
  { id: "golden-telegram", manifest: { id: "telegram", kind: "channel", name: "Golden Telegram" }, secrets: { botToken: "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawZ" }, config: { allowGroups: true } },
  KEY,
  NOW,
)
upsertConnector(
  db,
  {
    id: "golden-mcp",
    manifest: { id: "mcp", kind: "tools", name: "Golden MCP", trust: "external" },
    secrets: { token: "zkg_golden_token_0123456789", header: "x-api-key-0123456789abcdef", note: "unicode é 😀" },
    config: { url: "https://mcp.example.com/mcp", z: 1, a: [1, 2] },
  },
  KEY,
  NOW,
)

const chat = ensureChat(db, { channel: "telegram", externalId: "golden-chat", title: "Golden chat" }, NOW)
const hook = createHook(db, { name: "Golden hook", mode: "summarize", chatId: chat.id }, KEY, NOW)
// A request the TS code signed, for the Go verifier.
const HOOK_BODY = JSON.stringify({ action: "opened", number: 7, title: "Golden \u00e9" })
const HOOK_TS = String(Math.floor(NOW / 1000))
const hookSignature = signHookBody(hook.secret, HOOK_TS, HOOK_BODY)
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
  "base64",
)
const att = saveAttachment(db, { chatId: chat.id, title: "Golden pixel", description: "1x1 PNG", mimeType: "image/png", filename: "pixel.png", bytes: PNG }, KEY, NOW)

// What the Go side must decrypt: every encrypted value in the file.
const connectors = (db.$client.prepare("SELECT id, secrets FROM connectors WHERE secrets IS NOT NULL ORDER BY id").all() as { id: string; secrets: string }[]).map((r) => ({
  id: r.id,
  secrets: decryptJson(r.secrets, KEY),
}))
const hooks = (db.$client.prepare("SELECT id, slug, secret FROM hooks ORDER BY id").all() as { id: string; slug: string; secret: string }[]).map((r) => ({
  id: r.id,
  slug: r.slug,
  secret: decrypt(r.secret, KEY),
}))
const attachments = (db.$client.prepare("SELECT id, sha256, size, data FROM saved_attachments ORDER BY id").all() as { id: string; sha256: string; size: number; data: Buffer }[]).map((r) => {
  const plain = decryptBytes(r.data, KEY)
  if (createHash("sha256").update(plain).digest("hex") !== r.sha256) throw new Error(`attachment ${r.id} hash`)
  return { id: r.id, sha256: r.sha256, size: r.size }
})
const counts = Object.fromEntries(
  (db.$client.prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name").all() as { name: string }[]).map((t) => [
    t.name,
    (db.$client.prepare(`SELECT count(*) AS n FROM "${t.name}"`).get() as { n: number }).n,
  ]),
)
const state = schemaState(db.$client)
if (!state.ok) throw new Error(`schema state ${JSON.stringify(state)}`)

// One self-contained file: fold the WAL in and drop free pages.
db.$client.pragma("wal_checkpoint(TRUNCATE)")
db.$client.pragma("journal_mode = DELETE")
db.$client.exec("VACUUM")
db.$client.close()

const target = path.join(testdata, "seeded.db")
copyFileSync(file, target)
rmSync(dir, { recursive: true, force: true })

writeFileSync(
  path.join(testdata, "seeded.json"),
  `${JSON.stringify(
    {
      masterKey: KEY,
      now: NOW,
      schemaVersion: migrations.at(-1)!.name,
      golden: { chatId: chat.id, hookId: hook.hook.id, hookSlug: hook.hook.slug, hookSecret: hook.secret, hookBody: HOOK_BODY, hookTimestamp: HOOK_TS, hookSignature, attachmentId: att.row.id, attachmentBase64: PNG.toString("base64") },
      connectors,
      hooks,
      attachments,
      counts,
    },
    null,
    2,
  )}\n`,
)
console.log(`wrote testdata/seeded.db (${statSync(target).size} bytes) and testdata/seeded.json`)
