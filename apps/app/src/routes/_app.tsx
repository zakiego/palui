// The signed-in shell (PLAN 11.3): sidebar-07, header with live status, ⌘K.
// Every route under it requires a session.
import * as React from "react"
import { createFileRoute, Outlet, redirect, useNavigate, useRouter } from "@tanstack/react-router"

import { AppError } from "@/components/app-error"
import { AppSidebar } from "@/components/app-sidebar"
import { CommandMenu } from "@/components/command-menu"
import { PAGE_STATUS_ID, PageHeaderSkeleton, PagePending, SkeletonText, StatusTextSkeleton } from "@/components/page-pending"
import { SiteHeader } from "@/components/site-header"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import { LiveEventsProvider } from "@/hooks/use-live-events"
import { authClient } from "@/lib/auth-client"
import { getShell } from "@/lib/fns/home"
import { getSession } from "@/lib/fns/session"
import { LocaleProvider, useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

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

// The shell's own layout while the session and status load: sidebar-07's
// header, two nav groups and the user row, then the inset page with its
// sticky header. It shows on the first visit after sign-in, which is rare, so
// it fades in like a page skeleton does.
const NAV_GROUPS = [
  ["w-12", "w-14", "w-20", "w-10"],
  ["w-24", "w-16", "w-16"],
]

function ShellSkeleton() {
  return (
    <PagePending page="app.name" className="min-h-svh flex-row gap-0 bg-sidebar">
      <div className="hidden w-60 shrink-0 flex-col gap-4 p-2 md:flex">
        <div className="flex items-center gap-2 p-2">
          <Skeleton className="size-8 rounded-lg" />
          <div className="flex flex-col">
            <SkeletonText className="text-sm" width="w-12" />
            <SkeletonText className="text-xs" width="w-20" />
          </div>
        </div>
        {NAV_GROUPS.map((items, g) => (
          <div key={g} className="flex flex-col gap-1 p-2">
            <span className="flex h-8 items-center px-2">
              <Skeleton className="h-2.5 w-12" />
            </span>
            {items.map((w, i) => (
              <span key={i} className="flex h-8 items-center gap-2 px-2">
                <Skeleton className="size-4 rounded-sm" />
                <Skeleton className={cn("h-3", w)} />
              </span>
            ))}
          </div>
        ))}
        <div className="mt-auto flex items-center gap-2 p-2">
          <Skeleton className="size-8 rounded-full" />
          <div className="flex flex-col">
            <SkeletonText className="text-sm" width="w-12" />
            <SkeletonText className="text-xs" width="w-20" />
          </div>
        </div>
      </div>
      <div className="m-0 flex min-w-0 flex-1 flex-col bg-background md:m-2 md:rounded-xl md:shadow-(--shadow-border)">
        <div className="flex h-14 shrink-0 items-center gap-3 border-b px-4 md:px-6">
          <Skeleton className="size-7 rounded-md" />
          <Skeleton className="h-3 w-16" />
          <StatusTextSkeleton className="ms-auto" width="w-24" />
          <Skeleton className="h-8 w-24 max-sm:size-8" />
        </div>
        <div className="mx-auto flex w-full max-w-[90rem] flex-col gap-6 px-4 pt-6 md:px-6 lg:px-8 lg:pt-8">
          <PageHeaderSkeleton />
          <Skeleton className="h-64 rounded-xl" />
        </div>
      </div>
    </PagePending>
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
      {/* PagePending writes "Loading <page>…" here while a page's skeleton shows. */}
      <span id={PAGE_STATUS_ID} role="status" className="sr-only" />
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
