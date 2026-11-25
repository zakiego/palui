// WhatsApp inbox manifest: the owner's own number linked read-only, for
// proactive prompts (docs/rfc/proactive-prompt.md 10). Imported by the app and
// the runtime, so it only imports zod and the connector contract.
import { z } from "zod"
import { defineManifest } from "../define"

const phone = z.string().regex(/^\+?[\d\s().-]{8,24}$/, "Enter a phone number like 0812 3456 789 or +62 812 3456 789")

export default defineManifest({
  id: "wa-inbox",
  kind: "tools",
  name: "WhatsApp inbox",
  icon: "whatsapp",
  summary: "See which of your WhatsApp chats wait for a reply. It never sends anything.",
  multiple: true,
  trust: "external",
  config: z.object({
    // The number being linked: pairing codes are requested for it.
    pairPhone: phone.optional(),
    // Learned on connect: the digits of the linked account.
    linkedNumber: z.string().optional(),
  }),
  secrets: {},
  setup: [{ action: "pairWhatsApp", label: "Link your WhatsApp number" }],
  pauseNote: "While paused, nothing new is recorded, and the chats waiting stay as they were.",
  removeWarning: "This unlinks the device from your WhatsApp and forgets the chats it recorded. Your chats aren't touched.",
  unlinkOnRemove: true,
  restartOnError: false,
  docs: {
    notes: [
      "It links as one more device on your own number and never sends, reacts or marks anything read.",
      "Per chat it keeps the name, when the last message came in, when you last replied and a 200-character preview. Message bodies are never stored.",
      "It starts empty: only messages that arrive after linking count.",
    ],
  },
})
