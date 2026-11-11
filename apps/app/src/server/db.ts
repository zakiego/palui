// One database connection per app process. The app is the only service that
// runs migrations (PLAN 3.1), so it opens with migrate on.
import { config } from "@palui/core/config"
import { dbPath, openDb, type DB } from "@palui/db"
import { removeRetiredConnectors } from "@palui/core/connectors/store"

let instance: DB | undefined

export function getDb(): DB {
  if (!instance) {
    instance = openDb(dbPath(config().dataDir), { migrate: true })
    // Cleanup after the SQL migrations: drop rows of connector types that no
    // longer exist (the routines builtin). Idempotent, never fatal.
    try {
      removeRetiredConnectors(instance)
    } catch (e) {
      console.error("removing retired connectors failed", e)
    }
  }
  return instance
}

// Tests swap in a memory DB.
export function setDb(db: DB | undefined) {
  instance = db
}
