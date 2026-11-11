// One routine, drawn as its steps (components/routines/detail/flow.tsx), with
// its next times, done per item and its log. Editing stays in chat.
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { SearchXIcon } from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { PageHeaderSkeleton, PagePending, SkeletonText } from "@/components/page-pending"
import { FlowLayout } from "@/components/routines/detail/flow"
import { BackButton, type SetRange } from "@/components/routines/detail/parts"
import { Card, CardContent } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getRoutine } from "@/lib/fns/routines"
import { useT } from "@/lib/i18n"

const isoDate = z.string().regex(/^\d{4}-\d{2}-\d{2}$/)

export const Route = createFileRoute("/_app/routines_/$id")({
  validateSearch: z.object({ from: isoDate.optional().catch(undefined), to: isoDate.optional().catch(undefined) }),
  loaderDeps: ({ search }) => ({ from: search.from, to: search.to }),
  loader: ({ params, deps }) => getRoutine({ data: { id: params.id, from: deps.from, to: deps.to } }),
  head: () => ({ meta: [{ title: "Routine · Palui" }] }),
  component: RoutinePage,
  pendingComponent: RoutineSkeleton,
  errorComponent: AppError,
})

function RoutinePage() {
  const { detail, timezone } = Route.useLoaderData()
  const router = useRouter()
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "routines" || e.kind.startsWith("routine"))
  if (!detail) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SearchXIcon />
          </EmptyMedia>
          <EmptyTitle>{t("routines.notFound")}</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <BackButton />
        </EmptyContent>
      </Empty>
    )
  }
  const setRange: SetRange = (next) => void navigate({ search: (p) => ({ ...p, ...next }), replace: true })
  return <FlowLayout detail={detail} timezone={timezone} setRange={setRange} />
}

// The steps column and the activity column, in their final places.
function RoutineSkeleton() {
  return (
    <PagePending page="nav.routines" className="max-w-5xl">
      <Skeleton className="h-7 w-24" />
      <PageHeaderSkeleton title="w-48" description="w-64" />
      <div className="grid items-start gap-6 @4xl/main:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="flex flex-col gap-1">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex gap-4">
              <Skeleton className="mt-2.5 size-10 shrink-0 rounded-full" />
              <Card size="sm" className="flex-1">
                <CardContent className="flex flex-col gap-2">
                  <SkeletonText className="text-overline" width="w-12" />
                  <SkeletonText className="text-sm" width="w-3/4" />
                </CardContent>
              </Card>
            </div>
          ))}
        </div>
        <Card size="sm">
          <CardContent className="flex flex-col gap-2">
            {["w-20", "w-full", "w-full", "w-full"].map((w, i) => (
              <SkeletonText key={i} className="text-sm" width={w} />
            ))}
          </CardContent>
        </Card>
      </div>
    </PagePending>
  )
}
