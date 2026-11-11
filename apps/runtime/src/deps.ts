// Wires the reminders, scheduler and jobs modules into the engine (EngineDeps).
import type { EngineDeps } from "@palui/core/chat/deps"
import type { TurnOptions, TurnResult } from "@palui/core/chat/turn"
import { flowActions } from "@palui/core/flows/actions"
import { connectorFlowCaller } from "@palui/core/flows/call"
import { createFlowEngine, SILENT_TOKEN as FLOW_SILENT, type PromptRequest, type PromptResponse } from "@palui/core/flows/engine"
import { createJobRunner } from "@palui/core/jobs/runner"
import { meterToBudget } from "@palui/core/models/meter"
import { reminderActions } from "@palui/core/reminders/actions"
import { parseReminder } from "@palui/core/reminders/parse"
import { createScheduler } from "@palui/core/reminders/scheduler"
import { connectorToolCaller } from "@palui/core/tools/call"
import { getSetting } from "@palui/core/settings"
import type { DepsFactory } from "./main"

// A smart reminder counts as fired only when its turn really sent something (or
// is waiting on an approval prompt it sent). Anything else (draining, no model,
// budget pause, an error, an empty answer) throws, so the scheduler sends the
// plain reminder text instead of losing it.
export async function runSmartReminder(runTurn: (o: TurnOptions) => Promise<TurnResult>, r: { chatId: string; text: string; smartPrompt?: string | null }): Promise<void> {
  const res = await runTurn({ chatId: r.chatId, trigger: "reminder", prompt: r.smartPrompt ?? r.text })
  if (res.status === "awaiting_approval") return
  if (res.status === "ok" && res.text.trim()) return
  throw new Error(`smart reminder turn sent nothing (status ${res.status})`)
}

// A flow's prompt step: an unattended turn with trigger "flow" in the flow's
// chat, its own run id, the flow's agent and only the tools the step allowed.
export async function runFlowPrompt(runTurn: (o: TurnOptions) => Promise<TurnResult>, r: PromptRequest): Promise<PromptResponse> {
  const res = await runTurn({
    chatId: r.chatId,
    trigger: "flow",
    prompt: r.prompt,
    runId: r.turnRunId,
    agentId: r.agentId,
    toolNames: r.tools,
    noTools: r.tools.length === 0,
    deliver: r.send,
    silentToken: FLOW_SILENT,
    untrustedInput: r.tainted,
    modelRole: r.modelRole,
  })
  return { status: res.status, text: res.text, runId: res.runId, tainted: res.tainted }
}

export const buildDeps: DepsFactory = ({ db, engine, log, supervisor }) => {
  const flows = createFlowEngine({
    db,
    log: log.child({ component: "flows" }),
    tools: connectorFlowCaller(() => supervisor.running()),
    // Server-side, straight to the connector: never through a turn's tool list.
    readSource: connectorToolCaller(() => supervisor.running(), { tz: () => getSetting(db, "timezone") }),
    sourceUntrusted: (id) => supervisor.running().find((e) => e.id === id)?.row.trust === "external",
    runPrompt: (r) => runFlowPrompt((o) => engine.runTurn(o), r),
  })
  const runner = createJobRunner({
    db,
    log: log.child({ component: "jobs" }),
    runTurn: (opts) => engine.runTurn(opts),
  })
  const scheduler = createScheduler({
    db,
    log: log.child({ component: "scheduler" }),
    onJobDue: async (job) => {
      await runner.run(job)
    },
    // Smart reminders run their prompt as a turn in the reminder's chat.
    onSmartReminder: (r) => runSmartReminder((o) => engine.runTurn(o), r),
    extensions: [flows],
  })
  const deps: EngineDeps = {
    reminderActions,
    flowActions,
    // Parse tokens count toward the daily cap like a turn's.
    parseReminder: (text, tz, now, model) => parseReminder(text, tz, now, meterToBudget(db, model)),
    scheduler: {
      start: () => scheduler.start(),
      stop: async () => scheduler.stop(),
      poke: () => scheduler.poke(),
    },
    jobs: { runNow: (jobId) => runner.runNow(jobId) },
  }
  return deps
}
