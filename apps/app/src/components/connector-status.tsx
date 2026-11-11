// A connector's state as a word plus an icon, never color alone (PLAN 11.2):
// on = green + check, paused = zinc + pause, error = red + alert, connecting =
// Spinner. The tint crossfades in 150ms when the state changes.
import { CircleAlertIcon, CircleCheckIcon, CircleDashedIcon, CirclePauseIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/spinner"
import { useT, type Key } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type ConnectorStatus = "not_set_up" | "connecting" | "on" | "paused" | "error"

const LOOK: Record<ConnectorStatus, { variant: "success" | "secondary" | "destructive" | "outline"; label: Key }> = {
  on: { variant: "success", label: "status.on" },
  paused: { variant: "secondary", label: "status.paused" },
  error: { variant: "destructive", label: "status.error" },
  connecting: { variant: "outline", label: "status.connecting" },
  not_set_up: { variant: "outline", label: "status.notSetUp" },
}

export function ConnectorStatusBadge({ status, className }: { status: string; className?: string }) {
  const t = useT()
  const s = (status in LOOK ? status : "not_set_up") as ConnectorStatus
  const { variant, label } = LOOK[s]
  return (
    <Badge variant={variant} className={cn("status-tone", className)}>
      {s === "on" && <CircleCheckIcon aria-hidden="true" />}
      {s === "paused" && <CirclePauseIcon aria-hidden="true" />}
      {s === "error" && <CircleAlertIcon aria-hidden="true" />}
      {s === "connecting" && <Spinner aria-hidden="true" />}
      {s === "not_set_up" && <CircleDashedIcon aria-hidden="true" />}
      {t(label)}
    </Badge>
  )
}
