import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "outbound",
  kind: "skill",
  name: "Messages to contacts",
  icon: "send",
  summary: "Message saved contacts for you, now, later or on a repeat, with approval.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the send tools. Scheduled messages still go out; use the kill switch on the Outbound page to stop everything.",
  docs: {
    commands: ["chat <nama> sekarang, bilang <pesan>", "jam 8 malam chat <nama>, <pesan>", "tiap Jumat jam 11 kabarin <nama> <pesan>"],
    notes: [
      "Only saved contacts. A new number is saved when you approve the first message to it.",
      "Every message asks first, unless the contact is set to Always allow on the Outbound page and you named the contact yourself.",
      "Messages sent now wait 15 seconds so you can cancel them.",
      "WhatsApp messages go out from the number Palui is linked to.",
    ],
  },
})
