import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "flows",
  kind: "skill",
  name: "Flows",
  icon: "workflow",
  summary: "Automations Palui builds from chat: triggers plus steps that read tools, decide, ask and send.",
  description:
    "A flow is a small automation: one or more triggers (a schedule, a webhook, a chat command, a value that changes, another flow's event, or a manual run) and a list of steps (call a tool, branch on a condition, set a variable, send a message, ask a check-in, wait, run a prompt, or repeat for each item in a list). Steps pass data with templates and decide with simple conditions, so nothing calls the model unless a step asks for it, and every run logs what each step saw and did. Ask in chat (\"tiap pagi kirim jadwal sholat, cuaca sama pengingat hari ini\") and Palui builds it, dry-runs it, and shows you the result before saving.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the flow tools. Flows already saved keep running; pause one from chat to stop it.",
  docs: {
    commands: ["tiap pagi jam 6 kirim jadwal sholat, cuaca, sama pengingat hari ini", "kalau PR di palui di-merge, cek CI-nya terus kabari aku", "kenapa flow briefing tadi pagi nggak jalan?"],
    notes: [
      "Tool and condition steps run without the model. Only a prompt step calls it, with fetched data in a separate block it's told never to follow.",
      "Write tools are approved once per flow version. Tainted arguments, a changed tool class, or a tool set to ask every run still ask each time. Destructive tools never run in a flow.",
      "Every run is logged with each step's inputs, outputs and the branch it took, including runs that were skipped and why.",
    ],
  },
})
