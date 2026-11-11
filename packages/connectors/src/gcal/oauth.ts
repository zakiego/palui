// Google OAuth for the read-only Calendar connector. The owner brings their own
// OAuth client. State is signed like utusan's ticket.ts: an HMAC over a
// base64url payload, bound to one connector and valid for 10 minutes, so it can't
// be pointed at another connector. Its nonce is also stored on the connector when
// the link is issued and consumed by the first callback, so a state works once.
import { createHmac, randomBytes, timingSafeEqual } from "node:crypto"
import { eq } from "drizzle-orm"
import { connectors, type DB } from "@palui/db"
import { getConnector, getSecrets, patchConnectorConfig, upsertConnector } from "@palui/core/connectors/store"
import manifest from "./manifest"

export const GOOGLE_AUTH_URL = "https://accounts.google.com/o/oauth2/v2/auth"
export const GOOGLE_TOKEN_URL = "https://oauth2.googleapis.com/token"
export const CALENDAR_API = "https://www.googleapis.com/calendar/v3"
export const CALENDAR_SCOPE = "https://www.googleapis.com/auth/calendar.readonly"
export const STATE_TTL_MS = 10 * 60_000
const STATE_DOMAIN = "palui:oauth:google"

export class GoogleError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = "GoogleError"
  }
}

export function redirectUriFor(appUrl: string): string {
  return `${appUrl.replace(/\/+$/, "")}/api/oauth/google/callback`
}

// --- State -------------------------------------------------------------------------

type StatePayload = { c: string; e: number; n: string }

function sign(masterKey: string, payload: string): string {
  return createHmac("sha256", masterKey).update(`${STATE_DOMAIN}:${payload}`).digest("base64url")
}

export type IssuedState = { state: string; nonce: string; expiresAt: number }

export function issueState(masterKey: string, connectorId: string, now = Date.now()): IssuedState {
  const nonce = randomBytes(16).toString("base64url")
  const expiresAt = now + STATE_TTL_MS
  const payload = Buffer.from(JSON.stringify({ c: connectorId, e: expiresAt, n: nonce } satisfies StatePayload)).toString("base64url")
  return { state: `${payload}.${sign(masterKey, payload)}`, nonce, expiresAt }
}

// A signed state only. Callers that send the owner to Google use issueState and
// store the nonce with nonceConfig, or the callback refuses the state.
export function signState(masterKey: string, connectorId: string, now = Date.now()): string {
  return issueState(masterKey, connectorId, now).state
}

// Connector config keys that hold the one outstanding OAuth nonce. Written with
// patchConnectorConfig (no restart); never part of the manifest's config schema.
export function nonceConfig(issued: IssuedState): { oauthNonce: string; oauthNonceExp: number } {
  return { oauthNonce: issued.nonce, oauthNonceExp: issued.expiresAt }
}

export type StateResult = { ok: true; connectorId: string; nonce: string } | { ok: false; reason: "malformed" | "bad-signature" | "expired" }

export function verifyState(masterKey: string, state: string, now = Date.now()): StateResult {
  const dot = state.lastIndexOf(".")
  if (dot <= 0) return { ok: false, reason: "malformed" }
  const payload = state.slice(0, dot)
  const a = Buffer.from(state.slice(dot + 1))
  const b = Buffer.from(sign(masterKey, payload))
  if (a.length !== b.length || !timingSafeEqual(a, b)) return { ok: false, reason: "bad-signature" }
  let parsed: StatePayload
  try {
    parsed = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as StatePayload
  } catch {
    return { ok: false, reason: "malformed" }
  }
  if (typeof parsed.c !== "string" || typeof parsed.e !== "number" || typeof parsed.n !== "string") return { ok: false, reason: "malformed" }
  if (parsed.e <= now) return { ok: false, reason: "expired" }
  return { ok: true, connectorId: parsed.c, nonce: parsed.n }
}

// Takes the stored nonce off the connector if it matches; true when it did. The
// read and the write run in one synchronous step, so two callbacks can't both win.
export function consumeNonce(db: DB, connectorId: string, nonce: string, now = Date.now()): boolean {
  const row = getConnector(db, connectorId)
  if (!row) return false
  const { oauthNonce, oauthNonceExp, ...rest } = row.config as Record<string, unknown>
  if (oauthNonce === undefined && oauthNonceExp === undefined) return false
  // Remove it whatever the outcome: a mismatch means someone is guessing.
  db.update(connectors).set({ config: rest, updatedAt: now }).where(eq(connectors.id, connectorId)).run()
  if (typeof oauthNonce !== "string" || typeof oauthNonceExp !== "number" || oauthNonceExp <= now) return false
  const a = Buffer.from(oauthNonce)
  const b = Buffer.from(nonce)
  return a.length === b.length && timingSafeEqual(a, b)
}

// --- URLs and tokens ---------------------------------------------------------------

export function buildAuthUrl(input: { clientId: string; redirectUri: string; state: string; loginHint?: string }): string {
  const params = new URLSearchParams({
    client_id: input.clientId,
    redirect_uri: input.redirectUri,
    response_type: "code",
    scope: CALENDAR_SCOPE,
    access_type: "offline",
    prompt: "consent",
    include_granted_scopes: "true",
    state: input.state,
  })
  if (input.loginHint) params.set("login_hint", input.loginHint)
  return `${GOOGLE_AUTH_URL}?${params}`
}

// The URL the dashboard opens for the "oauth" wizard step. Works before the
// connector runs: it only needs the client id saved in the connector's secrets.
export function googleAuthUrl(db: DB, connectorId: string, masterKey: string, appUrl: string, now = Date.now()): string {
  const row = getConnector(db, connectorId)
  if (!row || row.type !== manifest.id) throw new Error(`No Google Calendar connector ${connectorId}`)
  const { clientId } = getSecrets(row, masterKey)
  if (!clientId) throw new Error("Save the OAuth client ID first")
  const email = typeof row.config.email === "string" ? row.config.email : undefined
  const issued = issueState(masterKey, connectorId, now)
  patchConnectorConfig(db, connectorId, nonceConfig(issued), now)
  return buildAuthUrl({ clientId, redirectUri: redirectUriFor(appUrl), state: issued.state, loginHint: email })
}

async function tokenCall(body: Record<string, string>, fetchImpl: typeof fetch): Promise<Record<string, unknown>> {
  let res: Response
  try {
    res = await fetchImpl(GOOGLE_TOKEN_URL, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded", accept: "application/json" },
      body: new URLSearchParams(body).toString(),
      signal: AbortSignal.timeout(15_000),
    })
  } catch (err) {
    throw new GoogleError(0, "network", `Can't reach Google: ${(err as Error)?.message ?? String(err)}`)
  }
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) {
    const code = typeof data.error === "string" ? data.error : `http_${res.status}`
    const desc = typeof data.error_description === "string" ? data.error_description : ""
    const hint =
      code === "invalid_grant"
        ? "Google access was revoked or expired. Connect with Google again."
        : code === "invalid_client"
          ? "Google rejected the OAuth client ID or secret."
          : code === "redirect_uri_mismatch"
            ? "The redirect URI isn't registered on the OAuth client."
            : `Google token error ${code}`
    throw new GoogleError(res.status, code, desc ? `${hint} (${desc})` : hint)
  }
  return data
}

export async function exchangeCode(
  input: { clientId: string; clientSecret: string; code: string; redirectUri: string },
  fetchImpl: typeof fetch = fetch,
): Promise<{ accessToken: string; refreshToken?: string; expiresIn: number; scope?: string }> {
  const data = await tokenCall(
    { grant_type: "authorization_code", code: input.code, client_id: input.clientId, client_secret: input.clientSecret, redirect_uri: input.redirectUri },
    fetchImpl,
  )
  return {
    accessToken: String(data.access_token ?? ""),
    refreshToken: typeof data.refresh_token === "string" ? data.refresh_token : undefined,
    expiresIn: Number(data.expires_in ?? 3600),
    scope: typeof data.scope === "string" ? data.scope : undefined,
  }
}

export async function refreshAccessToken(
  input: { clientId: string; clientSecret: string; refreshToken: string },
  fetchImpl: typeof fetch = fetch,
  now = Date.now(),
): Promise<{ accessToken: string; expiresAt: number }> {
  const data = await tokenCall(
    { grant_type: "refresh_token", refresh_token: input.refreshToken, client_id: input.clientId, client_secret: input.clientSecret },
    fetchImpl,
  )
  const accessToken = String(data.access_token ?? "")
  if (!accessToken) throw new GoogleError(500, "no_token", "Google returned no access token")
  return { accessToken, expiresAt: now + Number(data.expires_in ?? 3600) * 1000 }
}

// The primary calendar's id is the account email.
export async function primaryEmail(accessToken: string, fetchImpl: typeof fetch = fetch): Promise<string | undefined> {
  try {
    const res = await fetchImpl(`${CALENDAR_API}/calendars/primary`, { headers: { authorization: `Bearer ${accessToken}` }, signal: AbortSignal.timeout(10_000) })
    if (!res.ok) return undefined
    const body = (await res.json()) as { id?: string }
    return body.id && body.id.includes("@") ? body.id : undefined
  } catch {
    return undefined
  }
}

// --- Callback ----------------------------------------------------------------------

export type CallbackResult = { ok: boolean; detail: string; connectorId?: string }

// GET /api/oauth/google/callback?code&state (or ?error). Verifies the state,
// exchanges the code, stores the refresh token encrypted and learns the email.
// Changing the secrets changes the config hash, so the supervisor restarts it.
export async function handleGoogleCallback(
  db: DB,
  url: URL,
  masterKey: string,
  appUrl: string,
  fetchImpl: typeof fetch = fetch,
  now = Date.now(),
): Promise<CallbackResult> {
  const state = url.searchParams.get("state")
  if (!state) return { ok: false, detail: "The sign-in link is missing its state. Start again from the dashboard." }
  const verified = verifyState(masterKey, state, now)
  if (!verified.ok) {
    return { ok: false, detail: verified.reason === "expired" ? "The sign-in link expired (10 minutes). Start again." : "The sign-in link isn't valid. Start again." }
  }
  const connectorId = verified.connectorId
  const row = getConnector(db, connectorId)
  if (!row || row.type !== manifest.id) return { ok: false, connectorId, detail: "That Calendar connector no longer exists." }
  // Single use: the first callback with this state consumes it, whatever happens next.
  if (!consumeNonce(db, connectorId, verified.nonce, now)) {
    return { ok: false, connectorId, detail: "That sign-in link was already used or replaced by a newer one. Start again from the dashboard." }
  }
  const error = url.searchParams.get("error")
  if (error) return { ok: false, connectorId, detail: error === "access_denied" ? "You cancelled the Google sign-in." : `Google said: ${error}` }
  const code = url.searchParams.get("code")
  if (!code) return { ok: false, connectorId, detail: "Google didn't send a code." }

  const secrets = getSecrets(row, masterKey)
  if (!secrets.clientId || !secrets.clientSecret) return { ok: false, connectorId, detail: "Save the OAuth client ID and secret first." }

  let tokens: Awaited<ReturnType<typeof exchangeCode>>
  try {
    tokens = await exchangeCode({ clientId: secrets.clientId, clientSecret: secrets.clientSecret, code, redirectUri: redirectUriFor(appUrl) }, fetchImpl)
  } catch (err) {
    return { ok: false, connectorId, detail: (err as Error).message }
  }
  if (!tokens.refreshToken) {
    return {
      ok: false,
      connectorId,
      detail: "Google didn't return a refresh token. Remove Palui at myaccount.google.com/permissions and connect again.",
    }
  }
  if (tokens.scope && !tokens.scope.split(" ").includes(CALENDAR_SCOPE)) {
    return { ok: false, connectorId, detail: "Calendar access wasn't granted. Tick the calendar permission when Google asks." }
  }
  const email = await primaryEmail(tokens.accessToken, fetchImpl)
  upsertConnector(
    db,
    { id: connectorId, manifest, secrets: { refreshToken: tokens.refreshToken }, ...(email ? { config: { email } } : {}) },
    masterKey,
    now,
  )
  return { ok: true, connectorId, detail: email ? `Connected as ${email}` : "Connected to Google Calendar" }
}
