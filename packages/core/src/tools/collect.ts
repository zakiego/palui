// Tools for a turn (PLAN 8.5, 5.2, 10): every running connector's tools,
// namespaced `<connectorId>__<tool>`, capped at budgets.maxTools with the most
// relevant connectors first (skills, memory, then the rest).
import type { Tool } from "ai"
import type { ToolClass } from "@palui/db"
import type { Logger } from "../log"
import type { PaluiTool, TurnInfo } from "../connectors/define"
import type { RunningEntry } from "../connectors/supervisor"

export const TOOL_SEP = "__"

export type ToolMeta = {
  name: string
  connectorId: string
  tool: string
  class: ToolClass
  untrusted: boolean
  title?: string
}

export type CollectedTools = {
  tools: Record<string, Tool>
  meta: Record<string, ToolMeta>
  connectorIds: string[]
}

const KIND_ORDER: Record<string, number> = { skill: 0, memory: 1, tools: 2, channel: 3, model: 4 }

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
  // Jobs and hooks never get destructive tools (PLAN 7, 10).
  excludeDestructive?: boolean
  log: Logger
  // Called after an untrusted tool returns (taints the turn).
  onUntrustedResult: (meta: ToolMeta) => void
  // Called when any tool starts and ends, for timing.
  onToolStart?: (toolCallId: string) => void
}

export async function collectTools(running: RunningEntry[], opts: CollectOptions): Promise<CollectedTools> {
  const out: CollectedTools = { tools: {}, meta: {}, connectorIds: [] }
  const entries = running
    .filter((e) => e.handle.tools && (!opts.scope || opts.scope.includes(e.id)))
    .sort((a, b) => (KIND_ORDER[a.row.kind] ?? 9) - (KIND_ORDER[b.row.kind] ?? 9))
  let count = 0
  for (const e of entries) {
    if (count >= opts.maxTools) break
    let tools: Record<string, PaluiTool>
    try {
      tools = (await e.handle.tools!(opts.turn)) ?? {}
    } catch (err) {
      opts.log.warn("connector tools() threw, skipping its tools", { connector_id: e.id, error: err })
      continue
    }
    let used = false
    for (const [toolName, pt] of Object.entries(tools)) {
      if (count >= opts.maxTools) break
      if (!pt?.tool) continue
      const cls = normalizeClass(pt.class)
      if (opts.excludeDestructive && cls === "destructive") continue
      const name = namespaced(e.id, toolName)
      const meta: ToolMeta = { name, connectorId: e.id, tool: toolName, class: cls, untrusted: !!pt.untrusted || e.row.trust === "external", title: pt.title }
      out.tools[name] = wrapTool(pt.tool, meta, opts)
      out.meta[name] = meta
      count++
      used = true
    }
    if (used) out.connectorIds.push(e.id)
  }
  return out
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
