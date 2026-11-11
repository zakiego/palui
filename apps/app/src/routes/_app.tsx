// The signed-in shell (PLAN 11.3): sidebar-07, header with live status, ⌘K.
// Every route under it requires a session.
import * as React from "react"
import { createFileRoute, Outlet, redirect, useNavigate, useRouter } from "@tanstack/react-router"

import { AppError } from "@/components/app-error"
import { AppSidebar } from "@/components/app-sidebar"
import { CommandMenu } from "@/components/command-menu"
import { SiteHeader } from "@/components/site-header"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import { LiveEventsProvider } from "@/hooks/use-live-events"
import { authClient } from "@/lib/auth-client"
import { getShell } from "@/lib/fns/home"
import { getSession } from "@/lib/fns/session"
import { LocaleProvider, useT } from "@/lib/i18n"

// Dev only: the fixture toggle (break-ui). The import is behind the dev check,
// so a production build never loads or ships it.
const FixtureToggle = import.meta.env.DEV ? React.lazy(() => import("@/components/dev/fixture-toggle")) : null

export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ location }) => {
    const session = await getSession()
    if (!session.viewer) throw redirect({ to: "/login", search: { next: location.href } })
    return { viewer: session.viewer }
  },
  loader: () => getShell(),
  staleTime: 5_000,
  component: AppLayout,
  pendingComponent: ShellSkeleton,
  errorComponent: AppError,
})

// The shell's own layout while the session and status load.
function ShellSkeleton() {
  return (
    <div className="flex min-h-svh bg-sidebar">
      <div className="hidden w-60 shrink-0 flex-col gap-2 p-3 md:flex">
        <Skeleton className="h-10" />
        {[0, 1, 2, 3, 4, 5, 6].map((i) => (
          <Skeleton key={i} className="h-8" />
        ))}
      </div>
      <div className="m-0 flex flex-1 flex-col gap-6 bg-background p-6 md:m-2 md:rounded-xl">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-64 rounded-xl" />
      </div>
    </div>
  )
}

function AppLayout() {
  const shell = Route.useLoaderData()
  return (
    <LocaleProvider locale={shell.uiLanguage}>
      <LiveEventsProvider initial={shell.summary}>
        <Shell />
      </LiveEventsProvider>
    </LocaleProvider>
  )
}

function Shell() {
  const shell = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()
  const t = useT()
  const [commandOpen, setCommandOpen] = React.useState(false)

  const signOut = async () => {
    await authClient.signOut()
    await router.invalidate()
    await navigate({ to: "/login", search: {} })
  }

  return (
    <SidebarProvider style={{ "--sidebar-width": "calc(var(--spacing) * 60)", "--header-height": "calc(var(--spacing) * 14)" } as React.CSSProperties}>
      <a href="#main-content" className="skip-link">
        {t("nav.skip")}
      </a>
      <AppSidebar variant="inset" username={viewer.user.username} onLogout={() => void signOut()} />
      <SidebarInset className="min-w-0">
        <SiteHeader onOpenCommand={() => setCommandOpen(true)} />
        <main
          id="main-content"
          tabIndex={-1}
          data-no-ring=""
          className="@container/main mx-auto flex w-full max-w-[90rem] min-w-0 flex-1 flex-col gap-6 ps-[max(1rem,env(safe-area-inset-left))] pe-[max(1rem,env(safe-area-inset-right))] pt-6 pb-[max(2.5rem,env(safe-area-inset-bottom))] outline-none md:ps-6 md:pe-6 lg:ps-8 lg:pe-8 lg:pt-8"
        >
          <Outlet />
        </main>
      </SidebarInset>
      <CommandMenu open={commandOpen} onOpenChange={setCommandOpen} connectors={shell.connectors} />
      {FixtureToggle && (
        <React.Suspense fallback={null}>
          <FixtureToggle />
        </React.Suspense>
      )}
    </SidebarProvider>
  )
}
