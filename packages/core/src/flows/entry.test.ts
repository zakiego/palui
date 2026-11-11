import { describe, expect, it } from "vitest"
import { hooks } from "@palui/db"
import { flowCommands } from "../chat/commands-info"
import { route } from "../chat/router"
import { getChat } from "../chats"
import { createHook } from "../hooks/index"
import { flowActions } from "./actions"
import { deliveryKey, ingestFlowHook } from "./entry"
import { flowHarness, runsOf, sentTexts } from "./test-harness"

const KEY = "0123456789abcdef0123456789abcdef"

describe("chat triggers", () => {
  const BERES = {
    name: "Beres",
    triggers: [
      { id: "cmd", kind: "chat", command: "/beres", start: "ok" },
      { id: "say", kind: "chat", phrases: ["udah bayar listrik"], start: "ok" },
    ],
    steps: [{ id: "ok", kind: "message", text: "Dicatat: {{trigger.args | default(\"-\")}}" }],
  }

  it("matches the owner's command or phrase before the model and never reaches a turn", async () => {
    const h = flowHarness()
    const { flow } = h.create(BERES)
    const chat = getChat(h.db, h.chatId)!
    const r = await route({ db: h.db, chat, content: { text: "/beres listrik november" }, text: "/beres listrik november", messageId: "msg_1", flowActions, fromOwner: true, now: h.now() })
    expect(r).toEqual({ kind: "handled" })
    const p = await route({ db: h.db, chat, content: { text: "Udah bayar listrik!" }, text: "Udah bayar listrik!", messageId: "msg_2", flowActions, fromOwner: true, now: h.now() })
    expect(p).toEqual({ kind: "handled" })
    // A guest saying it, or anything else, is just a message.
    expect(await route({ db: h.db, chat, content: { text: "/beres" }, text: "/beres", messageId: "msg_3", flowActions, fromOwner: false, now: h.now() })).toEqual({ kind: "turn" })
    expect(await route({ db: h.db, chat, content: { text: "beres?" }, text: "beres?", messageId: "msg_4", flowActions, fromOwner: true, now: h.now() })).toEqual({ kind: "turn" })
    // Built-in commands win (they're refused at save anyway).
    expect(await route({ db: h.db, chat, content: { text: "/status" }, text: "/status", messageId: "msg_5", flowActions, fromOwner: true, now: h.now() })).toMatchObject({ kind: "command" })

    await h.run(h.now())
    expect(runsOf(h.db, flow.id).map((r) => r.runKey).sort()).toEqual(["chat:msg_1", "chat:msg_2"])
    expect(sentTexts(h.db).map((s) => s.text).sort()).toEqual(["Dicatat: -", "Dicatat: listrik november"])
  })

  it("lists a chat's flow commands for /help, phrases and other chats left out", () => {
    const h = flowHarness()
    h.create(BERES)
    expect(flowCommands(h.db, h.chatId)).toEqual([{ command: "/beres", flow: "Beres" }])
    expect(flowCommands(h.db, h.otherChatId)).toEqual([])
  })

  it("only matches in the flow's own chat", async () => {
    const h = flowHarness()
    h.create(BERES)
    expect(await flowActions.fromChat(h.db, { text: "/beres", chatId: h.otherChatId, messageId: "m", fromOwner: true }, h.now())).toBe(false)
  })
})

describe("webhook triggers", () => {
  it("keys runs by delivery id or body hash, and logs a hook nobody listens to", () => {
    const h = flowHarness()
    const { hook } = createHook(h.db, { name: "Kosong", mode: "flow", chatId: h.chatId }, KEY, h.now())
    const row = h.db.select().from(hooks).get()!
    expect(ingestFlowHook(h.db, row, "{}", {}, h.now())).toEqual({ started: 0, skipped: 0, listeners: 0 })
    expect(hook.mode).toBe("flow")
    expect(deliveryKey("a", { "x-palui-delivery": "abc" })).toBe("hook:abc")
    expect(deliveryKey("a", {})).toBe(deliveryKey("a", { "x-palui-delivery": "bad id with spaces" }))
  })
})
