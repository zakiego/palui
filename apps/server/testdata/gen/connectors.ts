// Exports M6 connector goldens from the real TS code into
// apps/server/testdata/connectors.json:
//   manifests  every connector manifest in catalog order (channels too, for
//              M7): the plain fields, the config keys and parse({}) result,
//              and the secret fields with whether each may be missing
//   catalogs   every tool of every built-in, model, tool and memory connector
//              for a few turns: name, title, description, class, untrusted,
//              guestSafe, flowApproval, whether needsApproval / preview are
//              set, and the input JSON schema as asSchema(...).jsonSchema
//              gives it to the provider
//   inputs     what streamText makes of a tool call's input: the parsed
//              input (defaults filled) for good inputs, the tool-error text
//              the model sees for bad ones
//   calls      scripted tool calls on a copy of testdata/seeded.db and on
//              fresh databases, with every result as JSON text
//   helpers    pure helpers (gcal ranges and events, t3code texts and
//              matching, recall profile text, webfetch extraction)
//
// Ids are normalized in the result text: every newId() id becomes <prefix>#N
// in order of first appearance, share tokens become <token>.
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/connectors.ts
import { copyFileSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs"
import { createRequire } from "node:module"
import { tmpdir } from "node:os"
import path from "node:path"
import { memoryDb, openDb, newId, messages as messagesTable, type DB } from "../../../../packages/db/src/index.ts"
import { DEV_MASTER_KEY, setConfig } from "../../../../packages/core/src/config.ts"
import { ensureChat, ensureWebChat } from "../../../../packages/core/src/chats.ts"
import { createAgent, getDefaultAgent } from "../../../../packages/core/src/agents.ts"
import { upsertConnector } from "../../../../packages/core/src/connectors/store.ts"
import { setSetting } from "../../../../packages/core/src/settings.ts"
import { PALUI_VERSION } from "../../../../packages/core/src/version.ts"
import { addContact, updateContact } from "../../../../packages/core/src/outbound/service.ts"
import { scriptedModel, callTool, say } from "../../../../packages/core/src/chat/testing.ts"
import { BRIEFING } from "../../../../packages/core/src/flows/test-helpers.ts"
import type { PaluiTool, TurnInfo } from "../../../../packages/core/src/connectors/define.ts"
import { manifestList } from "../../../../packages/connectors/src/manifests.ts"
import { attachmentTools } from "../../../../packages/connectors/src/attachments/runtime.ts"
import { chatTools, offersChatTools } from "../../../../packages/connectors/src/chats/runtime.ts"
import { flowTools, recapTool } from "../../../../packages/connectors/src/flows/runtime.ts"
import hooksRuntime from "../../../../packages/connectors/src/hooks/runtime.ts"
import jobsRuntime, { jobTools } from "../../../../packages/connectors/src/jobs/runtime.ts"
import notesRuntime, { notesTools } from "../../../../packages/connectors/src/notes/runtime.ts"
import outboundRuntime, { outboundTools } from "../../../../packages/connectors/src/outbound/runtime.ts"
import remindersRuntime, { reminderTools } from "../../../../packages/connectors/src/reminders/runtime.ts"
import remoteMcpRuntime, { remoteMcpTools } from "../../../../packages/connectors/src/remote-mcp/runtime.ts"
import settingsRuntime, { settingsTools } from "../../../../packages/connectors/src/settings/runtime.ts"
import attachmentsRuntime from "../../../../packages/connectors/src/attachments/runtime.ts"
import chatsRuntime from "../../../../packages/connectors/src/chats/runtime.ts"
import flowsRuntime from "../../../../packages/connectors/src/flows/runtime.ts"
import webfetchRuntime, { extractReadable, fetchPage, fetchPageTool } from "../../../../packages/connectors/src/webfetch/runtime.ts"
import { forgetDocumentTool, forgetTool, resolveContainer, shouldRecall } from "../../../../packages/connectors/src/recall/runtime.ts"
import { normalizeSourceId, profileText, RecallApi } from "../../../../packages/connectors/src/recall/api.ts"
import { getCalendarTool, createGcalRuntime } from "../../../../packages/connectors/src/gcal/runtime.ts"
import { GoogleTokens, presentEvent, resolveRange, sortEvents } from "../../../../packages/connectors/src/gcal/calendar.ts"
import { buildAuthUrl, redirectUriFor } from "../../../../packages/connectors/src/gcal/oauth.ts"
import { t3Tools, finishedText, waitingText, threadIdOf, environmentIdOf, parseConfig as t3ParseConfig } from "../../../../packages/connectors/src/t3code/runtime.ts"
import { matchProject, parseModel, T3Client } from "../../../../packages/connectors/src/t3code/client.ts"
import { suggestClass } from "../../../../packages/connectors/src/mcp/client.ts"
import { listingFor, selectTools } from "../../../../packages/connectors/src/mcp/runtime.ts"
import { checkElevenLabsKey, transcribeWithElevenLabs } from "../../../../packages/connectors/src/voice/runtime.ts"
import voiceManifest from "../../../../packages/connectors/src/voice/manifest.ts"
import gcalManifest from "../../../../packages/connectors/src/gcal/manifest.ts"
import mcpManifest from "../../../../packages/connectors/src/mcp/manifest.ts"
import cliproxyManifest from "../../../../packages/connectors/src/cliproxy/manifest.ts"

const connRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/connectors/package.json"))
const dbRequire = createRequire(path.resolve(import.meta.dirname, "../../../../packages/db/package.json"))
const { streamText, stepCountIs, asSchema } = connRequire("ai") as typeof import("ai")
const { sql } = dbRequire("drizzle-orm") as typeof import("drizzle-orm")

const KEY = DEV_MASTER_KEY
const APP = "https://palui.example.com"
setConfig({ masterKey: KEY, mode: "dev", appUrl: APP })

const testdata = path.resolve(import.meta.dirname, "..")
const seeded = JSON.parse(readFileSync(path.join(testdata, "seeded.json"), "utf8")) as { now: number; golden: { chatId: string } }
const SEED_NOW = seeded.now
const NOW = Date.parse("2026-11-10T02:00:00Z")
const WITA = "Asia/Makassar"

// --- normalization -----------------------------------------------------------------------

function normText(s: string): string {
  const seen = new Map<string, string>()
  return s
    .replace(/\/api\/share\/[A-Za-z0-9_-]{32}/g, "/api/share/<token>")
    .replace(/\b([a-z]{2,6})_[0-9a-z]{19}\b/g, (m, p: string) => {
      if (!seen.has(m)) seen.set(m, `${p}#${seen.size + 1}`)
      return seen.get(m)!
    })
}

// What the model gets from a tool result: JSON.stringify of the value, with
// undefined dropped, ids normalized. Strings stay strings.
function resultText(v: unknown): string {
  if (v === undefined) return "undefined"
  return normText(JSON.stringify(v))
}

// --- manifests ---------------------------------------------------------------------------

type Zodish = { safeParse(v: unknown): { success: boolean; data?: unknown; error?: { issues: unknown[] } }; shape?: Record<string, unknown> }

const manifests = manifestList.map((m) => {
  const { config, secrets, ...plain } = m as unknown as Record<string, unknown> & { config: Zodish; secrets: Record<string, Zodish> }
  const parsed = config.safeParse({})
  return {
    ...plain,
    configKeys: Object.keys(config.shape ?? {}),
    configDefaults: parsed.success ? { ok: true, value: parsed.data } : { ok: false, issues: parsed.error!.issues },
    secretFields: Object.entries(secrets).map(([name, s]) => ({ name, optional: s.safeParse(undefined).success })),
  }
})

// --- catalogs ----------------------------------------------------------------------------

async function describeTools(tools: Record<string, PaluiTool>) {
  const out = []
  for (const [name, t] of Object.entries(tools)) {
    out.push({
      name,
      title: t.title ?? null,
      description: t.tool.description ?? null,
      class: t.class,
      untrusted: t.untrusted ?? false,
      guestSafe: t.guestSafe ?? false,
      flowApproval: t.flowApproval ?? null,
      needsApproval: typeof t.needsApproval === "function",
      preview: typeof t.preview === "function",
      inputSchema: await asSchema(t.tool.inputSchema).jsonSchema,
    })
  }
  return out
}

function turnOf(chatId: string, extra: Partial<TurnInfo> = {}): TurnInfo {
  return { runId: "run_golden", chatId, channel: "telegram", trigger: "message", tz: WITA, now: NOW, ...extra }
}

const catalogDb = memoryDb()
const catalogChat = ensureWebChat(catalogDb, NOW)
const otherAgent = createAgent(catalogDb, { slug: "kerja", name: "Kerja", persona: "Asisten kerja." })
const ownerTurn = turnOf(catalogChat.id, { channel: "web", origin: "message", mode: "owner", tainted: () => false, ownerText: "halo" })

const fakeT3 = { add() {}, list: () => [], remember() {}, history: () => [], poll: async () => {} }
const catalogs: Record<string, { variant: string; tools: unknown[] }[]> = {
  attachments: [{ variant: "default", tools: await describeTools(attachmentTools(catalogDb, ownerTurn)) }],
  chats: [{ variant: "default", tools: await describeTools(chatTools(catalogDb, ownerTurn)) }],
  flows: [
    { variant: "owner", tools: await describeTools({ ...flowTools(catalogDb, ownerTurn), recap: recapTool(catalogDb, ownerTurn) }) },
    { variant: "flow", tools: await describeTools({ recap: recapTool(catalogDb, { ...ownerTurn, trigger: "flow", flowId: "flw_x" }) }) },
  ],
  hooks: [{ variant: "default", tools: await describeTools(await (await hooksRuntime.start(ctxFor(catalogDb, "hooks"))).tools!(ownerTurn)) }],
  jobs: [{ variant: "default", tools: await describeTools(jobTools(catalogDb)) }],
  notes: [
    { variant: "default", tools: await describeTools(notesTools(catalogDb)) },
    { variant: "agent", tools: await describeTools(notesTools(catalogDb, undefined, otherAgent.id)) },
  ],
  outbound: [
    { variant: "owner", tools: await describeTools(outboundTools(catalogDb, ownerTurn)) },
    { variant: "unattended", tools: await describeTools(outboundTools(catalogDb, { ...ownerTurn, origin: "schedule" })) },
  ],
  reminders: [{ variant: "default", tools: await describeTools(reminderTools(catalogDb, ownerTurn)) }],
  "remote-mcp": [{ variant: "default", tools: await describeTools(remoteMcpTools({ db: catalogDb, masterKey: KEY })) }],
  settings: [
    { variant: "default", tools: await describeTools(settingsTools(catalogDb)) },
    { variant: "agent", tools: await describeTools(settingsTools(catalogDb, undefined, otherAgent.id)) },
  ],
  webfetch: [{ variant: "default", tools: await describeTools(await (await webfetchRuntime.start(ctxFor(catalogDb, "webfetch"))).tools!(ownerTurn)) }],
  recall: [
    {
      variant: "rest",
      tools: await describeTools({
        forget_memory: { tool: forgetTool(new RecallApi("http://r", "rcl_x")), class: "destructive", title: "Forget memory" },
        forget_document: { tool: forgetDocumentTool(new RecallApi("http://r", "rcl_x")), class: "destructive", title: "Delete document" },
      }),
    },
  ],
  gcal: [
    {
      variant: "default",
      tools: await describeTools({
        get_calendar: {
          tool: getCalendarTool({ tokens: new GoogleTokens({ clientId: "c", clientSecret: "s", refreshToken: "r" }), config: { calendarIds: ["primary"] }, turn: ownerTurn }),
          class: "read",
          untrusted: true,
          title: "Check the calendar",
        },
      }),
    },
  ],
  t3code: [
    {
      variant: "default",
      tools: await describeTools(
        t3Tools({ client: () => ({}) as T3Client, config: t3ParseConfig({}), turn: ownerTurn, watcher: fakeT3, untrusted: false, onError: () => {} }),
      ),
    },
    {
      variant: "custom",
      tools: await describeTools(
        t3Tools({ client: () => ({}) as T3Client, config: t3ParseConfig({ baseRef: "develop" }), turn: ownerTurn, watcher: fakeT3, untrusted: true, onError: () => {} }),
      ),
    },
  ],
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

// --- inputs ------------------------------------------------------------------------------

// Runs one tool call through streamText and returns what the model sees: the
// tool-call input (zod's output) and, for a refused input, the error text.
async function streamInput(name: string, t: PaluiTool, input: unknown) {
  const wrapped = { ...t.tool, execute: async () => "executed" }
  const m = scriptedModel([callTool(name, input, "call_1"), say("done")])
  const result = streamText({ model: m.model, tools: { [name]: wrapped } as never, prompt: "x", stopWhen: stepCountIs(1) })
  const msgs = (await result.response).messages as { role: string; content: { type: string; input?: unknown; output?: { type: string; value: unknown } }[] }[]
  const call = msgs.find((x) => x.role === "assistant")?.content.find((p) => p.type === "tool-call")
  const res = msgs.find((x) => x.role === "tool")?.content.find((p) => p.type === "tool-result")
  const out = res?.output
  return out?.type === "error-text" ? { input, error: out.value } : { input, parsed: call?.input }
}

const inputCases: Record<string, Record<string, unknown[]>> = {
  attachments: {
    save_attachment: [{ title: "Struk" }, { nomor: 2, earlier: true, title: "x", description: "y" }, {}, { title: "" }, { nomor: 0, title: "x" }, { nomor: 1.5, title: "x" }, { title: "x".repeat(201) }, { title: 5 }],
    list_attachments: [{}, { query: "ktp", limit: 5 }, { limit: 51 }, { query: "q".repeat(101) }],
    get_attachment: [{ id: "att_x" }, {}, { id: "" }],
    share_attachment: [{ id: "att_x" }, { id: "att_x", visibility: "public", expires: "1h" }, { id: "att_x", visibility: "everyone" }, { id: "att_x", expires: "2d" }],
    unshare_attachment: [{ id: "att_x" }, { id: 1 }],
    delete_attachment: [{ id: "att_x" }, { id: "x".repeat(65) }],
  },
  chats: {
    search_chats: [{}, { query: "kel", channel: "whatsapp", kind: "group", limit: 3 }, { channel: "sms" }, { limit: 0 }, { limit: 21 }, { kind: "channel" }],
    read_chat: [{ chat: "chat_x" }, { chat: "x", limit: 5, before: "2026-11-10T09:00", since: "2026-11-09", query: "a" }, {}, { chat: "" }, { chat: "x", limit: 101 }, { chat: "x", channel: "fax" }],
  },
  flows: {
    create_flow: [{ definition: { name: "x" } }, {}, { definition: "x" }, { definition: [1] }],
    update_flow: [{ id: "f", patch: { a: 1 }, note: "n" }, {}, { id: "f", note: "n".repeat(201) }],
    check_flow: [{}, { id: "f" }, { definition: 1 }],
    test_flow: [{}, { id: "f", stubs: { a: 1 }, trigger: {}, start: "main", step: "s" }, { stubs: 1 }],
    list_flows: [{}, { filter: "paused" }, { filter: "broken" }],
    get_flow: [{ id: "f" }, { id: "f", version: 2 }, { id: "f", version: 0 }, { id: "f", version: 1.5 }, {}],
    pause_flow: [{ id: "f", paused: true }, { id: "f", paused: true, until: "2026-11-12T08:00" }, { id: "f", paused: "yes" }, { id: "f", paused: true, until: "besok" }, {}],
    delete_flow: [{ id: "f" }, {}],
    run_flow: [{ id: "f" }, { id: "f", trigger: "main", args: { x: 1 } }, { id: "f", args: [] }],
    flow_history: [{}, { id: "f", days: 30, status: "done", limit: 5 }, { days: 181 }, { status: "late" }, { limit: 0 }],
    get_run: [{ runId: "r" }, {}],
    cancel_run: [{ runId: "r" }, { runId: 3 }],
    answer_checkin: [{ answer: "udah" }, { answer: "", note: "x" }, { answer: "a".repeat(41) }, {}],
    explain_flow: [{ id: "f" }, { id: "f", at: "2026-11-10T03:00" }, { id: "f", at: "2026-11-10" }],
    recap: [{}, { flow: "self", days: 30, trigger: "main" }, { days: 91 }, { flow: "f".repeat(81) }],
  },
  jobs: { list_jobs: [{}, { includePaused: false }, { includePaused: "no" }] },
  notes: {
    save_instruction: [{ title: "t", body: "b" }, { id: "ins_x", title: "t", body: "b" }, { title: "", body: "b" }, { title: "t".repeat(81), body: "b" }, { title: "t" }],
    list_instructions: [{}],
    delete_instruction: [{ id: "x" }, {}],
    save_preference: [{ key: "default_city", value: "Banjarmasin" }, { key: "", value: "x" }, { key: "k".repeat(41), value: "x" }, { key: "k", value: "v".repeat(301) }],
    get_preferences: [{}],
    delete_preference: [{ key: "k" }, { key: "" }],
  },
  outbound: {
    list_contacts: [{}],
    list_outbound_messages: [{}, { filter: "recent", limit: 3 }, { filter: "all" }],
    send_message: [
      { to: "Zaki", text: "halo" },
      { to: "0822", name: "Budi", text: "x", signature: false, localDateTime: "2026-11-10T20:00", rrule: null, tz: "Asia/Jakarta" },
      { to: "", text: "x" },
      { to: "Zaki", text: "x".repeat(1801) },
      { to: "Zaki", text: "x", localDateTime: "20:00" },
      { to: "Zaki" },
    ],
    add_contact: [{ name: "Ana", phone: "0822" }, { name: "Ana", phone: "0822", note: "n".repeat(201) }, { name: "", phone: "1" }],
    cancel_message: [{ id: "obm_x" }, {}],
  },
  reminders: {
    set_reminder: [
      { text: "bayar", localDateTime: "2026-11-11T09:00" },
      { text: "bayar", localDateTime: "2026-11-11T09:00:30", rrule: "FREQ=DAILY", tz: "Asia/Jakarta" },
      { text: "bayar", localDateTime: "2026-11-11T09:00", rrule: null },
      { text: "bayar", localDateTime: "besok" },
      { text: "", localDateTime: "2026-11-11T09:00" },
      { text: "x".repeat(501), localDateTime: "2026-11-11T09:00" },
      { localDateTime: "2026-11-11T09:00" },
      {},
    ],
    list_reminders: [{}, { filter: "done", limit: 5 }, { filter: "all" }, { limit: 51 }, { limit: 2.5 }],
    update_reminder: [{ id: "rem_x" }, { id: "rem_x", text: "t", localDateTime: "2026-11-12T07:30", rrule: null, tz: "UTC" }, { id: "rem_x", localDateTime: "x" }, {}],
    cancel_reminder: [{ id: "rem_x" }, {}],
    snooze_reminder: [{ id: "rem_x", preset: "1h" }, { id: "rem_x", localDateTime: "2026-11-10T15:00" }, { id: "rem_x", preset: "2h" }, { id: "rem_x" }],
  },
  "remote-mcp": {
    add_mcp_server: [{ name: "jadwal-sholat", url: "https://x.example.com/mcp" }, { name: "Jadwal", url: "https://x" }, { name: "x", url: "u".repeat(501) }, {}],
    list_mcp_servers: [{}],
    remove_mcp_server: [{ name: "jadwal" }, { name: "" }, { name: "n".repeat(61) }],
  },
  settings: {
    get_settings: [{}],
    update_settings: [
      { timezone: "Asia/Jakarta" },
      { budgets: { dailyTokenCap: 0 }, modelRoles: { fast: [{ connectorId: "cliproxy", model: " m ", effort: "high" }] } },
      { timezone: "Mars/Olympus" },
      { timezone: "" },
      { language: "jv" },
      { budgets: { maxSteps: 500 } },
      { budgets: { dailyTokenCap: 5 } },
      { modelRoles: { fast: [] } },
      { modelRoles: { default: [{ connectorId: "", model: "" }] } },
      { fallbackChannel: "sms", uiLanguage: "fr" },
    ],
  },
  webfetch: { fetch_page: [{ url: "https://example.com" }, { url: "" }, {}, { url: 5 }] },
  recall: {
    forget_memory: [{ id: "mem_1" }, { ids: ["a", "b"] }, {}, { ids: [] }, { ids: Array.from({ length: 21 }, (_, i) => `m${i}`) }, { ids: [""] }, { id: "" }],
    forget_document: [{ id: "doc_1" }, { id: "" }, {}],
  },
  gcal: { get_calendar: [{}, { from: "today", to: "2026-11-12" }, { from: 1 }] },
  t3code: {
    list_projects: [{}],
    list_threads: [{}, { project: "palui", activeOnly: true }, { activeOnly: "yes" }],
    start_thread: [{ project: "palui", task: "Fix" }, { project: "p", task: "t", title: "x".repeat(81) }, { project: "", task: "t" }, { project: "p", task: "t", inPlace: 1 }, {}],
    send_to_thread: [{ thread: "t", message: "m" }, { thread: "", message: "m" }, { thread: "t" }],
    list_chat_threads: [{}],
    get_thread: [{ thread: "t" }, {}],
    stop_thread: [{ thread: "t" }, { thread: "" }],
  },
}

const inputs: Record<string, Record<string, unknown[]>> = {}
for (const [connector, perTool] of Object.entries(inputCases)) {
  const tools: Record<string, PaluiTool> = {}
  for (const v of catalogs[connector] ?? []) void v
  // Rebuild the live tool objects (catalogs above only kept descriptions).
  const live = liveTools(connector)
  inputs[connector] = {}
  for (const [name, cases] of Object.entries(perTool)) {
    const t = live[name] ?? tools[name]
    if (!t) throw new Error(`no tool ${connector}.${name}`)
    inputs[connector][name] = []
    for (const c of cases) inputs[connector][name].push(await streamInput(name, t, c))
  }
}

function liveTools(connector: string): Record<string, PaluiTool> {
  switch (connector) {
    case "attachments":
      return attachmentTools(catalogDb, ownerTurn)
    case "chats":
      return chatTools(catalogDb, ownerTurn)
    case "flows":
      return { ...flowTools(catalogDb, ownerTurn), recap: recapTool(catalogDb, ownerTurn) }
    case "jobs":
      return jobTools(catalogDb)
    case "notes":
      return notesTools(catalogDb)
    case "outbound":
      return outboundTools(catalogDb, ownerTurn)
    case "reminders":
      return reminderTools(catalogDb, ownerTurn)
    case "remote-mcp":
      return remoteMcpTools({ db: catalogDb, masterKey: KEY })
    case "settings":
      return settingsTools(catalogDb)
    case "webfetch":
      return { fetch_page: { tool: fetchPageTool(), class: "read" } }
    case "recall":
      return {
        forget_memory: { tool: forgetTool(new RecallApi("http://r", "rcl_x")), class: "destructive" },
        forget_document: { tool: forgetDocumentTool(new RecallApi("http://r", "rcl_x")), class: "destructive" },
      }
    case "gcal":
      return { get_calendar: { tool: getCalendarTool({ tokens: new GoogleTokens({ clientId: "c", clientSecret: "s", refreshToken: "r" }), config: { calendarIds: ["primary"] }, turn: ownerTurn }), class: "read" } }
    case "t3code":
      return t3Tools({ client: () => ({}) as T3Client, config: t3ParseConfig({}), turn: ownerTurn, watcher: fakeT3, untrusted: false, onError: () => {} })
  }
  throw new Error(connector)
}

// --- write -------------------------------------------------------------------------------

const out = { version: PALUI_VERSION, now: NOW, seedNow: SEED_NOW, manifests, catalogs, inputs, ...(await (await import("./connectors-calls.ts")).calls()) }
writeFileSync(path.join(testdata, "connectors.json"), `${JSON.stringify(out, null, 1)}\n`)
console.log(`connectors.json: ${manifests.length} manifests, ${Object.values(catalogs).flat().reduce((n, v) => n + v.tools.length, 0)} catalog tools`)
void [memoryDb, openDb, newId, messagesTable, sql, copyFileSync, mkdtempSync, readdirSync, tmpdir, ensureChat, getDefaultAgent, upsertConnector, setSetting, addContact, updateContact, BRIEFING, offersChatTools]
void [hooksRuntime, jobsRuntime, notesRuntime, outboundRuntime, remindersRuntime, remoteMcpRuntime, settingsRuntime, attachmentsRuntime, chatsRuntime, flowsRuntime]
void [extractReadable, fetchPage, resolveContainer, shouldRecall, normalizeSourceId, profileText, createGcalRuntime, presentEvent, resolveRange, sortEvents, buildAuthUrl, redirectUriFor]
void [finishedText, waitingText, threadIdOf, environmentIdOf, matchProject, parseModel, suggestClass, listingFor, selectTools, checkElevenLabsKey, transcribeWithElevenLabs, voiceManifest, gcalManifest, mcpManifest, cliproxyManifest, resultText]
