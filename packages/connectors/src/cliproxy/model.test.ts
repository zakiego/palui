import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { silentLogger } from "@palui/core/log"
import { setSetting } from "@palui/core/settings"
import { failoverModel } from "@palui/core/models/failover"
import { generateText } from "ai"
import type { ConnectorContext } from "@palui/core/connectors/define"
import aiGatewayManifest from "../ai-gateway/manifest"
import manifest from "./manifest"
import { gatewayRuntime } from "./model"

function fakeFetch(calls: { url: string; headers: Record<string, string>; body?: unknown }[]) {
  return (async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input)
    const headers = Object.fromEntries(new Headers(init?.headers).entries())
    calls.push({ url, headers, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { "content-type": "application/json" } })
    if (url.endsWith("/v1/models")) return json({ data: [{ id: "claude-sonnet-4-6" }, { id: "gemini-3-flash-preview" }] })
    if (url.endsWith("/v1/messages")) {
      return json({ id: "m1", type: "message", role: "assistant", model: "x", content: [{ type: "text", text: "OK" }], stop_reason: "max_tokens", usage: { input_tokens: 3, output_tokens: 1 } })
    }
    if (url.endsWith("/v1/chat/completions")) {
      return json({ id: "c1", object: "chat.completion", created: 1, model: "x", choices: [{ index: 0, message: { role: "assistant", content: "OK" }, finish_reason: "length" }], usage: { prompt_tokens: 3, completion_tokens: 1 } })
    }
    return new Response("nope", { status: 404 })
  }) as typeof fetch
}

function ctx(config: Record<string, unknown>, secrets: Record<string, string>, id = "cliproxy"): ConnectorContext {
  const db = memoryDb()
  setSetting(db, "modelRoles", {
    default: [{ connectorId: id, model: "claude-sonnet-4-6" }],
    fast: [{ connectorId: id, model: "gemini-3-flash-preview" }],
    vision: [{ connectorId: "other", model: "x" }],
  })
  return { id, type: id, config, secrets, trust: "own", db, log: silentLogger, dataDir: "/tmp", signal: new AbortController().signal, services: {} as never, setStatus() {}, event() {}, patchConfig() {} }
}

describe("gateway model connectors", () => {
  it("test lists models, then makes a 1-token call per role it serves, with the protocol per role", async () => {
    const calls: { url: string; headers: Record<string, string>; body?: unknown }[] = []
    const rt = gatewayRuntime(manifest, { fetch: fakeFetch(calls) })
    const r = await rt.test(ctx({ baseUrl: "http://proxy:8317/", protocols: { fast: "openai" } }, { apiKey: "secret-key" }) as never)
    expect(r.ok).toBe(true)
    expect(r.data?.models).toEqual(["claude-sonnet-4-6", "gemini-3-flash-preview"])
    expect(r.data?.roles).toEqual({ default: "claude-sonnet-4-6: ok", fast: "gemini-3-flash-preview: ok" })
    expect(calls.map((c) => c.url)).toEqual(["http://proxy:8317/v1/models", "http://proxy:8317/v1/messages", "http://proxy:8317/v1/chat/completions"])
    expect(calls[1]!.headers["x-api-key"]).toBe("secret-key")
    expect(calls[2]!.headers.authorization).toBe("Bearer secret-key")
    expect((calls[1]!.body as { max_tokens: number }).max_tokens).toBe(1)
  })

  it("reports an unreachable gateway and applies the default base URL", async () => {
    const rt = gatewayRuntime(aiGatewayManifest, {
      fetch: (async () => {
        throw new Error("ECONNREFUSED")
      }) as unknown as typeof fetch,
    })
    const r = await rt.test(ctx({}, { apiKey: "zkg_x" }, "ai-gateway") as never)
    expect(r).toMatchObject({ ok: false })
    expect(r.detail).toContain("http://host.docker.internal:8793")
    expect(aiGatewayManifest.secrets.apiKey.safeParse("sk-nope").success).toBe(false)
    expect(aiGatewayManifest.secrets.apiKey.safeParse("zkg_abc").success).toBe(true)
  })

  it("start builds AI SDK models per role", async () => {
    const rt = gatewayRuntime(manifest)
    const h = await rt.start(ctx({ protocols: { vision: "openai" } }, { apiKey: "k" }) as never)
    const def = h.model!("claude-sonnet-4-6", "default") as { provider: string }
    const vis = h.model!("gemini", "vision") as { provider: string }
    expect(def.provider).toContain("anthropic")
    expect(vis.provider).toContain("cliproxy")
    expect(h.live!()).toMatchObject({ baseUrl: "http://host.docker.internal:8317" })
  })

  it("start lists the gateway's models through the listModels action", async () => {
    const calls: { url: string; headers: Record<string, string>; body?: unknown }[] = []
    const rt = gatewayRuntime(manifest, { fetch: fakeFetch(calls) })
    const h = await rt.start(ctx({}, { apiKey: "k" }) as never)
    expect(await h.action!("listModels", {})).toEqual({ models: ["claude-sonnet-4-6", "gemini-3-flash-preview"] })
    await expect(h.action!("nope", {})).rejects.toThrow("Unknown action nope")
  })

  it("a link's effort reaches the gateway as reasoning effort, and only on that link", async () => {
    const calls: { url: string; headers: Record<string, string>; body?: unknown }[] = []
    const rt = gatewayRuntime(manifest, { fetch: fakeFetch(calls) })
    const h = await rt.start(ctx({ protocols: { default: "openai" } }, { apiKey: "k" }) as never)
    const lm = (id: string) => h.model!(id, "default") as never
    await generateText({ model: failoverModel([{ connectorId: "cliproxy", model: "gpt-5", effort: "high", lm: lm("gpt-5") }]), prompt: "hi" })
    await generateText({ model: failoverModel([{ connectorId: "cliproxy", model: "gpt-5", lm: lm("gpt-5") }]), prompt: "hi" })
    const bodies = calls.filter((c) => c.url.endsWith("/chat/completions")).map((c) => c.body as { reasoning_effort?: string })
    expect(bodies[0]!.reasoning_effort).toBe("high")
    expect(bodies[1]!.reasoning_effort).toBeUndefined()
  })
})
