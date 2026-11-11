// Dev only: fills $DATA_DIR/palui.db with one of the fixtures in
// src/dev/fixtures, replacing whatever product data is there.
//   DATA_DIR=/tmp/palui-demo pnpm tsx scripts/seed-demo.ts [demo|worst|empty|one|huge] [--force]
// `demo` (the default) is the data the pages were designed against; `worst` is
// the realistic worst case for every value they render. Once a database holds a
// fixture, the dev dashboard shows a toggle at the bottom to switch between them.
// Refuses to run when NODE_ENV is production, and refuses a database with data
// that didn't come from a fixture unless --force is given.
import { config } from "@palui/core/config"
import { dbPath, openDb } from "@palui/db"
import { applyFixture, FIXTURE_NAMES, type FixtureName } from "../src/dev/fixtures"

if (config().mode === "prod") throw new Error("seed-demo is for dev databases only")

const args = process.argv.slice(2)
const force = args.includes("--force")
const wanted = args.find((a) => !a.startsWith("--")) ?? "demo"
const name: FixtureName | undefined = FIXTURE_NAMES.find((n) => n === wanted)
if (!name) throw new Error(`Unknown fixture "${wanted}". Use one of: ${FIXTURE_NAMES.join(", ")}`)

const db = openDb(dbPath(config().dataDir))
applyFixture(db, name, { force })

console.log(`seeded ${dbPath(config().dataDir)} with the "${name}" fixture`)
