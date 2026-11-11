// Passkey-only login (PLAN 10). A lost passkey is recovered with a new owner
// link from the CLI, which the page says.
import * as React from "react"
import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { FingerprintIcon } from "lucide-react"
import { z } from "zod"

import { AuthFrame, INVITE_COMMAND } from "@/components/auth-frame"
import { CopyButton } from "@/components/copy-button"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardFooter } from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { authClient, authErrorMessage, passkeyCancelled } from "@/lib/auth-client"
import { errorMessage } from "@/lib/format"
import { getSession } from "@/lib/fns/session"
import { translate } from "@/lib/i18n"

const t = (k: Parameters<typeof translate>[1]) => translate("en", k)

export const Route = createFileRoute("/login")({
  validateSearch: z.object({ next: z.string().optional() }),
  beforeLoad: async () => {
    const session = await getSession()
    if (session.viewer) throw redirect({ to: "/" })
    return { configured: session.configured, ownerExists: session.ownerExists }
  },
  head: () => ({ meta: [{ title: "Log in · Palui" }] }),
  component: LoginPage,
})

// Only same-site paths, never an absolute URL from the query string.
export function safeNext(next: string | undefined) {
  return next?.startsWith("/") && !next.startsWith("//") ? next : "/"
}

function LoginPage() {
  const { configured, ownerExists } = Route.useRouteContext()
  const { next } = Route.useSearch()
  const router = useRouter()
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  const signIn = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const res = await authClient.signIn.passkey()
      if (res?.error) {
        if (!passkeyCancelled(res.error)) setError(authErrorMessage(res.error, t("login.failed")))
        return
      }
      await router.invalidate()
      await router.navigate({ href: safeNext(next) })
    } catch (err) {
      if (!passkeyCancelled({ message: errorMessage(err) })) setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthFrame>
      <div className="flex flex-col gap-1.5 text-center">
        <h1 className="font-heading text-title">{t("login.title")}</h1>
        <p className="text-sm text-pretty text-muted-foreground">{t("login.subtitle")}</p>
      </div>

      {!configured && (
        <Alert variant="destructive">
          <AlertTitle>{t("login.notConfigured")}</AlertTitle>
          <AlertDescription>{t("login.notConfiguredBody")}</AlertDescription>
        </Alert>
      )}
      {configured && !ownerExists && (
        <Alert>
          <AlertTitle>{t("login.noOwner")}</AlertTitle>
          <AlertDescription>
            {t("login.noOwnerBody")} <code>{INVITE_COMMAND}</code>
          </AlertDescription>
        </Alert>
      )}

      <Card>
        <CardContent className="flex flex-col gap-4">
          {error && (
            <Alert variant="destructive" role="alert" className="enter-in">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <Button size="lg" disabled={busy || !ownerExists} onClick={() => void signIn()}>
            {busy ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
            {t("login.passkey")}
          </Button>
        </CardContent>
        <CardFooter className="flex-col items-start gap-2">
          <p className="text-caption text-pretty text-muted-foreground">
            {t("login.lockedOut")} <code className="font-mono">{INVITE_COMMAND}</code> {t("login.lockedOutAfter")}
          </p>
          <CopyButton text={INVITE_COMMAND} label="Copy command" size="sm" variant="ghost" />
        </CardFooter>
      </Card>
    </AuthFrame>
  )
}
