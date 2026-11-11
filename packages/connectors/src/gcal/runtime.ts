import { tool, type Tool } from "ai"
import { DateTime } from "luxon"
import { z } from "zod"
import { config as appConfig, masterKey } from "@palui/core/config"
import { defineRuntime, type ConfigOf, type SecretsOf, type TurnInfo } from "@palui/core/saklar/define"
import { GoogleTokens, listEvents, presentEvent, resolveRange, sortEvents, type CalendarEvent } from "./calendar"
import manifest from "./manifest"
import { buildAuthUrl, GoogleError, issueState, nonceConfig, redirectUriFor } from "./oauth"

type Config = ConfigOf<typeof manifest>
type Secrets = SecretsOf<typeof manifest>

const NOT_CONNECTED = "Google sign-in isn't done yet. Press Connect with Google in the Calendar setup."

function explain(err: unknown): string {
  if (err instanceof GoogleError && err.code === "invalid_grant") return "Google access was revoked or expired. Connect with Google again in the dashboard."
  return (err as Error)?.message ?? String(err)
}

export function lihatKalenderTool(deps: { tokens: GoogleTokens; config: Config; turn: TurnInfo; fetchImpl?: typeof fetch }): Tool {
  return tool({
    description:
      "Read the owner's Google Calendar (read only). `from`/`to` are ISO dates (YYYY-MM-DD) or ISO times, or the words today, tomorrow or week. Without arguments it shows today. Times come back in the owner's timezone.",
    inputSchema: z.object({
      from: z.string().optional().describe('Start: "today", "tomorrow", "week", or an ISO date/time'),
      to: z.string().optional().describe("End (inclusive for dates). Optional"),
    }),
    execute: async ({ from, to }) => {
      const { tz, now } = deps.turn
      let range: ReturnType<typeof resolveRange>
      try {
        range = resolveRange(from, to, tz, now)
      } catch (err) {
        return { error: (err as Error).message }
      }
      try {
        const token = await deps.tokens.get()
        const all: CalendarEvent[] = []
        for (const calendarId of deps.config.calendarIds) {
          all.push(
            ...(await listEvents(
              { accessToken: token, calendarId, timeMin: range.timeMin.toUTC().toISO()!, timeMax: range.timeMax.toUTC().toISO()!, maxResults: 50 },
              deps.fetchImpl,
            )),
          )
        }
        const events = sortEvents(all, tz).map((e) => presentEvent(e, tz))
        return {
          range: { from: range.timeMin.toISO({ suppressMilliseconds: true }), to: range.timeMax.toISO({ suppressMilliseconds: true }), tz },
          count: events.length,
          events,
        }
      } catch (err) {
        if (err instanceof GoogleError && err.status === 401) deps.tokens.clear()
        return { error: explain(err) }
      }
    },
  })
}

function tokensFor(secrets: Secrets, fetchImpl?: typeof fetch): GoogleTokens | null {
  if (!secrets.clientId || !secrets.clientSecret || !secrets.refreshToken) return null
  return new GoogleTokens({ clientId: secrets.clientId, clientSecret: secrets.clientSecret, refreshToken: secrets.refreshToken }, fetchImpl)
}

// Exposed for tests: the runtime with an injectable fetch.
export function createGcalRuntime(fetchImpl?: typeof fetch) {
  return defineRuntime(manifest, {
    async test(ctx) {
      if (!ctx.secrets.clientId || !ctx.secrets.clientSecret) return { ok: false, detail: "Add the OAuth client ID and secret" }
      const tokens = tokensFor(ctx.secrets, fetchImpl)
      if (!tokens) return { ok: false, detail: NOT_CONNECTED }
      try {
        const token = await tokens.get()
        const now = DateTime.now()
        const calendarId = ctx.config.calendarIds[0] ?? "primary"
        const [next] = await listEvents(
          { accessToken: token, calendarId, timeMin: now.toUTC().toISO()!, timeMax: now.plus({ days: 30 }).toUTC().toISO()!, maxResults: 1 },
          fetchImpl,
        )
        const who = ctx.config.email ? `Connected as ${ctx.config.email}` : "Connected"
        return { ok: true, detail: next ? `${who}. Next event: ${next.title}` : `${who}. No events in the next 30 days.`, data: { nextEvent: next ?? null } }
      } catch (err) {
        return { ok: false, detail: explain(err) }
      }
    },

    async start(ctx) {
      const tokens = tokensFor(ctx.secrets, fetchImpl)
      if (!tokens) throw new Error(NOT_CONNECTED)
      return {
        async stop() {},
        async tools(turn) {
          // Event titles and descriptions come from whoever sends an invite, so the
          // results taint the turn like web pages do (PLAN 10).
          return { lihat_kalender: { tool: lihatKalenderTool({ tokens, config: ctx.config, turn, fetchImpl }), class: "read", untrusted: true, title: "Check the calendar" } }
        },
        live() {
          return { email: ctx.config.email ?? null, calendars: ctx.config.calendarIds }
        },
        async action(name) {
          if (name === "authUrl") {
            if (!ctx.secrets.clientId) throw new Error("Save the OAuth client ID first")
            const cfg = appConfig()
            const redirectUri = redirectUriFor(cfg.appUrl)
            const issued = issueState(masterKey(cfg), ctx.id)
            ctx.patchConfig(nonceConfig(issued))
            return { url: buildAuthUrl({ clientId: ctx.secrets.clientId, redirectUri, state: issued.state, loginHint: ctx.config.email }), redirectUri }
          }
          throw new Error(`Unknown action ${name}`)
        },
      }
    },
  })
}

export default createGcalRuntime()
