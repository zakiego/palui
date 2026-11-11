// Where `palui owner invite` lands (PLAN 10). Accepting signs the owner in server
// side, then the page immediately asks for a passkey and goes to /app.
import * as React from "react"
import { createFileRoute, Link, useRouter, type ErrorComponentProps } from "@tanstack/react-router"
import { FingerprintIcon, LinkIcon } from "lucide-react"

import { AuthFrame, INVITE_COMMAND } from "@/components/auth-frame"
import { CopyButton } from "@/components/copy-button"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { authClient, passkeyCancelled } from "@/lib/auth-client"
import { errorMessage, fullDate } from "@/lib/format"
import { acceptInvite, getInvite } from "@/lib/fns/session"
import { translate } from "@/lib/i18n"
import { toast } from "sonner"

const t = (k: Parameters<typeof translate>[1], v?: Record<string, string>) => translate("en", k, v)

export const Route = createFileRoute("/invite/$token")({
  loader: ({ params }) => getInvite({ data: { token: params.token } }),
  head: () => ({ meta: [{ title: "Claim · Palui" }] }),
  component: InvitePage,
  pendingComponent: () => (
    <AuthFrame>
      <Skeleton className="mx-auto h-8 w-40" />
      <Skeleton className="h-28 rounded-xl" />
    </AuthFrame>
  ),
  errorComponent: ({ error }: ErrorComponentProps) => (
    <AuthFrame>
      <Alert variant="destructive">
        <AlertTitle>{t("invite.invalidTitle")}</AlertTitle>
        <AlertDescription>{errorMessage(error)}</AlertDescription>
      </Alert>
    </AuthFrame>
  ),
})

function InvitePage() {
  const invite = Route.useLoaderData()
  const { token } = Route.useParams()
  const router = useRouter()
  const [step, setStep] = React.useState<"accept" | "passkey">("accept")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<string>()

  const enter = async () => {
    await router.invalidate()
    await router.navigate({ to: "/" })
  }

  if (!invite.ok && step === "accept") {
    return (
      <AuthFrame>
        <Empty className="rounded-xl bg-card shadow-(--shadow-border)">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <LinkIcon />
            </EmptyMedia>
            <EmptyTitle>{t("invite.invalidTitle")}</EmptyTitle>
            <EmptyDescription>
              {invite.problem} {t("invite.invalidBody")} <code className="font-mono break-all">{INVITE_COMMAND}</code>
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row flex-wrap justify-center">
            <CopyButton text={INVITE_COMMAND} label="Copy command" />
            <Button variant="outline" size="sm" render={<Link to="/login" search={{}} />}>
              Log in
            </Button>
          </EmptyContent>
        </Empty>
      </AuthFrame>
    )
  }

  const accept = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await acceptInvite({ data: { token } })
      setStep("passkey")
      // Ask right away: the owner just clicked, so the browser allows the prompt.
      await addPasskey()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const addPasskey = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const res = await authClient.passkey.addPasskey({ name: deviceName() })
      if (res?.error) {
        if (!passkeyCancelled(res.error)) setError(res.error.message ?? "That passkey couldn't be saved. Try again.")
        return
      }
      toast.success(t("invite.passkeyAdded"))
      await enter()
    } catch (err) {
      if (!passkeyCancelled({ message: errorMessage(err) })) setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (step === "passkey") {
    return (
      <AuthFrame>
        <Card>
          <CardHeader>
            <CardTitle>{t("invite.passkeyTitle")}</CardTitle>
            <CardDescription>{t("invite.passkeyBody")}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {error && (
              <Alert variant="destructive" role="alert" className="enter-in">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <Button size="lg" disabled={busy} onClick={() => void addPasskey()}>
              {busy ? <Spinner data-icon="inline-start" /> : <FingerprintIcon data-icon="inline-start" />}
              {t("invite.passkeyAdd")}
            </Button>
            {/* Never disabled: a passkey prompt that hangs must not trap the owner here. */}
            <Button variant="ghost" onClick={() => void enter()}>
              {t("invite.passkeySkip")}
            </Button>
          </CardContent>
        </Card>
      </AuthFrame>
    )
  }

  const recovery = invite.ok && invite.recovery
  return (
    <AuthFrame>
      <div className="flex flex-col gap-1.5 text-center">
        <h1 className="font-heading text-title text-balance">{recovery ? t("invite.recoveryTitle") : t("invite.title")}</h1>
        <p className="text-sm text-pretty text-muted-foreground">
          {recovery ? t("invite.recoverySubtitle") : t("invite.subtitle", { time: invite.ok ? fullDate(invite.expiresAt) : "" })}
        </p>
      </div>
      <Card>
        <CardContent className="flex flex-col gap-3">
          {error && (
            <Alert variant="destructive" role="alert" className="enter-in">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <Button size="lg" disabled={busy} onClick={() => void accept()} autoFocus>
            {busy && <Spinner data-icon="inline-start" />}
            {t("invite.accept")}
          </Button>
        </CardContent>
      </Card>
    </AuthFrame>
  )
}

// A readable default name for the passkey list in Settings.
function deviceName(): string {
  const ua = typeof navigator === "undefined" ? "" : navigator.userAgent
  const os = /iPhone|iPad/.test(ua) ? "iPhone" : /Android/.test(ua) ? "Android" : /Mac/.test(ua) ? "Mac" : /Windows/.test(ua) ? "Windows" : "This device"
  return `${os}, added ${new Date().toISOString().slice(0, 10)}`
}
