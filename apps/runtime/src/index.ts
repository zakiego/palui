// Entry point: node dist/index.mjs (prod) or tsx watch src/index.ts (dev).
import { createLogger } from "@palui/core/log"
import { SchemaMismatchError, startRuntime, type Runtime } from "./main"
import { buildDeps } from "./deps"

const log = createLogger({ service: "runtime" })

let runtime: Runtime
try {
  runtime = await startRuntime({ log, deps: buildDeps })
} catch (e) {
  log.error(e instanceof SchemaMismatchError ? e.message : "runtime failed to start", { error: e })
  process.exit(1)
}

let signalled = false
for (const sig of ["SIGTERM", "SIGINT"] as const) {
  process.on(sig, () => {
    if (signalled) return
    signalled = true
    log.info("signal received, shutting down", { signal: sig })
    runtime
      .stop()
      .then(() => process.exit(0))
      .catch((e) => {
        log.error("shutdown failed", { error: e })
        process.exit(1)
      })
  })
}

process.on("unhandledRejection", (e) => log.error("unhandled rejection", { error: e }))
process.on("uncaughtException", (e) => log.error("uncaught exception", { error: e }))
