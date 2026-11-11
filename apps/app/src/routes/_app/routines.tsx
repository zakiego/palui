// Routines: every routine across the owner's chats. A row is the trigger (its
// schedule), the action (its icon) and what needs a look: a failing source, a
// connector that's off, a check-in waiting for an answer. Routines are made
// and edited in chat; here they can be paused, resumed or deleted.
import * as React from "react"
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { CircleAlertIcon, MessageCircleQuestionIcon, WorkflowIcon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, SkeletonText } from "@/components/page-pending"
import { actionIcon, ConnectorNotice, RoutineActions, RoutineState, useKindLabel, useRecapText, type RoutineSummary } from "@/components/routines/routine-parts"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemSeparator, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusText } from "@/components/ui/status-text"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getRoutines } from "@/lib/fns/routines"
import { inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/routines")({
  loader: () => getRoutines(),
  head: () => ({ meta: [{ title: "Routines · Palui" }] }),
  component: RoutinesPage,
  pendingComponent: RoutinesSkeleton,
  errorComponent: AppError,
})

function RoutinesPage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  useLiveRefresh(() => void router.invalidate(), (e) => e.connectorId === "routines" || e.kind.startsWith("routine"))

  return (
    <>
      <PageHeader title={t("routines.title")} description={t("routines.description")} />
      {data.routineMode === "flows" && (
        <Alert role="note">
          <WorkflowIcon aria-hidden="true" />
          <AlertDescription>
            {t("routines.handedOver")}{" "}
            <Link to="/flows" className="underline underline-offset-4">
              {t("nav.flows")}
            </Link>
          </AlertDescription>
        </Alert>
      )}
      {data.routines.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <WorkflowIcon />
            </EmptyMedia>
            <EmptyTitle>{t("routines.empty")}</EmptyTitle>
            <EmptyDescription>{t("routines.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button variant="outline" nativeButton={false} render={<Link to="/chats" />}>
              {t("routines.openChat")}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <ItemGroup className="gap-0">
            {data.routines.map((r, i) => (
              <React.Fragment key={r.id}>
                {i > 0 && <ItemSeparator role="none" className="my-0" />}
                <RoutineRow routine={r} timezone={data.timezone} />
              </React.Fragment>
            ))}
          </ItemGroup>
        </Card>
      )}
    </>
  )
}

// A time-of-day label ("09.00") adds nothing under a time that already says it.
const isClock = (label: string) => /^\d{1,2}[.:]\d{2}$/.test(label)

function RoutineRow({ routine: r, timezone }: { routine: RoutineSummary; timezone: string }) {
  const t = useT()
  const kind = useKindLabel()
  const recap = useRecapText()
  const Icon = actionIcon(r.actionKind)
  const next = r.active ? r.next[0] : undefined
  const meta = [r.chat ? r.chat.title : t("routines.deletedChat"), kind("action", r.actionKind), r.recap && r.recap.kind !== "off" ? recap(r.recap) : null].filter(Boolean).join(" · ")

  return (
    // The name's link covers the row (after:inset-0), so the whole row opens the
    // routine and the actions menu still sits on top of it.
    <Item role="listitem" className="relative rounded-none px-5 py-4 hover:bg-muted/50">
      <ItemMedia variant="icon" className="self-start">
        <Icon aria-hidden="true" />
      </ItemMedia>
      <ItemContent className="min-w-0 gap-1">
        <ItemTitle className="w-full min-w-0 flex-wrap gap-x-2">
          <Link to="/routines/$id" params={{ id: r.id }} dir="auto" className="min-w-0 [overflow-wrap:anywhere] outline-none after:absolute after:inset-0 after:rounded-[inherit] focus-visible:after:ring-3 focus-visible:after:ring-ring/50 focus-visible:after:ring-inset">
            {r.name}
          </Link>
          <RoutineState routine={r} timezone={timezone} />
        </ItemTitle>
        <ItemDescription className="line-clamp-2 [overflow-wrap:anywhere] @2xl/main:line-clamp-1">
          {r.schedule ?? <span className="text-destructive-fg">{t("routines.invalidConfig")}</span>}
        </ItemDescription>
        <p className="truncate text-caption text-muted-foreground">
          {meta}
        </p>
        {next && (
          <p className="text-caption text-muted-foreground tabular-nums @2xl/main:hidden">
            {t("routines.next", { time: inZone(next.at, timezone) })}
            {!isClock(next.label) && ` · ${next.label}`}
          </p>
        )}
        <RowNotices routine={r} />
        {r.flow && (
          // Above the row's cover link, so it opens the flow, not the routine.
          <Link to="/flows/$id" params={{ id: r.flow.id }} className="relative z-10 inline-flex items-center gap-1.5 self-start text-caption text-muted-foreground underline-offset-4 hover:underline">
            <WorkflowIcon aria-hidden="true" className="size-3.5" />
            {r.flow.shadow ? t("routines.shadowFlow") : t("routines.asFlow")}
          </Link>
        )}
      </ItemContent>
      {next && (
        <div className="hidden w-40 shrink-0 self-start text-end @2xl/main:block">
          <p className="text-overline text-muted-foreground uppercase">{t("routines.nextUp")}</p>
          <p className="font-mono text-caption tabular-nums">{inZone(next.at, timezone)}</p>
          {!isClock(next.label) && <p className="line-clamp-2 text-caption text-muted-foreground">{next.label}</p>}
        </div>
      )}
      <ItemActions className="relative z-10 self-start">
        <RoutineActions routine={r} timezone={timezone} layout="menu" />
      </ItemActions>
    </Item>
  )
}

// What needs a look: the source failing, its connector off, a check-in waiting.
function RowNotices({ routine: r }: { routine: RoutineSummary }) {
  const t = useT()
  const connector = r.connector && r.connector.state !== "ok" ? r.connector : null
  if (!r.lastError && !r.openCheckins && !connector) return null
  return (
    <span className="mt-2 flex flex-col gap-1.5">
      {r.lastError && (
        <StatusText tone="destructive" icon={<CircleAlertIcon aria-hidden="true" />} className="max-w-full items-start whitespace-normal [&>svg]:mt-0.5">
          <span className="line-clamp-2 [overflow-wrap:anywhere]">
            {t("routines.sourceError")}: {r.lastError}
          </span>
        </StatusText>
      )}
      <ConnectorNotice connector={connector} compact />
      {r.openCheckins > 0 && (
        <StatusText tone="warning" icon={<MessageCircleQuestionIcon aria-hidden="true" />}>
          {r.openCheckins === 1 ? t("routines.waiting") : t("routines.waitingN", { n: r.openCheckins })}
        </StatusText>
      )}
    </span>
  )
}

function RoutinesSkeleton() {
  return (
    <PagePending page="nav.routines">
      <PageHeaderSkeleton title="w-24" description="w-72" />
      <Card className="gap-0 overflow-hidden py-0">
        <ItemGroup className="gap-0">
          {[0, 1, 2, 3].map((i) => (
            <React.Fragment key={i}>
              {i > 0 && <ItemSeparator className="my-0" />}
              <Item className="rounded-none px-5 py-4">
                <ItemMedia variant="icon" className="self-start">
                  <Skeleton className="size-4" />
                </ItemMedia>
                <ItemContent className="gap-1">
                  <SkeletonText className="text-sm" width="w-40" />
                  <SkeletonText className="text-sm" width="w-3/4" />
                  <SkeletonText className="text-caption" width="w-1/2" />
                </ItemContent>
              </Item>
            </React.Fragment>
          ))}
        </ItemGroup>
      </Card>
    </PagePending>
  )
}
