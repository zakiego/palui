// The routine engine: the part of the scheduler sweep that fires routines. Each
// step claims its rows with a guarded UPDATE, so two sweeps (or a restart in the
// middle of one) never send the same thing twice:
//
//   resume     paused routines whose pause ended
//   deadlines  open check-ins past their deadline -> missed
//   fire       due routines: one occurrence row per (routine, key), inserted in
//              the same transaction as its outbox row and the cursor advance
//   follow-ups open check-ins due for a re-ask (guarded on the re-ask count)
//   recaps     daily/weekly summaries
//   refresh    tool sources, async: the connector tool is called server-side (not
//              through a turn's tool list) and the result is parsed as data only
import { and, eq, isNotNull, lte, min, notInArray } from "drizzle-orm"
import { newId, routineOccurrences, routines, type DB, type OutboxPayload } from "@palui/db"
import { getChat } from "../chats"
import { logEvent } from "../events"
import type { Logger } from "../log"
import { enqueueOutbox, notifyOutbox } from "../outbox/enqueue"
import { notifyScheduleChanged } from "../reminders/signal"
import { formatClock } from "../reminders/time"
import type { AnswerOption, CheckinAction, RoutineAction } from "./config"
import {
  computeNext,
  configOf,
  occurrencesSince,
  recapText,
  renderTemplate,
  sourceInputOf,
  templateVars,
  nextRecapAt,
  type OccurrenceRow,
  type RoutineRow,
} from "./service"
import { localDate, nextLocalTime, refreshToolSource, upcoming, type Occurrence, type ToolCaller } from "./sources"

export const REFRESH_LEASE_MS = 5 * 60_000
export const REFRESH_BACKOFF_MS = [60_000, 5 * 60_000, 15 * 60_000, 30 * 60_000, 60 * 60_000]
// Tell the owner once a day after this many failed reads in a row.
export const NOTICE_AFTER_FAILURES = 3
export const LATE_NOTE_MS = 2 * 60_000
export const OUTBOX_TTL_MS = 24 * 3600_000
export const RECAP_LATE_MS = 2 * 3600_000

export type RoutineEngineOptions = {
  db: DB
  log: Logger
  now?: () => number
  // Calls a connector tool server-side. Tool sources fail (and retry) without it.
  callTool?: ToolCaller
  // Runs a prompt action as an unattended turn. Throws when nothing was sent.
  onPrompt?: (input: { routine: RoutineRow; occurrence: OccurrenceRow; prompt: string; untrusted: boolean }) => Promise<void>
}

export type RoutineEngine = {
  // Rows a crash left mid-fire go back to idle (they fire again; the key dedupes).
  recover(now: number): void
  // One pass. Returns how many things it acted on.
  sweep(now: number): number
  // The earliest time something is due, for the scheduler's timer.
  nextDue(): number | null
  // Resolves once the refreshes and prompt runs started so far have settled.
  idle(): Promise<void>
}

// Telegram button data: "rt:<occurrenceId>:<answerId>" (at most ~48 bytes).
export function checkinActions(occurrenceId: string, answers: AnswerOption[]): { id: string; label: string }[] {
  return answers.map((a) => ({ id: `rt:${occurrenceId}:${a.id}`, label: a.label }))
}

export function routineSource(occurrenceId: string): string {
  return `routine:${occurrenceId}`
}

export function createRoutineEngine(opts: RoutineEngineOptions): RoutineEngine {
  const { db, log } = opts
  const clock = opts.now ?? (() => Date.now())
  const inFlight = new Set<Promise<void>>()
  // Routines whose source is being read right now: they fire once it's done, on
  // fresh data, instead of on the stale cache.
  const refreshing = new Set<string>()
  let enqueued = 0

  function track(p: Promise<void>) {
    inFlight.add(p)
    void p.finally(() => inFlight.delete(p))
  }

  function send(r: Pick<RoutineRow, "chatId">, payload: OutboxPayload, source: string, now: number, expiresAt = now + OUTBOX_TTL_MS): string | null {
    const chat = getChat(db, r.chatId)
    if (!chat) return null
    const row = enqueueOutbox(db, { chatId: chat.id, channel: chat.channel, payload, source, expiresAt: Math.max(expiresAt, now + 60_000) }, now)
    enqueued++
    return row.id
  }

  // --- resume -----------------------------------------------------------------------

  function resume(now: number): number {
    const due = db
      .select()
      .from(routines)
      .where(and(eq(routines.enabled, false), isNotNull(routines.pausedUntil), lte(routines.pausedUntil, now)))
      .all()
    for (const r of due) {
      const next: RoutineRow = { ...r, enabled: true, pausedUntil: null, cursor: { at: now, key: "" }, nextRefreshAt: r.source.kind === "tool" ? now : r.nextRefreshAt, updatedAt: now }
      try {
        const occ = computeNext(next, now)
        Object.assign(next, { nextFireAt: occ?.at ?? null, nextOcc: occ })
      } catch (e) {
        next.lastError = String((e as Error)?.message ?? e)
      }
      db.update(routines).set(next).where(and(eq(routines.id, r.id), eq(routines.enabled, false))).run()
      logEvent(db, { kind: "routine.resumed", message: `Routine "${r.name}" resumed after its pause ended.`, data: { routineId: r.id } }, now)
    }
    return due.length
  }

  // --- deadlines --------------------------------------------------------------------

  function deadlines(now: number): number {
    const missed = db
      .update(routineOccurrences)
      .set({ status: "missed", nextFollowUpAt: null, updatedAt: now })
      .where(and(eq(routineOccurrences.status, "open"), isNotNull(routineOccurrences.deadlineAt), lte(routineOccurrences.deadlineAt, now)))
      .returning()
      .all()
    for (const o of missed) {
      const r = db.select().from(routines).where(eq(routines.id, o.routineId)).get()
      if (!r || !r.enabled) continue
      try {
        const action = configOf(r).action
        if (action.kind === "checkin" && action.missedText) {
          const text = renderTemplate(action.missedText, templateVars(r, { at: o.scheduledAt, label: o.label }, action, now, o.followUps))
          send(r, { text }, routineSource(o.id), now)
        }
      } catch (e) {
        log.warn("routine missed note failed", { routine_id: r.id, err: e as Error })
      }
    }
    return missed.length
  }

  // --- fire -------------------------------------------------------------------------

  function checkinPayload(r: RoutineRow, action: CheckinAction, occ: Pick<Occurrence, "at" | "fallback">, occurrenceId: string, now: number, text: string): OutboxPayload {
    return { text: decorate(text, r, occ, now), actions: checkinActions(occurrenceId, action.answers) }
  }

  // "(telat, harusnya 04.39)" after downtime, and a note when the times are stale.
  function decorate(text: string, r: RoutineRow, occ: Pick<Occurrence, "at" | "fallback">, now: number): string {
    let out = text
    if (now - occ.at > LATE_NOTE_MS) out += ` (telat, harusnya ${formatClock(occ.at, r.tz)})`
    if (occ.fallback) out += "\n(Jadwal pakai data terakhir yang berhasil diambil, sumbernya lagi error.)"
    return out
  }

  function deadlineFor(action: RoutineAction, occ: Occurrence, following: Occurrence | null): number | null {
    if (action.kind !== "checkin") return null
    if (action.deadline.kind === "after") return occ.at + action.deadline.minutes * 60_000
    return following?.at ?? occ.at + 24 * 3600_000
  }

  function fireOne(r: RoutineRow, now: number) {
    const cfg = configOf(r)
    const occ = r.nextOcc
    if (!occ) {
      db.update(routines).set({ status: "idle", nextFireAt: null, updatedAt: now }).where(eq(routines.id, r.id)).run()
      return
    }
    if (!getChat(db, r.chatId)) {
      db.update(routines).set({ status: "idle", enabled: false, nextFireAt: null, lastError: "The chat no longer exists.", updatedAt: now }).where(eq(routines.id, r.id)).run()
      logEvent(db, { level: "warn", kind: "routine.orphan", message: `Routine "${r.name}" was paused: its chat ${r.chatId} no longer exists.` }, now)
      return
    }
    const cursor = { at: occ.at, key: occ.key }
    const following = upcoming(sourceInputOf(r, cfg), cursor, 1)[0] ?? null
    const action = cfg.action
    const deadlineAt = deadlineFor(action, occ, following)
    const stale = action.kind === "checkin" ? (deadlineAt ?? Infinity) <= now : now - occ.at > r.lateGraceMin * 60_000
    const id = newId("rto")
    const vars = templateVars(r, occ, action, now)
    let runPrompt: { occurrence: OccurrenceRow; prompt: string } | null = null

    db.transaction(() => {
      let status: OccurrenceRow["status"] = stale ? "skipped" : action.kind === "checkin" ? "open" : "sent"
      let nextFollowUpAt: number | null = null
      if (status === "open" && action.kind === "checkin" && action.followUp.max > 0) {
        const at = now + action.followUp.everyMin * 60_000
        if (deadlineAt === null || at < deadlineAt) nextFollowUpAt = at
      }
      const row: OccurrenceRow = {
        id,
        routineId: r.id,
        chatId: r.chatId,
        key: occ.key,
        label: occ.label,
        scheduledAt: occ.at,
        firedAt: now,
        status,
        answer: null,
        answeredAt: null,
        answers: [],
        followUps: 0,
        nextFollowUpAt,
        deadlineAt,
        fallback: !!occ.fallback,
        createdAt: now,
        updatedAt: now,
      }
      const inserted = db.insert(routineOccurrences).values(row).onConflictDoNothing().returning().all()
      if (inserted.length && !stale) {
        if (action.kind === "message") send(r, { text: decorate(renderTemplate(action.text, vars), r, occ, now) }, routineSource(id), now)
        else if (action.kind === "checkin") send(r, checkinPayload(r, action, occ, id, now, renderTemplate(action.text, vars)), routineSource(id), now, deadlineAt ?? undefined)
        else runPrompt = { occurrence: row, prompt: renderTemplate(action.prompt, vars) }
      } else if (!inserted.length) {
        status = "skipped"
        log.info("routine occurrence already handled, not sending again", { routine_id: r.id, key: occ.key })
      }
      const advanced: RoutineRow = { ...r, cursor }
      const next = computeNext(advanced, now, cfg)
      db.update(routines)
        .set({ status: "idle", cursor, nextFireAt: next?.at ?? null, nextOcc: next, updatedAt: now })
        .where(and(eq(routines.id, r.id), eq(routines.status, "firing")))
        .run()
    })

    if (runPrompt) {
      const p = runPrompt as { occurrence: OccurrenceRow; prompt: string }
      track(runPromptAction(r, p.occurrence, p.prompt, cfg.source.kind === "tool"))
    }
  }

  async function runPromptAction(r: RoutineRow, occurrence: OccurrenceRow, prompt: string, untrusted: boolean) {
    try {
      if (!opts.onPrompt) throw new Error("no prompt runner is wired")
      await opts.onPrompt({ routine: r, occurrence, prompt, untrusted })
    } catch (e) {
      log.warn("routine prompt failed", { routine_id: r.id, err: e as Error })
      db.update(routineOccurrences).set({ status: "failed", updatedAt: clock() }).where(eq(routineOccurrences.id, occurrence.id)).run()
    }
  }

  // Several occurrences can be due at once after downtime (stale ones are logged
  // as skipped), so claim again until nothing is due. Bounded per sweep.
  function fire(now: number): number {
    let n = 0
    for (let round = 0; round < 200; round++) {
      const got = fireRound(now)
      n += got
      if (!got) break
    }
    return n
  }

  function fireRound(now: number): number {
    const claimed = db
      .update(routines)
      .set({ status: "firing", updatedAt: now })
      .where(and(eq(routines.status, "idle"), eq(routines.enabled, true), isNotNull(routines.nextFireAt), lte(routines.nextFireAt, now), notInArray(routines.id, [...refreshing])))
      .returning()
      .all()
    for (const r of claimed) {
      try {
        fireOne(r, now)
      } catch (e) {
        // Skip past the broken occurrence so one bad row can't spin the timer.
        log.error("routine fire failed", { routine_id: r.id, err: e as Error })
        const cursor = r.nextOcc ? { at: r.nextOcc.at, key: r.nextOcc.key } : { at: now, key: "" }
        let next: Occurrence | null = null
        try {
          next = computeNext({ ...r, cursor }, now)
        } catch {
          // Leave it without a next time until it's edited.
        }
        db.update(routines)
          .set({ status: "idle", cursor, nextFireAt: next?.at ?? null, nextOcc: next, lastError: String((e as Error)?.message ?? e).slice(0, 300), updatedAt: now })
          .where(eq(routines.id, r.id))
          .run()
      }
    }
    return claimed.length
  }

  // --- follow-ups -------------------------------------------------------------------

  function followUps(now: number): number {
    const due = db
      .select()
      .from(routineOccurrences)
      .where(and(eq(routineOccurrences.status, "open"), isNotNull(routineOccurrences.nextFollowUpAt), lte(routineOccurrences.nextFollowUpAt, now)))
      .all()
    let n = 0
    for (const o of due) {
      const r = db.select().from(routines).where(eq(routines.id, o.routineId)).get()
      try {
        const action = r ? configOf(r).action : null
        if (!r || !r.enabled || action?.kind !== "checkin") {
          db.update(routineOccurrences).set({ nextFollowUpAt: null, updatedAt: now }).where(eq(routineOccurrences.id, o.id)).run()
          continue
        }
        const count = o.followUps + 1
        let nextAt: number | null = count < action.followUp.max ? now + action.followUp.everyMin * 60_000 : null
        if (nextAt !== null && o.deadlineAt !== null && nextAt >= o.deadlineAt) nextAt = null
        db.transaction(() => {
          const changed = db
            .update(routineOccurrences)
            .set({ followUps: count, nextFollowUpAt: nextAt, updatedAt: now })
            .where(and(eq(routineOccurrences.id, o.id), eq(routineOccurrences.status, "open"), eq(routineOccurrences.followUps, o.followUps)))
            .run().changes
          if (!changed) return
          const vars = templateVars(r, { at: o.scheduledAt, label: o.label }, action, now, count)
          const text = action.followUp.text ? renderTemplate(action.followUp.text, vars) : `🔁 ${renderTemplate(action.text, vars)}`
          send(r, { text, actions: checkinActions(o.id, action.answers) }, routineSource(o.id), now, o.deadlineAt ?? undefined)
          n++
        })
      } catch (e) {
        log.error("routine follow-up failed", { occurrence_id: o.id, err: e as Error })
        db.update(routineOccurrences).set({ nextFollowUpAt: null, updatedAt: now }).where(eq(routineOccurrences.id, o.id)).run()
      }
    }
    return n
  }

  // --- recaps -----------------------------------------------------------------------

  function recaps(now: number): number {
    const due = db
      .select()
      .from(routines)
      .where(and(isNotNull(routines.nextRecapAt), lte(routines.nextRecapAt, now)))
      .all()
    let n = 0
    for (const r of due) {
      try {
        const cfg = configOf(r)
        const next = nextRecapAt(cfg.recap, r.tz, now)
        const changed = db
          .update(routines)
          .set({ nextRecapAt: next })
          .where(and(eq(routines.id, r.id), eq(routines.nextRecapAt, r.nextRecapAt as number)))
          .run().changes
        // Paused, off, or hours late after downtime: skip this one.
        if (!changed || !r.enabled || cfg.recap.kind === "off" || now - (r.nextRecapAt as number) > RECAP_LATE_MS) continue
        const period = cfg.recap.kind
        const rows = occurrencesSince(db, { routineId: r.id, since: now - (period === "daily" ? 1 : 7) * 24 * 3600_000 })
        // Nothing ran in the period: nothing to say.
        if (!rows.length) continue
        send(r, { text: recapText(r, rows, period) }, `routine-recap:${r.id}`, now)
        n++
      } catch (e) {
        log.error("routine recap failed", { routine_id: r.id, err: e as Error })
        db.update(routines).set({ nextRecapAt: null }).where(eq(routines.id, r.id)).run()
      }
    }
    return n
  }

  // --- refresh ----------------------------------------------------------------------

  // `now` is the sweep time: the read is for that moment even if the call is slow.
  async function refresh(id: string, now: number) {
    const r = db.select().from(routines).where(eq(routines.id, id)).get()
    if (!r) return
    const cfg = configOf(r)
    if (cfg.source.kind !== "tool") {
      db.update(routines).set({ nextRefreshAt: null }).where(eq(routines.id, id)).run()
      return
    }
    const src = cfg.source
    const call: ToolCaller =
      opts.callTool ??
      (async () => {
        throw new Error("No tool caller is wired in this process.")
      })
    const res = await refreshToolSource(src, r.tz, r.sourceState ?? {}, call, now)
    const fresh = db.select().from(routines).where(eq(routines.id, id)).get()
    // Edited in the meantime: the edit asked for its own refresh.
    if (!fresh || JSON.stringify(fresh.source) !== JSON.stringify(r.source) || fresh.tz !== r.tz) return
    const state = res.state
    let nextRefreshAt: number
    if (!res.error) nextRefreshAt = nextLocalTime(src.refreshAt, r.tz, now)
    else {
      const failures = state.failures ?? 1
      nextRefreshAt = now + REFRESH_BACKOFF_MS[Math.min(failures - 1, REFRESH_BACKOFF_MS.length - 1)]!
      log.warn("routine source read failed", { routine_id: id, failures, error: res.error })
      logEvent(db, { level: "warn", kind: "routine.source_failed", connectorId: src.connectorId, message: `Routine "${r.name}" couldn't read ${src.connectorId}.${src.tool} (try ${failures}): ${res.error}` }, now)
      const today = localDate(now, r.tz)
      if (failures >= NOTICE_AFTER_FAILURES && state.noticeFor !== today && fresh.enabled) {
        const hasData = (state.fetched ?? []).length > 0
        send(
          fresh,
          {
            text: `⚠️ Rutinitas "${r.name}" gagal ambil data dari ${src.connectorId}.${src.tool}. ${hasData ? "Sementara pakai jadwal dari data terakhir yang berhasil." : "Belum ada data, jadi belum bisa jalan."} Aku coba lagi otomatis.`,
          },
          `routine-notice:${id}`,
          now,
        )
        state.noticeFor = today
      }
    }
    const next: RoutineRow = { ...fresh, sourceState: state, lastError: res.error, nextRefreshAt, updatedAt: now }
    if (fresh.status === "idle" && fresh.enabled) {
      const occ = computeNext(next, now, cfg)
      Object.assign(next, { nextFireAt: occ?.at ?? null, nextOcc: occ })
    }
    db.update(routines)
      .set({ sourceState: next.sourceState, lastError: next.lastError, nextRefreshAt: next.nextRefreshAt, nextFireAt: next.nextFireAt, nextOcc: next.nextOcc, updatedAt: now })
      .where(eq(routines.id, id))
      .run()
    if (enqueued) {
      enqueued = 0
      notifyOutbox()
    }
    notifyScheduleChanged()
  }

  function refreshes(now: number): number {
    const due = db
      .select({ id: routines.id, at: routines.nextRefreshAt, enabled: routines.enabled, tz: routines.tz, source: routines.source })
      .from(routines)
      .where(and(isNotNull(routines.nextRefreshAt), lte(routines.nextRefreshAt, now)))
      .all()
    let n = 0
    for (const d of due) {
      // A paused routine reads its source again when it resumes.
      const lease = d.enabled ? now + REFRESH_LEASE_MS : nextLocalTime(String(d.source.refreshAt ?? "00:05"), d.tz, now)
      const changed = db
        .update(routines)
        .set({ nextRefreshAt: lease })
        .where(and(eq(routines.id, d.id), eq(routines.nextRefreshAt, d.at as number)))
        .run().changes
      if (!changed || !d.enabled) continue
      n++
      refreshing.add(d.id)
      track(
        refresh(d.id, now)
          .catch((e) => {
            log.error("routine refresh crashed", { routine_id: d.id, err: e as Error })
          })
          .finally(() => {
            refreshing.delete(d.id)
            notifyScheduleChanged()
          }),
      )
    }
    return n
  }

  // --- wiring -----------------------------------------------------------------------

  const steps: [string, (now: number) => number][] = [
    ["resume", resume],
    ["deadlines", deadlines],
    // Before fire: a due read holds back that routine's fire until it lands.
    ["refresh", refreshes],
    ["fire", fire],
    ["follow-ups", followUps],
    ["recaps", recaps],
  ]

  return {
    recover(now) {
      db.update(routines).set({ status: "idle", updatedAt: now }).where(eq(routines.status, "firing")).run()
    },
    sweep(now) {
      let n = 0
      for (const [name, step] of steps) {
        try {
          n += step(now)
        } catch (e) {
          log.error(`routine ${name} step failed`, { err: e as Error })
        }
      }
      if (enqueued) {
        enqueued = 0
        notifyOutbox()
      }
      return n
    },
    nextDue() {
      const vals = [
        db
          .select({ v: min(routines.nextFireAt) })
          .from(routines)
          .where(and(eq(routines.enabled, true), eq(routines.status, "idle")))
          .get()?.v,
        db.select({ v: min(routines.nextRefreshAt) }).from(routines).get()?.v,
        db.select({ v: min(routines.nextRecapAt) }).from(routines).get()?.v,
        db.select({ v: min(routines.pausedUntil) }).from(routines).where(eq(routines.enabled, false)).get()?.v,
        db.select({ v: min(routineOccurrences.nextFollowUpAt) }).from(routineOccurrences).where(eq(routineOccurrences.status, "open")).get()?.v,
        db.select({ v: min(routineOccurrences.deadlineAt) }).from(routineOccurrences).where(eq(routineOccurrences.status, "open")).get()?.v,
      ].filter((v): v is number => typeof v === "number")
      return vals.length ? Math.min(...vals) : null
    },
    async idle() {
      while (inFlight.size) await Promise.all([...inFlight])
    },
  }
}

