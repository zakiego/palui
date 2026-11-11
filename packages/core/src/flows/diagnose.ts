// explain_flow (RFC flows 10.1): "why didn't X fire?" walks a fixed checklist for
// a flow and a moment and returns the first thing that explains it, plus the
// facts behind it. The checklist lives here, so the answer doesn't depend on the
// model guessing; the model only phrases it.
import { and, desc, eq, gte, lte } from "drizzle-orm"
import { events, flowRuns, hooks, messages, pendingApprovals, type DB } from "@palui/db"
import { formatWhen } from "../reminders/time"
import { upcoming } from "./sources"
import { explainTrace } from "./expr"
import { currentVersion, definitionOf, scheduleInput, stepRunsOf, triggersOf, type FlowRow, type RunRow } from "./service"

// When this process started and last swept, for "was Palui down?".
export const engineClock = { startedAt: Date.now(), lastSweepAt: 0 }

export type Diagnosis = {
  verdict: string
  facts: string[]
  run?: { id: string; status: string; skipReason: string | null; endedAt: string | null; lastStep: string | null; branches: { step: string; branch: string | null; explain?: string }[]; error: string | null }
}

const WINDOW_BEFORE_MS = 6 * 3600_000
const WINDOW_AFTER_MS = 2 * 3600_000

function nearestRun(db: DB, flow: FlowRow, at: number): RunRow | undefined {
  const rows = db
    .select()
    .from(flowRuns)
    .where(and(eq(flowRuns.flowId, flow.id), eq(flowRuns.dryRun, false), gte(flowRuns.startedAt, at - WINDOW_BEFORE_MS - 24 * 3600_000), lte(flowRuns.startedAt, at + WINDOW_AFTER_MS)))
    .orderBy(desc(flowRuns.startedAt))
    .all()
  const t = (r: RunRow) => r.scheduledAt ?? r.startedAt
  return rows.filter((r) => t(r) >= at - WINDOW_BEFORE_MS && t(r) <= at + WINDOW_AFTER_MS).sort((a, b) => Math.abs(t(a) - at) - Math.abs(t(b) - at))[0]
}

const SKIP_TEXT: Record<string, string> = {
  late: "it was skipped as late: Palui was down or busy past the grace period at that time",
  duplicate: "it was a duplicate of a run that already happened",
  paused: "the flow was paused when it came in",
  rate_limited: "the flow hit its runs-per-hour limit",
  budget: "the flow hit its daily token cap",
  not_approved: "it reached a write step of a version the owner hasn't approved",
  chain_depth: "it was an event chain deeper than 3 flows",
}

export function diagnoseFlow(db: DB, flow: FlowRow, at: number, now: number): Diagnosis {
  const facts: string[] = []
  const tz = flow.tz
  const when = (ms: number) => formatWhen(ms, tz, now)
  if (!flow.enabled) {
    return {
      verdict: flow.pausedUntil ? `Flow "${flow.name}" is paused until ${when(flow.pausedUntil)}.` : `Flow "${flow.name}" is paused${flow.lastError ? ` (last error: ${flow.lastError})` : ""}.`,
      facts: flow.consecutiveFailures >= 5 ? [`It paused itself after ${flow.consecutiveFailures} failed runs in a row.`] : [],
    }
  }
  const def = definitionOf(currentVersion(db, flow))
  const run = nearestRun(db, flow, at)
  if (run) {
    const steps = stepRunsOf(db, run.id)
    const branches = steps
      .filter((s) => s.branch)
      .map((s) => ({ step: s.stepId, branch: s.branch, ...(s.kind === "if" ? { explain: String((s.input as { explain?: string })?.explain ?? explainTrace(((s.input as { values?: { expr: string; value: unknown }[] })?.values ?? []) as never)) } : {}) }))
    const last = steps.at(-1)
    const info = {
      id: run.id,
      status: run.status,
      skipReason: run.skipReason,
      endedAt: run.endedAt ? when(run.endedAt) : null,
      lastStep: last?.stepId ?? null,
      branches,
      error: run.error,
    }
    const started = when(run.scheduledAt ?? run.startedAt)
    let verdict: string
    if (run.status === "skipped") verdict = `It ran at ${started}, but ${SKIP_TEXT[run.skipReason ?? ""] ?? `was skipped (${run.skipReason})`}.`
    else if (run.status === "failed") verdict = `It ran at ${started} and failed at step ${last?.stepId ?? "?"}: ${run.error ?? "unknown error"}.`
    else if (run.status === "awaiting_approval") verdict = `It ran at ${started} and is waiting for the owner to approve version changes (step ${last?.stepId ?? "?"}).`
    else if (run.status === "waiting") {
      const w = run.wait as { kind?: string } | null
      const ap = w?.kind === "approval" ? db.select().from(pendingApprovals).where(eq(pendingApprovals.runId, run.id)).orderBy(desc(pendingApprovals.createdAt)).get() : undefined
      verdict = `It ran at ${started} and is waiting (${w?.kind ?? "?"}) at step ${last?.stepId ?? "?"}${run.wakeAt ? ` until ${when(run.wakeAt)}` : ""}${ap ? `, for the owner to approve ${ap.tool}` : ""}.`
    } else {
      const ifs = branches.filter((b) => b.explain)
      const stop = ifs.find((b) => b.branch === "else" && def.steps.find((s) => s.id === b.step && s.kind === "if" && s.else === null))
      verdict = stop
        ? `It ran at ${started}, but stopped at ${stop.step}: ${stop.explain} (the condition was false).`
        : `It ran at ${started} and ${run.status === "done" ? "finished" : run.status}${last ? ` after ${last.stepId}` : ""}.`
    }
    if (run.taint.length) facts.push(`Untrusted data in that run: ${run.taint.join(", ")}.`)
    return { verdict, facts, run: info }
  }

  // No run near that time: what the triggers say.
  const rows = triggersOf(db, flow.id)
  for (const t of def.triggers) {
    const row = rows.find((r) => r.triggerId === t.id)
    if (t.kind === "schedule" && row) {
      let near: string[] = []
      try {
        near = upcoming(scheduleInput(t, tz, row), { at: at - 24 * 3600_000, key: "" }, 40)
          .filter((o) => Math.abs(o.at - at) <= 12 * 3600_000)
          .slice(0, 6)
          .map((o) => `${when(o.at)} (${o.label})`)
      } catch (e) {
        facts.push(`Trigger ${t.id}: the schedule can't be computed: ${(e as Error).message}`)
      }
      facts.push(near.length ? `Trigger ${t.id} occurrences around then: ${near.join(", ")}.` : `Trigger ${t.id} has no occurrence within 12 hours of that time.`)
      const st = row.sourceState ?? {}
      if (st.failures) facts.push(`Trigger ${t.id}: its source failed ${st.failures} time(s) in a row; last error: ${st.lastError ?? row.lastError ?? "?"}.`)
      if (row.nextFireAt) facts.push(`Trigger ${t.id} fires next at ${when(row.nextFireAt)}.`)
      if (row.cursor && row.cursor.key === "" && row.cursor.at > at) facts.push(`Trigger ${t.id} was (re)started after that time (an edit or a resume starts from then).`)
    }
    if (t.kind === "poll" && row) {
      const p = row.pollState ?? {}
      facts.push(`Trigger ${t.id} (poll) last checked ${p.lastCheckedAt ? when(p.lastCheckedAt) : "never"}${p.lastError ? `, last error: ${p.lastError}` : ""}${p.failures ? ` (${p.failures} failures)` : ""}. It fires only when the value ${t.fire === "change" ? "changes" : "is non-empty"}; the first poll only remembers the value.`)
    }
    if (t.kind === "webhook") {
      const hook = db.select().from(hooks).where(eq(hooks.id, t.hookId)).get()
      if (!hook) facts.push(`Trigger ${t.id}: hook ${t.hookId} no longer exists.`)
      else facts.push(`Trigger ${t.id}: hook "${hook.name}" (${hook.enabled ? "on" : "off"}, mode ${hook.mode}) last received something ${hook.lastAt ? when(hook.lastAt) : "never"}.${hook.mode !== "flow" ? " Its mode isn't flow, so it doesn't start this flow." : ""}`)
    }
    if (t.kind === "chat") {
      const msgs = db
        .select({ text: messages.text, at: messages.createdAt })
        .from(messages)
        .where(and(eq(messages.chatId, flow.chatId), eq(messages.fromOwner, true), gte(messages.createdAt, at - WINDOW_BEFORE_MS), lte(messages.createdAt, at + WINDOW_AFTER_MS)))
        .all()
      facts.push(`Trigger ${t.id}: ${msgs.length} owner message(s) in this chat around then; none matched ${t.command ?? (t.phrases ?? []).join(" / ")} exactly.`)
    }
    if (t.kind === "event") facts.push(`Trigger ${t.id}: waits for ${t.event} from ${t.flow === "self" ? "this flow" : `flow "${t.flow}"`}; no such event started a run then.`)
  }
  if (!def.triggers.length) facts.push("The flow has no triggers; it only runs with run_flow.")
  // The engine itself.
  if (at < engineClock.startedAt) {
    const gap = db
      .select({ at: events.at })
      .from(events)
      .where(and(gte(events.at, at - 3600_000), lte(events.at, at + 3600_000)))
      .limit(1)
      .get()
    if (!gap) facts.push(`Palui logged nothing within an hour of that time and restarted at ${when(engineClock.startedAt)}: it was likely down.`)
  }
  if (engineClock.lastSweepAt) facts.push(`The scheduler last swept at ${when(engineClock.lastSweepAt)}.`)
  const verdict = facts.find((f) => f.includes("failed") || f.includes("down") || f.includes("no longer") || f.includes("no occurrence")) ?? `No run of "${flow.name}" near that time.`
  return { verdict, facts }
}
