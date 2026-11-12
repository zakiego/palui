// The runtime process (PLAN 3.1): channel sockets, turns, outbox, supervisor and
// the control API. Parts built elsewhere (reminders, jobs, scheduler) come in
// through `deps`.
import { rm } from "node:fs/promises"
import { dbPath, openDb, schemaState, type DB } from "@palui/db"
import { manifestList } from "@palui/connectors/manifests"
import { runtimes } from "@palui/connectors/runtimes"
import { authDirFor } from "@palui/connectors/whatsapp/runtime"
import { turnBus, type TurnBus } from "@palui/core/chat/bus"
import type { EngineDeps } from "@palui/core/chat/deps"
import { createEngine, type Engine } from "@palui/core/chat/engine"
import { config as loadConfig, getControlToken, masterKey, type Config } from "@palui/core/config"
import { createLogger, type Logger } from "@palui/core/log"
import { createOutboxWorker } from "@palui/core/outbox/worker"
import type { AnyRuntime, ConnectorManifest, ConnectorServices } from "@palui/core/connectors/define"
import type { ConnectorRow } from "@palui/core/connectors/store"
import { createSupervisor, type Supervisor } from "@palui/core/connectors/supervisor"
import { connectorFlowCaller } from "@palui/core/flows/call"
import { connectorToolCaller } from "@palui/core/tools/call"
import { getSetting } from "@palui/core/settings"
import { announceDeploy } from "@palui/core/deploy-notice"
import { PALUI_VERSION } from "@palui/core/version"
import { createControlServer } from "./control"

// Deps that need the engine (the job runner calls runTurn) are built by a factory
// that runs after the engine exists. The engine reads deps lazily from the same object.
export type DepsFactory = (ctx: { db: DB; engine: Engine; log: Logger; supervisor: Supervisor }) => EngineDeps

export type RuntimeOptions = {
  // The version to report as running (default: PALUI_VERSION). Tests set it.
  version?: string
  deps?: EngineDeps | DepsFactory
  // Connector runtimes by manifest id (default: @palui/connectors/runtimes).
  registry?: Record<string, AnyRuntime>
  // Manifests, for builtin rows on first boot (default: @palui/connectors/manifests).
  manifests?: ConnectorManifest[]
  config?: Config
  // An open database (tests). Otherwise $DATA_DIR/palui.db, opened without migrating.
  db?: DB
  // Control port (default config.controlPort, 0 picks a free one).
  port?: number
  host?: string
  log?: Logger
  bus?: TurnBus
}

export type Runtime = {
  port: number
  db: DB
  engine: Engine
  supervisor: Supervisor
  stop(): Promise<void>
}

export class SchemaMismatchError extends Error {}

// After a remove (PLAN 4.2 "deletes credentials"): a WhatsApp connector's session
// files go even when it was paused or in error, so working creds never stay on disk.
export function removeConnectorFiles(dataDir: string) {
  return async (row: ConnectorRow) => {
    if (row.type === "whatsapp") await rm(authDirFor(dataDir, row.id), { recursive: true, force: true })
  }
}

export async function startRuntime(opts: RuntimeOptions = {}): Promise<Runtime> {
  const cfg = opts.config ?? loadConfig()
  const log = opts.log ?? createLogger({ service: "runtime" })
  const startedAt = Date.now()
  const ownDb = !opts.db
  const db = opts.db ?? openDb(dbPath(cfg.dataDir), { migrate: false })

  // Only the app migrates (PLAN 3.1). Compose restarts us after it does.
  const schema = schemaState(db.$client)
  if (!schema.ok) {
    if (ownDb) db.$client.close()
    const detail = schema.reason === "behind" ? `missing migrations: ${schema.missing.join(", ")}` : `unknown migrations from a newer build: ${schema.unknown.join(", ")}`
    throw new SchemaMismatchError(`Database schema doesn't match this build (${detail}). The app runs migrations; the runtime will retry when compose restarts it.`)
  }

  const key = masterKey(cfg)
  const deps: EngineDeps = typeof opts.deps === "object" ? opts.deps : {}
  const bus = opts.bus ?? turnBus
  const services = {} as ConnectorServices
  let engineRef: Engine | undefined
  const supervisor = createSupervisor({
    db,
    registry: opts.registry ?? runtimes,
    services,
    log,
    masterKey: key,
    dataDir: cfg.dataDir,
    inUse: (id) => engineRef?.inUse(id) ?? false,
    onRemoved: removeConnectorFiles(cfg.dataDir),
  })
  const engine = createEngine({ db, host: supervisor, log, bus, deps })
  engineRef = engine
  if (typeof opts.deps === "function") Object.assign(deps, opts.deps({ db, engine, log, supervisor }))
  Object.assign(services, engine.services)
  services.reconcile = () => supervisor.poke()
  services.callTool = connectorToolCaller(() => supervisor.running(), { tz: () => getSetting(db, "timezone") })
  services.flowTools = connectorFlowCaller(() => supervisor.running())
  const worker = createOutboxWorker({ db, channel: (ch) => supervisor.channel(ch), connector: (id) => supervisor.get(id)?.handle.channel, log })
  const control = createControlServer({ db, token: getControlToken(cfg), engine, supervisor, deps, bus, log, startedAt })

  // The control API first, so /live answers while connectors come up.
  const port = await control.listen(opts.port ?? cfg.controlPort, opts.host)
  log.info("control API listening", { port, schema: schema.version })
  supervisor.ensureBuiltins(opts.manifests ?? manifestList)
  worker.start()
  await supervisor.start()
  await engine.start()
  await deps.scheduler?.start()
  // After the worker and channels are up, so the notice goes out right away.
  try {
    const notice = announceDeploy(db, opts.version ?? PALUI_VERSION)
    if (notice.sent) log.info("deploy notice queued", { chatId: notice.chatId })
    else if (notice.reason === "chat-gone") log.warn("deploy notice skipped: its chat is gone or off")
  } catch (e) {
    log.error("deploy notice failed", { error: e })
  }

  let stopping: Promise<void> | undefined
  async function stop() {
    stopping ??= (async () => {
      log.info("draining")
      // Stop taking new turns and let the current step finish (up to 50 s).
      await engine.stop()
      await deps.scheduler?.stop().catch((e) => log.error("scheduler stop failed", { error: e }))
      // Release outbox claims so the next process sends them.
      await worker.stop()
      await supervisor.stop()
      await control.close()
      if (ownDb) db.$client.close()
      log.info("stopped")
    })()
    return stopping
  }

  return { port, db, engine, supervisor, stop }
}
