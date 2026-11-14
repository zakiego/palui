// Golden for the dashboard RPC functions of apps/app/src/lib/fns/outbound.ts,
// jobs.ts, reminders.ts and flows.ts (M8b rpcsched). Run from apps/app:
//
//   pnpm tsx ../server/testdata/gen/rpc-sched.ts
//
// writes testdata/rpc-sched.json. The real handler bodies run: a module resolve
// hook swaps "@tanstack/react-start" and "@/server/session" for
// rpc-sched-stubs.ts (createServerFn keeps TanStack's validator step, the
// session is the owner), and the runtime client is replaced with
// setRuntime(): "down" rejects every call the way an unreachable runtime does,
// "fake" runs the real jobs runner with a scripted runTurn and answers
// parseReminder from a fixed table, "nomodel" refuses the parse with the
// runtime's 503. Each sequence runs on its own copy of testdata/seeded.db with
// a fixed clock (BASE + 1 s per call). For each call the golden keeps the JSON
// text of the result (or the thrown message, and whether the validator threw
// it) and, after a write, the rows the given SELECTs return.
process.env.TZ = "UTC"

import { copyFileSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { register } from "node:module"
import { tmpdir } from "node:os"
import path from "node:path"
import { pathToFileURL } from "node:url"

const testdata = path.resolve(import.meta.dirname, "..")
const appSrc = path.resolve(import.meta.dirname, "../../../app/src")
const seeded = JSON.parse(readFileSync(path.join(testdata, "seeded.json"), "utf8")) as { now: number }
const BASE = seeded.now + 3_600_000

const stubUrl = pathToFileURL(path.join(import.meta.dirname, "rpc-sched-stubs.ts")).href
// tsx rewrites the "@/" alias to a file URL before resolving, so the session
// module is matched by its URL.
const sessionUrl = pathToFileURL(path.join(appSrc, "server/session.ts")).href
const hook = `
export async function resolve(spec, ctx, next) {
  if (spec === "@tanstack/react-start" || spec === "@/server/session" || spec === ${JSON.stringify(sessionUrl)}) return { url: ${JSON.stringify(stubUrl)}, shortCircuit: true }
  return next(spec, ctx)
}`
register(`data:text/javascript,${encodeURIComponent(hook)}`)

let clock = BASE
Date.now = () => clock

const { openDb } = await import("../../../../packages/db/src/index.ts")
const { ControlError } = await import("../../../../packages/core/src/control.ts")
const { MODEL_OFF_TEXT } = await import("../../../../packages/core/src/chat/turn.ts")
const { silentLogger } = await import("../../../../packages/core/src/log.ts")
const { getSetting } = await import("../../../../packages/core/src/settings.ts")
const { createJobRunner } = await import("../../../../packages/core/src/jobs/runner.ts")
const runtimeMod = await import(pathToFileURL(path.join(appSrc, "server/runtime.ts")).href)
const fns = {
  ...(await import(pathToFileURL(path.join(appSrc, "lib/fns/outbound.ts")).href)),
  ...(await import(pathToFileURL(path.join(appSrc, "lib/fns/jobs.ts")).href)),
  ...(await import(pathToFileURL(path.join(appSrc, "lib/fns/reminders.ts")).href)),
  ...(await import(pathToFileURL(path.join(appSrc, "lib/fns/flows.ts")).href)),
} as Record<string, ((o?: { data?: unknown }) => Promise<unknown>) & { method: string }>

type DB = ReturnType<typeof openDb>

function scratch(): DB {
  const dir = mkdtempSync(path.join(tmpdir(), "palui-rpc-sched-"))
  const file = path.join(dir, "palui.db")
  copyFileSync(path.join(testdata, "seeded.db"), file)
  return openDb(file)
}

function client(db: DB) {
  return (db as unknown as { $client: { prepare: (sql: string) => { all: () => Record<string, unknown>[]; get: () => Record<string, unknown> } } }).$client
}

// Stable rows of seeded.db.
const S = {
  web: "chat_0mwfavogqfl8g9qpmbj",
  tgDm: "chat_0mwfavogqfl8g9qpmbo",
  tgOther: "chat_0mwfavoh8q5xv0b0os1",
  palui: "agt_0mwfavohdfkyeb1kl5c",
  contactAllow: "con_0mwfavoip2f7n0yy60c",
  contactAsk: "con_0mwfavoiq929bs5ebkg",
  contactRemoved: "con_0mwfavoirnribr9ytf1",
  obmScheduled: "obm_0mwfavoiq929bs5ebkk",
  obmSent: "obm_0mwfavoip2f7n0yy60f",
  jobRunning: "job_0mwfavohcl74i2aofif",
  jobOff: "job_0mwfavohcl74i2aofig",
  jobBriefing: "job_0mwfavohcl74i2aofih",
  jobBudget: "job_0mwfavohcl74i2aofij",
  jobOrphan: "job_0mwfavohcl74i2aofik",
  jobDaily: "job_0mwfavohcl74i2aofio",
  jobDaily2: "job_0mwfavohcl74i2aofip",
  remPending: "rem_0mwfavoiozgooh7fp4p",
  remFiring: "rem_0mwfavoin996hqfnjz7",
  remDone: "rem_0mwfavoiozgooh7fp52",
  remCancelled: "rem_0mwfavoiozgooh7fp53",
  flowOn: "flw_0mwfavoihdq0kcg0btx",
  flowOff: "flw_0mwfavohlx3z4o8bmex",
  flowArabic: "flw_0mwfavoie0m6mjt4ky7",
  tokenRun: "run_0mwfavoileillz5uies",
}

// The fake runtime's fixed answers, shared with the Go test.
const FAKE = {
  turns: {
    [S.jobDaily]: { runId: S.tokenRun, text: "Pagi! Hari ini ada 2 pengingat.", status: "ok" },
    [S.jobDaily2]: { runId: "run_fake_silent", text: "  NOTHING  ", status: "ok" },
    [S.jobBriefing]: { runId: "run_fake_error", text: "", status: "error" },
  } as Record<string, { runId: string; text: string; status: string }>,
  parse: [
    { text: "besok jam 9 bayar listrik", answer: { ok: true, text: "bayar listrik", fireAt: 1794708000000, fireAtIso: "2026-11-14T09:00:00.000+08:00", rrule: null, tz: "", echo: "Besok, Sabtu 14 Nov, 09.00 WITA" } },
    { text: "kapan-kapan", answer: { ok: false, text: "kapan-kapan", tz: "", error: "Waktunya belum jelas. Coba sebut jam atau tanggalnya." } },
  ],
}

const RUNTIME_GONE = "The runtime isn't reachable (fetch failed). Check docker compose logs runtime."

function fakeRuntime(db: DB, mode: "down" | "fake" | "nomodel") {
  const down = () => Promise.reject(new ControlError(RUNTIME_GONE, 503))
  if (mode === "down") return { reconcile: down, runJob: down, parseReminder: down }
  const runner = createJobRunner({
    db,
    log: silentLogger,
    now: () => Date.now(),
    runTurn: async (o) => {
      const t = FAKE.turns[o.jobId]
      if (!t) throw new Error(`no scripted turn for ${o.jobId}`)
      return t
    },
  })
  return {
    reconcile: async () => ({ ok: true as const }),
    runJob: (id: string) => runner.runNow(id),
    parseReminder: async (text: string, tz?: string) => {
      if (mode === "nomodel") throw new ControlError(MODEL_OFF_TEXT, 503)
      const hit = FAKE.parse.find((p) => p.text === text)
      if (!hit) throw new ControlError(`no scripted parse for ${text}`, 500)
      return { ...hit.answer, tz: tz || getSetting(db, "timezone") }
    },
  }
}

type Case = { fn: string; input?: unknown; rows?: string[] }

// Events the calls log: every row past the seed's last one.
const seedEvents = Number(client(scratch()).prepare("SELECT max(id) AS m FROM events").get().m)
const EVENTS = `SELECT * FROM events WHERE id > ${seedEvents} ORDER BY id`
const OUTBOUND_ROWS = [
  "SELECT * FROM settings WHERE key = 'outbound'",
  `SELECT * FROM contacts WHERE updated_at >= ${BASE} ORDER BY rowid`,
  `SELECT * FROM outbound_messages WHERE updated_at >= ${BASE} ORDER BY rowid`,
  "SELECT * FROM outbox WHERE source LIKE 'outbound%' ORDER BY rowid",
  "SELECT count(*) AS n FROM contacts",
]
const JOB_ROWS = [`SELECT * FROM jobs WHERE updated_at >= ${BASE} ORDER BY rowid`, "SELECT count(*) AS n FROM jobs", "SELECT * FROM usage ORDER BY rowid"]
const REMINDER_ROWS = [`SELECT * FROM reminders WHERE updated_at >= ${BASE} ORDER BY rowid`, "SELECT count(*) AS n FROM reminders"]

const job = (over: Record<string, unknown> = {}) => ({
  name: " Rekap mingguan ",
  rrule: "FREQ=WEEKLY;BYDAY=MO;BYHOUR=8;BYMINUTE=0",
  tz: "Asia/Makassar",
  localStart: "2026-11-16T08:00",
  prompt: "  Rekap penjualan minggu lalu, singkat.  ",
  chatId: S.web,
  modelRole: "fast",
  dailyTokenCap: 20000,
  ...over,
})

const limits = (over: Record<string, unknown> = {}) => ({ perContactPerDay: 10, perDay: 100, holdSeconds: 30, signatureOn: true, signature: "  ~ Zaki\r\nlewat asisten AI  ", ...over })

const MAIN: Case[] = [
  // Reads on the untouched copy.
  { fn: "getOutbound" },
  { fn: "getJobs" },
  { fn: "getJobPreset", input: { preset: "dailyBriefing", chatId: S.web } },
  { fn: "getJobPreset", input: { preset: "heartbeat", chatId: "chat_does_not_matter" } },
  { fn: "getJobPreset", input: { preset: "weekly", chatId: "" } },
  { fn: "getJobPreset" },
  { fn: "getReminders" },
  { fn: "getFlows" },
  { fn: "getFlowFn", input: { id: S.flowOn } },
  { fn: "getFlowFn", input: { id: S.flowOff } },
  { fn: "getFlowFn", input: { id: S.flowArabic } },
  { fn: "getFlowFn", input: { id: "flw_missing" } },
  { fn: "getFlowFn", input: { id: "" } },
  { fn: "getFlowFn", input: { id: "f".repeat(65) } },
  { fn: "getFlowFn", input: { id: 42 } },
  // Outbound writes.
  { fn: "setOutboundSwitch", input: { enabled: "yes" } },
  { fn: "setOutboundSwitch", input: {} },
  { fn: "saveOutboundLimits", input: limits(), rows: [...OUTBOUND_ROWS, EVENTS] },
  { fn: "saveOutboundLimits", input: limits({ signature: " \r\n " }), rows: OUTBOUND_ROWS.slice(0, 1) },
  { fn: "saveOutboundLimits", input: limits({ perContactPerDay: 1.5, perDay: 0, holdSeconds: 121, signatureOn: "no", signature: "x".repeat(151) }) },
  { fn: "saveOutboundLimits", input: limits({ signature: `${"y".repeat(150)}  \r\n` }), rows: OUTBOUND_ROWS.slice(0, 1) },
  { fn: "saveOutboundLimits", input: limits({ perContactPerDay: 50, perDay: 200, holdSeconds: 0, signatureOn: false, signature: "", enabled: false, extra: 1 }), rows: [...OUTBOUND_ROWS.slice(0, 1), EVENTS] },
  { fn: "saveOutboundLimits", input: { perContactPerDay: 5 } },
  { fn: "addContactFn", input: { agentId: S.palui, name: "  Budi Santoso  ", phone: " 0812-3456-7890 ", note: "  teman kantor  " }, rows: [...OUTBOUND_ROWS, EVENTS] },
  { fn: "addContactFn", input: { agentId: "agt_missing", name: "X", phone: "08123456789" } },
  { fn: "addContactFn", input: { agentId: S.palui, name: "Dup", phone: "+62 812 0000 0001" } },
  { fn: "addContactFn", input: { agentId: S.palui, name: "Tg", chatId: S.tgOther, policy: "allow", note: "   " }, rows: [...OUTBOUND_ROWS, EVENTS] },
  { fn: "addContactFn", input: { agentId: S.palui, name: "Nobody", phone: "", chatId: "" } },
  { fn: "addContactFn", input: { agentId: S.palui, name: "Bad", phone: "halo" } },
  { fn: "addContactFn", input: { agentId: "", name: "   ", policy: "deny", phone: "1".repeat(31) } },
  { fn: "setContactPolicy", input: { id: S.contactAsk, policy: "allow" }, rows: [...OUTBOUND_ROWS.slice(1, 2), EVENTS] },
  { fn: "setContactPolicy", input: { id: "con_missing", policy: "ask" } },
  { fn: "setContactPolicy", input: { id: S.contactAsk, policy: "never" } },
  { fn: "removeContactFn", input: { id: S.contactAllow }, rows: [...OUTBOUND_ROWS, EVENTS] },
  { fn: "removeContactFn", input: { id: S.contactRemoved } },
  { fn: "removeContactFn", input: { id: "con_missing" } },
  { fn: "cancelOutboundFn", input: { id: S.obmScheduled }, rows: [...OUTBOUND_ROWS, EVENTS] },
  { fn: "cancelOutboundFn", input: { id: S.obmScheduled } },
  { fn: "cancelOutboundFn", input: { id: S.obmSent } },
  { fn: "cancelOutboundFn", input: { id: "obm_missing" } },
  { fn: "cancelOutboundFn", input: { id: "" } },
  { fn: "setOutboundSwitch", input: { enabled: false }, rows: [...OUTBOUND_ROWS, EVENTS] },
  { fn: "setOutboundSwitch", input: { enabled: true }, rows: [...OUTBOUND_ROWS.slice(0, 1), EVENTS] },
  // Jobs.
  { fn: "setJobEnabledFn", input: { id: S.jobOff, enabled: true }, rows: [...JOB_ROWS, EVENTS] },
  { fn: "setJobEnabledFn", input: { id: S.jobBudget, enabled: false }, rows: JOB_ROWS },
  { fn: "setJobEnabledFn", input: { id: "job_missing", enabled: false } },
  { fn: "setJobEnabledFn", input: { id: S.jobOff, enabled: 1 } },
  { fn: "saveJob", input: job(), rows: [...JOB_ROWS, EVENTS] },
  { fn: "saveJob", input: job({ id: S.jobBudget, name: "Budget watcher v2", kind: "heartbeat", tz: "UTC", localStart: "2026-11-13T07:30:00", modelRole: "default", dailyTokenCap: 5_000_000 }), rows: JOB_ROWS },
  { fn: "saveJob", input: job({ id: "", name: "Empty id makes a new job", kind: "briefing", rrule: " FREQ=DAILY;BYHOUR=7;BYMINUTE=0 ", tz: "asia/makassar" }), rows: JOB_ROWS },
  { fn: "saveJob", input: job({ id: "job_missing" }) },
  { fn: "saveJob", input: job({ dailyTokenCap: 1500.5 }) },
  { fn: "saveJob", input: job({ dailyTokenCap: 999 }) },
  { fn: "saveJob", input: job({ dailyTokenCap: "20000" }) },
  { fn: "saveJob", input: { name: "", kind: "cron", rrule: "DAILY", tz: "Mars/Olympus", localStart: "2026-11-16 08:00", prompt: " ", chatId: "", modelRole: "slow", dailyTokenCap: 5_000_001 } },
  { fn: "saveJob", input: job({ chatId: "chat_missing" }) },
  { fn: "saveJob", input: job({ rrule: "FREQ=FOO;BYHOUR=8" }) },
  { fn: "saveJob", input: job({ localStart: "2026-02-30T08:00" }) },
  { fn: "saveJob", input: job({ tz: "local" }) },
  { fn: "deleteJobFn", input: { id: S.jobOrphan }, rows: JOB_ROWS },
  { fn: "deleteJobFn", input: { id: S.jobOrphan } },
  { fn: "deleteJobFn", input: {} },
  // Reminders.
  { fn: "createReminderFn", input: { chatId: S.web, text: " Bayar listrik ", fireAtIso: "2026-11-14T09:00:00+08:00", tz: "Asia/Makassar" }, rows: REMINDER_ROWS },
  { fn: "createReminderFn", input: { chatId: S.tgDm, text: "Standup", fireAtIso: "2026-11-16T01:00:00.000Z", tz: "Asia/Jakarta", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" }, rows: REMINDER_ROWS },
  { fn: "createReminderFn", input: { chatId: S.web, text: "Telepon Ibu", fireAtIso: "2026-11-14T10:30", tz: "America/New_York", rrule: null }, rows: REMINDER_ROWS },
  { fn: "createReminderFn", input: { chatId: S.web, text: "Earlier today", fireAtIso: "2026-11-13T01:00:00Z", tz: "UTC", rrule: "" }, rows: REMINDER_ROWS },
  { fn: "createReminderFn", input: { chatId: S.web, text: "Basic form", fireAtIso: "2026-11-14", tz: "Asia/Makassar" }, rows: REMINDER_ROWS },
  // luxon's other ISO forms and zone names.
  ...["20261114T0900Z", "2026-11-14T24:00:00Z", "2026-11-14T09:00:00,5+0800", "+002026-11-14T09:00Z", "2026-11-14T09+08", "2026-11-14T09:00:00.000+08:00[Asia/Makassar]", "2026-11-14T09:00:00 +08:00", "2026-11-14t09:00:00z"].map(
    (fireAtIso): Case => ({ fn: "createReminderFn", input: { chatId: S.web, text: `ISO ${fireAtIso}`, fireAtIso, tz: "Asia/Makassar" }, rows: REMINDER_ROWS.slice(1) }),
  ),
  ...["utc", "UTC+3", "utc-03:30", "local", "+08:00", "asia/jakarta"].map(
    (tz): Case => ({ fn: "createReminderFn", input: { chatId: S.web, text: `Zone ${tz}`, fireAtIso: "2026-11-14T09:00:00Z", tz }, rows: [`SELECT tz, local_start, fire_at FROM reminders WHERE updated_at >= ${BASE} ORDER BY rowid DESC LIMIT 1`] }),
  ),
  { fn: "createReminderFn", input: { chatId: S.web, text: "Bogus", fireAtIso: "not a date at all", tz: "Asia/Makassar" } },
  { fn: "createReminderFn", input: { chatId: S.web, text: "Bad day", fireAtIso: "2026-02-30T09:00:00Z", tz: "Asia/Makassar" } },
  { fn: "createReminderFn", input: { chatId: "chat_missing", text: "Lost", fireAtIso: "2026-11-14T09:00:00Z", tz: "UTC" } },
  { fn: "createReminderFn", input: { chatId: S.web, text: "Far off", fireAtIso: "2031-01-01T09:00:00Z", tz: "UTC" } },
  { fn: "createReminderFn", input: { chatId: S.web, text: "Bad rule", fireAtIso: "2026-11-14T09:00:00Z", tz: "UTC", rrule: "FREQ=SOMETIMES" } },
  { fn: "createReminderFn", input: { chatId: S.web, text: "", fireAtIso: "2026", tz: "Not/AZone", rrule: 5 } },
  { fn: "cancelReminderFn", input: { id: S.remPending }, rows: REMINDER_ROWS },
  { fn: "cancelReminderFn", input: { id: "rem_missing" } },
  { fn: "snoozeReminderFn", input: { id: S.remFiring, preset: "10m" }, rows: REMINDER_ROWS },
  { fn: "snoozeReminderFn", input: { id: S.remDone, preset: "tomorrow" }, rows: REMINDER_ROWS },
  { fn: "snoozeReminderFn", input: { id: S.remPending, preset: "1h" } },
  { fn: "snoozeReminderFn", input: { id: S.remCancelled, preset: "1h" } },
  { fn: "snoozeReminderFn", input: { id: "rem_missing", preset: "1h" } },
  { fn: "snoozeReminderFn", input: { id: S.remDone, preset: "2h" } },
  // The runtime is down: the calls that need it say so.
  { fn: "runJobNow", input: { id: S.jobDaily } },
  { fn: "parseReminder", input: { text: "besok jam 9 bayar listrik" } },
  { fn: "parseReminder", input: { text: "   " } },
  // Reads after the writes.
  { fn: "getOutbound" },
  { fn: "getJobs" },
  { fn: "getReminders" },
]

const RUNTIME: Case[] = [
  // No events dump here: the Go test's supervisor logs its own start-up rows.
  { fn: "runJobNow", input: { id: S.jobDaily }, rows: JOB_ROWS },
  { fn: "runJobNow", input: { id: S.jobDaily2 }, rows: JOB_ROWS },
  { fn: "runJobNow", input: { id: S.jobBriefing }, rows: JOB_ROWS },
  { fn: "runJobNow", input: { id: S.jobRunning } },
  { fn: "runJobNow", input: { id: "job_missing" } },
  { fn: "runJobNow", input: { id: "x".repeat(101) } },
  { fn: "parseReminder", input: { text: "  besok jam 9 bayar listrik ", tz: "Asia/Makassar" } },
  { fn: "parseReminder", input: { text: "besok jam 9 bayar listrik" } },
  { fn: "parseReminder", input: { text: "kapan-kapan", tz: "" } },
  { fn: "parseReminder", input: { text: "x".repeat(501), tz: "y".repeat(65) } },
  { fn: "getJobs" },
]

// Flows the seed doesn't have, made through the flows service on the
// sequence's copy; their rows go into the golden as setup for the Go test.
const KITCHEN = {
  schema: 1,
  name: "Kitchen sink",
  description: "Semua jenis langkah",
  tz: "Asia/Makassar",
  triggers: [
    { id: "manual_1", kind: "manual", start: "set_run" },
    { id: "hook_1", kind: "webhook", hookId: "hook_0mwfavoirnribr9ytf4", start: "set_flow" },
    { id: "event_1", kind: "event", event: "checkin.answered", start: "notify" },
    { id: "poll_1", kind: "poll", tool: "list_reminders", connectorId: "reminders", everyMin: 30, start: "wait_min" },
    { id: "sched_1", kind: "schedule", source: { kind: "times", times: [{ time: "07:00", label: "Pagi" }] }, start: "set_run" },
  ],
  steps: [
    { id: "set_run", kind: "set", values: { a: "1", b_2: "vars.a + 1" } },
    { id: "set_flow", kind: "set", scope: "flow", name: "  Hitung  ", values: { count: "flow.count + 1" } },
    { id: "wait_min", kind: "wait", minutes: 15 },
    { id: "wait_until", kind: "wait", until: "  2026-11-20T09:00  ", plusMin: -10 },
    { id: "wait_zero", kind: "wait", until: "{{vars.when}}", plusMin: 0 },
    { id: "gate", kind: "if", when: "vars.a > 0", then: "ask", else: "gate2" },
    { id: "gate2", kind: "if", when: "false", then: null },
    { id: "ask", kind: "checkin", text: "Sudah bayar?", deadline: { kind: "after", minutes: 60 }, on: { done: "summarize" }, onMissed: "lookup", onError: "notify" },
    { id: "summarize", kind: "prompt", prompt: "Ringkas  hasilnya", next: "lookup" },
    { id: "lookup", kind: "tool", tool: "list_reminders", connectorId: "reminders" },
    { id: "bare_tool", kind: "tool", tool: "reminders__list_reminders", onError: "continue" },
    { id: "loop", kind: "each", items: "vars.items", where: "item.ok", body: "notify" },
    { id: "notify", kind: "message", text: "Selesai, {{vars.a}}" },
  ],
}

async function flowSetup(db: DB): Promise<{ ids: Record<string, string> }> {
  const svc = await import("../../../../packages/core/src/flows/service.ts")
  const created = svc.createFlow(db, { chatId: S.web, definition: KITCHEN, createdBy: "chat", note: "pertama" })
  clock += 1000
  svc.updateFlow(db, created.flow.id, { definition: { ...KITCHEN, description: "Versi dua", steps: [...KITCHEN.steps.slice(0, -1), { id: "notify", kind: "message", text: "Beres" }] }, createdBy: "dashboard", note: "ubah pesan" })
  const c = client(db)
  c.prepare("UPDATE flows SET state = ?, enabled = 1, paused_until = NULL WHERE id = ?").run(JSON.stringify({ values: { count: 3, last: "Senin", nested: { a: [1, "x", null] } }, tainted: [] }), created.flow.id)
  const flow = svc.getFlow(db, created.flow.id)!
  const run = (id: string, over: Record<string, unknown>) =>
    c
      .prepare("INSERT INTO flow_runs (id, flow_id, version_id, chat_id, trigger_id, trigger_kind, run_key, parent_run_id, status, trigger, vars, wait, started_at, scheduled_at, error, dry_run, updated_at) VALUES (@id, @flow, @version, @chat, @triggerId, @kind, @key, @parent, @status, @trigger, @vars, @wait, @started, @scheduled, @error, @dry, @started)")
      .run({ id, flow: flow.id, version: flow.currentVersionId, chat: S.web, triggerId: null, kind: "manual", key: id, parent: null, status: "done", trigger: "{}", vars: "{}", wait: null, started: BASE - 3_600_000, scheduled: null, error: null, dry: 0, ...over })
  run("flr_000000000kitchen0001", { triggerId: "sched_1", kind: "schedule", trigger: JSON.stringify({ label: "Senin" }), scheduled: BASE - 3_600_000 })
  run("flr_000000000kitchen0002", { trigger: JSON.stringify({ label: 42 }), status: "failed", error: "boom", started: BASE - 7_200_000 })
  run("flr_000000000kitchen0003", { parent: "flr_000000000kitchen0001", trigger: JSON.stringify({ label: "" }), started: BASE - 1_800_000 })
  run("flr_000000000kitchen0004", { status: "waiting", wait: JSON.stringify({ kind: "checkin", stepId: "ask", answers: [{ answer: "done" }] }), started: BASE - 600_000 })
  run("flr_000000000kitchen0005", { dry: 1, started: BASE - 60_000 })
  run("flr_000000000kitchen0006", { trigger: JSON.stringify({ label: null }), started: BASE - 30 * 86_400_000 })
  // A flow whose version can't be read, and one with no versions at all.
  c.prepare("INSERT INTO flows (id, chat_id, name, tz, enabled, current_version_id, created_at, updated_at) VALUES ('flw_000000000broken00001', ?, 'Broken', 'UTC', 1, 'flv_000000000broken00001', ?, ?)").run(S.web, BASE - 1000, BASE - 1000)
  c.prepare("INSERT INTO flow_versions (id, flow_id, n, definition, hash, write_calls, created_by, created_at) VALUES ('flv_000000000broken00001', 'flw_000000000broken00001', 1, '{\"schema\":1,\"name\":\"Broken\",\"steps\":[]}', 'x', '[{\"stepId\":\"s\",\"kind\":\"message\",\"to\":\"chat_x\"}]', 'chat', ?)").run(BASE - 1000)
  c.prepare("INSERT INTO flow_triggers (id, flow_id, trigger_id, kind, status, next_fire_at, updated_at) VALUES ('ftr_000000000broken00001', 'flw_000000000broken00001', 't', 'schedule', 'active', ?, ?)").run(BASE + 86_400_000, BASE - 1000)
  c.prepare("INSERT INTO flows (id, chat_id, name, tz, enabled, created_at, updated_at) VALUES ('flw_000000000empty000001', 'chat_gone', 'Empty', 'UTC', 0, ?, ?)").run(BASE - 500, BASE - 500)
  return { ids: { kitchen: flow.id, broken: "flw_000000000broken00001", empty: "flw_000000000empty000001" } }
}

const NOMODEL: Case[] = [{ fn: "parseReminder", input: { text: "besok jam 9 bayar listrik", tz: "Asia/Makassar" } }]

// Values JSON can't carry, found in a result (the Decisions log lists them).
const nonJSON = new Set<string>()
function scan(fn: string, v: unknown, at: string) {
  if (v === undefined) nonJSON.add(`${fn}: undefined at ${at}`)
  else if (v instanceof Date) nonJSON.add(`${fn}: Date at ${at}`)
  else if (v instanceof Map || v instanceof Set) nonJSON.add(`${fn}: Map/Set at ${at}`)
  else if (typeof v === "bigint") nonJSON.add(`${fn}: bigint at ${at}`)
  else if (Array.isArray(v)) v.forEach((x) => scan(fn, x, `${at}[]`))
  else if (v && typeof v === "object") for (const [k, x] of Object.entries(v)) scan(fn, x, `${at}.${k}`)
}

const SETUP_TABLES = ["flows", "flow_versions", "flow_triggers", "flow_runs"]

async function runSequence(name: string, mode: "down" | "fake" | "nomodel", cases: Case[] | ((ids: Record<string, string>) => Case[]), setup?: (db: DB) => Promise<{ ids: Record<string, string> }>) {
  const db = scratch()
  ;(globalThis as { __rpcSched?: { db: unknown } }).__rpcSched = { db }
  runtimeMod.setRuntime(fakeRuntime(db, mode))
  const before = new Map(SETUP_TABLES.map((t) => [t, new Set(client(db).prepare(`SELECT rowid AS r FROM ${t}`).all().map((x) => x.r))]))
  clock = BASE - 10_000
  const made = setup ? await setup(db) : { ids: {} }
  // Rows the setup added, in table order (the Go test inserts them as they are).
  const setupRows = setup ? SETUP_TABLES.flatMap((t) => client(db).prepare(`SELECT rowid AS _rowid, * FROM ${t} ORDER BY rowid`).all().filter((r) => !before.get(t)!.has(r._rowid)).map(({ _rowid, ...row }) => ({ table: t, row }))) : []
  if (typeof cases === "function") cases = cases(made.ids)
  const out = []
  for (const [i, c] of cases.entries()) {
    clock = BASE + i * 1000
    const fn = fns[c.fn]
    if (!fn) throw new Error(`no server function ${c.fn}`)
    const rec: Record<string, unknown> = { fn: c.fn, method: fn.method, now: clock }
    if (c.input !== undefined) rec.input = JSON.stringify(c.input)
    try {
      const res = await fn(c.input === undefined ? undefined : { data: c.input })
      scan(c.fn, res, "result")
      rec.body = JSON.stringify(res)
    } catch (e) {
      rec.error = (e as Error).message
      if ((e as { validation?: boolean }).validation) rec.validation = true
    }
    if (c.rows) rec.rows = c.rows.map((sql) => ({ sql, rows: client(db).prepare(sql).all() }))
    out.push(rec)
  }
  return { name, runtime: mode, ...(setupRows.length ? { setup: setupRows } : {}), cases: out }
}

const FLOWS = (ids: Record<string, string>): Case[] => [
  { fn: "getFlows" },
  { fn: "getFlowFn", input: { id: ids.kitchen } },
  { fn: "getFlowFn", input: { id: ids.broken } },
  { fn: "getFlowFn", input: { id: ids.empty } },
]

const sequences = [
  await runSequence("main", "down", MAIN),
  await runSequence("runtime", "fake", RUNTIME),
  await runSequence("nomodel", "nomodel", NOMODEL),
  await runSequence("flows", "down", FLOWS, flowSetup),
]
const golden = { base: BASE, fake: FAKE, nonJSON: [...nonJSON].sort(), sequences }
writeFileSync(path.join(testdata, "rpc-sched.json"), `${JSON.stringify(golden, null, 1)}\n`)
const n = sequences.reduce((a, s) => a + s.cases.length, 0)
console.log(`wrote rpc-sched.json: ${n} calls; non-JSON values: ${golden.nonJSON.length ? golden.nonJSON.join(", ") : "none"}`)
process.exit(0)
