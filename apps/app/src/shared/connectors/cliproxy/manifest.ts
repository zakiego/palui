import { z } from "zod"
import { defineManifest } from "../define"

const proto = z.enum(["anthropic", "openai"]).default("anthropic")

export default defineManifest({
  id: "cliproxy",
  kind: "model",
  name: "CLIProxyAPI",
  icon: "cpu",
  summary: "Claude and Gemini through CLIProxyAPI on the host.",
  config: z.object({
    baseUrl: z.string().url().default("http://host.docker.internal:8317"),
    protocols: z.object({ default: proto, fast: proto, vision: proto }).partial().default({}),
  }),
  secrets: { apiKey: z.string().min(1) },
  setup: [
    { field: "baseUrl", label: "Base URL", placeholder: "http://host.docker.internal:8317", optional: true },
    { field: "apiKey", label: "API key", secret: true, help: "Copy a key from the api-keys list in CLIProxyAPI's config file." },
  ],
  docs: { commands: ["/connectors on cliproxy", "/connectors off cliproxy"] },
})
