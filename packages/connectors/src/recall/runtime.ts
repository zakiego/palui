import { tool, type Tool } from "ai"
import { z } from "zod"
import type { ToolClass } from "@palui/db"
import { defineRuntime, type ConfigOf, type MemoryHandle, type PaluiTool } from "@palui/core/saklar/define"
import { McpConnection } from "../mcp/client"
import { profileText, RecallApi, RecallError } from "./api"
import manifest from "./manifest"

type Config = ConfigOf<typeof manifest>

// recall's own MCP tools and their classes. Anything recall adds later is
// destructive until it's listed here (fail closed).
export const RECALL_TOOL_CLASSES: Record<string, ToolClass> = {
  add_memory: "write",
  search_memories: "read",
  ask_memory: "read",
  get_profile: "read",
  list_memories: "read",
}

export function recallToolClass(name: string): ToolClass {
  return RECALL_TOOL_CLASSES[name] ?? "destructive"
}

export type RecallStats = { calls: number; hits: number; empty: number; skippedShort: number; timeouts: number; errors: number }

// Auto-recall is skipped for commands and messages under 4 words (PLAN 5.3).
export function shouldRecall(query: string): boolean {
  const q = query.trim()
  if (!q || q.startsWith("/")) return false
  return q.split(/\s+/).length >= 4
}

export function createMemoryHandle(api: RecallApi, config: Config, stats: RecallStats): MemoryHandle {
  return {
    autoRecall: config.autoRecall,
    autoSave: config.autoSave,
    async recall(query, { timeoutMs }) {
      if (!shouldRecall(query)) {
        stats.skippedShort++
        return null
      }
      stats.calls++
      try {
        const p = await api.profile(config.container, query, AbortSignal.timeout(timeoutMs))
        const text = profileText(p)
        if (text) stats.hits++
        else stats.empty++
        return text
      } catch (err) {
        const name = (err as Error)?.name
        if (name === "TimeoutError" || name === "AbortError") stats.timeouts++
        else stats.errors++
        return null
      }
    },
    async save({ sourceId, content, title }) {
      if (!content.trim()) return
      await api.addMemory({ container: config.container, content, title, sourceId })
    },
  }
}

// The one REST tool: recall's MCP server has no forget, so it's DELETE /v1/memories/:id.
export function forgetTool(api: RecallApi): Tool {
  return tool({
    description: "Forget one memory in recall by its id (from search_memories or list_memories). The fact stops being recalled. Ask the owner first.",
    inputSchema: z.object({ id: z.string().min(1).describe("Memory id") }),
    execute: async ({ id }) => {
      try {
        const res = await api.forget(id)
        return { forgotten: true, id, fact: res.memory?.fact }
      } catch (err) {
        if (err instanceof RecallError && err.status === 404) return { forgotten: false, id, error: "No memory with that id" }
        throw err
      }
    },
  })
}

// Default recall's optional `container` argument to the configured container,
// so the model doesn't silently write into recall's server default.
function withContainer(t: Tool, container: string): Tool {
  const execute = t.execute
  if (!execute) return t
  return {
    ...t,
    execute: (input: unknown, options: Parameters<typeof execute>[1]) => {
      const args = (input && typeof input === "object" ? input : {}) as Record<string, unknown>
      return execute({ ...args, container: args.container || container }, options)
    },
  }
}

function explain(err: unknown): string {
  return err instanceof RecallError ? err.message : `Can't reach recall: ${(err as Error)?.message ?? String(err)}`
}

export default defineRuntime(manifest, {
  async test(ctx) {
    if (!ctx.secrets.apiKey) return { ok: false, detail: "Add the recall API key (rcl_...)" }
    const api = new RecallApi(ctx.config.url, ctx.secrets.apiKey)
    try {
      const health = await api.health(AbortSignal.timeout(5000))
      if (!health.ok) return { ok: false, detail: "recall /health says it isn't healthy" }
      const containers = await api.containers(AbortSignal.timeout(5000))
      return {
        ok: true,
        detail: `Connected, ${containers.length} container${containers.length === 1 ? "" : "s"}`,
        data: { containers },
      }
    } catch (err) {
      return { ok: false, detail: explain(err) }
    }
  },

  async start(ctx) {
    if (!ctx.secrets.apiKey) throw new Error("Add the recall API key (rcl_...)")
    const api = new RecallApi(ctx.config.url, ctx.secrets.apiKey)
    try {
      await api.health(AbortSignal.timeout(5000))
    } catch (err) {
      throw new Error(explain(err))
    }
    const mcp = new McpConnection({ url: api.mcpUrl, headers: { Authorization: `Bearer ${ctx.secrets.apiKey}` } })
    const stats: RecallStats = { calls: 0, hits: 0, empty: 0, skippedShort: 0, timeouts: 0, errors: 0 }
    const forget = forgetTool(api)
    let mcpError: string | null = null

    return {
      memory: createMemoryHandle(api, ctx.config, stats),
      async stop() {
        await mcp.close()
      },
      async tools() {
        const out: Record<string, PaluiTool> = {
          forget_memory: { tool: forget, class: "destructive", title: "Forget memory" },
        }
        try {
          const { tools: defs } = await mcp.listTools()
          const aiTools = await mcp.tools(defs)
          for (const def of defs) {
            const t = aiTools[def.name]
            if (t) out[def.name] = { tool: withContainer(t, ctx.config.container), class: recallToolClass(def.name), title: def.title ?? def.name }
          }
          if (mcpError) {
            mcpError = null
            ctx.setStatus("on")
          }
        } catch (err) {
          const detail = `recall MCP: ${(err as Error)?.message ?? String(err)}`
          if (detail !== mcpError) {
            mcpError = detail
            ctx.setStatus("error", detail)
          }
        }
        return out
      },
      live() {
        const tried = stats.calls
        return {
          container: ctx.config.container,
          autoRecall: ctx.config.autoRecall,
          autoSave: ctx.config.autoSave,
          recall: { ...stats, skipRate: tried ? (stats.timeouts + stats.errors) / tried : 0 },
          error: mcpError,
        }
      },
      async action(name) {
        if (name === "listContainers") return { containers: await api.containers(AbortSignal.timeout(5000)) }
        throw new Error(`Unknown action ${name}`)
      },
    }
  },
})
