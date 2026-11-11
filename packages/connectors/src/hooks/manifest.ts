import { z } from "zod"
import { defineManifest } from "@palui/core/saklar/define"

export default defineManifest({
  id: "hooks",
  kind: "skill",
  name: "Webhooks",
  icon: "webhook",
  summary: "Forward events from other apps to you",
  description:
    "Each hook gets its own URL (POST /api/hooks/<slug>) and HMAC secret. Senders send x-palui-timestamp (unix seconds) and sign <timestamp>.<raw body>: x-palui-signature: sha256=<hex HMAC-SHA256>. Old timestamps (over 5 minutes) and repeats are refused. Forward sends the event to you as text without the model; summarize lets the model write a short summary with zero tools. Hook payloads are untrusted.",
  builtin: true,
  trust: "external",
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, every hook URL answers 503 and senders should retry later.",
  docs: {
    commands: [
      "TS=$(date +%s); curl -X POST $URL -H \"x-palui-timestamp: $TS\" -H \"x-palui-signature: sha256=$(printf '%s.%s' \"$TS\" \"$BODY\" | openssl dgst -sha256 -hmac \"$SECRET\" -hex | cut -d' ' -f2)\" -d \"$BODY\"",
    ],
    notes: ["The secret is shown once when the hook is created or rotated.", "Bodies over 256 KB are refused; the text is capped at 8,000 characters."],
  },
})
