import { describe, expect, it } from "vitest"
import { isAppPath } from "./routing.ts"

describe("isAppPath", () => {
  it("sends the app's paths to the origin", () => {
    for (const p of [
      "/app",
      "/app/",
      "/app/connectors",
      "/app/chats/cht_1",
      "/app/assets/app-abc.css",
      "/app/_serverFn/abc",
      "/api",
      "/api/",
      "/api/auth/sign-in/passkey",
      "/api/auth/passkey/generate-authenticate-options",
      "/api/hooks/github",
      "/api/connectors",
      "/api/connectors/telegram/test",
      "/health",
    ]) {
      expect(isAppPath(p), p).toBe(true)
    }
  })

  it("serves everything else from the site", () => {
    for (const p of [
      "/",
      "/docs",
      "/docs/api",
      "/docs/connectors/webhooks",
      "/_next/static/x.js",
      "/og.png",
      "/og/docs/api/image.png",
      "/search.json",
      "/robots.txt",
      "/sitemap.xml",
      "/manifest.webmanifest",
      "/llms.txt",
      "/apple-icon.png",
      "/apple",
      "/apps",
      "/application",
      "/api-docs",
      "/api-docs/hooks",
      "/apis",
      "/apix",
      "/healthz",
      "/health-check",
      "/healthy",
      "/v1/memories",
      "/mcp",
    ]) {
      expect(isAppPath(p), p).toBe(false)
    }
  })

  it("matches by path segment, not by substring", () => {
    expect(isAppPath("/docs/app")).toBe(false)
    expect(isAppPath("/docs/health")).toBe(false)
    expect(isAppPath("/x/api/hooks/a")).toBe(false)
  })
})
