import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "chats",
  kind: "skill",
  name: "Chat history",
  icon: "messages-square",
  summary: "Find another chat and read its recent messages.",
  description:
    "Lets an agent look up the other chats it answers in (WhatsApp, Telegram, web) and read what was said there, so you can ask for a summary of a group or what you talked about yesterday. It's read-only, and an agent never sees chats another agent answers in. Palui only sees what it stored: your messages, its own replies, quoted text and webhook events. Messages other people send in a chat aren't stored, so they aren't readable either.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the chat history tools. Nothing is deleted.",
  docs: {
    commands: ["cari chat grup keluarga terus rangkum obrolan terakhirnya", "apa yang dibilang di chat Telegram kemarin?"],
    notes: [
      "The tools are offered only in a private chat with Palui (a DM or the web chat), never in a group, so another chat's messages can't land in front of other people.",
      "Each agent only finds and reads its own chats. Palui can't read a group another agent answers in, and that agent can't read Palui's chats.",
      "Reading another chat taints the turn: any write tool after it asks you first.",
      "Webhook turns never get them. Scheduled jobs do, when the job's target chat is a DM or the web chat.",
    ],
  },
})
