// Connectors page: list, pause/resume, the connect wizard, test, actions, remove.
import { config, masterKey } from "@palui/core/config"
import { getOwner, setWhatsAppOwner } from "@palui/core/owner"
import { getConnector, setConnectorOwner } from "@palui/core/connectors/store"
import { listAgents } from "@palui/core/agents"
import { listEvents } from "@palui/core/events"
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { googleAuthUrl, redirectUriFor } from "@palui/connectors/gcal/oauth"
import { getDb } from "@/server/db"
import { z } from "zod"
import { asJson, type JsonObject } from "@/lib/json"
import { callRuntime, runtime } from "@/server/runtime"
import { connectConnectorAction, listConnectorItems, pauseConnectorAction, removeConnectorAction, resumeConnectorAction, ConnectorError, saveDraft } from "@/server/connectors"
import { requireUser, requireWriter } from "@/server/session"

const id = z.string().min(1).max(64)

function deps(db: Parameters<typeof listConnectorItems>[0]["db"]) {
  return { db, rt: runtime(), masterKey: masterKey() }
}

export const getConnectorsPage = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const names = new Map(listAgents(db).map((a) => [a.id, a.name]))
  // ownerName: the agent that owns a scoped connector ("" once that agent is gone); null when global.
  const items = listConnectorItems(deps(db)).map((c) => ({ ...c, config: asJson<JsonObject>(c.config), ownerName: c.agentId ? (names.get(c.agentId) ?? "") : null }))
  let live: Record<string, { running: boolean; status: string; detail: string | null }> = {}
  let runtimeUp = true
  try {
    const s = await runtime().status()
    live = Object.fromEntries(s.connectors.map((c) => [c.id, { running: c.running, status: c.status, detail: c.detail }]))
  } catch {
    runtimeUp = false
  }
  // For the "Which agents" choice on a tool connector.
  const agents = listAgents(db).map((a) => ({ id: a.id, name: a.name, isDefault: a.isDefault, avatarImage: a.avatarImage, avatarIcon: a.avatarIcon, avatarColor: a.avatarColor }))
  return { items, live, runtimeUp, appUrl: config().appUrl, agents }
})

export const getConnectorDetail = createServerFn({ method: "GET" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    const evts = listEvents(db, { connectorId: data.id, limit: 15 }).map((e) => ({ id: e.id, at: e.at, level: e.level, kind: e.kind, message: e.message }))
    let live: JsonObject = {}
    let runtimeUp = true
    try {
      const s = await runtime().status()
      live = asJson<JsonObject>(s.connectors.find((c) => c.id === data.id)?.live ?? {})
    } catch {
      runtimeUp = false
    }
    const owners = {
      telegram: !!getOwner(db, "telegram"),
      whatsapp: getOwner(db, "whatsapp")?.externalId.replace(/@.*/, "") ?? null,
    }
    // The page polls this, so it carries the status too: the loader's copy goes
    // stale when a live event is missed and "Connecting" would stick.
    const row = getConnector(db, data.id)
    const status = row ? { status: row.status, statusDetail: row.statusDetail ?? null } : null
    return { events: evts, live, runtimeUp, owners, status, timezone: getSetting(db, "timezone") }
  })

export const pauseConnectorFn = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    return pauseConnectorAction(deps(db), data.id)
  })

export const resumeConnectorFn = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    return resumeConnectorAction(deps(db), data.id)
  })

export const removeConnectorFn = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    return removeConnectorAction(deps(db), data.id)
  })

type FieldResult = { ok: true } | { ok: false; message: string; fields: Record<string, string> }

function asFieldResult(err: unknown): FieldResult {
  if (err instanceof ConnectorError) return { ok: false, message: err.message, fields: err.fields }
  throw err
}

const draft = z.object({
  id,
  type: z.string().min(1).max(64),
  label: z.string().trim().min(1).max(80).optional(),
  config: z.record(z.string(), z.unknown()).optional(),
  secrets: z.record(z.string(), z.string().max(4000)).optional(),
  trust: z.enum(["own", "external"]).optional(),
  agentId: z.string().min(1).max(100).nullable().optional(),
})

export const saveConnectorDraft = createServerFn({ method: "POST" })
  .validator(draft)
  .handler(async ({ data }): Promise<FieldResult> => {
    const { db } = await requireWriter()
    try {
      saveDraft(deps(db), data)
      return { ok: true }
    } catch (err) {
      return asFieldResult(err)
    }
  })

// Save whatever the form holds, then ask the supervisor to bring it up.
export const connectConnectorFn = createServerFn({ method: "POST" })
  .validator(draft)
  .handler(async ({ data }): Promise<FieldResult & { runtimeReachable?: boolean }> => {
    const { db } = await requireWriter()
    try {
      saveDraft(deps(db), data)
      const res = await connectConnectorAction(deps(db), data.id)
      return { ok: true, runtimeReachable: res.runtimeReachable }
    } catch (err) {
      return asFieldResult(err)
    }
  })

export const testConnectorFn = createServerFn({ method: "POST" })
  .validator(z.object({ id }))
  .handler(async ({ data }) => {
    await requireWriter()
    const res = await callRuntime((rt) => rt.test(data.id))
    if (!res.ok) throw new Error(res.detail)
    return { ok: res.ok, detail: res.detail, data: asJson<JsonObject>(res.data ?? {}) }
  })

export const connectorActionFn = createServerFn({ method: "POST" })
  .validator(z.object({ id, name: z.string().min(1).max(64), input: z.record(z.string(), z.unknown()).optional() }))
  .handler(async ({ data }) => {
    await requireWriter()
    // The Google OAuth step runs before the connector is started, so the URL is
    // built here from the saved client id instead of through the runtime.
    if (data.name === "authUrl") {
      const url = googleAuthUrl(getDb(), data.id, masterKey(), config().appUrl)
      return asJson<JsonObject>({ url, redirectUri: redirectUriFor(config().appUrl) })
    }
    return asJson<JsonObject>(await callRuntime((rt) => rt.action(data.id, data.name, data.input ?? {})))
  })

export const setWhatsAppOwnerFn = createServerFn({ method: "POST" })
  .validator(z.object({ phone: z.string().trim().regex(/^\+?[\d\s-]{8,20}$/, "Use digits, like 0812 3456 7890 or +62 812 3456 7890.") }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const row = setWhatsAppOwner(db, data.phone)
    return { jid: row.externalId }
  })

// Make a tool connector global (null) or owned by one agent, from the agent
// page. The runtime reads the row each turn, so no poke is needed.
export const setConnectorOwnerFn = createServerFn({ method: "POST" })
  .validator(z.object({ id, agentId: z.string().min(1).max(100).nullable() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const row = getConnector(db, data.id)
    if (!row) throw new Error("That connector doesn't exist anymore.")
    if (row.kind !== "tools") throw new Error("Only tool connectors can belong to an agent.")
    if (data.agentId && !listAgents(db).some((a) => a.id === data.agentId)) throw new Error("That agent doesn't exist anymore.")
    setConnectorOwner(db, data.id, data.agentId)
    return { ok: true }
  })
