import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "voice",
  kind: "skill",
  name: "Voice notes",
  icon: "mic",
  summary: "Send Palui a voice note and it reads the transcript.",
  description:
    "Transcribes inbound voice notes with ElevenLabs Scribe, then treats the transcript like a typed message. The Gemini-through-CLIProxyAPI fallback from the plan isn't available: CLIProxyAPI serves no Gemini models today, so ElevenLabs is the only engine.",
  config: z.object({
    model: z.string().trim().min(1).default("scribe_v1"),
    // ISO 639 code. "auto" (or empty) lets Scribe detect the language.
    language: z.string().trim().max(10).optional().default("id"),
  }),
  secrets: { elevenLabsKey: z.string().min(10) },
  setup: [
    { field: "elevenLabsKey", label: "ElevenLabs API key", secret: true, help: "elevenlabs.io, Developers, API keys. It needs Speech to Text access." },
    { field: "language", label: "Language", optional: true, placeholder: "id", help: "id for Indonesian, or auto to detect" },
  ],
  pauseNote: "While paused, voice notes are not transcribed.",
  docs: { notes: ["Fallback engine: none yet. CLIProxyAPI has no Gemini models, so Gemini audio isn't wired."] },
})
