import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "routines",
  kind: "skill",
  name: "Routines",
  icon: "repeat",
  summary: "Automations Palui builds from chat: a schedule plus a message, a check-in or a prompt.",
  description:
    "A routine pairs WHEN with WHAT. When: fixed times, a repeat rule, or times read every day from any connector tool (an MCP server that returns a schedule, say). What: send a message, ask a check-in question with your own answer options and re-ask until you answer, or run a prompt. Each routine has its own zone, active days, re-ask interval, deadline, pause and optional daily or weekly recap. Ask in chat (\"ingetin minum obat jam 8 dan 20, tanya udah diminum belum\") and Palui sets it up; answer with the buttons, by replying to the question, or just say it in chat.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the routine tools. Routines already set keep firing; pause one from chat to stop it.",
  docs: {
    commands: ["ingetin minum obat jam 8 dan 20, tanya udah apa belum", "tiap Jumat jam 4 sore suruh aku review minggu ini", "jeda rutinitas olahraga sampai Senin"],
    notes: [
      "Tool sources only call tools classified read, server-side on a timer. Their results are read as data: only values that parse as times are used.",
      "When a tool source fails, Palui retries with backoff, keeps using the last times that worked, and tells you once a day.",
      "Each occurrence fires once, even across restarts. After downtime, a check-in past its deadline (or a message more than an hour late) is logged as skipped instead of sent.",
    ],
  },
})
