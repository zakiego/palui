import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "notes",
  kind: "skill",
  name: "Instructions & preferences",
  icon: "notebook-pen",
  summary: "Standing instructions and your defaults, remembered across chats.",
  description:
    "Palui saves short standing instructions (\"for prayer times, use the jadwal-sholat server\") and key-value preferences (default_city, timezone, home coordinates). Both go into every turn's system prompt. Saving or deleting an instruction always asks you first; preferences ask when the turn read something untrusted. You can also edit both on the Notes page.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the tools. Saved instructions and preferences still go into the prompt; turn one off on the Notes page.",
  docs: {
    commands: ["inget ya, kota default aku Banjarmasin", "simpan instruksi: kalau tanya jadwal sholat, pakai server jadwal-sholat"],
    notes: [
      "Up to 20 instructions of 1000 characters, and 50 preferences of 300 characters.",
      "Preference keys are snake_case, like default_city or home_lat.",
      "Instructions change how Palui behaves in every chat, so saving or deleting one is destructive and always asks first. Scheduled jobs can't do it.",
    ],
  },
})
