import { z } from "zod"
import { defineManifest } from "../define"

export const DEFAULT_T3_URL = "http://host.docker.internal:3773"

export const t3Config = z.object({
  // Where the T3 Code server answers, as seen from the runtime. From the Docker
  // runtime that's the Mac host: host.docker.internal (OrbStack and Docker
  // Desktop both forward it to the host's loopback).
  url: z.url({ protocol: /^https?$/ }).default(DEFAULT_T3_URL),
  // Base for a new thread's git worktree when the model doesn't name one.
  baseRef: z.string().trim().min(1).max(200).default("main"),
  // "<provider instance>/<model>" for projects without a default model. Empty:
  // the first model T3 Code offers.
  model: z
    .string()
    .trim()
    .regex(/^[^/\s]+\/\S+$/, "Use provider/model, e.g. claudeAgent/claude-sonnet-5-5")
    .optional(),
})

export default defineManifest({
  id: "t3code",
  kind: "tools",
  name: "T3 Code",
  icon: "square-terminal",
  summary: "Start coding threads in T3 Code from chat.",
  trust: "own",
  config: t3Config,
  secrets: { pairingCode: z.string().trim().min(8) },
  setup: [
    {
      field: "pairingCode",
      label: "Pairing code",
      secret: true,
      help: "Run this on the T3 Code Mac, then paste the code.",
      command: "t3 auth pairing create --ttl 10m",
    },
    {
      field: "url",
      label: "T3 Code URL",
      placeholder: DEFAULT_T3_URL,
      help: "In Docker on the same Mac, keep the default.",
    },
    {
      field: "baseRef",
      label: "Default base branch",
      optional: true,
      placeholder: "main",
    },
    {
      field: "model",
      label: "Model",
      optional: true,
      placeholder: "claudeAgent/claude-opus-5-5",
      help: "Used when a project has no default model.",
    },
  ],
  pauseNote: "While paused, running threads keep going and finish messages wait.",
  removeWarning: "This removes Palui's T3 Code token. Running threads keep going.",
  docs: {
    commands: ["t3 auth pairing create --ttl 10m"],
    notes: [
      "Threads run with full access, the same as a thread you start in T3 Code yourself.",
      "The token lasts 30 days and T3 Code can't renew it. When it runs out the connector shows an error; paste a fresh pairing code.",
      "Starting a thread and sending to one are write tools: they run right away in your own chat, and ask first after untrusted content or in a scheduled job.",
    ],
  },
})
