// GET /health, no auth (PLAN 3.1, 13). The compose healthcheck and deploy.sh read
// its status code, so only "this app must not serve" turns it non-200: a
// database migrated by a newer build (a bad rollback). Runtime and connector
// state go in the body and never change the code, so a WhatsApp outage can't
// fail a deploy.
import { listConnectors } from "@palui/core/connectors/store"
import { schemaState, type DB } from "@palui/db"

export type RuntimeLive = () => Promise<{ ok: boolean; schema: string }>

export async function health(db: DB, live: RuntimeLive): Promise<Response> {
  const state = schemaState(db.$client)
  if (!state.ok && state.reason === "newer") {
    return Response.json(
      {
        ok: false,
        error: "db_newer_than_app",
        message: `The database has migrations this build doesn't know (${state.unknown.join(", ")}). A newer version migrated it. Deploy that version again, or restore a backup taken before it.`,
        unknownMigrations: state.unknown,
      },
      { status: 503, headers: { "cache-control": "no-store" } },
    )
  }
  let runtime: { ok: boolean; schema: string } | "unreachable"
  try {
    runtime = await live()
  } catch {
    runtime = "unreachable"
  }
  let connectors: { id: string; status: string }[] = []
  try {
    connectors = listConnectors(db).map((c) => ({ id: c.id, status: c.status }))
  } catch {
    // A schema that's behind shows up in `schema` below; don't fail on it here.
  }
  return Response.json(
    { ok: true, schema: state.ok ? state.version : `behind (${state.reason === "behind" ? state.missing.join(", ") : ""})`, runtime, connectors },
    { headers: { "cache-control": "no-store" } },
  )
}
