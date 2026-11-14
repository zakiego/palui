// The "calls", "tests" and "helpers" sections of connectors.json (see
// connectors.ts). Scripted tool calls run on a copy of testdata/seeded.db at
// the seed's clock: every input goes through the tool's schema first (as a
// turn does), then execute; the result is JSON.stringify'd with ids
// normalized. An input string "{{N.path}}" is the raw value at path in step
// N's result (0-based), so a later step can name what an earlier one made.
import { copyFileSync, mkdtempSync, readFileSync, readdirSync } from "node:fs"
import { createRequire } from "node:module"
import { tmpdir } from "node:os"
import path from "node:path"
import { openDb, type DB } from "../../../../packages/db/src/index.ts"
import type { PaluiTool, TurnInfo, AnyRuntime } from "../../../../packages/core/src/connectors/define.ts"
import { attachmentTools } from "../../../../packages/connectors/src/attachments/runtime.ts"
import { chatTools, clip, parseTime } from "../../../../packages/connectors/src/chats/runtime.ts"
import { flowTools, recapTool } from "../../../../packages/connectors/src/flows/runtime.ts"
import { jobTools } from "../../../../packages/connectors/src/jobs/runtime.ts"
import { notesTools } from "../../../../packages/connectors/src/notes/runtime.ts"
import { outboundTools } from "../../../../packages/connectors/src/outbound/runtime.ts"
import { reminderTools } from "../../../../packages/connectors/src/reminders/runtime.ts"
import { remoteMcpTools } from "../../../../packages/connectors/src/remote-mcp/runtime.ts"
import { settingsTools } from "../../../../packages/connectors/src/settings/runtime.ts"
import { extractReadable, fetchPageTool } from "../../../../packages/connectors/src/webfetch/runtime.ts"
import { normalizeSourceId, profileText } from "../../../../packages/connectors/src/recall/api.ts"
import { resolveContainer, shouldRecall } from "../../../../packages/connectors/src/recall/runtime.ts"
import { presentEvent, resolveRange } from "../../../../packages/connectors/src/gcal/calendar.ts"
import { environmentIdOf, finishedText, threadIdOf, waitingText } from "../../../../packages/connectors/src/t3code/runtime.ts"
import { codeFingerprint, matchProject, parseModel } from "../../../../packages/connectors/src/t3code/client.ts"
import attachmentsRuntime from "../../../../packages/connectors/src/attachments/runtime.ts"
import chatsRuntime from "../../../../packages/connectors/src/chats/runtime.ts"
import flowsRuntime from "../../../../packages/connectors/src/flows/runtime.ts"
import hooksRuntime from "../../../../packages/connectors/src/hooks/runtime.ts"
import jobsRuntime from "../../../../packages/connectors/src/jobs/runtime.ts"
import notesRuntime from "../../../../packages/connectors/src/notes/runtime.ts"
import outboundRuntime from "../../../../packages/connectors/src/outbound/runtime.ts"
import remindersRuntime from "../../../../packages/connectors/src/reminders/runtime.ts"
import remoteMcpRuntime from "../../../../packages/connectors/src/remote-mcp/runtime.ts"
import settingsRuntime from "../../../../packages/connectors/src/settings/runtime.ts"
import webfetchRuntime from "../../../../packages/connectors/src/webfetch/runtime.ts"
import { DEV_MASTER_KEY } from "../../../../packages/core/src/config.ts"

const connRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/connectors/package.json"))
const { asSchema } = connRequire("ai") as typeof import("ai")

const testdata = path.resolve(import.meta.dirname, "..")
const seeded = JSON.parse(readFileSync(path.join(testdata, "seeded.json"), "utf8")) as { now: number }
const NOW = seeded.now
const WITA = "Asia/Makassar"
const APP = "https://palui.example.com"

// Stable rows of seeded.db.
export const SEED = {
  webChat: "chat_0mwfavogqfl8g9qpmbj",
  groupChat: "chat_0mwfavogrpdfz4jjerg",
  attachmentChat: "chat_0mwfavoh5u54sluocmm",
  attachment: "att_0mwfavojm46xavsrj14",
  otherAttachment: "att_0mwfavojtvoqisx8myi",
  flow: "flw_0mwfavoihdq0kcg0btx",
  palui: "agt_0mwfavohdfkyeb1kl5c",
  otherAgent: "agt_0mwfavoh23qqz2cayx0",
}

function normText(s: string): string {
  const seen = new Map<string, string>()
  return s
    .replace(/\/api\/share\/[A-Za-z0-9_-]{32}/g, "/api/share/<token>")
    .replace(/\b([a-z]{2,6})_[0-9a-z]{19}\b/g, (m, p: string) => {
      if (!seen.has(m)) seen.set(m, `${p}#${seen.size + 1}`)
      return seen.get(m)!
    })
}

function freshSeed(): DB {
  const dir = mkdtempSync(path.join(tmpdir(), "palui-calls-"))
  const file = path.join(dir, "palui.db")
  copyFileSync(path.join(testdata, "seeded.db"), file)
  return openDb(file)
}

function pick(v: unknown, p: string): unknown {
  let cur = v
  for (const k of p.split(".")) cur = cur == null ? undefined : (cur as Record<string, unknown>)[k]
  return cur
}

function fill(input: unknown, results: unknown[]): unknown {
  if (typeof input === "string") {
    const m = /^\{\{(\d+)\.(.+)\}\}$/.exec(input)
    return m ? pick(results[Number(m[1])], m[2]!) : input
  }
  if (Array.isArray(input)) return input.map((x) => fill(x, results))
  if (input && typeof input === "object") return Object.fromEntries(Object.entries(input).map(([k, v]) => [k, fill(v, results)]))
  return input
}

type Step = { tool: string; input: unknown; kind?: "call" | "preview" | "needsApproval" }

async function runSteps(tools: () => Record<string, PaluiTool>, steps: Step[]) {
  const results: unknown[] = []
  const out = []
  for (const s of steps) {
    const t = tools()[s.tool]
    if (!t) throw new Error(`no tool ${s.tool}`)
    const input = fill(s.input, results)
    let result: unknown
    if (s.kind === "preview") result = t.preview!(input)
    else if (s.kind === "needsApproval") result = t.needsApproval!(input)
    else {
      // A schema-valid input runs as a turn passes it (zod's output); one the
      // schema refuses runs as given, the way a flow step calls execute.
      const v = await asSchema(t.tool.inputSchema).validate!(input)
      try {
        result = await t.tool.execute!((v.success ? v.value : input) as never, { toolCallId: "tc_1", messages: [] } as never)
      } catch (e) {
        result = { thrown: (e as Error).message }
      }
    }
    results.push(result)
    out.push({ tool: s.tool, kind: s.kind ?? "call", input: s.input, result: result === undefined ? "undefined" : JSON.stringify(result) })
  }
  return out
}

function turn(chatId: string, extra: Partial<TurnInfo> = {}): TurnInfo {
  return { runId: "run_calls", chatId, channel: "web", trigger: "message", tz: WITA, now: NOW, ...extra }
}

type Scenario = { name: string; connector: string; turn: Record<string, unknown>; steps: Step[] }

const ownerTurn = { origin: "message", mode: "owner", agentId: SEED.palui, ownerText: "tolong kabarin" }

export const SCENARIOS: Scenario[] = [
  {
    name: "reminders",
    connector: "reminders",
    turn: { chatId: SEED.groupChat },
    steps: [
      { tool: "list_reminders", input: {} },
      { tool: "list_reminders", input: { filter: "recurring" } },
      { tool: "list_reminders", input: { filter: "done", limit: 5 } },
      { tool: "set_reminder", input: { text: "bayar listrik", localDateTime: "2026-11-15T09:00" } },
      { tool: "set_reminder", input: { text: "rapat", localDateTime: "2026-11-16T08:00", rrule: "FREQ=WEEKLY;BYDAY=MO", tz: "Asia/Jakarta" } },
      { tool: "set_reminder", input: { text: "x", localDateTime: "2026-11-01T09:00" } },
      { tool: "set_reminder", input: { text: "x", localDateTime: "2026-11-15T09:00", tz: "Mars/Base" } },
      { tool: "update_reminder", input: { id: "{{3.id}}", localDateTime: "2026-11-16T07:30", rrule: "FREQ=WEEKLY;BYDAY=MO" } },
      { tool: "update_reminder", input: { id: "{{3.id}}", rrule: null, text: "bayar listrik PLN" } },
      { tool: "snooze_reminder", input: { id: "{{3.id}}", preset: "1h" } },
      { tool: "snooze_reminder", input: { id: "{{3.id}}", localDateTime: "2026-11-14T15:00" } },
      { tool: "snooze_reminder", input: { id: "{{3.id}}" } },
      { tool: "cancel_reminder", input: { id: "{{3.id}}" } },
      { tool: "cancel_reminder", input: { id: "{{3.id}}" } },
      { tool: "cancel_reminder", input: { id: "rem_missing" } },
      { tool: "list_reminders", input: { filter: "done", limit: 2 } },
    ],
  },
  { name: "jobs", connector: "jobs", turn: { chatId: SEED.webChat }, steps: [{ tool: "list_jobs", input: {} }, { tool: "list_jobs", input: { includePaused: false } }] },
  {
    name: "notes",
    connector: "notes",
    turn: { chatId: SEED.webChat },
    steps: [
      { tool: "list_instructions", input: {} },
      { tool: "get_preferences", input: {} },
      { tool: "save_instruction", input: { title: "Jadwal sholat", body: "Pakai server jadwal-sholat." } },
      { tool: "save_instruction", input: { id: "{{2.instruction.id}}", title: "Jadwal sholat", body: "Pakai API v1." } },
      { tool: "save_instruction", input: { id: "ins_missing", title: "x", body: "y" } },
      { tool: "delete_instruction", input: { id: "{{2.instruction.id}}" } },
      { tool: "delete_instruction", input: { id: "{{2.instruction.id}}" } },
      { tool: "save_preference", input: { key: "Default City", value: "Banjarmasin" } },
      { tool: "save_preference", input: { key: "bad key!", value: "x" } },
      { tool: "delete_preference", input: { key: "default_city" } },
      { tool: "delete_preference", input: { key: "default_city" } },
    ],
  },
  { name: "notes-agent", connector: "notes", turn: { chatId: SEED.webChat, agentId: SEED.otherAgent }, steps: [{ tool: "list_instructions", input: {} }] },
  {
    name: "attachments",
    connector: "attachments",
    turn: { chatId: SEED.attachmentChat },
    steps: [
      { tool: "list_attachments", input: {} },
      { tool: "list_attachments", input: { query: "zzz" } },
      { tool: "get_attachment", input: { id: SEED.attachment } },
      { tool: "get_attachment", input: { id: SEED.otherAttachment } },
      { tool: "share_attachment", input: { id: SEED.attachment, visibility: "public", expires: "7d" } },
      { tool: "unshare_attachment", input: { id: SEED.attachment } },
      { tool: "unshare_attachment", input: { id: SEED.attachment } },
      { tool: "save_attachment", input: { title: "x" } },
      { tool: "save_attachment", input: { title: "x", earlier: true } },
      { tool: "delete_attachment", input: { id: SEED.attachment } },
      { tool: "delete_attachment", input: { id: SEED.attachment } },
    ],
  },
  {
    name: "chats",
    connector: "chats",
    turn: { chatId: SEED.webChat, agentId: SEED.palui },
    steps: [
      { tool: "search_chats", input: {} },
      { tool: "search_chats", input: { query: "a", limit: 3 } },
      { tool: "search_chats", input: { kind: "group" } },
      { tool: "search_chats", input: { query: "tidak-ada-chat-ini" } },
      { tool: "read_chat", input: { chat: SEED.webChat, limit: 5 } },
      { tool: "read_chat", input: { chat: SEED.groupChat } },
      { tool: "read_chat", input: { chat: "nope" } },
      { tool: "read_chat", input: { chat: SEED.webChat, before: "kemarin" } },
      { tool: "read_chat", input: { chat: SEED.webChat, since: "2026-11-13T00:00", query: "a" } },
    ],
  },
  {
    name: "settings",
    connector: "settings",
    turn: { chatId: SEED.webChat },
    steps: [
      { tool: "get_settings", input: {} },
      { tool: "update_settings", input: { timezone: "Asia/Jakarta", budgets: { dailyTokenCap: 3000000 } } },
      { tool: "update_settings", input: { budgets: { maxSteps: 500 } } },
      { tool: "update_settings", input: { modelRoles: { fast: [{ connectorId: "nope", model: "m" }] } } },
      { tool: "update_settings", input: { modelRoles: { fast: [{ connectorId: "cliproxy", model: " claude-haiku-4-5 ", effort: "low" }] } } },
      { tool: "update_settings", input: { timezone: "Asia/Jakarta" } },
    ],
  },
  {
    name: "outbound",
    connector: "outbound",
    turn: { chatId: SEED.webChat, ...ownerTurn },
    steps: [
      { tool: "list_contacts", input: {} },
      { tool: "list_outbound_messages", input: {} },
      { tool: "list_outbound_messages", input: { filter: "recent", limit: 5 } },
      { tool: "send_message", input: { to: "Nobody", text: "x" } },
      { tool: "send_message", input: { to: "0822 1111 2222", text: "x" } },
      { tool: "send_message", input: { to: "0822 1111 2222", name: "Budi", text: "halo" } },
      { tool: "send_message", input: { to: "J", text: "halo, ini asisten", localDateTime: "2026-11-14T20:00" }, kind: "preview" },
      { tool: "send_message", input: { to: "J", text: "halo", signature: false }, kind: "preview" },
      { tool: "send_message", input: { to: "0822 1111 2222", name: "Budi", text: "halo" }, kind: "preview" },
      { tool: "send_message", input: { to: "J", text: "halo" }, kind: "needsApproval" },
      { tool: "send_message", input: { to: "J", text: "halo, ini asisten", localDateTime: "2026-11-14T20:00" } },
      { tool: "send_message", input: { to: "J", text: "tiap jumat", localDateTime: "2026-11-20T11:00", rrule: "FREQ=WEEKLY;BYDAY=FR" } },
      { tool: "send_message", input: { to: "J", text: "halo", rrule: "FREQ=DAILY" } },
      { tool: "cancel_message", input: { id: "{{10.id}}" } },
      { tool: "cancel_message", input: { id: "obm_missing" } },
      { tool: "add_contact", input: { name: "Ana", phone: "+62 822-0000-1002", note: "istri" }, kind: "preview" },
      { tool: "add_contact", input: { name: "Ana", phone: "+62 822-0000-1002" }, kind: "needsApproval" },
      { tool: "add_contact", input: { name: "Ana", phone: "+62 822-0000-1002" } },
      { tool: "add_contact", input: { name: "Ana", phone: "12345" } },
    ],
  },
  {
    name: "flows",
    connector: "flows",
    turn: { chatId: SEED.groupChat },
    steps: [
      { tool: "list_flows", input: {} },
      { tool: "list_flows", input: { filter: "paused" } },
      { tool: "get_flow", input: { id: SEED.flow } },
      { tool: "get_flow", input: { id: SEED.flow, version: 99 } },
      { tool: "flow_history", input: { days: 30 } },
      { tool: "check_flow", input: { definition: { name: "x" } } },
      { tool: "check_flow", input: {} },
      {
        tool: "create_flow",
        input: { definition: { name: "Golden", triggers: [{ id: "main", kind: "schedule", start: "send", source: { kind: "times", times: [{ time: "07:00", label: "Pagi" }] } }], steps: [{ id: "send", kind: "message", text: "Pagi! {{now.date}}" }] } },
      },
      { tool: "test_flow", input: { id: "{{7.id}}" } },
      { tool: "update_flow", input: { id: "golden", patch: { steps: { send: { text: "Selamat pagi" } } }, note: "lebih ramah" } },
      { tool: "pause_flow", input: { id: "golden", paused: true, until: "2026-11-20T08:00" } },
      { tool: "run_flow", input: { id: "golden" } },
      { tool: "pause_flow", input: { id: "golden", paused: false } },
      { tool: "explain_flow", input: { id: "golden", at: "2026-11-14T07:00" } },
      { tool: "answer_checkin", input: { answer: "udah" } },
      { tool: "get_run", input: { runId: "flr_missing" } },
      { tool: "cancel_run", input: { runId: "flr_missing" } },
      { tool: "recap", input: { flow: "golden", days: 7 } },
      { tool: "recap", input: {} },
      { tool: "delete_flow", input: { id: "golden" } },
      { tool: "delete_flow", input: { id: "golden" } },
    ],
  },
  {
    name: "remote-mcp",
    connector: "remote-mcp",
    turn: { chatId: SEED.webChat },
    steps: [
      { tool: "list_mcp_servers", input: {} },
      { tool: "remove_mcp_server", input: { name: "golden-mcp" } },
      { tool: "remove_mcp_server", input: { name: "nope" } },
      { tool: "add_mcp_server", input: { name: "golden-mcp", url: "https://x.example.com/mcp" } },
      { tool: "add_mcp_server", input: { name: "plain", url: "http://x.example.com/mcp" } },
      { tool: "add_mcp_server", input: { name: "local", url: "https://127.0.0.1/mcp" } },
    ],
  },
  { name: "webfetch", connector: "webfetch", turn: { chatId: SEED.webChat }, steps: [{ tool: "fetch_page", input: { url: "http://127.0.0.1:4891/control" } }, { tool: "fetch_page", input: { url: "ftp://example.com/x" } }] },
]

function toolsFor(db: DB, connector: string, t: TurnInfo): Record<string, PaluiTool> {
  const clock = () => NOW
  switch (connector) {
    case "reminders":
      return reminderTools(db, t, clock)
    case "jobs":
      return jobTools(db, clock)
    case "notes":
      return notesTools(db, clock, t.agentId)
    case "attachments":
      return attachmentTools(db, t, { clock, key: () => DEV_MASTER_KEY, appUrl: () => APP })
    case "chats":
      return chatTools(db, t)
    case "settings":
      return settingsTools(db, clock, t.agentId)
    case "outbound":
      return outboundTools(db, t, clock)
    case "flows":
      return { ...flowTools(db, t, { clock }), recap: recapTool(db, t, clock) }
    case "remote-mcp":
      // DNS answers with a private address, so nothing connects anywhere.
      return remoteMcpTools({ db, masterKey: DEV_MASTER_KEY, clock, resolver: async () => [{ address: "10.0.0.5", family: 4 }] })
    case "webfetch":
      return { fetch_page: { tool: fetchPageTool(), class: "read" } }
  }
  throw new Error(connector)
}

const RUNTIMES: Record<string, AnyRuntime> = {
  attachments: attachmentsRuntime,
  chats: chatsRuntime,
  flows: flowsRuntime,
  hooks: hooksRuntime,
  jobs: jobsRuntime,
  notes: notesRuntime,
  outbound: outboundRuntime,
  reminders: remindersRuntime,
  "remote-mcp": remoteMcpRuntime,
  settings: settingsRuntime,
  webfetch: webfetchRuntime,
}

function ctxFor(db: DB, id: string) {
  return {
    id,
    type: id,
    config: {},
    secrets: {},
    trust: "own",
    db,
    log: { info() {}, warn() {}, error() {}, debug() {}, child() { return this } },
    dataDir: "/tmp",
    signal: new AbortController().signal,
    services: { inbound: async () => {}, claimOwner: async () => false, isOwner: () => false },
    setStatus() {},
    event() {},
    patchConfig() {},
  } as never
}

// --- webfetch pages ------------------------------------------------------------------

function pages() {
  const dir = path.join(testdata, "webfetch")
  const out: Record<string, { title: string; text: string }> = {}
  for (const f of readdirSync(dir).filter((x) => x.endsWith(".html")).sort()) out[f] = extractReadable(readFileSync(path.join(dir, f), "utf8"))
  return out
}

// --- helpers ---------------------------------------------------------------------------

function attempt<T>(fn: () => T): T | { error: string } {
  try {
    return fn()
  } catch (e) {
    return { error: (e as Error).message }
  }
}

const T3_PROJECTS = [
  { id: "p1", title: "palui", workspaceRoot: "/Users/z/Code/palui" },
  { id: "p2", title: "vorneo", workspaceRoot: "/Users/z/Code/vorneo" },
  { id: "p4", title: "palui-site", workspaceRoot: "/Users/z/Code/palui-site/" },
  { id: "p6", title: "Kopi Banjar", workspaceRoot: "/Users/z/Code/kopi" },
]

function helpers() {
  const now = Date.parse("2026-11-10T02:00:00Z")
  const ranges: [string | undefined, string | undefined, string, number][] = [
    ["today", undefined, WITA, now],
    [undefined, undefined, WITA, now],
    ["Hari ini", undefined, WITA, now],
    ["besok", undefined, "America/New_York", Date.parse("2026-11-01T03:00:00Z")],
    ["minggu ini", undefined, "America/New_York", Date.parse("2026-11-01T03:00:00Z")],
    ["2026-11-12", "2026-11-13", WITA, now],
    ["2026-11-12T08:00", "2026-11-12T17:30", WITA, now],
    ["2026-11-12T08:00:00Z", undefined, WITA, now],
    ["2026-11-12T08:00:00+07:00", "2026-11-14", WITA, now],
    ["2026-01-01", "2026-12-31", WITA, now],
    ["2026-03-07", "2026-03-09", "America/New_York", now],
    ["kapan-kapan", undefined, WITA, now],
    ["2026-11-12", "nanti", WITA, now],
    ["2026-11-12", "2026-11-01", WITA, now],
    ["2026-02-30", undefined, WITA, now],
  ]
  const events = [
    { id: "1", calendarId: "primary", title: "Call", start: "2026-11-10T01:00:00Z", end: "2026-11-10T02:00:00Z", allDay: false },
    { id: "2", calendarId: "primary", title: "Late", start: "2026-11-10T15:30:00Z", end: "2026-11-10T16:30:00Z", allDay: false, location: "Rumah" },
    { id: "3", calendarId: "kerja@example.com", title: "Libur", start: "2026-11-10", end: "2026-11-11", allDay: true },
    { id: "4", calendarId: "primary", title: "Cuti", start: "2026-11-10", end: "2026-11-13", allDay: true, location: "Bali" },
    { id: "5", calendarId: "primary", title: "Offset", start: "2026-11-10T09:00:00+08:00", end: "2026-11-10T09:15:30+08:00", allDay: false },
  ]
  const profiles = [
    { container: "c", static: "", dynamic: "", updatedAt: null, staticMemories: [], dynamicMemories: [] },
    { container: "c", static: "  Lives in Banjarbaru. ", dynamic: "Building Palui.", updatedAt: 1, staticMemories: [], dynamicMemories: [], searchResults: [{ type: "m", id: "1", text: " kopi " }, { type: "m", id: "2", text: "  " }] },
    { container: "c", static: "", dynamic: "", updatedAt: null, staticMemories: Array.from({ length: 12 }, (_, i) => ({ id: `${i}`, fact: `Fact ${i}` })), dynamicMemories: [{ id: "d", fact: "Busy" }] },
    { container: "c", static: "x".repeat(5000), dynamic: "", updatedAt: null, staticMemories: [], dynamicMemories: [] },
  ]
  const scope = { container: "agent-chef", read: ["kb-menu"], locked: true }
  const watch = { threadId: "t", runId: "r", chatId: "c", title: "Fix bug", link: "https://app.t3.codes/threads/e/t", since: 0 }
  return {
    gcalRanges: ranges.map(([from, to, tz, n]) => {
      const r = attempt(() => resolveRange(from, to, tz, n))
      return { from: from ?? null, to: to ?? null, tz, now: n, out: "error" in r ? r : { min: r.timeMin.toMillis(), max: r.timeMax.toMillis(), fromIso: r.timeMin.toISO({ suppressMilliseconds: true }), toIso: r.timeMax.toISO({ suppressMilliseconds: true }) } }
    }),
    gcalEvents: events.flatMap((e) => [WITA, "America/New_York"].map((tz) => ({ event: e, tz, out: JSON.stringify(presentEvent(e, tz)) }))),
    recallProfiles: profiles.flatMap((p) => [undefined, "Zaki"].map((about) => ({ profile: p, about: about ?? null, out: profileText(p as never, about) }))),
    recallShould: ["/new", "apa kabar", "ingat nggak aku suka apa", "  satu  dua tiga  empat ", "a\tb\nc d"].map((q) => ({ q, out: shouldRecall(q) })),
    recallSourceIds: ["ses_1:0-40", ` ${"y".repeat(200)} `, "x".repeat(201)].map((id) => ({ id, out: normalizeSourceId(id) })),
    recallContainers: [
      [undefined, "read", scope],
      ["kb-menu", "read", scope],
      ["kb-menu", "write", scope],
      ["zaki", "read", scope],
      ["x".repeat(80), "write", scope],
      ["other", "read", { container: "", read: [], locked: true }],
      ["anything", "write", { container: "zaki", read: [], locked: false }],
    ].map(([asked, cls, s]) => ({ asked: asked ?? null, cls, scope: s, out: attempt(() => resolveContainer(asked, cls as never, s as never)) })),
    t3Finished: [
      ["completed", "All good."],
      ["failed", null],
      ["completed", "x".repeat(2000)],
      ["completed", `  ${"kata ".repeat(400)}  `],
    ].map(([status, reply]) => ({ status, reply, out: finishedText(watch, status as string, reply as string | null) })),
    t3Waiting: [waitingText(watch), waitingText({ ...watch, link: undefined })],
    t3ThreadIds: ["https://app.t3.codes/threads/8b457a52-5eaa/b3b33950-a26e", " b3b33950 ", "https://app.t3.codes/threads/8b/mcp%3Alaunch%3Aa5/", "mcp:launch:a5", "https://x.dev/projects/p", "https://app.t3.codes/threads/e/%E0%A4%A"].map((x) => ({ in: x, out: threadIdOf(x) })),
    t3Environments: ["https://app.t3.codes/threads/8b457a52-1111/mcp%3Alaunch%3Aabc", "https://app.t3.codes/projects/x", "not a url", "https://app.t3.codes/threads/only"].map((x) => ({ in: x, out: environmentIdOf(x) ?? null })),
    t3Models: ["claudeAgent/claude-opus-5-5", "opus", " codex/gpt-5 ", "a/b c"].map((x) => ({ in: x, out: parseModel(x) })),
    t3Match: ["palui", "Palui", "pal", "p2", "kopi", "/users/z/code/vorneo", "nope", ""].map((q) => ({ q, out: matchProject(T3_PROJECTS as never, q) })),
    t3Fingerprints: ["PAIR-CODE-1234", "  PAIR-CODE-1234 "].map((c) => ({ in: c, out: codeFingerprint(c) })),
    chatTimes: ["2026-11-10T10:00", "2026-11-10", "2026-11-10T02:00:00Z", "2026-11-10T09:00:00+07:00", "2026-11-10T09:00:00.5+0700", "nanti", "2026-02-30"].map((x) => ({ in: x, out: parseTime(x, WITA) })),
    chatClips: [["abc", 5], ["x".repeat(10), 4], ["kata  ".repeat(5), 9], ["😀😀😀", 3]].map(([s, n]) => ({ in: s, max: n, out: clip(s as string, n as number) })),
    webfetchPages: pages(),
  }
}

export async function calls() {
  const out = []
  for (const s of SCENARIOS) {
    const db = freshSeed()
    const t = turn(s.turn.chatId as string, { ...(s.turn as Partial<TurnInfo>), tainted: () => false })
    const steps = await runSteps(() => toolsFor(db, s.connector, t), s.steps)
    out.push({ name: s.name, connector: s.connector, turn: s.turn, steps: steps.map((x) => ({ ...x, result: normText(x.result) })) })
  }
  const db = freshSeed()
  const tests: Record<string, unknown> = {}
  for (const [id, rt] of Object.entries(RUNTIMES)) {
    const ctx = ctxFor(db, id)
    tests[id] = await rt.test(ctx)
    if (id === "hooks") tests["hooks.live"] = (await rt.start(ctx)).live!()
  }
  return { calls: out, tests, helpers: helpers() }
}
