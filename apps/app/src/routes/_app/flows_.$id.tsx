// One flow (RFC flows 10), read-only, in tabs kept in ?tab=: Steps (its
// triggers, the steps as a graph with their branches, what it may change),
// Runs (each opens its step log on the run page, then the saved state), and
// Versions (what each version changed).
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import { ArrowLeftIcon, CircleAlertIcon, GitCommitVerticalIcon, HistoryIcon, MessagesSquareIcon, SearchXIcon, WorkflowIcon } from "lucide-react"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { PagePending, SkeletonText } from "@/components/page-pending"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusText } from "@/components/ui/status-text"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getFlowFn } from "@/lib/api/flows"
import { ago, inZone } from "@/lib/format"
import { useLabel, useT } from "@/lib/i18n"
import { FLOW_RUN_TONE, FlowBadges } from "@/components/flow-badges"
import { FlowGraph } from "@/components/flow-graph"

const TABS = ["steps", "runs", "versions"] as const
type Tab = (typeof TABS)[number]

export const Route = createFileRoute("/_app/flows_/$id")({
  validateSearch: z.object({ tab: z.enum(TABS).optional() }),
  loader: ({ params }) => getFlowFn({ data: { id: params.id } }),
  head: () => ({ meta: [{ title: "Flow · Palui" }] }),
  component: FlowPage,
  pendingComponent: FlowPageSkeleton,
  errorComponent: AppError,
})

type Flow = NonNullable<Awaited<ReturnType<typeof getFlowFn>>["flow"]>

function BackButton() {
  const t = useT()
  return (
    <Button variant="ghost" size="sm" className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/flows" />}>
      <ArrowLeftIcon data-icon="inline-start" />
      {t("nav.flows")}
    </Button>
  )
}

function FlowPage() {
  const { flow, timezone } = Route.useLoaderData()
  const router = useRouter()
  const navigate = useNavigate({ from: Route.fullPath })
  const tab: Tab = Route.useSearch().tab ?? "steps"
  const t = useT()
  useLiveRefresh(() => void router.invalidate(), (e) => e.kind.startsWith("flow"))
  if (!flow) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SearchXIcon />
          </EmptyMedia>
          <EmptyTitle>{t("flow.notFound")}</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <BackButton />
        </EmptyContent>
      </Empty>
    )
  }
  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <FlowHeader flow={flow} />
      {flow.lastError && (
        <Alert variant="destructive" role="note">
          <CircleAlertIcon aria-hidden="true" />
          <AlertTitle>{t("flow.lastError")}</AlertTitle>
          <AlertDescription>
            <span className="break-words">{flow.lastError}</span>
          </AlertDescription>
        </Alert>
      )}
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: { tab: v as Tab }, replace: true, resetScroll: false })} className="gap-6">
        <TabsList variant="line" aria-label={t("flow.sections")}>
          <TabsTrigger value="steps">
            <WorkflowIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("flow.steps")}
            <span className="font-normal text-muted-foreground tabular-nums">{flow.steps.length.toLocaleString()}</span>
          </TabsTrigger>
          <TabsTrigger value="runs">
            <HistoryIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("flow.runs")}
          </TabsTrigger>
          <TabsTrigger value="versions">
            <GitCommitVerticalIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("flow.versions")}
            <span className="font-normal text-muted-foreground tabular-nums">{flow.versions.length.toLocaleString()}</span>
          </TabsTrigger>
        </TabsList>
        <TabsContent value="steps" className="flex flex-col gap-6 text-base">
          <Triggers flow={flow} timezone={timezone} />
          <Steps flow={flow} />
          {flow.writeCalls.length > 0 && (
            <Card size="sm">
              <CardHeader>
                <CardTitle className="flex flex-wrap items-center gap-2">
                  {t("flow.writeCalls")}
                  <StatusText tone={flow.approval === "approved" ? "success" : "warning"}>{flow.approval === "approved" ? t("flow.approved") : t("flow.notApproved")}</StatusText>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <ul className="flex flex-col gap-1.5">
                  {flow.writeCalls.map((w) => (
                    <li key={w} className="font-mono text-xs break-all">
                      {w}
                    </li>
                  ))}
                </ul>
              </CardContent>
            </Card>
          )}
        </TabsContent>
        <TabsContent value="runs" className="flex flex-col gap-6 text-base">
          <Runs flow={flow} timezone={timezone} />
          {flow.state && (
            <Card size="sm">
              <CardHeader>
                <CardTitle>{t("flow.state")}</CardTitle>
              </CardHeader>
              <CardContent>
                <pre className="max-h-64 overflow-auto font-mono text-xs break-words whitespace-pre-wrap">{flow.state}</pre>
              </CardContent>
            </Card>
          )}
        </TabsContent>
        <TabsContent value="versions" className="flex flex-col gap-6 text-base">
          <Versions flow={flow} timezone={timezone} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function FlowHeader({ flow }: { flow: Flow }) {
  const t = useT()
  return (
    <div className="flex flex-col gap-3">
      <BackButton />
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 flex-col gap-2">
          <h1 dir="auto" className="line-clamp-3 font-heading text-title break-words" title={flow.name}>
            {flow.name}
          </h1>
          <div className="flex flex-wrap items-center gap-1.5">
            <Badge variant="outline">{t("flow.version", { n: String(flow.version) })}</Badge>
            <FlowBadges flow={flow} />
          </div>
          {flow.description && (
            <p dir="auto" className="text-sm break-words text-muted-foreground">
              {flow.description}
            </p>
          )}
        </div>
        <Button variant="outline" size="sm" render={<Link to="/chats" search={{ chat: flow.chatId }} />}>
          <MessagesSquareIcon data-icon="inline-start" />
          <span className="max-w-48 truncate">{flow.chatTitle || t("runs.deletedChat")}</span>
        </Button>
      </div>
    </div>
  )
}

function Triggers({ flow, timezone }: { flow: Flow; timezone: string }) {
  const t = useT()
  const label = useLabel()
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>{t("flow.triggers")}</CardTitle>
      </CardHeader>
      <CardContent>
        <ul className="flex flex-col gap-3">
          {flow.triggers.map((tr) => (
            <li key={tr.id} className="flex min-w-0 flex-col gap-0.5">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="font-mono text-sm">{tr.id}</span>
                <Badge variant="outline">{label("flow.trigger", tr.kind)}</Badge>
              </span>
              <span dir="auto" className="text-sm break-words text-muted-foreground">
                {tr.summary}
              </span>
              <span className="text-caption text-muted-foreground tabular-nums">
                {tr.nextAt ? t("flows.next", { time: inZone(tr.nextAt, timezone) }) : t("flows.notScheduled")}
              </span>
              {tr.lastError && <span className="text-caption break-words text-destructive">{tr.lastError}</span>}
            </li>
          ))}
          <li className="flex flex-wrap items-center gap-1.5">
            <span className="font-mono text-sm">manual</span>
            <Badge variant="outline">{label("flow.trigger", "manual")}</Badge>
          </li>
        </ul>
      </CardContent>
    </Card>
  )
}

// The definition as a graph (components/flow-graph.tsx): triggers on top,
// branches as labelled arrows, and the picked step in full below it.
function Steps({ flow }: { flow: Flow }) {
  return (
    <Card size="sm">
      <CardContent>
        <FlowGraph steps={flow.steps} triggers={flow.triggers} />
      </CardContent>
    </Card>
  )
}

function Runs({ flow, timezone }: { flow: Flow; timezone: string }) {
  const t = useT()
  const label = useLabel()
  return (
    <Card size="sm">
      <CardContent>
        {flow.runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("flow.noRuns")}</p>
        ) : (
          <ul className="flex flex-col divide-y">
            {flow.runs.map((r) => (
              <li key={r.id} className="py-2 first:pt-0 last:pb-0">
                <Link to="/activity/runs/$id" params={{ id: r.id }} className="flex min-w-0 flex-col gap-0.5 rounded-sm underline-offset-4 hover:underline">
                  <span className="flex flex-wrap items-center gap-1.5">
                    <StatusText tone={FLOW_RUN_TONE[r.status] ?? "outline"}>
                      {label("flow.status", r.status)}
                      {r.skipReason ? ` · ${label("flow.skip", r.skipReason)}` : ""}
                    </StatusText>
                    <Badge variant="outline">{label("flow.trigger", r.triggerKind)}</Badge>
                    {r.label && (
                      <span dir="auto" className="min-w-0 truncate text-sm">
                        {r.label}
                      </span>
                    )}
                    {r.answer && <span className="font-mono text-caption text-muted-foreground">{r.answer}</span>}
                  </span>
                  <span className="text-caption text-muted-foreground tabular-nums" title={inZone(r.startedAt, timezone)}>
                    {inZone(r.scheduledAt ?? r.startedAt, timezone)} · {ago(r.startedAt)}
                  </span>
                  {r.error && <span className="line-clamp-2 text-caption break-words text-destructive">{r.error}</span>}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function Versions({ flow, timezone }: { flow: Flow; timezone: string }) {
  const t = useT()
  const by = (s: string) => (s.startsWith("agent:") ? t("flow.by.agent") : s === "migration" ? t("flow.by.migration") : t("flow.by.owner"))
  return (
    <Card size="sm">
      <CardContent>
        <ul className="flex flex-col divide-y">
          {flow.versions.map((v) => (
            <li key={v.n} className="flex min-w-0 flex-col gap-0.5 py-2 first:pt-0 last:pb-0">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="text-sm font-medium tabular-nums">{t("flow.version", { n: String(v.n) })}</span>
                {v.current && <StatusText tone="secondary" icon={null}>{t("flow.current")}</StatusText>}
                {v.writes > 0 && <StatusText tone={v.approved ? "success" : "warning"}>{v.approved ? t("flow.approved") : t("flow.notApproved")}</StatusText>}
                <span className="text-caption text-muted-foreground tabular-nums">
                  {by(v.createdBy)} · {inZone(v.createdAt, timezone)}
                </span>
              </span>
              {v.note && (
                <span dir="auto" className="text-sm break-words">
                  {v.note}
                </span>
              )}
              {v.diff.length > 0 && <span className="font-mono text-caption break-words text-muted-foreground">{v.diff.join(" · ")}</span>}
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

function FlowPageSkeleton() {
  return (
    <PagePending page="nav.flows">
      <div className="flex w-full max-w-3xl flex-col gap-6">
        <Skeleton className="h-7 w-24" />
        <div className="flex flex-col gap-2">
          <SkeletonText className="font-heading text-title" width="w-64" />
          <Skeleton className="h-5 w-40" />
        </div>
        <Skeleton className="h-10" />
        <Skeleton className="h-32" />
        <Skeleton className="h-64" />
      </div>
    </PagePending>
  )
}
