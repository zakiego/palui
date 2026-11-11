import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { createTurnBus } from "@palui/core/chat/bus"
import { ensureWebChat } from "@palui/core/chats"
import { silentLogger } from "@palui/core/log"
import type { TurnEvent } from "@palui/core/control"
import { webRuntime } from "./runtime"

describe("web channel", () => {
  it("publishes replies on the turn bus and acks with a message id", async () => {
    const db = memoryDb()
    const chat = ensureWebChat(db)
    const bus = createTurnBus()
    const seen: TurnEvent[] = []
    bus.subscribe((e) => seen.push(e))
    const h = await webRuntime(bus).start({ id: "web", type: "web", config: {}, secrets: {}, trust: "own", db, log: silentLogger, dataDir: "/tmp", signal: new AbortController().signal, services: {} as never, setStatus() {}, event() {}, patchConfig() {} })
    expect(h.channel!.reachable()).toBe(true)
    const r = await h.channel!.send("web", { text: "halo" })
    expect(seen).toEqual([{ type: "message", chatId: chat.id, messageId: r.channelMsgId, role: "assistant", text: "halo" }])
  })
})
