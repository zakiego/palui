// Goldens for the M8b rpcpeople endpoints: the dashboard server functions of
// apps/app/src/lib/fns/agents.ts, chats.ts, notes.ts and attachments.ts, run
// with their real handler bodies on scratch copies of testdata/seeded.db.
// Writes testdata/rpc-people.json, which internal/httpapi/rpcpeople's golden
// test replays through the Go HTTP handlers.
//
// How the handlers run outside the web server: a module hook swaps
// `@tanstack/react-start` and `@/server/session` for rpc-people-stub.ts, whose
// createServerFn keeps the handler and runs the validator like TanStack
// Start's execValidator, and whose requireUser/requireWriter hand out the
// scratch database. The control client is the real `@/server/runtime` with a
// fake client set through setRuntime, answering from each step's `rt` spec
// (the Go test builds the same fake from the same spec).
//
// Run from apps/app (its tsconfig resolves the "@/" imports):
//   cd apps/app && pnpm tsx ../server/testdata/gen/rpc-people.ts
import { copyFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { registerHooks } from "node:module"
import { tmpdir } from "node:os"
import path from "node:path"
import { pathToFileURL } from "node:url"

const stubURL = pathToFileURL(path.join(import.meta.dirname, "rpc-people-stub.ts")).href
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === "@tanstack/react-start") return { url: stubURL, shortCircuit: true }
    const r = next(specifier, context)
    if (r.url.endsWith("/apps/app/src/server/session.ts")) return { url: stubURL, shortCircuit: true }
    return r
  },
})

const testdata = path.resolve(import.meta.dirname, "..")
const seeded = JSON.parse(readFileSync(path.join(testdata, "seeded.json"), "utf8")) as { now: number; masterKey: string }
const NOW = seeded.now
// apitest.AppURL on the Go side.
const APP_URL = "http://localhost:4890"

// Every Date.now() in the handlers (and their defaults) reads the seed's clock.
Date.now = () => NOW

const { setConfig } = await import("../../../../packages/core/src/config.ts")
setConfig({ masterKey: seeded.masterKey, mode: "dev", appUrl: APP_URL })
const { openDb } = await import("../../../../packages/db/src/index.ts")
const { ControlError } = await import("../../../../packages/core/src/control.ts")
const stub = await import("./rpc-people-stub.ts")
const { setRuntime } = await import("../../../app/src/server/runtime.ts")
const fns: Record<string, (input?: unknown) => Promise<unknown>> = {
  ...((await import("../../../app/src/lib/fns/agents.ts")) as never),
  ...((await import("../../../app/src/lib/fns/chats.ts")) as never),
  ...((await import("../../../app/src/lib/fns/notes.ts")) as never),
  ...((await import("../../../app/src/lib/fns/attachments.ts")) as never),
}

// Stable rows of seeded.db.
const S = {
  palui: "agt_0mwfavohdfkyeb1kl5c",
  arisan: "agt_0mwfavogs42b5joh3on",
  arisanSlug: "koordinator-arisan-haul-",
  cs: "agt_0mwfavoh23qqz2cayx0",
  webChat: "chat_0mwfavogqfl8g9qpmbj",
  groupChat: "chat_0mwfavogrpdfz4jjerg",
  dm: "chat_0mwfavogqfl8g9qpmbl",
  telegramDm: "chat_0mwfavogqfl8g9qpmbo",
  attachment: "att_0mwfavojm46xavsrj14",
  instruction: "ins_0mwfavogpjlrnvx3mlp",
}

// A fake control client's answers. Unset methods answer like upRuntime in
// apps/app/src/server/test-helpers.ts; `down` makes every call fail the way
// a stopped runtime does (and nothing is recorded); an { error } answer is
// a ControlError with that status and message.
type Answer = Record<string, unknown> | { error: { status: number; message: string } }
type RtSpec = { down?: boolean; webSend?: Answer; approve?: Answer; stop?: Answer; setChatAgent?: Answer; agentTools?: Answer; action?: Answer }

type Step = { fn: string; input?: unknown; rt?: RtSpec } | { sql: string }
type Case = { name: string; steps: Step[]; rows?: string[] }

const DEFAULTS: Record<string, Record<string, unknown>> = {
  webSend: { ok: true, chatId: S.webChat, messageId: "msg_fixed" },
  approve: { ok: true },
  stop: { ok: true },
  setChatAgent: { ok: true },
  agentTools: { tools: [] },
  action: {},
}

function fakeRuntime(spec: RtSpec, calls: unknown[][]) {
  const method = (name: string) =>
    async (...args: unknown[]) => {
      if (spec.down) throw new ControlError("The runtime isn't reachable (fetch failed). Check docker compose logs runtime.", 503)
      calls.push([name, ...args])
      const answer = (spec as Record<string, Answer | undefined>)[name] ?? DEFAULTS[name]
      if (answer && "error" in answer && typeof answer.error === "object") {
        const e = answer.error as { status: number; message: string }
        throw new ControlError(e.message, e.status)
      }
      return structuredClone(answer)
    }
  return Object.fromEntries(Object.keys(DEFAULTS).map((k) => [k, method(k)])) as never
}

const TOOL = { name: "mcp-files__read", tool: "read", connectorId: "mcp-files", connectorLabel: "Files", connectorType: "mcp", class: "read", title: null, untrusted: true, guestSafe: false }
const TOOL2 = { name: "reminders__set_reminder", tool: "set_reminder", connectorId: "reminders", connectorLabel: "Reminders", connectorType: "reminders", class: "write", title: "Set a reminder", untrusted: false, guestSafe: true }

const agentBody = (over: Record<string, unknown> = {}) => ({
  name: "Chef Banjar",
  persona: "Kamu koki Banjar yang ramah.",
  ownerPowers: "off",
  guestTools: "safe",
  dailyTokenCap: 50000,
  recallContainer: null,
  recallReadContainers: [],
  recallPerChat: false,
  telegramConnectorId: null,
  avatarImage: null,
  avatarIcon: "chef-hat",
  avatarColor: "orange",
  ...over,
})

const CONTAINERS = (perChat: string) => [
  { container: "default", memories: 12, documents: 3, lastActivity: NOW - 3600_000, extra: "kept as is" },
  { container: `${perChat}a1b2c3d4e5`, memories: 3, documents: 1, lastActivity: NOW - 86400_000 },
  { container: `${perChat}f6a7b8c9d0`, memories: 4, documents: 2, lastActivity: NOW },
  { container: "agent-cs-shop", memories: 50, documents: 5, lastActivity: NOW },
]

const AGENT_ROWS = "SELECT * FROM agents ORDER BY rowid"
const CHAT_ROWS = (id: string) => `SELECT * FROM chats WHERE id = '${id}' ORDER BY rowid`
const SETTING = (key: string) => `SELECT * FROM settings WHERE key = '${key}' ORDER BY rowid`

const cases: Case[] = [
  // --- agents.ts -------------------------------------------------------------------
  { name: "agents page", steps: [{ fn: "getAgentsPage" }, { fn: "getAgentsPage", input: { ignored: true } }] },
  {
    name: "agent page",
    steps: [
      { fn: "getAgentPage", input: { slug: "palui" } },
      { fn: "getAgentPage", input: { slug: "  CS  " } },
      { fn: "getAgentPage", input: { slug: S.arisanSlug } },
      { fn: "getAgentPage", input: { slug: "nobody" } },
      { fn: "getAgentPage", input: { slug: "   " } },
      { fn: "getAgentPage", input: { slug: "x".repeat(65) } },
      { fn: "getAgentPage", input: { slug: 7 } },
      { fn: "getAgentPage" },
    ],
  },
  {
    name: "agent tools",
    steps: [
      { fn: "getAgentToolsFn", input: { id: S.arisan }, rt: { agentTools: { tools: [TOOL, TOOL2] } } },
      { fn: "getAgentToolsFn", input: { id: S.palui } },
      { fn: "getAgentToolsFn", input: { id: S.cs }, rt: { down: true } },
      { fn: "getAgentToolsFn", input: { id: S.cs }, rt: { agentTools: { error: { status: 404, message: "No such agent" } } } },
      { fn: "getAgentToolsFn", input: { id: "agt_missing" } },
      { fn: "getAgentToolsFn", input: { id: "" } },
      { fn: "getAgentToolsFn", input: { id: "x".repeat(101) } },
    ],
  },
  {
    name: "agent memory",
    steps: [
      { fn: "getAgentMemoryFn", input: { id: S.palui }, rt: { action: { containers: CONTAINERS("x") } } },
      { fn: "getAgentMemoryFn", input: { id: S.arisan }, rt: { action: { containers: CONTAINERS("agent-arisan-keluarga-bani-abdurrahman-siddiq-banjarm-c") } } },
      { fn: "getAgentMemoryFn", input: { id: S.arisan }, rt: { action: { containers: [] } } },
      { fn: "getAgentMemoryFn", input: { id: S.cs }, rt: { action: { containers: CONTAINERS("x") } } },
      { fn: "getAgentMemoryFn", input: { id: S.cs }, rt: { action: { containers: "not a list" } } },
      { fn: "getAgentMemoryFn", input: { id: S.cs }, rt: { action: {} } },
      { fn: "getAgentMemoryFn", input: { id: S.cs }, rt: { down: true } },
      { fn: "getAgentMemoryFn", input: { id: S.cs }, rt: { action: { error: { status: 409, message: "recall isn't running" } } } },
      { fn: "getAgentMemoryFn", input: { id: "agt_missing" } },
      { sql: "UPDATE connectors SET enabled = 0, config = '{\"container\":\"rumah\"}' WHERE id = 'recall'" },
      { fn: "getAgentMemoryFn", input: { id: S.palui } },
      { fn: "getAgentMemoryFn", input: { id: S.cs } },
      { sql: "DELETE FROM connectors WHERE kind = 'memory'" },
      { fn: "getAgentMemoryFn", input: { id: S.palui } },
      { fn: "getAgentMemoryFn", input: { id: S.arisan } },
    ],
  },
  {
    name: "create an agent",
    steps: [
      { fn: "saveAgentFn", input: { ...agentBody({ recallReadContainers: [" KB-Resep ", "kb-resep", "", "menu"] }), slug: "  Chef " } },
      { fn: "getAgentPage", input: { slug: "chef" } },
      { fn: "saveAgentFn", input: { ...agentBody(), slug: "cs" } },
      { fn: "saveAgentFn", input: { ...agentBody(), slug: "9lives" } },
      { fn: "saveAgentFn", input: agentBody() },
      { fn: "saveAgentFn", input: { ...agentBody({ recallContainer: "default" }), slug: "chef2" } },
      { fn: "saveAgentFn", input: { ...agentBody({ recallReadContainers: ["default"] }), slug: "chef3" } },
      { fn: "saveAgentFn", input: { ...agentBody({ recallContainer: "Bad Name" }), slug: "chef4" } },
      { fn: "saveAgentFn", input: { ...agentBody({ telegramConnectorId: "tgbot-arisan" }), slug: "chef5" } },
      { fn: "saveAgentFn", input: { ...agentBody({ avatarImage: "data:image/gif;base64,R0lGOD" }), slug: "chef6" } },
      { fn: "saveAgentFn", input: { ...agentBody({ name: "  Multi\n\n\nline\r\nname  ", persona: "a\r\n\r\n\r\n\r\nb" }), slug: "chef7" } },
    ],
    rows: [AGENT_ROWS],
  },
  {
    name: "update an agent",
    steps: [
      { fn: "saveAgentFn", input: { ...agentBody({ name: "CS Toko", ownerPowers: "approve", guestTools: "none", recallContainer: "  shop  ", recallPerChat: true, avatarImage: "data:image/png;base64,iVBORw0KGgo=" }), id: S.cs } },
      { fn: "saveAgentFn", input: { ...agentBody({ name: "Palui", ownerPowers: "approve", guestTools: "none", telegramConnectorId: "tgbot-arisan", recallContainer: "rumah" }), id: S.palui } },
      { fn: "saveAgentFn", input: { ...agentBody(), id: "agt_missing" } },
      { fn: "saveAgentFn", input: { ...agentBody({ telegramConnectorId: "tgbot-arisan" }), id: S.cs } },
      { fn: "saveAgentFn", input: { ...agentBody({ telegramConnectorId: "tgbot-arisan" }), id: S.arisan } },
      { fn: "getAgentsPage" },
    ],
    rows: [AGENT_ROWS],
  },
  {
    name: "agent fields refused",
    steps: [
      { fn: "saveAgentFn", input: { ...agentBody({ dailyTokenCap: 1.5 }), slug: "chef" } },
      { fn: "saveAgentFn", input: { ...agentBody({ dailyTokenCap: -1, name: "   " }), slug: "chef" } },
      { fn: "saveAgentFn", input: { ...agentBody({ avatarIcon: "rocket", avatarColor: "gold", ownerPowers: "all" }), slug: "chef" } },
      { fn: "saveAgentFn", input: { ...agentBody({ recallReadContainers: ["a", "b", "c", "d", "e", "f"] }), slug: "chef" } },
      { fn: "saveAgentFn", input: { name: "Chef" } },
      { fn: "saveAgentFn", input: { ...agentBody({ name: "x".repeat(41) }), slug: "y".repeat(25), id: "" } },
      { fn: "saveAgentFn", input: { ...agentBody({ persona: "p".repeat(8001), telegramConnectorId: "" }), slug: "chef" } },
    ],
    rows: [AGENT_ROWS],
  },
  {
    name: "delete an agent",
    steps: [
      { fn: "deleteAgentFn", input: { id: S.cs } },
      { fn: "deleteAgentFn", input: { id: S.cs } },
      { fn: "deleteAgentFn", input: { id: S.palui } },
      { fn: "deleteAgentFn", input: { id: S.arisan } },
      { fn: "deleteAgentFn", input: {} },
    ],
    rows: [
      AGENT_ROWS,
      "SELECT id, agent_id FROM chats ORDER BY rowid",
      "SELECT id, enabled, agent_id FROM connectors ORDER BY rowid",
      "SELECT id, agent_id FROM instructions ORDER BY rowid",
      "SELECT * FROM contacts ORDER BY rowid",
      "SELECT * FROM outbound_messages ORDER BY rowid",
      "SELECT id, source, status, error, claimed_at FROM outbox ORDER BY rowid",
    ],
  },
  {
    name: "tool permissions",
    steps: [
      { fn: "setAgentToolPermissionsFn", input: { id: S.cs, allowAll: true, tools: { "mcp-j__search": "deny", web__fetch: "ask", "2": "allow" } } },
      { fn: "setAgentToolPermissionsFn", input: { id: S.cs, tools: { web__fetch: null, "mcp-j__search": "allow" } } },
      { fn: "setAgentToolPermissionsFn", input: { id: S.cs, allowAll: false, tools: { "mcp-j__search": null, "2": null } } },
      { fn: "setAgentToolPermissionsFn", input: { id: S.arisan } },
      { fn: "setAgentToolPermissionsFn", input: { id: S.cs, tools: { "bad name": "allow" } } },
      { fn: "setAgentToolPermissionsFn", input: { id: S.cs, tools: { ["k".repeat(65)]: "allow", ok: "maybe", fine: null } } },
      { fn: "setAgentToolPermissionsFn", input: { id: "agt_missing", allowAll: true } },
      { fn: "setAgentToolPermissionsFn", input: { id: S.cs, allowAll: "yes", tools: [] } },
    ],
    rows: ["SELECT id, tool_permissions, updated_at FROM agents ORDER BY rowid"],
  },
  {
    name: "switch a chat's agent through the runtime",
    steps: [
      { fn: "setChatAgentFn", input: { chatId: S.dm, agentId: S.cs } },
      { fn: "setChatAgentFn", input: { chatId: S.dm, agentId: null } },
      { fn: "setChatAgentFn", input: { chatId: S.dm, agentId: "agt_missing" }, rt: { setChatAgent: { error: { status: 400, message: "No agent agt_missing." } } } },
      { fn: "setChatAgentFn", input: { chatId: "chat_missing", agentId: null }, rt: { setChatAgent: { error: { status: 404, message: "No chat chat_missing" } } } },
      { fn: "setChatAgentFn", input: { chatId: S.dm } },
    ],
    rows: [CHAT_ROWS(S.dm)],
  },
  {
    name: "switch a chat's agent with the runtime down",
    steps: [
      { fn: "setChatAgentFn", input: { chatId: S.dm, agentId: S.cs }, rt: { down: true } },
      { fn: "setChatAgentFn", input: { chatId: S.groupChat, agentId: null }, rt: { down: true } },
      { fn: "setChatAgentFn", input: { chatId: S.dm, agentId: "agt_missing" }, rt: { down: true } },
      { fn: "setChatAgentFn", input: { chatId: "chat_missing", agentId: null }, rt: { down: true } },
      { sql: `UPDATE chats SET connector_id = 'tgbot-arisan', allowed = 0 WHERE id = '${S.telegramDm}'` },
      { fn: "setChatAgentFn", input: { chatId: S.telegramDm, agentId: S.cs }, rt: { down: true } },
      { fn: "setChatAgentFn", input: { chatId: S.telegramDm, agentId: S.arisan }, rt: { down: true } },
    ],
    rows: [
      `SELECT id, agent_id, allowed, session_id FROM chats WHERE id IN ('${S.dm}', '${S.groupChat}', '${S.telegramDm}') ORDER BY rowid`,
      `SELECT * FROM sessions WHERE chat_id IN ('${S.dm}', '${S.groupChat}', '${S.telegramDm}') ORDER BY rowid`,
    ],
  },
  {
    name: "reply mode, allowed and deploy notice",
    steps: [
      { fn: "setChatReplyModeFn", input: { chatId: S.groupChat, mode: "all" } },
      { fn: "setChatReplyModeFn", input: { chatId: "chat_missing", mode: "mention" } },
      { fn: "setChatReplyModeFn", input: { chatId: S.groupChat, mode: "never" } },
      { fn: "setChatAllowedFn", input: { chatId: S.dm, allowed: false } },
      { fn: "setChatAllowedFn", input: { chatId: "chat_missing", allowed: true } },
      { fn: "setChatAllowedFn", input: { chatId: S.dm } },
      { fn: "setChatDeployNoticeFn", input: { chatId: S.dm, on: true } },
      { fn: "setChatDeployNoticeFn", input: { chatId: S.groupChat, on: false } },
      { fn: "setChatDeployNoticeFn", input: { chatId: S.groupChat, on: true } },
      { fn: "setChatDeployNoticeFn", input: { chatId: "chat_missing", on: true } },
      { fn: "getAgentPage", input: { slug: S.arisanSlug } },
    ],
    rows: [CHAT_ROWS(S.groupChat), CHAT_ROWS(S.dm), SETTING("deployNoticeChats")],
  },
  {
    name: "ack",
    steps: [
      { fn: "setChatAckFn", input: { chatId: S.dm, on: false } },
      { fn: "setChatAckFn", input: { chatId: S.dm, emoji: " 👍🏽 " } },
      { fn: "setChatAckFn", input: { chatId: S.dm, on: true, emoji: "👨‍👩‍👧" } },
      { fn: "setChatAckFn", input: { chatId: S.dm, emoji: "ok" } },
      { fn: "setChatAckFn", input: { chatId: S.dm, emoji: "" } },
      { fn: "setChatAckFn", input: { chatId: S.dm, emoji: "👍👍" } },
      { fn: "setChatAckFn", input: { chatId: S.dm } },
      { fn: "setChatAckFn", input: { chatId: "chat_missing" } },
      { fn: "setChatAckFn", input: { chatId: "chat_missing", on: true } },
      { fn: "setChatAckFn", input: { chatId: S.dm, emoji: "x".repeat(33) } },
    ],
    rows: [CHAT_ROWS(S.dm)],
  },

  // --- chats.ts --------------------------------------------------------------------
  { name: "chat list", steps: [{ fn: "getChats" }] },
  {
    name: "chat list makes the web chat",
    steps: [{ sql: `DELETE FROM chats WHERE channel = 'web'` }, { fn: "getChats" }],
    rows: ["SELECT * FROM chats WHERE channel = 'web' ORDER BY rowid", "SELECT * FROM sessions WHERE chat_id NOT IN (SELECT id FROM chats WHERE channel != 'web') ORDER BY rowid"],
  },
  {
    name: "transcripts",
    steps: [
      { fn: "getTranscript", input: { chatId: S.webChat } },
      { fn: "getTranscript", input: { chatId: S.groupChat } },
      { fn: "getTranscript", input: { chatId: "chat_0mwfavoh5u54sluocmm" } },
      { sql: `UPDATE chats SET session_id = NULL WHERE id = '${S.groupChat}'` },
      { fn: "getTranscript", input: { chatId: S.groupChat } },
      { fn: "getTranscript", input: { chatId: "chat_missing" } },
      { fn: "getTranscript", input: {} },
    ],
  },
  {
    name: "web chat send",
    steps: [
      { fn: "sendWebMessage", input: { text: "  halo  " } },
      { fn: "sendWebMessage", input: { text: "lagi" } },
      { fn: "sendWebMessage", input: { text: "   " } },
      { fn: "sendWebMessage", input: { text: "x".repeat(8001) } },
      { fn: "sendWebMessage", input: { text: "halo" }, rt: { webSend: { error: { status: 400, message: "text is required" } } } },
    ],
    rows: [SETTING("firstRun")],
  },
  {
    name: "web chat send with the runtime down",
    steps: [{ fn: "sendWebMessage", input: { text: "halo" }, rt: { down: true } }],
    rows: [SETTING("firstRun")],
  },
  {
    name: "approvals and stop",
    steps: [
      { fn: "decideApproval", input: { id: "apr_1", approved: true } },
      { fn: "decideApproval", input: { id: "apr_1", approved: false }, rt: { approve: { ok: false } } },
      { fn: "decideApproval", input: { id: "apr_1", approved: true }, rt: { down: true } },
      { fn: "decideApproval", input: { id: "apr_1" } },
      { fn: "stopTurn", input: { chatId: S.webChat } },
      { fn: "stopTurn", input: { chatId: S.webChat }, rt: { stop: { ok: false } } },
      { fn: "stopTurn", input: { chatId: S.webChat }, rt: { down: true } },
      { fn: "stopTurn", input: { chatId: "" } },
    ],
  },

  // --- notes.ts --------------------------------------------------------------------
  {
    name: "notes page",
    steps: [
      { fn: "getNotes", input: {} },
      { fn: "getNotes", input: { agentId: S.arisan } },
      { fn: "getNotes", input: { agentId: "agt_missing" } },
      { fn: "getNotes", input: { agentId: "" } },
      { fn: "getNotes" },
    ],
  },
  {
    name: "instructions",
    steps: [
      { fn: "saveInstructionFn", input: { title: "  Jam\n\nkerja ", body: "Jangan ganggu\r\n\r\n\r\n\r\nsetelah jam 9." } },
      { fn: "saveInstructionFn", input: { title: "Toko", body: "Jawab sopan.", agentId: S.cs } },
      { fn: "saveInstructionFn", input: { id: S.instruction, title: "Diganti", body: "Isi baru" } },
      { fn: "saveInstructionFn", input: { id: "ins_missing", title: "x", body: "y" } },
      { fn: "saveInstructionFn", input: { title: "   ", body: "y" } },
      { fn: "saveInstructionFn", input: { title: "t".repeat(81), body: "b".repeat(2001) } },
      { fn: "setInstructionEnabledFn", input: { id: S.instruction, enabled: false } },
      { fn: "setInstructionEnabledFn", input: { id: "ins_missing", enabled: true } },
      { fn: "deleteInstructionFn", input: { id: "ins_0mwfavogpjlrnvx3mlq" } },
      { fn: "deleteInstructionFn", input: { id: "ins_0mwfavogpjlrnvx3mlq" } },
      { fn: "getNotes", input: {} },
      { fn: "getNotes", input: { agentId: S.cs } },
    ],
    rows: ["SELECT * FROM instructions ORDER BY rowid"],
  },
  {
    name: "preferences",
    steps: [
      { fn: "setPreferenceFn", input: { key: "  Home City ", value: " Banjarmasin\n\nSelatan " } },
      { fn: "setPreferenceFn", input: { key: "x", value: "diganti" } },
      { fn: "setPreferenceFn", input: { key: "1abc", value: "v" } },
      { fn: "setPreferenceFn", input: { key: "k", value: "v".repeat(501) } },
      { fn: "setPreferenceFn", input: { key: "", value: "" } },
      { fn: "deletePreferenceFn", input: { key: "X" } },
      { fn: "deletePreferenceFn", input: { key: "never_set" } },
      { fn: "deletePreferenceFn", input: { key: "k".repeat(41) } },
      { fn: "getNotes", input: {} },
    ],
    rows: ["SELECT * FROM preferences ORDER BY rowid"],
  },

  // --- attachments.ts --------------------------------------------------------------
  { name: "attachments page", steps: [{ fn: "getAttachments" }] },
  {
    name: "share links",
    steps: [
      { fn: "createAttachmentShareFn", input: { id: S.attachment, visibility: "public", duration: "1h" } },
      { fn: "createAttachmentShareFn", input: { id: S.attachment, visibility: "private", duration: "7d" } },
      { fn: "getAttachmentShares", input: { id: S.attachment } },
      { fn: "getAttachments" },
      { fn: "revokeAttachmentShareFn", input: { shareId: "shr_missing" } },
      { sql: "UPDATE attachment_shares SET revoked_at = 1 WHERE visibility = 'public'" },
      { fn: "getAttachmentShares", input: { id: S.attachment } },
      { fn: "getAttachmentShares", input: { id: "att_missing" } },
      { fn: "createAttachmentShareFn", input: { id: "att_missing", visibility: "public", duration: "24h" } },
      { fn: "createAttachmentShareFn", input: { id: S.attachment, visibility: "everyone", duration: "30d" } },
      { fn: "getAttachmentShares", input: { id: "" } },
    ],
    rows: ["SELECT * FROM attachment_shares ORDER BY rowid"],
  },
  {
    name: "revoke and delete files",
    steps: [
      { fn: "createAttachmentShareFn", input: { id: S.attachment, visibility: "public", duration: "24h" } },
      { sql: "UPDATE attachment_shares SET id = 'shr_fixed'" },
      { fn: "revokeAttachmentShareFn", input: { shareId: "shr_fixed" } },
      { fn: "revokeAttachmentShareFn", input: { shareId: "shr_fixed" } },
      { fn: "getAttachmentShares", input: { id: S.attachment } },
      { fn: "deleteAttachmentFn", input: { id: S.attachment } },
      { fn: "deleteAttachmentFn", input: { id: S.attachment } },
      { fn: "revokeAttachmentShareFn", input: {} },
      { fn: "getAttachments" },
    ],
    rows: ["SELECT id, chat_id, title FROM saved_attachments ORDER BY rowid", "SELECT * FROM attachment_shares ORDER BY rowid"],
  },
]

type StepOut = { fn: string; input?: string; rt?: RtSpec; result?: string; error?: string; validation?: boolean; calls: unknown[][] } | { sql: string }

const dir = mkdtempSync(path.join(tmpdir(), "palui-rpc-people-"))
const out: { now: number; appUrl: string; cases: { name: string; steps: StepOut[]; rows: { sql: string; rows: string }[] }[] } = { now: NOW, appUrl: APP_URL, cases: [] }

for (const [n, c] of cases.entries()) {
  const file = path.join(dir, `case-${n}.db`)
  copyFileSync(path.join(testdata, "seeded.db"), file)
  const db = openDb(file)
  stub.scratch.db = db
  const steps: StepOut[] = []
  for (const step of c.steps) {
    if ("sql" in step) {
      db.$client.exec(step.sql)
      steps.push({ sql: step.sql })
      continue
    }
    const fn = fns[step.fn]
    if (!fn) throw new Error(`no server function ${step.fn}`)
    const calls: unknown[][] = []
    setRuntime(fakeRuntime(step.rt ?? {}, calls))
    const rec: StepOut = { fn: step.fn, calls }
    if (step.input !== undefined) rec.input = JSON.stringify(step.input)
    if (step.rt) rec.rt = step.rt
    try {
      rec.result = JSON.stringify(await fn(step.input))
    } catch (err) {
      rec.error = (err as Error).message
      if (err instanceof stub.ValidationError) rec.validation = true
    }
    setRuntime(undefined)
    steps.push(rec)
  }
  const rows = (c.rows ?? []).map((sql) => ({ sql, rows: JSON.stringify(db.$client.prepare(sql).all()) }))
  db.$client.close()
  out.cases.push({ name: c.name, steps, rows })
}
rmSync(dir, { recursive: true, force: true })

writeFileSync(path.join(testdata, "rpc-people.json"), JSON.stringify(out, null, 2) + "\n")
const total = out.cases.reduce((n, c) => n + c.steps.filter((s) => "fn" in s).length, 0)
console.log(`rpc-people.json: ${out.cases.length} cases, ${total} calls`)
