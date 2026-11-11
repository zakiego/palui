// Check-in answers the router handles before the model (like reminder actions):
//   fromCallback  Telegram buttons "rt:<occurrenceId>:<answerId>"
//   fromText      a reply that quotes a check-in message and matches one answer
// A reply that doesn't quote one, or doesn't match exactly, goes to a turn: the
// open check-ins are in the system prompt and the model records it with
// jawab_checkin.
import { and, eq, like } from "drizzle-orm"
import { outbox, type DB } from "@palui/db"
import { answerCheckin, configOf, getOccurrence, getRoutine, matchAnswer, RoutineError } from "./service"

const CALLBACK_RE = /^rt:([A-Za-z0-9_-]+):([a-z0-9_]{1,20})$/

// The owner may answer from the routine's chat or a chat the check-in was
// delivered to (outbox fallback channel).
function allowedChat(db: DB, occurrenceId: string, chatId: string): string | null {
  const occ = getOccurrence(db, occurrenceId)
  if (!occ) return null
  if (occ.chatId === chatId) return occ.chatId
  const delivered = db
    .select({ id: outbox.id })
    .from(outbox)
    .where(and(eq(outbox.source, `routine:${occurrenceId}`), eq(outbox.chatId, chatId)))
    .limit(1)
    .all().length
  return delivered ? occ.chatId : null
}

function record(db: DB, occurrenceId: string, answer: string, chatId: string, now: number, text?: string): string {
  const owner = allowedChat(db, occurrenceId, chatId)
  if (!owner) return "Check-in itu nggak ketemu di chat ini."
  try {
    return answerCheckin(db, { chatId: owner, occurrenceId, answer, text }, now).reply
  } catch (e) {
    if (e instanceof RoutineError) return `Gagal: ${e.message}`
    throw e
  }
}

export const routineActions = {
  async fromCallback(db: DB, data: string, chatId: string, now: number = Date.now()): Promise<string | null> {
    const m = CALLBACK_RE.exec(data)
    if (!m) return null
    return record(db, m[1]!, m[2]!, chatId, now)
  },

  async fromText(db: DB, text: string, replyToChannelMsgId: string | undefined, chatId: string, now: number = Date.now()): Promise<string | null> {
    if (!replyToChannelMsgId || !text.trim()) return null
    const rows = db
      .select({ source: outbox.source, chatId: outbox.chatId })
      .from(outbox)
      .where(and(eq(outbox.channelMsgId, replyToChannelMsgId), like(outbox.source, "routine:%")))
      .all()
    const hit = rows.find((o) => o.chatId === chatId) ?? rows[0]
    if (!hit) return null
    const occurrenceId = hit.source.slice("routine:".length)
    const occ = getOccurrence(db, occurrenceId)
    const routine = occ ? getRoutine(db, occ.routineId) : undefined
    if (!routine) return null
    let action
    try {
      action = configOf(routine).action
    } catch {
      return null
    }
    if (action.kind !== "checkin") return null
    // Not an exact answer ("udah tapi telat"): let the model read it.
    const option = matchAnswer(action.answers, text)
    if (!option) return null
    return record(db, occurrenceId, option.id, chatId, now, text)
  },
}
