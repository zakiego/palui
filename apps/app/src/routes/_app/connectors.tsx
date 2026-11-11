// Connectors page (PLAN 4, 11.4): every connector grouped by kind. Health and control are
// one element: each connector shows its state and is one tap from off. Pause
// never asks; remove (on the connector's page) always does. The layout is the
// Explore pick (PLAN 11.7); `?v=1|2|3` shows the picker to flip between the
// three variants. Adding one happens on /connectors/new, and each connector
// opens on its own page, /connectors/<id>.
import { createFileRoute, Link, redirect, useNavigate, useRouter } from "@tanstack/react-router"
import { PlusIcon, ToggleRightIcon } from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending } from "@/components/page-pending"
import { RuntimeAlert } from "@/components/runtime-alert"
import { DEFAULT_CONNECTOR_LAYOUT, CONNECTOR_LAYOUTS } from "@/components/connectors/layouts"
import { PrototypePicker } from "@/components/connectors/prototype-picker"
import { StatusAnnouncer } from "@/components/connectors/shared"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getConnectorsPage } from "@/lib/fns/connectors"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/connectors")({
  validateSearch: z.object({
    // Old links opened a sheet with ?open=<id>; they now go to the connector's page.
    open: z.string().max(64).optional(),
    // The Explore picker: 1 Cards, 2 Rows, 3 Board. Absent means the pick.
    v: z.number().int().min(1).max(CONNECTOR_LAYOUTS.length).optional().catch(undefined),
  }),
  beforeLoad: ({ search }) => {
    if (search.open) throw redirect({ to: "/connectors/$id", params: { id: search.open }, replace: true })
  },
  loader: () => getConnectorsPage(),
  head: () => ({ meta: [{ title: "Connectors · Palui" }] }),
  component: ConnectorsPage,
  pendingComponent: ConnectorsSkeleton,
  errorComponent: AppError,
})

function ConnectorsPage() {
  const data = Route.useLoaderData()
  const { v } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const router = useRouter()
  const t = useT()

  useLiveRefresh(() => void router.invalidate(), (e) => !!e.connectorId)

  const open = (id: string) => void navigate({ to: "/connectors/$id", params: { id } })

  const visible = data.items
  const layoutIndex = v ? v - 1 : DEFAULT_CONNECTOR_LAYOUT
  const { Layout } = CONNECTOR_LAYOUTS[layoutIndex]

  return (
    <>
      <PageHeader
        title={t("connectors.title")}
        description={t("connectors.description")}
        actions={
          <Button render={<Link to="/connectors/new" />}>
            <PlusIcon data-icon="inline-start" />
            {t("connectors.add")}
          </Button>
        }
      />
      {!data.runtimeUp && <RuntimeAlert />}

      {visible.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ToggleRightIcon />
            </EmptyMedia>
            <EmptyTitle>{t("connectors.empty")}</EmptyTitle>
            <EmptyDescription>{t("connectors.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button render={<Link to="/connectors/new" />}>
              <PlusIcon data-icon="inline-start" />
              {t("connectors.add")}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <div className={cn("flex flex-col gap-6", v && "pb-20")}>
          <Layout key={layoutIndex} items={visible} live={data.live} onOpen={open} />
        </div>
      )}

      <StatusAnnouncer items={visible} />
      {v && (
        <PrototypePicker
          names={CONNECTOR_LAYOUTS.map((l) => l.name)}
          active={layoutIndex}
          onChange={(i) => void navigate({ search: (prev) => ({ ...prev, v: i + 1 }), replace: true })}
        />
      )}
    </>
  )
}

function ConnectorsSkeleton() {
  const { v } = Route.useSearch()
  const { Skeleton: LayoutSkeleton } = CONNECTOR_LAYOUTS[v ? v - 1 : DEFAULT_CONNECTOR_LAYOUT]
  return (
    <PagePending page="nav.connectors">
      <PageHeaderSkeleton title="w-36" description="w-80" action="w-36" />
      <div className="flex flex-col gap-6">
        <LayoutSkeleton />
      </div>
    </PagePending>
  )
}
