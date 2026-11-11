import { describe, expect, it } from "vitest"
import { cleanDetail, DETAIL_MAX, oauthReturnUrl } from "./oauth-return"

describe("OAuth callback redirect", () => {
  it("strips control and bidi characters and collapses whitespace", () => {
    expect(cleanDetail("Google said:\r\nSet-Cookie: x=1\u0000‮ evil")).toBe("Google said: Set-Cookie: x=1 evil")
  })

  it("caps the detail at 200 characters without splitting a surrogate pair", () => {
    expect(cleanDetail("a".repeat(500))).toHaveLength(DETAIL_MAX)
    expect(cleanDetail("\u{1F600}".repeat(300))).toBe("\u{1F600}".repeat(DETAIL_MAX))
  })

  it("always points at APP_URL's Connectors page", () => {
    const url = new URL(oauthReturnUrl("https://palui.example.com", { ok: false, detail: "Google said: //evil.example\n" }))
    expect(url.origin).toBe("https://palui.example.com")
    expect(url.pathname).toBe("/app/connectors")
    expect(url.searchParams.get("oauth")).toBe("error")
    expect(url.searchParams.get("detail")).toBe("Google said: //evil.example")
  })
})
