// Server-side tool calls for flow steps (RFC flows 5.1, closing the two gaps in
// section 2). Unlike a routine source, a flow step:
//   1. resolves the tool's class from the connector's tool list BEFORE calling it,
//      so a write or destructive tool is never run by accident, and
//   2. passes the flow's chat (and the flow id) in the tool context, so chat-scoped
//      built-in tools (reminders, notes, flows) act on the right chat.
// The turn's maxTools and tool search don't apply: a flow may use any enabled tool.
import { asSchema } from "ai"
import type { PaluiTool, TurnInfo } from "../connectors/define"
import type { RunningEntry } from "../connectors/supervisor"
import { namespaced, normalizeClass } from "../tools/collect"
import type { ToolInfo } from "./validate"

export class FlowToolError extends Error {}

export const STEP_TIMEOUT_MS = 20_000
export const MAX_STEP_TIMEOUT_MS = 60_000

export type FlowToolCaller = {
  // Every tool of every running connector, for save-time checks.
  list(turn: TurnInfo): Promise<ToolInfo[]>
  // One tool, by namespaced name or connector id + bare name. Null when missing.
  resolve(ref: { tool: string; connectorId?: string }, turn: TurnInfo): Promise<ToolInfo | null>
  // Calls a resolved tool. The caller has already decided the class is allowed.
  call(info: ToolInfo, args: Record<string, unknown>, turn: TurnInfo, timeoutMs?: number): Promise<unknown>
}

export function withTimeout<T>(p: Promise<T>, ms: number, what: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const t = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new FlowToolError(`${what} took longer than ${Math.round(ms / 1000)} s.`)), ms)
    timer.unref?.()
  })
  return Promise.race([p, t]).finally(() => clearTimeout(timer))
}

function infoOf(e: RunningEntry, name: string, pt: PaluiTool): ToolInfo {
  return {
    name: namespaced(e.id, name),
    connectorId: e.id,
    tool: name,
    class: normalizeClass(pt.class),
    untrusted: !!pt.untrusted || e.row.trust === "external",
    connectorType: e.row.type,
    flowApproval: pt.flowApproval ?? "version",
  }
}

async function toolsOf(e: RunningEntry, turn: TurnInfo): Promise<Record<string, PaluiTool>> {
  if (!e.handle.tools) return {}
  try {
    return (await e.handle.tools(turn)) ?? {}
  } catch {
    return {}
  }
}

// Validates args against the tool's input schema when the schema can say so.
async function checkInput(pt: PaluiTool, args: Record<string, unknown>, what: string) {
  const raw = pt.tool.inputSchema
  if (!raw) return
  let schema: ReturnType<typeof asSchema>
  try {
    schema = asSchema(raw as Parameters<typeof asSchema>[0])
  } catch {
    return
  }
  if (!schema.validate) return
  const r = await schema.validate(args)
  if (!r.success) {
    const msg = (r.error as Error)?.message ?? String(r.error)
    throw new FlowToolError(`${what}: the args don't match the tool's input (${msg.replace(/\s+/g, " ").slice(0, 300)}).`)
  }
}

export function connectorFlowCaller(running: () => RunningEntry[]): FlowToolCaller {
  async function list(turn: TurnInfo, only?: string): Promise<{ info: ToolInfo; pt: PaluiTool }[]> {
    const out: { info: ToolInfo; pt: PaluiTool }[] = []
    for (const e of running()) {
      if (only && e.id !== only) continue
      for (const [name, pt] of Object.entries(await toolsOf(e, turn))) if (pt?.tool) out.push({ info: infoOf(e, name, pt), pt })
    }
    return out
  }

  async function find(ref: { tool: string; connectorId?: string }, turn: TurnInfo) {
    if (ref.connectorId) {
      const all = await list(turn, ref.connectorId)
      return all.find((x) => x.info.tool === ref.tool) ?? all.find((x) => x.info.name === ref.tool) ?? null
    }
    // A namespaced name: try the connector it names first, then everything.
    const sep = ref.tool.indexOf("__")
    if (sep > 0) {
      const hit = (await list(turn, ref.tool.slice(0, sep))).find((x) => x.info.name === ref.tool)
      if (hit) return hit
    }
    return (await list(turn)).find((x) => x.info.name === ref.tool) ?? null
  }

  return {
    async list(turn) {
      return (await list(turn)).map((x) => x.info)
    },
    async resolve(ref, turn) {
      return (await find(ref, turn))?.info ?? null
    },
    async call(info, args, turn, timeoutMs = STEP_TIMEOUT_MS) {
      const hit = (await list(turn, info.connectorId)).find((x) => x.info.tool === info.tool)
      const what = `${info.connectorId}.${info.tool}`
      if (!hit?.pt.tool.execute) throw new FlowToolError(`${what} doesn't exist or is turned off.`)
      // The class is checked again here, right before the call: it may have changed.
      if (normalizeClass(hit.pt.class) !== info.class) throw new FlowToolError(`${what} is now classified ${normalizeClass(hit.pt.class)}, not ${info.class}.`)
      if (info.class === "destructive") throw new FlowToolError(`${what} is destructive; flows never run destructive tools.`)
      await checkInput(hit.pt, args, what)
      const exec = hit.pt.tool.execute as unknown as (input: unknown, o: { toolCallId: string; messages: [] }) => Promise<unknown>
      return await withTimeout(Promise.resolve(exec(args, { toolCallId: `${turn.runId}`, messages: [] })), Math.min(timeoutMs, MAX_STEP_TIMEOUT_MS), what)
    },
  }
}

// The JSON a tool step outputs: MCP structuredContent, the parsed text content
// (or the text itself when it isn't JSON), or the value as is. An error result
// throws with its text, which the step marks tainted when the tool is untrusted.
export function stepData(output: unknown): unknown {
  if (output && typeof output === "object") {
    const o = output as { isError?: boolean; structuredContent?: unknown; content?: unknown }
    const texts = Array.isArray(o.content)
      ? (o.content as { type?: string; text?: string }[])
          .filter((c) => c?.type === "text" && typeof c.text === "string")
          .map((c) => c.text as string)
          .join("\n")
      : null
    if (o.isError) throw new FlowToolError(`The tool returned an error: ${(texts ?? "no detail").replace(/\s+/g, " ").slice(0, 300)}`)
    if (o.structuredContent && typeof o.structuredContent === "object") return o.structuredContent
    if (texts !== null) {
      try {
        return JSON.parse(texts)
      } catch {
        return texts
      }
    }
  }
  return output
}
