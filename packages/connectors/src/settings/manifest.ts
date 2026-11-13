import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "settings",
  kind: "skill",
  name: "Settings",
  icon: "settings",
  summary: "Read and change settings from chat.",
  description:
    "Palui can look up its own setup (version, timezone, language, model roles, budgets, fallback channel, today's token use and which connectors are on) and change the settings from the Settings page when you ask in chat. Every change asks you first and shows the new values. Secrets and API keys are never shown or changed here.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the tools. Settings keep their values; change them on the Settings page.",
  docs: {
    commands: ["timezone kamu sekarang apa?", "ganti zona waktu ke Asia/Jakarta", "naikin batas token harian jadi 3 juta", "model fast pakai claude-haiku-4-5 aja"],
    notes: [
      "Changing a setting is destructive, so it always asks first and scheduled jobs, flows and hooks can't do it.",
      "Only Palui (the default agent) can change settings. Other agents can read them.",
      "It covers the Settings page keys: timezone, language, dashboard language, model roles, budgets and fallback channel.",
    ],
  },
})
