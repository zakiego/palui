import { tool, type Tool } from "ai"
import { z } from "zod"
import type { ToolClass } from "@palui/db"
import { defineRuntime, type ConfigOf, type MemoryHandle, type PaluiTool, type TurnInfo } from "@palui/core/connectors/define"
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
    async recall(query, { timeoutMs, container, about }) {
      if (!shouldRecall(query)) {
        stats.skippedShort++
        return null
      }
      stats.calls++
      try {
        const p = await api.profile(container || config.container || undefined, query, AbortSignal.timeout(timeoutMs))
        const text = profileText(p, about)
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
    async save({ sourceId, content, title, container }) {
      if (!content.trim()) return
      await api.addMemory({ container: container || config.container || undefined, content, title, sourceId })
    },
  }
}

export const FORGET_MAX = 20

type ForgetScope = { container: string; locked: boolean }

// A locked scope whose container is still unknown (recall didn't say what its
// default is) touches nothing: comparing against "" would only ever say "not found".
const UNKNOWN_CONTAINER = "Can't tell which recall container this agent uses right now. Try again in a moment."

// REST tools: recall's MCP server has no forget, so they're DELETE calls. A
// locked scope (every agent) may only touch its own container: the item is
// looked up first, and any other container answers "not found".
async function forgetOne(api: RecallApi, id: string, scope?: ForgetScope): Promise<{ forgotten: boolean; id: string; fact?: string; error?: string }> {
  if (scope?.locked && !scope.container) return { forgotten: false, id, error: UNKNOWN_CONTAINER }
  try {
    if (scope?.locked) {
      const found = await api.getMemory(id)
      if (found.memory?.container !== scope.container) return { forgotten: false, id, error: "No memory with that id" }
    }
    const res = await api.forget(id)
    return { forgotten: true, id, fact: res.memory?.fact }
  } catch (err) {
    if (err instanceof RecallError && err.status === 404) return { forgotten: false, id, error: "No memory with that id" }
    throw err
  }
}

// One call takes every id, so forgetting several facts asks the owner once.
export function forgetTool(api: RecallApi, scope?: ForgetScope): Tool {
  return tool({
    description: `Forget memories in recall by id (from search_memories or list_memories). The facts stop being recalled. Pass every id in one call, at most ${FORGET_MAX}: each call asks the owner first.`,
    inputSchema: z.object({
      ids: z.array(z.string().min(1)).min(1).max(FORGET_MAX).optional().describe("Memory ids"),
      id: z.string().min(1).optional().describe("One memory id (older form; prefer ids)"),
    }),
    execute: async ({ ids, id }) => {
      const all = [...new Set([...(ids ?? []), ...(id ? [id] : [])])].slice(0, FORGET_MAX)
      if (!all.length) return { forgotten: 0, results: [], error: "Pass ids." }
      const results = []
      for (const one of all) results.push(await forgetOne(api, one, scope))
      if (!ids && id) return results[0]!
      return { forgotten: results.filter((r) => r.forgotten).length, results }
    },
  })
}

// Deletes a document's stored text (the doc_ id add_memory returned, or a
// search hit's document). Facts taken from it stay: forget those with forget_memory.
export function forgetDocumentTool(api: RecallApi, scope?: ForgetScope): Tool {
  return tool({
    description: "Delete a document from recall by its id (doc_..., as add_memory returned it): its stored text stops showing up in searches. Facts already taken from it stay; forget those with forget_memory. Ask the owner first.",
    inputSchema: z.object({ id: z.string().min(1).describe("Document id, doc_...") }),
    execute: async ({ id }) => {
      if (scope?.locked && !scope.container) return { deleted: false, id, error: UNKNOWN_CONTAINER }
      try {
        const found = await api.getDocument(id)
        if (scope?.locked && found.document?.container !== scope.container) return { deleted: false, id, error: "No document with that id" }
        await api.deleteDocument(id)
        return { deleted: true, id, title: found.document?.title ?? null }
      } catch (err) {
        if (err instanceof RecallError && err.status === 404) return { deleted: false, id, error: "No document with that id" }
        throw err
      }
    },
  })
}

export class ContainerError extends Error {}

// The container a recall tool call runs against. An empty `container` argument
// becomes the turn's own container, so the model never writes into recall's
// server default. A locked scope (every agent) can't reach past its own
// container, and its read list for read tools: the owner's memory stays the owner's.
export function resolveContainer(asked: unknown, cls: ToolClass, scope: { container: string; read: string[]; locked: boolean }): string {
  const name = typeof asked === "string" ? asked.trim() : ""
  if (!name || name === scope.container) return scope.container
  if (!scope.locked) return name
  if (cls === "read" && scope.read.includes(name)) return name
  const allowed = (cls === "read" ? [scope.container, ...scope.read] : [scope.container]).filter(Boolean)
  const can = allowed.length ? `It can ${cls === "read" ? "read" : "write"} ${allowed.join(", ")}.` : "It can only use its own container."
  throw new ContainerError(`This agent can't use recall container ${name.slice(0, 64)}. ${can}`)
}

function withContainer(t: Tool, cls: ToolClass, scope: { container: string; read: string[]; locked: boolean }): Tool {
  const execute = t.execute
  if (!execute) return t
  return {
    ...t,
    execute: (input: unknown, options: Parameters<typeof execute>[1]) => {
      const args = (input && typeof input === "object" ? input : {}) as Record<string, unknown>
      return execute({ ...args, container: resolveContainer(args.container, cls, scope) }, options)
    },
  }
}

// The turn's scope with the connector's container filled in. No scope (the
// catalog, a caller that didn't say whose turn it is): the connector's own
// container, locked, so nothing without a scope can name another container.
// "" means neither names one, so it's recall's own default: the caller puts
// that name in before any tool compares containers.
export function turnScope(turn: Pick<TurnInfo, "memory"> | undefined, fallback: string | undefined): { container: string; read: string[]; locked: boolean } {
  const m = turn?.memory
  return { container: m?.container || fallback || "", read: m?.read ?? [], locked: m ? m.locked : true }
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
    let mcpError: string | null = null
    // A connector saved without a container (and Palui with no override) uses
    // recall's default. add_memory and search send no container and land there,
    // so forget has to compare against that same name. Asked once, kept while it answers.
    let serverDefault: Promise<string> | null = null
    const defaultContainer = () => {
      serverDefault ??= api
        .defaultContainer(AbortSignal.timeout(5000))
        .then((name) => {
          // Kept in the config so the dashboard knows this name is the owner's
          // memory too (ownerContainers in core) and no other agent can take it.
          if (ctx.config.defaultContainer !== name) ctx.patchConfig({ defaultContainer: name })
          return name
        })
        .catch((err) => {
          serverDefault = null
          throw err
        })
      return serverDefault
    }
    if (!ctx.config.container) {
      defaultContainer().catch((err) => ctx.log.warn("recall default container unknown for now", { error: err }))
    }

    return {
      memory: createMemoryHandle(api, ctx.config, stats),
      async stop() {
        await mcp.close()
      },
      async tools(turn) {
        const scope = turnScope(turn, ctx.config.container)
        if (!scope.container) {
          try {
            scope.container = await defaultContainer()
          } catch (err) {
            ctx.log.warn("recall default container unknown, forget tools refuse until it answers", { error: err })
          }
        }
        const out: Record<string, PaluiTool> = {
          forget_memory: { tool: forgetTool(api, scope), class: "destructive", title: "Forget memory" },
          forget_document: { tool: forgetDocumentTool(api, scope), class: "destructive", title: "Delete document" },
        }
        try {
          const { tools: defs } = await mcp.listTools()
          const aiTools = await mcp.tools(defs)
          for (const def of defs) {
            const t = aiTools[def.name]
            // A tool recall adds later may take arguments that reach other
            // containers (upcoming's all_containers), so a locked scope gets known tools only.
            if (scope.locked && !(def.name in RECALL_TOOL_CLASSES)) continue
            const cls = recallToolClass(def.name)
            if (t) out[def.name] = { tool: withContainer(t, cls, scope), class: cls, title: def.title ?? def.name }
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
