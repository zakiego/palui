// Connector actions behind the dashboard and the CLI (PLAN 4.2, 4.6). Each writes
// the desired state through @palui/core/connectors/store, then pokes the runtime.
// The write is the source of truth, so a down runtime never undoes it: the
// supervisor reconciles on start and every 30 s.
import { listAgents } from "@palui/core/agents"
import type { ControlClient } from "@palui/core/control"
import type { ConnectorManifest } from "@palui/core/connectors/define"
import {
  connectConnector,
  deleteConnector,
  getConnector,
  getSecrets,
  listConnectors,
  maskedSecrets,
  pauseConnector,
  resumeConnector,
  upsertConnector,
  type ConnectorRow,
} from "@palui/core/connectors/store"
import type { DB } from "@palui/db"
import { z } from "zod"
import { manifestFor } from "@/lib/manifests"
import { configShape, describeField } from "@/lib/zod-fields"
import { isRuntimeDown, runtimeErrorMessage } from "./runtime"

export { describeField }

export type ConnectorDeps = { db: DB; rt: ControlClient; masterKey: string }

export class ConnectorError extends Error {
  constructor(
    message: string,
    readonly fields: Record<string, string> = {},
  ) {
    super(message)
  }
}

export type PokeResult = { ok: true; runtimeReachable: boolean }

async function poke(rt: ControlClient): Promise<boolean> {
  try {
    await rt.reconcile()
    return true
  } catch {
    return false
  }
}

function mustExist(db: DB, id: string): ConnectorRow {
  const row = getConnector(db, id)
  if (!row) throw new ConnectorError(`No connector "${id}". Run \`palui connectors list\` to see them.`)
  return row
}

export async function pauseConnectorAction(deps: ConnectorDeps, id: string, by = "dashboard", now = Date.now()): Promise<PokeResult> {
  const row = mustExist(deps.db, id)
  if (row.status === "not_set_up") throw new ConnectorError(`${row.label} isn't set up yet, so there's nothing to pause.`)
  pauseConnector(deps.db, id, by, now)
  return { ok: true, runtimeReachable: await poke(deps.rt) }
}

export async function resumeConnectorAction(deps: ConnectorDeps, id: string, by = "dashboard", now = Date.now()): Promise<PokeResult> {
  const row = mustExist(deps.db, id)
  if (row.status === "not_set_up") throw new ConnectorError(`${row.label} isn't set up yet. Open it on the Connectors page and connect it first.`)
  resumeConnector(deps.db, id, by, now)
  return { ok: true, runtimeReachable: await poke(deps.rt) }
}

// Remove always goes through the runtime first, which unlinks devices (WhatsApp)
// and then deletes the row. When the runtime is down the row and its credentials
// are deleted here instead, and the caller is told the device wasn't unlinked.
export async function removeConnectorAction(deps: ConnectorDeps, id: string, now = Date.now()): Promise<{ ok: true; runtimeReachable: boolean; detail?: string }> {
  mustExist(deps.db, id)
  try {
    const res = await deps.rt.remove(id)
    if (!res.ok) throw new ConnectorError(res.detail ?? "The runtime couldn't remove it.")
    // The runtime normally deletes the row itself; make sure it's gone.
    if (getConnector(deps.db, id)) deleteConnector(deps.db, id, now)
    return { ok: true, runtimeReachable: true, detail: res.detail }
  } catch (err) {
    if (err instanceof ConnectorError) throw err
    if (!isRuntimeDown(err)) throw new ConnectorError(runtimeErrorMessage(err))
    deleteConnector(deps.db, id, now)
    return {
      ok: true,
      runtimeReachable: false,
      detail: "The runtime was down, so nothing was unlinked on the device side. Credentials are deleted.",
    }
  }
}

// --- wizard -----------------------------------------------------------------------

function issueText(err: z.ZodError): string {
  return err.issues[0]?.message ?? "Invalid value"
}

export type DraftInput = {
  id: string
  type: string
  label?: string
  config?: Record<string, unknown>
  // Only keys sent are replaced; an empty string deletes that secret.
  secrets?: Record<string, string>
  trust?: "own" | "external"
  // The agent that owns it (null: global). Left out, it's kept.
  agentId?: string | null
}

// Saves wizard progress as a `not_set_up` draft (PLAN 11.5), or updates the
// config of a connector that's already set up. Fields are checked one by one so
// the form can point at the bad one.
export function saveDraft(deps: ConnectorDeps, input: DraftInput, now = Date.now()): ConnectorRow {
  const m = manifestFor(input.type)
  if (!m) throw new ConnectorError(`Unknown connector type "${input.type}".`)
  const fields: Record<string, string> = {}
  const shape = configShape(m)
  const config: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(input.config ?? {})) {
    const schema = shape[k]
    if (!schema) continue
    // An emptied field clears the stored value (undefined drops out of the JSON).
    if (v === "" || v === undefined || v === null) {
      config[k] = undefined
      continue
    }
    const r = schema.safeParse(v)
    if (r.success) config[k] = r.data
    else fields[k] = issueText(r.error)
  }
  const secrets: Record<string, string> = {}
  for (const [k, v] of Object.entries(input.secrets ?? {})) {
    const schema = m.secrets[k]
    if (!schema) continue
    if (v === "") {
      secrets[k] = ""
      continue
    }
    const r = schema.safeParse(v)
    if (r.success) secrets[k] = String(r.data)
    else fields[k] = issueText(r.error)
  }
  if (Object.keys(fields).length) throw new ConnectorError("Some fields need a fix.", fields)
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(input.id)) throw new ConnectorError("Connector ids use lowercase letters, digits and dashes.")
  const existing = getConnector(deps.db, input.id)
  if (existing && existing.type !== m.id) throw new ConnectorError(`"${input.id}" is already used by another connector.`)
  // Only tool connectors have an owner, and only an agent that exists.
  if (input.agentId && !listAgents(deps.db).some((a) => a.id === input.agentId)) throw new ConnectorError("That agent doesn't exist anymore.")
  const agentId = m.kind === "tools" ? input.agentId : undefined
  return upsertConnector(deps.db, { id: input.id, manifest: m, label: input.label, config, secrets, trust: input.trust, agentId }, deps.masterKey, now)
}

// What's still missing before Connect: required config fields and secrets.
export function missingForConnect(row: ConnectorRow, m: ConnectorManifest, secretValues: Record<string, string>): Record<string, string> {
  const missing: Record<string, string> = {}
  const shape = configShape(m)
  const parsed = m.config.safeParse(row.config ?? {})
  if (!parsed.success) {
    for (const issue of parsed.error.issues) {
      const key = String(issue.path[0] ?? "")
      if (key && shape[key]) missing[key] = issue.message
    }
  }
  const optionalSecret = new Set(m.setup.filter((s) => "field" in s && s.optional).map((s) => ("field" in s ? s.field : "")))
  for (const k of Object.keys(m.secrets)) {
    if (!secretValues[k] && !optionalSecret.has(k)) missing[k] = "Required"
  }
  return missing
}

export async function connectConnectorAction(deps: ConnectorDeps, id: string, now = Date.now()): Promise<PokeResult> {
  const row = mustExist(deps.db, id)
  const m = manifestFor(row.type)
  if (!m) throw new ConnectorError(`Unknown connector type "${row.type}".`)
  const missing = missingForConnect(row, m, getSecrets(row, deps.masterKey))
  if (Object.keys(missing).length) throw new ConnectorError("Fill in the required fields first.", missing)
  connectConnector(deps.db, id, now)
  return { ok: true, runtimeReachable: await poke(deps.rt) }
}

// The list the Connectors page renders. Secrets are masked (PLAN 4.5).
export function listConnectorItems(deps: Pick<ConnectorDeps, "db" | "masterKey">) {
  return listConnectors(deps.db).map((row) => {
    let secrets: Record<string, string> = {}
    let secretsError: string | null = null
    try {
      secrets = maskedSecrets(row, deps.masterKey)
    } catch {
      secretsError = "These credentials can't be decrypted with the current MASTER_KEY."
    }
    return {
      id: row.id,
      type: row.type,
      kind: row.kind,
      label: row.label,
      enabled: row.enabled,
      status: row.status,
      statusDetail: row.statusDetail,
      trust: row.trust,
      agentId: row.agentId,
      config: row.config,
      secrets,
      secretsError,
      updatedAt: row.updatedAt,
    }
  })
}

export type ConnectorItem = ReturnType<typeof listConnectorItems>[number]
