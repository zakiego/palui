import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "web",
  kind: "channel",
  name: "Web chat",
  icon: "message-square",
  summary: "Chat from the dashboard. Always on.",
  config: z.object({}),
  secrets: {},
  setup: [],
  builtin: true,
})
