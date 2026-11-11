// What the router handles for flows before any model sees a message:
//   fromCallback  Telegram buttons "fl:<runId>:<answerId>" (check-in answers) and
//                 "flowver:approve|deny:<versionId>" (version approvals)
//   fromText      a reply quoting a check-in that matches one answer exactly, or
//                 "ya"/"batal" quoting a version prompt
//   fromChat      a `chat` trigger: the owner's command or phrase in the flow's chat
// A reply that doesn't match goes to a turn: open check-ins are in the system
// prompt and the model records them with jawab_checkin.
import { and, eq, gte, like } from "drizzle-orm"
import { outbox, type DB } from "@palui/db"
import { parseYesNo } from "../chat/router"
import { answerFlowCheckin, checkinWaitOf } from "./checkin"
import { matchChatTrigger } from "./entry"
import { definitionOf, decideVersion, FlowError, getFlow, getRun, getVersion, VERSION_APPROVAL_SOURCE } from "./service"
import { matchAnswer } from "../routines/service"

const ANSWER_RE = /^fl:([A-Za-z0-9_-]+):([a-z0-9_]{1,20})$/
const VERSION_RE = /^flowver:(approve|deny):([A-Za-z0-9_-]+)$/
// A bare "ya" answers a version prompt only this soon after it went out.
export const BARE_YES_WINDOW_MS = 30 * 60_000

function record(db: DB, runId: string, answer: string, chatId: string, now: number, text?: string): string {
  try {
    return answerFlowCheckin(db, { chatId, runId, answer, text }, now).reply
  } catch (e) {
    if (e instanceof FlowError) return `Gagal: ${e.message}`
    throw e
  }
}

function version(db: DB, versionId: string, approved: boolean, chatId: string, now: number): string {
  const v = getVersion(db, versionId)
  const flow = v ? getFlow(db, v.flowId) : undefined
  // Only the chat that was asked (the flow's own chat) can answer.
  if (!v || !flow || flow.chatId !== chatId) return "Persetujuan flow itu gak ketemu di chat ini."
  return decideVersion(db, versionId, approved, "chat", now).reply
}

export const flowActions = {
  async fromCallback(db: DB, data: string, chatId: string, now: number = Date.now()): Promise<string | null> {
    const a = ANSWER_RE.exec(data)
    if (a) return record(db, a[1]!, a[2]!, chatId, now)
    const v = VERSION_RE.exec(data)
    if (v) return version(db, v[2]!, v[1] === "approve", chatId, now)
    return null
  },

  async fromText(db: DB, text: string, replyToChannelMsgId: string | undefined, chatId: string, now: number = Date.now()): Promise<string | null> {
    if (!text.trim()) return null
    if (!replyToChannelMsgId) {
      // A bare "ya"/"batal" right after a single version prompt answers it.
      const yn = parseYesNo(text)
      if (yn === null) return null
      const recent = db
        .select({ source: outbox.source })
        .from(outbox)
        .where(and(eq(outbox.chatId, chatId), like(outbox.source, `${VERSION_APPROVAL_SOURCE}%`), gte(outbox.createdAt, now - BARE_YES_WINDOW_MS)))
        .all()
        .map((r) => r.source.slice(VERSION_APPROVAL_SOURCE.length))
        .filter((id) => {
          const v = getVersion(db, id)
          return !!v && v.approvedAt === null && !v.approvedVia && getFlow(db, v.flowId)?.currentVersionId === v.id
        })
      return recent.length === 1 ? version(db, recent[0]!, yn, chatId, now) : null
    }
    const rows = db
      .select({ source: outbox.source, chatId: outbox.chatId })
      .from(outbox)
      .where(eq(outbox.channelMsgId, replyToChannelMsgId))
      .all()
      .filter((o) => o.source.startsWith("flow:") || o.source.startsWith(VERSION_APPROVAL_SOURCE))
    const hit = rows.find((o) => o.chatId === chatId) ?? rows[0]
    if (!hit) return null
    if (hit.source.startsWith(VERSION_APPROVAL_SOURCE)) {
      const yn = parseYesNo(text)
      return yn === null ? null : version(db, hit.source.slice(VERSION_APPROVAL_SOURCE.length), yn, chatId, now)
    }
    const run = getRun(db, hit.source.slice("flow:".length))
    const w = run ? checkinWaitOf(run) : null
    if (!run || !w) return null
    const v = getVersion(db, run.versionId)
    const step = v ? definitionOf(v).steps.find((s) => s.id === w.stepId) : undefined
    if (step?.kind !== "checkin") return null
    // Not an exact answer ("udah tapi telat"): let the model read it.
    const option = matchAnswer(step.answers, text)
    if (!option) return null
    return record(db, run.id, option.id, chatId, now, text)
  },

  async fromChat(db: DB, input: { text: string; chatId: string; messageId: string; fromOwner: boolean }, now: number = Date.now()): Promise<boolean> {
    return matchChatTrigger(db, input, now)
  },
}
