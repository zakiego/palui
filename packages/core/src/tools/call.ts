// Calls a connector tool server-side, outside any turn: flow schedule sources and polls read
// their data on a timer, so they must not depend on which tools a turn happened
// to collect. Only `read` tools are allowed: nobody approves these calls.
import type { RunningEntry } from "../connectors/supervisor"
import { normalizeClass } from "./collect"
import type { ToolCaller } from "../flows/sources"

export class ToolCallError extends Error {}

export const TOOL_CALL_TIMEOUT_MS = 20_000

function withTimeout<T>(p: Promise<T>, ms: number, what: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const t = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new ToolCallError(`${what} took longer than ${Math.round(ms / 1000)} s.`)), ms)
    timer.unref?.()
  })
  return Promise.race([p, t]).finally(() => clearTimeout(timer))
}

export function connectorToolCaller(running: () => RunningEntry[], opts: { tz: () => string; timeoutMs?: number } = { tz: () => "UTC" }): ToolCaller {
  return async (connectorId, toolName, args) => {
    const entry = running().find((e) => e.id === connectorId)
    if (!entry) throw new ToolCallError(`Connector ${connectorId} isn't running. Turn it on in Connectors.`)
    const what = `${connectorId}.${toolName}`
    const timeout = opts.timeoutMs ?? TOOL_CALL_TIMEOUT_MS
    const ensureRead = (cls: unknown) => {
      if (normalizeClass(cls) !== "read") throw new ToolCallError(`${what} is classified ${normalizeClass(cls)}; only read tools can feed a flow source. Mark it read on the Connectors page if it only reads.`)
    }
    if (entry.handle.callTool) {
      return await withTimeout(
        entry.handle.callTool(toolName, args).then((r) => {
          ensureRead(r.class)
          return r.output
        }),
        timeout,
        what,
      )
    }
    if (!entry.handle.tools) throw new ToolCallError(`Connector ${connectorId} has no tools.`)
    const now = Date.now()
    const tools = await entry.handle.tools({ runId: `source_${now}`, chatId: "", channel: null, trigger: "schedule", tz: opts.tz(), now })
    const pt = tools[toolName]
    if (!pt?.tool.execute) throw new ToolCallError(`${what} doesn't exist or is turned off.`)
    ensureRead(pt.class)
    const exec = pt.tool.execute as unknown as (input: unknown, o: { toolCallId: string; messages: [] }) => Promise<unknown>
    return await withTimeout(Promise.resolve(exec(args, { toolCallId: `source_${now}`, messages: [] })), timeout, what)
  }
}
