import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client"
import { createLogger } from "./log.ts"
import { afterEach, describe, expect, it } from "vitest"
import { parseConfig } from "./config.ts"
import { startBridge, type Bridge } from "./server.ts"

const TOKEN = "t".repeat(40)

const config = parseConfig({
  token: TOKEN,
  port: 1, // replaced below
  commands: [
    { name: "say", title: "Say hi on the host", description: "Echo a word", class: "read", command: "/bin/echo", args: ["hi", "{word}"], params: { word: { pattern: "[a-z]+" } } },
    { name: "fail", description: "Exit 3", class: "write", command: "/bin/sh", args: ["-c", "echo nope >&2; exit 3"] },
    { name: "slow", description: "Sleep", class: "destructive", command: "/bin/sleep", args: ["5"], timeoutMs: 200 },
    { name: "flood", description: "Lots of output", class: "read", command: "/bin/sh", args: ["-c", "seq 1 20000"], maxOutputBytes: 1024 },
    { name: "nomatch", description: "grep with no match", class: "read", command: "/usr/bin/grep", args: ["zzz", "/dev/null"], okExitCodes: [0, 1] },
  ],
})

let bridge: Bridge | undefined
const logs: string[] = []
afterEach(async () => {
  await bridge?.close()
  bridge = undefined
  logs.length = 0
})

// legacy: the 2025 initialize handshake (Claude Code, mcporter, Palui's Go
// client against older servers). modern: spec 2026-07-28, no handshake.
type Era = "legacy" | "modern"

async function connect(token = TOKEN, era: Era = "legacy"): Promise<Client> {
  bridge ??= await startBridge({ ...config, port: 0 }, createLogger({}, (l) => logs.push(l)))
  const client = new Client(
    { name: "test", version: "1.0.0" },
    era === "modern" ? { versionNegotiation: { mode: { pin: "2026-07-28" } } } : undefined,
  )
  await client.connect(new StreamableHTTPClientTransport(new URL(bridge.url), { requestInit: { headers: { authorization: `Bearer ${token}` } } }))
  return client
}

function text(r: Awaited<ReturnType<Client["callTool"]>>): string {
  return (r.content as { type: string; text: string }[]).map((c) => c.text).join("")
}

describe("host bridge", () => {
  it("serves /health without a token", async () => {
    bridge = await startBridge({ ...config, port: 0 }, createLogger({}, () => {}))
    const res = await fetch(bridge.url.replace("/mcp", "/health"))
    expect(res.status).toBe(200)
  })

  it("refuses a wrong token", async () => {
    await expect(connect("wrong")).rejects.toThrow()
    expect(logs.some((l) => l.includes('"status":401'))).toBe(true)
  })

  it("lists commands as tools with class annotations", async () => {
    const client = await connect()
    const { tools } = await client.listTools()
    const byName = Object.fromEntries(tools.map((t) => [t.name, t]))
    expect(Object.keys(byName).sort()).toEqual(["fail", "flood", "nomatch", "say", "slow"])
    expect(byName.say!.title).toBe("Say hi on the host")
    expect(byName.fail!.title).toBe("Host: fail")
    expect(byName.say!.annotations).toMatchObject({ readOnlyHint: true })
    expect(byName.fail!.annotations).toMatchObject({ readOnlyHint: false, destructiveHint: false })
    expect(byName.slow!.annotations).toMatchObject({ destructiveHint: true })
    expect(byName.say!.inputSchema.required).toEqual(["word"])
  })

  it("runs a command and returns its output", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "say", arguments: { word: "palui" } })
    expect(r.isError).toBe(false)
    expect(text(r)).toBe("exit 0\n--- stdout ---\nhi palui\n")
    expect(JSON.parse(logs.at(-1)!)).toMatchObject({ msg: "tool call", tool: "say", argv: ["hi", "palui"], exit: 0 })
  })

  it("rejects input that doesn't match the param", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "say", arguments: { word: "$(id)" } })
    expect(r.isError).toBe(true)
    expect(logs.some((l) => l.includes('"tool":"say"'))).toBe(false)
  })

  it("reports a non-zero exit as an error with stderr", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "fail", arguments: {} })
    expect(r.isError).toBe(true)
    expect(text(r)).toBe("exit 3\n--- stderr ---\nnope\n")
  })

  it("kills a command that runs past its timeout", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "slow", arguments: {} })
    expect(r.isError).toBe(true)
    expect(text(r)).toMatch(/^timed out after 200 ms \(killed\)/)
  })

  it("keeps the start and the end of long output", async () => {
    const client = await connect()
    const t = text(await client.callTool({ name: "flood", arguments: {} }))
    expect(t).toMatch(/^exit 0\n--- stdout ---\n1\n2\n/)
    expect(t).toMatch(/\[\.\.\. stdout cut: showing the first and last 512 B of 106 KB \.\.\.\]/)
    expect(t).toMatch(/19999\n20000\n$/)
    expect(t.length).toBeLessThan(1200)
  })

  it("has no shell tool unless dangerouslyAllowAnyCommand is on", async () => {
    const client = await connect()
    const { tools } = await client.listTools()
    expect(tools.some((t) => t.name === "shell")).toBe(false)
  })

  it("runs any command line through a login shell when dangerouslyAllowAnyCommand is on", async () => {
    bridge = await startBridge(
      { ...config, port: 0, dangerouslyAllowAnyCommand: { shell: "/bin/sh", cwd: "/tmp", timeoutMs: 300, maxOutputBytes: 16_384 } },
      createLogger({}, (l) => logs.push(l)),
    )
    const client = await connect()
    const shell = (await client.listTools()).tools.find((t) => t.name === "shell")!
    expect(shell.annotations).toMatchObject({ readOnlyHint: false, destructiveHint: true })
    const r = await client.callTool({ name: "shell", arguments: { command: "echo $((1 + 2)) | tr 3 x\npwd" } })
    expect(r.isError).toBe(false)
    expect(text(r)).toMatch(/^exit 0\n--- stdout ---\nx\n(\/private)?\/tmp\n$/)
    const sub = await client.callTool({ name: "shell", arguments: { command: "pwd", cwd: "/" } })
    expect(text(sub)).toBe("exit 0\n--- stdout ---\n/\n")
    const slow = await client.callTool({ name: "shell", arguments: { command: "sleep 5" } })
    expect(text(slow)).toMatch(/^timed out after 300 ms/)
    expect(logs.some((l) => l.includes('"msg":"shell call"'))).toBe(true)
  })

  it("treats okExitCodes as success", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "nomatch", arguments: {} })
    expect(r.isError).toBe(false)
    expect(text(r)).toBe("exit 1\n(no output)")
  })
})

describe("host bridge over both protocol eras", () => {
  it.each(["legacy", "modern"] as const)("%s client lists and runs tools", async (era) => {
    const client = await connect(TOKEN, era)
    expect(client.getProtocolEra()).toBe(era)
    const { tools } = await client.listTools()
    expect(tools.map((t) => t.name).sort()).toEqual(["fail", "flood", "nomatch", "say", "slow"])
    const say = await client.callTool({ name: "say", arguments: { word: "palui" } })
    expect(say.isError).toBe(false)
    expect(text(say)).toBe("exit 0\n--- stdout ---\nhi palui\n")
    const fail = await client.callTool({ name: "fail", arguments: {} })
    expect(fail.isError).toBe(true)
  })

  it("gives a modern client the same tool list as a legacy one", async () => {
    const legacy = await (await connect(TOKEN, "legacy")).listTools()
    const modern = await (await connect(TOKEN, "modern")).listTools()
    expect(modern.tools).toEqual(legacy.tools)
  })

  it("answers server/discover with the 2026-07-28 revision", async () => {
    bridge ??= await startBridge({ ...config, port: 0 }, createLogger({}, (l) => logs.push(l)))
    const meta = {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": { name: "test", version: "1" },
      "io.modelcontextprotocol/clientCapabilities": {},
    }
    const res = await fetch(bridge.url, {
      method: "POST",
      headers: {
        authorization: `Bearer ${TOKEN}`,
        "content-type": "application/json",
        accept: "application/json, text/event-stream",
        "mcp-protocol-version": "2026-07-28",
        "mcp-method": "server/discover",
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "server/discover", params: { _meta: meta } }),
    })
    expect(res.status).toBe(200)
    const body = (await res.json()) as { result: { resultType: string; supportedVersions: string[]; capabilities: { tools?: object } } }
    expect(body.result.resultType).toBe("complete")
    expect(body.result.supportedVersions).toContain("2026-07-28")
    expect(body.result.capabilities.tools).toBeDefined()
  })

  it("refuses a wrong token from a modern client too", async () => {
    await expect(connect("wrong", "modern")).rejects.toThrow()
    expect(logs.some((l) => l.includes('"status":401'))).toBe(true)
  })
})
