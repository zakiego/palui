// Exports M4 scheduler goldens from the real TS code into
// apps/server/testdata/schedulers.json:
//   prompts     buildSystemPrompt for several zones and times
//   wire        the HTTP request parseReminder sends (Anthropic with native
//               structured output, Anthropic with the JSON tool fallback,
//               OpenAI-compatible) and the parse result for canned replies
//   finish      finishParse on fixed model fields (the parse tests plus every
//               eval phrase's expected fields at the eval's "now")
//   parseErrors parseReminder results for broken replies and refused input
//   echoes      reminderEcho / reminderNext / reminderMessage on fixed rows
//   actions     parseActionText, and the replies of a scripted run of the
//               Done / Snooze handlers
//   presets     the job presets, their schedule descriptions and next runs
//   outbound    contact labels, signatures, hold notices, cancel replies and
//               the notices the outbound scheduler and the outbox hook send
//   trace       a scheduler run across the America/New_York DST change with
//               reminders (plain, smart, series), a job and a scheduled send to
//               a contact: rows and outbox payloads after each time step
//
// Ids are normalized like chat.json: every newId() id becomes <prefix>#N in
// order of first appearance in the canonical (sorted-key) JSON of its section.
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/schedulers.ts
import { writeFileSync } from "node:fs"
import { createRequire } from "node:module"
import path from "node:path"
import { memoryDb, newId, runs, type DB } from "../../../../packages/db/src/index.ts"
import { ensureChat } from "../../../../packages/core/src/chats.ts"
import { silentLogger } from "../../../../packages/core/src/log.ts"
import { DEFAULT_SETTINGS, setSetting } from "../../../../packages/core/src/settings.ts"
import { enqueueOutbox } from "../../../../packages/core/src/outbox/enqueue.ts"
import { buildSystemPrompt, finishParse, parseReminder } from "../../../../packages/core/src/reminders/parse.ts"
import { parseActionText, reminderActions } from "../../../../packages/core/src/reminders/actions.ts"
import { createScheduler, reminderActions as reminderButtons, reminderMessage } from "../../../../packages/core/src/reminders/scheduler.ts"
import { createReminder, markDone, reminderEcho, reminderNext, snoozeReminder } from "../../../../packages/core/src/reminders/service.ts"
import { fakeClock } from "../../../../packages/core/src/reminders/test-helpers.ts"
import { localToUtc } from "../../../../packages/core/src/reminders/time.ts"
import { EVAL_NOW_LOCAL, EVAL_TZ, PHRASES } from "../../../../packages/core/src/reminders/eval/phrases.ts"
import { dailyBriefing, HEARTBEAT_RRULE, heartbeat } from "../../../../packages/core/src/jobs/presets.ts"
import { createJobRunner } from "../../../../packages/core/src/jobs/runner.ts"
import { createJob, describeJobSchedule, describeNextRun } from "../../../../packages/core/src/jobs/service.ts"
import {
  addContact,
  cancelOutbound,
  contactLabel,
  finalText,
  holdNotice,
  outboundFailed,
  saveOutboundSettings,
  scheduleSend,
  sendNow,
} from "../../../../packages/core/src/outbound/service.ts"
import { createOutboundScheduler } from "../../../../packages/core/src/outbound/scheduler.ts"
import { formatPhone, normalizePhone, phonesIn } from "../../../../packages/core/src/outbound/phone.ts"

const coreRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/core/package.json"))
const connRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/connectors/package.json"))
const dbRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/db/package.json"))
const { sql } = dbRequire("drizzle-orm") as typeof import("drizzle-orm")
const { createAnthropic } = connRequire("@ai-sdk/anthropic") as typeof import("@ai-sdk/anthropic")
const { createOpenAICompatible } = connRequire("@ai-sdk/openai-compatible") as typeof import("@ai-sdk/openai-compatible")
void coreRequire

const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"
const NY = "America/New_York"
const MIN = 60_000
const HOUR = 3600_000

// --- normalization ---------------------------------------------------------------------

function canonical(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(canonical)
  if (v && typeof v === "object") {
    const out: Record<string, unknown> = {}
    for (const k of Object.keys(v).sort()) {
      const x = (v as Record<string, unknown>)[k]
      if (x !== undefined) out[k] = canonical(x)
    }
    return out
  }
  return v
}

function normalize(v: unknown): unknown {
  let s = JSON.stringify(canonical(v))
  const seen = new Map<string, string>()
  s = s.replace(/\b([a-z]{2,6})_[0-9a-z]{19}\b/g, (m, p: string) => {
    if (!seen.has(m)) seen.set(m, `${p}#${seen.size + 1}`)
    return seen.get(m)!
  })
  return JSON.parse(s)
}

function rows(db: DB, table: string, where = "") {
  return db.all(sql.raw(`SELECT * FROM ${table} ${where} ORDER BY rowid`)) as Record<string, unknown>[]
}

// --- prompts ---------------------------------------------------------------------------

const PROMPT_CASES: { tz: string; now: number }[] = [
  { tz: WITA, now: NOW },
  { tz: "Asia/Jakarta", now: Date.parse("2026-12-31T16:30:00Z") },
  { tz: NY, now: Date.parse("2026-11-01T05:30:00Z") },
  { tz: NY, now: Date.parse("2026-03-08T06:59:00Z") },
  { tz: "Europe/London", now: Date.parse("2028-02-28T23:15:00Z") },
  { tz: "UTC", now: Date.parse("2026-11-15T00:00:00Z") },
]
const prompts = PROMPT_CASES.map((c) => ({ ...c, prompt: buildSystemPrompt(c.tz, c.now) }))

// --- wire ------------------------------------------------------------------------------

type Rec = { url: string; headers: Record<string, string>; body: unknown }
const BASE = "http://gateway.test:8317"

function anthropicReply(content: unknown[], stop = "end_turn") {
  return { id: "msg_1", type: "message", role: "assistant", model: "m", content, stop_reason: stop, stop_sequence: null, usage: { input_tokens: 10, output_tokens: 20 } }
}

function recorder(sink: Rec[], reply: () => Response) {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers: Record<string, string> = {}
    new Headers(init?.headers).forEach((v, k) => {
      headers[k] = v
    })
    sink.push({ url: String(input), headers, body: JSON.parse(String(init?.body)) })
    return reply()
  }) as typeof fetch
}

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "content-type": "application/json" } })

type Provider = "anthropic" | "openai"
function model(provider: Provider, modelId: string, mode: "text" | "tool", replyText: string, sink: Rec[]) {
  const reply = () => {
    if (provider === "openai") {
      return json({ id: "c1", object: "chat.completion", created: 1, model: modelId, choices: [{ index: 0, message: { role: "assistant", content: replyText }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 20 } })
    }
    if (mode === "tool") return json(anthropicReply([{ type: "tool_use", id: "toolu_1", name: "json", input: JSON.parse(replyText) }], "tool_use"))
    return json(anthropicReply([{ type: "text", text: replyText }]))
  }
  const f = recorder(sink, reply)
  if (provider === "anthropic") return createAnthropic({ baseURL: `${BASE}/v1`, apiKey: "test-key", fetch: f })(modelId)
  return createOpenAICompatible({ name: "cliproxy", baseURL: `${BASE}/v1`, apiKey: "test-key", fetch: f }).chatModel(modelId)
}

const WIRE_PHRASES: { text: string; reply: unknown }[] = [
  { text: "ingetin bayar listrik besok jam 9", reply: { text: "bayar listrik", localDateTime: "2026-11-11T09:00", rrule: null } },
  { text: "tiap senin jam 8 kirim laporan", reply: { text: "kirim laporan", localDateTime: "2026-11-16T08:00", rrule: "BYDAY=MO;FREQ=WEEKLY" } },
  { text: "remind me every weekday at 7pm to stretch", reply: { text: "stretch", localDateTime: "2026-11-11T19:00", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" } },
]
const WIRE_MODELS: { provider: Provider; model: string; mode: "text" | "tool" }[] = [
  { provider: "anthropic", model: "claude-haiku-4-5-20251001", mode: "text" },
  { provider: "anthropic", model: "claude-3-5-haiku-20241022", mode: "tool" },
  { provider: "openai", model: "gpt-x", mode: "text" },
]

async function wireCases() {
  const out: unknown[] = []
  for (const m of WIRE_MODELS) {
    for (const p of WIRE_PHRASES) {
      const sink: Rec[] = []
      const result = await parseReminder(p.text, WITA, NOW, model(m.provider, m.model, m.mode, JSON.stringify(p.reply), sink))
      out.push({ ...m, phrase: p.text, reply: p.reply, requests: sink, result })
    }
  }
  return out
}

// --- finishParse -----------------------------------------------------------------------

type ModelFields = { text: string; localDateTime: string; rrule: string | null }
const FINISH: { m: ModelFields; fallback: string; tz: string; now: number }[] = [
  { m: { text: "cek email", localDateTime: "2026-11-10T09:00", rrule: null }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2026-11-09T09:00", rrule: null }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "les", localDateTime: "2026-11-12T18:00", rrule: "FREQ=WEEKLY;BYDAY=TU,TH" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "standup", localDateTime: "2026-11-16T08:00", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2026-11-16T19:00", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "olahraga", localDateTime: "2026-11-11T07:00", rrule: "FREQ=DAILY" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "minum", localDateTime: "2026-11-10T13:00", rrule: "FREQ=HOURLY;INTERVAL=3" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2031-01-01T09:00", rrule: null }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2028-11-01T09:00", rrule: null }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2026-11-13T18:00", rrule: "FREQ=WEEKLY" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "", localDateTime: "2026-11-11T09:00:00+08:00", rrule: "" }, fallback: "the input", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2026-11-11T09:00", rrule: "FREQ=SOMETIMES" }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "2026-11-11T09:00Z", rrule: null }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "x", localDateTime: "nonsense", rrule: null }, fallback: "", tz: WITA, now: NOW },
  { m: { text: "  spaced  ", localDateTime: "2026-11-01T01:30", rrule: null }, fallback: "", tz: NY, now: Date.parse("2026-11-01T04:00:00Z") },
  { m: { text: "gap", localDateTime: "2026-03-08T02:30", rrule: "FREQ=DAILY" }, fallback: "", tz: NY, now: Date.parse("2026-03-07T12:00:00Z") },
  { m: { text: "x", localDateTime: "2026-11-11T09:00", rrule: null }, fallback: "", tz: "Bad/Zone", now: NOW },
  ...PHRASES.map((p) => ({ m: { text: p.text, localDateTime: p.localDateTime, rrule: p.rrule }, fallback: p.text, tz: EVAL_TZ, now: localToUtc(EVAL_NOW_LOCAL, EVAL_TZ) })),
]
const finish = FINISH.map((c) => ({ ...c, result: finishParse(c.m, c.fallback, c.tz, c.now) }))

// --- parse errors ----------------------------------------------------------------------

async function parseErrors() {
  const out: unknown[] = []
  const cases: { name: string; text: string; tz: string; reply: () => Response }[] = [
    { name: "not json", text: "besok", tz: WITA, reply: () => json(anthropicReply([{ type: "text", text: "not json at all" }])) },
    { name: "wrong shape", text: "besok", tz: WITA, reply: () => json(anthropicReply([{ type: "text", text: '{"text":"x"}' }])) },
    { name: "empty reply", text: "besok", tz: WITA, reply: () => json(anthropicReply([], "max_tokens")) },
    { name: "http 400", text: "besok jam 9", tz: WITA, reply: () => json({ type: "error", error: { type: "invalid_request_error", message: "prompt is too long" } }, 400) },
    { name: "empty", text: "   ", tz: WITA, reply: () => json({}) },
    { name: "bad zone", text: "besok", tz: "Bad/Zone", reply: () => json({}) },
  ]
  for (const c of cases) {
    const sink: Rec[] = []
    const m = createAnthropic({ baseURL: `${BASE}/v1`, apiKey: "test-key", fetch: recorder(sink, c.reply) })("claude-haiku-4-5-20251001")
    const result = await parseReminder(c.text, c.tz, NOW, m, { maxRetries: 0 })
    out.push({ name: c.name, text: c.text, tz: c.tz, calls: sink.length, result })
  }
  return out
}

// --- echoes ----------------------------------------------------------------------------

function echoes() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const cases: { localStart: string; tz: string; rrule?: string }[] = [
    { localStart: "2026-11-11T09:00", tz: WITA },
    { localStart: "2026-11-10T10:30", tz: WITA },
    { localStart: "2026-11-10T08:00", tz: WITA, rrule: "BYDAY=MO;FREQ=WEEKLY" },
    { localStart: "2026-11-10T07:00", tz: WITA, rrule: "FREQ=DAILY" },
    { localStart: "2026-11-10T13:00", tz: WITA, rrule: "FREQ=HOURLY;INTERVAL=3" },
    { localStart: "2026-11-27T17:00", tz: WITA, rrule: "FREQ=MONTHLY;BYDAY=-1FR" },
    { localStart: "2026-12-25T19:00", tz: WITA },
    { localStart: "2027-08-17T09:00", tz: WITA, rrule: "FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17" },
    { localStart: "2026-11-10T22:00", tz: NY },
    { localStart: "2026-11-12T09:00", tz: "Asia/Jakarta", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" },
  ]
  const out = cases.map((c) => {
    const r = createReminder(db, { chatId: chat.id, text: "x", localStart: c.localStart, tz: c.tz, rrule: c.rrule ?? null }, NOW)
    return { ...c, fireAt: r.fireAt, localStartStored: r.localStart, rruleStored: r.rrule, echo: reminderEcho(r, NOW), next: reminderNext(r, NOW) }
  })
  const msgs: { r: Parameters<typeof reminderMessage>[0]; now: number }[] = [
    { r: { text: "bayar listrik", fireAt: NOW, tz: WITA, rrule: null, localStart: "2026-11-10T10:00:00", snoozedUntil: null }, now: NOW },
    { r: { text: "bayar listrik", fireAt: NOW - 2 * MIN, tz: WITA, rrule: null, localStart: "", snoozedUntil: null }, now: NOW },
    { r: { text: "bayar listrik", fireAt: NOW - 3 * MIN, tz: WITA, rrule: null, localStart: "", snoozedUntil: null }, now: NOW },
    { r: { text: "x", fireAt: localToUtc("2026-11-08T21:00", WITA), tz: WITA, rrule: null, localStart: "2026-11-08T21:00:00", snoozedUntil: null }, now: NOW },
    { r: { text: "jalan pagi", fireAt: localToUtc("2026-11-07T08:00", WITA), tz: WITA, rrule: "FREQ=DAILY", localStart: "2026-11-07T08:00:00", snoozedUntil: null }, now: NOW },
    { r: { text: "snoozed", fireAt: localToUtc("2026-11-09T08:10", WITA), tz: WITA, rrule: "FREQ=DAILY", localStart: "2026-11-07T08:00:00", snoozedUntil: localToUtc("2026-11-09T08:10", WITA) }, now: NOW },
    { r: { text: "broken rule", fireAt: NOW - HOUR, tz: WITA, rrule: "FREQ=NOPE", localStart: "2026-11-07T08:00:00", snoozedUntil: null }, now: NOW },
    { r: { text: "ny", fireAt: Date.parse("2026-11-01T05:30:00Z"), tz: NY, rrule: null, localStart: "2026-11-01T01:30:00", snoozedUntil: null }, now: Date.parse("2026-11-01T07:00:00Z") },
  ]
  return { reminders: out, messages: msgs.map((m) => ({ ...m, text: reminderMessage(m.r, m.now) })), buttons: reminderButtons("rem_x") }
}

// --- actions ---------------------------------------------------------------------------

async function actions() {
  const texts = [
    "done", "Done!", "selesai", "udah", "udh", "udah ya", "Sudah dong", "beres!", "sip", "OK", "oke done", "✅", "👍", "kelar", "dah kok", "udah bayar listrik kemarin sih",
    "tunda", "tunda 10m", "tunda 10 menit", "tunda 1j", "tunda 1 jam", "tunda 1 jam lagi", "snooze 1h", "Snooze 10 min", "snooze tomorrow", "tunda sampai besok", "nanti besok jam 9",
    "ingetin lagi 1 jam", "remind me again in 1 hour", "later tmrw", "tunda 3 hari", "tunda besok 09.00", "tunda sejam", "later an hour", "", "   ", "makasih, nanti aku bayar",
  ]
  const parsed = texts.map((t) => ({ text: t, action: parseActionText(t) }))
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const other = ensureChat(db, { channel: "whatsapp", externalId: "628123@s.whatsapp.net" }, NOW)
  const a = createReminder(db, { chatId: chat.id, text: "a", localStart: "2026-11-10T10:00", tz: WITA }, NOW)
  const s = createReminder(db, { chatId: chat.id, text: "s", localStart: "2026-11-16T08:00", tz: WITA, rrule: "FREQ=WEEKLY;BYDAY=MO" }, NOW)
  const ny = createReminder(db, { chatId: chat.id, text: "ny", localStart: "2026-11-10T22:00", tz: NY }, NOW)
  const sched = createScheduler({ db, log: silentLogger, now: () => NOW, timers: fakeClock().timers })
  await sched.sweep()
  db.run(sql.raw(`UPDATE outbox SET status = 'sent', channel_msg_id = 'M1' WHERE source = 'reminder:${a.id}'`))
  const steps: [string, () => Promise<string | null>][] = [
    ["cb snooze 10m", () => reminderActions.fromCallback(db, `rem:snooze:${a.id}:10m`, chat.id, NOW)],
    ["cb snooze 1h", () => reminderActions.fromCallback(db, `rem:snooze:${a.id}:1h`, chat.id, NOW)],
    ["cb snooze tomorrow", () => reminderActions.fromCallback(db, `rem:snooze:${a.id}:tomorrow`, chat.id, NOW)],
    ["cb done", () => reminderActions.fromCallback(db, `rem:done:${a.id}`, chat.id, NOW)],
    ["cb done series", () => reminderActions.fromCallback(db, `rem:done:${s.id}`, chat.id, NOW)],
    ["cb snooze series 1h", () => reminderActions.fromCallback(db, `rem:snooze:${s.id}:1h`, chat.id, NOW)],
    ["cb done snoozed series", () => reminderActions.fromCallback(db, `rem:done:${s.id}`, chat.id, NOW + MIN)],
    ["cb snooze ny tomorrow", () => reminderActions.fromCallback(db, `rem:snooze:${ny.id}:tomorrow`, chat.id, NOW)],
    ["cb other chat", () => reminderActions.fromCallback(db, `rem:done:${s.id}`, other.id, NOW)],
    ["cb bad preset", () => reminderActions.fromCallback(db, `rem:snooze:${s.id}:2d`, chat.id, NOW)],
    ["cb snooze without preset", () => reminderActions.fromCallback(db, `rem:snooze:${s.id}`, chat.id, NOW)],
    ["cb missing", () => reminderActions.fromCallback(db, "rem:done:rem_missing", chat.id, NOW)],
    ["cb other prefix", () => reminderActions.fromCallback(db, "approve:x", chat.id, NOW)],
    ["text tunda besok", () => reminderActions.fromText(db, "tunda besok", "M1", chat.id, NOW)],
    ["text done", () => reminderActions.fromText(db, "udah ya", "M1", chat.id, NOW)],
    ["text no quote", () => reminderActions.fromText(db, "done", undefined, chat.id, NOW)],
    ["text unknown quote", () => reminderActions.fromText(db, "done", "M404", chat.id, NOW)],
    ["text other", () => reminderActions.fromText(db, "makasih", "M1", chat.id, NOW)],
  ]
  const replies: unknown[] = []
  for (const [name, fn] of steps) replies.push({ name, reply: await fn() })
  markDone(db, ny.id, NOW)
  snoozeReminder(db, ny.id, { until: NOW + 5 * MIN }, NOW)
  return { parsed, replies: normalize(replies), rows: normalize(rows(db, "reminders")) }
}

// --- presets ---------------------------------------------------------------------------

function presets() {
  const db = memoryDb()
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const out: unknown[] = []
  for (const tz of [WITA, NY, "Asia/Tokyo"]) {
    for (const make of [dailyBriefing, heartbeat]) {
      const input = make(chat.id, tz, NOW)
      const job = createJob(db, input, NOW)
      out.push({ tz, input, nextRunAt: job.nextRunAt, schedule: describeJobSchedule(job, NOW), next: describeNextRun(job, NOW) })
    }
  }
  return { heartbeatRrule: HEARTBEAT_RRULE, jobs: normalize(out) }
}

// --- outbound --------------------------------------------------------------------------

async function outbound() {
  const db = memoryDb()
  const owner = ensureChat(db, { channel: "telegram", externalId: "42" }, NOW)
  const meta = { origin: "chat" as const, approvedBy: "owner" as const, ownerChatId: owner.id }
  const zaki = addContact(db, { agentId: "agt_test", name: "Zaki", phone: "082200001001" }, NOW)
  const tgDm = ensureChat(db, { channel: "telegram", externalId: "777", title: "Budi" }, NOW)
  const budi = addContact(db, { agentId: "agt_test", name: "Budi", chatId: tgDm.id }, NOW)
  const labels = [contactLabel(zaki), contactLabel(budi), contactLabel({ name: "X", channel: "whatsapp", address: "14155550100" })]
  const phones = ["082200001001", "0822 0000-1001", "+62 822 0000 1001", "+62 0822 0000 1001", "82200001001", "+1 415 555 0100", "0065 9123 4567", "Zaki", "0812", "14155550100", "62 812"].map((p) => ({
    raw: p,
    normalized: normalizePhone(p),
  }))
  const formatted = ["6282200001001", "14155550100", "621234567"].map((d) => ({ digits: d, formatted: formatPhone(d) }))
  const found = phonesIn("chat Zaki (082200001001) atau +62 811-1111-1111, bukan 12345 ya")
  const held = sendNow(db, zaki, "Halo, ini asisten Zaki. Dia lagi otw.", { ...meta, holdMs: 15_000 }, NOW)
  holdNotice(db, held.row, zaki, held.sendAt, NOW)
  const instant = sendNow(db, budi, "otw", { ...meta, holdMs: 0 }, NOW)
  holdNotice(db, instant.row, budi, instant.sendAt, NOW)
  const cancelled = cancelOutbound(db, held.row.id, NOW + 1000).message
  const again = cancelOutbound(db, held.row.id, NOW + 2000).message
  db.run(sql.raw(`UPDATE outbox SET status = 'sent' WHERE id = '${instant.outboxId}'`))
  const late = cancelOutbound(db, instant.row.id, NOW + 2000).message
  const missing = cancelOutbound(db, "obm_missing", NOW).message
  const failedBox = enqueueOutbox(db, { chatId: zaki.chatId, channel: "whatsapp", payload: { text: "x" }, source: `outbound:${held.row.id}` }, NOW)
  outboundFailed(db, { source: failedBox.source, error: "x".repeat(200) }, NOW)
  outboundFailed(db, { source: failedBox.source, error: null }, NOW)
  setSetting(db, "outbound", { ...DEFAULT_SETTINGS.outbound, perContactPerDay: 2 }, NOW)
  const ext = createOutboundScheduler({ db, log: silentLogger })
  const over = scheduleSend(db, zaki, "malam", { localStart: "2026-11-10T20:00", tz: WITA }, meta, NOW)
  ext.sweep(over.fireAt)
  setSetting(db, "outbound", DEFAULT_SETTINGS.outbound, NOW)
  const lateSend = scheduleSend(db, budi, "late", { localStart: "2026-11-11T20:00", tz: WITA }, meta, NOW)
  ext.sweep(lateSend.fireAt + 31 * MIN)
  const gone = scheduleSend(db, budi, "gone", { localStart: "2026-11-12T20:00", tz: WITA, rrule: "FREQ=DAILY" }, meta, NOW)
  db.run(sql.raw(`DELETE FROM chats WHERE id = '${tgDm.id}'`))
  ext.sweep(gone.fireAt)
  const texts = [
    "otw",
    `otw\n\n${DEFAULT_SETTINGS.outbound.signature}`,
    "otw\n_dikirim otomatis oleh asisten ai zaki. balasan ke nomor ini tidak dibaca, silakan hubungi zaki langsung_ ",
    "Balasan ke nomor ini tidak dibaca?",
    "  spaced  \n\n",
  ].map((t) => ({ text: t, final: finalText(db, t), unsigned: finalText(db, t, { signature: false }) }))
  const errs: string[] = []
  for (const t of ["", "a".repeat(1990)]) {
    try {
      finalText(db, t)
    } catch (e) {
      errs.push((e as Error).message)
    }
  }
  saveOutboundSettings(db, { ...DEFAULT_SETTINGS.outbound, signature: "\u2014\n_Asisten Zaki_" }, NOW)
  saveOutboundSettings(db, { ...DEFAULT_SETTINGS.outbound, signature: "(dikirim asisten Zaki)\r\n" }, NOW + 1)
  const settingsRow = rows(db, "settings", "WHERE key = 'outbound'")
  return normalize({
    labels,
    phones,
    formatted,
    found,
    texts,
    errors: errs,
    cancel: { cancelled, again, late, missing },
    outbox: rows(db, "outbox"),
    outboundMessages: rows(db, "outbound_messages"),
    events: rows(db, "events").map(({ id: _id, ...e }) => e),
    settings: settingsRow,
  })
}

// --- trace -----------------------------------------------------------------------------

async function trace() {
  const db = memoryDb()
  // Saturday 2026-10-31 12:00 in New York (EDT, UTC-4). DST ends Sunday 02:00.
  const start = Date.parse("2026-10-31T16:00:00Z")
  const chat = ensureChat(db, { channel: "telegram", externalId: "42" }, start)
  const owner = chat
  setSetting(db, "timezone", NY, start)
  createReminder(db, { chatId: chat.id, text: "minum obat", localStart: "2026-10-31T01:30", tz: NY, rrule: "FREQ=DAILY" }, start)
  createReminder(db, { chatId: chat.id, text: "sekali", localStart: "2026-11-01T01:30", tz: NY }, start)
  createReminder(db, { chatId: chat.id, text: "jalan pagi", localStart: "2026-11-01T08:00", tz: NY, rrule: "FREQ=DAILY" }, start)
  createReminder(db, { chatId: chat.id, text: "cek cuaca", localStart: "2026-11-01T07:00", tz: NY, smartPrompt: "Cek cuaca New York" }, start)
  createReminder(db, { chatId: chat.id, text: "gagal pintar", localStart: "2026-11-01T09:15", tz: NY, smartPrompt: "fail" }, start)
  createReminder(db, { chatId: chat.id, text: "tiap 6 jam", localStart: "2026-10-31T18:00", tz: NY, rrule: "FREQ=HOURLY;INTERVAL=6" }, start)
  createJob(db, { name: "Pagi", rrule: "FREQ=DAILY", tz: NY, localStart: "2026-10-31T06:00", prompt: "briefing", chatId: chat.id, dailyTokenCap: 150 }, start)
  createJob(db, { name: "Malam", rrule: "FREQ=DAILY;BYHOUR=1,23;BYMINUTE=30", tz: NY, localStart: "2026-10-31T01:30", prompt: "cek", chatId: chat.id, modelRole: "fast" }, start)
  const zaki = addContact(db, { agentId: "agt_test", name: "Zaki", phone: "082200001001" }, start)
  scheduleSend(db, zaki, "kabar pagi", { localStart: "2026-11-01T09:00", tz: NY, rrule: "FREQ=DAILY" }, { origin: "chat", approvedBy: "owner", ownerChatId: owner.id }, start)

  const clock = fakeClock(start)
  const smartCalls: unknown[] = []
  const turns: unknown[] = []
  const runner = createJobRunner({
    db,
    log: silentLogger,
    now: clock.now,
    runTurn: async (o) => {
      turns.push({ ...o, at: clock.now() })
      const id = newId("run")
      db.insert(runs).values({ id, chatId: o.chatId, jobId: o.jobId, trigger: "schedule", tokensIn: 100, tokensOut: 20, tokensCached: 0, status: "ok", startedAt: clock.now() }).run()
      return { runId: id, text: o.prompt === "cek" ? "NOTHING" : `ok ${o.prompt}`, status: "ok" }
    },
  })
  const s = createScheduler({
    db,
    log: silentLogger,
    now: clock.now,
    timers: clock.timers,
    onSmartReminder: async (r) => {
      smartCalls.push({ id: r.id, at: clock.now(), prompt: r.smartPrompt })
      if (r.smartPrompt === "fail") throw new Error("model off")
    },
    onJobDue: async (job) => {
      await runner.run(job)
    },
    extensions: [createOutboundScheduler({ db, log: silentLogger })],
  })
  const steps: unknown[] = []
  const snap = (label: string, ran: number) =>
    steps.push({
      label,
      now: clock.now(),
      ran,
      armed: clock.armed.at(-1) ?? null,
      reminders: rows(db, "reminders"),
      jobs: rows(db, "jobs"),
      outbound: rows(db, "outbound_messages"),
      outbox: rows(db, "outbox"),
      usage: rows(db, "usage"),
      events: rows(db, "events").map(({ id: _id, ...e }) => e),
    })
  await s.start()
  await s.idle()
  snap("start", 0)
  const plan: [string, number][] = [
    ["+6h", 6 * HOUR],
    ["+4h", 4 * HOUR],
    ["+3h", 3 * HOUR],
    ["+50m", 50 * MIN],
    ["+1h", HOUR],
    ["+2h", 2 * HOUR],
    ["+6h", 6 * HOUR],
    ["+12h", 12 * HOUR],
    ["+24h", 24 * HOUR],
  ]
  for (const [label, ms] of plan) {
    const ran = await clock.advance(ms, () => s.idle())
    snap(label, ran)
  }
  s.stop()
  return normalize({ start, steps, smartCalls, turns, pending: clock.pending.size })
}

// --- main ------------------------------------------------------------------------------

const out = {
  now: NOW,
  prompts,
  wire: await wireCases(),
  finish,
  parseErrors: await parseErrors(),
  echoes: echoes(),
  actions: await actions(),
  presets: presets(),
  outbound: await outbound(),
  trace: await trace(),
}
const file = path.resolve(import.meta.dirname, "../schedulers.json")
writeFileSync(file, `${JSON.stringify(out, null, 1)}\n`)
console.log(`wrote ${file}`)
