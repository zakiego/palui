// Where /api/oauth/authorize sends the owner when an MCP client asks to
// connect to /mcp. Signed out, it goes through the login
// page and comes back. Allow or Cancel sends the browser back to the app.
import * as React from "react"
import { createFileRoute, redirect, type ErrorComponentProps } from "@tanstack/react-router"
import { PlugIcon } from "lucide-react"
import { z } from "zod"

import { AuthFrame } from "@/components/auth-frame"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { decideOAuthRequest, getOAuthRequest } from "@/lib/api/mcp"
import { errorMessage } from "@/lib/format"
import { translate } from "@/lib/i18n"
import { currentViewer } from "@/lib/viewer"

const t = (k: Parameters<typeof translate>[1], v?: Record<string, string>) => translate("en", k, v)

export const Route = createFileRoute("/oauth/consent")({
  validateSearch: z.object({ request: z.string().optional() }),
  beforeLoad: async ({ location }) => {
    const viewer = await currentViewer()
    if (!viewer) throw redirect({ to: "/login", search: { next: location.href } })
  },
  loaderDeps: ({ search }) => ({ request: search.request }),
  loader: async ({ deps }) => (deps.request ? getOAuthRequest({ data: { id: deps.request } }) : null),
  head: () => ({ meta: [{ title: "Connect an app · Palui" }] }),
  component: ConsentPage,
  pendingComponent: () => (
    <AuthFrame>
      <Skeleton className="mx-auto h-8 w-56" />
      <Skeleton className="h-40 rounded-xl" />
    </AuthFrame>
  ),
  errorComponent: ({ error }: ErrorComponentProps) => <Gone detail={errorMessage(error)} />,
})

function Gone({ detail }: { detail?: string }) {
  return (
    <AuthFrame>
      <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <PlugIcon />
          </EmptyMedia>
          <EmptyTitle>{t("oauth.goneTitle")}</EmptyTitle>
          <EmptyDescription>{detail ?? t("oauth.goneBody")}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    </AuthFrame>
  )
}

function ConsentPage() {
  const req = Route.useLoaderData()
  const [busy, setBusy] = React.useState<"allow" | "deny" | null>(null)
  const [error, setError] = React.useState<string>()

  if (!req) return <Gone detail={t("oauth.missing")} />

  const decide = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny")
    setError(undefined)
    try {
      const res = await decideOAuthRequest({ data: { id: req.id, allow } })
      // Leaves the dashboard: the app picks up the code (or the refusal).
      window.location.assign(res.redirectTo)
    } catch (err) {
      setError(errorMessage(err))
      setBusy(null)
    }
  }

  return (
    <AuthFrame>
      <Card>
        <CardHeader>
          <CardTitle className="text-balance [overflow-wrap:anywhere]">
            <span dir="auto">{t("oauth.title", { app: req.clientName })}</span>
          </CardTitle>
          <CardDescription className="[overflow-wrap:anywhere]">{t("oauth.body", { app: req.clientName })}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 text-sm text-muted-foreground">
          <p className="[overflow-wrap:anywhere]">{t("oauth.returnsTo", { host: req.redirectHost })}</p>
          <p>{t("oauth.revokeLater")}</p>
          {error && (
            <Alert variant="destructive" role="alert" className="enter-in">
              <AlertTitle>{t("oauth.goneTitle")}</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
        </CardContent>
        <CardFooter className="flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" className="w-full sm:w-auto" disabled={busy !== null} onClick={() => void decide(false)}>
            {busy === "deny" && <Spinner data-icon="inline-start" />}
            {t("oauth.deny")}
          </Button>
          <Button className="w-full sm:w-auto" disabled={busy !== null} onClick={() => void decide(true)}>
            {busy === "allow" && <Spinner data-icon="inline-start" />}
            {t("oauth.allow")}
          </Button>
        </CardFooter>
      </Card>
    </AuthFrame>
  )
}
