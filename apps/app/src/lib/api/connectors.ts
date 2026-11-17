// Connectors page: list, pause/resume, the connect wizard, test, actions, remove.
import { noInput, withInput } from "./client"
import type { AgentChoice, ConnectorKind, ConnectorStatus, EventLevel, JsonObject, Ok, Trust } from "./types"

export type ConnectorItem = {
  id: string
  type: string
  kind: ConnectorKind
  label: string
  enabled: boolean
  status: ConnectorStatus
  statusDetail: string | null
  trust: Trust
  agentId: string | null
  // Masked: the stored values are never sent back.
  secrets: { [field: string]: string }
  secretsError: string | null
  updatedAt: number
  config: JsonObject
  // The agent that owns a scoped connector ("" once that agent is gone); null when global.
  ownerName: string | null
}

export type ConnectorLive = { running: boolean; status: string; detail: string | null }

export type ConnectorsPage = {
  items: ConnectorItem[]
  live: { [id: string]: ConnectorLive }
  runtimeUp: boolean
  appUrl: string
  agents: (AgentChoice & { slug: string })[]
}

export type ConnectorDetail = {
  events: { id: number; at: number; level: EventLevel; kind: string; message: string }[]
  live: JsonObject
  runtimeUp: boolean
  owners: { telegram: boolean; whatsapp: string | null }
  status: { status: ConnectorStatus; statusDetail: string | null } | null
  timezone: string
}

export type PokeResult = { ok: true; runtimeReachable: boolean }

export type ConnectorDraft = {
  id: string
  type: string
  label?: string
  config?: { [field: string]: unknown }
  secrets?: { [field: string]: string }
  trust?: Trust
  agentId?: string | null
}

export type FieldResult = { ok: true } | { ok: false; message: string; fields: { [field: string]: string } }

export const getConnectorsPage = noInput<ConnectorsPage>("getConnectorsPage", "GET")
export const getConnectorDetail = withInput<{ id: string }, ConnectorDetail>("getConnectorDetail", "GET")
export const pauseConnectorFn = withInput<{ id: string }, PokeResult>("pauseConnectorFn", "POST")
export const resumeConnectorFn = withInput<{ id: string }, PokeResult>("resumeConnectorFn", "POST")
export const removeConnectorFn = withInput<{ id: string }, { ok: true; runtimeReachable: boolean; detail?: string }>("removeConnectorFn", "POST")
export const saveConnectorDraft = withInput<ConnectorDraft, FieldResult>("saveConnectorDraft", "POST")
// Save whatever the form holds, then ask the supervisor to bring it up.
export const connectConnectorFn = withInput<ConnectorDraft, FieldResult & { runtimeReachable?: boolean }>("connectConnectorFn", "POST")
export const testConnectorFn = withInput<{ id: string }, { ok: true; detail: string; data: JsonObject }>("testConnectorFn", "POST")
export const connectorActionFn = withInput<{ id: string; name: string; input?: { [key: string]: unknown } }, JsonObject>("connectorActionFn", "POST")
export const setWhatsAppOwnerFn = withInput<{ phone: string }, { jid: string }>("setWhatsAppOwnerFn", "POST")
export const setConnectorOwnerFn = withInput<{ id: string; agentId: string | null }, Ok>("setConnectorOwnerFn", "POST")
// Mark an MCP server as holding no private data (PLAN 19.4), or clear the mark.
export const setConnectorDataFn = withInput<{ id: string; noPrivateData: boolean }, Ok>("setConnectorDataFn", "POST")
