// Dev only: named datasets for the dashboard, swapped at the data boundary (the
// SQLite file every page reads). `applyFixture` clears the product tables and
// fills them again, so flipping between "Demo" and "Worst case" changes what
// the queries return and nothing else. Sign-in rows are left alone, so the
// session survives a swap.
//
// Nothing here runs in production: the server function refuses outside a dev
// build, and the script refuses when NODE_ENV is production.
import { schema, type DB } from "@palui/db"
import { eq, like, sql } from "drizzle-orm"
import { demo } from "./demo"
import { huge } from "./huge"
import { createKit, FIXTURE_PASSKEY_PREFIX, type Kit } from "./kit"
import { one } from "./one"
import { worst } from "./worst"

// The toggle's segments, in order (src/components/dev/fixture-toggle.tsx).
export const FIXTURE_NAMES = ["demo", "worst", "empty", "one", "huge"] as const

export type FixtureName = (typeof FIXTURE_NAMES)[number]

const fill: Record<FixtureName, (k: Kit) => void> = {
  demo,
  worst,
  // A fresh install: no connectors, no chats, nothing to list.
  empty: () => {},
  one,
  huge,
}

// The `settings` row that remembers which fixture is loaded. Not one of the
// typed settings, so the app's own settings code never reads it.
const MARKER_KEY = "devFixture"

const PRODUCT_TABLES = [
  schema.connectors,
  schema.identities,
  schema.claims,
  schema.chats,
  schema.sessions,
  schema.messages,
  schema.runs,
  schema.toolCalls,
  schema.pendingApprovals,
  schema.reminders,
  schema.jobs,
  schema.routines,
  schema.routineOccurrences,
  schema.outbox,
  schema.events,
  schema.reports,
  schema.hooks,
  schema.instructions,
  schema.agents,
  schema.preferences,
  schema.savedAttachments,
  schema.usage,
  schema.settings,
  schema.flowStepRuns,
  schema.flowRuns,
  schema.flowTriggers,
  schema.flowVersions,
  schema.flows,
] as const

function clearProductData(db: DB) {
  for (const table of PRODUCT_TABLES) db.delete(table).run()
  db.delete(schema.passkey).where(like(schema.passkey.id, `${FIXTURE_PASSKEY_PREFIX}%`)).run()
}

export function currentFixture(db: DB): FixtureName | null {
  const row = db.select().from(schema.settings).where(eq(schema.settings.key, MARKER_KEY)).get()
  const name = row?.value
  return FIXTURE_NAMES.find((n) => n === name) ?? null
}

// True when the database has connectors or messages that no fixture put there:
// someone's working dev database, with pairings and history worth keeping.
export function holdsRealData(db: DB): boolean {
  if (currentFixture(db)) return false
  const count = (table: typeof schema.connectors | typeof schema.messages) => Number(db.select({ n: sql<number>`count(*)` }).from(table).get()?.n ?? 0)
  return count(schema.connectors) > 0 || count(schema.messages) > 0
}

// Destructive: every product row in this database is replaced. It refuses a
// database that holds real data unless `force` says the caller means it.
export function applyFixture(db: DB, name: FixtureName, opts: { now?: number; force?: boolean } = {}) {
  if (!opts.force && holdsRealData(db)) throw new Error("This database has data that didn't come from a fixture. Point DATA_DIR at a scratch directory, or pass --force to replace it.")
  const now = opts.now ?? Date.now()
  db.transaction(() => {
    clearProductData(db)
    fill[name](createKit(db, now))
    db.insert(schema.settings).values({ key: MARKER_KEY, value: name, updatedAt: now }).run()
  })
}
