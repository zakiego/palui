import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "watches",
  kind: "skill",
  name: "Watches",
  icon: "eye",
  summary: "Keep an eye on something and tell the chat once it happens.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the watch tools. Watches already set keep checking.",
  docs: {
    commands: ["kabarin kalau <sesuatu> udah keluar", "tell me when <something> happens"],
    notes: [
      "A watch checks with the agent's read tools (web search, fetch page, any read MCP tool) every 15 to 1440 minutes, 30 by default.",
      "It can start later (\"mulai jam 16.00 besok\"): nothing is checked before its start time, and the first check runs right at it.",
      "It posts once, with the answer and its source, and stops. It ends after 7 days by default, 14 days at most, with a short note if nothing was found.",
      "Anyone the agent answers in a chat can set one there. A chat has at most 10 running, and each watch may spend up to 1,000,000 tokens.",
    ],
  },
})
