// Home (PLAN 11.4): a first-run checklist until it's done, then a connector
// summary, the next 3 reminders, the last 5 runs and tokens today.
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { BellIcon, CircleCheckIcon, CircleIcon, HistoryIcon, ToggleRightIcon } from "lucide-react"
import { toast } from "sonner"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending, SkeletonText } from "@/components/page-pending"
import { RuntimeAlert } from "@/components/runtime-alert"
import { ConnectorMap } from "@/components/connectors/connector-map"
import { StatusAnnouncer } from "@/components/connectors/shared"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { compact, duration, errorMessage, inZone } from "@/lib/format"
import { dismissChecklist, getHome } from "@/lib/fns/home"
import { useT, type Key } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/")({
  loader: () => getHome(),
  head: () => ({ meta: [{ title: "Home · Palui" }] }),
  component: HomePage,
  pendingComponent: HomeSkeleton,
  errorComponent: AppError,
})

type HomeData = Awaited<ReturnType<typeof getHome>>

function HomePage() {
  const data = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  useLiveRefresh(() => void router.invalidate(), (e) => !!e.connectorId)

  const c = data.checklist
  const requiredDone = c.passkey && c.model && c.channel && c.hi
  const showChecklist = !requiredDone && !c.dismissed

  return (
    <>
      <PageHeader title={t("home.title")} description={t("home.description")} />
      {!data.runtimeUp && <RuntimeAlert />}
      {showChecklist && <Checklist checklist={c} />}
      <div className="grid gap-4 @3xl/main:grid-cols-2">
        <ConnectorSummary connectors={data.connectors} />
        <StatusAnnouncer items={data.connectors} />
        <div className="grid content-start gap-4">
          <TokensCard tokens={data.tokens} />
          <RemindersCard reminders={data.reminders} />
        </div>
        <RunsCard runs={data.runs} timezone={data.timezone} />
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
        <CardDescription>{t("home.checklistDescription")}</CardDescription>
        <CardAction>
          <Badge variant="outline" className="font-mono tabular-nums">
            {done}/4
          </Badge>
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
              <span className="sr-only">{s.done ? "Done" : "To do"}</span>
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

function ConnectorSummary({ connectors }: { connectors: HomeConnector[] }) {
  const t = useT()
  const shown = connectors.filter((c) => c.status !== "not_set_up")
  const on = shown.filter((c) => c.status === "on").length
  return (
    <Card className="@3xl/main:row-span-2">
      <CardHeader>
        <CardTitle>{t("home.connectors")}</CardTitle>
        {shown.length > 0 && <CardDescription className="tabular-nums">{t("home.map.on", { n: on, total: shown.length })}</CardDescription>}
        <CardAction>
          <Button variant="outline" size="sm" render={<Link to="/connectors" />}>
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
              <Button size="sm" render={<Link to="/connectors" />}>
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

function TokensCard({ tokens }: { tokens: { tokensIn: number; tokensOut: number; runs: number } }) {
  const t = useT()
  return (
    <Card>
      <CardHeader>
        <CardDescription>{t("home.tokens")}</CardDescription>
        <CardTitle className="font-mono text-display tabular-nums">{compact(tokens.tokensIn + tokens.tokensOut)}</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/activity" search={{ tab: "usage" }} />}>
            {t("activity.usage")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-caption text-muted-foreground tabular-nums">
        <span>
          {compact(tokens.tokensIn)} {t("home.tokensIn")}
        </span>
        <span>
          {compact(tokens.tokensOut)} {t("home.tokensOut")}
        </span>
        <span>{tokens.runs} runs</span>
      </CardContent>
    </Card>
  )
}

function RemindersCard({ reminders }: { reminders: HomeData["reminders"] }) {
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
          <Empty className="p-4">
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
          <ItemGroup className="gap-0">
            {reminders.map((r) => (
              <Item key={r.id} size="sm" className="px-0">
                <ItemContent className="min-w-0">
                  <ItemTitle className="w-full min-w-0">
                    <span className="line-clamp-2 break-words">{r.text}</span>
                  </ItemTitle>
                  <ItemDescription className="font-mono tabular-nums">
                    {inZone(r.fireAt, r.tz)}
                    {r.rrule ? " · repeats" : ""}
                  </ItemDescription>
                </ItemContent>
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}

const RUN_TONE: Record<string, "success" | "destructive" | "secondary" | "outline" | "warning"> = {
  ok: "success",
  error: "destructive",
  aborted: "secondary",
  running: "outline",
  awaiting_approval: "warning",
  budget: "warning",
}

function RunsCard({ runs, timezone }: { runs: HomeData["runs"]; timezone: string }) {
  const t = useT()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("home.runs")}</CardTitle>
        <CardAction>
          <Button variant="ghost" size="sm" render={<Link to="/activity" search={{ tab: "runs" }} />}>
            {t("activity.runs")}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {runs.length === 0 ? (
          <Empty className="p-4">
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
          <ItemGroup className="gap-0">
            {runs.map((r) => (
              <Item key={r.id} size="sm" className="px-0">
                <ItemContent className="min-w-0">
                  <ItemTitle className="w-full min-w-0">
                    <span className="truncate">{r.job ?? r.chat ?? r.trigger}</span>
                  </ItemTitle>
                  <ItemDescription className="truncate font-mono tabular-nums">
                    {inZone(r.startedAt, timezone)} · {r.model ?? "no model"} · {compact(r.tokensIn + r.tokensOut)} tok{r.ms ? ` · ${duration(r.ms)}` : ""}
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <Badge variant={RUN_TONE[r.status] ?? "outline"}>{r.status.replace("_", " ")}</Badge>
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
      <PageHeaderSkeleton title="w-24" description="w-80" />
      <div className="grid gap-4 @3xl/main:grid-cols-2">
        <Card className="@3xl/main:row-span-2">
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
                      <Skeleton className="hidden h-5 w-12 rounded-full @sm/map:block" />
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
        <div className="grid content-start gap-4">
          <Card>
            <CardHeader>
              <SkeletonText className="text-sm" width="w-24" />
              <SkeletonText className="text-display" width="w-24" />
              <CardAction>
                <Skeleton className="h-7 w-16 pointer-coarse:h-9" />
              </CardAction>
            </CardHeader>
            <CardContent>
              <SkeletonText className="text-caption" width="w-48" />
            </CardContent>
          </Card>
          <Card>
            <CardHeaderSkeleton title="w-32" action="w-14" />
            <CardContent>
              <ItemListSkeleton rows={3} />
            </CardContent>
          </Card>
        </div>
        <Card>
          <CardHeaderSkeleton title="w-24" action="w-12" />
          <CardContent>
            <ItemListSkeleton rows={5} badge />
          </CardContent>
        </Card>
      </div>
    </PagePending>
  )
}

const ITEM_WIDTHS = ["w-3/4", "w-1/2", "w-2/3", "w-3/5", "w-4/5"]
const SUB_WIDTHS = ["w-56", "w-40", "w-48", "w-36", "w-52"]

// Item rows as RemindersCard and RunsCard render them: a title, a mono line under it.
function ItemListSkeleton({ rows, badge }: { rows: number; badge?: boolean }) {
  return (
    <ItemGroup className="gap-0">
      {Array.from({ length: rows }, (_, i) => (
        <Item key={i} size="sm" className="px-0">
          <ItemContent className="min-w-0">
            <SkeletonText className="text-sm" width={ITEM_WIDTHS[i % ITEM_WIDTHS.length]} />
            <SkeletonText className="text-sm" width={SUB_WIDTHS[i % SUB_WIDTHS.length]} />
          </ItemContent>
          {badge && (
            <ItemActions>
              <Skeleton className="h-5 w-10 rounded-full" />
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
