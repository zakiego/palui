import { z } from "zod"
import { defineManifest } from "../define"

export default defineManifest({
  id: "webfetch",
  kind: "skill",
  name: "Web fetch",
  icon: "globe",
  summary: "Search the web and read the pages it finds.",
  builtin: true,
  trust: "external",
  config: z.object({}),
  secrets: {},
  setup: [],
  pauseNote: "While paused, Palui can't open links.",
  docs: {
    notes: [
      "Only http and https pages on public addresses. Local and private network addresses are refused.",
      "Search goes through DuckDuckGo's plain HTML results page. No key needed.",
    ],
  },
})
