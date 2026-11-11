import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export const RECALL_DEFAULT_URL = "http://host.docker.internal:4800"

export default defineManifest({
  id: "recall",
  kind: "memory",
  name: "recall",
  icon: "brain",
  summary: "Palui remembers what you tell it.",
  config: z.object({
    url: z.url({ protocol: /^https?$/ }).default(RECALL_DEFAULT_URL),
    container: z.string().trim().min(1).max(100).default("default"),
    autoRecall: z.boolean().default(true),
    autoSave: z.boolean().default(true),
  }),
  secrets: { apiKey: z.string().regex(/^rcl_[A-Za-z0-9_-]+$/, "recall keys start with rcl_") },
  setup: [
    { field: "url", label: "recall URL", placeholder: RECALL_DEFAULT_URL, help: "In Docker on the same host, keep the default." },
    { field: "apiKey", label: "API key", secret: true, placeholder: "rcl_...", help: "recall Settings > API keys." },
    { action: "pickContainer", label: "Pick a container" },
  ],
  trust: "own",
  pauseNote: "While paused, Palui doesn't recall or save memories.",
  removeWarning: "This removes the recall key from Palui. Your memories stay in recall.",
  docs: {
    notes: [
      "Tools: add_memory (write), search_memories, ask_memory, get_profile, list_memories (read), forget_memory (destructive).",
      "Auto-save uses source_id <session>:<range>, so saving the same slice twice is a no-op.",
    ],
  },
})
