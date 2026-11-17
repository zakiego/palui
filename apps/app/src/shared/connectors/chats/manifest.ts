import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "chats",
  kind: "skill",
  name: "Chat history",
  icon: "messages-square",
  summary: "Find another chat and read its recent messages.",
  description:
    "Lets an agent look up the other chats it answers in (WhatsApp, Telegram, web) and read what was said there, so you can ask for a summary of a group or what you talked about yesterday. It's read-only. Palui, in your own DM or the web chat, can also read the chats other agents answer in, but no other agent can read Palui's chats or another agent's. It reads what was stored: your messages, the agent's replies, quoted text, webhook events, and in a group with an agent, what everyone else said.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the chat history tools. Nothing is deleted.",
  docs: {
    commands: ["cari chat grup keluarga terus rangkum obrolan terakhirnya", "apa yang dibilang di chat Telegram kemarin?"],
    notes: [
      "The tools are offered only in a private chat with Palui (a DM or the web chat), never in a group, so another chat's messages can't land in front of other people.",
      "Each agent finds and reads its own chats. Palui also lists and reads the chats other agents answer in (list_agent_chats, read_agent_chat), only on your own messages or jobs in your DM or the web chat. No other agent reads Palui's chats or another agent's.",
      "Reading another chat taints the turn: any write tool after it asks you first. After reading another agent's chat, even a tool you set to allow asks.",
      "Webhook turns never get them. Scheduled jobs do, when the job's target chat is a DM or the web chat.",
    ],
  },
})
