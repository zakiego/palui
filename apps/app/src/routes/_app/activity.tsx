// Activity (PLAN 11.4): Events (filter by connector and level), Runs, and Usage
// (tokens per day, model, chat and job). No dollar figures: CLIProxyAPI has no
// usage stats and subscriptions have no per-token cost.
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import { ActivityIcon, BarChart3Icon, BugIcon, HistoryIcon, InfoIcon } from "lucide-react"
import type { ReactNode } from "react"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { ListCapNote } from "@/components/list-cap-note"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, LineTabsSkeleton, SkeletonButton, SkeletonText, TableSkeleton } from "@/components/page-pending"
import { SimpleSelect } from "@/components/simple-select"
import { Badge } from "@/components/ui/badge"
import { StatusText } from "@/components/ui/status-text"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getEvents, getRuns, getUsage } from "@/lib/api/activity"
import { compact, duration, inZone } from "@/lib/format"
import { useCount, useLabel, useT } from "@/lib/i18n"
import { RUN_TONE, runTitle } from "@/lib/runs"

const LEVELS = ["debug", "info", "warn", "error"] as const
type Level = (typeof LEVELS)[number]

export const Route = createFileRoute("/_app/activity")({
  validateSearch: z.object({
    tab: z.enum(["events", "runs", "usage"]).optional(),
    connector: z.string().max(64).optional(),
    levels: z.array(z.enum(LEVELS)).max(4).optional(),
  }),
  loaderDeps: ({ search }) => ({ tab: search.tab ?? "events", connector: search.connector, levels: search.levels }),
  loader: async ({ deps }) => {
    if (deps.tab === "runs") return { tab: "runs" as const, runs: await getRuns() }
    if (deps.tab === "usage") return { tab: "usage" as const, usage: await getUsage() }
    return { tab: "events" as const, events: await getEvents({ data: { connectorId: deps.connector, levels: deps.levels } }) }
  },
  head: () => ({ meta: [{ title: "Activity · Palui" }] }),
  component: ActivityPage,
  pendingComponent: ActivitySkeleton,
  errorComponent: AppError,
})

// Times use the owner's zone from Settings, not the server's or the browser's,
// so server and client render the same text.
const when = (at: number, tz: string) => inZone(at, tz, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" })

function ActivityPage() {
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const router = useRouter()
  const t = useT()
  const tab = search.tab ?? "events"
  useLiveRefresh(() => void router.invalidate(), () => tab === "events", 800)

  return (
    <>
      <PageHeader title={t("activity.title")} />
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: (p) => ({ ...p, tab: v as typeof tab }) })} className="gap-6">
        <TabsList variant="line" aria-label={t("activity.title")}>
          <TabsTrigger value="events">
            <ActivityIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("activity.events")}
          </TabsTrigger>
          <TabsTrigger value="runs">
            <HistoryIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("activity.runs")}
          </TabsTrigger>
          <TabsTrigger value="usage">
            <BarChart3Icon aria-hidden="true" className="hidden @xl/main:block" />
            {t("activity.usage")}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="events">{data.tab === "events" && <EventsTab data={data.events} />}</TabsContent>
        <TabsContent value="runs">{data.tab === "runs" && <RunsTab runs={data.runs.runs} total={data.runs.total} timezone={data.runs.timezone} />}</TabsContent>
        <TabsContent value="usage">{data.tab === "usage" && <UsageTab usage={data.usage} />}</TabsContent>
      </Tabs>
    </>
  )
}

const LEVEL_TONE: Record<Level, "outline" | "secondary" | "warning" | "destructive"> = { debug: "outline", info: "secondary", warn: "warning", error: "destructive" }
const LEVEL_ICON: Record<string, ReactNode> = { debug: <BugIcon aria-hidden="true" />, info: <InfoIcon aria-hidden="true" /> }

function EventsTab({ data }: { data: Awaited<ReturnType<typeof getEvents>> }) {
  const t = useT()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const levels = search.levels ?? []
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <SimpleSelect
          aria-label={t("table.connector")}
          className="w-48"
          value={search.connector ?? "all"}
          options={[{ value: "all", label: t("activity.allConnectors") }, ...data.connectors.map((c) => ({ value: c.id, label: c.label }))]}
          onChange={(v) => void navigate({ search: (p) => ({ ...p, connector: v === "all" ? undefined : v }) })}
        />
        <ToggleGroup
          multiple
          variant="outline"
          aria-label={t("table.level")}
          value={levels}
          onValueChange={(v: string[]) => void navigate({ search: (p) => ({ ...p, levels: v.length ? (v as Level[]) : undefined }) })}
        >
          {LEVELS.map((l) => (
            <ToggleGroupItem key={l} value={l} aria-label={t("activity.showLevel", { level: l })}>
              {l}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      {data.events.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ActivityIcon />
            </EmptyMedia>
            <EmptyTitle>{t("activity.eventsEmpty")}</EmptyTitle>
            <EmptyDescription>{search.connector || levels.length ? t("activity.eventsEmptyFiltered") : t("activity.eventsEmptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            {search.connector || levels.length ? (
              <Button variant="outline" size="sm" onClick={() => void navigate({ search: (p) => ({ ...p, connector: undefined, levels: undefined }) })}>
                {t("activity.clearFilters")}
              </Button>
            ) : (
              <Button variant="outline" size="sm" render={<Link to="/connectors" />}>
                {t("nav.connectors")}
              </Button>
            )}
          </EmptyContent>
        </Empty>
      ) : (
        <Card className="gap-0 overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-36 max-sm:hidden">{t("table.when")}</TableHead>
                <TableHead className="w-20">{t("table.level")}</TableHead>
                <TableHead className="w-36 max-md:hidden">{t("table.connector")}</TableHead>
                <TableHead>{t("table.message")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.events.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="font-mono text-caption tabular-nums max-sm:hidden">{when(e.at, data.timezone)}</TableCell>
                  <TableCell>
                    <StatusText tone={LEVEL_TONE[e.level as Level] ?? "outline"} icon={LEVEL_ICON[e.level]}>{e.level}</StatusText>
                  </TableCell>
                  <TableCell className="max-w-0 truncate font-mono text-caption max-md:hidden" title={e.connectorId ?? undefined}>
                    {e.connectorId ?? t("activity.system")}
                  </TableCell>
                  <TableCell className="max-w-0 whitespace-normal">
                    {/* A stack trace can run to thousands of characters: three lines, then a toggle. */}
                    <ClampText text={e.message} lines={3} />
                    <span className="block font-mono text-caption break-all text-muted-foreground md:hidden">
                      {e.connectorId ?? t("activity.system")}
                      <span className="sm:hidden"> · {when(e.at, data.timezone)}</span>
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <ListCapNote shown={data.events.length} total={data.total} />
        </Card>
      )}
    </div>
  )
}

function RunsTab({ runs, total, timezone }: { runs: Awaited<ReturnType<typeof getRuns>>["runs"]; total: number; timezone: string }) {
  const t = useT()
  const label = useLabel()
  if (runs.length === 0) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <HistoryIcon />
          </EmptyMedia>
          <EmptyTitle>{t("activity.runsEmpty")}</EmptyTitle>
          <EmptyDescription>{t("activity.runsEmptyBody")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" size="sm" render={<Link to="/chats" />}>
            {t("command.openChat")}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-36 max-sm:hidden">{t("table.started")}</TableHead>
            <TableHead>{t("table.run")}</TableHead>
            <TableHead className="hidden w-56 @5xl/main:table-cell">{t("table.model")}</TableHead>
            <TableHead className="w-28 text-end">{t("table.tokens")}</TableHead>
            <TableHead className="w-24 text-end max-sm:hidden">{t("table.time")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((r) => (
            // The title links to the run; its ::after stretches over the row, so the whole row opens it.
            <TableRow key={r.id} className="relative">
              <TableCell className="font-mono text-caption tabular-nums max-sm:hidden">{inZone(r.startedAt, timezone)}</TableCell>
              <TableCell className="max-w-0 whitespace-normal">
                <Link
                  to="/activity/runs/$id"
                  params={{ id: r.id }}
                  className="block truncate text-left outline-none after:absolute after:inset-0 focus-visible:after:rounded-md focus-visible:after:ring-2 focus-visible:after:ring-ring focus-visible:after:ring-inset"
                  dir="auto"
                >
                  {runTitle(r, t, label)}
                </Link>
                <span className="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
                  <StatusText tone={RUN_TONE[r.status] ?? "outline"}>{label("run.status", r.status)}</StatusText>
                  <Badge variant="outline">{label("run.trigger", r.trigger)}</Badge>
                  {r.tainted && <StatusText tone="warning">{t("run.tainted")}</StatusText>}
                </span>
                {/* Two lines here; the run page shows the whole error. */}
                {r.error && (
                  <span dir="auto" className="mt-1 line-clamp-2 text-caption break-words whitespace-pre-line text-destructive-fg">
                    {r.error}
                  </span>
                )}
                {/* What the hidden columns hold folds in under the run: the model below a wide page, the start and duration on a phone. */}
                <span className="mt-1 block truncate font-mono text-caption text-muted-foreground @5xl/main:hidden">
                  {r.model ?? t("runs.noModel")}
                </span>
                <span className="mt-1 block font-mono text-caption text-muted-foreground tabular-nums sm:hidden">
                  {inZone(r.startedAt, timezone)}
                  {r.ms != null ? ` · ${duration(r.ms)}` : ""}
                </span>
              </TableCell>
              <TableCell className="hidden max-w-0 truncate font-mono text-caption @5xl/main:table-cell">
                {r.model ?? t("runs.noModel")}
              </TableCell>
              <TableCell className="text-end font-mono text-caption tabular-nums">
                {compact(r.tokensIn)} / {compact(r.tokensOut)}
              </TableCell>
              <TableCell className="text-end font-mono text-caption tabular-nums max-sm:hidden">{duration(r.ms)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ListCapNote shown={runs.length} total={total} />
    </Card>
  )
}

function UsageTab({ usage }: { usage: Awaited<ReturnType<typeof getUsage>> }) {
  const t = useT()
  const dayChart = {
    tokensIn: { label: t("home.tokensIn"), color: "var(--chart-1)" },
    tokensOut: { label: t("home.tokensOut"), color: "var(--chart-3)" },
  } satisfies ChartConfig
  const total = usage.days.reduce((n, d) => n + d.tokensIn + d.tokensOut, 0)
  const days = usage.days.map((d) => ({ ...d, label: d.day.slice(5) }))
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>{t("activity.perDay")}</CardTitle>
          <CardDescription>
            {t("activity.last14", { tz: usage.timezone })} <span className="font-mono tabular-nums">{t("activity.inTotal", { n: compact(total) })}</span>
          </CardDescription>
        </CardHeader>
        <CardContent>
          {total === 0 ? (
            <Empty className="p-4">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <BarChart3Icon />
                </EmptyMedia>
                <EmptyDescription>{t("activity.usageEmpty")}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <ChartContainer config={dayChart} className="aspect-auto h-64 w-full">
              <BarChart data={days} accessibilityLayer>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={16} />
                <YAxis tickLine={false} axisLine={false} width={44} allowDecimals={false} tickFormatter={(v: number) => compact(v)} />
                <ChartTooltip content={<ChartTooltipContent />} />
                <ChartLegend content={<ChartLegendContent />} />
                <Bar dataKey="tokensIn" stackId="t" fill="var(--color-tokensIn)" radius={[0, 0, 0, 0]} isAnimationActive={false} />
                <Bar dataKey="tokensOut" stackId="t" fill="var(--color-tokensOut)" radius={[6, 6, 0, 0]} isAnimationActive={false} />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
      <div className="grid gap-4 @4xl/main:grid-cols-2">
        <Breakdown title={t("activity.perAgent")} list={usage.perAgent} unnamed={t("runs.deletedAgent")} />
        <Breakdown title={t("activity.perModel")} list={usage.perModel} mono />
        <Breakdown title={t("activity.perChat")} list={usage.perChat} unnamed={t("runs.deletedChat")} />
        <Breakdown title={t("activity.perJob")} list={usage.perJob} unnamed={t("runs.deletedJob")} />
      </div>
    </div>
  )
}

// `unnamed` is what a row with an empty key is called: runs whose agent, chat or job
// was deleted since group under one.
function Breakdown({ title, list, mono, unnamed }: { title: string; list: { rows: { key: string; tokens: number; runs: number }[]; total: number }; mono?: boolean; unnamed?: string }) {
  const t = useT()
  const count = useCount()
  const { rows, total } = list
  const max = Math.max(1, ...rows.map((r) => r.tokens))
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {total > rows.length && <CardDescription className="tabular-nums">{t("activity.topOf", { shown: rows.length, total })}</CardDescription>}
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("activity.nothing14")}</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {rows.map((r) => {
              const name = r.key || unnamed || r.key
              return (
                <li key={r.key} className="flex flex-col gap-1">
                  <span className="flex min-w-0 items-baseline justify-between gap-2 text-sm">
                    <span dir="auto" className={mono ? "min-w-0 truncate text-left font-mono text-caption" : "min-w-0 truncate text-left"} title={name}>
                      {name}
                    </span>
                    <span className="shrink-0 font-mono text-caption text-muted-foreground tabular-nums">
                      {compact(r.tokens)} · {count(r.runs, "count.run", "count.runs")}
                    </span>
                  </span>
                  <Progress value={Math.round((r.tokens / max) * 100)} aria-label={`${name}: ${compact(r.tokens)} tok`} />
                </li>
              )
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function ActivitySkeleton() {
  const { tab = "events" } = Route.useSearch()
  return (
    <PagePending page="nav.activity">
      <PageHeaderSkeleton title="w-28" />
      <div className="flex flex-col gap-6">
        <LineTabsSkeleton widths={["w-16", "w-10", "w-14"]} />
        {tab === "events" && (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
              <SkeletonButton className="w-48" />
              <SkeletonButton className="w-52" />
            </div>
            <TableSkeleton
              rows={10}
              columns={[
                { className: "w-36 max-sm:hidden", head: "w-10", text: "font-mono text-caption", short: "w-28" },
                { className: "w-20", head: "w-10", status: true, short: "w-8" },
                { className: "w-36 max-md:hidden", head: "w-16", text: "font-mono text-caption", short: "w-20" },
                { head: "w-16" },
              ]}
            />
          </div>
        )}
        {tab === "runs" && (
          <TableSkeleton
            rows={10}
            columns={[
              { className: "w-36 max-sm:hidden", head: "w-12", text: "font-mono text-caption", short: "w-28" },
              { head: "w-8", sub: true },
              { className: "hidden w-56 @5xl/main:table-cell", head: "w-12", text: "font-mono text-caption", short: "w-32" },
              { className: "w-28 text-end", head: "w-12", text: "font-mono text-caption", short: "w-12", align: "end" },
              { className: "w-24 text-end max-sm:hidden", head: "w-8", text: "font-mono text-caption", short: "w-10", align: "end" },
            ]}
          />
        )}
        {tab === "usage" && (
          <div className="flex flex-col gap-4">
            <Card>
              <CardHeader>
                <SkeletonText className="font-heading text-heading" width="w-40" />
                <SkeletonText className="text-sm" width="w-72" />
              </CardHeader>
              <CardContent>
                <Skeleton className="h-64 w-full rounded-lg" />
              </CardContent>
            </Card>
            <div className="grid gap-4 @4xl/main:grid-cols-3">
              {["w-24", "w-20", "w-20"].map((title, i) => (
                <Card key={i}>
                  <CardHeader>
                    <SkeletonText className="font-heading text-heading" width={title} />
                  </CardHeader>
                  <CardContent>
                    <ul className="flex flex-col gap-3">
                      {["w-2/3", "w-1/2", "w-3/5"].map((w, k) => (
                        <li key={k} className="flex flex-col gap-1">
                          <span className="flex items-center justify-between gap-2">
                            <SkeletonText className="min-w-0 flex-1 text-sm" width={w} />
                            <SkeletonText className="text-caption" width="w-20" />
                          </span>
                          <Skeleton className="h-1 w-full rounded-full" />
                        </li>
                      ))}
                    </ul>
                  </CardContent>
                </Card>
              ))}
            </div>
          </div>
        )}
      </div>
    </PagePending>
  )
}

