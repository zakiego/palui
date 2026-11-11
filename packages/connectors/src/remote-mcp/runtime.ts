// Add, list and remove MCP server connectors from chat. A server added here is an
// ordinary `mcp` connector row (id mcp-<name>, external trust, no auth) with
// `addedFromChat` set, so it shows up on the Connectors page like any other and
// its runtime only ever talks to public addresses.
import { tool } from "ai"
import { z } from "zod"
import type { DB } from "@palui/db"
import { config as loadConfig, masterKey } from "@palui/core/config"
import { defineRuntime, type PaluiTool } from "@palui/core/connectors/define"
import { connectConnector, deleteConnector, getConnector, listConnectors, upsertConnector, type ConnectorRow } from "@palui/core/connectors/store"
import { logEvent } from "@palui/core/events"
import { assertPublicUrl, hostOf, publicOnlyFetch, resolvePublic, SsrfError, systemResolver, type Resolver } from "@palui/core/net/ssrf"
import { McpConnection, suggestClass } from "../mcp/client"
import mcpManifest from "../mcp/manifest"
import manifest from "./manifest"

// Server names become the connector id suffix: mcp-<name>.
export const SERVER_NAME = /^[a-z0-9][a-z0-9-]{0,39}$/
const PROBE_TIMEOUT_MS = 15_000

export type RemoteMcpDeps = {
  db: DB
  masterKey: string
  // Pokes the supervisor so the new server starts now, not on the next tick.
  reconcile?: () => void
  // Injected in tests.
  resolver?: Resolver
  fetch?: typeof fetch
  clock?: () => number
}

type McpConfig = { url?: string; authMode?: string; toolPrefs?: Record<string, { enabled: boolean }>; addedFromChat?: boolean }

// The URL without its query or fragment, which can carry a key.
function shownUrl(raw: string | undefined): string | null {
  if (!raw) return null
  try {
    const u = new URL(raw)
    return `${u.origin}${u.pathname}`
  } catch {
    return null
  }
}

function serverView(row: ConnectorRow) {
  const c = (row.config ?? {}) as McpConfig
  const prefs = Object.values(c.toolPrefs ?? {})
  return {
    id: row.id,
    name: row.label,
    url: shownUrl(c.url),
    status: row.status,
    auth: c.authMode ?? "bearer",
    toolsEnabled: prefs.filter((p) => p.enabled).length,
    addedFromChat: c.addedFromChat === true,
  }
}

function idFor(nameOrId: string): string {
  const v = nameOrId.trim().toLowerCase()
  return v.startsWith("mcp-") ? v : `mcp-${v}`
}

// Checks the URL is https and public, before anything connects to it.
export async function checkServerUrl(raw: string, resolver: Resolver = systemResolver): Promise<URL> {
  const url = assertPublicUrl(raw)
  if (url.protocol !== "https:") throw new SsrfError("blocked_protocol", "Only https URLs can be added from chat")
  await resolvePublic(hostOf(url), resolver)
  return url
}

export function remoteMcpTools(deps: RemoteMcpDeps): Record<string, PaluiTool> {
  const { db } = deps
  const clock = deps.clock ?? (() => Date.now())
  return {
    tambah_server_mcp: {
      class: "destructive",
      title: "Add an MCP server",
      tool: tool({
        description:
          "Add a public remote MCP server (Streamable HTTP, no auth) as a connector, only when the owner asks for it with the URL. Checks the URL, connects once to list its tools, then saves it. The tools are usable from the owner's next message. Servers that need a token: tell the owner to add them on the Connectors page instead.",
        inputSchema: z.object({
          name: z.string().regex(SERVER_NAME, "lowercase letters, digits and dashes").describe("Short name, e.g. jadwal-sholat. The connector id becomes mcp-<name>."),
          url: z.string().max(500).describe("The server's Streamable HTTP endpoint, https only, e.g. https://example.com/mcp."),
        }),
        execute: async ({ name, url: rawUrl }) => {
          const id = `mcp-${name}`
          if (getConnector(db, id)) return { ok: false as const, error: `Udah ada konektor ${id}. Pakai nama lain, atau hapus yang lama dulu.` }
          let url: URL
          try {
            url = await checkServerUrl(rawUrl, deps.resolver)
          } catch (e) {
            if (e instanceof SsrfError) return { ok: false as const, error: `URL ditolak: ${e.message}` }
            throw e
          }
          const conn = new McpConnection({ url: url.href, headers: {}, fetch: publicOnlyFetch(deps.fetch ?? fetch, deps.resolver) })
          let defs
          try {
            const listed = conn.listTools({ fresh: true })
            const timeout = new Promise<never>((_, reject) => setTimeout(() => reject(new Error("no answer within 15 s")), PROBE_TIMEOUT_MS).unref())
            defs = (await Promise.race([listed, timeout])).tools
          } catch (e) {
            return { ok: false as const, error: `Nggak bisa konek ke server MCP itu: ${(e as Error)?.message ?? e}. Kalau servernya butuh token, tambahin dari halaman Connectors.` }
          } finally {
            await conn.close()
          }
          // Same prefill as the dashboard's tool picker. A server mislabelling its
          // own tools only hurts itself: results are external, so they taint the turn.
          const toolPrefs = Object.fromEntries(defs.map((d) => [d.name, { enabled: true, class: suggestClass(d.annotations) }]))
          const now = clock()
          upsertConnector(
            db,
            {
              id,
              manifest: mcpManifest,
              label: name,
              trust: "external",
              enabled: true,
              config: { url: url.href, authMode: "none", toolPrefs, addedFromChat: true },
            },
            deps.masterKey,
            now,
          )
          connectConnector(db, id, now)
          logEvent(db, { connectorId: id, kind: "mcp.added", message: `MCP server ${name} added from chat`, data: { url: shownUrl(url.href), tools: defs.length } }, now)
          deps.reconcile?.()
          return {
            ok: true as const,
            id,
            message: `Server ${name} ditambahin dengan ${defs.length} tool. Bisa dipakai mulai pesan berikutnya. Atur tool-nya di halaman Connectors.`,
            tools: defs.map((d) => ({ name: `${id}__${d.name}`, class: toolPrefs[d.name]!.class, description: (d.description ?? "").slice(0, 200) })),
          }
        },
      }),
    },

    daftar_server_mcp: {
      class: "read",
      title: "List MCP servers",
      tool: tool({
        description: "List the MCP server connectors with their status and how many tools are on. Never shows tokens.",
        inputSchema: z.object({}),
        execute: async () => {
          const rows = listConnectors(db).filter((r) => r.type === "mcp")
          return { ok: true as const, count: rows.length, servers: rows.map(serverView), message: rows.length ? undefined : "Belum ada server MCP." }
        },
      }),
    },

    hapus_server_mcp: {
      class: "destructive",
      title: "Remove an MCP server",
      tool: tool({
        description: "Remove an MCP server that was added from chat, by name or id (from daftar_server_mcp). Servers set up on the Connectors page are removed there.",
        inputSchema: z.object({ name: z.string().min(1).max(60).describe("Server name or connector id, e.g. jadwal-sholat or mcp-jadwal-sholat.") }),
        execute: async ({ name }) => {
          const id = idFor(name)
          const row = getConnector(db, id)
          if (!row || row.type !== "mcp") return { ok: false as const, error: `Nggak ada server MCP ${id}. Panggil daftar_server_mcp untuk lihat id-nya.` }
          if ((row.config as McpConfig)?.addedFromChat !== true) return { ok: false as const, error: `${row.label} dipasang dari dashboard. Hapus dari halaman Connectors ya.` }
          deleteConnector(db, id, clock())
          deps.reconcile?.()
          return { ok: true as const, message: `Server ${row.label} dihapus.` }
        },
      }),
    },
  }
}

export default defineRuntime(manifest, {
  test: async (ctx) => {
    const n = listConnectors(ctx.db).filter((r) => r.type === "mcp").length
    return { ok: true, detail: n ? `${n} MCP server${n === 1 ? "" : "s"}` : "Ready, no MCP servers yet" }
  },
  start: async (ctx) => {
    const deps: RemoteMcpDeps = { db: ctx.db, masterKey: masterKey(loadConfig()), reconcile: () => ctx.services.reconcile?.() }
    return {
      stop: async () => {},
      tools: async () => remoteMcpTools(deps),
    }
  },
})
