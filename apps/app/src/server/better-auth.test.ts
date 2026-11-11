import { describe, expect, it } from "vitest"
import { blockedAuthPath, crossOriginWrite } from "./better-auth"

const req = (path: string, init: RequestInit = {}) => new Request(`http://localhost:4890${path}`, init)

describe("auth endpoint guard", () => {
  it("allows only what the passkey-only dashboard needs", () => {
    for (const p of ["/api/auth/get-session", "/api/auth/sign-out", "/api/auth/passkey/generate-authenticate-options", "/api/auth/passkey/verify-authentication", "/api/auth/passkey/verify-registration"]) {
      expect(blockedAuthPath(req(p))).toBe(false)
    }
  })

  it("blocks password sign-in, sign-up and deleting passkeys from outside", () => {
    for (const p of ["/api/auth/sign-in/username", "/api/auth/sign-in/email", "/api/auth/sign-up/email", "/api/auth/change-password", "/api/auth/passkey/delete-passkey", "/api/auth/SIGN-IN/username", "/api/auth//sign-in/username", "/api/auth/%73ign-in/username", "/api/auth/list-sessions", "/api/auth/revoke-sessions"]) {
      expect(blockedAuthPath(req(p))).toBe(true)
    }
  })

  it("refuses cross-origin writes", () => {
    expect(crossOriginWrite(req("/api/auth/passkey/verify-authentication", { method: "POST", headers: { origin: "https://evil.example" } }))).toBe(true)
    expect(crossOriginWrite(req("/api/auth/passkey/verify-authentication", { method: "POST", headers: { origin: "http://localhost:4890" } }))).toBe(false)
    expect(crossOriginWrite(req("/api/auth/get-session", { headers: { origin: "https://evil.example" } }))).toBe(false)
  })
})
