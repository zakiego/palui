import { afterEach, describe, expect, it, vi } from "vitest"
import worker from "./index.ts"

const SITE = "https://palui-web.pages.dev"
const bindings = { SITE_ORIGIN: SITE }

afterEach(() => vi.unstubAllGlobals())

function stubFetch(res: () => Response) {
  const calls: { input: Request; init?: RequestInit }[] = []
  vi.stubGlobal("fetch", async (input: Request, init?: RequestInit) => {
    calls.push({ input, init })
    return res()
  })
  return calls
}

describe("edge worker", () => {
  it("hands app requests to the origin as the very same Request", async () => {
    const upstream = new Response("data: x\n\n", {
      headers: { "set-cookie": "palui.session_token=a; Path=/; HttpOnly", "content-type": "text/event-stream" },
    })
    const calls = stubFetch(() => upstream)
    const req = new Request("https://palui.zakiego.com/api/hooks/github?x=1", {
      method: "POST",
      body: "{}",
      headers: { "x-palui-signature": "sha256=abc" },
    })
    const res = await worker.fetch(req, bindings)
    expect(calls).toHaveLength(1)
    expect(calls[0].input).toBe(req)
    expect(calls[0].init).toBeUndefined()
    expect(res).toBe(upstream)
    expect(res.headers.get("set-cookie")).toContain("palui.session_token")
  })

  it("sends /app, /api/auth and /health to the origin", async () => {
    for (const path of ["/app", "/app/saklar", "/api/auth/get-session", "/health"]) {
      const calls = stubFetch(() => new Response("ok"))
      const req = new Request(`https://palui.zakiego.com${path}`)
      await worker.fetch(req, bindings)
      expect(calls[0].input, path).toBe(req)
      vi.unstubAllGlobals()
    }
  })

  it("passes origin redirects through untouched", async () => {
    stubFetch(() => new Response(null, { status: 302, headers: { location: "/app/login" } }))
    const res = await worker.fetch(new Request("https://palui.zakiego.com/app"), bindings)
    expect(res.status).toBe(302)
    expect(res.headers.get("location")).toBe("/app/login")
  })

  it("serves the rest from Pages, keeping path, query and headers", async () => {
    const calls = stubFetch(() => new Response("<html>"))
    await worker.fetch(new Request("https://palui.zakiego.com/docs/api?q=1", { headers: { accept: "text/html" } }), bindings)
    expect(calls[0].input.url).toBe(`${SITE}/docs/api?q=1`)
    expect(calls[0].input.headers.get("accept")).toBe("text/html")
    expect(calls[0].init?.redirect).toBe("manual")
  })

  it("doesn't send look-alike paths to the app", async () => {
    for (const path of ["/apple-icon.png", "/api-docs", "/apps", "/healthz", "/search.json"]) {
      const calls = stubFetch(() => new Response("site"))
      await worker.fetch(new Request(`https://palui.zakiego.com${path}`), bindings)
      expect(calls[0].input.url, path).toBe(`${SITE}${path}`)
      vi.unstubAllGlobals()
    }
  })

  it("keeps Pages redirects on the public hostname", async () => {
    stubFetch(() => new Response(null, { status: 308, headers: { location: `${SITE}/docs` } }))
    const res = await worker.fetch(new Request("https://palui.zakiego.com/docs/"), bindings)
    expect(res.status).toBe(308)
    expect(res.headers.get("location")).toBe("https://palui.zakiego.com/docs")
  })

  it("leaves redirects to other hosts alone", async () => {
    stubFetch(() => new Response(null, { status: 302, headers: { location: "https://example.com/x" } }))
    const res = await worker.fetch(new Request("https://palui.zakiego.com/out"), bindings)
    expect(res.headers.get("location")).toBe("https://example.com/x")
  })

  it("on the preview worker, forwards app paths to APP_ORIGIN", async () => {
    const calls = stubFetch(() => new Response("ok"))
    await worker.fetch(new Request("https://palui-edge-preview.example.workers.dev/health?full=1"), {
      SITE_ORIGIN: SITE,
      APP_ORIGIN: "https://palui.zakiego.com",
    })
    expect(calls[0].input.url).toBe("https://palui.zakiego.com/health?full=1")
  })
})
