import { Client } from "@modelcontextprotocol/sdk/client/index.js"
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js"
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

async function connect(token = TOKEN): Promise<Client> {
  bridge ??= await startBridge({ ...config, port: 0 }, (l) => logs.push(l))
  const client = new Client({ name: "test", version: "1.0.0" })
  await client.connect(new StreamableHTTPClientTransport(new URL(bridge.url), { requestInit: { headers: { authorization: `Bearer ${token}` } } }))
  return client
}

function text(r: Awaited<ReturnType<Client["callTool"]>>): string {
  return (r.content as { type: string; text: string }[]).map((c) => c.text).join("")
}

describe("host bridge", () => {
  it("serves /health without a token", async () => {
    bridge = await startBridge({ ...config, port: 0 })
    const res = await fetch(bridge.url.replace("/mcp", "/health"))
    expect(res.status).toBe(200)
  })

  it("refuses a wrong token", async () => {
    await expect(connect("wrong")).rejects.toThrow()
    expect(logs.some((l) => l.includes("401"))).toBe(true)
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
    expect(logs.at(-1)).toMatch(/say \["hi","palui"\] exit=0/)
  })

  it("rejects input that doesn't match the param", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "say", arguments: { word: "$(id)" } })
    expect(r.isError).toBe(true)
    expect(logs.some((l) => l.includes(" say "))).toBe(false)
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

  it("treats okExitCodes as success", async () => {
    const client = await connect()
    const r = await client.callTool({ name: "nomatch", arguments: {} })
    expect(r.isError).toBe(false)
    expect(text(r)).toBe("exit 1\n(no output)")
  })
})
