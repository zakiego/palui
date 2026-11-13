// Home (PLAN 11.4): a first-run checklist until it's done, then what needs a
// look, tokens today, the next 3 reminders, the last 5 runs and the connector
// map. Problems come first; the long map sits last on a phone.
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { BellIcon, CircleCheckIcon, CircleIcon, HistoryIcon, ToggleRightIcon } from "lucide-react"
import { Bar, BarChart, Cell, XAxis } from "recharts"
import { toast } from "sonner"

import { AppError } from "@/components/app-error"
import { connectorIcon } from "@/components/connector-icon"
import { ConnectorStatusBadge } from "@/components/connector-status"
import { IconTile } from "@/components/icon-tile"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, SkeletonText, StatusTextSkeleton } from "@/components/page-pending"
import { RuntimeAlert } from "@/components/runtime-alert"
import { ConnectorMap } from "@/components/connectors/connector-map"
import { StatusAnnouncer } from "@/components/connectors/shared"
import { StatusText } from "@/components/ui/status-text"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { compact, duration, errorMessage, inOtherZone, inZone } from "@/lib/format"
import { dismissChecklist, getHome } from "@/lib/fns/home"
import { useCount, useLabel, useLocale, useT, type Key } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { RUN_TONE, runTitle } from "@/lib/runs"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/")({
  loader: () => getHome(),
  head: () => ({ meta: [{ title: "Home · Palui" }] }),
  component: HomePage,
  pendingComponent: HomeSkeleton,
  errorComponent: AppError,
})

type HomeData = Awaited<ReturnType<typeof getHome>>

// The same rule as the header status: an error, or a paused connector that's switched on.
const needsLook = (c: { status: string; enabled: boolean }) => c.status === "error" || (c.enabled && c.status === "paused")

function HomePage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  useLiveRefresh(() => void router.invalidate(), (e) => !!e.connectorId)

  const c = data.checklist
  const requiredDone = c.passkey && c.model && c.channel && c.hi
  const showChecklist = !requiredDone && !c.dismissed
  const attention = data.connectors.filter(needsLook)

  return (
    <>
      <PageHeader title={t("home.title")} />
      {!data.runtimeUp && <RuntimeAlert />}
      {showChecklist && <Checklist checklist={c} />}
      {attention.length > 0 && <AttentionCard connectors={attention} />}
      {/* Two independent columns, so the long connector map never stretches the cards beside it. */}
      <div className="grid items-start gap-6 @4xl/main:grid-cols-[minmax(0,7fr)_minmax(0,5fr)]">
        <div className="grid min-w-0 content-start gap-6">
          <TokensCard tokens={data.tokens} week={data.week} />
          <RemindersCard reminders={data.reminders} timezone={data.timezone} />
          <RunsCard runs={data.runs} timezone={data.timezone} />
        </div>
        <ConnectorSummary connectors={data.connectors} />
        <StatusAnnouncer items={data.connectors} />
      </div>
    </>
  )
}

function Checklist({ checklist }: { checklist: HomeData["checklist"] }) {
  const t = useT()
  const router = useRouter()
  const steps: { done: boolean; label: Key; to: "/settings" | "/connectors" | "/chats"; optional?: boolean }[] = [
    { done: checklist.passkey, label: "home.check.passkey", to: "/settings" },
    { done: checklist.model, label: "home.check.model", to: "/connectors" },
    { done: checklist.channel, label: "home.check.channel", to: "/connectors" },
    { done: checklist.hi, label: "home.check.hi", to: "/chats" },
    { done: checklist.recall, label: "home.check.recall", to: "/connectors", optional: true },
    { done: checklist.mcp, label: "home.check.mcp", to: "/connectors", optional: true },
  ]
  const done = steps.filter((s) => !s.optional && s.done).length
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("home.checklist")}</CardTitle>
        <CardAction>
          <span className="font-mono text-caption text-muted-foreground tabular-nums">
            {done}/4
          </span>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <ItemGroup className="gap-0">
          {steps.map((s) => (
            <Item key={s.label} size="xs" className="rounded-lg" render={<Link to={s.to} />}>
              <ItemMedia>
                {s.done ? <CircleCheckIcon className="text-success" aria-hidden="true" /> : <CircleIcon className="text-muted-foreground" aria-hidden="true" />}
              </ItemMedia>
              <ItemContent>
                <ItemTitle className={s.done ? "text-muted-foreground line-through decoration-from-font" : undefined}>{t(s.label)}</ItemTitle>
              </ItemContent>
              <span className="sr-only">{s.done ? t("home.check.done") : t("home.check.todo")}</span>
            </Item>
          ))}
        </ItemGroup>
        <Button
          variant="ghost"
          size="sm"
          className="self-start"
          onClick={async () => {
            try {
              await dismissChecklist()
              await router.invalidate()
            } catch (err) {
              toast.error(errorMessage(err))
            }
          }}
        >
          {t("home.check.dismiss")}
        </Button>
      </CardContent>
    </Card>
  )
}

type HomeConnector = HomeData["connectors"][number]

const ATTENTION_SHOWN = 3
// One column per problem shown, up to three, so a single problem gets the full row.
const ATTENTION_COLS = ["", "", "@3xl/main:grid-cols-2", "@3xl/main:grid-cols-3"]

// What's broken, first, one tap from its fix. Only rendered when something is.
function AttentionCard({ connectors }: { connectors: HomeConnector[] }) {
  const t = useT()
  const shown = connectors.slice(0, ATTENTION_SHOWN)
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("home.attention")}</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/connectors" />}>
            {t("common.open")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        <ItemGroup className={cn("gap-4 @3xl/main:grid", ATTENTION_COLS[shown.length])}>
          {shown.map((c) => {
            const Icon = connectorIcon(manifestFor(c.type)?.icon)
            return (
              <Item key={c.id} size="sm" variant="muted" className="items-start" render={<Link to="/connectors/$id" params={{ id: c.id }} />}>
                <ItemMedia>
                  <IconTile size="sm" className="border bg-card">
                    <Icon />
                  </IconTile>
                </ItemMedia>
                <ItemContent className="min-w-0">
                  <ItemTitle className="w-full min-w-0">
                    <span dir="auto" className="truncate text-start" title={c.label}>
                      {c.label}
                    </span>
                  </ItemTitle>
                  <ConnectorStatusBadge status={c.status} />
                  {c.statusDetail && (
                    <ItemDescription dir="auto" className="break-words text-start" title={c.statusDetail}>
                      {c.statusDetail}
                    </ItemDescription>
                  )}
                </ItemContent>
              </Item>
            )
          })}
        </ItemGroup>
      </CardContent>
    </Card>
  )
}

function ConnectorSummary({ connectors }: { connectors: HomeConnector[] }) {
  const t = useT()
  const shown = connectors.filter((c) => c.status !== "not_set_up")
  const on = shown.filter((c) => c.status === "on").length
  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>{t("home.connectors")}</CardTitle>
        {shown.length > 0 && <CardDescription className="tabular-nums">{t("home.map.on", { n: on, total: shown.length })}</CardDescription>}
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/connectors" />}>
            {t("common.open")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {shown.length === 0 ? (
          <Empty className="border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ToggleRightIcon />
              </EmptyMedia>
              <EmptyTitle>{t("connectors.empty")}</EmptyTitle>
              <EmptyDescription>{t("connectors.emptyBody")}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button size="sm" render={<Link to="/connectors/new" />}>
                {t("connectors.add")}
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <ConnectorMap connectors={shown} />
        )}
      </CardContent>
    </Card>
  )
}

// An ISO date as a calendar day (no zone shift), in the UI language: "Thu", "Kam".
const INTL_LOCALE = { en: "en-GB", id: "id-ID" } as const
const dayName = (day: string, locale: keyof typeof INTL_LOCALE, opts: Intl.DateTimeFormatOptions) =>
  new Intl.DateTimeFormat(INTL_LOCALE[locale], { ...opts, timeZone: "UTC" }).format(new Date(`${day}T00:00:00Z`))

function TokensCard({ tokens, week }: { tokens: { tokensIn: number; tokensOut: number; runs: number }; week: HomeData["week"] }) {
  const t = useT()
  const count = useCount()
  const locale = useLocale()
  const days = week.map((d) => ({ day: d.day, tokens: d.tokensIn + d.tokensOut }))
  // The svg isn't focusable (no accessibilityLayer), so the week's numbers go in the label.
  const weekLabel = `${t("home.week")}: ${days.map((d) => `${dayName(d.day, locale, { weekday: "short" })} ${compact(d.tokens)}`).join(", ")}`
  const config = { tokens: { label: t("activity.usage"), color: "var(--chart-1)" } } satisfies ChartConfig
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("home.tokens")}</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/activity" search={{ tab: "usage" }} />}>
            {t("activity.usage")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-5 @xl/main:flex-row @xl/main:items-end @xl/main:justify-between">
        <div className="flex min-w-0 flex-col gap-2">
          <p className="font-mono text-display tabular-nums">{compact(tokens.tokensIn + tokens.tokensOut)}</p>
          <p className="flex flex-wrap gap-x-5 gap-y-1 font-mono text-caption text-muted-foreground tabular-nums">
            <span>
              <span className="text-foreground">{compact(tokens.tokensIn)}</span> {t("home.tokensIn")}
            </span>
            <span>
              <span className="text-foreground">{compact(tokens.tokensOut)}</span> {t("home.tokensOut")}
            </span>
            <span>{count(tokens.runs, "count.run", "count.runs")}</span>
          </p>
        </div>
        {/* Seven bars, today in ink and the rest muted, so today reads against the week. */}
        <ChartContainer config={config} className="aspect-auto h-[4.5rem] w-full shrink-0 @xl/main:w-60" role="img" aria-label={weekLabel}>
          <BarChart data={days} margin={{ top: 0, right: 0, bottom: 0, left: 0 }} barCategoryGap={4} accessibilityLayer={false}>
            <XAxis dataKey="day" tickLine={false} axisLine={false} tickMargin={4} height={22} interval={0} tickFormatter={(d: string) => dayName(d, locale, { weekday: "short" })} />
            <ChartTooltip cursor={false} isAnimationActive={false} content={<ChartTooltipContent hideIndicator labelFormatter={(_, p) => dayName(String(p?.[0]?.payload?.day ?? ""), locale, { weekday: "short", day: "numeric", month: "short" })} />} />
            <Bar dataKey="tokens" radius={[6, 6, 0, 0]} minPointSize={3} isAnimationActive={false}>
              {days.map((d, i) => (
                <Cell key={d.day} fill={i === days.length - 1 ? "var(--color-tokens)" : "var(--muted-foreground)"} fillOpacity={i === days.length - 1 ? 1 : 0.45} />
              ))}
            </Bar>
          </BarChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}

const DAY: Intl.DateTimeFormatOptions = { day: "numeric", month: "short" }
const CLOCK: Intl.DateTimeFormatOptions = { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }

function RemindersCard({ reminders, timezone }: { reminders: HomeData["reminders"]; timezone: string }) {
  const t = useT()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("home.reminders")}</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/reminders" />}>
            {t("common.open")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {reminders.length === 0 ? (
          <Empty className="border border-dashed p-6">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <BellIcon />
              </EmptyMedia>
              <EmptyDescription>{t("home.remindersEmpty")}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" size="sm" render={<Link to="/reminders" search={{ new: true }} />}>
                {t("reminders.new")}
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <ItemGroup className="gap-4">
            {reminders.map((r) => {
              const other = inOtherZone(r.fireAt, r.tz, timezone)
              return (
                <Item key={r.id} size="sm" variant="muted" className="items-start" render={<Link to="/reminders" search={{ tab: r.rrule ? "recurring" : "upcoming" }} />}>
                  {/* An agenda: the time in the owner's zone on the left, what it says on the right. */}
                  <ItemMedia className="w-16 flex-col items-start gap-0.5 font-mono tabular-nums">
                    <span className="text-sm font-medium">{inZone(r.fireAt, timezone, CLOCK)}</span>
                    <span className="text-caption text-muted-foreground">{inZone(r.fireAt, timezone, DAY)}</span>
                  </ItemMedia>
                  <ItemContent className="min-w-0">
                    <ItemTitle className="w-full min-w-0">
                      <span dir="auto" className="line-clamp-2 break-words text-start" title={r.text}>
                        {r.text}
                      </span>
                    </ItemTitle>
                    {(other || r.rrule) && (
                      <ItemDescription className="font-mono tabular-nums">
                        {[other, r.rrule ? t("reminders.repeats") : null].filter(Boolean).join(" · ")}
                      </ItemDescription>
                    )}
                  </ItemContent>
                </Item>
              )
            })}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}

function RunsCard({ runs, timezone }: { runs: HomeData["runs"]; timezone: string }) {
  const t = useT()
  const label = useLabel()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("home.runs")}</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/activity" search={{ tab: "runs" }} />}>
            {t("common.open")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {runs.length === 0 ? (
          <Empty className="border border-dashed p-6">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <HistoryIcon />
              </EmptyMedia>
              <EmptyDescription>{t("home.runsEmpty")}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" size="sm" render={<Link to="/chats" />}>
                {t("command.openChat")}
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <ItemGroup className="gap-1">
            {runs.map((r) => (
              <Item key={r.id} size="sm" className="-mx-3 rounded-lg px-3" render={<Link to="/activity/runs/$id" params={{ id: r.id }} />}>
                <ItemContent className="min-w-0">
                  <ItemTitle className="w-full min-w-0">
                    <span dir="auto" className="truncate text-start" title={runTitle(r, t, label)}>
                      {runTitle(r, t, label)}
                    </span>
                  </ItemTitle>
                  {/* The numbers wrap to a second line before they get cut; only the model name truncates. */}
                  <div className="flex min-w-0 flex-wrap gap-x-3 font-mono text-caption text-muted-foreground tabular-nums">
                    <span className="shrink-0">{inZone(r.startedAt, timezone)}</span>
                    <span className="max-w-full min-w-0 truncate" title={r.model ?? undefined}>
                      {r.model ?? t("runs.noModel")}
                    </span>
                    <span className="shrink-0">
                      {t(r.tokensIn + r.tokensOut === 1 ? "home.tokOne" : "home.tokCount", { n: compact(r.tokensIn + r.tokensOut) })}
                      {r.ms != null ? ` · ${duration(r.ms)}` : ""}
                    </span>
                  </div>
                </ItemContent>
                <ItemActions>
                  <StatusText tone={RUN_TONE[r.status] ?? "outline"}>{label("run.status", r.status)}</StatusText>
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}

// The connector map's rows while it loads: a group label, then its connectors.
const MAP_ROWS: { group?: string; name?: string }[] = [{ group: "w-16" }, { name: "w-24" }, { name: "w-28" }, { group: "w-14" }, { name: "w-32" }, { name: "w-20" }, { group: "w-16" }, { name: "w-24" }]

function HomeSkeleton() {
  return (
    <PagePending page="nav.home">
      <PageHeaderSkeleton title="w-24" />
      <div className="grid items-start gap-6 @4xl/main:grid-cols-[minmax(0,7fr)_minmax(0,5fr)]">
        <div className="grid min-w-0 content-start gap-6">
          <Card>
            <CardHeaderSkeleton title="w-28" action="w-16" />
            <CardContent className="flex flex-col gap-5 @xl/main:flex-row @xl/main:items-end @xl/main:justify-between">
              <div className="flex flex-col gap-2">
                <SkeletonText className="text-display" width="w-24" />
                <SkeletonText className="text-caption" width="w-48" />
              </div>
              <Skeleton className="h-[4.5rem] w-full rounded-md @xl/main:w-60" />
            </CardContent>
          </Card>
          <Card>
            <CardHeaderSkeleton title="w-32" action="w-14" />
            <CardContent>
              <ItemListSkeleton rows={3} agenda />
            </CardContent>
          </Card>
          <Card>
            <CardHeaderSkeleton title="w-24" action="w-12" />
            <CardContent>
              <ItemListSkeleton rows={5} status />
            </CardContent>
          </Card>
        </div>
        <Card>
          <CardHeaderSkeleton title="w-28" description="w-20" action="w-14" />
          <CardContent>
            <ul className="@container/map flex flex-col">
              <li className="flex min-h-9 items-center gap-3">
                <Skeleton className="size-9 rounded-lg" />
                <SkeletonText className="text-sm" width="w-12" />
              </li>
              {MAP_ROWS.map((row, i) =>
                row.group ? (
                  <li key={i} className="flex min-h-9 items-center gap-3 pt-2">
                    <span className="w-9 shrink-0" />
                    <SkeletonText className="text-overline" width={row.group} />
                  </li>
                ) : (
                  <li key={i} className="flex min-h-9 items-center gap-3">
                    <span className="w-9 shrink-0" />
                    <span className="flex min-w-0 flex-1 items-center gap-2.5 py-1">
                      <Skeleton className="size-8 rounded-md" />
                      <SkeletonText className="min-w-0 flex-1 text-sm" width={row.name!} />
                      <StatusTextSkeleton className="hidden @sm/map:flex" />
                      <span className="-me-2 flex size-11 shrink-0 items-center justify-center">
                        <Skeleton className="h-[18.4px] w-8 rounded-full" />
                      </span>
                    </span>
                  </li>
                ),
              )}
            </ul>
          </CardContent>
        </Card>
      </div>
    </PagePending>
  )
}

const ITEM_WIDTHS = ["w-3/4", "w-1/2", "w-2/3", "w-3/5", "w-4/5"]
const SUB_WIDTHS = ["w-56", "w-40", "w-48", "w-36", "w-52"]

// Item rows as RemindersCard (agenda: muted rows, time on the left) and RunsCard
// (a title, a mono line under it, a status) render them.
function ItemListSkeleton({ rows, status, agenda }: { rows: number; status?: boolean; agenda?: boolean }) {
  return (
    <ItemGroup className={agenda ? "gap-4" : "gap-1"}>
      {Array.from({ length: rows }, (_, i) => (
        <Item key={i} size="sm" variant={agenda ? "muted" : "default"} className={agenda ? "items-start" : "px-0"}>
          {agenda && (
            <ItemMedia className="w-16 flex-col items-start gap-0.5">
              <SkeletonText className="text-sm" width="w-11" />
              <SkeletonText className="text-caption" width="w-12" />
            </ItemMedia>
          )}
          <ItemContent className="min-w-0">
            <SkeletonText className="text-sm" width={ITEM_WIDTHS[i % ITEM_WIDTHS.length]} />
            <SkeletonText className={agenda ? "text-sm" : "text-caption"} width={SUB_WIDTHS[i % SUB_WIDTHS.length]} />
          </ItemContent>
          {status && (
            <ItemActions>
              <StatusTextSkeleton />
            </ItemActions>
          )}
        </Item>
      ))}
    </ItemGroup>
  )
}

// A CardHeader with a title, an optional description and a small action button.
function CardHeaderSkeleton({ title, description, action }: { title: string; description?: string; action?: string }) {
  return (
    <CardHeader>
      <SkeletonText className="font-heading text-heading" width={title} />
      {description && <SkeletonText className="text-sm" width={description} />}
      {action && (
        <CardAction>
          <Skeleton className={cn("h-7 pointer-coarse:h-9", action)} />
        </CardAction>
      )}
    </CardHeader>
  )
}
