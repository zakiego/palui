// One connector's page: setup, Test, Connect, recent events and remove. A new
// connector opens here as a draft (`?type=<type>&preset=<id>`) from
// /connectors/new and keeps the same URL after its first save.
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { ArrowLeftIcon, SearchXIcon } from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ConnectorDetail } from "@/components/connectors/connector-detail"
import { draftTarget, isDraftId, toTarget } from "@/components/connectors/targets"
import { PagePending } from "@/components/page-pending"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getConnectorsPage } from "@/lib/fns/connectors"
import { useT } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { presetFor } from "@/lib/presets"

export const Route = createFileRoute("/_app/connectors_/$id")({
  validateSearch: z.object({
    type: z.string().max(64).optional().catch(undefined),
    preset: z.string().max(64).optional().catch(undefined),
    agent: z.string().max(100).optional().catch(undefined),
  }),
  loader: () => getConnectorsPage(),
  head: ({ loaderData, params }) => {
    const label = loaderData?.items.find((i) => i.id === params.id)?.label ?? "Connector"
    return { meta: [{ title: `${label} · Palui` }] }
  },
  component: ConnectorPage,
  pendingComponent: ConnectorPageSkeleton,
  errorComponent: AppError,
})

function ConnectorPage() {
  const data = Route.useLoaderData()
  const { id } = Route.useParams()
  const { type, preset, agent } = Route.useSearch()
  const router = useRouter()
  const t = useT()

  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === id)

  const item = data.items.find((i) => i.id === id)
  const m = manifestFor(item?.type ?? type ?? "")
  const target = item ? toTarget(item) : m && isDraftId(m, id) ? draftTarget(m, id, presetFor(m, preset), data.agents.some((a) => a.id === agent) ? agent : undefined) : null

  if (!target || !m) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SearchXIcon />
          </EmptyMedia>
          <EmptyTitle>{t("connectors.notFound")}</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" render={<Link to="/connectors" />}>
            <ArrowLeftIcon data-icon="inline-start" />
            {t("nav.connectors")}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }
  return <ConnectorDetail key={target.id} target={target} manifest={m} appUrl={data.appUrl} agents={data.agents} fromAgent={data.agents.find((a) => a.id === agent)} />
}

function ConnectorPageSkeleton() {
  return (
    <PagePending page="nav.connectors">
      <Skeleton className="h-7 w-28" />
      <div className="flex flex-col gap-2">
        <Skeleton className="h-8 w-56" />
        <Skeleton className="h-4 w-80 max-w-full" />
      </div>
      <div className="flex w-full max-w-2xl flex-col gap-4">
        <Skeleton className="h-16" />
        <Skeleton className="h-16" />
        <Skeleton className="h-16" />
      </div>
    </PagePending>
  )
}
