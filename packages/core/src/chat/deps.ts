// Parts of the engine built elsewhere (reminders, jobs, scheduler) are injected
// so the engine compiles and runs without them. The runtime main wires them.
import type { LanguageModel } from "ai"
import type { DB } from "@palui/db"
import type { ParsedReminder } from "../control"

export type ReminderActions = {
  // Telegram button data starting with "rem:". Returns the reply, or null when it isn't a reminder action.
  fromCallback(db: DB, data: string, chatId: string, now?: number): Promise<string | null>
  // WhatsApp-style text actions ("done", "tunda 1j") quoting a reminder. Null when it isn't one.
  fromText(db: DB, text: string, replyToChannelMsgId: string | undefined, chatId: string, now?: number): Promise<string | null>
}

export type EngineDeps = {
  reminderActions?: ReminderActions
  parseReminder?: (text: string, tz: string, now: number, model: LanguageModel) => Promise<ParsedReminder>
  scheduler?: { start(): void | Promise<void>; stop(): Promise<void>; poke(): void }
  jobs?: { runNow(jobId: string): Promise<{ ok: boolean; runId?: string; detail?: string }> }
}
