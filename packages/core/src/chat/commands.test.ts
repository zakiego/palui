import { eq, sql } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { chats, memoryDb, messages, outbox, runs, sessions, type DB } from "@palui/db"
import { listEvents } from "../events"
import { claimOwner, createClaim } from "../owner"
import { getSetting, setSetting } from "../settings"
import { parseCommand } from "./router"
import { channelRuntime, fakeChannel, fnTool, harness, modelRuntime, promptText, say, scriptedModel, tgMessage, toolsRuntime } from "./testing"
import { MODEL_OFF_TEXT } from "./turn"

function ownTelegram(db: DB) {
  const { nonce } = createClaim(db, "telegram", 1)
  claimOwner(db, { channel: "telegram", nonce, externalId: "42" }, 2)
}

function outboxTexts(db: DB) {
  return db.select().from(outbox).orderBy(sql`rowid`).all().map((r) => r.payload.text)
}

const lastText = (db: DB) => outboxTexts(db).at(-1)!

type Extra = { registry?: Record<string, import("../connectors/define").AnyRuntime>; rows?: Record<string, "tools" | "skill" | "memory" | "model" | "channel">; models?: Record<string, string[] | Error> }

// One model connector per key of `models` (default: cliproxy), each answering `listModels`.
async function setup(steps: Parameters<typeof scriptedModel>[0] = [], extra: Extra = {}) {
  const db = memoryDb()
  ownTelegram(db)
  const offered = extra.models ?? { cliproxy: ["m"] }
  const links = Object.keys(offered).map((connectorId) => ({ connectorId, model: "m" }))
  setSetting(db, "modelRoles", { default: links, fast: links, vision: links })
  const m = scriptedModel(steps)
  const registry: Extra["registry"] = { telegram: channelRuntime("telegram", fakeChannel("telegram")), ...(extra.registry ?? {}) }
  const rows: Extra["rows"] = { telegram: "channel", ...(extra.rows ?? {}) }
  for (const [id, list] of Object.entries(offered)) {
    registry[id] = modelRuntime(id, () => m.model, {
      action: async () => {
        if (list instanceof Error) throw list
        return { models: list }
      },
    })
    rows[id] = "model"
  }
  const h = await harness({ db, registry, rows })
  const send = async (text: string, more: Parameters<typeof tgMessage>[1] = {}) => {
    h.clock.now += 1000
    const r = await h.engine.inbound(tgMessage(text, more))
    await h.engine.idle()
    return r!
  }
  return { ...h, m, send }
}

describe("parseCommand", () => {
  it("knows the built-ins, with a bot suffix and in any case", () => {
    expect(parseCommand("/help")).toEqual({ name: "help", args: [] })
    expect(parseCommand("/Tools@palui_bot alpha  beta")).toEqual({ name: "tools", args: ["alpha", "beta"] })
    expect(parseCommand("/beres")).toBeNull()
    expect(parseCommand("help")).toBeNull()
  })
})

describe("/help", () => {
  it("lists every built-in command and works with the model off", async () => {
    const h = await setup([], { models: {} })
    await h.send("/help")
    const text = lastText(h.db)
    for (const c of ["/help", "/status", "/usage", "/new", "/compact", "/stop", "/btw", "/model", "/models", "/tools", "/connectors", "/agent", "/id", "/report"]) expect(text).toContain(`• ${c}`)
    expect(h.db.select().from(runs).all()).toHaveLength(0)
  })
})

describe("/id", () => {
  it("shows the chat, session and message ids, and the ids of a quoted message", async () => {
    const h = await setup([say("halo juga")])
    const first = await h.send("halo palui", { channelMsgId: "wa-1" })
    const chat = h.db.select().from(chats).get()!
    const mine = await h.send("/id")
    expect(lastText(h.db)).toContain(`Chat: \`${chat.id}\` (telegram, 42)`)
    expect(lastText(h.db)).toContain(`Sesi: \`${chat.sessionId}\``)
    expect(lastText(h.db)).toContain(`Pesan ini: \`${mine.messageId}\``)
    expect(lastText(h.db)).toContain("Quote sebuah pesan")

    await h.send("/id", { replyToChannelMsgId: "wa-1" })
    const run = h.db.select().from(runs).get()!
    expect(lastText(h.db)).toContain(`• Pesan: \`${first.messageId}\` (user)`)
    expect(lastText(h.db)).toContain(`• Run: \`${run.id}\``)
    expect(lastText(h.db)).toContain(`• Sesi: \`${chat.sessionId}\``)
  })

  it("finds a quoted reply or command reply through the outbox", async () => {
    const h = await setup([say("halo juga")])
    await h.send("halo palui")
    await h.send("/status")
    const [reply, status] = h.db.select().from(outbox).orderBy(sql`rowid`).all()
    h.db.update(outbox).set({ channelMsgId: "out-1" }).where(eq(outbox.id, reply!.id)).run()
    h.db.update(outbox).set({ channelMsgId: "out-2" }).where(eq(outbox.id, status!.id)).run()
    const run = h.db.select().from(runs).get()!
    const answer = h.db.select().from(messages).where(eq(messages.role, "assistant")).get()!

    await h.send("/id", { replyToChannelMsgId: "out-1" })
    expect(lastText(h.db)).toContain(`• Outbox: \`${reply!.id}\` (reply:${run.id})`)
    expect(lastText(h.db)).toContain(`• Pesan: \`${answer.id}\` (assistant)`)
    await h.send("/id", { replyToChannelMsgId: "out-2" })
    expect(lastText(h.db)).toContain(`• Outbox: \`${status!.id}\` (system)`)
    await h.send("/id", { replyToChannelMsgId: "nope" })
    expect(lastText(h.db)).toContain("Gak ketemu di database")
  })
})

describe("/usage", () => {
  it("breaks today's runs down by model and agent", async () => {
    const h = await setup([say("halo juga")])
    await h.send("halo palui")
    await h.send("/usage")
    const text = lastText(h.db)
    expect(text).toContain("Run: 1 (jam ini 1 / 60)")
    expect(text).toMatch(/• cliproxy\/m: 1 run, input \d+, output \d+/)
    expect(text).toMatch(/• Palui: 1 run, \d+ \/ tanpa batas/)
    expect(text).toContain("Chat ini: 1 run")
    expect(text).toContain("7 hari terakhir: 1 run")
  })
})

describe("/tools", () => {
  it("counts tools per connector by class and lists one connector's tools", async () => {
    const alpha = toolsRuntime("alpha", () => ({
      baca: { tool: fnTool("read it", () => "ok"), class: "read", title: "Baca data" },
      hapus: { tool: fnTool("delete it", () => "ok"), class: "destructive" },
    }))
    const h = await setup([], { registry: { alpha }, rows: { alpha: "tools" } })
    await h.send("/tools")
    expect(lastText(h.db)).toContain("Tool buat Palui di chat ini: 2")
    expect(lastText(h.db)).toContain("• alpha (2): read 1, destructive 1")
    await h.send("/tools alpha")
    expect(lastText(h.db)).toBe("alpha (2 tool):\n• baca (read): Baca data\n• hapus (destructive)")
    await h.send("/tools beta")
    expect(lastText(h.db)).toContain("Gak ada tool dari `beta`")
  })
})

describe("/models and /model", () => {
  const models = { cliproxy: ["claude-opus-4-7", "claude-sonnet-4-6", "m"], gateway: ["claude-sonnet-4-6", "gpt-5"] }

  it("/models lists what each model connector offers, filtered by a word or a connector", async () => {
    const h = await setup([], { models })
    await h.send("/models")
    expect(lastText(h.db)).toContain("cliproxy (3 model):\n• claude-opus-4-7\n• claude-sonnet-4-6\n• m (dipakai: default, fast, vision)")
    expect(lastText(h.db)).toContain("gateway (2 model):")
    await h.send("/models gpt")
    expect(lastText(h.db)).toBe("gateway (1 model):\n• gpt-5\n\nGanti model: /model <nama>.")
    await h.send("/models gateway")
    expect(lastText(h.db)).not.toContain("cliproxy (")
    await h.send("/models llama")
    expect(lastText(h.db)).toBe("Gak ada model yang cocok sama `llama`.")
  })

  it("/model shows the chains and puts a model first on every connector that offers it", async () => {
    const h = await setup([], { models })
    await h.send("/model")
    expect(lastText(h.db)).toContain("• default: cliproxy/m → gateway/m")
    await h.send("/model sonnet")
    expect(getSetting(h.db, "modelRoles").default).toEqual([
      { connectorId: "cliproxy", model: "claude-sonnet-4-6" },
      { connectorId: "gateway", model: "claude-sonnet-4-6" },
      { connectorId: "cliproxy", model: "m" },
      { connectorId: "gateway", model: "m" },
    ])
    expect(lastText(h.db)).toContain("Oke, model default sekarang cliproxy/claude-sonnet-4-6.")
    expect(lastText(h.db)).toContain("Pesan bergambar tetap pakai cliproxy/m.")
    expect(listEvents(h.db, { limit: 50 }).some((e) => e.kind === "settings.update" && e.message.includes("default model set to cliproxy/claude-sonnet-4-6"))).toBe(true)
    // The other roles are untouched, and asking again changes nothing.
    expect(getSetting(h.db, "modelRoles").fast).toEqual([{ connectorId: "cliproxy", model: "m" }, { connectorId: "gateway", model: "m" }])
    await h.send("/model claude-sonnet-4-6")
    expect(lastText(h.db)).toBe("Model default udah cliproxy/claude-sonnet-4-6.")
  })

  it("/model takes a role and a connector, and refuses a name that is unknown or matches several", async () => {
    const h = await setup([], { models })
    await h.send("/model fast gateway/gpt-5")
    expect(getSetting(h.db, "modelRoles").fast[0]).toEqual({ connectorId: "gateway", model: "gpt-5" })
    expect(getSetting(h.db, "modelRoles").default[0]).toEqual({ connectorId: "cliproxy", model: "m" })
    await h.send("/model fast")
    expect(lastText(h.db)).toContain("Model sekarang:")
    await h.send("/model claude")
    expect(lastText(h.db)).toContain("Ada 2 model yang cocok sama `claude`")
    await h.send("/model llama")
    expect(lastText(h.db)).toContain("Gak ada model yang cocok sama `llama`.")
    expect(getSetting(h.db, "modelRoles").default[0]).toEqual({ connectorId: "cliproxy", model: "m" })
  })

  it("says so when a connector's model list can't be fetched", async () => {
    const h = await setup([], { models: { cliproxy: new Error("503") } })
    await h.send("/models")
    expect(lastText(h.db)).toContain("cliproxy: daftar modelnya gagal diambil.")
    await h.send("/model opus")
    expect(lastText(h.db)).toContain("Daftar dari cliproxy gagal diambil.")
  })
})

describe("/btw", () => {
  it("answers with the session as context and leaves the session untouched", async () => {
    const h = await setup([say("halo juga"), say("Makassar, WITA.")])
    await h.send("halo palui, aku lagi di Banjarmasin")
    const before = h.db.select().from(messages).all().length
    const cmd = await h.send("/btw zona waktu sini apa?")
    expect(lastText(h.db)).toBe("Makassar, WITA.")
    expect(h.db.select().from(outbox).orderBy(sql`rowid`).all().at(-1)!.payload.format).toBe("markdown")
    const prompt = promptText(h.m.prompts.at(-1))
    expect(prompt).toContain("Owner: halo palui, aku lagi di Banjarmasin")
    expect(prompt).toContain("Side question: zona waktu sini apa?")
    // Only the command message itself was stored, with no run behind it.
    const rows = h.db.select().from(messages).all()
    expect(rows).toHaveLength(before + 1)
    expect(rows.find((r) => r.id === cmd.messageId)).toMatchObject({ status: "handled", runId: null })
    expect(h.db.select().from(runs).all()).toHaveLength(1)
  })

  it("needs a question and a model", async () => {
    const h = await setup([], { models: {} })
    await h.send("/btw")
    expect(lastText(h.db)).toContain("Formatnya: /btw <pertanyaan>")
    await h.send("/btw jam berapa?")
    expect(lastText(h.db)).toBe(MODEL_OFF_TEXT)
  })

  it("tells the owner when the model call fails", async () => {
    const h = await setup([new Error("boom"), new Error("boom")])
    await h.send("/btw halo?")
    expect(lastText(h.db)).toBe("/btw gagal jalan. Coba lagi ya.")
  })
})

describe("/compact", () => {
  it("folds all but the last six messages into the summary", async () => {
    const h = await setup([say("satu"), say("dua"), say("tiga"), say("empat"), say("RINGKASAN: empat sapaan")])
    for (const t of ["a", "b", "c", "d"]) await h.send(`halo ${t}`)
    await h.send("/compact")
    expect(lastText(h.db)).toBe("Oke, 2 pesan lama udah aku ringkas. 6 pesan terakhir tetap utuh.")
    const ses = h.db.select().from(sessions).get()!
    expect(ses.summary).toBe("RINGKASAN: empat sapaan")
    expect(ses.summarizedUntil).not.toBeNull()
    // Nothing left to fold right after.
    await h.send("/compact")
    expect(lastText(h.db)).toBe("Obrolannya masih pendek, belum ada yang perlu diringkas.")
  })
})
