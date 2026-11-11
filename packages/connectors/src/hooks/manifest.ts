import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "hooks",
  kind: "skill",
  name: "Webhooks",
  icon: "webhook",
  summary: "Forward events from other apps to you",
  builtin: true,
  trust: "external",
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, hook URLs answer 503.",
  docs: {
    commands: [
      "TS=$(date +%s); curl -X POST $URL -H \"x-palui-timestamp: $TS\" -H \"x-palui-signature: sha256=$(printf '%s.%s' \"$TS\" \"$BODY\" | openssl dgst -sha256 -hmac \"$SECRET\" -hex | cut -d' ' -f2)\" -d \"$BODY\"",
    ],
    notes: ["The secret is shown once when the hook is created or rotated.", "Bodies over 256 KB are refused; the text is capped at 8,000 characters."],
  },
})
