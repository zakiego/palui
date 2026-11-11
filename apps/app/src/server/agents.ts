// Read-side shapes for the Agents pages. Server only: the server fns in
// src/lib/fns/agents.ts call these inside their handlers, so none of it reaches
// the browser bundle.
import { agentContainer, type AgentRow } from "@palui/core/agents"
import { agentTokensToday } from "@palui/core/budget/index"
import { listConnectors } from "@palui/core/connectors/store"
import { listInstructions } from "@palui/core/notes/service"
import { runs, toolCalls, type DB } from "@palui/db"
import { and, count, eq, gte } from "drizzle-orm"
import type { CatalogTool } from "@palui/core/tools/catalog"
import { runtime, runtimeErrorMessage } from "@/server/runtime"

export function agentView(db: DB, a: AgentRow, chatCount: number) {
  return {
    id: a.id,
    slug: a.slug,
    name: a.name,
    persona: a.persona,
    isDefault: a.isDefault,
    ownerPowers: a.ownerPowers,
    guestTools: a.guestTools,
    dailyTokenCap: a.dailyTokenCap,
    recallContainer: a.recallContainer,
    container: agentContainer(a) ?? null,
    telegramConnectorId: a.telegramConnectorId,
    avatarImage: a.avatarImage,
    avatarIcon: a.avatarIcon,
    avatarColor: a.avatarColor,
    chats: chatCount,
    instructions: listInstructions(db, { agentId: a.id }).length,
    tokensToday: agentTokensToday(db, a.id),
  }
}

export function telegramBots(db: DB) {
  return listConnectors(db)
    .filter((c) => c.type === "tgbot")
    .map((c) => ({ id: c.id, label: c.label, username: typeof c.config.botUsername === "string" ? c.config.botUsername : null, status: c.status }))
}

const DAY = 24 * 60 * 60 * 1000

// The Tools tab: what the runtime would offer this agent now, and how often it
// called each tool in the last 7 days. `tools` is null when the runtime can't say.
export async function agentTools(db: DB, a: AgentRow, now = Date.now()) {
  let tools: CatalogTool[] | null = null
  let error: string | null = null
  try {
    tools = (await runtime().agentTools(a.id)).tools
  } catch (err) {
    error = runtimeErrorMessage(err)
  }
  const used = db
    .select({ tool: toolCalls.tool, n: count() })
    .from(toolCalls)
    .innerJoin(runs, eq(runs.id, toolCalls.runId))
    .where(and(eq(runs.agentId, a.id), eq(toolCalls.status, "ok"), gte(toolCalls.createdAt, now - 7 * DAY)))
    .groupBy(toolCalls.tool)
    .all()
  // Tool connectors (MCP servers and the like) and their owner agent; null means global.
  const scopes = Object.fromEntries(listConnectors(db).filter((c) => c.kind === "tools").map((c) => [c.id, c.agentId])) as Record<string, string | null>
  return { tools, error, used: Object.fromEntries(used.map((u) => [u.tool, u.n])) as Record<string, number>, scopes }
}

type ContainerStats = { container: string; memories: number; documents: number; lastActivity: number | null }

// The Memory tab: where the agent's memory lives and how much is in it. The
// memory connector (recall) is optional; with none, the agent remembers nothing
// past its session.
export async function agentMemory(db: DB, a: AgentRow) {
  const conn = listConnectors(db).find((c) => c.kind === "memory")
  const configured = typeof conn?.config.container === "string" && conn.config.container ? conn.config.container : "default"
  const container = agentContainer(a) ?? configured
  if (!conn) return { connector: null, container, stats: null, error: null }
  let stats: ContainerStats | null = null
  let error: string | null = null
  if (conn.enabled) {
    try {
      const r = await runtime().action(conn.id, "listContainers")
      const all = Array.isArray(r.containers) ? (r.containers as ContainerStats[]) : []
      stats = all.find((c) => c.container === container) ?? { container, memories: 0, documents: 0, lastActivity: null }
    } catch (err) {
      error = runtimeErrorMessage(err)
    }
  }
  return { connector: { id: conn.id, label: conn.label, status: conn.status, enabled: conn.enabled }, container, stats, error }
}
