import { z } from "zod"
import { defineManifest } from "@palui/core/saklar/define"

export default defineManifest({
  id: "jobs",
  kind: "skill",
  name: "Scheduled jobs",
  icon: "calendar-clock",
  summary: "Prompts that run on a schedule, like the morning briefing and the heartbeat.",
  description:
    "A job is a prompt that runs as a turn on a repeat rule, in a chat you pick. Jobs can't use destructive tools, and write tools ask you first. Create and edit jobs in the dashboard; in chat Palui can list them.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the job list tool. Jobs keep running; pause a job itself on the Jobs page.",
  docs: {
    notes: [
      "The heartbeat runs every 30 minutes from 08:00 to 22:00 and sends nothing when nothing matters.",
      "Each job has a daily token cap. When it's used up, runs are skipped until the next day.",
    ],
  },
})
