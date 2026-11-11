// Admin CLI entry. Dev: `pnpm cli <command>`. Production image:
// `node .output/server/cli.mjs <command>` (bundled by vite.cli.config.ts), which
// the image exposes as `palui`. Opens $DATA_DIR/palui.db with migrations, same
// as the app.
import { config, masterKey } from "@palui/core/config"
import { dbPath } from "@palui/db"
import { runCli } from "./server/admin-cli"
import { getDb } from "./server/db"
import { runtime } from "./server/runtime"

process.exitCode = await runCli(process.argv.slice(2), {
  db: getDb,
  rt: runtime,
  appUrl: () => config().appUrl,
  masterKey: () => masterKey(),
  dbFile: () => dbPath(config().dataDir),
  out: (line) => console.log(line),
  err: (line) => console.error(line),
  readStdin: async () => {
    const chunks: Buffer[] = []
    for await (const c of process.stdin) chunks.push(c as Buffer)
    return Buffer.concat(chunks).toString("utf8")
  },
})
