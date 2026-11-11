// The bridge's HTTP server: GET /health without auth, and a stateless
// Streamable HTTP MCP endpoint at /mcp behind a bearer token. Each allowlisted
// command becomes one MCP tool with annotations matching its class.
import { createHash, timingSafeEqual } from "node:crypto"
import http from "node:http"
import type { AddressInfo } from "node:net"
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js"
import { z } from "zod"
import { ArgError, buildArgv, type BridgeConfig, type CommandSpec, type ParamSpec } from "./config.ts"
import { formatResult, runCommand } from "./run.ts"

const MAX_BODY = 1_048_576

export type Logger = (line: string) => void

export type Bridge = {
  url: string
  close(): Promise<void>
}

export async function startBridge(config: BridgeConfig, log: Logger = (l) => console.log(l)): Promise<Bridge> {
  const expected = digest(`Bearer ${config.token}`)
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
      log(`${stamp()} 401 ${req.socket.remoteAddress ?? "?"}`)
      res.writeHead(401, { "content-type": "application/json" })
      res.end(JSON.stringify({ jsonrpc: "2.0", error: { code: -32001, message: "missing or invalid token" }, id: null }))
      return
    }
    let body: unknown
    try {
      body = await readJson(req)
    } catch (err) {
      res.writeHead(400, { "content-type": "application/json" })
      res.end(JSON.stringify({ jsonrpc: "2.0", error: { code: -32700, message: (err as Error).message }, id: null }))
      return
    }
    const mcp = buildMcp(config, log)
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true })
    try {
      await mcp.connect(transport)
      await transport.handleRequest(req, res, body)
    } catch (err) {
      log(`${stamp()} error ${(err as Error).message}`)
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

export function buildMcp(config: BridgeConfig, log: Logger): McpServer {
  const mcp = new McpServer({ name: "palui-host-bridge", version: "1.0.0" })
  for (const spec of config.commands) {
    mcp.registerTool(
      spec.name,
      {
        description: spec.description,
        inputSchema: inputShape(spec),
        annotations: annotationsFor(spec.class),
      },
      async (input: Record<string, unknown>) => {
        let argv: string[]
        try {
          argv = buildArgv(spec, input ?? {})
        } catch (err) {
          if (err instanceof ArgError) return { content: [{ type: "text" as const, text: err.message }], isError: true }
          throw err
        }
        const r = await runCommand(spec, argv)
        log(`${stamp()} ${spec.name} ${JSON.stringify(argv)} ${r.timedOut ? "timeout" : `exit=${r.exitCode}`} ${r.durationMs}ms`)
        return { content: [{ type: "text" as const, text: formatResult(r) }], isError: r.timedOut || r.exitCode !== 0 }
      },
    )
  }
  return mcp
}

export function annotationsFor(cls: CommandSpec["class"]): { readOnlyHint: boolean; destructiveHint: boolean } {
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
  return z.string().regex(new RegExp(`^(?:${p.pattern})$`))
}

function digest(s: string): Buffer {
  return createHash("sha256").update(s).digest()
}

async function readJson(req: http.IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = []
  let size = 0
  for await (const c of req) {
    size += (c as Buffer).length
    if (size > MAX_BODY) throw new Error("request body too large")
    chunks.push(c as Buffer)
  }
  const raw = Buffer.concat(chunks).toString("utf8")
  if (!raw) return undefined
  try {
    return JSON.parse(raw)
  } catch {
    throw new Error("request body isn't valid JSON")
  }
}

function stamp(): string {
  return new Date().toISOString()
}
