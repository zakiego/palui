import { describe, expect, it } from "vitest"
import { SafeFetchError, type SafeResponse } from "@palui/core/net/fetch"
import type { TurnInfo } from "@palui/core/connectors/define"
import { fakeCtx } from "../mcp/test-helpers"
import runtime, { extractReadable, fetchPage, MAX_TEXT_CHARS, type Fetcher } from "./runtime"

function response(body: string, contentType: string, extra: Partial<SafeResponse> = {}): SafeResponse {
  const bytes = new TextEncoder().encode(body)
  return {
    url: extra.url ?? "https://example.com/post",
    status: extra.status ?? 200,
    ok: (extra.status ?? 200) < 300,
    headers: new Headers({ "content-type": contentType }),
    contentType,
    charset: "utf-8",
    bytes,
    truncated: extra.truncated ?? false,
    redirects: [],
    text: () => body,
    json: () => JSON.parse(body),
  }
}

const fake =
  (r: SafeResponse | Error): Fetcher =>
  async () => {
    if (r instanceof Error) throw r
    return r
  }

const ARTICLE = `<!doctype html><html><head><title>Kopi Banjar | Blog</title><script>var x = 1</script></head>
<body><nav>Home About Contact</nav>
<article><h1>Kopi Banjar</h1>
<p>${"Kopi dari Kalimantan Selatan punya rasa yang khas dan aromanya kuat sekali. ".repeat(8)}</p>
<p>${"Banyak warung di Banjarmasin menyajikannya dengan susu kental manis. ".repeat(8)}</p>
</article><footer>Copyright</footer></body></html>`

describe("extractReadable", () => {
  it("keeps the article text and title", () => {
    const out = extractReadable(ARTICLE)
    expect(out.title).toMatch(/Kopi Banjar/)
    expect(out.text).toContain("Kalimantan Selatan")
    expect(out.text).toContain("susu kental manis")
    expect(out.text).not.toContain("var x")
  })

  it("falls back to body text for tiny pages", () => {
    const out = extractReadable("<html><head><title>Hi</title><style>p{}</style></head><body><p>Just a line</p></body></html>")
    expect(out).toEqual({ title: "Hi", text: "Just a line" })
  })
})

describe("fetchPage", () => {
  it("returns title and readable text for HTML", async () => {
    const out = await fetchPage("https://example.com/post", fake(response(ARTICLE, "text/html")))
    expect(out).toMatchObject({ ok: true, url: "https://example.com/post", contentType: "text/html", truncated: false })
    if (out.ok) expect(out.text).toContain("Kalimantan Selatan")
  })

  it("passes JSON and plain text through", async () => {
    const json = await fetchPage("https://api.example.com/x", fake(response('{"a":1,"b":[1,2]}', "application/json")))
    expect(json).toMatchObject({ ok: true, text: '{\n  "a": 1,\n  "b": [\n    1,\n    2\n  ]\n}' })
    const txt = await fetchPage("https://example.com/robots.txt", fake(response("  User-agent: *\n", "text/plain")))
    expect(txt).toMatchObject({ ok: true, title: "", text: "User-agent: *" })
  })

  it("truncates long text to about 12k characters", async () => {
    const out = await fetchPage("https://example.com/big", fake(response("a".repeat(50_000), "text/plain")))
    expect(out.ok && out.truncated).toBe(true)
    if (out.ok) expect(out.text.length).toBeLessThanOrEqual(MAX_TEXT_CHARS + 20)
  })

  it("reports guard refusals and HTTP errors as results, not throws", async () => {
    const blocked = await fetchPage("http://169.254.169.254/", fake(new SafeFetchError("blocked_address", "Address 169.254.169.254 is link-local")))
    expect(blocked).toMatchObject({ ok: false, error: expect.stringMatching(/SSRF guard/) })
    const notFound = await fetchPage("https://example.com/404", fake(response("nope", "text/html", { status: 404 })))
    expect(notFound).toEqual({ ok: false, url: "https://example.com/post", error: "The server answered 404" })
  })

  it("uses the real guard by default: loopback is refused without a request", async () => {
    const out = await fetchPage("http://127.0.0.1:4891/control")
    expect(out).toMatchObject({ ok: false, error: expect.stringMatching(/SSRF guard/) })
  })
})

describe("webfetch runtime", () => {
  it("exposes fetch_page as an untrusted read tool", async () => {
    const turn: TurnInfo = { runId: "r", chatId: "c", channel: "web", trigger: "message", tz: "Asia/Makassar", now: 0 }
    const running = await runtime.start(fakeCtx({}, {}, { id: "webfetch" }))
    const tools = await running.tools!(turn)
    expect(Object.keys(tools)).toEqual(["fetch_page"])
    expect(tools.fetch_page).toMatchObject({ class: "read", untrusted: true })
    expect(await runtime.test(fakeCtx({}, {}))).toMatchObject({ ok: true })
  })
})
