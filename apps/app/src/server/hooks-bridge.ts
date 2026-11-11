// Inbound hook management (PLAN 5.5), from the core hooks module. The CLI
// (`palui hook create`) and the Settings page call these.
import { createHook as coreCreateHook, listHooks as coreListHooks, type HookMode, type HookView } from "@palui/core/hooks/index"
import type { DB } from "@palui/db"

export type HookRow = HookView
export type { HookMode }

// Returns the plain secret once; only the encrypted form is stored.
export function createHook(
  db: DB,
  input: { name: string; chatId: string; mode?: HookMode },
  masterKey: string,
  now = Date.now(),
): { hook: HookRow; secret: string } {
  return coreCreateHook(db, { name: input.name, chatId: input.chatId, mode: input.mode ?? "forward" }, masterKey, now)
}

export function listHooks(db: DB): HookRow[] {
  return coreListHooks(db)
}
