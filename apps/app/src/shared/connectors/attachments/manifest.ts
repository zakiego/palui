import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "attachments",
  kind: "skill",
  name: "Attachments",
  icon: "paperclip",
  summary: "Keep files you send in chat, like a receipt.",
  description:
    "When you send a photo or a document that's worth keeping, Palui saves it with a title and a short note. By default nothing is saved automatically: Palui only keeps what matters, or what you ask it to. Turn on auto-save for a chat on the Chats page and every photo and document sent there is kept for as long as you pick. Saved files are encrypted with MASTER_KEY inside the database and can only be downloaded from the dashboard after you sign in.",
  builtin: true,
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "Pausing hides the attachment tools. Files already saved stay on the Attachments page.",
  docs: {
    commands: ["simpan KTP ini", "simpen struk ini ya", "file tiket pesawat yang kemarin mana?"],
    notes: [
      "Only images (JPEG, PNG, WebP, GIF, HEIC), PDF, plain text, CSV and Office documents, up to 10 MB each. The channel's own limits still apply first (WhatsApp and Telegram documents over 1 MB aren't read, though a chat with auto-save on still keeps them up to 10 MB).",
      "Files from the message being answered can be saved, and so can files sent in the same chat in the last 24 hours (the 10 most recent), so a receipt still gets saved after you confirm a preview. Never a URL or another chat's file.",
      "Saving the same file twice in a chat keeps one copy.",
      "Auto-save is off in every chat until you turn it on. It keeps photos and documents (not stickers or voice notes) from anyone the chat accepts, up to 500 MB per chat and 2 GB in all, and deletes them after 30 days unless you pick another retention.",
    ],
  },
})
