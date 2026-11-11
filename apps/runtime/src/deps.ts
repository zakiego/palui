// Wires the reminders, scheduler and jobs modules into the engine (EngineDeps).
import type { EngineDeps } from "@palui/core/chat/deps"
import type { TurnOptions, TurnResult } from "@palui/core/chat/turn"
import { createJobRunner } from "@palui/core/jobs/runner"
import { meterToBudget } from "@palui/core/models/meter"
import { reminderActions } from "@palui/core/reminders/actions"
import { parseReminder } from "@palui/core/reminders/parse"
import { createScheduler } from "@palui/core/reminders/scheduler"
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

export const buildDeps: DepsFactory = ({ db, engine, log }) => {
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
  })
  const deps: EngineDeps = {
    reminderActions,
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
