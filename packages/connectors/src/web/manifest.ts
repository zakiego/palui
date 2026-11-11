import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "web",
  kind: "channel",
  name: "Web chat",
  icon: "message-square",
  summary: "Chat with Palui from the dashboard. Always on.",
  description: "The dashboard's chat goes through the same router and turn as Telegram and WhatsApp, streamed, with tool calls visible. Handy for testing without a phone.",
  config: z.object({}),
  secrets: {},
  setup: [],
  builtin: true,
})
