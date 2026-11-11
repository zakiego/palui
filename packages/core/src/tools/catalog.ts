// The tools an agent can be offered right now, for the agent page (PLAN 19.7):
// the collection a turn in the owner's own chat runs (`chatId` is that chat, so
// tools that only show in a DM count), with each tool's guestSafe flag saying
// whether guest turns get it too.
import type { RunningEntry } from "../connectors/supervisor"
import type { Logger } from "../log"
import type { Channel, ToolClass } from "@palui/db"
import { collectTools, SEARCH_TOOL } from "./collect"

export type CatalogTool = {
  // Namespaced: <connectorId>__<tool>.
  name: string
  tool: string
  connectorId: string
  connectorLabel: string
  // The connector's type ("mcp" for an MCP server).
  connectorType: string
  class: ToolClass
  title: string | null
  // Its results come from outside (an external connector), so they taint the turn.
  untrusted: boolean
  guestSafe: boolean
}

export async function toolCatalog(running: RunningEntry[], opts: { agentId: string; chatId: string; channel: Channel; tz: string; now: number; log: Logger }): Promise<CatalogTool[]> {
  const all = await collectTools(running, {
    turn: { runId: "", chatId: opts.chatId, channel: opts.channel, trigger: "message", tz: opts.tz, now: opts.now, agentId: opts.agentId },
    // Every tool lands in `meta`, active or deferred, whatever this is.
    maxTools: Number.MAX_SAFE_INTEGER,
    log: opts.log,
    onUntrustedResult: () => {},
  })
  const rows = new Map(running.map((e) => [e.id, e.row]))
  return Object.values(all.meta)
    .filter((m) => m.name !== SEARCH_TOOL)
    .map((m) => ({
      name: m.name,
      tool: m.tool,
      connectorId: m.connectorId,
      connectorLabel: rows.get(m.connectorId)?.label || m.connectorId,
      connectorType: rows.get(m.connectorId)?.type ?? "",
      class: m.class,
      title: m.title ?? null,
      untrusted: m.untrusted,
      guestSafe: !!m.guestSafe,
    }))
}
