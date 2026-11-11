// WhatsApp channel manifest (PLAN 4.1, 5.1). Imported by the app and the runtime,
// so it only imports zod and the connector contract.
import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

const phone = z.string().regex(/^\+?[\d\s().-]{8,24}$/, "Enter a phone number like 0812 3456 789 or +62 812 3456 789")

export default defineManifest({
  id: "whatsapp",
  kind: "channel",
  name: "WhatsApp",
  icon: "message-circle",
  summary: "Chat with Palui from WhatsApp, as a linked device.",
  config: z.object({
    // The owner's own number. Messages from it (and its learned LID) are accepted.
    ownerPhone: phone.optional(),
    // The number being linked (the account Palui runs as). Pairing codes are
    // requested for this number, so the code is typed on that phone.
    pairPhone: phone.optional(),
    // Learned on connect: the digits of the linked account.
    linkedNumber: z.string().optional(),
  }),
  secrets: {},
  setup: [
    { action: "ownerPhone", label: "Your phone number", help: "Palui only answers DMs from this number." },
    {
      action: "pairWhatsApp",
      label: "Link the WhatsApp number",
    },
  ],
  removeWarning: "This unlinks the WhatsApp device. You'll need to pair again.",
  unlinkOnRemove: true,
  // The runtime reconnects with its own backoff, and its error states (unlinked,
  // pairing expired) are final on purpose: restarting would push new pairing codes.
  restartOnError: false,
  docs: {
    notes: [
      "DM only in v1. Groups, status updates and newsletters are ignored.",
      "An ⏳ reaction shows while Palui is thinking and clears when it replies.",
      "Buttons become a reply hint, for example \"Balas: ya / batal\".",
    ],
  },
})
