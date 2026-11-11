// Agents page (PLAN 19.7): each agent's persona, shared-chat rules, daily cap,
// recall container and Telegram bot. The runtime reads them at the start of
// every turn, so writes go straight to the table.
import { AgentError, AGENT_COLORS, AGENT_ICONS, MAX_AVATAR_IMAGE, agentContainer, assignAgent, createAgent, deleteAgent, listAgents, MAX_AGENT_NAME, MAX_PERSONA, updateAgent } from "@palui/core/agents"
import { agentTokensToday } from "@palui/core/budget/index"
import { rotateSession } from "@palui/core/chats"
import { listConnectors } from "@palui/core/connectors/store"
import { listInstructions } from "@palui/core/notes/service"
import { chats } from "@palui/db"
import { createServerFn } from "@tanstack/react-start"
import { eq } from "drizzle-orm"
import { z } from "zod"
import { callRuntime, isRuntimeDown, runtime } from "@/server/runtime"
import { requireUser, requireWriter } from "@/server/session"

function rethrow(err: unknown): never {
  if (err instanceof AgentError) throw new Error(err.message)
  throw err
}

export const getAgentsPage = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const all = db.select({ agentId: chats.agentId }).from(chats).all()
  const agents = listAgents(db).map((a) => ({
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
    chats: all.filter((c) => c.agentId === a.id).length,
    instructions: listInstructions(db, { agentId: a.id }).length,
    tokensToday: agentTokensToday(db, a.id),
  }))
  const bots = listConnectors(db)
    .filter((c) => c.type === "tgbot")
    .map((c) => ({ id: c.id, label: c.label, username: typeof c.config.botUsername === "string" ? c.config.botUsername : null, status: c.status }))
  return { agents, bots }
})

const fields = z.object({
  name: z.string().trim().min(1).max(MAX_AGENT_NAME),
  persona: z.string().trim().min(1).max(MAX_PERSONA),
  ownerPowers: z.enum(["off", "approve"]),
  guestTools: z.enum(["none", "safe"]),
  dailyTokenCap: z.number().int().min(0).max(100_000_000),
  recallContainer: z.string().trim().max(64).nullable(),
  telegramConnectorId: z.string().min(1).max(64).nullable(),
  avatarImage: z.string().max(MAX_AVATAR_IMAGE).nullable(),
  avatarIcon: z.enum(AGENT_ICONS).nullable(),
  avatarColor: z.enum(AGENT_COLORS).nullable(),
})

export const saveAgentFn = createServerFn({ method: "POST" })
  .validator(fields.extend({ id: z.string().min(1).max(100).optional(), slug: z.string().trim().max(24).optional() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      const { id, slug, ...rest } = data
      const row = id ? updateAgent(db, id, rest) : createAgent(db, { slug, ...rest })
      return { id: row.id, slug: row.slug }
    } catch (err) {
      rethrow(err)
    }
  })

export const deleteAgentFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      if (!deleteAgent(db, data.id)) throw new Error("That agent doesn't exist anymore.")
      return { ok: true }
    } catch (err) {
      rethrow(err)
    }
  })

// Switch a chat's agent. The runtime saves the session to the old agent's
// memory first; when it's down, the switch still happens without that save.
export const setChatAgentFn = createServerFn({ method: "POST" })
  .validator(z.object({ chatId: z.string().min(1).max(100), agentId: z.string().min(1).max(100).nullable() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      await runtime().setChatAgent(data.chatId, data.agentId)
      return { saved: true }
    } catch (err) {
      if (!isRuntimeDown(err)) return callRuntime(() => Promise.reject(err))
    }
    try {
      assignAgent(db, data.chatId, data.agentId)
      rotateSession(db, data.chatId)
      return { saved: false }
    } catch (err) {
      rethrow(err)
    }
  })

export const setChatAllowedFn = createServerFn({ method: "POST" })
  .validator(z.object({ chatId: z.string().min(1).max(100), allowed: z.boolean() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const n = db.update(chats).set({ allowed: data.allowed }).where(eq(chats.id, data.chatId)).run().changes
    if (!n) throw new Error("That chat doesn't exist anymore.")
    return { ok: true }
  })
