// Flows (RFC flows 10): read-only. Every flow with its triggers, next time and
// last run. Flows are built and changed from chat, so there are no edit actions.
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { ClockAlertIcon, WorkflowIcon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, TableSkeleton } from "@/components/page-pending"
import { Card } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { StatusText } from "@/components/ui/status-text"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getFlows } from "@/lib/fns/flows"
import { ago, inZone } from "@/lib/format"
import { useCount, useLabel, useT } from "@/lib/i18n"
import { FLOW_RUN_TONE, FlowBadges } from "@/components/flow-badges"

export const Route = createFileRoute("/_app/flows")({
  loader: () => getFlows(),
  head: () => ({ meta: [{ title: "Flows · Palui" }] }),
  component: FlowsPage,
  pendingComponent: FlowsSkeleton,
  errorComponent: AppError,
})

type Data = Awaited<ReturnType<typeof getFlows>>
type Flow = Data["flows"][number]

// An enabled flow this far past its next time means the scheduler isn't running it.
const OVERDUE_AFTER_MS = 2 * 60_000

function FlowsPage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  useLiveRefresh(() => void router.invalidate(), (e) => e.kind.startsWith("flow") || e.connectorId === "flows")

  return (
    <>
      <PageHeader title={t("flows.title")} />
      {data.flows.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <WorkflowIcon />
            </EmptyMedia>
            <EmptyTitle>{t("flows.empty")}</EmptyTitle>
            <EmptyDescription>{t("flows.emptyBody")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("table.flow")}</TableHead>
                <TableHead className="hidden w-48 @4xl/main:table-cell">{t("table.nextRun")}</TableHead>
                <TableHead className="hidden w-56 @3xl/main:table-cell">{t("table.lastRun")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.flows.map((f) => (
                <FlowRow key={f.id} flow={f} timezone={data.timezone} />
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </>
  )
}

function FlowRow({ flow, timezone }: { flow: Flow; timezone: string }) {
  const t = useT()
  const label = useLabel()
  const count = useCount()
  const next = flow.nextAt ? inZone(flow.nextAt, timezone) : null
  const overdue = flow.enabled && flow.nextAt != null && flow.nextAt < Date.now() - OVERDUE_AFTER_MS
  const last = flow.lastRun
  const lastStatus = last && (
    <StatusText tone={FLOW_RUN_TONE[last.status] ?? "outline"} className="max-w-full" title={last.error ?? undefined}>
      <span className="truncate">
        {label("flow.status", last.status)}
        {last.skipReason ? ` · ${label("flow.skip", last.skipReason)}` : ""}
      </span>
    </StatusText>
  )
  const triggers = flow.triggers.map((x) => x.summary).join(" · ")
  return (
    // The name links to the flow; its ::after stretches over the row, so the whole row opens it.
    <TableRow className="relative">
      <TableCell className="max-w-0 whitespace-normal">
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          <Link to="/flows/$id" params={{ id: flow.id }} dir="auto" className="min-w-0 text-left font-medium break-words underline-offset-4 outline-none after:absolute after:inset-0 hover:underline focus-visible:after:rounded-md focus-visible:after:ring-2 focus-visible:after:ring-ring focus-visible:after:ring-inset">
            {flow.name}
          </Link>
          <FlowBadges flow={flow} />
          {/* The last-run column is hidden on a narrow page; a failed run still has to say so. */}
          {last && <span className="inline-flex max-w-full min-w-0 @3xl/main:hidden">{lastStatus}</span>}
        </span>
        {flow.description && (
          <span dir="auto" className="block truncate text-caption text-muted-foreground" title={flow.description}>
            {flow.description}
          </span>
        )}
        <span className="block truncate text-caption text-muted-foreground" title={triggers || undefined}>
          {triggers || label("flow.trigger", "manual")} · {count(flow.steps, "flows.steps.one", "flows.steps.many")}
        </span>
        <span className="flex flex-wrap items-center gap-x-1.5 text-caption text-muted-foreground @4xl/main:hidden">
          <span className="tabular-nums">{next ? t("flows.next", { time: next }) : t("flows.notScheduled")}</span>
          {overdue && (
            <StatusText tone="warning" icon={<ClockAlertIcon aria-hidden="true" />}>
              {t("reminders.overdue")}
            </StatusText>
          )}
        </span>
      </TableCell>
      <TableCell className="hidden align-top @4xl/main:table-cell">
        <span className="flex flex-col gap-1">
          <span className="text-sm tabular-nums">{next ?? <span className="text-muted-foreground">{t("flows.notScheduled")}</span>}</span>
          {overdue && (
            <StatusText tone="warning" icon={<ClockAlertIcon aria-hidden="true" />}>
              {t("reminders.overdue")}
            </StatusText>
          )}
        </span>
      </TableCell>
      <TableCell className="hidden max-w-0 align-top whitespace-normal @3xl/main:table-cell">
        {last ? (
          <span className="flex min-w-0 flex-col gap-1">
            {/* Above the row's cover link, so it opens the run, not the flow. */}
            <Link to="/activity/runs/$id" params={{ id: last.id }} className="relative z-10 inline-flex max-w-full min-w-0">
              {lastStatus}
            </Link>
            <span className="text-caption text-muted-foreground tabular-nums">
              {ago(last.startedAt)} · {count(flow.runs7d, "flows.runs.one", "flows.runs.many")}
            </span>
          </span>
        ) : (
          <span className="text-sm text-muted-foreground">{t("flows.neverRan")}</span>
        )}
      </TableCell>
    </TableRow>
  )
}

function FlowsSkeleton() {
  return (
    <PagePending page="nav.flows">
      <PageHeaderSkeleton />
      <TableSkeleton
        rows={4}
        columns={[
          { head: "w-8", sub: ["", "@4xl/main:hidden"] },
          { className: "hidden w-48 @4xl/main:table-cell", head: "w-14", text: "font-mono text-caption", short: "w-28" },
          { className: "hidden w-56 @3xl/main:table-cell", head: "w-16", status: true, sub: true },
        ]}
      />
    </PagePending>
  )
}
