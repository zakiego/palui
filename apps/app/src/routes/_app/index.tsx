// Home (PLAN 11.4): a first-run checklist until it's done, then a Saklar
// summary, the next 3 reminders, the last 5 runs and tokens today.
import { createFileRoute, Link, useRouter } from "@tanstack/react-router"
import { BellIcon, CircleCheckIcon, CircleIcon, HistoryIcon, ToggleRightIcon } from "lucide-react"
import { toast } from "sonner"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { RuntimeAlert } from "@/components/runtime-alert"
import { ConnectorMap } from "@/components/saklar/connector-map"
import { StatusAnnouncer } from "@/components/saklar/shared"
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
        <SaklarSummary connectors={data.connectors} />
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
  const steps: { done: boolean; label: Key; to: "/settings" | "/saklar" | "/chats"; optional?: boolean }[] = [
    { done: checklist.passkey, label: "home.check.passkey", to: "/settings" },
    { done: checklist.model, label: "home.check.model", to: "/saklar" },
    { done: checklist.channel, label: "home.check.channel", to: "/saklar" },
    { done: checklist.hi, label: "home.check.hi", to: "/chats" },
    { done: checklist.recall, label: "home.check.recall", to: "/saklar", optional: true },
    { done: checklist.mcp, label: "home.check.mcp", to: "/saklar", optional: true },
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

function SaklarSummary({ connectors }: { connectors: HomeConnector[] }) {
  const t = useT()
  const shown = connectors.filter((c) => c.status !== "not_set_up")
  const on = shown.filter((c) => c.status === "on").length
  return (
    <Card className="@3xl/main:row-span-2">
      <CardHeader>
        <CardTitle>{t("home.saklar")}</CardTitle>
        {shown.length > 0 && <CardDescription className="tabular-nums">{t("home.map.on", { n: on, total: shown.length })}</CardDescription>}
        <CardAction>
          <Button variant="outline" size="sm" render={<Link to="/saklar" />}>
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
              <EmptyTitle>{t("saklar.empty")}</EmptyTitle>
              <EmptyDescription>{t("saklar.emptyBody")}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button size="sm" render={<Link to="/saklar" />}>
                {t("saklar.add")}
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

function HomeSkeleton() {
  return (
    <>
      <div className="flex flex-col gap-2">
        <Skeleton className="h-8 w-32" />
        <Skeleton className="h-4 w-full max-w-md" />
      </div>
      <div className="grid gap-4 @3xl/main:grid-cols-2">
        <Skeleton className="h-80 rounded-xl @3xl/main:row-span-2" />
        <Skeleton className="h-32 rounded-xl" />
        <Skeleton className="h-44 rounded-xl" />
        <Skeleton className="h-64 rounded-xl" />
      </div>
    </>
  )
}
