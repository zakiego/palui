// Test helpers shared by the channel connector tests: a ConnectorContext backed by
// an in-memory database, with every status change, event and inbound recorded.
import { memoryDb, type DB } from "@palui/db"
import { silentLogger } from "@palui/core/log"
import { claimOwner, isOwner } from "@palui/core/owner"
import type { ConnectorContext, InboundMessage } from "@palui/core/connectors/define"

export type TestContext<C, S> = ConnectorContext<C, S> & {
  inbound: InboundMessage[]
  statuses: { status: string; detail?: string }[]
  events: { kind: string; message: string }[]
  patches: Record<string, unknown>[]
  abort: AbortController
}

export function testContext<C extends Record<string, unknown>, S extends Record<string, string | undefined>>(
  opts: { id?: string; type: string; config?: C; secrets?: S; db?: DB; dataDir?: string },
): TestContext<C, S> {
  const db = opts.db ?? memoryDb()
  const abort = new AbortController()
  const inbound: InboundMessage[] = []
  const statuses: { status: string; detail?: string }[] = []
  const events: { kind: string; message: string }[] = []
  const patches: Record<string, unknown>[] = []
  const ctx: TestContext<C, S> = {
    id: opts.id ?? opts.type,
    type: opts.type,
    config: (opts.config ?? {}) as C,
    secrets: (opts.secrets ?? {}) as S,
    trust: "own",
    db,
    log: silentLogger,
    dataDir: opts.dataDir ?? "/tmp/palui-test",
    signal: abort.signal,
    services: {
      async inbound(msg) {
        inbound.push(msg)
      },
      async claimOwner(input) {
        return claimOwner(db, input)
      },
      isOwner(input) {
        return isOwner(db, input)
      },
    },
    setStatus(status, detail) {
      statuses.push(detail === undefined ? { status } : { status, detail })
    },
    event(kind, message) {
      events.push({ kind, message })
    },
    patchConfig(patch) {
      patches.push(patch)
      ctx.config = { ...ctx.config, ...patch }
    },
    inbound,
    statuses,
    events,
    patches,
    abort,
  }
  return ctx
}

// Resolves once every pending microtask and immediate has run.
export function flush(): Promise<void> {
  return new Promise((r) => setImmediate(r))
}
