// Exports M5 flow goldens from the real TS code into apps/server/testdata/flows.json:
//   definitions  validateDefinition on valid and invalid definitions: ok, the
//                problems (zod's issue text included), the write calls, the
//                stored definition JSON (exact text) and the version hash
//   exprs        parseExpr + evaluate: value, trace and warnings, or the error
//   templates    renderString / renderText in data and message mode, or the error
//   taint        keyTainted, valueTainted, taintedArgs, addTaint
//   schedules    upcoming() occurrences for trigger specs across the
//                America/New_York DST changes, day filters and tool sources
//   traces       scripted flows (a small step language the Go test replays):
//                the rows of flows, flow_versions, flow_triggers, flow_runs,
//                flow_step_runs, outbox, events, pending_approvals,
//                tool_calls, outbound_messages and hooks after every step,
//                plus each step's result
//
// Ids are normalized like chat.json: every newId() id becomes <prefix>#N in
// order of first appearance in the canonical (sorted-key) JSON of its section
// (each trace is one section). events.id is dropped. Clocks are fixed.
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/flows.ts
import { writeFileSync } from "node:fs"
import { createRequire } from "node:module"
import path from "node:path"
import type { DB } from "../../../../packages/db/src/index.ts"
import { localToUtc } from "../../../../packages/core/src/reminders/time.ts"
import { canonicalJson } from "../../../../packages/core/src/flows/schema.ts"
import { resolverFrom, validateDefinition, type ValidateContext } from "../../../../packages/core/src/flows/validate.ts"
import { evaluate, parseExpr } from "../../../../packages/core/src/flows/expr.ts"
import { renderString, renderText } from "../../../../packages/core/src/flows/template.ts"
import { addTaint, keyTainted, taintedArgs, valueTainted } from "../../../../packages/core/src/flows/taint.ts"
import { upcoming } from "../../../../packages/core/src/flows/sources.ts"
import { sourceSchema } from "../../../../packages/core/src/flows/source-config.ts"
import { hashDefinition, decideVersion, setFlowPaused, updateFlow, wakeFlowRun, currentVersion, getFlow } from "../../../../packages/core/src/flows/service.ts"
import { answerFlowCheckin } from "../../../../packages/core/src/flows/checkin.ts"
import { flowActions } from "../../../../packages/core/src/flows/actions.ts"
import { ingestFlowHook } from "../../../../packages/core/src/flows/entry.ts"
import { dryRunFlow } from "../../../../packages/core/src/flows/dryrun.ts"
import { exampleConnectors, flowHarness, runsOf } from "../../../../packages/core/src/flows/test-harness.ts"
import { EXAMPLE_TOOLS, fakePrayerTool } from "../../../../packages/core/src/flows/test-helpers.ts"
import { createHook } from "../../../../packages/core/src/hooks/index.ts"
import { addContact } from "../../../../packages/core/src/outbound/service.ts"
import { decide, expireDue } from "../../../../packages/core/src/approvals/store.ts"
import { getDefaultAgent } from "../../../../packages/core/src/agents.ts"
import { flowRuns, flowStepRuns, hooks } from "../../../../packages/db/src/index.ts"

const dbRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/db/package.json"))
const { sql, eq } = dbRequire("drizzle-orm") as typeof import("drizzle-orm")

const WITA = "Asia/Makassar"
const NY = "America/New_York"
const KEY = "0123456789abcdef0123456789abcdef"
const at = (local: string, tz = WITA) => localToUtc(local, tz)

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

// Non-finite numbers and undefined, which JSON can't hold.
function plain(v: unknown): unknown {
  if (v === undefined) return { $undefined: true }
  if (typeof v === "number" && !Number.isFinite(v)) return { $num: String(v) }
  if (Array.isArray(v)) return v.map(plain)
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, plain(x)]))
  return v
}

function rows(db: DB, table: string) {
  return db.all(sql.raw(`SELECT * FROM ${table} ORDER BY rowid`)) as Record<string, unknown>[]
}

// --- definitions -----------------------------------------------------------------------

const vctx: ValidateContext = {
  resolveTool: resolverFrom(EXAMPLE_TOOLS),
  hookExists: (id) => id === "hk_github_palui",
  chatId: "chat_owner",
  chatAllowed: (id) => (id === "chat_owner" || id === "chat_web" ? "own" : id === "chat_family" ? "allowed" : false),
  resolveContact: (ref) => (ref.toLowerCase() === "zaki" || ref === "ctc_zaki" ? { id: "ctc_zaki", name: "Zaki" } : null),
  flowNames: ["Olahraga", "T"],
}

const msg = (id: string, text = "x", extra: Record<string, unknown> = {}) => ({ id, kind: "message", text, ...extra })
const one = (steps: unknown[], extra: Record<string, unknown> = {}) => ({ name: "T", steps, ...extra })
const emoji = (n: number) => "😀".repeat(n)

const DEFINITIONS: { name: string; def: unknown }[] = [
  // Valid ones, defaults filled.
  { name: "minimal", def: one([msg("a")]) },
  { name: "every step kind", def: {
    name: "  Semua  ",
    description: "Contoh",
    tz: "Asia/Jakarta",
    limits: { runsPerHour: 5, dailyTokens: 2000 },
    triggers: [
      { id: "s1", kind: "schedule", start: "t", source: { kind: "times", times: [{ time: "06:00" }] }, days: ["MO", "FR"], lateGraceMin: 30 },
      { id: "s2", kind: "schedule", start: "t", source: { kind: "rrule", rrule: "FREQ=DAILY", start: "2026-11-10T07:00" } },
      { id: "s3", kind: "schedule", start: "t", source: { kind: "tool", connectorId: "mcp-x", tool: "times", list: { at: "due" } } },
      { id: "s4", kind: "schedule", start: "t", source: { kind: "tool", connectorId: "mcp-x", tool: "times", items: [{ field: "a.b" }] }, days: null },
      { id: "w", kind: "webhook", hookId: "hk_github_palui", start: "t" },
      { id: "c", kind: "chat", command: " /catat ", start: "t" },
      { id: "c2", kind: "chat", phrases: [" udah "], start: "t" },
      { id: "p", kind: "poll", tool: "mcp-github__get_commit_status", everyMin: 5, start: "t" },
    ],
    steps: [
      { id: "t", kind: "tool", tool: "reminders__list_reminders", parse: { as: "json" }, retry: { max: 1 }, timeoutSec: 10 },
      { id: "i", kind: "if", when: "steps.t.ok", then: "m" },
      { id: "m", kind: "message", text: " hai ", name: "Pesan" },
      { id: "k", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 30 } },
      { id: "w", kind: "wait", minutes: 5 },
      { id: "p", kind: "prompt", prompt: "Ringkas", input: { Data: "{{steps.t.data}}" } },
      { id: "s", kind: "set", values: { n: "1" } },
      { id: "e", kind: "each", items: "steps.t.data", body: "b", next: null },
      { id: "b", kind: "message", text: "{{item}}" },
    ],
  } },
  { name: "write calls: tool, contact (unsigned) and an allowed chat", def: one([
    { id: "pay", kind: "tool", connectorId: "mcp-keuangan", tool: "mark_paid", args: { id: "{{trigger.id}}", n: 2 } },
    msg("tell", "x", { contact: "Zaki", signature: false }),
    msg("fam", "x", { to: "chat_family" }),
    msg("own", "x", { to: "chat_web" }),
  ]) },
  { name: "emoji at the limit (code points, not UTF-16 units)", def: { name: emoji(80), steps: [msg("a")] } },
  // zod issues.
  { name: "not an object", def: "flow" },
  { name: "empty object", def: {} },
  { name: "emoji over the limit", def: { name: emoji(81), steps: [msg("a")] } },
  { name: "wrong types everywhere", def: { name: 5, description: "", tz: 3, triggers: "x", steps: [{ id: 1, kind: "message", text: null }], limits: { runsPerHour: 0.5, dailyTokens: 999 } } },
  { name: "unknown kinds", def: { name: "T", triggers: [{ id: "x", kind: "cron", start: "a" }, { kind: "manual" }], steps: [{ id: "a", kind: "shell" }, "str"] } },
  { name: "bad ids and targets", def: one([msg("Bad-Id", "x", { next: "Nope" }), { id: "i", kind: "if", when: "true", then: 3, else: "X" }, msg("c", "x", { onError: "Retry" })]) },
  { name: "onError not a string", def: one([msg("a", "x", { onError: 5 })]) },
  { name: "numbers out of range", def: { name: "T", triggers: [{ id: "p", kind: "poll", tool: "x", everyMin: 1, start: "a", lateGraceMin: 2000 }], steps: [msg("a", "x", { timeoutSec: 61 }), { id: "w", kind: "wait", minutes: 0, plusMin: -20000 }, { id: "e", kind: "each", items: "x", body: "a", max: 51 }] } },
  { name: "non-integers", def: one([{ id: "w", kind: "wait", minutes: 1.5 }, { id: "t", kind: "tool", tool: "x", retry: { max: 2.5, backoffSec: 4 } }]) },
  { name: "strings too long and empty after trim", def: { name: "   ", description: "d".repeat(201), steps: [msg("a", "x".repeat(4001)), { id: "p", kind: "prompt", prompt: " " }] } },
  { name: "enums and literals", def: { schema: 2, name: "T", triggers: [{ id: "p", kind: "poll", tool: "x", everyMin: 5, start: "a", fire: "always" }, { id: "e", kind: "event", event: "run.started", start: "a" }], steps: [{ id: "a", kind: "set", scope: "global", values: { a: "1" } }, { id: "p", kind: "prompt", prompt: "x", model: "big" }] } },
  { name: "deadline unions", def: one([
    { id: "a", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 0 } },
    { id: "b", kind: "checkin", text: "?", deadline: { kind: "after", minutes: "" } },
    { id: "c", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 1.5 } },
    { id: "d", kind: "checkin", text: "?", deadline: { kind: "soon" } },
    { id: "e", kind: "checkin", text: "?", deadline: { kind: "next", plusMin: 2000 } },
  ]) },
  { name: "answers refine and words", def: one([
    { id: "a", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 5 }, answers: [{ id: "x", label: "X", closes: false }, { id: "x", label: "Y", closes: false }] },
    { id: "b", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 5 }, answers: [] },
    { id: "c", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 5 }, answers: [{ id: "Bad Id", label: "", words: ["", "w".repeat(41)] }] },
    { id: "d", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 5 }, answers: [{ id: 1, label: "L" }] },
  ]) },
  { name: "record keys", def: one([
    { id: "s", kind: "set", values: { Bad: "1", ok: "2", "9x": "3" } },
    { id: "s2", kind: "set", values: {} },
    { id: "p", kind: "prompt", prompt: "x", input: { "bad key": "v", ok: 5 } },
    { id: "k", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 5 }, on: { done: "Nope" } },
  ]) },
  { name: "sources", def: { name: "T", triggers: [
    { id: "a", kind: "schedule", start: "m", source: { kind: "times", times: [{ time: "6:00" }, { time: "24:00", label: "" }] } },
    { id: "b", kind: "schedule", start: "m", source: { kind: "rrule", rrule: "", start: "2026-11-10 07:00" } },
    { id: "c", kind: "schedule", start: "m", source: { kind: "tool", connectorId: "", tool: "t", refreshAt: "25:00", items: [], list: { at: "", time: "9am", offsetMin: 20000 } } },
    { id: "d", kind: "schedule", start: "m", source: { kind: "weather" }, days: ["MO", "XX"] },
    { id: "e", kind: "schedule", start: "m", source: { kind: "times", times: [] }, days: [] },
  ], steps: [msg("m")] } },
  { name: "chat and poll strings", def: { name: "T", triggers: [
    { id: "c", kind: "chat", command: "beres", start: "m" },
    { id: "c2", kind: "chat", phrases: [], start: "m" },
    { id: "p", kind: "poll", tool: "x", everyMin: 5, select: "items[0]", start: "m" },
  ], steps: [msg("m")] } },
  { name: "more than 12 issues", def: { name: "T", steps: Array.from({ length: 14 }, (_, i) => ({ id: `S${i}`, kind: "message", text: "" })) } },
  { name: "too many steps and triggers", def: { name: "T", triggers: Array.from({ length: 9 }, (_, i) => ({ id: `t${i}`, kind: "manual", start: "s0" })), steps: Array.from({ length: 41 }, (_, i) => msg(`s${i}`)) } },
  { name: "over 32 KB", def: one(Array.from({ length: 10 }, (_, i) => msg(`s${i}`, "é".repeat(2000)))) },
  { name: "under 32 KB in bytes", def: one(Array.from({ length: 8 }, (_, i) => msg(`s${i}`, "x".repeat(3900)))) },
  // Graph, data and tool checks.
  { name: "unknown zone and duplicate ids", def: { name: "T", tz: "Mars/Base", triggers: [{ id: "t", kind: "manual", start: "a" }, { id: "t", kind: "manual", start: "zz" }], steps: [msg("a"), msg("a"), msg("continue")] } },
  { name: "if with next, missing targets, on answers", def: one([
    { id: "a", kind: "if", when: "true", then: "b", next: "b" },
    { id: "b", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 5 }, on: { nope: "a", not_yet: "a" }, onMissed: "gone" },
  ]) },
  { name: "cycle", def: one([msg("a", "x", { next: "b" }), msg("b", "x", { next: "a" })]) },
  { name: "unreachable and nested each", def: one([
    { id: "t", kind: "tool", tool: "mcp-keuangan__list_bills" },
    { id: "o", kind: "each", items: "steps.t.data", body: "i", next: null },
    { id: "i", kind: "each", items: "item.list", body: "m", next: null },
    msg("m", "{{index}}"),
    msg("orphan", "{{item.x}}"),
  ]) },
  { name: "expressions and templates", def: one([
    { id: "a", kind: "if", when: "steps.x =", then: null },
    { id: "b", kind: "set", values: { n: "len(", m: "{{ steps.x | lines(1, 2) }}", o: "{{steps.x | truncate(\"a\") }}" } },
    { id: "c", kind: "each", items: "steps.x.y + 1", body: "d", next: null, key: "plain" },
    msg("d", "{{item.name | upper(1)}} {{ foo }} {{steps.x"),
  ]) },
  { name: "literal places", def: one([
    { id: "a", kind: "tool", tool: "reminders__list_reminders", args: { "{{k}}": "v" } },
    { id: "b", kind: "prompt", prompt: "{{trigger.x}}" },
    msg("c", "x", { to: "{{trigger.chat}}" }),
    msg("d", "x", { contact: "{{vars.who}}" }),
    msg("e", "x", { to: "chat_family", contact: "Zaki" }),
    msg("f", "x", { signature: true }),
    { id: "w", kind: "wait", until: "tomorrow", plusMin: 5 },
    { id: "w2", kind: "wait", minutes: 5, plusMin: 5 },
    { id: "w3", kind: "wait", minutes: 5, until: "2026-11-11" },
  ]) },
  { name: "triggers checks", def: { name: "T", triggers: [
    { id: "c", kind: "chat", start: "m" },
    { id: "c2", kind: "chat", command: "/help", phrases: ["x"], start: "m" },
    { id: "w", kind: "webhook", hookId: "hk_nope", start: "m" },
    { id: "e", kind: "event", event: "run.failed", start: "m" },
    { id: "e2", kind: "event", event: "run.done", flow: "Nobody", start: "m" },
    { id: "p", kind: "poll", tool: "nope", connectorId: "mcp-x", everyMin: 5, start: "m" },
    { id: "p2", kind: "poll", tool: "notes__save_note", everyMin: 5, start: "m", args: { a: "{{trigger.x}}", b: ["{{y}}"] } },
  ], steps: [msg("m"), { id: "k", kind: "checkin", text: "?", next: null }] } },
  { name: "tools and side effects", def: one([
    { id: "a", kind: "tool", tool: "reminders__cancel_reminder" },
    { id: "b", kind: "tool", tool: "nope", connectorId: "mcp-x" },
    { id: "c", kind: "prompt", prompt: "x", tools: ["*", "nope__x", "reminders__cancel_reminder", "notes__save_note"] },
    msg("d", "x", { contact: "Budi" }),
    msg("e", "x", { to: "chat_stranger" }),
  ]) },
]

function definitions() {
  return DEFINITIONS.map((d) => {
    const r = validateDefinition(d.def, vctx)
    return {
      name: d.name,
      // Inputs are JSON text, so normalize() doesn't sort their keys.
      def: JSON.stringify(d.def),
      ok: r.ok,
      problems: r.problems,
      writeCalls: r.writeCalls,
      stored: r.definition ? JSON.stringify(r.definition) : null,
      canonical: r.definition ? canonicalJson(r.definition) : null,
      hash: r.definition ? hashDefinition(r.definition) : null,
    }
  })
}

// --- expressions -----------------------------------------------------------------------

const NOW = at("2026-11-11T09:00")
const SCOPE = {
  trigger: { list: [{ s: "a", n: 1 }, { s: "b", n: "2" }, { s: "a" }], text: "Halo Dunia", label: "Subuh", args: { mode: "a", n: "07", d: "2026-11-13" }, empty: [], obj: {}, big: "x".repeat(250), nested: { a: [1, [2, 3], { b: null }] } },
  steps: { bills: { data: { items: [{ due: "2026-11-13", id: "a" }, { due: "2026-11-09", id: "b" }] } }, ci: { ok: true, data: { state: "success" } } },
  vars: { n: 3, s: "3.5", z: 0, f: false, neg: -2 },
  flow: { paid: { a: true }, streak: 2, name: "F" },
  item: { id: "a", due: "2026-11-12T10:00", name: "Listrik" },
  index: 1,
  now: { at: NOW, date: "2026-11-11", time: "09:00", weekday: "WE" },
  run: { id: "flr_x", attempt: 1, followUps: 2 },
}

const EXPRS: { src: string; tz?: string; now?: number }[] = [
  ...[
    "1 + 2 * 3", "(1 + 2) * 3", "10 / 4", "1 / 0", "-vars.n", "- -3", "-trigger.text", "7 - 2 - 1", "2 * 3 / 4", "0.1 + 0.2", "1.5 * 2", "vars.n * vars.s", "vars.neg * 0",
    "not true", "not 0", "not \"\"", "not trigger.empty", "not trigger.obj", "true and 0", "1 or false", "null or vars.z", "vars.f == false",
    "3 == \"3\"", "\"3\" == \"3.0\"", "3 == \"3.0\"", "\"03\" == 3", "true == \"true\"", "null == trigger.nope", "trigger.list == trigger.list", "trigger.obj == steps.nope", "trigger.args == trigger.args",
    "1 != 2", "trigger.nope != null", "2 < 10", "\"2\" < \"10\"", "\"b\" < \"a\"", "\"2026-11-13\" > \"2026-11-12\"", "trigger.text < 3", "trigger.list > 1", "true < 2", "trigger.nope <= 0", "vars.s >= 3.5",
    "\"a\" + \"b\"", "\"a\" + 1", "\"3\" + 1", "1 + \"x\"", "\"x\" + trigger.list", "\"x\" + trigger.obj", "\"x\" + null", "null + null", "flow.nope + 1", "flow.streak - trigger.nope", "true + 1", "trigger.list * 2", "null * 2", "trigger.text / 2",
    "steps.bills.data.items.0.due", "steps.bills.data.items[1][\"id\"]", "steps.bills.data.items[index].id", "flow.paid[item.id]", "flow.paid[item.nope]", "steps.bills.data.items.5", "steps.bills.data.items[-1]", "trigger.nested.a.1.0", "trigger.nested.a.2.b", "trigger.text.length", "trigger.list[vars.n]", "steps[\"ci\"].data.state", "run.followUps", "now.weekday", "index",
    "exists(trigger.text)", "exists(trigger.nope)", "exists(null)", "empty(trigger.empty)", "empty(trigger.obj)", "empty(0)", "empty(\"\")",
    "contains(trigger.text, \"Dunia\")", "contains(trigger.text, 1)", "contains(trigger.list, \"a\")", "contains(steps.bills.data.items, null)", "contains([1], 1)",
    "contains(\"a12b\", 12)", "startsWith(trigger.text, \"Ha\")", "startsWith(123, 1)",
    "len(trigger.text)", "len(trigger.list)", "len(trigger.obj)", "len(5)", "len(\"😀\")",
    "lower(trigger.text)", "lower(5)", "lower(\"İSTANBUL\")",
    "any(trigger.list, \"s\", \"b\")", "count(trigger.list, \"s\", \"a\")", "count(trigger.list, \"n\", 2)", "any(trigger.text, \"s\", 1)", "count(trigger.list, \"\", null)",
    "number(\"12.5\")", "number(\" 7 \")", "number(\"1e3\")", "number(\"-0\")", "number(true)",
    "round(2.456, 2)", "round(2.5)", "round(-2.5)", "round(1.005, 2)", "round(\"x\")", "round(2.45, 9)", "round(2.45, -1)",
    "min(3, 5)", "max(\"3\", 5)", "min(null, 4)", "max(\"x\", null)",
    "choose(3 > 2, \"a\", \"b\")", "choose(null, 1, 2)", "choose(trigger.list, trigger.text, 0)",
    "date(\"2026-11-11\")", "date(\"2026-11-11T04:39:00+08:00\")", "date(\"2026-11-11T04:39+0800\")", "date(\"2026-11-11T04:39:00.123456Z\")", "date(\"2026-11-11T04:39Z\")", "date(\"2026-13-01\")", "date(\"2026-02-30\")",
    "date(\"04:39\")", "date(\"4.39\")", "date(\"24:00\")", "date(\"2026-11-11 08:15\")", "date(\"2026-11-11T08:15:30.5\")", "date(1794429540000)", "date(1794429540)", "date(12345)", "date(\"soon\")", "date(trigger.list)",
    "daysUntil(\"2026-11-13\")", "daysUntil(\"2026-11-11T23:59\")", "daysUntil(\"2026-11-01\")", "daysUntil(\"bad\")", "daysUntil(null)", "daysUntil(trigger.list)",
    "hoursUntil(\"2026-11-11T12:00\")", "hoursUntil(\"2026-11-11T09:20\")", "minutesSince(\"08:30\")", "minutesSince(\"2026-11-11T09:00:59Z\")",
    "weekday(\"2026-11-15\")", "weekday()", "weekday(\"bad\")", "today()",
    "daysUntil(item.due) < 3 and not flow.paid[item.id]", "trigger.big",
  ].map((src) => ({ src })),
  ...[
    "daysUntil(\"2026-11-02\")", "daysUntil(\"2026-11-01T23:30\")", "hoursUntil(\"2026-11-01T03:00\")", "date(\"2026-11-01T01:30\")", "date(\"2026-03-08T02:30\")", "weekday()", "today()", "minutesSince(\"00:30\")",
  ].map((src) => ({ src, tz: NY, now: Date.parse("2026-11-01T05:30:00Z") })),
  ...["daysUntil(\"2026-03-09\")", "date(\"02:30\")", "hoursUntil(\"2026-03-08T04:00\")"].map((src) => ({ src, tz: NY, now: Date.parse("2026-03-08T06:30:00Z") })),
  // Parse errors.
  ...[
    "", "   ", "foo.bar", "n + 1", "eval(1)", "vars.a = 1", "true && false", "a || b", "!true", "1 < 2 < 3", "len(1, 2)", "round()", "weekday(1, 2)", "today(1)", "\"open", "'esc\\'", "(1 + 2", "1 2",
    "and 1", "steps.", "steps.x[foo]", "steps.x[", "@", "#x", "steps.0.1", "x".repeat(2001), `${"1 + ".repeat(200)}1`, "trigger.list[vars.n", "len(", "1 +", "steps.x.\"y\"", "null.x", "trigger.a.b(1)",
  ].map((src) => ({ src })),
]

function exprs() {
  return EXPRS.map((c) => {
    const tz = c.tz ?? WITA
    const now = c.now ?? NOW
    try {
      const r = evaluate(parseExpr(c.src), { scope: SCOPE, tz, now })
      return { ...c, tz, now, result: plain(r) }
    } catch (e) {
      return { ...c, tz, now, error: (e as Error).message }
    }
  })
}

// --- templates -------------------------------------------------------------------------

const TSCOPE = {
  ...SCOPE,
  trigger: { ...SCOPE.trigger, payload: { title: `Fix\r\n\n\n[Ya](tg://x)\u0007\u2028 now ${"y".repeat(600)}` } },
  steps: {
    ...SCOPE.steps,
    r: { data: { text: "📊 Rekap\r\n\n\n\n- Sudah: 3\t!", list: Array.from({ length: 23 }, (_, i) => ({ t: `#${i}`, n: { v: i } })), nums: [1, 2.5, null, "x", true, [1, 2], { a: 1 }] } },
  },
}

const TEMPLATES: { src: string; mode: "data" | "message" | "text"; taint?: string[] }[] = [
  ...[
    "no template", "{{trigger.args}}", "n={{trigger.args.n}}", "{{ steps.r.data.nums }}", "x{{steps.r.data.nums}}", "{{steps.nope}}", "a {{steps.nope}} b", "{{vars.f}}{{vars.z}}{{vars.neg}}",
    "{{now.date | date}}", "{{trigger.args.d | date}}", "{{\"2027-01-02\" | date}}", "{{item.due | time}}", "{{trigger.text | time}}", "{{vars.n | time}}",
    "{{steps.r.data.nums | json}}", "{{trigger.nope | json}}", "{{trigger.nope | default(\"-\")}}", "{{trigger.empty | default(5)}}", "{{trigger.text | default(\"-\")}}",
    "{{steps.r.data.nums | join}}", "{{steps.r.data.nums | join(\" | \")}}", "{{steps.r.data.nums | join(0)}}", "{{trigger.text | join}}",
    "{{steps.r.data.list | lines(\"t\")}}", "{{steps.r.data.list | lines(\"n.v\")}}", "{{steps.r.data.nums | lines}}", "{{steps.r.data.nums | lines(\"length\")}}",
    "{{trigger.text | upper}}", "{{\"straße\" | upper}}", "{{trigger.text | lower}}", "{{vars.s | round}}", "{{vars.s | round(1)}}", "{{trigger.text | round}}", "{{vars.n | round(\"2\")}}",
    "{{trigger.text | truncate(3)}}", "{{trigger.text | truncate(0)}}", "{{trigger.text | truncate(50)}}", "{{steps.r.data.nums | truncate(5)}}", "{{trigger.big | truncate(\"a\")}}",
    "{{trigger.text | lower | upper | truncate(4)}}", "{{ trigger.text|upper }}", "{{trigger.list[1].s}}", "{{steps[\"ci\"].data.state | upper}}", "{{\"x\" | default('y,z') | join(\"\\n\")}}",
  ].map((src) => ({ src, mode: "data" as const })),
  ...[
    "PR: {{trigger.payload.title}}\nDone", "{{steps.r.data.text}}", "{{steps.r.data.list | lines(\"t\")}}", "{{trigger.payload}}", "{{trigger.payload.title | truncate(10)}}", "{{trigger.big}}",
  ].flatMap((src) => [
    { src, mode: "message" as const },
    { src, mode: "message" as const, taint: ["trigger"] },
    { src, mode: "text" as const, taint: [] },
  ]),
  { src: `${"x".repeat(3990)}{{trigger.big}}`, mode: "text", taint: [] },
  // Errors.
  ...["{{steps.x", "{{}}", "a {{  }} b", "{{foo}}", "{{steps.x | shout}}", "{{steps.x | truncate}}", "{{steps.x | time(1)}}", "{{steps.x | join(1, 2)}}", "{{steps.x | lines(x)}}", "{{steps.x | 9}}", "{{len(steps.x)}}", "😀{{steps.x", "{{steps.x | default(\"a\" \"b\")}}"].map((src) => ({ src, mode: "data" as const })),
]

function templates() {
  return TEMPLATES.map((c) => {
    const env = { scope: TSCOPE, tz: WITA, now: NOW }
    const tainted = c.taint ? (k: string) => keyTainted(c.taint!, k) : undefined
    try {
      const out = c.mode === "text" ? renderText(c.src, env, tainted) : renderString(c.src, env, c.mode, tainted)
      return { ...c, result: plain(out) }
    } catch (e) {
      return { ...c, error: (e as Error).message }
    }
  })
}

// --- taint -----------------------------------------------------------------------------

const TAINT_KEYS = ["trigger", "steps", "steps.weather", "steps.prayer", "vars", "vars.x", "flow", "flow.n", "item", "index", "now", "run"]
const TAINT_VALUES: unknown[] = ["plain", "{{trigger.x}}", { ref: "{{trigger.payload.sha}}" }, { a: ["{{steps.weather.data}}"] }, "{{steps[vars.k]}}", "{{now.date}} {{run.id}}", "{{flow.n | upper}}", { deep: { er: { x: "{{item.id}}" } } }, ["{{index}}"]]
const TAINT_ARGS = { owner: "zakiego", ref: "{{trigger.payload.sha}}", note: "{{steps.prayer.data}}", list: ["{{vars.x}}"], n: 3, k: "{{flow.n}}" }

function taint() {
  const sets: string[][] = [[], ["trigger"], ["steps.weather", "vars.x"], ["flow.n", "item"], ["steps.b", "steps.a", "trigger"]]
  const keys = TAINT_KEYS
  const values = TAINT_VALUES
  const args = TAINT_ARGS
  return sets.map((s) => ({
    taint: s,
    keys: Object.fromEntries(keys.map((k) => [k, keyTainted(s, k)])),
    values: values.map((v) => valueTainted(s, v)),
    args: taintedArgs(s, args),
    added: addTaint(s, "trigger", "steps.z", "item"),
  }))
}

// --- schedules -------------------------------------------------------------------------

const SCHEDULES: { name: string; source: unknown; tz: string; days?: string[] | null; cursor: { at: number; key: string }; n: number; state?: unknown }[] = [
  { name: "NY times across fall back", source: { kind: "times", times: [{ time: "01:30", label: "a" }, { time: "02:30" }, { time: "00:00" }] }, tz: NY, cursor: { at: Date.parse("2026-10-31T12:00:00Z"), key: "" }, n: 8 },
  { name: "NY times across spring forward", source: { kind: "times", times: [{ time: "02:30" }, { time: "03:00" }] }, tz: NY, cursor: { at: Date.parse("2026-03-07T12:00:00Z"), key: "" }, n: 6 },
  { name: "NY rrule daily 02:30 across spring forward", source: { kind: "rrule", rrule: "FREQ=DAILY", start: "2026-03-06T02:30" }, tz: NY, cursor: { at: Date.parse("2026-03-07T00:00:00Z"), key: "" }, n: 4 },
  { name: "NY rrule daily 01:30 across fall back", source: { kind: "rrule", rrule: "FREQ=DAILY", start: "2026-10-30T01:30", label: "Pagi" }, tz: NY, cursor: { at: Date.parse("2026-10-31T00:00:00Z"), key: "" }, n: 4 },
  { name: "weekly with days filter", source: { kind: "times", times: [{ time: "07:00" }] }, tz: WITA, days: ["SA", "SU"], cursor: { at: at("2026-11-10T10:00"), key: "" }, n: 4 },
  { name: "rrule monthly last friday", source: { kind: "rrule", rrule: "FREQ=MONTHLY;BYDAY=-1FR", start: "2026-11-27T17:00" }, tz: WITA, days: ["FR"], cursor: { at: at("2026-11-10T10:00"), key: "" }, n: 3 },
  { name: "same time two items in key order", source: { kind: "times", times: [{ time: "09:00", label: "b" }, { time: "09:00", label: "a" }] }, tz: WITA, cursor: { at: at("2026-11-10T09:00"), key: "2026-11-10:09:00" }, n: 3 },
  {
    name: "tool items with fallback",
    source: { kind: "tool", connectorId: "c", tool: "t", items: [{ field: "x" }] },
    tz: NY,
    cursor: { at: Date.parse("2026-10-31T12:00:00Z"), key: "" },
    n: 6,
    state: { entries: [{ date: "2026-10-30", key: "x", label: "X", at: Date.parse("2026-10-30T05:30:00Z") }, { date: "2026-10-31", key: "x", label: "X", at: Date.parse("2026-10-31T05:30:00Z") }], fetched: ["2026-10-31", "2026-10-30"] },
  },
  {
    name: "tool list",
    source: { kind: "tool", connectorId: "c", tool: "t", list: { path: "bills", at: "due" } },
    tz: WITA,
    cursor: { at: at("2026-11-10T10:00"), key: "" },
    n: 5,
    state: { entries: [{ date: "2026-11-12", key: "09:00:B", label: "B", at: at("2026-11-12T09:00") }, { date: "2026-11-11", key: "09:00:A", label: "A", at: at("2026-11-11T09:00") }, { date: "2026-11-09", key: "09:00:Z", label: "Z", at: at("2026-11-09T09:00") }] },
  },
]

function schedules() {
  return SCHEDULES.map((c) => {
    try {
      const source = sourceSchema.parse(c.source)
      const occ = upcoming({ source, tz: c.tz, days: c.days ?? null, state: (c.state ?? {}) as never }, c.cursor, c.n)
      return { ...c, occurrences: occ }
    } catch (e) {
      return { ...c, error: (e as Error).message }
    }
  })
}

// --- traces ----------------------------------------------------------------------------
// A scenario is fake tools plus steps. Strings in steps may hold references the
// replayer resolves: {chat} {other} {flow:F} {version:F} {run:F:R} {approval:N}
// {hook:N} {contact:N}. Times are WITA local datetimes.

type Spec = { returns?: unknown; fails?: string; sequence?: Spec[] }
type Step = Record<string, unknown> & { do: string }
type Scenario = { name: string; tools?: Record<string, Spec>; prompt?: { status: string; text: string; tokens?: [number, number, number]; tainted?: boolean }; readSource?: "prayer"; steps: Step[] }

const SCENARIOS: Scenario[] = [
  {
    name: "scheduled message",
    steps: [
      { do: "create", def: { name: "Pagi", triggers: [{ id: "main", kind: "schedule", start: "hi", source: { kind: "times", times: [{ time: "06:00", label: "Pagi" }] }, lateGraceMin: 30 }], steps: [{ id: "hi", kind: "message", text: "Selamat {{trigger.label | lower}}! {{trigger.date}} {{trigger.time}}" }] } },
      { do: "sweep", at: "2026-11-11T06:00" },
      { do: "sweep", at: "2026-11-12T06:05" },
      { do: "sweep", at: "2026-11-13T07:00" },
    ],
  },
  {
    name: "poll with dedupe",
    tools: { "mcp-github__get_commit_status": { sequence: [{ returns: { state: "pending" } }, { returns: { state: "pending" } }, { returns: { state: "success" } }, { returns: { state: "success" } }, { fails: "503" }] } },
    steps: [
      { do: "create", def: { name: "CI", triggers: [{ id: "ci", kind: "poll", tool: "mcp-github__get_commit_status", args: { ref: "main" }, everyMin: 5, select: "state", start: "say" }], steps: [{ id: "say", kind: "message", text: "CI: {{trigger.previous}} -> {{trigger.value}}" }] } },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "sweep", at: "2026-11-10T10:05" },
      { do: "sweep", at: "2026-11-10T10:10" },
      { do: "sweep", at: "2026-11-10T10:15" },
      { do: "sweep", at: "2026-11-10T10:20" },
    ],
  },
  {
    name: "chat trigger",
    steps: [
      { do: "create", def: { name: "Beres", triggers: [{ id: "cmd", kind: "chat", command: "/beres", start: "ok" }, { id: "say", kind: "chat", phrases: ["udah bayar listrik"], start: "ok" }], steps: [{ id: "ok", kind: "message", text: "Dicatat: {{trigger.args | default(\"-\")}}" }] } },
      { do: "chat", text: "/beres listrik november", messageId: "msg_1", fromOwner: true, at: "2026-11-10T10:00" },
      { do: "chat", text: "/beres@palui_bot", messageId: "msg_1", fromOwner: true, at: "2026-11-10T10:00" },
      { do: "chat", text: "Udah bayar listrik!", messageId: "msg_2", fromOwner: true, at: "2026-11-10T10:00" },
      { do: "chat", text: "/beres", messageId: "msg_3", fromOwner: false, at: "2026-11-10T10:00" },
      { do: "chat", text: "/beres", messageId: "msg_4", fromOwner: true, chat: "{other}", at: "2026-11-10T10:00" },
      { do: "sweep", at: "2026-11-10T10:00" },
    ],
  },
  {
    name: "hook trigger",
    steps: [
      { do: "hook", name: "GitHub", mode: "forward", at: "2026-11-10T09:00" },
      { do: "create", def: { name: "PR", triggers: [{ id: "gh", kind: "webhook", hookId: "{hook:0}", start: "say" }], steps: [{ id: "say", kind: "message", text: "PR #{{trigger.payload.number}}: {{trigger.payload.title}} ({{trigger.headers[\"x-github-event\"] | default(\"?\")}})" }] } },
      { do: "ingest", hook: 0, body: "{\"number\":7,\"title\":\"feat: x\\nline two\"}", headers: { "x-github-delivery": "abc-1", "x-github-event": "pull_request", "content-type": "application/json", authorization: "secret" }, at: "2026-11-10T10:00" },
      { do: "ingest", hook: 0, body: "{\"number\":7}", headers: { "x-github-delivery": "abc-1" }, at: "2026-11-10T10:01" },
      { do: "ingest", hook: 0, body: "plain text body", headers: {}, at: "2026-11-10T10:02" },
      { do: "sweep", at: "2026-11-10T10:02" },
    ],
  },
  {
    name: "prompt step",
    tools: { "flows__recap": { returns: { total: 35, missed: 2, byAnswer: { done: 33 }, text: "📊 Rekap: terlewat 2" } } },
    prompt: { status: "ok", text: "Semangat!", tokens: [20000, 100, 18000] },
    steps: [
      { do: "create", def: { name: "Rekap", steps: [
        { id: "recap", kind: "tool", tool: "flows__recap", args: { days: 7 } },
        { id: "cheer", kind: "prompt", prompt: "Tulis pesan pendek.", input: { recap: "{{steps.recap.data}}", label: "{{trigger.label}}" }, model: "fast", send: true },
        { id: "say", kind: "message", text: "{{steps.cheer.text}}" },
      ] } },
      { do: "start", flow: 0, kind: "manual", key: "m1", payload: { label: "Minggu ini" } },
      { do: "sweep", at: "2026-11-10T10:00" },
    ],
  },
  {
    name: "read tool, untrusted",
    tools: { "mcp-github__get_commit_status": { returns: { content: [{ type: "text", text: "{\"state\":\"failure\",\"note\":\"line\\nbreak\"}" }] } } },
    steps: [
      { do: "create", def: { name: "Cek", steps: [
        { id: "ci", kind: "tool", tool: "mcp-github__get_commit_status", args: { ref: "{{trigger.ref}}" } },
        { id: "ok", kind: "if", when: "steps.ci.data.state == \"success\"", then: "good", else: "bad" },
        { id: "good", kind: "message", text: "hijau", next: null },
        { id: "bad", kind: "message", text: "CI {{steps.ci.data.state}}: {{steps.ci.data.note}}" },
      ] } },
      { do: "start", flow: 0, kind: "manual", key: "m1", payload: { ref: "main" } },
      { do: "sweep", at: "2026-11-10T10:00" },
    ],
  },
  {
    name: "write tool with version approval",
    steps: [
      { do: "create", def: { name: "Catat", steps: [{ id: "hi", kind: "message", text: "Mulai" }, { id: "note", kind: "tool", tool: "notes__save_note", args: { text: "{{now.date}}" } }, { id: "done", kind: "message", text: "Beres" }] } },
      { do: "start", flow: 0, kind: "manual", key: "m1" },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "text", text: "ya", at: "2026-11-10T10:05" },
      { do: "sweep", at: "2026-11-10T10:05" },
      { do: "update", flow: 0, patch: { steps: { done: { text: "Beres banget" } } }, at: "2026-11-10T10:10" },
      { do: "update", flow: 0, patch: { steps: { note: { args: { text: "beda" } } } }, at: "2026-11-10T10:11" },
      { do: "callback", data: "flowver:deny:{version:0}", at: "2026-11-10T10:12" },
    ],
  },
  {
    name: "every_run approval",
    steps: [
      { do: "create", def: { name: "Komen", steps: [{ id: "c", kind: "tool", tool: "mcp-github__add_comment", args: { body: "ok {{trigger.n}}" } }, { id: "m", kind: "message", text: "dikomen" }] } },
      { do: "decide", flow: 0, approved: true, at: "2026-11-10T09:59" },
      { do: "start", flow: 0, kind: "webhook", key: "w1", payload: { n: 1 }, taint: ["trigger"] },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "approve", approval: 0, approved: true, at: "2026-11-10T10:03" },
      { do: "sweep", at: "2026-11-10T10:03" },
      { do: "start", flow: 0, kind: "manual", key: "m2", payload: { n: 2 }, at: "2026-11-10T10:04" },
      { do: "sweep", at: "2026-11-10T10:04" },
      { do: "expire", at: "2026-11-10T10:20" },
      { do: "sweep", at: "2026-11-10T10:20" },
    ],
  },
  {
    name: "wait",
    steps: [
      { do: "create", def: { name: "Tunggu", steps: [{ id: "w", kind: "wait", minutes: 30 }, { id: "w2", kind: "wait", until: "{{trigger.until}}", plusMin: 5 }, { id: "m", kind: "message", text: "bangun" }] } },
      { do: "start", flow: 0, kind: "manual", key: "m1", payload: { until: "2026-11-10T11:00" } },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "sweep", at: "2026-11-10T10:30" },
      { do: "sweep", at: "2026-11-10T11:06" },
    ],
  },
  {
    name: "check-in answered",
    steps: [
      { do: "create", def: { name: "Obat", triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "08:00", label: "pagi" }] } }], steps: [
        { id: "ask", kind: "checkin", text: "💊 Obat {{trigger.label}}?", answers: [{ id: "taken", label: "Udah", words: ["udah"], closes: true, reply: "Mantap {{trigger.label}} 👍" }, { id: "later", label: "Nanti", closes: false }], followUp: { everyMin: 15, max: 2 }, deadline: { kind: "after", minutes: 120 }, on: { taken: "yay" }, next: null },
        { id: "yay", kind: "message", text: "Tercatat." },
      ] } },
      { do: "sweep", at: "2026-11-11T08:00" },
      { do: "answer", answer: "nanti", at: "2026-11-11T08:05" },
      { do: "sweep", at: "2026-11-11T08:20" },
      { do: "callback", data: "fl:{run:0:0}:taken", at: "2026-11-11T08:22" },
      { do: "sweep", at: "2026-11-11T08:22" },
      { do: "answer", run: "{run:0:0}", answer: "later", at: "2026-11-11T08:30" },
    ],
  },
  {
    name: "check-in times out",
    steps: [
      { do: "create", def: { name: "Olahraga", triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "times", times: [{ time: "06:00", label: "pagi" }] } }], steps: [
        { id: "ask", kind: "checkin", text: "Udah olahraga?", followUp: { everyMin: 20, max: 2, text: "Pengingat ke-{{run.followUps}}" }, deadline: { kind: "after", minutes: 60 }, missedText: "Kelewat ya.", onMissed: "fallback", next: null },
        { id: "fallback", kind: "message", text: "Besok coba lagi." },
      ] } },
      { do: "create", def: { name: "Streak", triggers: [{ id: "miss", kind: "event", event: "checkin.missed", flow: "Olahraga", start: "count" }], steps: [{ id: "count", kind: "set", scope: "flow", values: { missed: "flow.missed + 1" } }] } },
      { do: "sweep", at: "2026-11-11T06:00" },
      { do: "sweep", at: "2026-11-11T06:20" },
      { do: "sweep", at: "2026-11-11T06:40" },
      { do: "sweep", at: "2026-11-11T07:00" },
    ],
  },
  {
    name: "branch",
    steps: [
      { do: "create", def: { name: "Cabang", steps: [
        { id: "pick", kind: "if", when: "trigger.args.mode == \"a\" and daysUntil(trigger.args.due) <= 3", then: "a", else: "b" },
        { id: "a", kind: "set", values: { n: "flow.n + 1", label: "{{trigger.args.mode | upper}}" }, scope: "flow" },
        { id: "say_a", kind: "message", text: "A ke-{{flow.n}} ({{flow.label}})", next: null },
        { id: "b", kind: "message", text: "B" },
      ] } },
      { do: "start", flow: 0, kind: "manual", key: "m1", payload: { args: { mode: "a", due: "2026-11-12" } } },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "start", flow: 0, kind: "manual", key: "m2", payload: { args: { mode: "a", due: "2026-11-30" } }, at: "2026-11-10T10:01" },
      { do: "sweep", at: "2026-11-10T10:01" },
    ],
  },
  {
    name: "item fan-out",
    tools: { "mcp-keuangan__list_bills": { returns: { items: [{ id: "pln", name: "Listrik", amount: 350000, due: "2026-11-12" }, { id: "net", name: "Internet", amount: 400000, due: "2026-11-25" }, { id: "bpjs", name: "BPJS", amount: 150000, due: "2026-11-11" }] } } },
    steps: [
      { do: "create", def: {
        name: "Tagihan",
        triggers: [{ id: "daily", kind: "schedule", start: "bills", source: { kind: "times", times: [{ time: "09:00", label: "Cek tagihan" }] } }],
        steps: [
          { id: "bills", kind: "tool", tool: "mcp-keuangan__list_bills", args: { status: "unpaid" } },
          { id: "per_bill", kind: "each", items: "steps.bills.data.items", where: "daysUntil(item.due) <= 3 and daysUntil(item.due) >= 0", key: "{{item.id}}:{{item.due}}", max: 20, body: "ask", next: null },
          { id: "ask", kind: "checkin", text: "Tagihan {{item.name}} Rp{{item.amount}} jatuh tempo {{item.due | date}}. Udah bayar?", answers: [{ id: "paid", label: "Udah", words: ["lunas"], closes: true }, { id: "later", label: "Belum", closes: false }], followUp: { everyMin: 240, max: 3 }, deadline: { kind: "after", minutes: 4320 }, on: { paid: "mark" }, next: null },
          { id: "mark", kind: "set", scope: "flow", values: { last_paid: "{{item.name}} {{now.date}}" } },
        ],
      } },
      { do: "sweep", at: "2026-11-11T09:00" },
      { do: "answer", run: "{run:0:1}", answer: "lunas", at: "2026-11-11T09:30" },
      { do: "sweep", at: "2026-11-11T09:30" },
      { do: "sweep", at: "2026-11-12T09:00" },
    ],
  },
  {
    name: "failing step with retries",
    tools: { "reminders__list_reminders": { fails: "boom\n  detail" }, "mcp-keuangan__list_bills": { sequence: [{ fails: "ECONNRESET" }, { returns: { items: [] } }] } },
    steps: [
      { do: "create", def: { name: "Rusak", steps: [{ id: "flaky", kind: "tool", tool: "mcp-keuangan__list_bills" }, { id: "t", kind: "tool", tool: "reminders__list_reminders", retry: { max: 1, backoffSec: 10 } }] } },
      { do: "create", def: { name: "Jaga", triggers: [{ id: "f", kind: "event", event: "run.failed", flow: "Rusak", start: "m" }], steps: [{ id: "m", kind: "message", text: "Rusak gagal: {{trigger.error}}" }] } },
      { do: "start", flow: 0, kind: "manual", key: "m1" },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "sweep", at: "2026-11-10T10:00:30" },
      { do: "sweep", at: "2026-11-10T10:00:40" },
    ],
  },
  {
    name: "tainted value refused in a message to a contact",
    steps: [
      { do: "contact", name: "Zaki", phone: "082200001001", at: "2026-11-10T09:00" },
      { do: "create", def: { name: "Kabar", steps: [
        { id: "safe", kind: "message", text: "Halo dari flow", contact: "Zaki" },
        { id: "leak", kind: "message", text: "Isi: {{trigger.payload}}", contact: "zaki", onError: "continue" },
        { id: "own", kind: "message", text: "Isi: {{trigger.payload}}" },
      ] } },
      { do: "decide", flow: 0, approved: true, at: "2026-11-10T09:59" },
      { do: "start", flow: 0, kind: "webhook", key: "w1", payload: { payload: "rahasia\nbaris" }, taint: ["trigger"] },
      { do: "sweep", at: "2026-11-10T10:00" },
    ],
  },
  {
    name: "dry run",
    tools: { "mcp-keuangan__list_bills": { returns: { items: [{ id: "a", name: "A", due: "2026-11-11" }, { id: "b", name: "B", due: "2026-11-12" }, { id: "c", name: "C", due: "2026-11-12" }, { id: "d", name: "D", due: "2026-11-13" }] } } },
    steps: [
      { do: "create", def: { name: "Coba", triggers: [{ id: "daily", kind: "schedule", start: "bills", source: { kind: "times", times: [{ time: "09:00", label: "Pagi" }] } }], steps: [
        { id: "bills", kind: "tool", tool: "mcp-keuangan__list_bills" },
        { id: "each", kind: "each", items: "steps.bills.data.items", body: "ask", next: "note" },
        { id: "note", kind: "tool", tool: "notes__save_note", args: { text: "{{trigger.label}} {{now.date}}" } },
        { id: "w", kind: "wait", minutes: 30 },
        { id: "p", kind: "prompt", prompt: "Ringkas", input: { x: "{{steps.bills.data}}" } },
        { id: "m", kind: "message", text: "{{steps.p.text}}", next: null },
        { id: "ask", kind: "checkin", text: "Bayar {{item.name}}?", deadline: { kind: "after", minutes: 60 }, next: null },
      ] } },
      { do: "dry", flow: 0, stubs: { p: { text: "ringkasan" }, ask: "not_yet", note: { saved: 1 } }, at: "2026-11-10T10:00" },
      { do: "dry", def: { name: "Draft", triggers: [{ id: "h", kind: "webhook", hookId: "hk_x", start: "m" }], steps: [{ id: "m", kind: "message", text: "Dari web: {{trigger.payload.text}}" }] }, trigger: { payload: { text: "halo\nsana" } }, at: "2026-11-10T10:05" },
    ],
  },
  {
    name: "pause and resume",
    steps: [
      { do: "create", def: { name: "Jam", triggers: [{ id: "main", kind: "schedule", start: "m", source: { kind: "times", times: [{ time: "12:00" }, { time: "18:00" }] } }], steps: [{ id: "m", kind: "message", text: "Jam {{trigger.time}}" }] } },
      { do: "pause", flow: 0, until: "2026-11-10T15:00", reason: "libur", at: "2026-11-10T10:00" },
      { do: "sweep", at: "2026-11-10T12:00" },
      { do: "sweep", at: "2026-11-10T15:00" },
      { do: "sweep", at: "2026-11-10T18:00" },
      { do: "pause", flow: 0, at: "2026-11-10T18:30" },
      { do: "sweep", at: "2026-11-11T12:00" },
      { do: "resume", flow: 0, at: "2026-11-11T13:00" },
      { do: "sweep", at: "2026-11-11T18:00" },
    ],
  },
  {
    name: "lease recovery after a crash",
    tools: { "reminders__list_reminders": { returns: { reminders: [] } } },
    steps: [
      { do: "create", def: { name: "Baca", steps: [{ id: "r", kind: "tool", tool: "reminders__list_reminders" }, { id: "n", kind: "set", values: { n: "len(steps.r.data.reminders)" } }, { id: "m", kind: "message", text: "{{vars.n}} pengingat" }] } },
      { do: "start", flow: 0, kind: "manual", key: "m1" },
      { do: "crash", run: "{run:0:0}", step: "r", kind: "tool", lease: "2026-11-10T10:02" },
      { do: "sweep", at: "2026-11-10T10:05" },
      { do: "sweep", at: "2026-11-10T10:13" },
    ],
  },
  {
    name: "schedule tool source",
    readSource: "prayer",
    steps: [
      { do: "create", def: { name: "Sholat", tz: "Asia/Makassar", triggers: [{ id: "main", kind: "schedule", start: "ask", source: { kind: "tool", connectorId: "mcp-jadwal-sholat", tool: "get_prayer_times", args: { city: "Banjarmasin", date: "{{date}}" }, items: [{ field: "times.dhuhr", label: "Zuhur" }, { field: "times.asr", label: "Asar" }] } }], steps: [
        { id: "ask", kind: "checkin", text: "Udah sholat {{trigger.label}} ({{trigger.time}})?", followUp: { everyMin: 15, max: 1 }, deadline: { kind: "next", plusMin: -10 }, next: null },
      ] } },
      { do: "sweep", at: "2026-11-10T10:00" },
      { do: "sweep", at: "2026-11-10T12:00" },
      { do: "sweep", at: "2026-11-10T12:15" },
      { do: "sweep", at: "2026-11-10T15:15" },
    ],
  },
]

const TABLES = ["flows", "flow_versions", "flow_triggers", "flow_runs", "flow_step_runs", "outbox", "events", "pending_approvals", "tool_calls", "outbound_messages", "hooks"]

function specRun(spec: Spec) {
  let i = 0
  return () => {
    let s = spec
    if (spec.sequence) s = spec.sequence[Math.min(i++, spec.sequence.length - 1)]!
    if (s.fails !== undefined) throw new Error(s.fails)
    return structuredClone(s.returns)
  }
}

async function trace(sc: Scenario) {
  const over: Record<string, () => unknown> = {}
  for (const [name, spec] of Object.entries(sc.tools ?? {})) over[name] = specRun(spec)
  const prayer = fakePrayerTool()
  const h = flowHarness({
    connectors: exampleConnectors(over),
    engine: sc.readSource === "prayer" ? { readSource: prayer } : {},
    prompt: (r) => {
      const p = sc.prompt ?? { status: "ok", text: "Semangat!" }
      if (p.tokens) h.db.run(sql`INSERT INTO runs (id, chat_id, trigger, status, tokens_in, tokens_out, tokens_cached, started_at) VALUES (${r.turnRunId}, ${h.chatId}, 'flow', 'ok', ${p.tokens[0]}, ${p.tokens[1]}, ${p.tokens[2]}, ${h.now()})`)
      return { status: p.status, text: p.text, runId: r.turnRunId, tainted: p.tainted }
    },
  })
  const flowIds: string[] = []
  const hookIds: string[] = []
  const contactIds: string[] = []
  const approvalIds = () => (rows(h.db, "pending_approvals") as { id: string }[]).map((r) => r.id)
  const resolve = (v: unknown): unknown => {
    if (typeof v === "string")
      return v.replace(/\{(chat|other|flow|version|run|approval|hook|contact)(?::(\d+))?(?::(\d+))?\}/g, (_m, k: string, a?: string, b?: string) => {
        const i = Number(a ?? 0)
        switch (k) {
          case "chat":
            return h.chatId
          case "other":
            return h.otherChatId
          case "flow":
            return flowIds[i]!
          case "version":
            return getFlow(h.db, flowIds[i]!)!.currentVersionId!
          case "run":
            return runsOf(h.db, flowIds[i]!)[Number(b ?? 0)]!.id
          case "approval":
            return approvalIds()[i]!
          case "hook":
            return hookIds[i]!
          case "contact":
            return contactIds[i]!
        }
        return _m
      })
    if (Array.isArray(v)) return v.map(resolve)
    if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, resolve(x)]))
    return v
  }
  const time = (s: unknown) => (typeof s === "string" ? at(s) : h.now())
  const snaps: unknown[] = []
  for (const raw of sc.steps) {
    const st = resolve(raw) as Step
    if (st.at && st.do !== "sweep") h.setNow(time(st.at))
    let result: unknown = null
    try {
      switch (st.do) {
        case "create": {
          const r = h.create(st.def)
          flowIds.push(r.flow.id)
          result = { needsApproval: r.needsApproval, changed: r.changed, writeCalls: r.writeCalls, diff: r.diff }
          break
        }
        case "update": {
          const r = updateFlow(h.db, flowIds[st.flow as number]!, { definition: st.def, patch: st.patch, createdBy: "test", validate: { resolveTool: resolverFrom(EXAMPLE_TOOLS), hookExists: () => true } }, h.now())
          result = { needsApproval: r.needsApproval, changed: r.changed, writeCalls: r.writeCalls, diff: r.diff, version: r.version.n }
          break
        }
        case "sweep":
          result = await h.run(time(st.at))
          break
        case "start": {
          const r = h.engine.startRun({ flowId: flowIds[st.flow as number]!, triggerKind: st.kind as string, runKey: st.key as string, payload: (st.payload ?? {}) as Record<string, unknown>, taint: st.taint as string[] | undefined, now: h.now() })
          result = { run: !!r.run, skipped: r.skipped ?? null }
          break
        }
        case "chat":
          result = await flowActions.fromChat(h.db, { text: st.text as string, chatId: (st.chat as string) ?? h.chatId, messageId: st.messageId as string, fromOwner: st.fromOwner as boolean }, h.now())
          break
        case "answer": {
          const r = answerFlowCheckin(h.db, { chatId: h.chatId, runId: st.run as string | undefined, answer: st.answer as string, text: st.text as string | undefined }, h.now())
          result = { reply: r.reply, closed: r.closed, early: r.early ?? false }
          break
        }
        case "callback":
          result = await flowActions.fromCallback(h.db, st.data as string, h.chatId, h.now())
          break
        case "text":
          result = await flowActions.fromText(h.db, st.text as string, st.replyTo as string | undefined, h.chatId, h.now())
          break
        case "decide":
          result = decideVersion(h.db, currentVersion(h.db, getFlow(h.db, flowIds[st.flow as number]!)!).id, st.approved as boolean, "test", h.now()).reply
          break
        case "approve": {
          const id = approvalIds()[st.approval as number]!
          const runId = (h.db.all(sql`SELECT run_id AS r FROM pending_approvals WHERE id = ${id}`) as { r: string }[])[0]!.r
          result = decide(h.db, id, st.approved as boolean, h.now())
          wakeFlowRun(h.db, runId, h.now())
          break
        }
        case "expire": {
          const ids = expireDue(h.db, h.now())
          for (const id of ids) wakeFlowRun(h.db, id, h.now())
          result = ids.length
          break
        }
        case "pause":
          setFlowPaused(h.db, flowIds[st.flow as number]!, true, st.until ? at(st.until as string) : null, h.now(), st.reason as string | undefined)
          break
        case "resume":
          setFlowPaused(h.db, flowIds[st.flow as number]!, false, null, h.now())
          break
        case "hook": {
          const r = createHook(h.db, { name: st.name as string, mode: st.mode as "forward", chatId: h.chatId }, KEY, h.now())
          hookIds.push(r.hook.id)
          break
        }
        case "ingest": {
          const hook = h.db.select().from(hooks).where(eq(hooks.id, hookIds[st.hook as number]!)).get()!
          result = ingestFlowHook(h.db, hook, st.body as string, st.headers as Record<string, string>, h.now())
          break
        }
        case "contact": {
          const c = addContact(h.db, { agentId: getDefaultAgent(h.db).id, name: st.name as string, phone: st.phone as string }, h.now())
          contactIds.push(c.id)
          break
        }
        case "dry": {
          const r = await dryRunFlow(h.db, h.engine, {
            chatId: h.chatId,
            flow: st.flow !== undefined ? getFlow(h.db, flowIds[st.flow as number]!) : undefined,
            definition: st.def ? validateDefinition(st.def, { resolveTool: resolverFrom(EXAMPLE_TOOLS) }).definition! : undefined,
            trigger: st.trigger as Record<string, unknown> | undefined,
            stubs: st.stubs as Record<string, unknown> | undefined,
            tz: WITA,
            now: h.now(),
          })
          result = r
          break
        }
        case "crash": {
          h.db.update(flowRuns).set({ status: "running", leaseUntil: at(st.lease as string) }).where(eq(flowRuns.id, st.run as string)).run()
          h.db.insert(flowStepRuns).values({ id: "fls_crashcrashcrashcrash", runId: st.run as string, seq: 1, stepId: st.step as string, kind: st.kind as string, status: "running", startedAt: h.now() }).run()
          break
        }
        default:
          throw new Error(`unknown step ${st.do}`)
      }
    } catch (e) {
      result = { error: (e as Error).message }
    }
    const tables: Record<string, unknown> = {}
    for (const t of TABLES) tables[t] = t === "events" ? rows(h.db, t).map(({ id: _id, ...e }) => e) : rows(h.db, t)
    snaps.push({ step: JSON.stringify(raw), result, tables })
  }
  return normalize({ name: sc.name, tools: JSON.stringify(sc.tools ?? {}), prompt: sc.prompt ?? null, readSource: sc.readSource ?? null, steps: snaps })
}

// --- main ------------------------------------------------------------------------------

const out = {
  now: NOW,
  exprScope: SCOPE,
  templateScope: TSCOPE,
  taintInputs: { keys: TAINT_KEYS, values: TAINT_VALUES, args: TAINT_ARGS },
  definitions: normalize(definitions()),
  exprs: exprs(),
  templates: templates(),
  taint: taint(),
  schedules: schedules(),
  traces: [] as unknown[],
}
for (const sc of SCENARIOS) {
  try {
    out.traces.push(await trace(sc))
  } catch (e) {
    console.error(`scenario ${sc.name} failed`)
    throw e
  }
}
const file = path.resolve(import.meta.dirname, "../flows.json")
writeFileSync(file, `${JSON.stringify(out, null, 1)}\n`)
console.log(`wrote ${file}`)
