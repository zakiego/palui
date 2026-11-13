// Read-side queries for the dashboard pages. Plain drizzle over the shared
// SQLite file; writes go through @palui/core or the bridge modules.
import fs from "node:fs"
import path from "node:path"
import { agents, chats, events, jobs, messages, pendingApprovals, reminders, runs, schema, toolCalls, type DB, type EventLevel } from "@palui/db"
import { and, asc, desc, eq, gte, inArray, isNotNull, isNull, ne, sql, type SQL } from "drizzle-orm"
import { DEFAULT_AGENT_NAME } from "@palui/core/agents"
import { budgetTokens } from "@palui/core/budget/index"
import { DateTime } from "luxon"
import { clip } from "@/lib/format"

export function startOfDay(tz: string, now = Date.now()): number {
  return DateTime.fromMillis(now, { zone: tz }).startOf("day").toMillis()
}

// --- chats ------------------------------------------------------------------------

// A chat's display name: its title, or the channel id when it has none yet.
export function chatTitles(db: DB): Map<string, string> {
  return new Map(db.select({ id: chats.id, title: chats.title, externalId: chats.externalId }).from(chats).all().map((c) => [c.id, c.title || c.externalId]))
}

// Every chat, or only the ones `where` picks (an agent's chats).
export function chatList(db: DB, where?: SQL) {
  const rows = db.select().from(chats).where(where).orderBy(desc(sql`coalesce(${chats.lastMessageAt}, ${chats.createdAt})`)).all()
  return rows.map((c) => {
    const last = db
      .select({ text: messages.text, role: messages.role, createdAt: messages.createdAt })
      .from(messages)
      .where(and(eq(messages.chatId, c.id), inArray(messages.role, ["user", "assistant"])))
      .orderBy(desc(messages.createdAt))
      .limit(1)
      .get()
    return {
      id: c.id,
      channel: c.channel,
      title: c.title || c.externalId,
      kind: c.kind,
      allowed: c.allowed,
      replyMode: c.replyMode,
      ack: c.ack,
      ackEmoji: c.ackEmoji,
      lastAt: last?.createdAt ?? c.lastMessageAt ?? c.createdAt,
      preview: last ? clip(last.text, 140) : "",
      previewRole: last?.role ?? null,
    }
  })
  // Sorted by the time the list shows, so it never reads out of order when
  // lastMessageAt lags the newest message.
  .sort((a, b) => b.lastAt - a.lastAt)
}

export type ToolCallView = {
  id: string
  tool: string
  connectorId: string | null
  class: string
  status: string
  ms: number | null
  args: string
  result: string | null
}

export function transcript(db: DB, chatId: string, limit = 150) {
  const chat = db.select().from(chats).where(eq(chats.id, chatId)).get()
  if (!chat) return null
  const where = chat.sessionId
    ? and(eq(messages.chatId, chatId), eq(messages.sessionId, chat.sessionId))
    : eq(messages.chatId, chatId)
  const rows = db
    .select()
    .from(messages)
    .where(and(where, inArray(messages.role, ["user", "assistant"])))
    .orderBy(desc(messages.createdAt))
    .limit(limit)
    .all()
    .reverse()
  const runIds = [...new Set(rows.map((m) => m.runId).filter((r): r is string => !!r))]
  const calls = runIds.length ? db.select().from(toolCalls).where(inArray(toolCalls.runId, runIds)).orderBy(asc(toolCalls.createdAt)).all() : []
  const byRun = new Map<string, ToolCallView[]>()
  for (const c of calls) {
    const list = byRun.get(c.runId) ?? []
    list.push({
      id: c.id,
      tool: c.tool,
      connectorId: c.connectorId,
      class: c.class,
      status: c.status,
      ms: c.ms,
      args: c.args === null || c.args === undefined ? "" : JSON.stringify(c.args, null, 2).slice(0, 2000),
      result: c.resultPreview,
    })
    byRun.set(c.runId, list)
  }
  // Tool calls show once, above the first assistant message of their run.
  const shown = new Set<string>()
  const items = rows.map((m) => {
    let tools: ToolCallView[] = []
    if (m.role === "assistant" && m.runId && !shown.has(m.runId)) {
      shown.add(m.runId)
      tools = byRun.get(m.runId) ?? []
    }
    // Inbound webhook events are stored as user messages; the UI shows them as events, not as mine.
    const source = m.senderId?.startsWith("hook:") ? ("hook" as const) : null
    // Someone other than the owner in a shared chat (PLAN 19.3).
    const sender = m.role === "user" && !m.fromOwner ? (m.senderName ?? m.senderId ?? "?") : null
    return { id: m.id, role: m.role as "user" | "assistant", source, sender, text: m.text, status: m.status, createdAt: m.createdAt, tools }
  })
  const approvals = db
    .select()
    .from(pendingApprovals)
    .where(and(eq(pendingApprovals.chatId, chatId), eq(pendingApprovals.status, "pending")))
    .orderBy(asc(pendingApprovals.createdAt))
    .all()
    .map((a) => ({ id: a.id, tool: a.tool, class: a.toolClass, argsPreview: a.argsPreview, expiresAt: a.expiresAt }))
  return {
    chat: { id: chat.id, channel: chat.channel, title: chat.title || chat.externalId, kind: chat.kind, agentId: chat.agentId, allowed: chat.allowed, replyMode: chat.replyMode, ack: chat.ack, ackEmoji: chat.ackEmoji, connectorId: chat.connectorId },
    messages: items,
    approvals,
  }
}

// --- reminders --------------------------------------------------------------------

export function reminderLists(db: DB) {
  const titles = new Map(
    db
      .select({ id: chats.id, title: chats.title, channel: chats.channel, externalId: chats.externalId })
      .from(chats)
      .all()
      .map((c) => [c.id, { id: c.id, title: c.title || c.externalId, channel: c.channel }]),
  )
  const view = (r: typeof reminders.$inferSelect) => ({
    id: r.id,
    text: r.text,
    rrule: r.rrule,
    tz: r.tz,
    fireAt: r.fireAt,
    status: r.status,
    lastFiredAt: r.lastFiredAt,
    smart: !!r.smartPrompt,
    chat: titles.get(r.chatId) ?? null,
  })
  const upcoming = db
    .select()
    .from(reminders)
    .where(and(inArray(reminders.status, ["pending", "firing"]), isNull(reminders.rrule)))
    .orderBy(asc(reminders.fireAt))
    .limit(200)
    .all()
    .map(view)
  const recurring = db
    .select()
    .from(reminders)
    .where(and(inArray(reminders.status, ["pending", "firing"]), isNotNull(reminders.rrule)))
    .orderBy(asc(reminders.fireAt))
    .limit(200)
    .all()
    .map(view)
  const done = db
    .select()
    .from(reminders)
    .where(inArray(reminders.status, ["done", "cancelled"]))
    .orderBy(desc(reminders.updatedAt))
    .limit(100)
    .all()
    .map(view)
  // The lists above are capped; these are how many rows there really are.
  const total = (where: SQL | undefined) => Number(db.select({ n: sql<number>`count(*)` }).from(reminders).where(where).get()?.n ?? 0)
  const totals = {
    upcoming: total(and(inArray(reminders.status, ["pending", "firing"]), isNull(reminders.rrule))),
    recurring: total(and(inArray(reminders.status, ["pending", "firing"]), isNotNull(reminders.rrule))),
    done: total(inArray(reminders.status, ["done", "cancelled"])),
  }
  return { upcoming, recurring, done, totals }
}

export function nextReminders(db: DB, n = 3, now = Date.now()) {
  return db
    .select({ id: reminders.id, text: reminders.text, fireAt: reminders.fireAt, tz: reminders.tz, rrule: reminders.rrule })
    .from(reminders)
    .where(and(eq(reminders.status, "pending"), gte(reminders.fireAt, now - 3600_000)))
    .orderBy(asc(reminders.fireAt))
    .limit(n)
    .all()
}

export function chatOptions(db: DB) {
  return db
    .select({ id: chats.id, title: chats.title, channel: chats.channel, externalId: chats.externalId })
    .from(chats)
    .where(eq(chats.allowed, true))
    .orderBy(asc(chats.channel), asc(chats.title))
    .all()
    .map((c) => ({ id: c.id, channel: c.channel, title: c.title || c.externalId }))
}

// --- runs and usage ---------------------------------------------------------------

export function recentRuns(db: DB, limit = 50) {
  const titles = chatTitles(db)
  const jobNames = new Map(db.select({ id: jobs.id, name: jobs.name }).from(jobs).all().map((j) => [j.id, j.name]))
  return db
    .select()
    .from(runs)
    .orderBy(desc(runs.startedAt))
    .limit(limit)
    .all()
    .map((r) => ({
      id: r.id,
      trigger: r.trigger,
      model: r.model,
      status: r.status,
      error: r.error,
      tokensIn: r.tokensIn,
      tokensOut: r.tokensOut,
      tainted: r.tainted,
      startedAt: r.startedAt,
      ms: r.ms,
      chat: r.chatId ? (titles.get(r.chatId) ?? null) : null,
      job: r.jobId ? (jobNames.get(r.jobId) ?? null) : null,
      // The run outlived its chat or job. The page says "Deleted chat", not the id.
      chatGone: !!r.chatId && !titles.has(r.chatId),
      jobGone: !!r.jobId && !jobNames.has(r.jobId),
    }))
}

// One run for its page: the run, the messages it read and wrote, and every
// tool call in order. Null when the id is unknown.
export function runDetail(db: DB, id: string) {
  const r = db.select().from(runs).where(eq(runs.id, id)).get()
  if (!r) return null
  const chat = r.chatId ? db.select({ title: chats.title, externalId: chats.externalId }).from(chats).where(eq(chats.id, r.chatId)).get() : undefined
  const job = r.jobId ? db.select({ name: jobs.name }).from(jobs).where(eq(jobs.id, r.jobId)).get() : undefined
  const msgs = db
    .select({ id: messages.id, role: messages.role, text: messages.text, createdAt: messages.createdAt })
    .from(messages)
    .where(and(eq(messages.runId, id), inArray(messages.role, ["user", "assistant"])))
    .orderBy(asc(messages.createdAt))
    .all()
  const tools: ToolCallView[] = db
    .select()
    .from(toolCalls)
    .where(eq(toolCalls.runId, id))
    .orderBy(asc(toolCalls.createdAt))
    .all()
    .map((c) => ({
      id: c.id,
      tool: c.tool,
      connectorId: c.connectorId,
      class: c.class,
      status: c.status,
      ms: c.ms,
      args: c.args === null || c.args === undefined ? "" : JSON.stringify(c.args, null, 2).slice(0, 2000),
      result: c.resultPreview,
    }))
  return {
    id: r.id,
    trigger: r.trigger,
    model: r.model,
    status: r.status,
    error: r.error,
    resultPreview: r.resultPreview,
    tokensIn: r.tokensIn,
    tokensOut: r.tokensOut,
    tokensCached: r.tokensCached,
    tokensCacheWrite: r.tokensCacheWrite,
    // What the run counted against the daily caps: cache reads at a tenth.
    tokensCounted: budgetTokens(r),
    steps: r.steps,
    tainted: r.tainted,
    startedAt: r.startedAt,
    ms: r.ms,
    chatId: chat ? r.chatId : null,
    jobId: job ? r.jobId : null,
    chat: chat ? chat.title || chat.externalId : null,
    job: job?.name ?? null,
    chatGone: !!r.chatId && !chat,
    jobGone: !!r.jobId && !job,
    prompt: msgs.filter((m) => m.role === "user" && m.text.trim()),
    reply: msgs.filter((m) => m.role === "assistant" && m.text.trim()),
    tools,
  }
}

export function runCount(db: DB): number {
  return Number(db.select({ n: sql<number>`count(*)` }).from(runs).get()?.n ?? 0)
}

export function tokensSince(db: DB, since: number) {
  const row = db
    .select({ tin: sql<number>`coalesce(sum(${runs.tokensIn}), 0)`, tout: sql<number>`coalesce(sum(${runs.tokensOut}), 0)`, n: sql<number>`count(*)` })
    .from(runs)
    .where(gte(runs.startedAt, since))
    .get()
  return { tokensIn: Number(row?.tin ?? 0), tokensOut: Number(row?.tout ?? 0), runs: Number(row?.n ?? 0) }
}

// Tokens per day (in the owner's zone), per model, per agent, per chat and per job over
// the last `days` days. No money: subscriptions don't bill per token.
export function usage(db: DB, tz: string, days = 14, now = Date.now()) {
  const since = DateTime.fromMillis(now, { zone: tz }).startOf("day").minus({ days: days - 1 }).toMillis()
  const rows = db.select().from(runs).where(gte(runs.startedAt, since)).all()
  const perDay = new Map<string, { day: string; tokensIn: number; tokensOut: number }>()
  for (let i = 0; i < days; i++) {
    const day = DateTime.fromMillis(since, { zone: tz }).plus({ days: i }).toISODate()!
    perDay.set(day, { day, tokensIn: 0, tokensOut: 0 })
  }
  const titles = chatTitles(db)
  const jobNames = new Map(db.select({ id: jobs.id, name: jobs.name }).from(jobs).all().map((j) => [j.id, j.name]))
  const agentRows = db.select({ id: agents.id, name: agents.name, isDefault: agents.isDefault }).from(agents).all()
  const agentNames = new Map(agentRows.map((a) => [a.id, a.name]))
  // Runs from before agents (agent_id null) were all the default agent's.
  const defaultAgent = agentRows.find((a) => a.isDefault)?.name ?? DEFAULT_AGENT_NAME
  const bump = (m: Map<string, { key: string; tokens: number; runs: number }>, key: string, tokens: number) => {
    const cur = m.get(key) ?? { key, tokens: 0, runs: 0 }
    cur.tokens += tokens
    cur.runs++
    m.set(key, cur)
  }
  const perModel = new Map<string, { key: string; tokens: number; runs: number }>()
  const perAgent = new Map<string, { key: string; tokens: number; runs: number }>()
  const perChat = new Map<string, { key: string; tokens: number; runs: number }>()
  const perJob = new Map<string, { key: string; tokens: number; runs: number }>()
  for (const r of rows) {
    const day = DateTime.fromMillis(r.startedAt, { zone: tz }).toISODate()!
    const d = perDay.get(day)
    if (d) {
      d.tokensIn += r.tokensIn
      d.tokensOut += r.tokensOut
    }
    const total = r.tokensIn + r.tokensOut
    bump(perModel, r.model ?? "unknown", total)
    bump(perAgent, r.agentId ? (agentNames.get(r.agentId) ?? "") : defaultAgent, total)
    // An agent, chat or job deleted since groups under "", which the page shows as "Deleted chat".
    if (r.chatId) bump(perChat, titles.get(r.chatId) ?? "", total)
    if (r.jobId) bump(perJob, jobNames.get(r.jobId) ?? "", total)
  }
  // The top 20 of each, with how many there were in all so the page can say so.
  const sorted = (m: Map<string, { key: string; tokens: number; runs: number }>) => ({ rows: [...m.values()].sort((a, b) => b.tokens - a.tokens).slice(0, 20), total: m.size })
  return { days: [...perDay.values()], perModel: sorted(perModel), perAgent: sorted(perAgent), perChat: sorted(perChat), perJob: sorted(perJob) }
}

// --- events -----------------------------------------------------------------------

function eventFilter(opts: { connectorId?: string; levels?: EventLevel[] }) {
  const where = []
  if (opts.connectorId) where.push(eq(events.connectorId, opts.connectorId))
  if (opts.levels?.length) where.push(inArray(events.level, opts.levels))
  return where
}

// How many events match the same filters `eventList` takes, with no cap.
export function eventCount(db: DB, opts: { connectorId?: string; levels?: EventLevel[] }): number {
  const where = eventFilter(opts)
  return Number(db.select({ n: sql<number>`count(*)` }).from(events).where(where.length ? and(...where) : undefined).get()?.n ?? 0)
}

export function eventList(db: DB, opts: { connectorId?: string; levels?: EventLevel[]; limit?: number }) {
  const where = eventFilter(opts)
  return db
    .select()
    .from(events)
    .where(where.length ? and(...where) : undefined)
    .orderBy(desc(events.id))
    .limit(opts.limit ?? 200)
    .all()
    .map((e) => ({ id: e.id, at: e.at, level: e.level, connectorId: e.connectorId, kind: e.kind, message: e.message }))
}

// --- jobs -------------------------------------------------------------------------

export function jobList(db: DB, tz: string, now = Date.now()) {
  const titles = chatTitles(db)
  const today = startOfDay(tz, now)
  return db
    .select()
    .from(jobs)
    .orderBy(asc(jobs.createdAt))
    .all()
    .map((j) => {
      const used = db
        .select({ t: sql<number>`coalesce(sum(${runs.tokensIn} + ${runs.tokensOut}), 0)` })
        .from(runs)
        .where(and(eq(runs.jobId, j.id), gte(runs.startedAt, today)))
        .get()
      return { ...j, chatTitle: titles.get(j.chatId) ?? null, tokensToday: Number(used?.t ?? 0) }
    })
}

// --- home checklist ---------------------------------------------------------------

export function firstRunFacts(db: DB) {
  const passkeys = db.select({ n: sql<number>`count(*)` }).from(schema.passkey).get()?.n ?? 0
  const userMessages =
    db
      .select({ n: sql<number>`count(*)` })
      .from(messages)
      .where(and(eq(messages.role, "user"), ne(messages.status, "ignored")))
      .get()?.n ?? 0
  return { passkeys: Number(passkeys), userMessages: Number(userMessages) }
}

export function passkeyList(db: DB, userId: string) {
  return db
    .select({ id: schema.passkey.id, name: schema.passkey.name, deviceType: schema.passkey.deviceType, backedUp: schema.passkey.backedUp, createdAt: schema.passkey.createdAt })
    .from(schema.passkey)
    .where(eq(schema.passkey.userId, userId))
    .orderBy(desc(schema.passkey.createdAt))
    .all()
    .map((p) => ({ ...p, createdAt: p.createdAt?.getTime() ?? null }))
}

export function deletePasskey(db: DB, userId: string, id: string) {
  return db.delete(schema.passkey).where(and(eq(schema.passkey.id, id), eq(schema.passkey.userId, userId))).run().changes > 0
}

// --- backups ----------------------------------------------------------------------

// The newest pre-deploy snapshot in $DATA_DIR/backups, if there is one.
export function latestBackup(dataDir: string): { file: string; bytes: number; at: number } | null {
  const dir = path.join(dataDir, "backups")
  let names: string[]
  try {
    names = fs.readdirSync(dir)
  } catch {
    return null
  }
  let best: { file: string; bytes: number; at: number } | null = null
  for (const name of names) {
    // Skip backupDb's temp files: a half-written snapshot isn't a backup.
    if (name.includes(".tmp-") || !/\.(db|gz|sqlite)(\.gz)?$/.test(name)) continue
    try {
      const st = fs.statSync(path.join(dir, name))
      if (st.isFile() && (!best || st.mtimeMs > best.at)) best = { file: name, bytes: st.size, at: st.mtimeMs }
    } catch {
      // vanished between readdir and stat
    }
  }
  return best
}

