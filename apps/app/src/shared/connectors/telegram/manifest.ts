// Telegram channel manifest (PLAN 4.1, 5.1). Imported by the app and the runtime,
// so it only imports zod and the connector contract.
import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "telegram",
  kind: "channel",
  name: "Telegram",
  icon: "telegram",
  summary: "Chat from a Telegram bot.",
  config: z.object({
    // Learned at runtime from getMe, so the claim link works before polling starts.
    botUsername: z.string().optional(),
  }),
  secrets: { botToken: z.string().regex(/^\d+:[\w-]{30,}$/, "That doesn't look like a bot token from @BotFather (123456789:AA...).") },
  setup: [
    { field: "botToken", label: "Bot token", help: "Send this to @BotFather, then paste the token.", command: "/newbot", secret: true, placeholder: "123456789:AA..." },
    { action: "claimOwner", label: "Open the link and press Start" },
  ],
  removeWarning: "This deletes the bot token. The bot stays in @BotFather.",
  docs: {
    notes: [
      "Dev and prod need different bot tokens: two getUpdates consumers on one token get HTTP 409.",
      "Photos, voice notes and documents up to 1 MB are passed to the agent.",
    ],
  },
})
