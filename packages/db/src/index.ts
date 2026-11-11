import fs from "node:fs"
import path from "node:path"
import Database from "better-sqlite3"
import { drizzle, type BetterSQLite3Database } from "drizzle-orm/better-sqlite3"
import { migrate, migrations, unknownMigrations } from "./migrate"
import * as schema from "./schema"

export type Sqlite = Database.Database
export type DB = BetterSQLite3Database<typeof schema> & { $client: Sqlite }

export type OpenOptions = {
  // Only the app runs migrations (PLAN 3.1). The runtime opens with migrate: false
  // and checks schemaState() instead.
  migrate?: boolean
}

export function openDb(file: string, opts: OpenOptions = {}): DB {
  if (file !== ":memory:") fs.mkdirSync(path.dirname(file), { recursive: true })
  const sqlite = new Database(file)
  sqlite.pragma("journal_mode = WAL")
  sqlite.pragma("synchronous = NORMAL")
  sqlite.pragma("foreign_keys = ON")
  sqlite.pragma("busy_timeout = 5000")
  if (opts.migrate !== false) migrate(sqlite)
  return drizzle(sqlite, { schema }) as DB
}

// In-memory database with every migration applied, for tests.
export function memoryDb(): DB {
  return openDb(":memory:")
}

export function dataDir(): string {
  return process.env.DATA_DIR || "./data"
}

export function dbPath(dir = dataDir()): string {
  return path.join(dir, "palui.db")
}

export type SchemaState =
  | { ok: true; version: string }
  | { ok: false; reason: "behind"; missing: string[] }
  | { ok: false; reason: "newer"; unknown: string[] }

// Whether this build's code matches the database schema. "newer" means a newer
// image migrated the file (a bad rollback); "behind" means migrations haven't run.
export function schemaState(sqlite: Sqlite): SchemaState {
  sqlite.exec("CREATE TABLE IF NOT EXISTS _migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)")
  const unknown = unknownMigrations(sqlite)
  if (unknown.length) return { ok: false, reason: "newer", unknown }
  const applied = new Set(sqlite.prepare("SELECT name FROM _migrations").pluck().all() as string[])
  const missing = migrations.map((m) => m.name).filter((n) => !applied.has(n))
  if (missing.length) return { ok: false, reason: "behind", missing }
  return { ok: true, version: migrations.at(-1)?.name ?? "none" }
}

export { schema, migrate, migrations, unknownMigrations }
export * from "./schema"
export { newId, randomToken } from "./ids"
