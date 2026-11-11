// A cached MCP client over Streamable HTTP (PLAN 5.4), shared by the generic MCP
// connector and recall. One connection per connector instance; any transport
// failure drops the client so the next call reconnects.
import { createMCPClient, type ListToolsResult, type MCPClient, type McpToolAnnotations } from "@ai-sdk/mcp"
import type { Tool } from "ai"
import type { ToolClass } from "@palui/db"

export type McpToolDef = ListToolsResult["tools"][number]

export type McpEndpoint = {
  url: string
  headers?: Record<string, string>
  // Injected in tests; defaults to global fetch.
  fetch?: typeof fetch
  clientName?: string
}

// Prefill for a tool's class from its MCP annotations. MCP says destructiveHint
// defaults to true, so only an explicit `destructiveHint: false` makes a
// non-read-only tool `write`. No annotations at all means unknown: destructive.
export function suggestClass(annotations: McpToolAnnotations | undefined): ToolClass {
  if (annotations?.readOnlyHint === true) return "read"
  if (annotations?.destructiveHint === false) return "write"
  return "destructive"
}

export class McpConnection {
  private client: Promise<MCPClient> | null = null
  private defs: { at: number; result: ListToolsResult } | null = null

  constructor(
    private readonly endpoint: McpEndpoint,
    private readonly opts: { defsTtlMs?: number } = {},
  ) {}

  private connect(): Promise<MCPClient> {
    this.client ??= createMCPClient({
      clientName: this.endpoint.clientName ?? "palui",
      transport: {
        type: "http",
        url: this.endpoint.url,
        headers: this.endpoint.headers,
        ...(this.endpoint.fetch ? { fetch: this.endpoint.fetch } : {}),
      },
    }).catch((err: unknown) => {
      this.client = null
      throw err
    })
    return this.client
  }

  // Drop the cached client (and its tool list). The next call reconnects.
  reset(): void {
    const old = this.client
    this.client = null
    this.defs = null
    if (old) void old.then((c) => c.close()).catch(() => {})
  }

  async close(): Promise<void> {
    const old = this.client
    this.client = null
    this.defs = null
    if (old) await old.then((c) => c.close()).catch(() => {})
  }

  // Run fn with a connected client, retrying once on a fresh client if it throws.
  private async withClient<T>(fn: (c: MCPClient) => Promise<T>): Promise<T> {
    try {
      return await fn(await this.connect())
    } catch {
      this.reset()
      return await fn(await this.connect())
    }
  }

  // Every tool the server lists (all pages), cached for defsTtlMs (default 5 min).
  async listTools(opts: { fresh?: boolean; now?: number } = {}): Promise<ListToolsResult> {
    const now = opts.now ?? Date.now()
    const ttl = this.opts.defsTtlMs ?? 5 * 60_000
    if (!opts.fresh && this.defs && now - this.defs.at < ttl) return this.defs.result
    const result = await this.withClient(async (c) => {
      const tools: McpToolDef[] = []
      let cursor: string | undefined
      for (let page = 0; page < 20; page++) {
        const res = await c.listTools(cursor ? { params: { cursor } } : undefined)
        tools.push(...res.tools)
        cursor = res.nextCursor
        if (!cursor) break
      }
      return { tools } as ListToolsResult
    })
    this.defs = { at: now, result }
    return result
  }

  // AI SDK tools for the given definitions. A call that throws (transport
  // trouble, not an isError result) resets the connection for the next turn.
  async tools(defs: McpToolDef[]): Promise<Record<string, Tool>> {
    const client = await this.connect()
    const set = client.toolsFromDefinitions({ tools: defs } as ListToolsResult) as Record<string, Tool>
    const out: Record<string, Tool> = {}
    for (const [name, t] of Object.entries(set)) {
      const execute = t.execute
      out[name] = execute
        ? {
            ...t,
            execute: async (input: unknown, options: Parameters<typeof execute>[1]) => {
              try {
                return await execute(input, options)
              } catch (err) {
                this.reset()
                throw err
              }
            },
          }
        : t
    }
    return out
  }
}

export function authHeaders(input: { authMode: "none" | "bearer" | "header"; headerName?: string; token?: string }): Record<string, string> {
  if (input.authMode === "none" || !input.token) return {}
  if (input.authMode === "bearer") return { Authorization: `Bearer ${input.token}` }
  return { [input.headerName?.trim() || "Authorization"]: input.token }
}
