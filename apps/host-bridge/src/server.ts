// The bridge's HTTP server: GET /health without auth, and a stateless
// Streamable HTTP MCP endpoint at /mcp behind a bearer token. Each allowlisted
// command becomes one MCP tool with annotations matching its class.
import { createHash, timingSafeEqual } from "node:crypto"
import http from "node:http"
import type { AddressInfo } from "node:net"
import { homedir } from "node:os"
import { isAbsolute, join, resolve } from "node:path"
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js"
import { createLogger, type Logger } from "./log.ts"
import { z } from "zod"
import { ArgError, buildArgv, SHELL_TOOL, type BridgeConfig, type CommandSpec, type DangerousSpec, type ParamSpec } from "./config.ts"
import { formatResult, runCommand } from "./run.ts"

export type Bridge = {
  url: string
  close(): Promise<void>
}

type ToolDef = {
  name: string
  config: { title: string; description: string; inputSchema: Record<string, z.ZodType>; annotations: { readOnlyHint: boolean; destructiveHint: boolean } }
  run: (input: Record<string, unknown>) => Promise<{ content: { type: "text"; text: string }[]; isError: boolean }>
}

export async function startBridge(config: BridgeConfig, log: Logger = createLogger({ svc: "host-bridge" })): Promise<Bridge> {
  const expected = digest(`Bearer ${config.token}`)
  // Built once. Only the McpServer is per request: the SDK binds one to one transport.
  const tools = config.commands.map((spec) => toolDef(spec, log))
  if (config.dangerouslyAllowAnyCommand) tools.push(shellToolDef(config.dangerouslyAllowAnyCommand, log))
  const server = http.createServer(async (req, res) => {
    const pathname = new URL(req.url ?? "/", "http://x").pathname
    if (pathname === "/health" && req.method === "GET") {
      res.writeHead(200, { "content-type": "text/plain" }).end("ok")
      return
    }
    if (pathname !== "/mcp") {
      res.writeHead(404).end()
      return
    }
    if (!timingSafeEqual(digest(req.headers.authorization ?? ""), expected)) {
      log.warn("unauthorized", { status: 401, from: req.socket.remoteAddress })
      res.writeHead(401, { "content-type": "application/json" })
      res.end(JSON.stringify({ jsonrpc: "2.0", error: { code: -32001, message: "missing or invalid token" }, id: null }))
      return
    }
    const mcp = new McpServer({ name: "palui-host-bridge", version: "1.0.0" })
    for (const t of tools) mcp.registerTool(t.name, t.config, t.run)
    // The transport reads and parses the body itself (1 MB cap by default).
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true })
    try {
      await mcp.connect(transport)
      await transport.handleRequest(req, res)
    } catch (err) {
      log.error("request failed", { err })
      if (!res.headersSent) res.writeHead(500).end()
    } finally {
      void mcp.close()
    }
  })
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject)
    server.listen(config.port, config.host, () => resolve())
  })
  const { port } = server.address() as AddressInfo
  return {
    url: `http://${config.host}:${port}/mcp`,
    close: () =>
      new Promise<void>((r) => {
        server.closeAllConnections()
        server.close(() => r())
      }),
  }
}

function toolDef(spec: CommandSpec, log: Logger): ToolDef {
  return {
    name: spec.name,
    config: {
      title: spec.title ?? `Host: ${spec.name}`,
      description: spec.description,
      inputSchema: inputShape(spec),
      annotations: annotationsFor(spec.class),
    },
    async run(input) {
      let argv: string[]
      try {
        argv = buildArgv(spec, input ?? {})
      } catch (err) {
        if (err instanceof ArgError) return { content: [{ type: "text", text: err.message }], isError: true }
        throw err
      }
      const r = await runCommand(spec, argv)
      log.info("tool call", { tool: spec.name, argv, exit: r.exitCode, timedOut: r.timedOut, ms: r.durationMs })
      return { content: [{ type: "text", text: formatResult(r, spec.timeoutMs) }], isError: !r.ok }
    },
  }
}

// dangerouslyAllowAnyCommand: any command line, through a login shell. No param
// checks on purpose; the owner's approval in Palui is the only gate.
function shellToolDef(d: DangerousSpec, log: Logger): ToolDef {
  const base: CommandSpec = {
    name: SHELL_TOOL,
    description: "",
    class: "destructive",
    command: d.shell,
    args: [],
    params: {},
    cwd: d.cwd,
    env: {},
    timeoutMs: d.timeoutMs,
    maxOutputBytes: d.maxOutputBytes,
    okExitCodes: [0],
  }
  return {
    name: SHELL_TOOL,
    config: {
      title: "Run any shell command on the host",
      description: `Run a command line on the host with ${d.shell} -lc, as the owner's user, with full access. Pipes, redirects and multi-line scripts work. Default working directory ${d.cwd}. Times out after ${Math.round(d.timeoutMs / 1000)} s. Prefer a dedicated tool when one fits.`,
      inputSchema: {
        command: z.string().min(1).describe("The command line to run, e.g. `ls -la ~/Downloads | head`"),
        cwd: z.string().min(1).optional().describe("Working directory. Absolute, ~/..., or relative to the default"),
      },
      annotations: annotationsFor("destructive"),
    },
    async run(input) {
      const command = String(input.command ?? "")
      const cwd = typeof input.cwd === "string" ? resolveCwd(d.cwd, input.cwd) : d.cwd
      const r = await runCommand({ ...base, cwd }, ["-lc", command])
      log.warn("shell call", { command, cwd, exit: r.exitCode, timedOut: r.timedOut, ms: r.durationMs })
      return { content: [{ type: "text", text: formatResult(r, d.timeoutMs) }], isError: !r.ok }
    },
  }
}

function resolveCwd(base: string, cwd: string): string {
  if (cwd === "~" || cwd.startsWith("~/")) return join(homedir(), cwd.slice(1))
  return isAbsolute(cwd) ? cwd : resolve(base, cwd)
}

function annotationsFor(cls: CommandSpec["class"]): { readOnlyHint: boolean; destructiveHint: boolean } {
  if (cls === "read") return { readOnlyHint: true, destructiveHint: false }
  if (cls === "write") return { readOnlyHint: false, destructiveHint: false }
  return { readOnlyHint: false, destructiveHint: true }
}

// The JSON schema the model sees. buildArgv re-checks everything, since this is
// only as strict as JSON schema can say.
function inputShape(spec: CommandSpec): Record<string, z.ZodType> {
  const shape: Record<string, z.ZodType> = {}
  for (const [name, p] of Object.entries(spec.params)) {
    const one = valueSchema(p)
    let s: z.ZodType = p.list ? z.array(one) : one
    if (p.description) s = s.describe(p.description)
    shape[name] = p.optional ? s.optional() : s
  }
  return shape
}

function valueSchema(p: ParamSpec): z.ZodType {
  if (p.enum) return z.enum(p.enum as [string, ...string[]])
  return z.string().regex(p.pattern!)
}

function digest(s: string): Buffer {
  return createHash("sha256").update(s).digest()
}
