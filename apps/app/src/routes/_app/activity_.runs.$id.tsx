// One run (a turn): what started it, the numbers, the full error, the prompt it
// read, every tool call with its args and result, and the reply. Opened from
// the Runs tab and the Home runs card.
import { createFileRoute, Link } from "@tanstack/react-router"
import { ArrowLeftIcon, CalendarClockIcon, MessagesSquareIcon, SearchXIcon } from "lucide-react"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { Markdown } from "@/components/markdown"
import { PagePending, SkeletonText } from "@/components/page-pending"
import { ToolTrace } from "@/components/tool-trace"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusText } from "@/components/ui/status-text"
import { getRun } from "@/lib/fns/activity"
import { compact, duration, inZone } from "@/lib/format"
import { useLabel, useT } from "@/lib/i18n"
import { RUN_TONE, runTitle } from "@/lib/runs"

export const Route = createFileRoute("/_app/activity_/runs/$id")({
  loader: ({ params }) => getRun({ data: { id: params.id } }),
  head: () => ({ meta: [{ title: "Run · Palui" }] }),
  component: RunPage,
  pendingComponent: RunPageSkeleton,
  errorComponent: AppError,
})

type Run = NonNullable<Awaited<ReturnType<typeof getRun>>["run"]>

function BackButton() {
  const t = useT()
  return (
    <Button variant="ghost" size="sm" className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/activity" search={{ tab: "runs" }} />}>
      <ArrowLeftIcon data-icon="inline-start" />
      {t("nav.activity")}
    </Button>
  )
}

function RunPage() {
  const { run, timezone } = Route.useLoaderData()
  const t = useT()
  if (!run) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <SearchXIcon />
          </EmptyMedia>
          <EmptyTitle>{t("run.notFound")}</EmptyTitle>
        </EmptyHeader>
        <EmptyContent>
          <BackButton />
        </EmptyContent>
      </Empty>
    )
  }
  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <RunHeader run={run} />
      <RunStats run={run} timezone={timezone} />
      {run.error && (
        // A past error, not a live one: no role="alert", so it isn't announced on every visit.
        <Alert variant="destructive" role="note">
          <AlertDescription>
            <pre className="max-h-80 overflow-auto font-mono text-xs break-words whitespace-pre-wrap">{run.error}</pre>
          </AlertDescription>
        </Alert>
      )}
      {run.prompt.length > 0 && (
        <Card size="sm">
          <CardHeader>
            <CardTitle>{t("run.prompt")}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {run.prompt.map((m) => (
              <ClampText key={m.id} text={m.text} lines={3} className="text-sm break-words whitespace-pre-wrap" />
            ))}
          </CardContent>
        </Card>
      )}
      {run.tools.length > 0 && (
        <Card size="sm">
          <CardHeader>
            <CardTitle className="tabular-nums">
              {t("run.tools")} · {run.tools.length}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <ToolTrace
              limit={20}
              tools={run.tools.map((x) => ({ id: x.id, tool: x.tool, class: x.class, status: x.status as "ok" | "error" | "denied" | "pending", ms: x.ms ?? undefined, preview: x.result ?? undefined, args: x.args }))}
            />
          </CardContent>
        </Card>
      )}
      {(run.reply.length > 0 || run.resultPreview) && (
        <Card size="sm">
          <CardHeader>
            <CardTitle>{t("run.reply")}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {run.reply.length > 0 ? run.reply.map((m) => <Markdown key={m.id} text={m.text} />) : <p className="text-sm break-words whitespace-pre-wrap">{run.resultPreview}</p>}
          </CardContent>
        </Card>
      )}
    </div>
  )
}

function RunHeader({ run }: { run: Run }) {
  const t = useT()
  const label = useLabel()
  const title = runTitle(run, t, label)
  return (
    <div className="flex flex-col gap-3">
      <BackButton />
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 flex-col gap-2">
          <h1 dir="auto" className="line-clamp-3 font-heading text-title break-words" title={title}>
            {title}
          </h1>
          <div className="flex flex-wrap items-center gap-1.5">
            <StatusText tone={RUN_TONE[run.status] ?? "outline"}>{label("run.status", run.status)}</StatusText>
            <Badge variant="outline">{label("run.trigger", run.trigger)}</Badge>
            {run.tainted && <StatusText tone="warning">{t("run.tainted")}</StatusText>}
          </div>
        </div>
        {(run.chatId || run.jobId) && (
          <div className="flex flex-wrap gap-2">
            {run.chatId && (
              <Button variant="outline" size="sm" render={<Link to="/chats" search={{ chat: run.chatId }} />}>
                <MessagesSquareIcon data-icon="inline-start" />
                {t("command.openChat")}
              </Button>
            )}
            {run.jobId && (
              <Button variant="outline" size="sm" render={<Link to="/jobs" />}>
                <CalendarClockIcon data-icon="inline-start" />
                {t("nav.jobs")}
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function RunStats({ run, timezone }: { run: Run; timezone: string }) {
  const t = useT()
  const stats: { label: string; value: string; title?: string; wide?: boolean }[] = [
    { label: t("table.started"), value: inZone(run.startedAt, timezone) },
    { label: t("table.time"), value: duration(run.ms) },
    { label: t("run.steps"), value: String(run.steps) },
    { label: t("run.tokensIn"), value: compact(run.tokensIn), title: run.tokensIn.toLocaleString() },
    { label: t("run.tokensOut"), value: compact(run.tokensOut), title: run.tokensOut.toLocaleString() },
    { label: t("run.tokensCached"), value: compact(run.tokensCached), title: run.tokensCached.toLocaleString() },
    { label: t("table.model"), value: run.model ?? t("runs.noModel"), title: run.model ?? undefined, wide: true },
  ]
  return (
    <Card size="sm">
      <CardContent>
        <dl className="grid grid-cols-2 gap-x-6 gap-y-4 @xl/main:grid-cols-3">
          {stats.filter((s) => s.value).map((s) => (
            <div key={s.label} className={s.wide ? "col-span-full min-w-0" : "min-w-0"}>
              <dt className="text-caption text-muted-foreground">{s.label}</dt>
              <dd className="mt-0.5 font-mono text-sm break-all tabular-nums" title={s.title}>
                {s.value}
              </dd>
            </div>
          ))}
        </dl>
      </CardContent>
    </Card>
  )
}

function RunPageSkeleton() {
  return (
    <PagePending page="nav.activity">
      <div className="flex w-full max-w-3xl flex-col gap-6">
        <Skeleton className="h-7 w-24" />
        <div className="flex flex-col gap-2">
          <SkeletonText className="font-heading text-title" width="w-64" />
          <Skeleton className="h-5 w-40" />
        </div>
        <Skeleton className="h-36" />
        <Skeleton className="h-48" />
      </div>
    </PagePending>
  )
}
