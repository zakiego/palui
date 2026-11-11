import { z } from "zod"
import { defineManifest } from "@palui/core/saklar/define"

export default defineManifest({
  id: "reminders",
  kind: "skill",
  name: "Reminders",
  icon: "alarm-clock",
  summary: "Set, list, move and snooze reminders from chat.",
  description:
    "Palui reads phrases like \"ingetin bayar listrik besok jam 9\" or \"remind me every Monday 8am\" and echoes the time back so a wrong read shows right away. Reminders fire on time with Done and Snooze buttons (on WhatsApp, reply \"done\" or \"tunda 1j\" quoting the reminder). Plain reminders never call the model.",
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
