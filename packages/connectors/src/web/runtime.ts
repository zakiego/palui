// The web chat channel: replies go to the turn bus, which the control API
// streams to the dashboard over SSE. Nothing to connect, so it's always up.
import { newId } from "@palui/db"
import { turnBus, type TurnBus } from "@palui/core/chat/bus"
import { findChat } from "@palui/core/chats"
import { defineRuntime } from "@palui/core/saklar/define"
import manifest from "./manifest"

export function webRuntime(bus: TurnBus = turnBus) {
  return defineRuntime(manifest, {
    test: async () => ({ ok: true, detail: "Web chat is built in." }),
    start: async (ctx) => ({
      stop: async () => {},
      channel: {
        channel: "web",
        reachable: () => true,
        async send(chatExternalId, payload) {
          const chat = findChat(ctx.db, "web", chatExternalId)
          const messageId = newId("webmsg")
          bus.publish({ type: "message", chatId: chat?.id ?? chatExternalId, messageId, role: "assistant", text: payload.text })
          return { channelMsgId: messageId }
        },
      },
    }),
  })
}

export default webRuntime()
