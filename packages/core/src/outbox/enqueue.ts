// Writing to the outbox (PLAN 8.7, 6). Every outbound message goes through here;
// the outbox worker (outbox/worker.ts) claims, sends, acks and retries.
import { outbox, newId, type Channel, type DB, type OutboxPayload } from "@palui/db"

export type EnqueueInput = {
  chatId: string
  channel: Channel
  // The agent bot to send through (chats.connectorId); omitted means the channel's main connector.
  connectorId?: string | null
  payload: OutboxPayload
  // "reply:<runId>", "reminder:<id>", "job:<id>", "approval:<id>", "system"
  source: string
  // Give up (status failed) after this time. Reminders: 24 h (PLAN 6).
  expiresAt?: number | null
  // Not before this time (default now).
  notBefore?: number
}

export type OutboxRow = typeof outbox.$inferSelect

export function enqueueOutbox(db: DB, input: EnqueueInput, now = Date.now()): OutboxRow {
  return db
    .insert(outbox)
    .values({
      id: newId("out"),
      chatId: input.chatId,
      channel: input.channel,
      connectorId: input.connectorId || null,
      payload: input.payload,
      status: "pending",
      attempts: 0,
      nextAttemptAt: input.notBefore ?? now,
      source: input.source,
      expiresAt: input.expiresAt ?? null,
      createdAt: now,
    })
    .returning()
    .get()
}

type Listener = () => void
const listeners = new Set<Listener>()

// The worker subscribes so a new row is sent at once instead of on the next tick.
export function onOutboxEnqueued(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function notifyOutbox() {
  for (const l of listeners) l()
}
