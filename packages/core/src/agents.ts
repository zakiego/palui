// Agents (PLAN 19): named personas with their own instructions, recall
// container and daily cap. Exactly one is the default (Palui), the owner's
// personal agent; it never answers in a shared chat.
import { createHash } from "node:crypto"
import { asc, desc, eq, isNull, or, sql, type SQL } from "drizzle-orm"
import { agents, chats, connectors, instructions, newId, type DB, type GuestTools, type OwnerPowers, type ToolPermission } from "@palui/db"
import type { ChatRow } from "./chats"
import { isOwner } from "./owner"
import { getSetting } from "./settings"
import { MAX_TOOL_PERMISSIONS, readPermissions, TOOL_NAME, TOOL_PERMISSIONS } from "./tools/permissions"

export type AgentRow = typeof agents.$inferSelect

export const DEFAULT_AGENT_SLUG = "palui"
export const DEFAULT_AGENT_NAME = "Palui"
export const AGENT_SLUG = /^[a-z][a-z0-9-]{1,23}$/
export const MAX_AGENT_NAME = 40
export const MAX_PERSONA = 8000
export const MAX_AGENTS = 20
// recall container names: what recall itself accepts.
export const RECALL_CONTAINER = /^[a-z0-9][a-z0-9_-]{0,63}$/
export const MAX_READ_CONTAINERS = 5

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

// Whether this agent answers in the chat: the same rule as agentForChat, so a
// chat with no agent (or a deleted one) is the default agent's.
export function servesChat(db: DB, agentId: string, chat: Pick<ChatRow, "agentId">): boolean {
  return agentForChat(db, chat).id === agentId
}

// SQL filter for the chats this agent answers in, matching servesChat. The
// chat history tools use it so an agent only ever sees its own chats.
export function chatsServedBy(db: DB, agentId: string): SQL {
  const agent = getAgent(db, agentId)
  if (!agent) return sql`0`
  if (!agent.isDefault) return eq(chats.agentId, agent.id)
  return or(isNull(chats.agentId), eq(chats.agentId, agent.id), sql`${chats.agentId} not in (select "id" from "agents")`)!
}

// Where the agent's memory lives; undefined means the recall connector's own container.
// With `recallPerChat` and a chat, each chat gets its own container, so what one
// customer said never comes back in another customer's chat.
export function agentContainer(agent: Pick<AgentRow, "isDefault" | "recallContainer" | "slug" | "recallPerChat">, chatId?: string | null): string | undefined {
  const base = agent.isDefault ? (agent.recallContainer ?? undefined) : (agent.recallContainer ?? `agent-${agent.slug}`)
  if (!agent.recallPerChat || !chatId || !base) return base
  return chatContainer(base, chatId)
}

// `<base>-c<10 hex>`, at most 64 characters (what recall accepts).
export function chatContainer(base: string, chatId: string): string {
  const hash = createHash("sha256").update(chatId).digest("hex").slice(0, 10)
  return `${chatContainerPrefix(base)}${hash}`
}

// What every per-chat container of this base starts with.
export function chatContainerPrefix(base: string): string {
  return `${base.slice(0, 64 - 12).replace(/[-_]+$/, "")}-c`
}

// Containers it reads on top of its own and never writes.
export function agentReadContainers(agent: Pick<AgentRow, "recallReadContainers">): string[] {
  return Array.isArray(agent.recallReadContainers) ? agent.recallReadContainers.filter((c) => typeof c === "string" && RECALL_CONTAINER.test(c)) : []
}

// What a turn of this agent may touch in recall (PLAN 19.5). The default agent
// is the owner's: its tools may name any container. Every other agent is
// locked to its own container for writes, plus its read list for reads.
export type MemoryScope = { container: string | undefined; read: string[]; locked: boolean }

export function memoryScope(agent: AgentRow, chatId?: string | null): MemoryScope {
  return { container: agentContainer(agent, chatId), read: agentReadContainers(agent), locked: !agent.isDefault }
}

// The owner's own memory: the memory connector's container and the default
// agent's override. No other agent may read or write it.
export function ownerContainers(db: DB): string[] {
  const out = new Set<string>()
  for (const c of db.select({ kind: connectors.kind, config: connectors.config }).from(connectors).where(eq(connectors.kind, "memory")).all()) {
    const name = typeof c.config?.container === "string" && c.config.container ? c.config.container : "default"
    out.add(name)
  }
  const def = db.select({ recallContainer: agents.recallContainer }).from(agents).where(eq(agents.isDefault, true)).get()
  if (def?.recallContainer) out.add(def.recallContainer)
  return [...out]
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
  recallReadContainers?: string[] | null
  recallPerChat?: boolean
  telegramConnectorId?: string | null
  avatarImage?: string | null
  avatarIcon?: string | null
  avatarColor?: string | null
}

// Trimmed, lowercased, unique; null when empty.
function readList(list: string[] | null | undefined): string[] | null {
  const out = [...new Set((list ?? []).map((c) => c.trim().toLowerCase()).filter(Boolean))]
  return out.length ? out : null
}

function clean(text: string): string {
  return text
    .replace(/\r\n?/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim()
}

function checkFields(db: DB, i: Partial<AgentInput>, selfId?: string, isDefault = false) {
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
  if (i.recallReadContainers) {
    if (i.recallReadContainers.length > MAX_READ_CONTAINERS) throw new AgentError(`At most ${MAX_READ_CONTAINERS} shared containers.`)
    for (const c of i.recallReadContainers) if (!RECALL_CONTAINER.test(c)) throw new AgentError(`${c.slice(0, 64)} isn't a recall container name: lowercase letters, digits, - and _.`)
  }
  // The owner's memory stays Palui's (PLAN 19.1): no other agent writes or reads it.
  if (!isDefault) {
    const owner = ownerContainers(db)
    if (i.recallContainer && owner.includes(i.recallContainer)) throw new AgentError(`${i.recallContainer} is the owner's memory. Only Palui uses it.`)
    const hit = i.recallReadContainers?.find((c) => owner.includes(c))
    if (hit) throw new AgentError(`${hit} is the owner's memory. Only Palui reads it.`)
  }
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
  if (fields.recallReadContainers !== undefined) fields.recallReadContainers = readList(fields.recallReadContainers)
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
      recallReadContainers: fields.recallReadContainers ?? null,
      recallPerChat: fields.recallPerChat ?? false,
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
  if (patch.recallReadContainers !== undefined) fields.recallReadContainers = readList(patch.recallReadContainers)
  // The default agent never runs in shared chats, so it has no shared-chat rules or bot of its own.
  if (row.isDefault) {
    delete fields.ownerPowers
    delete fields.guestTools
    delete fields.telegramConnectorId
  }
  checkFields(db, fields, id, row.isDefault)
  const set: Partial<AgentRow> = { updatedAt: now }
  if (fields.name !== undefined) set.name = fields.name
  if (fields.persona !== undefined) set.persona = fields.persona
  if (fields.ownerPowers !== undefined) set.ownerPowers = fields.ownerPowers
  if (fields.guestTools !== undefined) set.guestTools = fields.guestTools
  if (fields.dailyTokenCap !== undefined) set.dailyTokenCap = fields.dailyTokenCap
  if (fields.recallContainer !== undefined) set.recallContainer = fields.recallContainer || null
  if (fields.recallReadContainers !== undefined) set.recallReadContainers = fields.recallReadContainers
  if (fields.recallPerChat !== undefined) set.recallPerChat = fields.recallPerChat
  if (fields.telegramConnectorId !== undefined) set.telegramConnectorId = fields.telegramConnectorId || null
  if (fields.avatarImage !== undefined) set.avatarImage = fields.avatarImage || null
  if (fields.avatarIcon !== undefined) set.avatarIcon = fields.avatarIcon || null
  if (fields.avatarColor !== undefined) set.avatarColor = fields.avatarColor || null
  return db.update(agents).set(set).where(eq(agents.id, id)).returning().get()
}

// Tool permissions from the agent page (tools/permissions.ts). `allowAll`
// changes the default; `tools` sets one tool, null putting it back to the default.
export type ToolPermissionsPatch = { allowAll?: boolean; tools?: Record<string, ToolPermission | null> }

export function setToolPermissions(db: DB, id: string, patch: ToolPermissionsPatch, now = Date.now()): AgentRow {
  const row = getAgent(db, id)
  if (!row) throw new AgentError(`No agent ${id}.`)
  const next = readPermissions(row.toolPermissions)
  const tools = { ...next.tools }
  for (const [name, p] of Object.entries(patch.tools ?? {})) {
    if (!TOOL_NAME.test(name)) throw new AgentError(`Unknown tool name ${name}.`)
    if (p === null) delete tools[name]
    else if ((TOOL_PERMISSIONS as readonly string[]).includes(p)) tools[name] = p
    else throw new AgentError("A tool permission is allow, ask or deny.")
  }
  if (Object.keys(tools).length > MAX_TOOL_PERMISSIONS) throw new AgentError(`At most ${MAX_TOOL_PERMISSIONS} tool permissions.`)
  const allowAll = patch.allowAll ?? next.allowAll
  const toolPermissions = allowAll || Object.keys(tools).length ? { allowAll, tools } : null
  return db.update(agents).set({ toolPermissions, updatedAt: now }).where(eq(agents.id, id)).returning().get()
}

// Chats it served go back to the default agent and stop answering guests.
export function deleteAgent(db: DB, id: string): boolean {
  const row = getAgent(db, id)
  if (!row) return false
  if (row.isDefault) throw new AgentError("Palui is the default agent and can't be deleted.")
  db.transaction((tx) => {
    tx.update(chats).set({ agentId: null }).where(eq(chats.agentId, id)).run()
    // Connectors it owned keep the old owner id, so they serve nobody (never
    // everyone), and pause until the owner makes them global or gives them a new agent.
    tx.update(connectors).set({ enabled: false }).where(eq(connectors.agentId, id)).run()
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
