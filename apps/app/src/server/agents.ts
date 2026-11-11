// Read-side shapes for the Agents pages. Server only: the server fns in
// src/lib/fns/agents.ts call these inside their handlers, so none of it reaches
// the browser bundle.
import { agentContainer, type AgentRow } from "@palui/core/agents"
import { agentTokensToday } from "@palui/core/budget/index"
import { listConnectors } from "@palui/core/connectors/store"
import { listInstructions } from "@palui/core/notes/service"
import type { DB } from "@palui/db"

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
