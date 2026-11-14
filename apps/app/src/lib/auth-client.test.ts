import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

// The browser's WebAuthn prompt, mocked: tests decide what the authenticator answers.
const webauthn = vi.hoisted(() => ({
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))
vi.mock("@simplewebauthn/browser", () => webauthn)

import { authClient, authErrorMessage, passkeyCancelled } from "./auth-client"

type Call = { url: string; method: string; body: unknown }

let calls: Call[]
let routes: Record<string, () => Response>

beforeEach(() => {
  calls = []
  routes = {}
  webauthn.startAuthentication.mockReset()
  webauthn.startRegistration.mockReset()
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    const method = init.method ?? "GET"
    calls.push({ url, method, body: init.body === undefined ? undefined : JSON.parse(String(init.body)) })
    const route = routes[`${method} ${url}`]
    return route ? route() : Response.json({ code: "NOT_FOUND", message: "Not found" }, { status: 404 })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const requestOptions = { challenge: "Y2hhbGxlbmdl", rpId: "localhost", timeout: 60000, userVerification: "preferred", allowCredentials: [] }
const creationOptions = {
  rp: { name: "Palui", id: "localhost" },
  user: { id: "dXNlcg", name: "owner@palui.invalid", displayName: "owner@palui.invalid" },
  challenge: "Y2hhbGxlbmdl",
  pubKeyCredParams: [{ type: "public-key", alg: -7 }],
  excludeCredentials: [],
}
const session = { session: { id: "ses_1", expiresAt: 2, createdAt: 1 }, user: { id: "usr_1", username: "owner", name: "Owner" } }

function notAllowed() {
  const cause = new DOMException("The operation either timed out or was not allowed.", "NotAllowedError")
  return Object.assign(new Error(cause.message), { name: "WebAuthnError", code: "ERROR_PASSTHROUGH_SEE_CAUSE_PROPERTY", cause })
}

describe("passkey sign-in", () => {
  it("asks for options, runs the ceremony and posts the response", async () => {
    routes["GET /api/auth/passkey/authenticate-options"] = () => Response.json(requestOptions)
    routes["POST /api/auth/passkey/authenticate"] = () => Response.json(session)
    webauthn.startAuthentication.mockResolvedValue({ id: "cred", rawId: "cred", type: "public-key", response: { clientDataJSON: "e30" }, clientExtensionResults: {} })

    const res = await authClient.signIn.passkey()

    expect(res).toEqual({ data: session, error: null })
    expect(webauthn.startAuthentication).toHaveBeenCalledWith({ optionsJSON: requestOptions })
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual(["GET /api/auth/passkey/authenticate-options", "POST /api/auth/passkey/authenticate"])
    expect(calls[1]!.body).toEqual({ response: { id: "cred", rawId: "cred", type: "public-key", response: { clientDataJSON: "e30" }, clientExtensionResults: {} } })
  })

  it("accepts options wrapped in { publicKey }", async () => {
    routes["GET /api/auth/passkey/authenticate-options"] = () => Response.json({ publicKey: requestOptions })
    routes["POST /api/auth/passkey/authenticate"] = () => Response.json(session)
    webauthn.startAuthentication.mockResolvedValue({ id: "cred" })
    await authClient.signIn.passkey()
    expect(webauthn.startAuthentication).toHaveBeenCalledWith({ optionsJSON: requestOptions })
  })

  it("reports a closed prompt as cancelled and posts nothing", async () => {
    routes["GET /api/auth/passkey/authenticate-options"] = () => Response.json(requestOptions)
    webauthn.startAuthentication.mockRejectedValue(notAllowed())
    const res = await authClient.signIn.passkey()
    expect(res.data).toBeNull()
    expect(res.error?.code).toBe("NotAllowedError")
    expect(passkeyCancelled(res.error)).toBe(true)
    expect(calls).toHaveLength(1)
  })

  it("passes the server's refusal through", async () => {
    routes["GET /api/auth/passkey/authenticate-options"] = () => Response.json(requestOptions)
    routes["POST /api/auth/passkey/authenticate"] = () => Response.json({ code: "PASSKEY_NOT_FOUND", message: "Passkey not found" }, { status: 401 })
    webauthn.startAuthentication.mockResolvedValue({ id: "cred" })
    const res = await authClient.signIn.passkey()
    expect(res.error).toEqual({ status: 401, code: "PASSKEY_NOT_FOUND", message: "Passkey not found" })
    expect(passkeyCancelled(res.error)).toBe(false)
    expect(authErrorMessage(res.error, "fallback")).toBe("Passkey not found")
  })

  it("says to wait on the rate limit", async () => {
    routes["GET /api/auth/passkey/authenticate-options"] = () => Response.json({ code: "TOO_MANY_REQUESTS", message: "Too many requests. Please try again later." }, { status: 429 })
    const res = await authClient.signIn.passkey()
    expect(webauthn.startAuthentication).not.toHaveBeenCalled()
    expect(authErrorMessage(res.error, "fallback")).toBe("Too many tries. Wait a minute and try again.")
  })

  it("turns a network failure into an error, not a throw", async () => {
    vi.stubGlobal("fetch", async () => {
      throw new TypeError("Failed to fetch")
    })
    const res = await authClient.signIn.passkey()
    expect(res.error).toEqual({ status: 0, message: "Failed to fetch" })
  })
})

describe("passkey enrollment", () => {
  it("registers a passkey under a name", async () => {
    const item = { id: "pk_1", name: "Mac", deviceType: "multiDevice", backedUp: true, createdAt: 1 }
    routes["GET /api/auth/passkey/register-options"] = () => Response.json(creationOptions)
    routes["POST /api/auth/passkey/register"] = () => Response.json(item)
    webauthn.startRegistration.mockResolvedValue({ id: "cred", response: { attestationObject: "o", clientDataJSON: "c", transports: ["internal"] } })

    const res = await authClient.passkey.addPasskey({ name: "Mac" })

    expect(res).toEqual({ data: item, error: null })
    expect(webauthn.startRegistration).toHaveBeenCalledWith({ optionsJSON: creationOptions })
    expect(calls[1]).toEqual({
      url: "/api/auth/passkey/register",
      method: "POST",
      body: { response: { id: "cred", response: { attestationObject: "o", clientDataJSON: "c", transports: ["internal"] } }, name: "Mac" },
    })
  })

  it("needs a session for the options", async () => {
    routes["GET /api/auth/passkey/register-options"] = () => Response.json({ code: "UNAUTHORIZED", message: "Unauthorized" }, { status: 401 })
    const res = await authClient.passkey.addPasskey({ name: "Mac" })
    expect(res.error).toEqual({ status: 401, code: "UNAUTHORIZED", message: "Unauthorized" })
    expect(webauthn.startRegistration).not.toHaveBeenCalled()
  })

  it("reports a closed prompt as cancelled", async () => {
    routes["GET /api/auth/passkey/register-options"] = () => Response.json(creationOptions)
    webauthn.startRegistration.mockRejectedValue(notAllowed())
    const res = await authClient.passkey.addPasskey({ name: "Mac" })
    expect(passkeyCancelled(res.error)).toBe(true)
    expect(calls).toHaveLength(1)
  })
})

describe("sign out", () => {
  it("posts to sign-out", async () => {
    routes["POST /api/auth/sign-out"] = () => Response.json({ success: true })
    expect(await authClient.signOut()).toEqual({ data: { success: true }, error: null })
    expect(calls).toEqual([{ url: "/api/auth/sign-out", method: "POST", body: undefined }])
  })
})
