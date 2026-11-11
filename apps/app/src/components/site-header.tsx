// Sticky header: the sidebar toggle, the page title, the global status and ⌘K.
// The status is live over SSE and announced politely once per change, never on
// load or per tick (PLAN 11.5).
import * as React from "react"
import { Link, useRouterState } from "@tanstack/react-router"
import { CircleAlertIcon, CircleCheckIcon, CircleDashedIcon, SearchIcon } from "lucide-react"

import { RouteProgress } from "@/components/route-progress"
import { ThemeToggle } from "@/components/theme-toggle"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { useLiveSummary, type LiveSummary } from "@/hooks/use-live-events"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"
import { pageFor } from "@/lib/pages"

export function statusText(summary: LiveSummary | null, t: ReturnType<typeof useT>): { text: string; tone: "ok" | "attention" | "none" } {
  if (!summary || summary.total === 0) return { text: t("status.none"), tone: "none" }
  const n = summary.attention.length
  if (n === 0) return { text: t("status.allOn"), tone: "ok" }
  return { text: n === 1 ? t("status.attentionOne") : t("status.attention", { n }), tone: "attention" }
}

export function SiteHeader({ onOpenCommand }: { onOpenCommand: () => void }) {
  const t = useT()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const page = pageFor(pathname)
  const summary = useLiveSummary()
  const status = statusText(summary, t)

  // Announce only changes after the first render.
  const [announcement, setAnnouncement] = React.useState("")
  const previous = React.useRef(status.text)
  React.useEffect(() => {
    if (previous.current !== status.text) {
      previous.current = status.text
      setAnnouncement(status.text)
    }
  }, [status.text])

  return (
    <header className="sticky top-0 z-20 flex h-(--header-height) shrink-0 items-center border-b bg-background/85 backdrop-blur-md md:rounded-t-xl">
      <div className="flex w-full min-w-0 items-center gap-2 ps-[max(1rem,env(safe-area-inset-left))] pe-[max(1rem,env(safe-area-inset-right))] md:ps-6 md:pe-6">
        <SidebarTrigger className="-ms-1.5" />
        <Separator orientation="vertical" className="mx-1 h-4 data-vertical:self-auto" />
        <span className="min-w-0 truncate text-sm font-medium">{page ? t(page.label) : t("app.name")}</span>
        <div className="ms-auto flex min-w-0 items-center gap-1.5">
          <Link
            to="/connectors"
            className={cn(
              "status-tone flex min-h-10 min-w-0 max-w-[11rem] items-center gap-1.5 rounded-md px-2 text-sm font-medium hover:bg-muted [&>svg]:size-4 [&>svg]:shrink-0",
              status.tone === "ok" && "text-success-fg",
              status.tone === "attention" && "text-destructive-fg",
              status.tone === "none" && "text-muted-foreground",
            )}
            aria-label={`${status.text}. ${t("nav.connectors")}`}
          >
            {status.tone === "ok" && <CircleCheckIcon aria-hidden="true" />}
            {status.tone === "attention" && <CircleAlertIcon aria-hidden="true" />}
            {status.tone === "none" && <CircleDashedIcon aria-hidden="true" />}
            <span className="truncate">{status.text}</span>
          </Link>
          <Button variant="outline" size="sm" onClick={onOpenCommand} aria-label={t("command.button")} aria-keyshortcuts="Meta+K Control+K" className="max-sm:size-8 max-sm:pointer-coarse:size-10 max-sm:bg-transparent max-sm:px-0 max-sm:shadow-none">
            <SearchIcon data-icon="inline-start" aria-hidden="true" className="max-sm:m-0" />
            <span className="max-sm:hidden">{t("command.button")}</span>
            <Kbd className="max-sm:hidden">⌘K</Kbd>
          </Button>
          <ThemeToggle />
        </div>
      </div>
      <span role="status" aria-live="polite" className="sr-only">
        {announcement}
      </span>
      <RouteProgress />
    </header>
  )
}
