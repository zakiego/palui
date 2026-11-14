// Passkey sign-in, passkey enrollment and sign-out against the Go server's
// auth routes (apps/server/internal/httpapi/auth.go), with the browser's
// WebAuthn API through @simplewebauthn/browser. Same origin as the dashboard,
// mounted at /api/auth:
//
//   GET  /api/auth/passkey/authenticate-options  WebAuthn request options JSON
//   POST /api/auth/passkey/authenticate          { response } -> { session, user }, sets the cookie
//   GET  /api/auth/passkey/register-options      WebAuthn creation options JSON (signed in)
//   POST /api/auth/passkey/register              { response, name? } -> the passkey
//   POST /api/auth/sign-out                      { success: true }
//
// Errors are { code, message } with the HTTP status. Each call resolves to
// { data, error } and never throws, the way the Better Auth client did, so the
// pages keep their error handling.
import {
  startAuthentication,
  startRegistration,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
} from "@simplewebauthn/browser"
import type { PasskeyItem, Viewer } from "@/lib/api/types"

export const AUTH_BASE = "/api/auth"

export type AuthError = { status: number; code?: string; message?: string }
export type AuthResult<T> = { data: T; error: null } | { data: null; error: AuthError }

export type SessionBody = { session: { id: string; expiresAt: number; createdAt: number }; user: Viewer["user"] }

async function call<T>(method: "GET" | "POST", path: string, body?: unknown): Promise<AuthResult<T>> {
  let res: Response
  try {
    res = await fetch(`${AUTH_BASE}${path}`, {
      method,
      credentials: "same-origin",
      headers: body === undefined ? { accept: "application/json" } : { accept: "application/json", "content-type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (err) {
    return { data: null, error: { status: 0, message: err instanceof Error ? err.message : String(err) } }
  }
  let json: unknown = null
  try {
    const text = await res.text()
    json = text ? JSON.parse(text) : null
  } catch {
    json = null
  }
  if (!res.ok) {
    const e = (json && typeof json === "object" ? json : {}) as { code?: unknown; message?: unknown }
    return {
      data: null,
      error: {
        status: res.status,
        code: typeof e.code === "string" ? e.code : undefined,
        message: typeof e.message === "string" ? e.message : res.statusText || undefined,
      },
    }
  }
  return { data: json as T, error: null }
}

// go-webauthn answers with the options object; accept the `{ publicKey }`
// envelope (navigator.credentials' shape) too.
function unwrap<T>(opts: unknown): T {
  if (opts && typeof opts === "object" && "publicKey" in opts && (opts as { publicKey?: unknown }).publicKey) return (opts as { publicKey: T }).publicKey
  return opts as T
}

// A WebAuthn failure (the person closed the prompt, the device said no) as an
// AuthError: the DOMException name ("NotAllowedError", "AbortError") is the code.
function webAuthnError(err: unknown): AuthError {
  if (err instanceof Error) {
    const cause = (err as { cause?: unknown }).cause
    const name = cause instanceof Error ? cause.name : err.name
    return { status: 400, code: name, message: err.message }
  }
  return { status: 400, message: String(err) }
}

async function signInWithPasskey(): Promise<AuthResult<SessionBody>> {
  const options = await call<unknown>("GET", "/passkey/authenticate-options")
  if (options.error) return options
  let response: unknown
  try {
    response = await startAuthentication({ optionsJSON: unwrap<PublicKeyCredentialRequestOptionsJSON>(options.data) })
  } catch (err) {
    return { data: null, error: webAuthnError(err) }
  }
  return call<SessionBody>("POST", "/passkey/authenticate", { response })
}

async function addPasskey(opts: { name?: string } = {}): Promise<AuthResult<PasskeyItem>> {
  const options = await call<unknown>("GET", "/passkey/register-options")
  if (options.error) return options
  let response: unknown
  try {
    response = await startRegistration({ optionsJSON: unwrap<PublicKeyCredentialCreationOptionsJSON>(options.data) })
  } catch (err) {
    return { data: null, error: webAuthnError(err) }
  }
  return call<PasskeyItem>("POST", "/passkey/register", opts.name === undefined ? { response } : { response, name: opts.name })
}

export const authClient = {
  signIn: { passkey: signInWithPasskey },
  passkey: { addPasskey },
  signOut: () => call<{ success: true }>("POST", "/sign-out"),
}

// Auth errors carry a status; 429 means the sign-in rate limit kicked in.
export function authErrorMessage(error: { status?: number; message?: string; code?: string } | null | undefined, fallback: string) {
  if (!error) return fallback
  if (error.status === 429) return "Too many tries. Wait a minute and try again."
  return error.message || fallback
}

// WebAuthn errors when the person closes the prompt or the device says no.
export function passkeyCancelled(error: { message?: string; code?: string } | null | undefined) {
  const s = `${error?.code ?? ""} ${error?.message ?? ""}`
  return /NotAllowed|AbortError|cancel|timed out|not allowed/i.test(s)
}
