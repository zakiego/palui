import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

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
  description:
    "Talks to the T3 Code server on your Mac. Palui can list your projects, start a thread with a task (in its own git worktree by default), send follow-ups, check on a thread and stop one. When a thread Palui started finishes, Palui messages the chat it came from with the agent's reply and the thread link.",
  trust: "own",
  config: t3Config,
  secrets: { pairingCode: z.string().trim().min(8) },
  setup: [
    {
      field: "url",
      label: "T3 Code URL",
      placeholder: DEFAULT_T3_URL,
      help: "The T3 Code server as the runtime sees it. In Docker on the same Mac, keep the default.",
    },
    {
      field: "pairingCode",
      label: "Pairing code",
      secret: true,
      help: "On the Mac running T3 Code, run: t3 auth pairing create --ttl 10m. Palui trades it for a 30-day full-access token, so paste a new code when that runs out.",
    },
    { field: "baseRef", label: "Default base branch", optional: true, placeholder: "main", help: "New threads branch off this in their own worktree" },
    {
      field: "model",
      label: "Model",
      optional: true,
      placeholder: "claudeAgent/claude-opus-5-5",
      help: "Provider and model for projects without a default model in T3 Code. Empty: the first model T3 Code offers.",
    },
  ],
  pauseNote: "While paused, Palui can't start or check T3 Code threads, and it holds the finish messages until you resume. Threads keep running in T3 Code.",
  removeWarning: "This removes the T3 Code token from Palui. Threads already running in T3 Code keep going.",
  docs: {
    commands: ["t3 auth pairing create --ttl 10m"],
    notes: [
      "Threads run with full access, the same as a thread you start in T3 Code yourself.",
      "The token lasts 30 days and T3 Code can't renew it. When it runs out the connector shows an error; paste a fresh pairing code.",
      "Starting a thread and sending to one are write tools: they run right away in your own chat, and ask first after untrusted content or in a scheduled job.",
    ],
  },
})
