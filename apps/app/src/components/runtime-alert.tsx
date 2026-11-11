// The one message for a stopped runtime, so every page says it the same way.
import { ServerOffIcon } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { useT } from "@/lib/i18n"

export function RuntimeAlert({ className }: { className?: string }) {
  const t = useT()
  return (
    <Alert variant="destructive" className={className}>
      <ServerOffIcon aria-hidden="true" />
      <AlertTitle>{t("runtime.downTitle")}</AlertTitle>
      <AlertDescription>{t("runtime.down")}</AlertDescription>
    </Alert>
  )
}
