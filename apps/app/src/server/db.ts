// One database connection per app process. The app is the only service that
// runs migrations (PLAN 3.1), so it opens with migrate on.
import { config } from "@palui/core/config"
import { dbPath, openDb, type DB } from "@palui/db"
import { syncRoutineFlows } from "@palui/core/flows/migrate"

let instance: DB | undefined

export function getDb(): DB {
  if (!instance) {
    instance = openDb(dbPath(config().dataDir), { migrate: true })
    // The data migration after the SQL ones (RFC flows 3.6): routines get their
    // flows (shadow ones until the owner hands over). Idempotent, never fatal.
    try {
      syncRoutineFlows(instance)
    } catch (e) {
      console.error("routine to flow migration failed", e)
    }
  }
  return instance
}

// Tests swap in a memory DB.
export function setDb(db: DB | undefined) {
  instance = db
}
