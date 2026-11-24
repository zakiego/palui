import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "proactive",
  kind: "skill",
  name: "Proactive",
  icon: "radar",
  summary: "Tells you what matters without being asked.",
  description:
    "An agent with proactive on runs a prompt you write every 30 minutes and messages you first when something matters: when to leave, a meeting about to start, a chat waiting for your reply, plus a morning and an evening summary. The prompt mentions the read tools it may use, like @gcal__get_calendar, so any connector can be a source. Set it up per agent on the agent's Proactive tab, or from chat with these tools.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the chat tools. Proactive keeps running; turn it off on the agent's Proactive tab.",
  docs: {
    commands: ["nyalain proactive di chat ini", "jam tenang jadi 23:00", "jangan ingetin grup kantor lagi", "inget ya, Pak Budi selalu bales telat", "proactive hari ini ngabarin apa aja?"],
    notes: [
      "Proactive is off for every agent until you turn it on. Its messages only go to your own DM or the web chat.",
      "The prompt can only mention read tools the agent may use. A mention that doesn't resolve fails the run, and three failed runs in a row pause proactive.",
    ],
  },
})
