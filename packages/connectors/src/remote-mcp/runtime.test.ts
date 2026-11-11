import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { afterEach, describe, expect, it, vi } from "vitest"
import { z } from "zod"
import { memoryDb, type DB } from "@palui/db"
import type { PaluiTool } from "@palui/core/connectors/define"
import { getConnector, upsertConnector } from "@palui/core/connectors/store"
import type { Resolver } from "@palui/core/net/ssrf"
import mcpManifest from "../mcp/manifest"
import { startMcpServer, type TestMcpServer } from "../mcp/test-helpers"
import { checkServerUrl, remoteMcpTools } from "./runtime"

const KEY = "test-master-key-test-master-key-0000"
const PUBLIC: Resolver = async () => [{ address: "93.184.216.34", family: 4 }]
const PRIVATE: Resolver = async () => [{ address: "10.0.0.5", family: 4 }]

function register(server: McpServer) {
  server.registerTool("jadwal", { description: "Prayer times", inputSchema: { kota: z.string() }, annotations: { readOnlyHint: true } }, async ({ kota }) => ({
    content: [{ type: "text", text: `subuh ${kota}` }],
  }))
  server.registerTool("mystery", { description: "No hints" }, async () => ({ content: [{ type: "text", text: "?" }] }))
}

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

// Sends https://sholat.example.com/... to the local test server.
function toLocal(srv: TestMcpServer): typeof fetch {
  return (input, init) => {
    const raw = input instanceof Request ? input.url : String(input)
    return fetch(raw.replace("https://sholat.example.com/mcp", srv.url), init)
  }
}

describe("checkServerUrl", () => {
  it("accepts public https only", async () => {
    await expect(checkServerUrl("https://sholat.example.com/mcp", PUBLIC)).resolves.toBeInstanceOf(URL)
    await expect(checkServerUrl("http://sholat.example.com/mcp", PUBLIC)).rejects.toThrow(/https/)
    await expect(checkServerUrl("https://127.0.0.1/mcp", PUBLIC)).rejects.toThrow(/loopback|internal|private/i)
    await expect(checkServerUrl("https://localhost/mcp", PUBLIC)).rejects.toThrow(/internal/)
    await expect(checkServerUrl("https://host.docker.internal/mcp", PUBLIC)).rejects.toThrow()
    await expect(checkServerUrl("https://sneaky.example.com/mcp", PRIVATE)).rejects.toThrow(/10\.0\.0\.5/)
  })
})

describe("remote-mcp tools", () => {
  let srv: TestMcpServer | undefined
  let db: DB
  afterEach(async () => {
    await srv?.close()
    srv = undefined
  })

  it("adds, lists and removes a server added from chat", async () => {
    db = memoryDb()
    srv = await startMcpServer({ register })
    const reconcile = vi.fn()
    const tools = remoteMcpTools({ db, masterKey: KEY, reconcile, resolver: PUBLIC, fetch: toLocal(srv), clock: () => 1000 })
    expect(tools.tambah_server_mcp!.class).toBe("destructive")
    expect(tools.hapus_server_mcp!.class).toBe("destructive")
    expect(tools.daftar_server_mcp!.class).toBe("read")

    const added = await call(tools.tambah_server_mcp, { name: "jadwal-sholat", url: "https://sholat.example.com/mcp" })
    expect(added.ok).toBe(true)
    expect(added.tools.map((t: { name: string; class: string }) => [t.name, t.class])).toEqual([
      ["mcp-jadwal-sholat__jadwal", "read"],
      ["mcp-jadwal-sholat__mystery", "destructive"],
    ])
    const row = getConnector(db, "mcp-jadwal-sholat")!
    expect(row).toMatchObject({ type: "mcp", trust: "external", enabled: true, status: "connecting", secrets: null })
    expect(row.config).toMatchObject({ url: "https://sholat.example.com/mcp", authMode: "none", addedFromChat: true })
    expect(reconcile).toHaveBeenCalledTimes(1)

    const again = await call(tools.tambah_server_mcp, { name: "jadwal-sholat", url: "https://sholat.example.com/mcp" })
    expect(again.ok).toBe(false)

    const list = await call(tools.daftar_server_mcp, {})
    expect(list.servers).toEqual([
      { id: "mcp-jadwal-sholat", name: "jadwal-sholat", url: "https://sholat.example.com/mcp", status: "connecting", auth: "none", toolsEnabled: 2, addedFromChat: true },
    ])

    expect((await call(tools.hapus_server_mcp, { name: "jadwal-sholat" })).ok).toBe(true)
    expect(getConnector(db, "mcp-jadwal-sholat")).toBeUndefined()
    expect(reconcile).toHaveBeenCalledTimes(2)
  })

  it("refuses private targets without connecting", async () => {
    db = memoryDb()
    const fetchSpy = vi.fn()
    const tools = remoteMcpTools({ db, masterKey: KEY, resolver: PRIVATE, fetch: fetchSpy as never })
    const r = await call(tools.tambah_server_mcp, { name: "evil", url: "https://evil.example.com/mcp" })
    expect(r.ok).toBe(false)
    expect(r.error).toContain("URL ditolak")
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(getConnector(db, "mcp-evil")).toBeUndefined()
  })

  it("won't remove a server set up in the dashboard, and never lists its token", async () => {
    db = memoryDb()
    upsertConnector(db, { id: "mcp-vorneo", manifest: mcpManifest, config: { url: "https://vorneo.id/api/mcp/?key=abc", authMode: "bearer" }, secrets: { token: "zkg_secret" } }, KEY)
    const tools = remoteMcpTools({ db, masterKey: KEY, resolver: PUBLIC })
    const list = await call(tools.daftar_server_mcp, {})
    expect(JSON.stringify(list)).not.toContain("zkg_secret")
    expect(list.servers[0].url).toBe("https://vorneo.id/api/mcp/")
    const r = await call(tools.hapus_server_mcp, { name: "mcp-vorneo" })
    expect(r.ok).toBe(false)
    expect(getConnector(db, "mcp-vorneo")).toBeDefined()
  })
})
