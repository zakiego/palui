import { z } from "zod"
import { defineManifest } from "@palui/core/saklar/define"

const proto = z.enum(["anthropic", "openai"]).default("anthropic")

export default defineManifest({
  id: "cliproxy",
  kind: "model",
  name: "CLIProxyAPI",
  icon: "cpu",
  summary: "Claude and Gemini through CLIProxyAPI on the host.",
  description: "The default model connector. Each role (default, fast, vision) picks a protocol: anthropic uses /v1/messages, openai uses /v1/chat/completions.",
  config: z.object({
    baseUrl: z.string().url().default("http://host.docker.internal:8317"),
    protocols: z.object({ default: proto, fast: proto, vision: proto }).partial().default({}),
  }),
  secrets: { apiKey: z.string().min(1) },
  setup: [
    { field: "baseUrl", label: "Base URL", placeholder: "http://host.docker.internal:8317", optional: true },
    { field: "apiKey", label: "API key", secret: true, help: "One of the api-keys in CLIProxyAPI's config.yaml" },
  ],
  docs: { commands: ["/saklar on cliproxy", "/saklar off cliproxy"] },
})
