// Shared runtime for OpenAI/Anthropic-compatible model gateways (CLIProxyAPI,
// ai-gateway). Protocol per role: anthropic uses /v1/messages, openai uses
// /v1/chat/completions (PLAN 5.2).
import { createAnthropic } from "@ai-sdk/anthropic"
import { createOpenAICompatible } from "@ai-sdk/openai-compatible"
import { generateText, type LanguageModel } from "ai"
import { defineRuntime, type ConnectorManifest, type ConnectorRuntime } from "@palui/core/connectors/define"
import { getSetting, type ModelRole } from "@palui/core/settings"

export const PROTOCOLS = ["anthropic", "openai"] as const
export type Protocol = (typeof PROTOCOLS)[number]

export const ROLES: ModelRole[] = ["default", "fast", "vision"]

export type GatewayConfig = { baseUrl: string; protocols: Partial<Record<ModelRole, Protocol>> }

export function buildModels(name: string, cfg: GatewayConfig, apiKey: string | undefined, fetchImpl?: typeof fetch) {
  const base = `${cfg.baseUrl.replace(/\/+$/, "")}/v1`
  const anthropic = createAnthropic({ baseURL: base, apiKey: apiKey ?? "", fetch: fetchImpl })
  const openai = createOpenAICompatible({ name, baseURL: base, apiKey, fetch: fetchImpl })
  return (modelId: string, role: ModelRole): LanguageModel => ((cfg.protocols[role] ?? "anthropic") === "openai" ? openai.chatModel(modelId) : anthropic(modelId))
}

export async function listModels(cfg: GatewayConfig, apiKey: string | undefined, signal?: AbortSignal, fetchImpl: typeof fetch = fetch): Promise<string[]> {
  const res = await fetchImpl(`${cfg.baseUrl.replace(/\/+$/, "")}/v1/models`, {
    headers: apiKey ? { authorization: `Bearer ${apiKey}`, "x-api-key": apiKey } : {},
    signal,
  })
  if (!res.ok) throw new Error(`GET /v1/models answered ${res.status}`)
  const body = (await res.json()) as { data?: { id: string }[] }
  return (body.data ?? []).map((m) => m.id)
}

// A model connector runtime for a gateway manifest.
export function gatewayRuntime<M extends ConnectorManifest>(manifest: M, opts: { fetch?: typeof fetch } = {}): ConnectorRuntime<M> {
  const parse = (raw: unknown): GatewayConfig => manifest.config.parse(raw ?? {}) as GatewayConfig
  return defineRuntime(manifest, {
    async test(ctx) {
      const cfg = parse(ctx.config)
      const key = (ctx.secrets as { apiKey?: string }).apiKey
      let models: string[]
      try {
        models = await listModels(cfg, key, ctx.signal, opts.fetch)
      } catch (e) {
        return { ok: false, detail: `Can't reach ${cfg.baseUrl}: ${(e as Error).message}` }
      }
      // One 1-token call per role this connector serves in the model chains.
      const chains = getSetting(ctx.db, "modelRoles")
      const model = buildModels(ctx.id, cfg, key, opts.fetch)
      const roles: Record<string, string> = {}
      let ok = true
      for (const role of ROLES) {
        const link = chains[role]?.find((l) => l.connectorId === ctx.id)
        if (!link) continue
        try {
          await generateText({ model: model(link.model, role), prompt: "Reply with OK.", maxOutputTokens: 1, maxRetries: 0, abortSignal: ctx.signal })
          roles[role] = `${link.model}: ok`
        } catch (e) {
          ok = false
          roles[role] = `${link.model}: ${(e as Error).message.slice(0, 120)}`
        }
      }
      const detail = ok ? `${models.length} models available${Object.keys(roles).length ? `, ${Object.keys(roles).join(", ")} answered` : ""}` : `Model call failed: ${Object.entries(roles).map(([r, v]) => `${r} ${v}`).join("; ")}`
      return { ok, detail, data: { models, roles } }
    },
    async start(ctx) {
      const cfg = parse(ctx.config)
      const key = (ctx.secrets as { apiKey?: string }).apiKey
      const model = buildModels(ctx.id, cfg, key, opts.fetch)
      return {
        stop: async () => {},
        model,
        live: () => ({ baseUrl: cfg.baseUrl, protocols: cfg.protocols }),
        // The settings page lists these in the model picker.
        async action(name) {
          if (name !== "listModels") throw Object.assign(new Error(`Unknown action ${name}`), { status: 404 })
          return { models: await listModels(cfg, key, AbortSignal.timeout(10_000), opts.fetch) }
        },
      }
    },
  })
}
