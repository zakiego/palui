import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "attachments",
  kind: "skill",
  name: "Attachments",
  icon: "paperclip",
  summary: "Keep files you send in chat, like a receipt.",
  description:
    "When you send a photo or a document that's worth keeping, Palui saves it with a title and a short note. Nothing is saved automatically: Palui only keeps what matters, or what you ask it to. Saved files are encrypted with MASTER_KEY inside the database and can only be downloaded from the dashboard after you sign in.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the attachment tools. Files already saved stay on the Attachments page.",
  docs: {
    commands: ["simpan KTP ini", "simpen struk ini ya", "file tiket pesawat yang kemarin mana?"],
    notes: [
      "Only images (JPEG, PNG, WebP, GIF, HEIC), PDF, plain text, CSV and Office documents, up to 10 MB each. The channel's own limits still apply first (WhatsApp and Telegram documents are capped at 1 MB today).",
      "Files from the message being answered can be saved, and so can files sent in the same chat in the last 24 hours (the 10 most recent), so a receipt still gets saved after you confirm a preview. Never a URL or another chat's file.",
      "Saving the same file twice in a chat keeps one copy.",
    ],
  },
})
