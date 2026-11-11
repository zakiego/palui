import type { ToolClass } from "@palui/db"
import { defineRuntime, type ConfigOf, type PaluiTool, type SecretsOf } from "@palui/core/connectors/define"
import { publicOnlyFetch } from "@palui/core/net/ssrf"
import { authHeaders, McpConnection, suggestClass, type McpEndpoint, type McpToolDef } from "./client"
import manifest from "./manifest"

type Config = ConfigOf<typeof manifest>
type Secrets = SecretsOf<typeof manifest>
type ToolPrefs = Config["toolPrefs"]

// One row of the listTools wizard step / action.
export type McpToolListing = {
  name: string
  title?: string
  description: string
  // From annotations: readOnlyHint -> read, destructiveHint: false -> write, else destructive.
  suggestedClass: ToolClass
  // Saved pref, or the prefill (enabled, suggestedClass) when nothing is saved yet.
  enabled: boolean
  class: ToolClass
  // Whether a pref is saved. Until it is, the runtime offers the tool as destructive.
  saved: boolean
  // Flow write steps: approved once per version, or asked every run.
  flowApproval: "version" | "every_run"
}

export function endpointFor(config: Config, secrets: Secrets, fetchImpl?: typeof fetch): McpEndpoint {
  if (config.authMode !== "none" && !secrets.token) throw new Error("This server needs a token")
  if (config.authMode === "header" && !config.headerName) throw new Error("Set the header name for the custom header auth mode")
  // A server added from chat never gets to reach the compose network or the host.
  const guarded = config.addedFromChat ? publicOnlyFetch(fetchImpl ?? fetch) : fetchImpl
  return { url: config.url, headers: authHeaders({ authMode: config.authMode, headerName: config.headerName, token: secrets.token }), fetch: guarded }
}

export function listingFor(defs: McpToolDef[], prefs: ToolPrefs = {}): McpToolListing[] {
  return defs.map((d) => {
    const suggested = suggestClass(d.annotations)
    const pref = prefs[d.name]
    return {
      name: d.name,
      ...(d.title || d.annotations?.title ? { title: d.title ?? d.annotations?.title } : {}),
      description: d.description ?? "",
      suggestedClass: suggested,
      enabled: pref?.enabled ?? true,
      class: pref?.class ?? suggested,
      saved: Boolean(pref),
      flowApproval: pref?.flowApproval ?? "version",
    }
  })
}

// The tools a turn may see: enabled ones only, with their saved class. A tool the
// owner hasn't classified yet is destructive (fail closed, PLAN 10).
export function selectTools(defs: McpToolDef[], prefs: ToolPrefs = {}): { def: McpToolDef; class: ToolClass; flowApproval: "version" | "every_run" }[] {
  const out: { def: McpToolDef; class: ToolClass; flowApproval: "version" | "every_run" }[] = []
  for (const def of defs) {
    const pref = prefs[def.name]
    if (pref && !pref.enabled) continue
    out.push({ def, class: pref?.class ?? "destructive", flowApproval: pref?.flowApproval ?? "version" })
  }
  return out
}

function errorText(err: unknown): string {
  const msg = (err as Error)?.message ?? String(err)
  if (/401|unauthori[sz]ed/i.test(msg)) return `The server rejected the token (401): ${msg}`
  return msg
}

export default defineRuntime(manifest, {
  async test(ctx) {
    let conn: McpConnection
    try {
      conn = new McpConnection(endpointFor(ctx.config, ctx.secrets))
    } catch (err) {
      return { ok: false, detail: (err as Error).message }
    }
    try {
      const { tools } = await conn.listTools({ fresh: true })
      const listing = listingFor(tools, ctx.config.toolPrefs)
      return { ok: true, detail: `${tools.length} tool${tools.length === 1 ? "" : "s"} available`, data: { tools: listing } }
    } catch (err) {
      return { ok: false, detail: errorText(err) }
    } finally {
      await conn.close()
    }
  },

  async start(ctx) {
    const conn = new McpConnection(endpointFor(ctx.config, ctx.secrets))
    let lastError: string | null = null
    let toolCount = 0
    try {
      toolCount = (await conn.listTools({ fresh: true })).tools.length
    } catch (err) {
      await conn.close()
      throw new Error(errorText(err))
    }
    const untrusted = ctx.trust === "external"

    return {
      async stop() {
        await conn.close()
      },
      async tools() {
        // A failed listing or connect marks the connector as errored (shown on the
        // Connectors page) instead of quietly offering no tools.
        let defs: McpToolDef[]
        let picked: ReturnType<typeof selectTools>
        let aiTools: Awaited<ReturnType<McpConnection["tools"]>>
        try {
          defs = (await conn.listTools()).tools
          picked = selectTools(defs, ctx.config.toolPrefs)
          aiTools = await conn.tools(picked.map((p) => p.def))
        } catch (err) {
          ctx.log.warn("mcp tool listing failed, offering no tools this turn", { connector_id: ctx.id, error: err })
          conn.reset()
          const detail = errorText(err)
          if (detail !== lastError) {
            lastError = detail
            ctx.setStatus("error", detail)
          }
          return {}
        }
        if (lastError) {
          lastError = null
          ctx.setStatus("on")
        }
        toolCount = defs.length
        const out: Record<string, PaluiTool> = {}
        for (const p of picked) {
          const t = aiTools[p.def.name]
          if (!t) continue
          out[p.def.name] = { tool: t, class: p.class, untrusted, flowApproval: p.flowApproval, title: p.def.title ?? p.def.annotations?.title ?? p.def.name }
        }
        return out
      },
      // Direct call for flow sources and polls: the tool must exist and not be turned off. The
      // class is the saved one, else the server's own hint (readOnlyHint -> read).
      async callTool(name, args) {
        const def = (await conn.listTools()).tools.find((d) => d.name === name) ?? (await conn.listTools({ fresh: true })).tools.find((d) => d.name === name)
        if (!def) throw new Error(`No tool ${name} on this server`)
        const pref = ctx.config.toolPrefs?.[name]
        if (pref && !pref.enabled) throw new Error(`Tool ${name} is turned off on the Connectors page`)
        const cls: ToolClass = pref?.class ?? suggestClass(def.annotations)
        if (cls !== "read") return { class: cls, output: null }
        return { class: cls, output: await conn.callTool(name, args) }
      },
      live() {
        return { tools: toolCount, url: ctx.config.url, error: lastError }
      },
      async action(name) {
        if (name === "listTools") {
          const { tools } = await conn.listTools({ fresh: true })
          toolCount = tools.length
          return { tools: listingFor(tools, ctx.config.toolPrefs) }
        }
        throw new Error(`Unknown action ${name}`)
      },
    }
  },
})
