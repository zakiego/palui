// One database connection per app process. The app is the only service that
// runs migrations (PLAN 3.1), so it opens with migrate on.
import { config } from "@palui/core/config"
import { dbPath, openDb, type DB } from "@palui/db"

let instance: DB | undefined

export function getDb(): DB {
  instance ??= openDb(dbPath(config().dataDir), { migrate: true })
  return instance
}

// Tests swap in a memory DB.
export function setDb(db: DB | undefined) {
  instance = db
}
