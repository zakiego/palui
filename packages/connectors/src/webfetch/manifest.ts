import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

export default defineManifest({
  id: "webfetch",
  kind: "skill",
  name: "Web fetch",
  icon: "globe",
  summary: "Let Palui read a web page you send it.",
  description:
    "Fetches a page behind the SSRF guard (public addresses only, redirects re-checked, size and time limits) and keeps the readable text. Page content is untrusted: it taints the turn, so any write after it asks you first.",
  builtin: true,
  trust: "external",
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, Palui can't open links.",
  docs: { notes: ["Only http and https pages on public addresses. Local and private network addresses are refused."] },
})
