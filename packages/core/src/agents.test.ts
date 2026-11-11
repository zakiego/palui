import { describe, expect, it } from "vitest"
import { eq } from "drizzle-orm"
import { chats, memoryDb } from "@palui/db"
import { AgentError, agentContainer, assignAgent, createAgent, deleteAgent, getDefaultAgent, listAgents, updateAgent } from "./agents"
import { ensureChat, getChat } from "./chats"
import { createInstruction, listInstructions } from "./notes/service"
import { setSetting } from "./settings"

const PNG = "data:image/png;base64,iVBORw0KGgo="

describe("agents", () => {
  it("creates Palui from the legacy persona and moves old instructions to it", () => {
    const db = memoryDb()
    setSetting(db, "persona", "Kamu Palui versi lama.")
    const ins = createInstruction(db, { title: "a", body: "b" })
    const palui = getDefaultAgent(db)
    expect(palui).toMatchObject({ slug: "palui", isDefault: true, persona: "Kamu Palui versi lama." })
    expect(ins.agentId).toBe(palui.id)
    expect(getDefaultAgent(db).id).toBe(palui.id)
    expect(agentContainer(palui)).toBeUndefined()
  })

  it("lists Palui first, validates fields and gives new agents their own container", () => {
    const db = memoryDb()
    const chef = createAgent(db, { slug: "chef", name: " Chef ", persona: "p" })
    expect(listAgents(db).map((a) => a.slug)).toEqual(["palui", "chef"])
    expect(chef.name).toBe("Chef")
    expect(agentContainer(chef)).toBe("agent-chef")
    expect(() => createAgent(db, { slug: "chef", name: "x", persona: "p" })).toThrow(AgentError)
    expect(() => createAgent(db, { slug: "9x", name: "x", persona: "p" })).toThrow(AgentError)
    expect(() => updateAgent(db, chef.id, { dailyTokenCap: -1 })).toThrow(AgentError)
  })

  it("keeps an avatar photo small and the icon and color to known names", () => {
    const db = memoryDb()
    const a = createAgent(db, { slug: "cs", name: "CS", persona: "p", avatarIcon: "headset", avatarColor: "teal" })
    expect(a).toMatchObject({ avatarIcon: "headset", avatarColor: "teal", avatarImage: null })
    expect(updateAgent(db, a.id, { avatarImage: PNG }).avatarImage).toBe(PNG)
    expect(updateAgent(db, a.id, { avatarImage: null }).avatarImage).toBeNull()
    expect(() => updateAgent(db, a.id, { avatarImage: "data:image/svg+xml;base64,PHN2Zz4=" })).toThrow(AgentError)
    expect(() => updateAgent(db, a.id, { avatarImage: `data:image/png;base64,${"A".repeat(100_000)}` })).toThrow(AgentError)
    expect(() => updateAgent(db, a.id, { avatarIcon: "skull" })).toThrow(AgentError)
    expect(() => updateAgent(db, a.id, { avatarColor: "#ff0000" })).toThrow(AgentError)
  })

  it("never gives Palui shared-chat rules or a bot of its own", () => {
    const db = memoryDb()
    const palui = getDefaultAgent(db)
    const r = updateAgent(db, palui.id, { ownerPowers: "approve", guestTools: "none", telegramConnectorId: "tgbot-x", name: "Palui 2" })
    expect(r).toMatchObject({ ownerPowers: "off", guestTools: "safe", telegramConnectorId: null, name: "Palui 2" })
    expect(() => deleteAgent(db, palui.id)).toThrow(AgentError)
  })

  it("assigns an agent to a chat and allows it; back to Palui clears it; delete frees its chats", () => {
    const db = memoryDb()
    const chef = createAgent(db, { slug: "chef", name: "Chef", persona: "p" })
    createInstruction(db, { title: "a", body: "b", agentId: chef.id })
    const group = ensureChat(db, { channel: "whatsapp", externalId: "1@g.us", kind: "group", allowed: false })
    expect(assignAgent(db, group.id, chef.id).chat).toMatchObject({ agentId: chef.id, allowed: true })
    db.update(chats).set({ allowed: false }).where(eq(chats.id, group.id)).run()
    // Back to Palui still turns the chat on: Palui answers the owner there.
    expect(assignAgent(db, group.id, null).chat).toMatchObject({ agentId: null, allowed: true })
    assignAgent(db, group.id, chef.id)
    deleteAgent(db, chef.id)
    expect(getChat(db, group.id)!.agentId).toBeNull()
    expect(listInstructions(db, { agentId: chef.id })).toEqual([])
  })

  it("refuses to give a chat from one agent's bot to another agent", () => {
    const db = memoryDb()
    const chef = createAgent(db, { slug: "chef", name: "Chef", persona: "p", telegramConnectorId: "tgbot-chef" })
    const cs = createAgent(db, { slug: "cs", name: "CS", persona: "p" })
    const chat = ensureChat(db, { channel: "telegram", externalId: "42", connectorId: "tgbot-chef", agentId: chef.id })
    expect(() => assignAgent(db, chat.id, cs.id)).toThrow(AgentError)
    expect(() => createAgent(db, { slug: "x2", name: "x", persona: "p", telegramConnectorId: "tgbot-chef" })).toThrow(AgentError)
  })
})
