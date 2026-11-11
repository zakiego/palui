// Telegram channel manifest (PLAN 4.1, 5.1). Imported by the app and the runtime,
// so it only imports zod and the connector contract.
import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "telegram",
  kind: "channel",
  name: "Telegram",
  icon: "send",
  summary: "Chat with Palui from a Telegram bot.",
  description:
    "Long polling with grammY. Paste a bot token from @BotFather, then open the claim link and press Start to become the owner. Replies are sent as HTML, split at 4096 characters, with inline buttons for reminders, approvals and /connectors.",
  config: z.object({
    // Learned at runtime from getMe, so the claim link works before polling starts.
    botUsername: z.string().optional(),
  }),
  secrets: { botToken: z.string().regex(/^\d+:[\w-]{30,}$/, "That doesn't look like a bot token from @BotFather (123456789:AA...).") },
  setup: [
    { field: "botToken", label: "Bot token", help: "From @BotFather, /newbot", secret: true, placeholder: "123456789:AA..." },
    { action: "claimOwner", label: "Open the link and press Start" },
  ],
  removeWarning: "This deletes the bot token from Palui. The bot stops answering until you connect it again. The bot itself stays in @BotFather.",
  docs: {
    notes: [
      "Dev and prod need different bot tokens: two getUpdates consumers on one token get HTTP 409.",
      "Photos, voice notes and documents up to 1 MB are passed to the agent.",
    ],
  },
})
