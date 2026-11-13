// Scheduled and repeating sends to contacts, swept on the reminder scheduler's
// timer (a SchedulerExtension, like flows). A row is claimed scheduled -> firing in
// one UPDATE ... RETURNING, so two sweeps never send the same occurrence twice.
// Every fire runs the same guards as a send from chat: kill switch, contact still
// saved, rate limits. No model runs here; the text is the one approved at creation.
import { and, eq, lte, min } from "drizzle-orm"
import { outboundMessages, type DB } from "@palui/db"
import { logEvent } from "../events"
import type { Logger } from "../log"
import { notifyOutbox, enqueueOutbox } from "../outbox/enqueue"
import { getChat } from "../chats"
import type { SchedulerExtension } from "../reminders/scheduler"
import { nextOccurrence } from "../reminders/time"
import { assertCanSend, getContact, noticeToOwner, OUTBOUND_LATE_SKIP_MS, OUTBOUND_TTL_MS, OutboundError, sourceOf, type OutboundRow } from "./service"

export function createOutboundScheduler(opts: { db: DB; log: Logger }): SchedulerExtension {
  const { db, log } = opts

  // One-off -> done; series -> next occurrence after now (missed runs collapse).
  function advance(r: OutboundRow, now: number, outboxId: string | null) {
    const set: Partial<OutboundRow> = { updatedAt: now }
    if (outboxId) set.lastOutboxId = outboxId
    let next: number | null = null
    if (r.rrule) {
      try {
        next = nextOccurrence({ rrule: r.rrule, tz: r.tz, localStart: r.localStart }, now)
      } catch (e) {
        log.error("outbound rule broke, stopping the series", { outbound_id: r.id, err: e as Error })
      }
    }
    if (next === null) set.status = "done"
    else Object.assign(set, { status: "scheduled", fireAt: next })
    db.update(outboundMessages)
      .set(set)
      .where(and(eq(outboundMessages.id, r.id), eq(outboundMessages.status, "firing")))
      .run()
  }

  function fire(r: OutboundRow, now: number): boolean {
    const contact = getContact(db, r.contactId)
    const who = contact?.name ?? "kontak"
    if (!contact || contact.removedAt) {
      db.update(outboundMessages).set({ status: "cancelled", updatedAt: now }).where(eq(outboundMessages.id, r.id)).run()
      logEvent(db, { kind: "outbound.orphan", level: "warn", message: `Scheduled message ${r.id} cancelled: its contact is gone`, data: { outboundId: r.id } }, now)
      return false
    }
    if (now - r.fireAt > OUTBOUND_LATE_SKIP_MS) {
      advance(r, now, null)
      noticeToOwner(db, r.ownerChatId, `Pesan terjadwal ke ${who} kelewat (Palui mati waktu jadwalnya), jadi nggak aku kirim:\n\n${r.text}`, "system", {}, now)
      return false
    }
    try {
      assertCanSend(db, contact, now)
    } catch (e) {
      if (!(e instanceof OutboundError)) throw e
      advance(r, now, null)
      logEvent(db, { kind: "outbound.skipped", level: "warn", message: `Scheduled message to ${who} skipped: ${e.message}`, data: { outboundId: r.id } }, now)
      noticeToOwner(db, r.ownerChatId, `Pesan terjadwal ke ${who} nggak aku kirim: ${e.message}`, "system", {}, now)
      return false
    }
    const chat = getChat(db, contact.chatId)
    if (!chat) {
      db.update(outboundMessages).set({ status: "cancelled", updatedAt: now }).where(eq(outboundMessages.id, r.id)).run()
      noticeToOwner(db, r.ownerChatId, `Pesan terjadwal ke ${who} dibatalin: chat-nya udah nggak ada.`, "system", {}, now)
      return false
    }
    db.transaction(() => {
      const row = enqueueOutbox(
        db,
        { chatId: chat.id, channel: chat.channel, connectorId: chat.connectorId || null, payload: { text: r.text, format: "plain" }, source: sourceOf(r.id), expiresAt: now + OUTBOUND_TTL_MS },
        now,
      )
      advance(r, now, row.id)
    })
    return true
  }

  return {
    recover(now) {
      db.update(outboundMessages).set({ status: "scheduled", updatedAt: now }).where(eq(outboundMessages.status, "firing")).run()
    },
    sweep(now) {
      const claimed = db
        .update(outboundMessages)
        .set({ status: "firing", updatedAt: now })
        .where(and(eq(outboundMessages.status, "scheduled"), lte(outboundMessages.fireAt, now)))
        .returning()
        .all()
      let sent = 0
      for (const r of claimed) {
        try {
          if (fire(r, now)) sent++
        } catch (e) {
          log.error("outbound fire failed", { outbound_id: r.id, err: e as Error })
          db.update(outboundMessages).set({ status: "scheduled" }).where(and(eq(outboundMessages.id, r.id), eq(outboundMessages.status, "firing"))).run()
        }
      }
      if (sent) notifyOutbox()
      return claimed.length
    },
    nextDue() {
      return db.select({ v: min(outboundMessages.fireAt) }).from(outboundMessages).where(eq(outboundMessages.status, "scheduled")).get()?.v ?? null
    },
  }
}
