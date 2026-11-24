import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "proactive",
  kind: "skill",
  name: "Proactive",
  icon: "radar",
  summary: "Tells you what matters without being asked.",
  description:
    "An agent with proactive on checks your calendar and WhatsApp every 30 minutes and messages you first: when to leave, a meeting about to start, a changed event, a chat waiting for your reply, plus a morning and an evening summary. Set it up per agent on the agent's Proactive tab, or from chat with these tools.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the chat tools. Proactive keeps running; turn it off on the agent's Proactive tab.",
  docs: {
    commands: ["nyalain proactive di chat ini", "jam tenang jadi 23:00", "jangan ingetin grup kantor lagi", "proactive hari ini ngabarin apa aja?"],
    notes: [
      "Proactive is off for every agent until you turn it on. Its messages only go to your own DM or the web chat.",
      "WhatsApp needs a wa-hub MCP server whose list_chats returns structured content, classified read on the Connectors page.",
    ],
  },
})
