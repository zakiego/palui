import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "webfetch",
  kind: "skill",
  name: "Web fetch",
  icon: "globe",
  summary: "Read a web page you send.",
  builtin: true,
  trust: "external",
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, Palui can't open links.",
  docs: { notes: ["Only http and https pages on public addresses. Local and private network addresses are refused."] },
})
