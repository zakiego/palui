// Fake running connectors for flow tests (not imported by production code).
import { tool } from "ai"
import { z } from "zod"
import type { ToolClass, Trust } from "@palui/db"
import type { PaluiTool, TurnInfo } from "../connectors/define"
import type { RunningEntry } from "../connectors/supervisor"

export type FakeTool = {
  class: ToolClass
  untrusted?: boolean
  flowApproval?: "version" | "every_run"
  input?: z.ZodType
  run: (args: Record<string, unknown>, turn: TurnInfo) => unknown | Promise<unknown>
}

export type FakeConnector = { id: string; type?: string; trust?: Trust; tools: Record<string, FakeTool> }

// Every call any fake tool received, with the turn it saw.
export type CallLog = { connectorId: string; tool: string; args: Record<string, unknown>; turn: TurnInfo }[]

export function fakeEntries(connectors: FakeConnector[], log: CallLog = []): RunningEntry[] {
  return connectors.map((c) => ({
    id: c.id,
    row: { id: c.id, type: c.type ?? c.id, kind: "tools", label: c.id, trust: c.trust ?? "own" } as RunningEntry["row"],
    handle: {
      stop: async () => {},
      tools: async (turn: TurnInfo) => {
        const out: Record<string, PaluiTool> = {}
        for (const [name, t] of Object.entries(c.tools)) {
          out[name] = {
            class: t.class,
            untrusted: t.untrusted,
            flowApproval: t.flowApproval,
            tool: tool({
              description: name,
              inputSchema: (t.input ?? z.record(z.string(), z.unknown())) as z.ZodType<Record<string, unknown>>,
              execute: async (args: Record<string, unknown>) => {
                log.push({ connectorId: c.id, tool: name, args, turn })
                return await t.run(args, turn)
              },
            }),
          }
        }
        return out
      },
    },
  }))
}
