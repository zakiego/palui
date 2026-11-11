// Agents page (PLAN 19.7): each agent's persona, shared-chat rules, daily cap,
// recall container and Telegram bot. The runtime reads them at the start of
// every turn, so writes go straight to the table.
import { AgentError, AGENT_COLORS, AGENT_ICONS, MAX_AVATAR_IMAGE, assignAgent, getAgent, createAgent, deleteAgent, getAgentBySlug, listAgents, MAX_AGENT_NAME, MAX_PERSONA, updateAgent } from "@palui/core/agents"
import { rotateSession, setReplyMode } from "@palui/core/chats"
import { listInstructions } from "@palui/core/notes/service"
import { chats, REPLY_MODES } from "@palui/db"
import { createServerFn } from "@tanstack/react-start"
import { eq, isNull } from "drizzle-orm"
import { z } from "zod"
import { agentMemory, agentTools, agentView, telegramBots } from "@/server/agents"
import { callRuntime, isRuntimeDown, runtime } from "@/server/runtime"
import { chatList } from "@/server/queries"
import { requireUser, requireWriter } from "@/server/session"

// Chats the agent page lists at once, newest first. Past this, the Chats page has search.
const AGENT_CHATS_LIMIT = 100

function rethrow(err: unknown): never {
  if (err instanceof AgentError) throw new Error(err.message)
  throw err
}

export const getAgentsPage = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const all = db.select({ agentId: chats.agentId }).from(chats).all()
  const agents = listAgents(db).map((a) => agentView(db, a, all.filter((c) => c.agentId === a.id).length))
  return { agents, bots: telegramBots(db) }
})

// One agent's page (/agents/<slug>): its settings, its instructions and the
// chats it answers in. `agent` is null when the slug doesn't exist.
export const getAgentPage = createServerFn({ method: "GET" })
  .validator(z.object({ slug: z.string().trim().min(1).max(64) }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    const row = getAgentBySlug(db, data.slug)
    if (!row) return { agent: null }
    // Palui answers every chat that has no agent of its own.
    const own = chatList(db, row.isDefault ? isNull(chats.agentId) : eq(chats.agentId, row.id))
    // A bot that belongs to another agent isn't offered.
    const taken = new Set(listAgents(db).filter((a) => a.id !== row.id && a.telegramConnectorId).map((a) => a.telegramConnectorId))
    return {
      agent: agentView(db, row, own.length),
      bots: telegramBots(db).filter((b) => !taken.has(b.id)),
      instructions: listInstructions(db, { agentId: row.id }),
      chats: own.slice(0, AGENT_CHATS_LIMIT).map((c) => ({ id: c.id, title: c.title, channel: c.channel, kind: c.kind, lastAt: c.lastAt, allowed: c.allowed, replyMode: c.replyMode })),
      chatsTotal: own.length,
    }
  })

// The Tools and Memory tabs load on their own: both ask the runtime, which can
// be slow or down, and the settings form shouldn't wait on it.
export const getAgentToolsFn = createServerFn({ method: "GET" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    const row = getAgent(db, data.id)
    if (!row) throw new Error("That agent doesn't exist anymore.")
    return agentTools(db, row)
  })

export const getAgentMemoryFn = createServerFn({ method: "GET" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    const row = getAgent(db, data.id)
    if (!row) throw new Error("That agent doesn't exist anymore.")
    return agentMemory(db, row)
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

// Groups only: answer @mentions and replies, or every message. The runtime
// reads the chat row on every inbound message, so this goes straight to the table.
export const setChatReplyModeFn = createServerFn({ method: "POST" })
  .validator(z.object({ chatId: z.string().min(1).max(100), mode: z.enum(REPLY_MODES) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!setReplyMode(db, data.chatId, data.mode)) throw new Error("That chat doesn't exist anymore.")
    return { ok: true }
  })

export const setChatAllowedFn = createServerFn({ method: "POST" })
  .validator(z.object({ chatId: z.string().min(1).max(100), allowed: z.boolean() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const n = db.update(chats).set({ allowed: data.allowed }).where(eq(chats.id, data.chatId)).run().changes
    if (!n) throw new Error("That chat doesn't exist anymore.")
    return { ok: true }
  })
