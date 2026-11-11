// Shared fixtures for reminder and job tests (not imported by production code).
import { memoryDb, type DB } from "@palui/db"
import { ensureChat } from "../chats"

// Tuesday 2026-11-10 10:00 WITA (Asia/Makassar, UTC+8).
export const NOW = Date.parse("2026-11-10T02:00:00Z")
export const WITA = "Asia/Makassar"
export const MIN = 60_000
export const HOUR = 3600_000

export function setup(): { db: DB; chatId: string; otherChatId: string } {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const other = ensureChat(db, { channel: "whatsapp", externalId: "628123@s.whatsapp.net" }, NOW)
  return { db, chatId: chat.id, otherChatId: other.id }
}

// A fake clock plus timers that record what was armed, for driving the scheduler.
export function fakeClock(start = NOW) {
  let now = start
  type T = { id: number; fn: () => void; at: number; every?: number }
  let seq = 0
  const pending = new Map<number, T>()
  const armed: number[] = []
  return {
    now: () => now,
    set(t: number) {
      now = t
    },
    armed,
    pending,
    timers: {
      setTimeout: (fn: () => void, ms: number) => {
        armed.push(ms)
        const id = ++seq
        pending.set(id, { id, fn, at: now + ms })
        return id
      },
      clearTimeout: (h: never) => {
        pending.delete(h as unknown as number)
      },
      setInterval: (fn: () => void, ms: number) => {
        const id = ++seq
        pending.set(id, { id, fn, at: now + ms, every: ms })
        return id
      },
      clearInterval: (h: never) => {
        pending.delete(h as unknown as number)
      },
    },
    // Advance time, running due timers in order. Returns how many callbacks ran.
    async advance(ms: number, flush: () => Promise<void> = async () => {}) {
      const end = now + ms
      let ran = 0
      for (;;) {
        const due = [...pending.values()].filter((t) => t.at <= end).sort((a, b) => a.at - b.at)[0]
        if (!due) break
        now = Math.max(now, due.at)
        if (due.every) due.at += due.every
        else pending.delete(due.id)
        due.fn()
        ran++
        await flush()
        if (ran > 10_000) throw new Error("timer loop")
      }
      now = end
      return ran
    },
  }
}
