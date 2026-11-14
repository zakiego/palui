// Writes apps/server/testdata/rpc-setup.json: the dashboard server functions
// of apps/app/src/lib/fns/connectors.ts, settings.ts, home.ts and activity.ts
// called on copies of testdata/seeded.db, with the exact JSON each returns or
// the error it throws, and the rows writes leave behind. The Go test
// (internal/httpapi/rpcsetup/golden_test.go) replays every call through the
// HTTP handler and compares.
//
// The real fns modules are imported: rpc-setup-loader.mjs points
// @tanstack/react-start and @/server/session at rpc-setup-shim.ts, so each
// export is { method, call(input) } running the real validator and handler.
// The runtime is a fake ControlClient (setRuntime) with fixed answers; the Go
// test builds a supervisor that answers the same. Date.now is fixed to the
// seed's clock.
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/rpc-setup.ts
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, utimesSync, writeFileSync } from "node:fs"
import { register } from "node:module"
import { tmpdir } from "node:os"
import path from "node:path"
import { pathToFileURL } from "node:url"

const gen = import.meta.dirname
const testdata = path.resolve(gen, "..")
register(pathToFileURL(path.join(gen, "rpc-setup-loader.mjs")), { data: { shim: pathToFileURL(path.join(gen, "rpc-setup-shim.ts")).href } })

const seeded = JSON.parse(readFileSync(path.join(testdata, "seeded.json"), "utf8")) as { now: number; masterKey: string }
const NOW = seeded.now
const KEY = seeded.masterKey
const APP = "http://localhost:4890"
Date.now = () => NOW

const { setConfig } = await import("../../../../packages/core/src/config.ts")
const { openDb } = await import("../../../../packages/db/src/index.ts")
const { getConnector, getSecrets } = await import("../../../../packages/core/src/connectors/store.ts")
const { ControlError } = await import("../../../../packages/core/src/control.ts")
const { setDb } = await import("../../../app/src/server/db.ts")
const { setRuntime } = await import("../../../app/src/server/runtime.ts")
type ServerFn = import("./rpc-setup-shim.ts").ServerFn
const fns: Record<string, ServerFn> = {
  ...((await import("../../../app/src/lib/fns/connectors.ts")) as unknown as Record<string, ServerFn>),
  ...((await import("../../../app/src/lib/fns/settings.ts")) as unknown as Record<string, ServerFn>),
  ...((await import("../../../app/src/lib/fns/home.ts")) as unknown as Record<string, ServerFn>),
  ...((await import("../../../app/src/lib/fns/activity.ts")) as unknown as Record<string, ServerFn>),
}

// Stable rows of seeded.db.
const SEED = {
  webChat: "chat_0mwfavogqfl8g9qpmbj",
  palui: "agt_0mwfavohdfkyeb1kl5c",
  otherAgent: "agt_0mwfavoh23qqz2cayx0",
  run: "run_0mwfavohamlxk7b8msz",
  runNoChat: "run_0mwfavoimi7ui80xojl",
  runJob: "run_0mwfavoimi7ui80xojn",
  flowRun: "flr_0mwfavoid5j097cd9bc",
  flowRunWaiting: "flr_0mwfavoiia5ukduzqt0",
  flowRunParent: "flr_0mwfavoiia5ukduzqsv",
  flowRunApproval: "flr_0mwfavoid5j097cd9bl",
}

// --- the fake runtime ----------------------------------------------------------------
// One spec both stacks build from. Each connector here is a row in the fake
// runtime's own list: enabled ones run (status "on"), the rest are paused.
type Answer = { result: unknown } | { status: number; error: string }
type FakeConnector = { id: string; enabled: boolean; live: Record<string, unknown>; test: { ok: boolean; detail: string; data?: Record<string, unknown> }; actions: Record<string, Answer> | null }
const FAKE: FakeConnector[] = [
  { id: "telegram", enabled: true, live: { username: "palui_bot", ownerClaimed: true, polling: true }, test: { ok: true, detail: "Connected as @palui_bot" }, actions: {} },
  {
    id: "whatsapp",
    enabled: true,
    live: { connection: "open", linked: true, linkedNumber: "6281234567890", qr: null, pairingCode: null, reason: null },
    test: { ok: true, detail: "Linked" },
    actions: { pair: { result: { ok: false, error: "Already linked. Unlink it first." } } },
  },
  {
    id: "cliproxy",
    enabled: true,
    live: { baseUrl: "http://host.docker.internal:8317", protocols: ["anthropic", "openai"] },
    test: { ok: true, detail: "3 models", data: { models: ["claude-sonnet-4-6", "gpt-4.1"], roles: { default: true } } },
    actions: {
      listModels: {
        result: { models: ["claude-sonnet-4-6", "Claude-haiku-4-5", "claude-haiku-4-5", "claude-sonnet-4-6", 7, null, "gpt-4.1", "_internal", "a10", "a9", "Ångström-1", "zeta", "Zeta", "claude-3-5", "claude-35"] },
      },
      boom: { status: 500, error: "Something broke inside the action" },
    },
  },
  { id: "ai-gateway", enabled: false, live: {}, test: { ok: false, detail: "401 Unauthorized: the key was refused" }, actions: {} },
  { id: "mcp-fox", enabled: false, live: {}, test: { ok: true, detail: "2 tools" }, actions: {} },
  { id: "mcp-files", enabled: true, live: { tools: 3, url: "https://files.example.com/mcp", error: null }, test: { ok: true, detail: "3 tools", data: { tools: [] } }, actions: null },
]

function fakeRuntime() {
  const find = (id: string) => FAKE.find((c) => c.id === id)
  const answer = (a: Answer) => {
    if ("result" in a) return a.result as Record<string, unknown>
    throw new ControlError(a.error, a.status)
  }
  return {
    status: async () => ({
      ok: true,
      startedAt: 0,
      schema: "x",
      connectors: FAKE.map((c) => ({ id: c.id, running: c.enabled, status: c.enabled ? "on" : "paused", detail: null, live: c.enabled ? c.live : {} })),
      turnsPaused: { until: null, reason: null },
      activeTurns: [],
    }),
    reconcile: async () => ({ ok: true as const }),
    test: async (id: string) => {
      const c = find(id)
      if (!c) return { ok: false, detail: `No connector ${id}` }
      return c.test
    },
    action: async (id: string, name: string) => {
      const c = find(id)
      if (!c || !c.enabled) {
        // The supervisor answers wizard lookups on a draft from test().
        if (c && (name === "listTools" || name === "listContainers")) {
          if (!c.test.ok) throw new ControlError(c.test.detail, 400)
          return c.test.data ?? {}
        }
        if (!c && (name === "listTools" || name === "listContainers")) throw new ControlError(`No connector ${id}`, 400)
        throw new ControlError(`Connector ${id} isn't running`, 409)
      }
      if (!c.actions) throw new ControlError(`Connector ${id} has no actions`, 404)
      const a = c.actions[name]
      if (!a) throw new ControlError(`Unknown action ${name}`, 404)
      return answer(a)
    },
    remove: async (id: string) => {
      if (!find(id)) throw new ControlError(`No connector ${id}`, 404)
      return { ok: true }
    },
  }
}

function downRuntime() {
  const down = () => Promise.reject(new ControlError("The runtime isn't reachable (fetch failed). Check docker compose logs runtime.", 503))
  return new Proxy({}, { get: () => down })
}

// --- fixtures both stacks add ----------------------------------------------------------

// Two passkeys for the signed-in owner (the Go test inserts them for its own user id).
const PASSKEYS = [
  { id: "pk_golden_1", name: "MacBook", deviceType: "multiDevice", backedUp: 1, createdAt: NOW - 86_400_000 },
  { id: "pk_golden_2", name: null, deviceType: "singleDevice", backedUp: 0, createdAt: NOW - 3_600_000 },
]
// Files in $DATA_DIR/backups (mtime in whole seconds), plus ones latestBackup skips.
const BACKUPS = [
  { file: "palui-2026-11-12.db.gz", bytes: 1234, mtime: NOW / 1000 - 172_800 },
  { file: "palui-2026-11-13.db.gz", bytes: 2345, mtime: NOW / 1000 - 86_400 },
  { file: "palui-2026-11-14.db.gz.tmp-123", bytes: 10, mtime: NOW / 1000 },
  { file: "notes.txt", bytes: 5, mtime: NOW / 1000 },
]

const seedText = readFileSync(path.join(testdata, "seeded.db")).toString("latin1")
const ID_RE = /\b([a-z]{2,6})_[0-9a-z]{19}\b/g
const seedIds = new Set([...seedText.matchAll(ID_RE)].map((m) => m[0]))
const maxEvent = 50

function freshDb(dir: string) {
  const file = path.join(dir, "palui.db")
  copyFileSync(path.join(testdata, "seeded.db"), file)
  const db = openDb(file)
  const c = db.$client
  if ((c.prepare("SELECT max(id) AS n FROM events").get() as { n: number }).n !== maxEvent) throw new Error("seeded.db changed: update maxEvent")
  c.prepare("INSERT INTO user (id, name, email, email_verified, username, display_username, created_at, updated_at) VALUES ('<user>', 'Owner', 'owner@palui.invalid', 0, 'owner', 'owner', ?, ?)").run(NOW, NOW)
  for (const p of PASSKEYS)
    c.prepare("INSERT INTO passkey (id, name, public_key, user_id, credential_id, counter, device_type, backed_up, transports, created_at, aaguid) VALUES (?, ?, 'pk', '<user>', ?, 0, ?, ?, '', ?, NULL)").run(p.id, p.name, `cred-${p.id}`, p.deviceType, p.backedUp, p.createdAt)
  mkdirSync(path.join(dir, "backups"))
  for (const b of BACKUPS) {
    const f = path.join(dir, "backups", b.file)
    writeFileSync(f, Buffer.alloc(b.bytes))
    utimesSync(f, b.mtime, b.mtime)
  }
  return db
}

// --- rows after writes -----------------------------------------------------------------

const CONNECTOR_COLS = "id, type, kind, label, enabled, status, status_detail, trust, config, agent_id, secrets IS NOT NULL AS has_secrets, error_since, alerted_at, sort_order, created_at, updated_at"
const ROWS: Record<string, (arg: string) => string> = {
  connector: (id) => `SELECT ${CONNECTOR_COLS} FROM connectors WHERE id = '${id}'`,
  connectors: () => `SELECT id, status, enabled, agent_id, updated_at FROM connectors ORDER BY id`,
  events: () => `SELECT id, at, level, connector_id, kind, message, data FROM events WHERE id > ${maxEvent} ORDER BY id`,
  setting: (key) => `SELECT key, value, updated_at FROM settings WHERE key = '${key}'`,
  hooks: () => `SELECT id, length(slug) AS slug_length, name, length(secret) > 0 AS has_secret, mode, chat_id, enabled, last_at, created_at FROM hooks ORDER BY created_at, id`,
  identities: () => `SELECT id, channel, external_id, alt_id, display_name, role, created_at FROM identities ORDER BY channel, id`,
}

// Ids made during a call (not in seeded.db) become <prefix>#N in order of
// first appearance; an OAuth state and nonce are random. A new hook's slug
// and secret are replaced in every later text of the scenario.
let volatile: [string, string][] = []
function norm(text: string): string {
  const seen = new Map<string, string>()
  for (const [from, to] of volatile) text = text.split(from).join(to)
  return text
    .replace(ID_RE, (m, p: string) => {
      if (seedIds.has(m)) return m
      if (!seen.has(m)) seen.set(m, `${p}#${seen.size + 1}`)
      return seen.get(m)!
    })
    .replace(/state=[^&"\\]*/g, "state=<state>")
    .replace(/"oauthNonce":"[^"]*"/g, '"oauthNonce":"<nonce>"')
    .replace(/\\"oauthNonce\\":\\"[^"\\]*\\"/g, '\\"oauthNonce\\":\\"<nonce>\\"')
}

// Values JSON can't carry (Date, Map, undefined, ...) by path, so the Go side
// can say how it writes them.
const nonJSON = new Set<string>()
function walk(fn: string, v: unknown, p: string) {
  if (v === undefined) nonJSON.add(`${fn}: ${p} is undefined`)
  else if (v instanceof Date) nonJSON.add(`${fn}: ${p} is a Date`)
  else if (v instanceof Map || v instanceof Set) nonJSON.add(`${fn}: ${p} is a ${v.constructor.name}`)
  else if (typeof v === "bigint") nonJSON.add(`${fn}: ${p} is a bigint`)
  else if (typeof v === "number" && !Number.isFinite(v)) nonJSON.add(`${fn}: ${p} is ${v}`)
  else if (Array.isArray(v)) v.forEach((x, i) => walk(fn, x, `${p}[${i}]`))
  else if (v && typeof v === "object") for (const [k, x] of Object.entries(v)) walk(fn, x, `${p}.${k}`)
}

type Step = { fn: string; input?: unknown; rows?: string[]; secrets?: string[] }
type Scenario = { name: string; runtime: "up" | "down"; steps: Step[] }

const long = "x".repeat(65)
const SCENARIOS: Scenario[] = [
  {
    name: "reads",
    runtime: "up",
    steps: [
      { fn: "getConnectorsPage" },
      { fn: "getConnectorDetail", input: { id: "whatsapp" } },
      { fn: "getConnectorDetail", input: { id: "cliproxy" } },
      { fn: "getConnectorDetail", input: { id: "nope" } },
      { fn: "getConnectorDetail", input: { id: "" } },
      { fn: "getConnectorDetail", input: { id: long } },
      { fn: "getConnectorDetail" },
      { fn: "getSettingsPage" },
      { fn: "listConnectorModels", input: { id: "cliproxy" } },
      { fn: "listConnectorModels", input: { id: "ai-gateway" } },
      { fn: "listConnectorModels", input: { id: "mcp-files" } },
      { fn: "listConnectorModels", input: { id: "telegram" } },
      { fn: "listConnectorModels", input: { id: 5 } },
      { fn: "getShell" },
      { fn: "getHome" },
      { fn: "getStatusSummary" },
      { fn: "getEvents", input: {} },
      { fn: "getEvents", input: { connectorId: "whatsapp" } },
      { fn: "getEvents", input: { levels: ["error", "warn"] } },
      { fn: "getEvents", input: { connectorId: "", levels: [] } },
      { fn: "getEvents", input: { connectorId: "nope", levels: ["debug"] } },
      { fn: "getEvents", input: { levels: ["fatal"] } },
      { fn: "getEvents", input: { levels: ["info", "info", "info", "info", "info"] } },
      { fn: "getEvents", input: { connectorId: long } },
      { fn: "getEvents" },
      { fn: "getRuns" },
      { fn: "getRun", input: { id: SEED.run } },
      { fn: "getRun", input: { id: SEED.runNoChat } },
      { fn: "getRun", input: { id: SEED.runJob } },
      { fn: "getRun", input: { id: SEED.flowRun } },
      { fn: "getRun", input: { id: SEED.flowRunWaiting } },
      { fn: "getRun", input: { id: SEED.flowRunParent } },
      { fn: "getRun", input: { id: SEED.flowRunApproval } },
      { fn: "getRun", input: { id: "flr_nope" } },
      { fn: "getRun", input: { id: "run_nope" } },
      { fn: "getRun", input: { id: "" } },
      { fn: "getRun", input: { id: long } },
      { fn: "getUsage" },
    ],
  },
  {
    name: "reads with the runtime down",
    runtime: "down",
    steps: [
      { fn: "getConnectorsPage" },
      { fn: "getConnectorDetail", input: { id: "whatsapp" } },
      { fn: "getHome" },
      { fn: "listConnectorModels", input: { id: "cliproxy" } },
      { fn: "testConnectorFn", input: { id: "cliproxy" } },
      { fn: "connectorActionFn", input: { id: "whatsapp", name: "pair", input: { phone: "0812" } } },
    ],
  },
  {
    name: "connector writes",
    runtime: "up",
    steps: [
      { fn: "pauseConnectorFn", input: { id: "telegram" }, rows: ["connector:telegram", "events"] },
      { fn: "resumeConnectorFn", input: { id: "telegram" }, rows: ["connector:telegram", "events"] },
      { fn: "resumeConnectorFn", input: { id: "ai-gateway" }, rows: ["connector:ai-gateway"] },
      { fn: "pauseConnectorFn", input: { id: "gcal" }, rows: ["connector:gcal"] },
      { fn: "resumeConnectorFn", input: { id: "voice" } },
      { fn: "pauseConnectorFn", input: { id: "nope" } },
      { fn: "pauseConnectorFn", input: {} },
      { fn: "saveConnectorDraft", input: { id: "telegram-2", type: "telegram", secrets: { botToken: "nope" } }, rows: ["connector:telegram-2"] },
      { fn: "saveConnectorDraft", input: { id: "telegram-2", type: "telegram", label: "  Second bot  ", secrets: { botToken: "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq" }, agentId: SEED.otherAgent }, rows: ["connector:telegram-2", "events"], secrets: ["telegram-2"] },
      { fn: "saveConnectorDraft", input: { id: "Bad Id", type: "telegram" } },
      { fn: "saveConnectorDraft", input: { id: "telegram-2", type: "whatsapp" } },
      { fn: "saveConnectorDraft", input: { id: "x", type: "nope" } },
      { fn: "saveConnectorDraft", input: { id: "mcp-crm", type: "mcp", config: { url: "https://crm.example.com/mcp", authMode: "bearer", junk: 1 }, agentId: "agt_gone" } },
      { fn: "saveConnectorDraft", input: { id: "mcp-crm", type: "mcp", config: { url: "https://crm.example.com/mcp", authMode: "bearer", junk: 1 }, trust: "external", agentId: SEED.otherAgent }, rows: ["connector:mcp-crm"] },
      { fn: "saveConnectorDraft", input: { id: "mcp-crm", type: "mcp", config: { url: "" }, agentId: null }, rows: ["connector:mcp-crm"] },
      { fn: "saveConnectorDraft", input: { id: "mcp-crm", type: "mcp", label: "   " } },
      { fn: "saveConnectorDraft", input: { id: "mcp-crm", type: "mcp", trust: "mine" } },
      { fn: "saveConnectorDraft", input: { id: "mcp-crm", type: "mcp", secrets: { token: "y".repeat(4001) } } },
      { fn: "connectConnectorFn", input: { id: "mcp-crm", type: "mcp" }, rows: ["connector:mcp-crm"] },
      { fn: "connectConnectorFn", input: { id: "mcp-crm", type: "mcp", config: { url: "https://crm.example.com/mcp", authMode: "none" } }, rows: ["connector:mcp-crm", "events"] },
      { fn: "connectConnectorFn", input: { id: "cliproxy-2", type: "cliproxy", config: { baseUrl: "http://localhost:8317" } }, rows: ["connector:cliproxy-2"] },
      { fn: "saveConnectorDraft", input: { id: "gcal", type: "gcal", secrets: { clientId: "123-abc.apps.googleusercontent.com", clientSecret: "GOCSPX-secret" }, config: { email: "owner@example.com" } }, rows: ["connector:gcal"], secrets: ["gcal"] },
      { fn: "connectorActionFn", input: { id: "gcal", name: "authUrl" }, rows: ["connector:gcal"] },
      { fn: "connectorActionFn", input: { id: "golden-telegram", name: "authUrl" } },
      { fn: "connectorActionFn", input: { id: "cliproxy", name: "listModels" } },
      { fn: "connectorActionFn", input: { id: "whatsapp", name: "pair", input: { phone: "0812 3456 7890" } } },
      { fn: "connectorActionFn", input: { id: "cliproxy", name: "nope" } },
      { fn: "connectorActionFn", input: { id: "cliproxy", name: "boom" } },
      { fn: "connectorActionFn", input: { id: "mcp-files", name: "listTools" } },
      { fn: "connectorActionFn", input: { id: "mcp-fox", name: "listTools" } },
      { fn: "connectorActionFn", input: { id: "ai-gateway", name: "listContainers" } },
      { fn: "connectorActionFn", input: { id: "recall", name: "listContainers" } },
      { fn: "connectorActionFn", input: { id: "recall", name: "go" } },
      { fn: "connectorActionFn", input: { id: "cliproxy", name: "" } },
      { fn: "connectorActionFn", input: { id: "cliproxy", name: "listModels", input: [1] } },
      { fn: "testConnectorFn", input: { id: "cliproxy" } },
      { fn: "testConnectorFn", input: { id: "telegram" } },
      { fn: "testConnectorFn", input: { id: "ai-gateway" } },
      { fn: "testConnectorFn", input: { id: "recall" } },
      { fn: "setConnectorOwnerFn", input: { id: "mcp-j", agentId: SEED.otherAgent }, rows: ["connector:mcp-j"] },
      { fn: "setConnectorOwnerFn", input: { id: "mcp-j", agentId: null }, rows: ["connector:mcp-j"] },
      { fn: "setConnectorOwnerFn", input: { id: "mcp-j", agentId: "agt_gone" } },
      { fn: "setConnectorOwnerFn", input: { id: "whatsapp", agentId: null } },
      { fn: "setConnectorOwnerFn", input: { id: "nope", agentId: null } },
      { fn: "setConnectorOwnerFn", input: { id: "mcp-j" } },
      { fn: "setWhatsAppOwnerFn", input: { phone: " 0812 3456 7890 " }, rows: ["identities"] },
      { fn: "setWhatsAppOwnerFn", input: { phone: "+62 811-2222-3333" }, rows: ["identities"] },
      { fn: "setWhatsAppOwnerFn", input: { phone: "0812" } },
      { fn: "setWhatsAppOwnerFn", input: { phone: "0812 3456 abcd" } },
      { fn: "getConnectorDetail", input: { id: "whatsapp" } },
      { fn: "getConnectorsPage" },
      { fn: "getShell" },
    ],
  },
  {
    name: "remove with the runtime up",
    runtime: "up",
    steps: [
      { fn: "removeConnectorFn", input: { id: "mcp-fox" }, rows: ["connectors", "events"] },
      { fn: "removeConnectorFn", input: { id: "recall" }, rows: ["connector:recall"] },
      { fn: "removeConnectorFn", input: { id: "nope" } },
    ],
  },
  {
    name: "writes with the runtime down",
    runtime: "down",
    steps: [
      { fn: "removeConnectorFn", input: { id: "mcp-fox" }, rows: ["connectors", "events"] },
      { fn: "pauseConnectorFn", input: { id: "telegram" }, rows: ["connector:telegram"] },
      { fn: "resumeConnectorFn", input: { id: "telegram" }, rows: ["connector:telegram"] },
      { fn: "connectConnectorFn", input: { id: "cliproxy-2", type: "cliproxy", config: { baseUrl: "http://localhost:8317" }, secrets: { apiKey: "sk-123456789" } }, rows: ["connector:cliproxy-2", "events"] },
      { fn: "testConnectorFn", input: { id: "cliproxy" } },
    ],
  },
  {
    name: "settings writes",
    runtime: "up",
    steps: [
      { fn: "saveSettings", input: { timezone: "Asia/Jakarta", budgets: { maxSteps: 10, turnTimeoutMs: 120000, maxTools: 32, dailyTokenCap: 0, maxRunsPerHour: 30 } }, rows: ["setting:timezone", "setting:budgets"] },
      {
        fn: "saveSettings",
        input: { modelRoles: { vision: [{ connectorId: "cliproxy", model: "  claude-sonnet-4-6  " }], default: [{ connectorId: "cliproxy", model: "claude-opus-4-1", effort: "high" }], fast: [{ model: "m", connectorId: "ai-gateway" }] }, uiLanguage: "en" },
        rows: ["setting:modelRoles", "setting:uiLanguage"],
      },
      { fn: "saveSettings", input: {}, rows: ["setting:timezone"] },
      { fn: "saveSettings", input: { timezone: "Mars/Base" } },
      { fn: "saveSettings", input: { budgets: { maxSteps: 10, turnTimeoutMs: 120000, maxTools: 32, dailyTokenCap: 5, maxRunsPerHour: 30 } } },
      { fn: "saveSettings", input: { fallbackChannel: "email", language: "fr" } },
      { fn: "saveSettings", input: { modelRoles: { default: [], fast: [], vision: [] } } },
      { fn: "saveSettings", input: { budgets: { maxSteps: 1.5, turnTimeoutMs: 120000, maxTools: 32, dailyTokenCap: 0, maxRunsPerHour: 30 } } },
      { fn: "saveSettings", input: { timezone: "asia/makassar", junk: true }, rows: ["setting:timezone"] },
      { fn: "createHookFn", input: { name: "  Deploys  ", chatId: SEED.webChat, mode: "forward" }, rows: ["hooks"] },
      { fn: "createHookFn", input: { name: "Alerts", chatId: "chat_nope", mode: "summarize" } },
      { fn: "createHookFn", input: { name: "Flow", chatId: SEED.webChat, mode: "flow" } },
      { fn: "createHookFn", input: { name: "   ", chatId: SEED.webChat, mode: "forward" } },
      { fn: "dismissChecklist", rows: ["setting:firstRun"] },
      { fn: "dismissChecklist", input: { anything: true }, rows: ["setting:firstRun"] },
      { fn: "getHome" },
      { fn: "getSettingsPage" },
      { fn: "getShell" },
      { fn: "getUsage" },
    ],
  },
]

type StepOut = { fn: string; method: string; input: string | null; result?: string; error?: string; issues?: string; rows?: { sql: string; rows: string }[]; secrets?: Record<string, string> }

const out: { now: number; appUrl: string; masterKey: string; fake: FakeConnector[]; passkeys: typeof PASSKEYS; backups: typeof BACKUPS; maxEvent: number; scenarios: { name: string; runtime: string; steps: StepOut[] }[]; nonJSON: string[] } = {
  now: NOW,
  appUrl: APP,
  masterKey: KEY,
  fake: FAKE,
  passkeys: PASSKEYS,
  backups: BACKUPS,
  maxEvent,
  scenarios: [],
  nonJSON: [],
}

for (const sc of SCENARIOS) {
  const dir = mkdtempSync(path.join(tmpdir(), "palui-rpc-setup-"))
  setConfig({ masterKey: KEY, mode: "dev", appUrl: APP, dataDir: dir })
  const db = freshDb(dir)
  setDb(db)
  setRuntime((sc.runtime === "up" ? fakeRuntime() : downRuntime()) as never)
  const steps: StepOut[] = []
  volatile = []
  for (const st of sc.steps) {
    const fn = fns[st.fn]
    if (!fn) throw new Error(`no server function ${st.fn}`)
    const o: StepOut = { fn: st.fn, method: fn.method, input: st.input === undefined ? null : JSON.stringify(st.input) }
    try {
      const res = await fn.call(st.input)
      walk(st.fn, res, "result")
      if (st.fn === "createHookFn") {
        const r = res as { slug: string; secret: string }
        volatile.push([r.slug, "<slug>"], [r.secret, "<secret>"])
      }
      o.result = norm(JSON.stringify(res))
    } catch (err) {
      const e = err as Error & { issues?: unknown }
      o.error = norm(e.message)
      if (e.name === "ZodError" || (e.constructor?.name ?? "").includes("ZodError")) o.issues = JSON.stringify(e.issues)
    }
    if (st.rows) {
      o.rows = st.rows.map((r) => {
        const [kind, arg] = r.split(":") as [string, string]
        const sql = ROWS[kind]!(arg)
        return { sql, rows: norm(JSON.stringify(db.$client.prepare(sql).all())) }
      })
    }
    if (st.secrets) {
      o.secrets = {}
      for (const id of st.secrets) {
        const row = getConnector(db, id)
        o.secrets[id] = row ? JSON.stringify(getSecrets(row, KEY)) : "null"
      }
    }
    steps.push(o)
  }
  out.scenarios.push({ name: sc.name, runtime: sc.runtime, steps })
  db.$client.close()
}
out.nonJSON = [...nonJSON].sort()

writeFileSync(path.join(testdata, "rpc-setup.json"), `${JSON.stringify(out, null, 2)}\n`)
console.log(`wrote rpc-setup.json: ${out.scenarios.reduce((n, s) => n + s.steps.length, 0)} calls in ${out.scenarios.length} scenarios`)
for (const n of out.nonJSON) console.log(`non-JSON: ${n}`)
