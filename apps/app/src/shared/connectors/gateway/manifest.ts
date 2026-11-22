import { z } from "zod"
import { defineManifest } from "../define"

const proto = z.enum(["anthropic", "openai"]).default("anthropic")

export default defineManifest({
  id: "gateway",
  kind: "model",
  name: "Model gateway",
  icon: "network",
  summary: "Models from any OpenAI- or Anthropic-compatible endpoint.",
  multiple: true,
  config: z.object({
    baseUrl: z.string().url(),
    protocols: z.object({ default: proto, fast: proto, vision: proto }).partial().default({}),
  }),
  secrets: { apiKey: z.string().min(1) },
  setup: [
    { field: "baseUrl", label: "Base URL", placeholder: "https://llm.example.com", help: "The address before /v1, like a LiteLLM or OpenRouter endpoint or your own proxy." },
    { field: "apiKey", label: "API key", secret: true, help: "Sent as a bearer token and as x-api-key." },
  ],
  docs: { notes: ["Each gateway you add gets its own id, like gateway-openrouter. Pick its models per role in Settings."] },
})
