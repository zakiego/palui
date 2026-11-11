// Desired connector state (PLAN 4.2, 4.4): the `connectors` rows. The app writes
// here (connect, pause, resume, remove, configure) and pokes the runtime; the
// supervisor reconciles running handles against these rows.
import { createHash } from "node:crypto"
import { asc, eq } from "drizzle-orm"
import { connectors, type ConnectorKind, type ConnectorStatus, type DB, type Trust } from "@palui/db"
import { decryptJson, encryptJson, maskSecret } from "@palui/db/crypto"
import { logEvent } from "../events"
import { redact } from "../log"
import type { ConnectorManifest } from "./define"

export type ConnectorRow = typeof connectors.$inferSelect

export function configHash(config: Record<string, unknown>, secretsBlob: string | null): string {
  return createHash("sha256")
    .update(JSON.stringify(config ?? {}))
    .update("\0")
    .update(secretsBlob ?? "")
    .digest("hex")
    .slice(0, 16)
}

export function listConnectors(db: DB): ConnectorRow[] {
  return db.select().from(connectors).orderBy(asc(connectors.sortOrder), asc(connectors.createdAt), asc(connectors.id)).all()
}

export function getConnector(db: DB, id: string): ConnectorRow | undefined {
  return db.select().from(connectors).where(eq(connectors.id, id)).get()
}

export function getSecrets(row: Pick<ConnectorRow, "secrets">, masterKey: string): Record<string, string> {
  return decryptJson(row.secrets, masterKey)
}

// Secrets are write-only (PLAN 4.5): the UI only ever gets masked values.
export function maskedSecrets(row: Pick<ConnectorRow, "secrets">, masterKey: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(getSecrets(row, masterKey))) if (v) out[k] = maskSecret(v)
  return out
}

export type UpsertInput = {
  id: string
  manifest: Pick<ConnectorManifest, "id" | "kind" | "name" | "trust">
  label?: string
  config?: Record<string, unknown>
  // Only keys present are replaced; an empty string deletes that secret.
  secrets?: Record<string, string>
  trust?: Trust
  enabled?: boolean
  // The agent that owns it (null: global). Left out, it's kept.
  agentId?: string | null
}

// Create or update a connector row. New rows start as `not_set_up` drafts so the
// wizard keeps its progress when the sheet closes (PLAN 11.5).
export function upsertConnector(db: DB, input: UpsertInput, masterKey: string, now = Date.now()): ConnectorRow {
  const existing = getConnector(db, input.id)
  let secretsBlob = existing?.secrets ?? null
  if (input.secrets && Object.keys(input.secrets).length) {
    const merged = { ...(existing ? getSecrets(existing, masterKey) : {}) }
    for (const [k, v] of Object.entries(input.secrets)) {
      if (v === "") delete merged[k]
      else merged[k] = v
    }
    secretsBlob = Object.keys(merged).length ? encryptJson(merged, masterKey) : null
  }
  const config = { ...(existing?.config ?? {}), ...(input.config ?? {}) }
  const hash = configHash(config, secretsBlob)
  if (!existing) {
    return db
      .insert(connectors)
      .values({
        id: input.id,
        type: input.manifest.id,
        kind: input.manifest.kind as ConnectorKind,
        label: input.label ?? input.manifest.name,
        enabled: input.enabled ?? false,
        status: "not_set_up",
        trust: input.trust ?? input.manifest.trust ?? "own",
        config,
        secrets: secretsBlob,
        configHash: hash,
        agentId: input.agentId ?? null,
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
  }
  return db
    .update(connectors)
    .set({
      label: input.label ?? existing.label,
      config,
      secrets: secretsBlob,
      configHash: hash,
      trust: input.trust ?? existing.trust,
      enabled: input.enabled ?? existing.enabled,
      ...(input.agentId !== undefined ? { agentId: input.agentId } : {}),
      updatedAt: now,
    })
    .where(eq(connectors.id, input.id))
    .returning()
    .get()
}

// Make a connector global (null) or owned by one agent. The runtime reads the
// row on every turn, so this applies from the next one.
export function setConnectorOwner(db: DB, id: string, agentId: string | null, now = Date.now()): ConnectorRow | undefined {
  return db.update(connectors).set({ agentId, updatedAt: now }).where(eq(connectors.id, id)).returning().get()
}

// Whether an agent's turn gets this connector's tools: a global one serves
// everyone, an owned one only its owner. A turn with no agent gets globals only.
export function connectorServesAgent(row: Pick<ConnectorRow, "agentId">, agentId: string | undefined | null): boolean {
  return !row.agentId || row.agentId === agentId
}

// Connect: mark desired = on. The supervisor moves it through connecting -> on|error.
export function connectConnector(db: DB, id: string, now = Date.now()) {
  db.update(connectors).set({ enabled: true, status: "connecting", statusDetail: null, updatedAt: now }).where(eq(connectors.id, id)).run()
  logEvent(db, { connectorId: id, kind: "connect", message: "Connect requested" }, now)
}

// Pause keeps credentials (PLAN 4.2). Instant, no confirmation.
export function pauseConnector(db: DB, id: string, by = "dashboard", now = Date.now()) {
  db.update(connectors).set({ enabled: false, status: "paused", statusDetail: null, errorSince: null, updatedAt: now }).where(eq(connectors.id, id)).run()
  logEvent(db, { connectorId: id, kind: "pause", message: `Paused from ${by}` }, now)
}

export function resumeConnector(db: DB, id: string, by = "dashboard", now = Date.now()) {
  db.update(connectors).set({ enabled: true, status: "connecting", statusDetail: null, updatedAt: now }).where(eq(connectors.id, id)).run()
  logEvent(db, { connectorId: id, kind: "resume", message: `Resumed from ${by}` }, now)
}

// Remove deletes credentials and the row (PLAN 4.2). Device unlinking is done by
// the runtime before it calls this (the supervisor's remove path).
export function deleteConnector(db: DB, id: string, now = Date.now()) {
  db.delete(connectors).where(eq(connectors.id, id)).run()
  logEvent(db, { connectorId: id, kind: "remove", message: "Removed, credentials deleted", level: "warn" }, now)
}

// Connector types that no longer exist (routines went to flows). Their rows are
// removed when the app opens the database, so no page lists a connector it can't
// render and the supervisor never looks for a runtime that isn't there.
export const RETIRED_CONNECTOR_TYPES: readonly string[] = ["routines"]

export function removeRetiredConnectors(db: DB, now = Date.now()): number {
  const rows = listConnectors(db).filter((c) => RETIRED_CONNECTOR_TYPES.includes(c.type))
  for (const r of rows) deleteConnector(db, r.id, now)
  return rows.length
}

export function setConnectorStatus(db: DB, id: string, status: ConnectorStatus, rawDetail: string | null = null, now = Date.now()) {
  // Connector errors can echo a token back (a bot URL, an auth header): redact before storing.
  const detail = rawDetail === null ? null : redact(rawDetail)
  const row = getConnector(db, id)
  if (!row) return
  if (row.status === status && row.statusDetail === detail) return
  const errorSince = status === "error" ? (row.errorSince ?? now) : null
  db.update(connectors)
    .set({ status, statusDetail: detail, errorSince, alertedAt: status === "error" ? row.alertedAt : null, updatedAt: now })
    .where(eq(connectors.id, id))
    .run()
  logEvent(
    db,
    {
      connectorId: id,
      kind: "status",
      level: status === "error" ? "error" : "info",
      message: detail ? `${status}: ${detail}` : status,
      data: { from: row.status, to: status },
    },
    now,
  )
}

// Runtime-learned secrets (a token swapped for a one-time code). Merged like
// upsertConnector, but the hash stays put so the running handle isn't restarted.
export function patchConnectorSecrets(db: DB, id: string, patch: Record<string, string>, masterKey: string, now = Date.now()) {
  const row = getConnector(db, id)
  if (!row) return
  const merged = getSecrets(row, masterKey)
  for (const [k, v] of Object.entries(patch)) {
    if (v === "") delete merged[k]
    else merged[k] = v
  }
  db.update(connectors)
    .set({ secrets: Object.keys(merged).length ? encryptJson(merged, masterKey) : null, updatedAt: now })
    .where(eq(connectors.id, id))
    .run()
}

export function patchConnectorConfig(db: DB, id: string, patch: Record<string, unknown>, now = Date.now()) {
  const row = getConnector(db, id)
  if (!row) return
  // Runtime-learned config doesn't change the hash, so it doesn't trigger a restart.
  db.update(connectors)
    .set({ config: { ...row.config, ...patch }, updatedAt: now })
    .where(eq(connectors.id, id))
    .run()
}
