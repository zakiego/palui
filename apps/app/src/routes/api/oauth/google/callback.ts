// GET /api/oauth/google/callback: the Google Calendar connector's OAuth redirect.
// The signed `state` names the connector and expires after 10 minutes, so this
// route needs no session. On success the refresh token is stored encrypted and
// the browser goes back to the Connectors page.
import { createFileRoute } from "@tanstack/react-router"
import { config, masterKey } from "@palui/core/config"
import { handleGoogleCallback } from "@palui/connectors/gcal/oauth"
import { getDb } from "@/server/db"
import { oauthReturnUrl } from "@/server/oauth-return"
import { runtime } from "@/server/runtime"

export const Route = createFileRoute("/api/oauth/google/callback")({
  server: {
    handlers: {
      GET: async ({ request }: { request: Request }) => {
        const url = new URL(request.url)
        const result = await handleGoogleCallback(getDb(), url, masterKey(), config().appUrl)
        if (result.ok) {
          runtime()
            .reconcile()
            .catch(() => {})
        }
        return new Response(null, { status: 302, headers: { location: oauthReturnUrl(config().appUrl, result) } })
      },
    },
  },
})
