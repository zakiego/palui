import { CircleCheckIcon, CircleXIcon, InfoIcon, LightbulbIcon, TriangleAlertIcon } from "lucide-react"
import type { ComponentProps, ReactNode } from "react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { cn } from "@/lib/cn"

type CalloutType = "info" | "tip" | "idea" | "warn" | "warning" | "error" | "success"

// MDX <Callout>, on shadcn's Alert. The tone colors the icon through the
// Alert's own `*:[svg]` slot, since Alert sets `*:[svg]:text-current` and a
// class on the icon would lose to it. Linear's callout: the fill step, a
// hairline, radius 8, and a flat 16px icon. Only warning, error and success
// carry a hue, on the icon alone; info and idea stay neutral.
const tones = {
  info: { icon: InfoIcon, className: "*:[svg]:text-muted-foreground" },
  idea: { icon: LightbulbIcon, className: "*:[svg]:text-muted-foreground" },
  warning: { icon: TriangleAlertIcon, className: "*:[svg]:text-fd-warning" },
  error: { icon: CircleXIcon, className: "*:[svg]:text-fd-error" },
  success: { icon: CircleCheckIcon, className: "*:[svg]:text-fd-success" },
} as const

function resolve(type: CalloutType): keyof typeof tones {
  if (type === "warn") return "warning"
  if (type === "tip") return "info"
  return type
}

export function Callout({
  type = "info",
  title,
  icon,
  children,
  className,
  ...props
}: Omit<ComponentProps<"div">, "title"> & { type?: CalloutType; title?: ReactNode; icon?: ReactNode }) {
  // MDX props aren't type-checked, so an unknown type falls back to info.
  const tone = tones[resolve(type)] ?? tones.info
  const Icon = tone.icon
  return (
    <Alert
      role="note"
      className={cn(
        "not-prose my-6 gap-y-1 rounded-lg border-(--docs-code-edge) bg-muted px-4 py-3 text-[0.9375rem] has-[>svg]:gap-x-3 *:[svg]:translate-y-[0.1875rem]",
        tone.className,
        className,
      )}
      {...props}
    >
      {icon ?? <Icon aria-hidden />}
      {title ? <AlertTitle className="font-semibold text-foreground">{title}</AlertTitle> : null}
      <AlertDescription className="text-[0.9375rem] leading-relaxed text-(--docs-text-2) [&_a]:text-foreground [&_a]:decoration-(--docs-faint) [&_code]:rounded [&_code]:bg-(--docs-hover) [&_code]:px-1 [&_code]:text-[0.85em] [&_p+p]:mt-3 [&_p:not(:last-child)]:mb-0">
        {children}
      </AlertDescription>
    </Alert>
  )
}
