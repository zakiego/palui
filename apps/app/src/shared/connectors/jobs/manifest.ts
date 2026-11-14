import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "jobs",
  kind: "skill",
  name: "Scheduled jobs",
  icon: "calendar-clock",
  summary: "Prompts that run on a schedule.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, Palui can't list jobs. Jobs keep running.",
  docs: {
    notes: [
      "The heartbeat runs every 30 minutes from 08:00 to 22:00 and sends nothing when nothing matters.",
      "Each job has a daily token cap. When it's used up, runs are skipped until the next day.",
    ],
  },
})
