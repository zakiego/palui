// Reminders page.
import { noInput, withInput } from "./client"
import type { Channel, ChatOption, ReminderStatus } from "./types"

export type ReminderItem = {
  id: string
  text: string
  rrule: string | null
  tz: string
  fireAt: number
  status: ReminderStatus
  lastFiredAt: number | null
  smart: boolean
  chat: { id: string; title: string; channel: Channel } | null
}

export type RemindersPage = {
  upcoming: ReminderItem[]
  recurring: ReminderItem[]
  done: ReminderItem[]
  // The lists are capped; these are how many rows there really are.
  totals: { upcoming: number; recurring: number; done: number }
  chats: ChatOption[]
  defaultChatId: string
  timezone: string
}

// The runtime's parse of a typed reminder (control.ts ParsedReminder).
export type ParsedReminder = {
  ok: boolean
  text: string
  fireAtIso?: string
  fireAt?: number
  rrule?: string | null
  tz: string
  echo?: string
  error?: string
}

export type CreateReminderInput = { chatId: string; text: string; fireAtIso: string; tz: string; rrule?: string | null }

export const getReminders = noInput<RemindersPage>("getReminders", "GET")
export const parseReminder = withInput<{ text: string; tz?: string }, ParsedReminder>("parseReminder", "POST")
export const createReminderFn = withInput<CreateReminderInput, { id: string }>("createReminderFn", "POST")
export const cancelReminderFn = withInput<{ id: string }, { ok: true }>("cancelReminderFn", "POST")
export const snoozeReminderFn = withInput<{ id: string; preset: "10m" | "1h" | "tomorrow" }, { fireAt: number }>("snoozeReminderFn", "POST")
