// Watches (PLAN 6e): what the agent is keeping an eye on in every chat, and
// what it found or gave up on. Watches are made from chat, so the page only
// lists and cancels.
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { ClockAlertIcon, EllipsisIcon, EyeIcon, HistoryIcon, RadarIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"
import { z } from "zod"

import { AppError } from "@/components/app-error"
import { ClampText } from "@/components/clamp-text"
import { ListCapNote } from "@/components/list-cap-note"
import { PageHeader } from "@/components/page-header"
import { LineTabsSkeleton, PageHeaderSkeleton, PagePending, TableSkeleton } from "@/components/page-pending"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { StatusText, type StatusTone } from "@/components/ui/status-text"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cancelWatchFn, getWatches, type WatchItem } from "@/lib/api/watches"
import type { WatchStatus } from "@/lib/api/types"
import { clip, compact, duration, errorMessage, inZone } from "@/lib/format"
import { useCount, useLabel, useT } from "@/lib/i18n"

export const Route = createFileRoute("/_app/watches")({
  validateSearch: z.object({ tab: z.enum(["running", "ended"]).optional() }),
  loader: () => getWatches(),
  head: () => ({ meta: [{ title: "Watches · Palui" }] }),
  component: WatchesPage,
  pendingComponent: WatchesSkeleton,
  errorComponent: AppError,
})

function WatchesPage() {
  const data = Route.useLoaderData()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const t = useT()
  const tab = search.tab ?? "running"

  return (
    <>
      <PageHeader title={t("watches.title")} />
      <Tabs value={tab} onValueChange={(v) => void navigate({ search: (p) => ({ ...p, tab: v as typeof tab }), replace: true })} className="gap-6">
        <TabsList variant="line" aria-label={t("watches.title")}>
          <TabsTrigger value="running">
            <RadarIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("watches.running")} <span className="font-mono text-muted-foreground tabular-nums">{data.totals.active.toLocaleString("en")}</span>
          </TabsTrigger>
          <TabsTrigger value="ended">
            <HistoryIcon aria-hidden="true" className="hidden @xl/main:block" />
            {t("watches.ended")}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="running">
          <WatchTable rows={data.active} total={data.totals.active} timezone={data.timezone} />
        </TabsContent>
        <TabsContent value="ended">
          <WatchTable rows={data.ended} total={data.totals.ended} timezone={data.timezone} ended />
        </TabsContent>
      </Tabs>
    </>
  )
}

// A check this far past its time didn't run when it should have (Palui was down or busy).
const OVERDUE_AFTER_MS = 2 * 60_000

const TONE: Record<WatchStatus, StatusTone> = {
  active: "outline",
  checking: "outline",
  done: "success",
  expired: "secondary",
  cancelled: "secondary",
  failed: "destructive",
}

function WatchTable({ rows, total, timezone, ended }: { rows: WatchItem[]; total: number; timezone: string; ended?: boolean }) {
  const t = useT()
  const label = useLabel()
  const count = useCount()
  const now = Date.now()
  if (rows.length === 0) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <EyeIcon />
          </EmptyMedia>
          <EmptyTitle>{ended ? t("watches.endedEmpty") : t("watches.empty")}</EmptyTitle>
          {!ended && <EmptyDescription>{t("watches.emptyRunning")}</EmptyDescription>}
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("watches.title")}</TableHead>
            <TableHead className="hidden w-52 @2xl/main:table-cell">{ended ? t("table.status") : t("watches.nextCheck")}</TableHead>
            <TableHead className="hidden w-56 @4xl/main:table-cell">{t("table.chat")}</TableHead>
            <TableHead className="w-12">
              <span className="sr-only">{t("table.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((w) => {
            const chat = w.chat?.title || w.chat?.channel || t("runs.deletedChat")
            const overdue = w.status === "active" && w.nextCheckAt != null && w.nextCheckAt < now - OVERDUE_AFTER_MS
            const endedAt = w.endedAt ?? w.lastCheckAt ?? w.expiresAt
            // What to read under the topic: the message it sent, why it stopped, or where things stand.
            const body = w.status === "done" ? (w.result ?? w.lastSummary) : w.status === "failed" ? (w.lastError ?? w.lastSummary) : w.lastSummary
            const when = ended ? (
              <>
                <StatusText tone={TONE[w.status]}>{label("watch.status", w.status)}</StatusText>
                <span className="block text-caption text-muted-foreground">{inZone(endedAt, timezone)}</span>
              </>
            ) : w.status === "checking" ? (
              <StatusText tone="outline">{label("watch.status", w.status)}</StatusText>
            ) : w.nextCheckAt != null ? (
              <>
                {inZone(w.nextCheckAt, timezone)}
                <span className="block text-caption text-muted-foreground">{t("watches.endsIn", { time: duration(Math.max(w.expiresAt - now, 0)) })}</span>
              </>
            ) : null
            return (
              <TableRow key={w.id}>
                <TableCell className="max-w-0 whitespace-normal">
                  <ClampText text={w.topic} lines={2} className="font-medium" />
                  {body ? (
                    <ClampText text={body} lines={2} className={w.status === "failed" ? "mt-0.5 text-caption text-destructive-fg" : "mt-0.5 text-caption text-muted-foreground"} />
                  ) : (
                    !ended &&
                    w.checks === 0 && (
                      // Set to start later: nothing is checked until then.
                      <span className="mt-0.5 block text-caption text-muted-foreground">
                        {w.startsAt != null && w.startsAt > now ? t("watches.startsAt", { time: inZone(w.startsAt, timezone) }) : t("watches.noSummary")}
                      </span>
                    )
                  )}
                  <span className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
                    {w.status === "checking" && <StatusText tone="outline" className="@2xl/main:hidden">{label("watch.status", w.status)}</StatusText>}
                    {overdue && (
                      <StatusText tone="warning" icon={<ClockAlertIcon aria-hidden="true" />}>
                        {t("watches.overdue")}
                      </StatusText>
                    )}
                    {!ended && w.fails > 0 && (
                      <StatusText tone="warning" icon={<TriangleAlertIcon aria-hidden="true" />} title={w.lastError ?? undefined}>
                        {t("watches.failedChecks", { n: w.fails.toLocaleString("en") })}
                      </StatusText>
                    )}
                    {ended && <StatusText tone={TONE[w.status]} className="@2xl/main:hidden">{label("watch.status", w.status)}</StatusText>}
                    <span className="font-mono tabular-nums" title={`${w.tokensUsed.toLocaleString("en")} / ${w.tokenCap.toLocaleString("en")}`}>
                      {count(w.checks, "watches.check", "watches.checks")} · {t(w.tokensUsed === 1 ? "watches.token" : "watches.tokens", { n: compact(w.tokensUsed) })}
                    </span>
                    {w.status === "active" && w.nextCheckAt != null && <span className="font-mono tabular-nums @2xl/main:hidden">{inZone(w.nextCheckAt, timezone)}</span>}
                    {/* The chat column is gone on narrow screens; two watches on the same topic still need telling apart. */}
                    <span dir="auto" className="max-w-full min-w-0 truncate @4xl/main:hidden" title={chat}>
                      {chat}
                    </span>
                  </span>
                </TableCell>
                <TableCell className="hidden font-mono text-caption whitespace-normal tabular-nums @2xl/main:table-cell">{when}</TableCell>
                <TableCell dir="auto" className="hidden max-w-0 truncate text-left @4xl/main:table-cell" title={chat}>
                  {chat}
                </TableCell>
                <TableCell>{(!ended || w.lastRunId) && <RowActions row={w} ended={ended} />}</TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      <ListCapNote shown={rows.length} total={total} />
    </Card>
  )
}

function RowActions({ row, ended }: { row: WatchItem; ended?: boolean }) {
  const t = useT()
  const label = useLabel()
  const router = useRouter()
  const navigate = useNavigate()
  const [confirm, setConfirm] = React.useState(false)
  const cancel = async () => {
    try {
      // It may have ended on its own since the page loaded; say what really happened.
      const res = await cancelWatchFn({ data: { id: row.id } })
      if (res.status === "cancelled") toast.success(t("watches.cancelled"))
      else toast.info(t("watches.alreadyEnded", { status: label("watch.status", res.status) }))
      void router.invalidate()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  const runId = row.lastRunId
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("watches.actionsFor", { text: clip(row.topic, 40) })} />}>
          <EllipsisIcon aria-hidden="true" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-48">
          {runId && (
            <DropdownMenuGroup>
              <DropdownMenuItem onSelect={() => void navigate({ to: "/activity/runs/$id", params: { id: runId } })}>{t("watches.lastRun")}</DropdownMenuItem>
            </DropdownMenuGroup>
          )}
          {runId && !ended && <DropdownMenuSeparator />}
          {!ended && (
            <DropdownMenuGroup>
              <DropdownMenuItem variant="destructive" onSelect={() => setConfirm(true)}>
                {t("watches.cancel")}
              </DropdownMenuItem>
            </DropdownMenuGroup>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("watches.cancelTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("watches.cancelBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.keep")}</AlertDialogCancel>
            <AlertDialogAction variant="danger" onClick={() => void cancel()}>
              {t("watches.cancel")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function WatchesSkeleton() {
  return (
    <PagePending page="nav.watches">
      <PageHeaderSkeleton title="w-32" />
      <div className="flex flex-col gap-6">
        <LineTabsSkeleton widths={["w-20", "w-14"]} />
        <TableSkeleton
          rows={8}
          columns={[
            { head: "w-16", sub: true },
            { className: "hidden w-52 @2xl/main:table-cell", head: "w-20", text: "font-mono text-caption", short: "w-28", sub: true },
            { className: "hidden w-56 @4xl/main:table-cell", head: "w-8", short: "w-28" },
            { className: "w-12", controls: ["size-4 rounded-sm"] },
          ]}
        />
      </div>
    </PagePending>
  )
}
