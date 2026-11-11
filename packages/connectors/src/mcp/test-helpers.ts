// Shared test fixtures for connector tests: a fake ConnectorContext and a local
// MCP server (Streamable HTTP, stateless) on an ephemeral port.
import http from "node:http"
import type { AddressInfo } from "node:net"
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js"
import { memoryDb, type Trust } from "@palui/db"
import { silentLogger } from "@palui/core/log"
import type { ConnectorContext } from "@palui/core/saklar/define"

export type FakeCtx<C, S> = ConnectorContext<C, S> & {
  statuses: { status: string; detail?: string }[]
  patches: Record<string, unknown>[]
  events: { kind: string; message: string }[]
}

export function fakeCtx<C, S>(config: C, secrets: S, opts: { id?: string; type?: string; trust?: Trust } = {}): FakeCtx<C, S> {
  const statuses: { status: string; detail?: string }[] = []
  const patches: Record<string, unknown>[] = []
  const events: { kind: string; message: string }[] = []
  return {
    id: opts.id ?? "test",
    type: opts.type ?? opts.id ?? "test",
    config,
    secrets,
    trust: opts.trust ?? "own",
    db: memoryDb(),
    log: silentLogger,
    dataDir: "/tmp/palui-test",
    signal: new AbortController().signal,
    services: {
      inbound: async () => {},
      claimOwner: async () => false,
      isOwner: () => false,
    },
    setStatus: (status, detail) => statuses.push({ status, detail }),
    event: (kind, message) => events.push({ kind, message }),
    patchConfig: (p) => patches.push(p),
    statuses,
    patches,
    events,
  }
}

export type TestMcpServer = {
  url: string
  // Every request's Authorization header, in order.
  auths: (string | undefined)[]
  close(): Promise<void>
}

// Starts an MCP server on 127.0.0.1:0 that serves `path` (default /mcp). When
// `token` is set, requests without `Authorization: Bearer <token>` get a 401.
export async function startMcpServer(opts: {
  register: (server: McpServer) => void
  token?: string
  path?: string
  // Fixed port (to restart a server on the same URL). Default: ephemeral.
  port?: number
  // Extra routes for a fake REST API next to /mcp. Return true when handled.
  rest?: (req: http.IncomingMessage, res: http.ServerResponse, body: string) => boolean | Promise<boolean>
}): Promise<TestMcpServer> {
  const path = opts.path ?? "/mcp"
  const auths: (string | undefined)[] = []
  const server = http.createServer(async (req, res) => {
    const chunks: Buffer[] = []
    for await (const c of req) chunks.push(c as Buffer)
    const raw = Buffer.concat(chunks).toString("utf8")
    const pathname = new URL(req.url ?? "/", "http://x").pathname
    if (pathname !== path) {
      if (opts.rest && (await opts.rest(req, res, raw))) return
      res.writeHead(404).end()
      return
    }
    auths.push(req.headers.authorization)
    if (opts.token && req.headers.authorization !== `Bearer ${opts.token}`) {
      res.writeHead(401, { "content-type": "application/json" })
      res.end(JSON.stringify({ jsonrpc: "2.0", error: { code: -32001, message: "missing or invalid API key" }, id: null }))
      return
    }
    const mcp = new McpServer({ name: "test", version: "1.0.0" })
    opts.register(mcp)
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true })
    await mcp.connect(transport)
    try {
      await transport.handleRequest(req, res, raw ? JSON.parse(raw) : undefined)
    } finally {
      void mcp.close()
    }
  })
  await new Promise<void>((r) => server.listen(opts.port ?? 0, "127.0.0.1", r))
  const { port } = server.address() as AddressInfo
  return {
    url: `http://127.0.0.1:${port}${path}`,
    auths,
    close: () =>
      new Promise<void>((r) => {
        server.closeAllConnections()
        server.close(() => r())
      }),
  }
}
