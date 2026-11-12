import { describe, expect, it } from "vitest"
import { eq } from "drizzle-orm"
import { chats, memoryDb, messages, newId, type DB, type MessageRole } from "@palui/db"
import { ensureChat, readChatMessages, searchChats, type ChatRow } from "@palui/core/chats"
import { assignAgent, createAgent, deleteAgent, getDefaultAgent } from "@palui/core/agents"
import type { PaluiTool, TurnInfo } from "@palui/core/connectors/define"
import manifest from "./manifest"
import { chatTools, offersChatTools, parseTime, MESSAGE_MAX_CHARS } from "./runtime"

// Tuesday 2026-11-10 10:00 WITA.
const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"
const MIN = 60_000

function addMessage(db: DB, chat: ChatRow, role: MessageRole, text: string, at: number, content: Record<string, unknown> = {}) {
  db.insert(messages)
    .values({ id: newId("msg"), chatId: chat.id, sessionId: chat.sessionId, role, text, content: { text, ...content }, createdAt: at })
    .run()
  db.update(chats).set({ lastMessageAt: at }).where(eq(chats.id, chat.id)).run()
}

function setup() {
  const db = memoryDb()
  const dm = ensureChat(db, { channel: "whatsapp", externalId: "6281234@s.whatsapp.net", title: "Zaki" }, NOW - 100 * MIN)
  const family = ensureChat(db, { channel: "whatsapp", externalId: "1203630@g.us", title: "Keluarga Besar", kind: "group" }, NOW - 90 * MIN)
  const work = ensureChat(db, { channel: "telegram", externalId: "-100777", title: "Tim Vorneo", kind: "group" }, NOW - 80 * MIN)
  const web = ensureChat(db, { channel: "web", externalId: "web", title: "Palui (web)" }, NOW - 70 * MIN)
  addMessage(db, family, "user", "besok kumpul jam 7 ya", NOW - 60 * MIN)
  addMessage(db, family, "assistant", "Oke, aku catat.", NOW - 59 * MIN)
  addMessage(db, family, "tool", "{\"ok\":true}", NOW - 58 * MIN)
  addMessage(db, family, "system", "summary", NOW - 57 * MIN)
  addMessage(db, work, "user", "deploy jam 3", NOW - 30 * MIN)
  addMessage(db, dm, "user", "halo", NOW - 10 * MIN)
  const turn: TurnInfo = { runId: "run_1", chatId: dm.id, channel: "whatsapp", trigger: "message", tz: WITA, now: NOW }
  return { db, dm, family, work, web, turn, tools: chatTools(db, turn) }
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

describe("chat history manifest", () => {
  it("is a builtin skill", () => {
    expect(manifest).toMatchObject({ id: "chats", kind: "skill", builtin: true, icon: "messages-square" })
  })
})

describe("chat tools", () => {
  it("exposes two read tools, both untrusted", () => {
    const { tools } = setup()
    expect(Object.fromEntries(Object.entries(tools).map(([k, v]) => [k, [v.class, v.untrusted]]))).toEqual({
      search_chats: ["read", true],
      read_chat: ["read", true],
    })
  })

  it("are offered in a DM or the web chat, never in a group or a hook turn", () => {
    const { db, turn, family, web } = setup()
    expect(offersChatTools(db, turn)).toBe(true)
    expect(offersChatTools(db, { ...turn, chatId: web.id, channel: "web" })).toBe(true)
    expect(offersChatTools(db, { ...turn, trigger: "schedule" })).toBe(true)
    expect(offersChatTools(db, { ...turn, chatId: family.id })).toBe(false)
    expect(offersChatTools(db, { ...turn, trigger: "hook" })).toBe(false)
    expect(offersChatTools(db, { ...turn, chatId: "chat_missing" })).toBe(false)
  })
})

describe("search_chats", () => {
  it("lists the newest conversation first, chats with no messages last", async () => {
    const { tools } = setup()
    const res = await call(tools.search_chats, {})
    expect(res.chats.map((c: { title: string }) => c.title)).toEqual(["Zaki", "Tim Vorneo", "Keluarga Besar", "Palui (web)"])
    expect(res.chats[2]).toEqual({
      id: expect.stringMatching(/^chat_/),
      channel: "whatsapp",
      externalId: "1203630@g.us",
      title: "Keluarga Besar",
      kind: "group",
      lastMessageAt: "2026-11-10T09:03:00.000+08:00",
      messageCount: 2,
    })
    expect(res.chats[3]).toMatchObject({ lastMessageAt: null, messageCount: 0 })
  })

  it("matches title, external id and chat id, case-insensitive", async () => {
    const { tools, family } = setup()
    expect((await call(tools.search_chats, { query: "keluarga" })).chats.map((c: { id: string }) => c.id)).toEqual([family.id])
    expect((await call(tools.search_chats, { query: "@g.us" })).chats.map((c: { id: string }) => c.id)).toEqual([family.id])
    expect((await call(tools.search_chats, { query: family.id })).chats.map((c: { id: string }) => c.id)).toEqual([family.id])
  })

  it("treats % and _ in the query literally", async () => {
    const { db, tools } = setup()
    ensureChat(db, { channel: "telegram", externalId: "9", title: "Diskon 50% hari ini" }, NOW)
    expect((await call(tools.search_chats, { query: "0% h" })).chats.map((c: { title: string }) => c.title)).toEqual(["Diskon 50% hari ini"])
    expect((await call(tools.search_chats, { query: "k%r" })).count).toBe(0)
    expect((await call(tools.search_chats, { query: "t_m" })).count).toBe(0)
  })

  it("filters by channel and kind", async () => {
    const { tools } = setup()
    expect((await call(tools.search_chats, { channel: "whatsapp" })).chats.map((c: { title: string }) => c.title)).toEqual(["Zaki", "Keluarga Besar"])
    expect((await call(tools.search_chats, { kind: "group" })).chats.map((c: { title: string }) => c.title)).toEqual(["Tim Vorneo", "Keluarga Besar"])
    expect((await call(tools.search_chats, { channel: "telegram", kind: "dm" })).count).toBe(0)
  })

  it("says so when nothing matches", async () => {
    const { tools } = setup()
    expect(await call(tools.search_chats, { query: "arisan" })).toEqual({ ok: true, count: 0, chats: [], message: 'Nggak ada chat yang cocok sama "arisan".' })
  })

  it("caps results at 20", () => {
    const { db } = setup()
    for (let i = 0; i < 30; i++) ensureChat(db, { channel: "telegram", externalId: String(i) }, NOW)
    expect(searchChats(db, { limit: 500 })).toHaveLength(20)
    expect(searchChats(db, { limit: 3 })).toHaveLength(3)
  })
})

describe("read_chat", () => {
  it("reads owner and Palui messages oldest first, skipping tool and system rows", async () => {
    const { tools, family } = setup()
    const res = await call(tools.read_chat, { chat: family.id })
    expect(res.ok).toBe(true)
    expect(res.chat).toMatchObject({ id: family.id, title: "Keluarga Besar" })
    expect(res.messages).toEqual([
      { role: "user", time: "2026-11-10T09:00:00.000+08:00", text: "besok kumpul jam 7 ya" },
      { role: "assistant", time: "2026-11-10T09:01:00.000+08:00", text: "Oke, aku catat." },
    ])
  })

  it("finds a chat by its exact external id, and asks for the channel when that's ambiguous", async () => {
    const { db, tools, work } = setup()
    expect((await call(tools.read_chat, { chat: "-100777" })).chat.id).toBe(work.id)
    expect((await call(tools.read_chat, { chat: "-1007" })).error).toBe('No chat "-1007". Call search_chats to get the id.')
    ensureChat(db, { channel: "whatsapp", externalId: "-100777" }, NOW)
    const res = await call(tools.read_chat, { chat: "-100777" })
    expect(res.ok).toBe(false)
    expect(res.error).toBe('"-100777" matches chats on telegram, whatsapp. Pass channel, or use the chat id.')
    expect((await call(tools.read_chat, { chat: "-100777", channel: "telegram" })).chat.id).toBe(work.id)
  })

  it("returns the newest N, capped at 100", () => {
    const { db, web } = setup()
    for (let i = 0; i < 150; i++) addMessage(db, web, "user", `pesan ${i}`, NOW + i * MIN)
    const last5 = readChatMessages(db, web.id, { limit: 5 })
    expect(last5.map((m) => m.text)).toEqual(["pesan 145", "pesan 146", "pesan 147", "pesan 148", "pesan 149"])
    expect(readChatMessages(db, web.id)).toHaveLength(30)
    expect(readChatMessages(db, web.id, { limit: 1000 })).toHaveLength(100)
  })

  it("filters by before, since and text", async () => {
    const { db, tools, web } = setup()
    for (let i = 0; i < 6; i++) addMessage(db, web, i % 2 ? "assistant" : "user", `Rapat ${i}`, NOW + i * 60 * MIN)
    // NOW is 10:00 WITA, so messages are at 10:00, 11:00 ... 15:00.
    const read = async (input: Record<string, unknown>) => (await call(tools.read_chat, { chat: web.id, ...input })).messages.map((m: { text: string }) => m.text)
    expect(await read({ before: "2026-11-10T12:00" })).toEqual(["Rapat 0", "Rapat 1"])
    expect(await read({ since: "2026-11-10T14:00" })).toEqual(["Rapat 4", "Rapat 5"])
    expect(await read({ since: "2026-11-10T11:00", before: "2026-11-10T13:00" })).toEqual(["Rapat 1", "Rapat 2"])
    expect(await read({ since: "2026-11-10T05:00:00Z" })).toEqual(["Rapat 3", "Rapat 4", "Rapat 5"])
    expect(await read({ query: "rapat 3" })).toEqual(["Rapat 3"])
    expect((await call(tools.read_chat, { chat: web.id, before: "kemarin" })).error).toBe('"kemarin" isn\'t a time. Use YYYY-MM-DDTHH:mm.')
  })

  it("clips long messages and shows media, quotes and hooks as short markers", async () => {
    const { db, tools, web } = setup()
    addMessage(db, web, "user", "x".repeat(2000), NOW)
    addMessage(db, web, "user", "ini KTP", NOW + 1, { media: [{ kind: "image", mimeType: "image/jpeg", data: "A".repeat(5000) }, { kind: "document", mimeType: "application/pdf", filename: "tiket.pdf" }] })
    addMessage(db, web, "user", "", NOW + 2, { media: [{ kind: "audio", mimeType: "audio/ogg" }] })
    addMessage(db, web, "user", "setuju", NOW + 3, { quoted: { from: "other", text: "y".repeat(500) } })
    addMessage(db, web, "user", "build failed", NOW + 4, { hook: { id: "hook_1", name: "GitHub", mode: "summarize" } })
    const [long, media, audio, quote, hook] = (await call(tools.read_chat, { chat: web.id })).messages
    expect(long.text).toBe(`${"x".repeat(MESSAGE_MAX_CHARS)}… [dipotong, 2000 karakter]`)
    expect(media.text).toBe("ini KTP [gambar] [dokumen: tiket.pdf]")
    expect(JSON.stringify(media)).not.toContain("AAAA")
    expect(audio.text).toBe("[audio]")
    expect(quote.replyTo).toBe(`${"y".repeat(200)}… [dipotong, 500 karakter]`)
    expect(hook).toMatchObject({ role: "user", text: "build failed", hook: "GitHub" })
  })
})

// The report: Palui in the owner's DM read the "Finansial" group, which the
// finansial agent answers in. Each agent sees only its own chats.
describe("chat tools per agent", () => {
  function agents() {
    const base = setup()
    const { db, dm, family } = base
    const palui = getDefaultAgent(db)
    const fin = createAgent(db, { slug: "finansial", name: "Finansial", persona: "Catat keuangan." })
    const finGroup = ensureChat(db, { channel: "whatsapp", externalId: "120363413215483666@g.us", title: "Finansial", kind: "group", agentId: fin.id }, NOW - 50 * MIN)
    const finDm = ensureChat(db, { channel: "whatsapp", externalId: "6289999@s.whatsapp.net", title: "Owner ke Finansial", agentId: fin.id }, NOW - 40 * MIN)
    addMessage(db, finGroup, "user", "laundry 43rb dari Jago", NOW - 20 * MIN)
    addMessage(db, finGroup, "assistant", "Tercatat.", NOW - 19 * MIN)
    addMessage(db, finDm, "user", "rekap bulan ini", NOW - 5 * MIN)
    const paluiTurn: TurnInfo = { ...base.turn, agentId: palui.id }
    const finTurn: TurnInfo = { ...base.turn, chatId: finDm.id, agentId: fin.id }
    return { ...base, palui, fin, finGroup, finDm, paluiTools: chatTools(db, paluiTurn), finTools: chatTools(db, finTurn), dm, family }
  }
  const ids = (r: { chats: { id: string }[] }) => r.chats.map((c) => c.id).sort()

  it("search_chats lists only the caller's chats, by title, JID or id", async () => {
    const { dm, family, work, web, finGroup, finDm, paluiTools, finTools } = agents()
    expect(ids(await call(paluiTools.search_chats, {}))).toEqual([dm.id, family.id, work.id, web.id].sort())
    expect(ids(await call(finTools.search_chats, {}))).toEqual([finGroup.id, finDm.id].sort())
    for (const query of ["Finansial", "120363413215483666", finGroup.id]) {
      expect((await call(paluiTools.search_chats, { query })).count).toBe(0)
    }
    expect((await call(finTools.search_chats, { query: "Keluarga" })).count).toBe(0)
  })

  it("read_chat refuses another agent's chat by id or external id and leaks nothing", async () => {
    const { finGroup, dm, paluiTools, finTools } = agents()
    for (const chat of [finGroup.id, finGroup.externalId]) {
      const res = await call(paluiTools.read_chat, { chat })
      expect(res).toEqual({ ok: false, error: `Chat "${chat}" belongs to another agent. This agent can only read the chats it answers in.` })
      expect(JSON.stringify(res)).not.toContain("laundry")
    }
    expect((await call(finTools.read_chat, { chat: dm.id })).ok).toBe(false)
  })

  it("read_chat still reads the caller's own chats", async () => {
    const { finGroup, family, paluiTools, finTools } = agents()
    expect((await call(finTools.read_chat, { chat: finGroup.externalId })).messages.map((m: { text: string }) => m.text)).toEqual(["laundry 43rb dari Jago", "Tercatat."])
    expect((await call(paluiTools.read_chat, { chat: family.id })).messages.map((m: { text: string }) => m.text)).toEqual(["besok kumpul jam 7 ya", "Oke, aku catat."])
  })

  it("follows the chat's agent: reassigning moves the chat, deleting the agent hands it back to Palui", async () => {
    const { db, fin, finGroup, family, palui } = agents()
    const turn: TurnInfo = { runId: "run_1", chatId: family.id, channel: "whatsapp", trigger: "message", tz: WITA, now: NOW, agentId: palui.id }
    assignAgent(db, family.id, fin.id)
    expect((await call(chatTools(db, turn).read_chat, { chat: family.id })).ok).toBe(false)
    deleteAgent(db, fin.id)
    expect((await call(chatTools(db, turn).read_chat, { chat: finGroup.id })).ok).toBe(true)
    expect((await call(chatTools(db, turn).read_chat, { chat: family.id })).ok).toBe(true)
  })

  it("without agentId on the turn, uses the agent of the turn's chat", async () => {
    const { db, finDm, finGroup, dm } = agents()
    const tools = chatTools(db, { runId: "run_1", chatId: finDm.id, channel: "whatsapp", trigger: "schedule", tz: WITA, now: NOW })
    expect((await call(tools.read_chat, { chat: finGroup.id })).ok).toBe(true)
    expect((await call(tools.read_chat, { chat: dm.id })).ok).toBe(false)
  })

  it("an unknown chat id on the turn sees nothing", async () => {
    const { db } = agents()
    const tools = chatTools(db, { runId: "source_1", chatId: "", channel: null, trigger: "schedule", tz: WITA, now: NOW })
    expect((await call(tools.search_chats, {})).count).toBe(0)
  })
})

describe("parseTime", () => {
  it("reads wall-clock time in the zone, or an instant with an offset", () => {
    expect(parseTime("2026-11-10T10:00", WITA)).toBe(NOW)
    expect(parseTime("2026-11-10", WITA)).toBe(Date.parse("2026-11-09T16:00:00Z"))
    expect(parseTime("2026-11-10T02:00:00Z", WITA)).toBe(NOW)
    expect(parseTime("nanti", WITA)).toBeNull()
  })
})
