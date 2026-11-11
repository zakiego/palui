// Tools for a turn (PLAN 8.5, 5.2, 10): every running connector's tools,
// namespaced `<connectorId>__<tool>`. When there are more than budgets.maxTools,
// only a core set is active up front (tools loaded earlier in the session, then
// skills, memory and the rest) and the others are loadable through tool search
// (`palui__cari_tool`), so nothing is dropped however many connectors are on.
import { connectorServesAgent } from "../connectors/store"
import { asSchema, tool, type Tool } from "ai"
import { z } from "zod"
import type { ToolClass } from "@palui/db"
import type { Logger } from "../log"
import type { PaluiTool, TurnInfo } from "../connectors/define"
import type { RunningEntry } from "../connectors/supervisor"
import { searchTools, type SearchDoc } from "./search"

export const TOOL_SEP = "__"
// The tool search meta tool. Namespaced like the rest, under the `palui` prefix.
export const SEARCH_TOOL = `palui${TOOL_SEP}cari_tool`
// Matches loaded per search call.
export const SEARCH_LIMIT = 5
// Loaded tools a session keeps, most recent first.
export const STICKY_MAX = 20

export type ToolMeta = {
  name: string
  connectorId: string
  tool: string
  class: ToolClass
  untrusted: boolean
  title?: string
  // Connector label, for search and the capability list.
  label?: string
  // Offered in guest turns too (PLAN 19.4).
  guestSafe?: boolean
}

export type CollectedTools = {
  // Every tool the turn may run: the active ones and the ones search can load.
  tools: Record<string, Tool>
  meta: Record<string, ToolMeta>
  // Names the model is offered right now. Tool search appends to it mid-turn.
  active: string[]
  // Names not active at the start of the turn, loadable through SEARCH_TOOL.
  deferred: string[]
  connectorIds: string[]
}

const KIND_ORDER: Record<string, number> = { skill: 0, memory: 1, tools: 2, channel: 3, model: 4 }

export function emptyTools(): CollectedTools {
  return { tools: {}, meta: {}, active: [], deferred: [], connectorIds: [] }
}

export function namespaced(connectorId: string, tool: string): string {
  return `${connectorId}${TOOL_SEP}${tool}`.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 64)
}

export function normalizeClass(c: unknown): ToolClass {
  return c === "read" || c === "write" || c === "destructive" ? c : "destructive"
}

export type CollectOptions = {
  turn: TurnInfo
  maxTools: number
  // Connector ids allowed (jobs); null or undefined means all.
  scope?: string[] | null
  // Namespaced tool names allowed (flow prompt steps); "*" or null means all.
  only?: string[] | null
  // Jobs and hooks never get destructive tools (PLAN 7, 10).
  excludeDestructive?: boolean
  // Names loaded earlier in the session, most recent first. Active before the rest.
  sticky?: string[] | null
  // Called with the names a search just loaded, to keep them for the session.
  onLoad?: (names: string[]) => void
  // Guest turns only get tools marked guestSafe (PLAN 19.4, fail closed).
  guestOnly?: boolean
  log: Logger
  // Called after an untrusted tool returns (taints the turn).
  onUntrustedResult: (meta: ToolMeta) => void
  // Called when any tool starts and ends, for timing.
  onToolStart?: (toolCallId: string) => void
}

export async function collectTools(running: RunningEntry[], opts: CollectOptions): Promise<CollectedTools> {
  const out = emptyTools()
  const entries = running
    // An agent-scoped connector (an MCP server for one agent) only serves its agents.
    .filter((e) => e.handle.tools && (!opts.scope || opts.scope.includes(e.id)) && connectorServesAgent(e.row, opts.turn.agentId))
    .sort((a, b) => (KIND_ORDER[a.row.kind] ?? 9) - (KIND_ORDER[b.row.kind] ?? 9))
  const order: string[] = []
  for (const e of entries) {
    let tools: Record<string, PaluiTool>
    try {
      tools = (await e.handle.tools!(opts.turn)) ?? {}
    } catch (err) {
      opts.log.warn("connector tools() threw, skipping its tools", { connector_id: e.id, error: err })
      continue
    }
    let used = false
    for (const [toolName, pt] of Object.entries(tools)) {
      if (!pt?.tool) continue
      const cls = normalizeClass(pt.class)
      if (opts.excludeDestructive && cls === "destructive") continue
      if (opts.guestOnly && !pt.guestSafe) continue
      const name = namespaced(e.id, toolName)
      if (opts.only && !opts.only.includes("*") && !opts.only.includes(name)) continue
      const meta: ToolMeta = { name, connectorId: e.id, tool: toolName, class: cls, untrusted: !!pt.untrusted || e.row.trust === "external", title: pt.title, label: e.row.label, guestSafe: !!pt.guestSafe }
      out.tools[name] = wrapTool(pt.tool, meta, opts)
      out.meta[name] = meta
      order.push(name)
      used = true
    }
    if (used) out.connectorIds.push(e.id)
  }

  if (order.length <= opts.maxTools) {
    out.active = order
    return out
  }
  // Over the budget: one slot goes to search, the rest to session-loaded tools
  // first, then by connector kind. Nothing is dropped, the rest is searchable.
  const budget = Math.max(1, opts.maxTools - 1)
  const sticky = (opts.sticky ?? []).filter((n) => out.meta[n]).slice(0, STICKY_MAX)
  const active = new Set<string>()
  for (const n of [...sticky, ...order]) {
    if (active.size >= budget) break
    active.add(n)
  }
  out.active = [...active]
  out.deferred = order.filter((n) => !active.has(n))
  const searchMeta: ToolMeta = { name: SEARCH_TOOL, connectorId: "palui", tool: "cari_tool", class: "read", untrusted: false, title: "Cari tool" }
  out.tools[SEARCH_TOOL] = wrapTool(searchTool(out, order, opts), searchMeta, opts)
  out.meta[SEARCH_TOOL] = searchMeta
  out.active.push(SEARCH_TOOL)
  return out
}

function descriptionOf(t: Tool | undefined): string {
  return typeof t?.description === "string" ? t.description : ""
}

async function inputSchemaOf(t: Tool | undefined): Promise<unknown> {
  try {
    return t?.inputSchema ? await asSchema(t.inputSchema as Parameters<typeof asSchema>[0]).jsonSchema : undefined
  } catch {
    return undefined
  }
}

// Finds tools by keyword or exact name and adds them to the active set, so the
// model can call them from its next step on.
function searchTool(out: CollectedTools, order: string[], opts: CollectOptions): Tool {
  return tool({
    description:
      "Find and load tools that are not loaded yet. Search by keywords in any language (what you want to do, a service or server name), or load exact tool names. Loaded tools can be called right after this, and stay loaded for the rest of the conversation.",
    inputSchema: z.object({
      query: z.string().max(200).optional().describe("Keywords, e.g. 'jadwal sholat' or 'prayer times'"),
      names: z.array(z.string().max(100)).max(SEARCH_LIMIT).optional().describe("Exact tool names to load"),
      for: z.enum(["turn", "flow"]).optional().describe('"flow" when picking a tool for a flow step: destructive tools are left out, since flows never run them.'),
    }),
    execute: async ({ query, names, for: purpose }) => {
      const docs: SearchDoc[] = order.filter((n) => purpose !== "flow" || out.meta[n]!.class !== "destructive").map((n) => {
        const m = out.meta[n]!
        return { name: n, connectorId: m.connectorId, connectorLabel: m.label ?? m.connectorId, tool: m.tool, title: m.title, description: descriptionOf(out.tools[n]) }
      })
      const picked = new Set<string>()
      for (const raw of names ?? []) {
        const hit = docs.find((d) => d.name === raw || d.tool === raw)
        if (hit) picked.add(hit.name)
      }
      for (const h of searchTools(docs, query ?? "", SEARCH_LIMIT)) {
        if (picked.size >= SEARCH_LIMIT) break
        picked.add(h.doc.name)
      }
      const loaded = [...picked]
      const fresh = loaded.filter((n) => !out.active.includes(n))
      out.active.push(...fresh)
      if (loaded.length) opts.onLoad?.(loaded)
      if (fresh.length) opts.log.info("tool search loaded tools", { query, tools: fresh })
      if (!loaded.length) return { loaded: [], note: "No enabled tool matches. Try other words, or tell the owner this isn't available." }
      return {
        loaded: await Promise.all(
          loaded.map(async (n) => {
            const m = out.meta[n]!
            // class says whether a flow step needs approval; untrusted whether its output taints.
            return { name: n, connector: m.label ?? m.connectorId, class: m.class, untrusted: m.untrusted, description: descriptionOf(out.tools[n]), input: await inputSchemaOf(out.tools[n]) }
          }),
        ),
        note: "These tools are loaded. Call them directly.",
      }
    },
  }) as Tool
}

function wrapTool(t: Tool, meta: ToolMeta, opts: CollectOptions): Tool {
  const exec = t.execute
  if (!exec) return t
  const wrapped = async (input: unknown, options: Parameters<NonNullable<Tool["execute"]>>[1]) => {
    opts.onToolStart?.(options.toolCallId)
    try {
      return await (exec as (i: unknown, o: typeof options) => unknown)(input, options)
    } finally {
      // Even an error from an untrusted tool may carry its content.
      if (meta.untrusted) opts.onUntrustedResult(meta)
    }
  }
  return { ...t, execute: wrapped } as Tool
}
