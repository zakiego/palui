// Exports M2 tool goldens from the TS code into apps/server/testdata/tools.json:
// namespaced() for awkward ids, the palui__search_tools meta-tool (description,
// JSON schema, and what its execute returns for several inputs), and the
// collected order, active and deferred names for a few budgets.
//
// Run from apps/app:
//   cd apps/app && pnpm tsx ../server/testdata/gen/tools.ts
import { writeFileSync } from "node:fs"
import { createRequire } from "node:module"
import path from "node:path"
import { collectTools, namespaced, SEARCH_TOOL } from "../../../../packages/core/src/tools/collect.ts"
import { searchTools, tokens } from "../../../../packages/core/src/tools/search.ts"
import { silentLogger } from "../../../../packages/core/src/log.ts"

const require = createRequire(path.resolve(import.meta.dirname, "../../../../packages/core/package.json"))
const ai = require("ai") as typeof import("ai")

const turn = { runId: "run_1", chatId: "chat_1", channel: "telegram", trigger: "message", tz: "Asia/Makassar", now: 1 }

type Def = { name: string; description: string; schema: Record<string, unknown>; class: string; untrusted?: boolean; title?: string }
function entry(id: string, kind: string, label: string, trust: string, defs: Def[]) {
  const tools: Record<string, unknown> = {}
  for (const d of defs)
    tools[d.name] = { tool: ai.tool({ description: d.description, inputSchema: ai.jsonSchema(d.schema as never), execute: async () => "ok" }), class: d.class, ...(d.untrusted ? { untrusted: true } : {}), ...(d.title ? { title: d.title } : {}) }
  return { id, row: { id, kind, label, trust, type: id, agentId: null }, handle: { stop: async () => {}, tools: async () => tools } }
}

const OBJ = { type: "object", properties: { q: { type: "string" } } }
const running = () => [
  entry("notes", "tools", "Notes", "own", [
    { name: "add", description: "Add a note", schema: OBJ, class: "write" },
    { name: "list", description: "List notes", schema: OBJ, class: "read", title: "List notes" },
    { name: "remove", description: "Remove a note", schema: OBJ, class: "destructive" },
  ]),
  entry("recall", "memory", "Recall", "own", [{ name: "search", description: "Search memory", schema: OBJ, class: "read" }]),
  entry("skills", "skill", "Skills", "own", [{ name: "read", description: "Read a skill", schema: OBJ, class: "read" }]),
  entry("mcp-jadwal-sholat", "tools", "jadwal-sholat", "external", [
    { name: "search_cities", description: "Search Indonesian cities by name", schema: OBJ, class: "read" },
    { name: "get_prayer_times", description: "Get the prayer times for a city and a date", schema: { type: "object", properties: { city: { type: "string" }, date: { type: "string" } }, required: ["city"] }, class: "read" },
  ]),
  entry("web", "tools", "Web", "own", [
    { name: "fetch", description: "Fetch a page", schema: OBJ, class: "read", untrusted: true },
    { name: "search", description: "Search the web", schema: OBJ, class: "bogus" },
  ]),
]

async function main() {
  const ids = ["notes", "mcp-jadwal.sholat", "MCP Ünïcode", "emoji😀id", "x".repeat(70), "a/b c", "ok-id_1"]
  const names = ids.flatMap((id) => ["add", "tool.name", "über"].map((t) => ({ id, tool: t, name: namespaced(id, t) })))

  const budgets = [64, 10, 4, 2, 1]
  const collections = []
  for (const maxTools of budgets) {
    for (const sticky of [null, ["web__fetch", "notes__remove"], ["missing__x", "mcp-jadwal-sholat__get_prayer_times"]]) {
      const c = await collectTools(running() as never, { turn: turn as never, maxTools, sticky, log: silentLogger, onUntrustedResult: () => {} })
      collections.push({ maxTools, sticky, tools: Object.keys(c.tools), active: c.active, deferred: c.deferred, connectorIds: c.connectorIds, meta: Object.values(c.meta).map((m) => ({ name: m.name, connectorId: m.connectorId, tool: m.tool, class: m.class, untrusted: m.untrusted, title: m.title ?? null, label: m.label ?? null, guestSafe: m.guestSafe ?? null })) })
    }
  }

  const c = await collectTools(running() as never, { turn: turn as never, maxTools: 3, log: silentLogger, onUntrustedResult: () => {} })
  const search = c.tools[SEARCH_TOOL]!
  const schema = await ai.asSchema(search.inputSchema as never).jsonSchema
  const inputs = [
    { query: "jadwal sholat" },
    { query: "prayer time" },
    { names: ["notes__remove", "fetch", "nope"] },
    { names: ["web__search"], query: "notes" },
    { query: "zzz" },
    { query: "remove", for: "flow" },
    { query: "" },
    {},
  ]
  const results = []
  for (const input of inputs) {
    const loaded: string[][] = []
    const withLoad = await collectTools(running() as never, { turn: turn as never, maxTools: 3, log: silentLogger, onUntrustedResult: () => {}, onLoad: (n) => loaded.push(n) })
    const out = await (withLoad.tools[SEARCH_TOOL]!.execute as (i: unknown, o: unknown) => Promise<unknown>)(input, { toolCallId: "t", messages: [] })
    results.push({ input, output: out, active: withLoad.active, onLoad: loaded })
  }

  const tokenCases = ["get_prayer_times", "Jadwal-Sholat", "Über café 2026 a b 😀 Ⅻ", "İstanbul ΣΑΣ", ""].map((s) => ({ input: s, tokens: tokens(s) }))

  writeFileSync(
    path.resolve(import.meta.dirname, "../tools.json"),
    `${JSON.stringify({ names, collections, search: { description: search.description, schema, results }, tokens: tokenCases, rank: searchTools([{ name: "a__x", connectorId: "a", connectorLabel: "A", tool: "x", description: "sholat sholatnya" }], "sholat", 5) }, null, 1)}\n`,
  )
  console.log("wrote testdata/tools.json")
}

await main()
