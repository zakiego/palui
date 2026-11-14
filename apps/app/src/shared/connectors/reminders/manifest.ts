import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "reminders",
  kind: "skill",
  name: "Reminders",
  icon: "alarm-clock",
  summary: "Set, list, move and snooze reminders from chat.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the reminder tools. Reminders already set still fire.",
  docs: {
    commands: ["ingetin <apa> <kapan>", "remind me <when> to <what>"],
    notes: [
      "Times are wall-clock times in the reminder's own zone (default from Settings, Asia/Makassar). A daily 08:00 reminder stays at 08:00 across DST changes.",
      "Repeats use RRULE. A monthly reminder on the 31st skips months that don't have one.",
      "If Palui was down when a reminder was due, it fires once on start with \"(telat, harusnya 09.00)\".",
    ],
  },
})
