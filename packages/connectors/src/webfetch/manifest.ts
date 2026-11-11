import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "webfetch",
  kind: "skill",
  name: "Web fetch",
  icon: "globe",
  summary: "Let Palui read a web page you send it.",
  builtin: true,
  trust: "external",
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, Palui can't open links.",
  docs: { notes: ["Only http and https pages on public addresses. Local and private network addresses are refused."] },
})
