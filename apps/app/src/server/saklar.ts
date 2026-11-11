// Saklar actions behind the dashboard and the CLI (PLAN 4.2, 4.6). Each writes
// the desired state through @palui/core/saklar/store, then pokes the runtime.
// The write is the source of truth, so a down runtime never undoes it: the
// supervisor reconciles on start and every 30 s.
import type { ControlClient } from "@palui/core/control"
import type { ConnectorManifest } from "@palui/core/saklar/define"
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
} from "@palui/core/saklar/store"
import type { DB } from "@palui/db"
import { z } from "zod"
import { manifestFor } from "@/lib/manifests"
import { configShape, describeField } from "@/lib/zod-fields"
import { isRuntimeDown, runtimeErrorMessage } from "./runtime"

export { describeField }

export type SaklarDeps = { db: DB; rt: ControlClient; masterKey: string }

export class SaklarError extends Error {
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
  if (!row) throw new SaklarError(`No connector "${id}". Run \`palui saklar list\` to see them.`)
  return row
}

export async function pauseSaklar(deps: SaklarDeps, id: string, by = "dashboard", now = Date.now()): Promise<PokeResult> {
  const row = mustExist(deps.db, id)
  if (row.status === "not_set_up") throw new SaklarError(`${row.label} isn't set up yet, so there's nothing to pause.`)
  pauseConnector(deps.db, id, by, now)
  return { ok: true, runtimeReachable: await poke(deps.rt) }
}

export async function resumeSaklar(deps: SaklarDeps, id: string, by = "dashboard", now = Date.now()): Promise<PokeResult> {
  const row = mustExist(deps.db, id)
  if (row.status === "not_set_up") throw new SaklarError(`${row.label} isn't set up yet. Open it in Saklar and connect it first.`)
  resumeConnector(deps.db, id, by, now)
  return { ok: true, runtimeReachable: await poke(deps.rt) }
}

// Remove always goes through the runtime first, which unlinks devices (WhatsApp)
// and then deletes the row. When the runtime is down the row and its credentials
// are deleted here instead, and the caller is told the device wasn't unlinked.
export async function removeSaklar(deps: SaklarDeps, id: string, now = Date.now()): Promise<{ ok: true; runtimeReachable: boolean; detail?: string }> {
  mustExist(deps.db, id)
  try {
    const res = await deps.rt.remove(id)
    if (!res.ok) throw new SaklarError(res.detail ?? "The runtime couldn't remove it.")
    // The runtime normally deletes the row itself; make sure it's gone.
    if (getConnector(deps.db, id)) deleteConnector(deps.db, id, now)
    return { ok: true, runtimeReachable: true, detail: res.detail }
  } catch (err) {
    if (err instanceof SaklarError) throw err
    if (!isRuntimeDown(err)) throw new SaklarError(runtimeErrorMessage(err))
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
}

// Saves wizard progress as a `not_set_up` draft (PLAN 11.5), or updates the
// config of a connector that's already set up. Fields are checked one by one so
// the form can point at the bad one.
export function saveDraft(deps: SaklarDeps, input: DraftInput, now = Date.now()): ConnectorRow {
  const m = manifestFor(input.type)
  if (!m) throw new SaklarError(`Unknown connector type "${input.type}".`)
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
  if (Object.keys(fields).length) throw new SaklarError("Some fields need a fix.", fields)
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(input.id)) throw new SaklarError("Connector ids use lowercase letters, digits and dashes.")
  const existing = getConnector(deps.db, input.id)
  if (existing && existing.type !== m.id) throw new SaklarError(`"${input.id}" is already used by another connector.`)
  return upsertConnector(deps.db, { id: input.id, manifest: m, label: input.label, config, secrets, trust: input.trust }, deps.masterKey, now)
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

export async function connectSaklar(deps: SaklarDeps, id: string, now = Date.now()): Promise<PokeResult> {
  const row = mustExist(deps.db, id)
  const m = manifestFor(row.type)
  if (!m) throw new SaklarError(`Unknown connector type "${row.type}".`)
  const missing = missingForConnect(row, m, getSecrets(row, deps.masterKey))
  if (Object.keys(missing).length) throw new SaklarError("Fill in the required fields first.", missing)
  connectConnector(deps.db, id, now)
  return { ok: true, runtimeReachable: await poke(deps.rt) }
}

// The list the Saklar page renders. Secrets are masked (PLAN 4.5).
export function listSaklar(deps: Pick<SaklarDeps, "db" | "masterKey">) {
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
      config: row.config,
      secrets,
      secretsError,
      updatedAt: row.updatedAt,
    }
  })
}

export type SaklarItem = ReturnType<typeof listSaklar>[number]
