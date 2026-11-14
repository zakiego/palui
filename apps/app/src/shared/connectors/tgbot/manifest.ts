// An agent's own Telegram bot (PLAN 19.6). Same runtime as the main Telegram
// connector; several instances allowed. Link it to an agent on the Agents page.
// Imported by the app and the runtime, so it only imports zod and the contract.
import { defineManifest } from "../define"
import telegram from "../telegram/manifest"

export default defineManifest({
  ...telegram,
  id: "tgbot",
  name: "Agent bot (Telegram)",
  icon: "telegram",
  summary: "A Telegram bot of its own for one agent.",
  description:
    "Give an agent its own Telegram bot so people can add it to a group and @mention it by name. Create the bot with @BotFather, paste its token here, then pick it on the agent's page. Chats with this bot belong to that agent.",
  multiple: true,
  presets: [{ id: "agent", label: "Agent bot", config: {} }],
  setup: [{ field: "botToken", label: "Bot token", help: "Send this to @BotFather, then paste the token.", command: "/newbot", secret: true, placeholder: "123456789:AA..." }],
  docs: {
    notes: [
      "Each agent bot needs its own token from @BotFather. The owner is the same Telegram account as on the main bot.",
      "Leave privacy mode on: in groups the bot then only sees commands, mentions and replies to it.",
    ],
  },
})
