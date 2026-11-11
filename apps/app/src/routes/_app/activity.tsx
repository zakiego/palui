// Activity (PLAN 11.4): Events (filter by connector and level), Runs, and Usage
// (tokens per day, model, chat and job). No dollar figures: CLIProxyAPI has no
// usage stats and subscriptions have no per-token cost.
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import { ActivityIcon, BarChart3Icon, HistoryIcon } from "lucide-react"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { SimpleSelect } from "@/components/simple-select"
import { Badge } from "@/components/ui/badge"
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
import { getEvents, getRuns, getUsage } from "@/lib/fns/activity"
import { compact, duration, inZone } from "@/lib/format"
import { useT } from "@/lib/i18n"

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
      <PageHeader title={t("activity.title")} description={t("activity.description")} />
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: (p) => ({ ...p, tab: v as typeof tab }) })} className="gap-4">
        <TabsList>
          <TabsTrigger value="events">{t("activity.events")}</TabsTrigger>
          <TabsTrigger value="runs">{t("activity.runs")}</TabsTrigger>
          <TabsTrigger value="usage">{t("activity.usage")}</TabsTrigger>
        </TabsList>
        <TabsContent value="events">{data.tab === "events" && <EventsTab data={data.events} />}</TabsContent>
        <TabsContent value="runs">{data.tab === "runs" && <RunsTab runs={data.runs.runs} timezone={data.runs.timezone} />}</TabsContent>
        <TabsContent value="usage">{data.tab === "usage" && <UsageTab usage={data.usage} />}</TabsContent>
      </Tabs>
    </>
  )
}

const LEVEL_TONE: Record<Level, "outline" | "secondary" | "warning" | "destructive"> = { debug: "outline", info: "secondary", warn: "warning", error: "destructive" }

function EventsTab({ data }: { data: Awaited<ReturnType<typeof getEvents>> }) {
  const t = useT()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const levels = search.levels ?? []
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <SimpleSelect
          aria-label="Connector"
          className="w-48"
          value={search.connector ?? "all"}
          options={[{ value: "all", label: t("activity.allConnectors") }, ...data.connectors.map((c) => ({ value: c.id, label: c.label }))]}
          onChange={(v) => void navigate({ search: (p) => ({ ...p, connector: v === "all" ? undefined : v }) })}
        />
        <ToggleGroup
          multiple
          variant="outline"
          aria-label="Levels"
          value={levels}
          onValueChange={(v: string[]) => void navigate({ search: (p) => ({ ...p, levels: v.length ? (v as Level[]) : undefined }) })}
        >
          {LEVELS.map((l) => (
            <ToggleGroupItem key={l} value={l} aria-label={`Show ${l}`}>
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
                    <Badge variant={LEVEL_TONE[e.level as Level] ?? "outline"}>{e.level}</Badge>
                  </TableCell>
                  <TableCell className="max-w-0 truncate font-mono text-caption max-md:hidden">{e.connectorId ?? "system"}</TableCell>
                  <TableCell className="max-w-0 whitespace-normal">
                    <span className="break-words">{e.message}</span>
                    <span className="block font-mono text-caption text-muted-foreground md:hidden">
                      {e.connectorId ?? "system"}
                      <span className="sm:hidden"> · {when(e.at, data.timezone)}</span>
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  )
}

const RUN_TONE: Record<string, "success" | "destructive" | "secondary" | "warning" | "outline"> = {
  ok: "success",
  error: "destructive",
  aborted: "secondary",
  budget: "warning",
  awaiting_approval: "warning",
  running: "outline",
}

function RunsTab({ runs, timezone }: { runs: Awaited<ReturnType<typeof getRuns>>["runs"]; timezone: string }) {
  const t = useT()
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
            <TableHead className="w-48 max-lg:hidden">{t("table.model")}</TableHead>
            <TableHead className="w-28 text-end">{t("table.tokens")}</TableHead>
            <TableHead className="w-24 text-end max-sm:hidden">{t("table.time")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((r) => (
            <TableRow key={r.id}>
              <TableCell className="font-mono text-caption tabular-nums max-sm:hidden">{inZone(r.startedAt, timezone)}</TableCell>
              <TableCell className="max-w-0 whitespace-normal">
                <span className="block truncate" dir="auto" title={r.job ?? r.chat ?? r.trigger}>
                  {r.job ?? r.chat ?? r.trigger}
                </span>
                <span className="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
                  <Badge variant={RUN_TONE[r.status] ?? "outline"}>{r.status.replace("_", " ")}</Badge>
                  <Badge variant="outline">{r.trigger}</Badge>
                  {r.tainted && <Badge variant="warning">tainted</Badge>}
                </span>
                {r.error && <span className="mt-1 line-clamp-2 text-caption break-words text-destructive-fg">{r.error}</span>}
                {/* On a phone the Started column folds in under the run. */}
                <span className="mt-1 block font-mono text-caption text-muted-foreground tabular-nums sm:hidden">{inZone(r.startedAt, timezone)}</span>
              </TableCell>
              <TableCell className="max-w-0 truncate font-mono text-caption max-lg:hidden">{r.model ?? "none"}</TableCell>
              <TableCell className="text-end font-mono text-caption tabular-nums">
                {compact(r.tokensIn)} / {compact(r.tokensOut)}
              </TableCell>
              <TableCell className="text-end font-mono text-caption tabular-nums max-sm:hidden">{duration(r.ms)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  )
}

const dayChart = {
  tokensIn: { label: "In", color: "var(--chart-1)" },
  tokensOut: { label: "Out", color: "var(--chart-3)" },
} satisfies ChartConfig

function UsageTab({ usage }: { usage: Awaited<ReturnType<typeof getUsage>> }) {
  const t = useT()
  const total = usage.days.reduce((n, d) => n + d.tokensIn + d.tokensOut, 0)
  const days = usage.days.map((d) => ({ ...d, label: d.day.slice(5) }))
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>{t("activity.perDay")}</CardTitle>
          <CardDescription>
            Last 14 days in {usage.timezone}. <span className="font-mono tabular-nums">{compact(total)}</span> in total.
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
                <YAxis tickLine={false} axisLine={false} width={44} tickFormatter={(v: number) => compact(v)} />
                <ChartTooltip content={<ChartTooltipContent />} />
                <ChartLegend content={<ChartLegendContent />} />
                <Bar dataKey="tokensIn" stackId="t" fill="var(--color-tokensIn)" radius={[0, 0, 0, 0]} isAnimationActive={false} />
                <Bar dataKey="tokensOut" stackId="t" fill="var(--color-tokensOut)" radius={[6, 6, 0, 0]} isAnimationActive={false} />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
      <div className="grid gap-4 @4xl/main:grid-cols-3">
        <Breakdown title={t("activity.perModel")} rows={usage.perModel} mono />
        <Breakdown title={t("activity.perChat")} rows={usage.perChat} />
        <Breakdown title={t("activity.perJob")} rows={usage.perJob} />
      </div>
    </div>
  )
}

function Breakdown({ title, rows, mono }: { title: string; rows: { key: string; tokens: number; runs: number }[]; mono?: boolean }) {
  const max = Math.max(1, ...rows.map((r) => r.tokens))
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing in the last 14 days.</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {rows.map((r) => (
              <li key={r.key} className="flex flex-col gap-1">
                <span className="flex min-w-0 items-baseline justify-between gap-2 text-sm">
                  <span className={mono ? "min-w-0 truncate font-mono text-caption" : "min-w-0 truncate"} title={r.key}>
                    {r.key}
                  </span>
                  <span className="shrink-0 font-mono text-caption text-muted-foreground tabular-nums">
                    {compact(r.tokens)} · {r.runs} runs
                  </span>
                </span>
                <Progress value={Math.round((r.tokens / max) * 100)} aria-label={`${r.key}: ${compact(r.tokens)} tokens`} />
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function ActivitySkeleton() {
  return (
    <>
      <div className="flex flex-col gap-2">
        <Skeleton className="h-8 w-28" />
        <Skeleton className="h-4 w-96 max-w-full" />
      </div>
      <Skeleton className="h-8 w-60" />
      <div className="flex flex-col gap-2 rounded-xl p-2 shadow-(--shadow-border)">
        {[0, 1, 2, 3, 4].map((i) => (
          <Skeleton key={i} className="h-10" />
        ))}
      </div>
    </>
  )
}

