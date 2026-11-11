import { z } from "zod"
import { defineManifest } from "@palui/core/connectors/define"

const proto = z.enum(["anthropic", "openai"]).default("anthropic")

export default defineManifest({
  id: "ai-gateway",
  kind: "model",
  name: "ai-gateway",
  icon: "network",
  summary: "Zaki's ai-gateway on the host, the fallback in every model chain.",
  config: z.object({
    baseUrl: z.string().url().default("http://host.docker.internal:8793"),
    protocols: z.object({ default: proto, fast: proto, vision: proto }).partial().default({}),
  }),
  secrets: { apiKey: z.string().regex(/^zkg_/, "ai-gateway keys start with zkg_") },
  setup: [
    { field: "baseUrl", label: "Base URL", placeholder: "http://host.docker.internal:8793", optional: true },
    { field: "apiKey", label: "API key", secret: true, placeholder: "zkg_..." },
  ],
  docs: { commands: ["/connectors on ai-gateway", "/connectors off ai-gateway"] },
})
