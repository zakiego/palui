// What the connector page edits: a saved connector, or a draft that doesn't
// exist yet. A draft lives at /connectors/<id>?type=<type>&preset=<id> until
// its first save, so the URL stays the same once it's real.
import type { ConnectorManifest } from "@/shared/connectors/define"

import type { ConnectorItem } from "@/components/connectors/variants/types"
import type { JsonObject } from "@/lib/json"
import type { Preset } from "@/lib/presets"
import { slugify } from "@/lib/slug"

export type ConnectorTarget = {
  id: string
  type: string
  label: string
  isNew: boolean
  status: string
  statusDetail: string | null
  config: JsonObject
  secrets: Record<string, string>
  secretsError: string | null
  trust: "own" | "external"
  // The agent that owns it; null means global.
  agentId: string | null
}

export function toTarget(item: ConnectorItem): ConnectorTarget {
  return {
    id: item.id,
    type: item.type,
    label: item.label,
    isNew: false,
    status: item.status,
    statusDetail: item.statusDetail,
    config: item.config,
    secrets: item.secrets,
    secretsError: item.secretsError,
    trust: item.trust,
    agentId: item.agentId,
  }
}

// The id a new connector gets: the manifest id for a single one, else
// `<type>-<preset or "server">`, numbered past any id already taken.
export function newConnectorId(m: ConnectorManifest, existing: { id: string }[], preset?: Preset): string {
  if (!m.multiple) return m.id
  const base = `${m.id}-${slugify(preset?.id ?? "server")}`
  let id = base
  for (let n = 2; existing.some((e) => e.id === id); n++) id = `${base}-${n}`
  return id
}

// Whether `id` could be a new connector of this manifest (guards a typed URL).
export function isDraftId(m: ConnectorManifest, id: string): boolean {
  return m.multiple ? id.startsWith(`${m.id}-`) && slugify(id) === id : id === m.id
}

export function draftTarget(m: ConnectorManifest, id: string, preset?: Preset, agentId?: string): ConnectorTarget {
  return {
    id,
    type: m.id,
    label: m.multiple ? (preset?.label ?? "New server") : m.name,
    isNew: true,
    status: "not_set_up",
    statusDetail: null,
    config: (preset?.config ?? {}) as JsonObject,
    secrets: {},
    secretsError: null,
    trust: preset?.trust ?? m.trust ?? "own",
    // Added from an agent's page: that agent owns it.
    agentId: agentId && m.kind === "tools" ? agentId : null,
  }
}
