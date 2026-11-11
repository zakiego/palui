// A status as a colored word with an icon, no pill. Badge stays for real
// labels (a kind, a channel, a mode); a state that changes (on, failed,
// overdue, paused) reads as plain text so it doesn't look like a tag.
import type * as React from "react"
import { CircleAlertIcon, CircleCheckIcon, TriangleAlertIcon } from "lucide-react"

import { cn } from "@/lib/utils"

export type StatusTone = "success" | "warning" | "destructive" | "secondary" | "outline"

const TONE: Record<StatusTone, string> = {
  success: "text-success-fg",
  warning: "text-warning-fg",
  destructive: "text-destructive-fg",
  secondary: "text-muted-foreground",
  outline: "text-muted-foreground",
}

const ICON: Partial<Record<StatusTone, React.ReactNode>> = {
  success: <CircleCheckIcon aria-hidden="true" />,
  warning: <TriangleAlertIcon aria-hidden="true" />,
  destructive: <CircleAlertIcon aria-hidden="true" />,
}

export function StatusText({
  tone = "outline",
  icon,
  className,
  children,
  ...props
}: React.ComponentProps<"span"> & { tone?: StatusTone; icon?: React.ReactNode }) {
  return (
    <span
      data-slot="status-text"
      className={cn(
        "status-tone inline-flex w-fit min-w-0 shrink-0 items-center gap-1 text-xs font-medium whitespace-nowrap [&>svg]:pointer-events-none [&>svg]:size-3.5 [&>svg]:shrink-0",
        TONE[tone],
        className,
      )}
      {...props}
    >
      {icon === undefined ? ICON[tone] : icon}
      {children}
    </span>
  )
}
