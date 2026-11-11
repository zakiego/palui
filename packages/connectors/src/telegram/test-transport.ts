// A grammY test transport: an API transformer that records every outgoing call
// and answers with canned results, so tests never reach api.telegram.org.
import { Bot } from "grammy"
import type { UserFromGetMe } from "grammy/types"

export const FAKE_ME: UserFromGetMe = {
  id: 999,
  is_bot: true,
  first_name: "Palui",
  username: "palui_test_bot",
  can_join_groups: true,
  can_read_all_group_messages: false,
  supports_inline_queries: false,
  can_connect_to_business: false,
  has_main_web_app: false,
  has_topics_enabled: false,
  allows_users_to_create_topics: false,
} as UserFromGetMe

export class FakeApiError {
  constructor(
    readonly code: number,
    readonly description: string,
  ) {}
}

type Handler = (payload: Record<string, unknown>) => unknown

export type FakeTelegram = {
  calls: { method: string; payload: Record<string, unknown> }[]
  bots: Bot[]
  // Override a method; return a FakeApiError to make Telegram "fail".
  on(method: string, handler: Handler): void
  createBot(token: string): Bot
  callsTo(method: string): Record<string, unknown>[]
}

export function fakeTelegram(): FakeTelegram {
  const calls: FakeTelegram["calls"] = []
  const handlers = new Map<string, Handler>()
  const bots: Bot[] = []
  let nextMessageId = 100

  const defaults: Record<string, Handler> = {
    getMe: () => FAKE_ME,
    deleteWebhook: () => true,
    sendChatAction: () => true,
    answerCallbackQuery: () => true,
    getFile: (p) => ({ file_id: p.file_id, file_unique_id: `u-${String(p.file_id)}`, file_path: `files/${String(p.file_id)}` }),
    sendMessage: (p) => ({ message_id: nextMessageId++, date: 0, chat: { id: Number(p.chat_id), type: "private", first_name: "X" }, text: p.text }),
  }

  const fake: FakeTelegram = {
    calls,
    bots,
    on(method, handler) {
      handlers.set(method, handler)
    },
    callsTo(method) {
      return calls.filter((c) => c.method === method).map((c) => c.payload)
    },
    createBot(token) {
      const bot = new Bot(token, { botInfo: FAKE_ME })
      bot.api.config.use(async (_prev, method, payload, signal) => {
        const p = (payload ?? {}) as Record<string, unknown>
        if (method === "getUpdates" && !handlers.has("getUpdates")) {
          // Long polling: park until the bot stops (the abort) and return nothing.
          if (signal && !signal.aborted) {
            await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }))
            throw new Error("aborted")
          }
          return { ok: true, result: [] } as never
        }
        calls.push({ method, payload: p })
        const handler = handlers.get(method) ?? defaults[method]
        const result = handler ? await handler(p) : true
        if (result instanceof FakeApiError) return { ok: false, error_code: result.code, description: result.description } as never
        return { ok: true, result } as never
      })
      bots.push(bot)
      return bot
    },
  }
  return fake
}
