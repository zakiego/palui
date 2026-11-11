// Better Auth on the app's better-sqlite3 connection through the Drizzle adapter
// (PLAN 10). One owner, created only by `palui owner invite`; sign-up is off from
// the first boot. The dashboard signs in with passkeys only. Username + password
// stays enabled solely so the invite flow can sign the owner in server side with
// a random password nobody ever sees; its HTTP endpoints are blocked below.
import { randomBytes } from "node:crypto"
import { passkey } from "@better-auth/passkey"
import { config } from "@palui/core/config"
import { schema, type DB } from "@palui/db"
import { betterAuth } from "better-auth"
import { drizzleAdapter } from "better-auth/adapters/drizzle"
import { username } from "better-auth/plugins/username"
import { tanstackStartCookies } from "better-auth/tanstack-start"
import { getDb } from "./db"

export const AUTH_BASE_PATH = "/api/auth"
export const COOKIE_PREFIX = "palui"
// Set by the route handler from the proxy's headers, never trusted from the client.
export const CLIENT_IP_HEADER = "x-palui-client-ip"

// Without a real secret sessions only last until the process restarts, and the
// login page says the server isn't configured.
let fallbackSecret: string | undefined
export function authConfigured() {
  return config().authSecret.length >= 32
}

export function createAuth(db: DB) {
  const cfg = config()
  const url = new URL(cfg.appUrl)
  fallbackSecret ??= randomBytes(32).toString("base64")
  return betterAuth({
    appName: "Palui",
    baseURL: cfg.appUrl,
    basePath: AUTH_BASE_PATH,
    secret: authConfigured() ? cfg.authSecret : fallbackSecret,
    trustedOrigins: [url.origin],
    database: drizzleAdapter(db, {
      provider: "sqlite",
      schema: {
        user: schema.user,
        session: schema.session,
        account: schema.account,
        verification: schema.verification,
        passkey: schema.passkey,
      },
    }),
    emailAndPassword: {
      enabled: true,
      // No sign-up ever. The owner comes from `palui owner invite`.
      disableSignUp: true,
    },
    session: { expiresIn: 30 * 24 * 60 * 60, updateAge: 24 * 60 * 60 },
    advanced: {
      cookiePrefix: COOKIE_PREFIX,
      useSecureCookies: url.protocol === "https:",
      defaultCookieAttributes: { httpOnly: true, sameSite: "lax" },
      ipAddress: { ipAddressHeaders: [CLIENT_IP_HEADER] },
      // Better Auth turns these off under a test runner. Keep them on so the
      // tests exercise what production runs.
      disableOriginCheck: false,
      disableCSRFCheck: false,
    },
    rateLimit: {
      enabled: process.env.NODE_ENV !== "test",
      storage: "memory",
      window: 60,
      max: 100,
      customRules: {
        "/passkey/verify-authentication": { window: 60, max: 5 },
        "/passkey/generate-authenticate-options": { window: 60, max: 10 },
        "/passkey/verify-registration": { window: 60, max: 5 },
        "/passkey/generate-register-options": { window: 60, max: 10 },
      },
    },
    plugins: [
      username(),
      passkey({ rpID: url.hostname, rpName: "Palui Agent", origin: url.origin }),
      // Last, so it forwards cookies set by the plugins above.
      tanstackStartCookies(),
    ],
  })
}

export type Auth = ReturnType<typeof createAuth>

const instances = new WeakMap<DB, Auth>()

export function getAuth(db: DB = getDb()): Auth {
  let auth = instances.get(db)
  if (!auth) {
    auth = createAuth(db)
    instances.set(db, auth)
  }
  return auth
}

// The real client IP from the proxy (Cloudflare tunnel in prod), copied into a
// header the client can't set, so rate limits key on it. Built from parts:
// Nitro's request isn't a native Request, so `new Request(request)` throws.
export async function withClientIp(request: Request): Promise<Request> {
  const ip =
    request.headers.get("cf-connecting-ip") ??
    request.headers.get("x-forwarded-for")?.split(",")[0]?.trim() ??
    request.headers.get("x-real-ip") ??
    "local"
  const headers = new Headers(request.headers)
  headers.set(CLIENT_IP_HEADER, ip)
  const hasBody = request.method !== "GET" && request.method !== "HEAD"
  return new Request(request.url, { method: request.method, headers, body: hasBody ? await request.arrayBuffer() : undefined })
}

// Better Auth only checks Origin on requests that already carry cookies. A
// sign-in has none, so a hostile page could log a visitor into a session of its
// choosing. Every write to /api/auth must come from APP_URL.
export function crossOriginWrite(request: Request): boolean {
  if (request.method === "GET" || request.method === "HEAD" || request.method === "OPTIONS") return false
  if (request.headers.get("sec-fetch-site") === "cross-site") return true
  const origin = request.headers.get("origin")
  return !!origin && origin !== new URL(config().appUrl).origin
}

// The dashboard only needs these endpoints. Everything else (password sign-in,
// sign-up, password changes, profile edits) stays unreachable from outside, so
// a passkey is the only way in.
// delete-passkey stays blocked: the Settings server function removes passkeys
// and refuses to remove the last one. list-sessions stays blocked too: nothing
// calls it and it returns every session token.
const ALLOWED = [
  /^\/get-session$/,
  /^\/sign-out$/,
  /^\/passkey\/(generate-register-options|verify-registration|generate-authenticate-options|verify-authentication|list-user-passkeys|update-passkey)$/,
  /^\/ok$/,
  /^\/error$/,
]

export function blockedAuthPath(request: Request): boolean {
  let path = new URL(request.url).pathname
  try {
    path = decodeURIComponent(path)
  } catch {
    return true
  }
  path = path.replace(/\/{2,}/g, "/").toLowerCase()
  if (!path.startsWith(`${AUTH_BASE_PATH}/`)) return true
  const rest = path.slice(AUTH_BASE_PATH.length).replace(/\/$/, "")
  return !ALLOWED.some((re) => re.test(rest))
}

export function authHandler(db: DB) {
  return async (request: Request): Promise<Response> => {
    if (blockedAuthPath(request)) return Response.json({ code: "NOT_FOUND", message: "Not found" }, { status: 404 })
    if (crossOriginWrite(request)) return Response.json({ code: "INVALID_ORIGIN", message: "Invalid origin" }, { status: 403 })
    return getAuth(db).handler(await withClientIp(request))
  }
}

export function handleAuth(request: Request): Promise<Response> {
  return authHandler(getDb())(request)
}
