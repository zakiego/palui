// Agents (PLAN 19): named personas with their own instructions, recall
// container and daily cap. Exactly one is the default (Palui), the owner's
// personal agent; it never answers in a shared chat.
import { asc, desc, eq, isNull } from "drizzle-orm"
import { agents, chats, instructions, newId, type DB, type GuestTools, type OwnerPowers } from "@palui/db"
import type { ChatRow } from "./chats"
import { isOwner } from "./owner"
import { getSetting } from "./settings"

export type AgentRow = typeof agents.$inferSelect

export const DEFAULT_AGENT_SLUG = "palui"
export const DEFAULT_AGENT_NAME = "Palui"
export const AGENT_SLUG = /^[a-z][a-z0-9-]{1,23}$/
export const MAX_AGENT_NAME = 40
export const MAX_PERSONA = 8000
export const MAX_AGENTS = 20
// recall container names: what recall itself accepts.
export const RECALL_CONTAINER = /^[a-z0-9][a-z0-9_-]{0,63}$/

// Avatar choices (PLAN 19.7). Names only: the dashboard maps them to lucide
// icons and theme colors, so a stored value it doesn't know falls back.
export const AGENT_ICONS = [
  "bot",
  "sparkles",
  "smile",
  "heart",
  "star",
  "chef-hat",
  "coffee",
  "briefcase",
  "headset",
  "store",
  "home",
  "book-open",
  "graduation-cap",
  "code",
  "wrench",
  "calculator",
  "scale",
  "stethoscope",
  "dumbbell",
  "leaf",
  "music",
  "camera",
  "plane",
  "moon",
] as const
export type AgentIcon = (typeof AGENT_ICONS)[number]
export const AGENT_COLORS = ["zinc", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"] as const
export type AgentColor = (typeof AGENT_COLORS)[number]
// A browser-resized photo is a few KB; this cap keeps a pasted original out of every page load.
export const MAX_AVATAR_IMAGE = 96 * 1024
export const AVATAR_IMAGE = /^data:image\/(png|jpeg|webp);base64,[A-Za-z0-9+/]+=*$/

export class AgentError extends Error {}

// The default agent, created on first use from the legacy settings.persona.
export function getDefaultAgent(db: DB, now = Date.now()): AgentRow {
  const found = db.select().from(agents).where(eq(agents.isDefault, true)).get()
  if (found) return found
  return db.transaction((tx) => {
    const again = tx.select().from(agents).where(eq(agents.isDefault, true)).get()
    if (again) return again
    const row = tx
      .insert(agents)
      .values({
        id: newId("agt"),
        slug: DEFAULT_AGENT_SLUG,
        name: DEFAULT_AGENT_NAME,
        persona: getSetting(tx as unknown as DB, "persona"),
        isDefault: true,
        createdAt: now,
        updatedAt: now,
      })
      .returning()
      .get()
    // Instructions saved before agents were Palui's.
    tx.update(instructions).set({ agentId: row.id }).where(isNull(instructions.agentId)).run()
    return row
  })
}

export function listAgents(db: DB): AgentRow[] {
  getDefaultAgent(db)
  return db.select().from(agents).orderBy(desc(agents.isDefault), asc(agents.createdAt), asc(agents.id)).all()
}

export function getAgent(db: DB, id: string): AgentRow | undefined {
  return db.select().from(agents).where(eq(agents.id, id)).get()
}

export function getAgentBySlug(db: DB, slug: string): AgentRow | undefined {
  return db
    .select()
    .from(agents)
    .where(eq(agents.slug, slug.trim().toLowerCase()))
    .get()
}

export function agentForTelegramConnector(db: DB, connectorId: string): AgentRow | undefined {
  return db.select().from(agents).where(eq(agents.telegramConnectorId, connectorId)).get()
}

// The agent that answers in this chat. A chat whose agent was deleted falls back to the default.
export function agentForChat(db: DB, chat: Pick<ChatRow, "agentId">): AgentRow {
  return (chat.agentId ? getAgent(db, chat.agentId) : undefined) ?? getDefaultAgent(db)
}

// Where the agent's memory lives; undefined means the recall connector's own container.
export function agentContainer(agent: AgentRow): string | undefined {
  if (agent.isDefault) return agent.recallContainer ?? undefined
  return agent.recallContainer ?? `agent-${agent.slug}`
}

// A group, or a DM with someone who isn't the owner (PLAN 19.2).
export function isSharedChat(db: DB, chat: Pick<ChatRow, "channel" | "kind" | "externalId">): boolean {
  if (chat.channel === "web") return false
  if (chat.kind === "group") return true
  return !isOwner(db, { channel: chat.channel, externalId: chat.externalId })
}

export type AgentInput = {
  slug?: string
  name: string
  persona: string
  ownerPowers?: OwnerPowers
  guestTools?: GuestTools
  dailyTokenCap?: number
  recallContainer?: string | null
  telegramConnectorId?: string | null
  avatarImage?: string | null
  avatarIcon?: string | null
  avatarColor?: string | null
}

function clean(text: string): string {
  return text
    .replace(/\r\n?/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim()
}

function checkFields(db: DB, i: Partial<AgentInput>, selfId?: string) {
  if (i.name !== undefined) {
    if (!i.name) throw new AgentError("Name is empty.")
    if (i.name.length > MAX_AGENT_NAME) throw new AgentError(`Name is at most ${MAX_AGENT_NAME} characters.`)
  }
  if (i.persona !== undefined) {
    if (!i.persona) throw new AgentError("Persona is empty.")
    if (i.persona.length > MAX_PERSONA) throw new AgentError(`Persona is at most ${MAX_PERSONA} characters.`)
  }
  if (i.ownerPowers !== undefined && i.ownerPowers !== "off" && i.ownerPowers !== "approve") throw new AgentError("Owner powers must be off or approve.")
  if (i.guestTools !== undefined && i.guestTools !== "none" && i.guestTools !== "safe") throw new AgentError("Guest tools must be none or safe.")
  if (i.dailyTokenCap !== undefined && !(Number.isInteger(i.dailyTokenCap) && i.dailyTokenCap >= 0)) throw new AgentError("Daily cap must be a whole number, 0 or more.")
  if (i.recallContainer && !RECALL_CONTAINER.test(i.recallContainer)) throw new AgentError("Recall container uses lowercase letters, digits, - and _.")
  if (i.avatarImage) {
    if (i.avatarImage.length > MAX_AVATAR_IMAGE) throw new AgentError("That photo is too big. Pick a smaller one.")
    if (!AVATAR_IMAGE.test(i.avatarImage)) throw new AgentError("The photo must be a PNG, JPEG or WebP image.")
  }
  if (i.avatarIcon && !(AGENT_ICONS as readonly string[]).includes(i.avatarIcon)) throw new AgentError("Unknown avatar icon.")
  if (i.avatarColor && !(AGENT_COLORS as readonly string[]).includes(i.avatarColor)) throw new AgentError("Unknown avatar color.")
  if (i.telegramConnectorId) {
    const taken = agentForTelegramConnector(db, i.telegramConnectorId)
    if (taken && taken.id !== selfId) throw new AgentError(`That Telegram bot already belongs to ${taken.name}.`)
  }
}

export function createAgent(db: DB, input: AgentInput, now = Date.now()): AgentRow {
  const slug = (input.slug ?? "").trim().toLowerCase()
  if (!AGENT_SLUG.test(slug)) throw new AgentError("Slug is 2 to 24 characters: lowercase letters, digits and -, starting with a letter.")
  if (getAgentBySlug(db, slug)) throw new AgentError(`There's already an agent called ${slug}.`)
  const fields = { ...input, name: clean(input.name).replace(/\n+/g, " "), persona: clean(input.persona) }
  checkFields(db, fields)
  if (listAgents(db).length >= MAX_AGENTS) throw new AgentError(`At most ${MAX_AGENTS} agents.`)
  return db
    .insert(agents)
    .values({
      id: newId("agt"),
      slug,
      name: fields.name,
      persona: fields.persona,
      ownerPowers: fields.ownerPowers ?? "off",
      guestTools: fields.guestTools ?? "safe",
      dailyTokenCap: fields.dailyTokenCap ?? 0,
      recallContainer: fields.recallContainer || null,
      telegramConnectorId: fields.telegramConnectorId || null,
      avatarImage: fields.avatarImage || null,
      avatarIcon: fields.avatarIcon || null,
      avatarColor: fields.avatarColor || null,
      createdAt: now,
      updatedAt: now,
    })
    .returning()
    .get()
}

export function updateAgent(db: DB, id: string, patch: Partial<Omit<AgentInput, "slug">>, now = Date.now()): AgentRow {
  const row = getAgent(db, id)
  if (!row) throw new AgentError(`No agent ${id}.`)
  const fields: Partial<AgentInput> = { ...patch }
  if (patch.name !== undefined) fields.name = clean(patch.name).replace(/\n+/g, " ")
  if (patch.persona !== undefined) fields.persona = clean(patch.persona)
  // The default agent never runs in shared chats, so it has no shared-chat rules or bot of its own.
  if (row.isDefault) {
    delete fields.ownerPowers
    delete fields.guestTools
    delete fields.telegramConnectorId
  }
  checkFields(db, fields, id)
  const set: Partial<AgentRow> = { updatedAt: now }
  if (fields.name !== undefined) set.name = fields.name
  if (fields.persona !== undefined) set.persona = fields.persona
  if (fields.ownerPowers !== undefined) set.ownerPowers = fields.ownerPowers
  if (fields.guestTools !== undefined) set.guestTools = fields.guestTools
  if (fields.dailyTokenCap !== undefined) set.dailyTokenCap = fields.dailyTokenCap
  if (fields.recallContainer !== undefined) set.recallContainer = fields.recallContainer || null
  if (fields.telegramConnectorId !== undefined) set.telegramConnectorId = fields.telegramConnectorId || null
  if (fields.avatarImage !== undefined) set.avatarImage = fields.avatarImage || null
  if (fields.avatarIcon !== undefined) set.avatarIcon = fields.avatarIcon || null
  if (fields.avatarColor !== undefined) set.avatarColor = fields.avatarColor || null
  return db.update(agents).set(set).where(eq(agents.id, id)).returning().get()
}

// Chats it served go back to the default agent and stop answering guests.
export function deleteAgent(db: DB, id: string): boolean {
  const row = getAgent(db, id)
  if (!row) return false
  if (row.isDefault) throw new AgentError("Palui is the default agent and can't be deleted.")
  db.transaction((tx) => {
    tx.update(chats).set({ agentId: null }).where(eq(chats.agentId, id)).run()
    tx.delete(instructions).where(eq(instructions.agentId, id)).run()
    tx.delete(agents).where(eq(agents.id, id)).run()
  })
  return true
}

// Give a chat an agent (PLAN 19.2) and turn the chat on. The default agent
// answers only the owner in a shared chat: its memory is the owner's.
// The caller rotates the session (it saves the old one to memory first).
export function assignAgent(db: DB, chatId: string, agentId: string | null): { chat: ChatRow; agent: AgentRow } {
  const chat = db.select().from(chats).where(eq(chats.id, chatId)).get()
  if (!chat) throw new AgentError(`No chat ${chatId}.`)
  const agent = agentId ? getAgent(db, agentId) : getDefaultAgent(db)
  if (!agent) throw new AgentError(`No agent ${agentId}.`)
  if (chat.connectorId && agent.telegramConnectorId !== chat.connectorId) throw new AgentError("This chat came in on another agent's bot, so its agent is fixed.")
  // Picking an agent turns the chat on. Back to the default agent, guests stop
  // being answered (they need an agent) but the owner still is.
  const updated = db.update(chats).set({ agentId: agent.isDefault ? null : agent.id, allowed: true }).where(eq(chats.id, chatId)).returning().get()
  return { chat: updated, agent }
}
