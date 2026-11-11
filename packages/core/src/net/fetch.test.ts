import http from "node:http"
import type { AddressInfo } from "node:net"
import zlib from "node:zlib"
import { afterAll, beforeAll, describe, expect, it } from "vitest"
import { httpTransport, safeFetch, type Transport, type TransportRequest } from "./fetch"
import type { Resolver } from "./ssrf"

const PUBLIC = "93.184.216.34"

const resolver =
  (map: Record<string, string[]>): Resolver =>
  async (h) => {
    const rows = map[h]
    if (!rows) throw new Error("ENOTFOUND")
    return rows.map((address) => ({ address, family: address.includes(":") ? 6 : 4 }))
  }

type Route = { status?: number; headers?: Record<string, string>; body?: string | Uint8Array; chunks?: Uint8Array[] }

// A fake transport keyed by URL. Records every request it saw.
function fakeTransport(routes: Record<string, Route>) {
  const seen: TransportRequest[] = []
  const transport: Transport = async (req) => {
    seen.push(req)
    const r = routes[req.url.href]
    if (!r) return { status: 404, headers: new Headers(), body: (async function* () {})(), cancel() {} }
    const bodyChunks = r.chunks ?? [typeof r.body === "string" ? new TextEncoder().encode(r.body) : (r.body ?? new Uint8Array())]
    let cancelled = false
    return {
      status: r.status ?? 200,
      headers: new Headers(r.headers ?? { "content-type": "text/plain" }),
      body: (async function* () {
        for (const c of bodyChunks) {
          if (cancelled) return
          yield c
        }
      })(),
      cancel() {
        cancelled = true
      },
    }
  }
  return { transport, seen }
}

describe("safeFetch", () => {
  it("fetches a public URL pinned to the resolved address", async () => {
    const { transport, seen } = fakeTransport({ "https://example.com/": { body: "hello", headers: { "content-type": "text/plain; charset=utf-8" } } })
    const res = await safeFetch("https://example.com/", { transport, resolver: resolver({ "example.com": [PUBLIC] }) })
    expect(res.ok).toBe(true)
    expect(res.text()).toBe("hello")
    expect(res.contentType).toBe("text/plain")
    expect(res.charset).toBe("utf-8")
    expect(seen[0]?.address).toEqual({ address: PUBLIC, family: 4 })
    expect(seen[0]?.headers["user-agent"]).toMatch(/Palui/)
  })

  it("refuses hosts that resolve to internal addresses before connecting", async () => {
    const { transport, seen } = fakeTransport({})
    await expect(safeFetch("https://evil.test/", { transport, resolver: resolver({ "evil.test": ["169.254.169.254"] }) })).rejects.toMatchObject({
      code: "blocked_address",
    })
    expect(seen).toHaveLength(0)
  })

  it("refuses literal internal addresses and bad schemes", async () => {
    const { transport } = fakeTransport({})
    await expect(safeFetch("http://127.0.0.1:4891/", { transport })).rejects.toMatchObject({ code: "blocked_address" })
    await expect(safeFetch("file:///etc/passwd", { transport })).rejects.toMatchObject({ code: "blocked_protocol" })
  })

  it("follows redirects and re-checks every hop", async () => {
    const { transport, seen } = fakeTransport({
      "https://a.test/": { status: 302, headers: { location: "/b" } },
      "https://a.test/b": { status: 301, headers: { location: "https://c.test/final" } },
      "https://c.test/final": { body: "done" },
    })
    const res = await safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC], "c.test": ["1.1.1.1"] }) })
    expect(res.text()).toBe("done")
    expect(res.url).toBe("https://c.test/final")
    expect(res.redirects).toEqual(["https://a.test/", "https://a.test/b"])
    expect(seen.map((s) => s.address.address)).toEqual([PUBLIC, PUBLIC, "1.1.1.1"])
  })

  it("refuses a redirect into the internal network", async () => {
    const { transport, seen } = fakeTransport({
      "https://a.test/": { status: 302, headers: { location: "http://169.254.169.254/latest/meta-data/" } },
    })
    await expect(safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC] }) })).rejects.toMatchObject({ code: "blocked_address" })
    expect(seen).toHaveLength(1)
  })

  it("refuses a redirect to a name that rebinds to loopback", async () => {
    const { transport } = fakeTransport({ "https://a.test/": { status: 307, headers: { location: "https://rebind.test/" } } })
    await expect(
      safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC], "rebind.test": ["127.0.0.1"] }) }),
    ).rejects.toMatchObject({ code: "blocked_address" })
  })

  it("stops after too many redirects", async () => {
    const routes: Record<string, Route> = {}
    for (let i = 0; i < 10; i++) routes[`https://a.test/${i}`] = { status: 302, headers: { location: `/${i + 1}` } }
    const { transport } = fakeTransport(routes)
    await expect(safeFetch("https://a.test/0", { transport, resolver: resolver({ "a.test": [PUBLIC] }) })).rejects.toMatchObject({
      code: "too_many_redirects",
    })
  })

  it("drops credentials on cross-origin redirects", async () => {
    const { transport, seen } = fakeTransport({
      "https://a.test/": { status: 302, headers: { location: "https://b.test/" } },
      "https://b.test/": { body: "ok" },
    })
    await safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC], "b.test": [PUBLIC] }), headers: { Authorization: "Bearer x" } })
    expect(seen[0]?.headers.authorization).toBe("Bearer x")
    expect(seen[1]?.headers.authorization).toBeUndefined()
  })

  it("enforces the byte limit, or truncates when asked", async () => {
    const big = new Uint8Array(1000).fill(65)
    const { transport } = fakeTransport({ "https://a.test/": { chunks: [big, big, big] } })
    const opts = { transport, resolver: resolver({ "a.test": [PUBLIC] }), maxBytes: 1500 }
    await expect(safeFetch("https://a.test/", opts)).rejects.toMatchObject({ code: "too_large" })
    const res = await safeFetch("https://a.test/", { ...opts, truncate: true })
    expect(res.bytes.byteLength).toBe(1500)
    expect(res.truncated).toBe(true)
  })

  it("refuses a declared content-length over the limit without reading", async () => {
    const { transport } = fakeTransport({ "https://a.test/": { headers: { "content-length": "999999999", "content-type": "text/plain" }, body: "x" } })
    await expect(safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC] }), maxBytes: 10 })).rejects.toMatchObject({
      code: "too_large",
    })
  })

  it("checks the content type allowlist", async () => {
    const { transport } = fakeTransport({
      "https://a.test/pdf": { headers: { "content-type": "application/pdf" }, body: "%PDF" },
      "https://a.test/json": { headers: { "content-type": "application/ld+json" }, body: "{}" },
    })
    const opts = { transport, resolver: resolver({ "a.test": [PUBLIC] }), allowedContentTypes: ["text/*", "application/json", "+json"] }
    await expect(safeFetch("https://a.test/pdf", opts)).rejects.toMatchObject({ code: "content_type" })
    await expect(safeFetch("https://a.test/json", opts)).resolves.toMatchObject({ contentType: "application/ld+json" })
  })

  it("times out", async () => {
    const transport: Transport = (req) =>
      new Promise((_, reject) => {
        req.signal.addEventListener("abort", () => reject(new Error("aborted")))
      })
    await expect(safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC] }), timeoutMs: 30 })).rejects.toMatchObject({
      code: "timeout",
    })
  })

  it("returns non-2xx answers instead of throwing", async () => {
    const { transport } = fakeTransport({ "https://a.test/": { status: 500, body: "boom" } })
    const res = await safeFetch("https://a.test/", { transport, resolver: resolver({ "a.test": [PUBLIC] }) })
    expect(res.ok).toBe(false)
    expect(res.status).toBe(500)
  })
})

// The real transport against a loopback server: proves the socket goes to the
// pinned address no matter what the hostname would resolve to.
describe("httpTransport", () => {
  let server: http.Server
  let port = 0
  const hosts: string[] = []

  beforeAll(async () => {
    server = http.createServer((req, res) => {
      hosts.push(req.headers.host ?? "")
      if (req.url === "/gz") {
        res.writeHead(200, { "content-type": "text/plain", "content-encoding": "gzip" })
        res.end(zlib.gzipSync("compressed hello"))
        return
      }
      res.writeHead(200, { "content-type": "text/plain" })
      res.end("pinned")
    })
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r))
    port = (server.address() as AddressInfo).port
  })
  afterAll(() => new Promise<void>((r) => server.close(() => r())))

  async function read(url: string) {
    const res = await httpTransport({
      url: new URL(url),
      address: { address: "127.0.0.1", family: 4 },
      method: "GET",
      headers: {},
      signal: AbortSignal.timeout(5000),
    })
    let s = ""
    for await (const c of res.body) s += Buffer.from(c).toString()
    return { status: res.status, text: s }
  }

  it("connects to the pinned address, not DNS", async () => {
    const out = await read(`http://does-not-exist.invalid:${port}/`)
    expect(out).toEqual({ status: 200, text: "pinned" })
    expect(hosts.at(-1)).toBe(`does-not-exist.invalid:${port}`)
  })

  it("decompresses gzip bodies", async () => {
    expect((await read(`http://x.invalid:${port}/gz`)).text).toBe("compressed hello")
  })
})
