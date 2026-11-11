import { afterEach, describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { setConfig } from "@palui/core/config"
import { getConnector, getSecrets, upsertConnector } from "@palui/core/saklar/store"
import type { TurnInfo } from "@palui/core/saklar/define"
import { fakeCtx } from "../mcp/test-helpers"
import { listEvents, presentEvent, resolveRange } from "./calendar"
import manifest from "./manifest"
import { buildAuthUrl, CALENDAR_SCOPE, googleAuthUrl, handleGoogleCallback, redirectUriFor, refreshAccessToken, signState, STATE_TTL_MS, verifyState } from "./oauth"
import { createGcalRuntime } from "./runtime"

const KEY = "test-master-key-0123456789-abcdefghijklmnop"
const APP = "https://palui.example.com"
const CLIENT = { clientId: "123-abc.apps.googleusercontent.com", clientSecret: "GOCSPX-secret-value" }
const TZ = "Asia/Makassar"
// 2026-11-10T10:00:00+08:00
const NOW = Date.parse("2026-11-10T02:00:00Z")

type Call = { url: string; init: RequestInit }

function google(opts: { refresh?: "ok" | "invalid_grant"; noRefreshToken?: boolean; events?: unknown[] } = {}) {
  const calls: Call[] = []
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } })
  const f = (async (input: string | URL | Request, init: RequestInit = {}) => {
    const url = String(input)
    calls.push({ url, init })
    if (url === "https://oauth2.googleapis.com/token") {
      const body = new URLSearchParams(String(init.body))
      if (body.get("grant_type") === "authorization_code") {
        if (body.get("code") !== "good-code") return json(400, { error: "invalid_grant", error_description: "Bad Request" })
        return json(200, {
          access_token: "ya29.access",
          expires_in: 3599,
          scope: CALENDAR_SCOPE,
          token_type: "Bearer",
          ...(opts.noRefreshToken ? {} : { refresh_token: "1//refresh-token" }),
        })
      }
      if (opts.refresh === "invalid_grant") return json(400, { error: "invalid_grant", error_description: "Token has been expired or revoked." })
      return json(200, { access_token: "ya29.fresh", expires_in: 3599 })
    }
    if (url === "https://www.googleapis.com/calendar/v3/calendars/primary") return json(200, { id: "zaki@example.com", summary: "zaki@example.com" })
    if (url.startsWith("https://www.googleapis.com/calendar/v3/calendars/")) {
      return json(200, {
        items: opts.events ?? [
          { id: "e1", status: "confirmed", summary: "Standup", start: { dateTime: "2026-11-10T09:00:00+08:00" }, end: { dateTime: "2026-11-10T09:15:00+08:00" } },
          { id: "e2", status: "cancelled", summary: "Old", start: { dateTime: "2026-11-10T11:00:00+08:00" }, end: { dateTime: "2026-11-10T12:00:00+08:00" } },
          { id: "e3", summary: "Libur", start: { date: "2026-11-10" }, end: { date: "2026-11-11" } },
        ],
      })
    }
    return json(404, {})
  }) as typeof fetch
  return { f, calls }
}

afterEach(() => setConfig(undefined))

describe("oauth state", () => {
  it("round-trips and binds the connector id", () => {
    const s = signState(KEY, "gcal", NOW)
    expect(verifyState(KEY, s, NOW + 1000)).toMatchObject({ ok: true, connectorId: "gcal" })
  })

  it("expires after 10 minutes and rejects tampering", () => {
    const s = signState(KEY, "gcal", NOW)
    expect(verifyState(KEY, s, NOW + STATE_TTL_MS)).toEqual({ ok: false, reason: "expired" })
    expect(verifyState("another-master-key-0123456789-abcdefghij", s, NOW)).toEqual({ ok: false, reason: "bad-signature" })
    const [payload, sig] = s.split(".")
    const forged = Buffer.from(JSON.stringify({ c: "mcp-vorneo", e: NOW + 1e9, n: "x" })).toString("base64url")
    expect(verifyState(KEY, `${forged}.${sig}`, NOW)).toEqual({ ok: false, reason: "bad-signature" })
    expect(verifyState(KEY, `${payload}`, NOW)).toEqual({ ok: false, reason: "malformed" })
  })
})

describe("buildAuthUrl", () => {
  it("asks for calendar.readonly, offline access and consent", () => {
    const url = new URL(buildAuthUrl({ clientId: CLIENT.clientId, redirectUri: redirectUriFor(`${APP}/`), state: "st" }))
    expect(url.origin + url.pathname).toBe("https://accounts.google.com/o/oauth2/v2/auth")
    expect(Object.fromEntries(url.searchParams)).toMatchObject({
      client_id: CLIENT.clientId,
      redirect_uri: "https://palui.example.com/api/oauth/google/callback",
      response_type: "code",
      scope: "https://www.googleapis.com/auth/calendar.readonly",
      access_type: "offline",
      prompt: "consent",
      state: "st",
    })
  })

  it("googleAuthUrl reads the client id from the stored secrets", () => {
    const db = memoryDb()
    upsertConnector(db, { id: "gcal", manifest, secrets: CLIENT }, KEY)
    const url = new URL(googleAuthUrl(db, "gcal", KEY, APP, NOW))
    expect(url.searchParams.get("client_id")).toBe(CLIENT.clientId)
    expect(verifyState(KEY, url.searchParams.get("state")!, NOW)).toMatchObject({ ok: true, connectorId: "gcal" })
    // The nonce is stored on the connector without changing its config hash.
    const row = getConnector(db, "gcal")!
    expect(typeof row.config.oauthNonce).toBe("string")
    expect(row.config.oauthNonceExp).toBe(NOW + STATE_TTL_MS)
    expect(() => googleAuthUrl(db, "nope", KEY, APP)).toThrow(/No Google Calendar/)
  })
})

describe("handleGoogleCallback", () => {
  function setup() {
    const db = memoryDb()
    upsertConnector(db, { id: "gcal", manifest, secrets: CLIENT }, KEY, 1)
    return db
  }
  const cb = (params: Record<string, string>) => new URL(`${APP}/api/oauth/google/callback?${new URLSearchParams(params)}`)
  // A state issued the way the dashboard does it (nonce stored on the connector).
  const issue = (db: ReturnType<typeof setup>, now = NOW) => new URL(googleAuthUrl(db, "gcal", KEY, APP, now)).searchParams.get("state")!

  it("exchanges the code, stores the refresh token encrypted and learns the email", async () => {
    const db = setup()
    const g = google()
    const res = await handleGoogleCallback(db, cb({ code: "good-code", state: issue(db) }), KEY, APP, g.f, NOW)
    expect(res).toEqual({ ok: true, connectorId: "gcal", detail: "Connected as zaki@example.com" })
    const row = getConnector(db, "gcal")!
    expect(row.config.oauthNonce).toBeUndefined()
    expect(getSecrets(row, KEY)).toEqual({ ...CLIENT, refreshToken: "1//refresh-token" })
    expect(row.secrets).not.toContain("refresh-token")
    expect(row.config.email).toBe("zaki@example.com")
    const exchange = new URLSearchParams(String(g.calls[0]?.init.body))
    expect(exchange.get("redirect_uri")).toBe("https://palui.example.com/api/oauth/google/callback")
    expect(exchange.get("client_secret")).toBe(CLIENT.clientSecret)
  })

  it("refuses a bad or expired state before calling Google", async () => {
    const db = setup()
    const g = google()
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state: "x.y" }), KEY, APP, g.f, NOW)).toMatchObject({ ok: false })
    const old = issue(db, NOW - STATE_TTL_MS - 1)
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state: old }), KEY, APP, g.f, NOW)).toMatchObject({ ok: false, detail: /expired/ })
    expect(g.calls).toHaveLength(0)
  })

  it("reports a cancelled sign-in, a rejected code and a missing refresh token", async () => {
    const db = setup()
    expect(await handleGoogleCallback(db, cb({ error: "access_denied", state: issue(db) }), KEY, APP, google().f, NOW)).toMatchObject({ ok: false, detail: /cancelled/ })
    expect(await handleGoogleCallback(db, cb({ code: "bad", state: issue(db) }), KEY, APP, google().f, NOW)).toMatchObject({ ok: false, detail: /revoked or expired/ })
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state: issue(db) }), KEY, APP, google({ noRefreshToken: true }).f, NOW)).toMatchObject({
      ok: false,
      detail: /refresh token/,
    })
    expect(getSecrets(getConnector(db, "gcal")!, KEY).refreshToken).toBeUndefined()
  })

  it("accepts a state once, and refuses one that was never stored or was replaced", async () => {
    const db = setup()
    const state = issue(db)
    const g = google()
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state }), KEY, APP, g.f, NOW)).toMatchObject({ ok: true })
    const calls = g.calls.length
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state }), KEY, APP, g.f, NOW)).toMatchObject({ ok: false, detail: /already used/ })
    expect(g.calls).toHaveLength(calls)
    // Signed but never issued through the dashboard (no stored nonce).
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state: signState(KEY, "gcal", NOW) }), KEY, APP, g.f, NOW)).toMatchObject({ ok: false, detail: /already used/ })
    // A newer link replaces the older one.
    const first = issue(db)
    const second = issue(db)
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state: first }), KEY, APP, g.f, NOW)).toMatchObject({ ok: false })
    expect(await handleGoogleCallback(db, cb({ code: "good-code", state: second }), KEY, APP, g.f, NOW)).toMatchObject({ ok: false, detail: /already used/ })
  })

  it("refuses a state for a connector of another type", async () => {
    const db = setup()
    upsertConnector(db, { id: "mcp-x", manifest: { id: "mcp", kind: "tools", name: "MCP" } }, KEY)
    const res = await handleGoogleCallback(db, cb({ code: "good-code", state: signState(KEY, "mcp-x", NOW) }), KEY, APP, google().f, NOW)
    expect(res).toMatchObject({ ok: false, detail: /no longer exists/ })
  })
})

describe("tokens and events", () => {
  it("refreshAccessToken posts the refresh grant", async () => {
    const g = google()
    expect(await refreshAccessToken({ ...CLIENT, refreshToken: "1//r" }, g.f, NOW)).toEqual({ accessToken: "ya29.fresh", expiresAt: NOW + 3599_000 })
    expect(new URLSearchParams(String(g.calls[0]?.init.body)).get("grant_type")).toBe("refresh_token")
  })

  it("listEvents drops cancelled events and maps all-day ones", async () => {
    const g = google()
    const events = await listEvents({ accessToken: "t", calendarId: "primary", timeMin: "a", timeMax: "b" }, g.f)
    expect(events.map((e) => [e.id, e.allDay])).toEqual([
      ["e1", false],
      ["e3", true],
    ])
    const url = new URL(g.calls[0]!.url)
    expect(url.pathname).toBe("/calendar/v3/calendars/primary/events")
    expect(url.searchParams.get("singleEvents")).toBe("true")
    expect((g.calls[0]?.init.headers as Record<string, string>).authorization).toBe("Bearer t")
  })
})

describe("resolveRange", () => {
  const iso = (r: ReturnType<typeof resolveRange>) => [r.timeMin.toISO(), r.timeMax.toISO()]

  it("understands keywords in the turn timezone", () => {
    expect(iso(resolveRange("today", undefined, TZ, NOW))).toEqual(["2026-11-10T00:00:00.000+08:00", "2026-11-11T00:00:00.000+08:00"])
    expect(iso(resolveRange(undefined, undefined, TZ, NOW))[0]).toBe("2026-11-10T00:00:00.000+08:00")
    expect(iso(resolveRange("besok", undefined, TZ, NOW))[0]).toBe("2026-11-11T00:00:00.000+08:00")
    expect(iso(resolveRange("week", undefined, TZ, NOW))[1]).toBe("2026-11-17T00:00:00.000+08:00")
  })

  it("treats a date-only `to` as inclusive and caps long ranges", () => {
    expect(iso(resolveRange("2026-11-12", "2026-11-13", TZ, NOW))).toEqual(["2026-11-12T00:00:00.000+08:00", "2026-11-14T00:00:00.000+08:00"])
    const long = resolveRange("2026-01-01", "2026-12-31", TZ, NOW)
    expect(long.timeMax.diff(long.timeMin, "days").days).toBe(31)
    expect(() => resolveRange("kapan-kapan", undefined, TZ, NOW)).toThrow(/Can't read/)
    expect(() => resolveRange("2026-11-12", "2026-11-01", TZ, NOW)).toThrow(/after the start/)
  })

  it("presents events in the turn timezone", () => {
    const e = presentEvent({ id: "1", calendarId: "primary", title: "Call", start: "2026-11-10T01:00:00Z", end: "2026-11-10T02:00:00Z", allDay: false }, TZ)
    expect(e).toMatchObject({ when: "Tue 10 Nov 09:00-10:00", start: "2026-11-10T09:00:00+08:00" })
  })
})

describe("gcal runtime", () => {
  const turn: TurnInfo = { runId: "r", chatId: "c", channel: "telegram", trigger: "message", tz: TZ, now: NOW }
  const cfg = manifest.config.parse({ email: "zaki@example.com" })

  it("test() explains when OAuth hasn't been done", async () => {
    const res = await createGcalRuntime(google().f).test(fakeCtx(cfg, { ...CLIENT }))
    expect(res).toEqual({ ok: false, detail: expect.stringMatching(/sign-in isn't done/) })
  })

  it("test() refreshes and lists one event", async () => {
    const g = google()
    const res = await createGcalRuntime(g.f).test(fakeCtx(cfg, { ...CLIENT, refreshToken: "1//r" }))
    expect(res).toMatchObject({ ok: true, detail: "Connected as zaki@example.com. Next event: Standup" })
    expect(new URL(g.calls[1]!.url).searchParams.get("maxResults")).toBe("1")
  })

  it("test() explains a revoked token", async () => {
    const res = await createGcalRuntime(google({ refresh: "invalid_grant" }).f).test(fakeCtx(cfg, { ...CLIENT, refreshToken: "1//r" }))
    expect(res).toMatchObject({ ok: false, detail: /Connect with Google again/ })
  })

  it("lihat_kalender returns events for today in the turn tz, caching the token", async () => {
    const g = google()
    const running = await createGcalRuntime(g.f).start(fakeCtx(cfg, { ...CLIENT, refreshToken: "1//r" }, { id: "gcal" }))
    const tools = await running.tools!(turn)
    expect(tools.lihat_kalender?.class).toBe("read")
    // Invite titles come from anyone: results taint the turn.
    expect(tools.lihat_kalender?.untrusted).toBe(true)
    const exec = tools.lihat_kalender!.tool.execute!
    const out = (await exec({ from: "today" }, { toolCallId: "1", messages: [] } as never)) as { count: number; events: { title: string; when: string }[] }
    expect(out.count).toBe(2)
    expect(out.events.map((e) => e.title)).toEqual(["Libur", "Standup"])
    const listed = new URL(g.calls.find((c) => c.url.includes("/events"))!.url)
    expect(listed.searchParams.get("timeMin")).toBe("2026-11-09T16:00:00.000Z")
    expect(listed.searchParams.get("timeMax")).toBe("2026-11-10T16:00:00.000Z")
    await exec({}, { toolCallId: "2", messages: [] } as never)
    expect(g.calls.filter((c) => c.url.includes("oauth2")).length).toBe(1)
    expect(await exec({ from: "nanti" }, { toolCallId: "3", messages: [] } as never)).toMatchObject({ error: /Can't read/ })
  })

  it("start() refuses without a refresh token", async () => {
    await expect(createGcalRuntime(google().f).start(fakeCtx(cfg, { ...CLIENT }))).rejects.toThrow(/sign-in isn't done/)
  })

  it("action authUrl uses APP_URL and signs the state for this connector", async () => {
    setConfig({ appUrl: APP, masterKey: KEY })
    const ctx = fakeCtx(cfg, { ...CLIENT, refreshToken: "1//r" }, { id: "gcal" })
    const running = await createGcalRuntime(google().f).start(ctx)
    const res = (await running.action!("authUrl", {})) as { url: string; redirectUri: string }
    expect(res.redirectUri).toBe(`${APP}/api/oauth/google/callback`)
    const state = new URL(res.url).searchParams.get("state")!
    const verified = verifyState(KEY, state)
    expect(verified).toMatchObject({ ok: true, connectorId: "gcal" })
    // The nonce goes to the connector config so the callback can consume it once.
    expect(ctx.patches.at(-1)).toMatchObject({ oauthNonce: verified.ok ? verified.nonce : "?" })
  })
})
