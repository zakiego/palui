// Saklar layout 3, "Board": a wiring board with Palui in the middle and every
// connector a node on a cable (PLAN 11.7 variant 3, the spatial axis). Channels
// plug in on the left, everything Palui uses on the right. A running connector's
// cable is solid; a paused one hangs unplugged (dashed, with a gap at the bus).
// On a phone it folds into one column: Palui on top, one bus down the side.
import { CircleAlertIcon, PlugIcon, PlugZapIcon } from "lucide-react"

import { connectorIcon } from "@/components/connector-icon"
import { ConnectorStatusBadge } from "@/components/connector-status"
import { IconTile } from "@/components/icon-tile"
import { PaluiMark } from "@/components/palui-mark"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Item, ItemActions, ItemContent, ItemDescription, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { useT } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { cn } from "@/lib/utils"
import { SAKLAR_GROUPS, SaklarSwitch } from "../shared"
import { detailFor, type SaklarItem, type SaklarLayoutProps, type SaklarLive } from "./types"

type Side = "in" | "out"

export function SaklarBoard({ items, live, onOpen }: SaklarLayoutProps) {
  const t = useT()
  const on = items.filter((i) => i.status === "on").length
  const setUp = items.filter((i) => i.status !== "not_set_up").length
  const inputs = SAKLAR_GROUPS.filter((g) => g.kind === "channel")
  const outputs = SAKLAR_GROUPS.filter((g) => g.kind !== "channel")
  return (
    <div className="grid gap-y-0 @5xl/main:grid-cols-[minmax(0,1fr)_15rem_minmax(0,1fr)] @5xl/main:items-center">
      <div className="order-first @5xl/main:order-none @5xl/main:col-start-2 @5xl/main:row-start-1 @5xl/main:flex @5xl/main:items-center">
        <span aria-hidden="true" className="hidden h-px flex-1 bg-border-strong @5xl/main:block" />
        <Card size="sm" className="w-full max-w-[16rem] shrink-0 @5xl/main:w-auto @5xl/main:max-w-none @5xl/main:min-w-0 @5xl/main:flex-[0_1_12rem]">
          <CardHeader className="grid-cols-[auto_1fr] items-center gap-x-3">
            <IconTile size="lg" tone="brand" className="row-span-2">
              <PaluiMark />
            </IconTile>
            <CardTitle>{t("app.name")}</CardTitle>
            <CardDescription className="tabular-nums">{t("home.map.on", { n: on, total: setUp })}</CardDescription>
          </CardHeader>
        </Card>
        <span aria-hidden="true" className="hidden h-px flex-1 bg-border-strong @5xl/main:block" />
      </div>
      <BoardSide side="in" groups={inputs} items={items} live={live} onOpen={onOpen} className="@5xl/main:col-start-1 @5xl/main:row-start-1" />
      <BoardSide side="out" groups={outputs} items={items} live={live} onOpen={onOpen} className="@5xl/main:col-start-3 @5xl/main:row-start-1" />
    </div>
  )
}

// One side of the board. The bus is the border on the side that faces Palui
// (the start edge on a phone, where both sides stack under the hub).
function BoardSide({ side, groups, items, live, onOpen, className }: { side: Side; groups: typeof SAKLAR_GROUPS; items: SaklarItem[]; live: SaklarLive; onOpen: (id: string) => void; className?: string }) {
  const t = useT()
  const present = groups.filter((g) => items.some((i) => i.kind === g.kind))
  if (!present.length) return null
  return (
    <div className={cn("ms-8 flex flex-col gap-5 border-s border-border-strong pt-5 @5xl/main:ms-0 @5xl/main:py-2", side === "in" ? "@5xl/main:border-s-0 @5xl/main:border-e" : "", className)}>
      {present.map(({ kind, label }) => {
        const rows = items.filter((i) => i.kind === kind)
        return (
          <section key={kind} aria-labelledby={`group-${kind}`} className="flex flex-col gap-2">
            <h2 id={`group-${kind}`} className={cn("ps-4 text-overline text-muted-foreground uppercase @5xl/main:px-4", side === "in" && "@5xl/main:text-end")}>
              {t(label)} <span className="tabular-nums">{rows.length}</span>
            </h2>
            <ul className="flex flex-col gap-2">
              {rows.map((item) => (
                <li key={item.id}>
                  <BoardNode side={side} item={item} live={live} onOpen={() => onOpen(item.id)} />
                </li>
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

function BoardNode({ side, item, live, onOpen }: { side: Side; item: SaklarItem; live: SaklarLive; onOpen: () => void }) {
  const t = useT()
  const m = manifestFor(item.type)
  const Icon = connectorIcon(m?.icon)
  const setUp = item.status !== "not_set_up"
  const detail = detailFor(item, live)
  return (
    <div className={cn("flex items-center", side === "in" && "@5xl/main:flex-row-reverse")}>
      <Cable status={item.status} side={side} />
      <Item variant="outline" size="sm" className="min-w-0 flex-1 flex-nowrap items-start bg-card">
        <ItemMedia variant="icon">
          <Icon aria-hidden="true" />
        </ItemMedia>
        <ItemContent className="min-w-0">
          <ItemTitle className="w-full min-w-0">
            <span className="min-w-0 break-words">{item.label}</span>
          </ItemTitle>
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <ConnectorStatusBadge status={item.status} />
            {item.trust === "external" && <Badge variant="outline">{t("saklar.external")}</Badge>}
          </div>
          {(detail || (item.status === "paused" && m?.pauseNote)) && (
            <ItemDescription className="line-clamp-2 break-words">{detail ?? m?.pauseNote}</ItemDescription>
          )}
        </ItemContent>
        <ItemActions className="-my-1.5 -me-1.5 flex-col items-end gap-0 self-stretch">
          {setUp && <SaklarSwitch item={item} />}
          <Button variant="ghost" size="sm" className="mt-auto" onClick={onOpen} aria-label={`${setUp ? t("common.open") : t("saklar.continueSetup")} ${item.label}`}>
            {setUp ? t("common.open") : t("saklar.continueSetup")}
          </Button>
        </ItemActions>
      </Item>
    </div>
  )
}

// The cable between the bus and a node. Its look follows the status, and the
// status is also written on the node, so the cable is never the only signal.
function Cable({ status, side }: { status: string; side: Side }) {
  const live = status === "on" || status === "connecting"
  const broken = status === "error"
  return (
    <span aria-hidden="true" className={cn("relative flex w-5 shrink-0 items-center @5xl/main:w-10", side === "in" && "@5xl/main:flex-row-reverse")}>
      <span
        className={cn(
          "status-tone h-0 flex-1 border-t-2",
          live && "border-primary",
          broken && "border-destructive",
          !live && !broken && "ms-1.5 border-dashed border-border-strong",
          side === "in" && !live && !broken && "@5xl/main:ms-0 @5xl/main:me-1.5",
        )}
      />
      <span className="absolute inset-x-0 hidden justify-center @5xl/main:flex">
        {broken ? (
          <CircleAlertIcon className="size-3.5 bg-background text-destructive" />
        ) : live ? (
          <PlugZapIcon className="size-3.5 bg-background text-primary" />
        ) : status === "paused" ? (
          <PlugIcon className="size-3.5 bg-background text-muted-foreground" />
        ) : null}
      </span>
    </span>
  )
}

export function SaklarBoardSkeleton() {
  return (
    <div className="grid gap-4 @5xl/main:grid-cols-[1fr_15rem_1fr] @5xl/main:items-center">
      <Skeleton className="h-16 max-w-[16rem] rounded-xl @5xl/main:col-start-2 @5xl/main:max-w-none" />
      <div className="flex flex-col gap-2 @5xl/main:col-start-1 @5xl/main:row-start-1">
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-20 rounded-lg" />
        ))}
      </div>
      <div className="flex flex-col gap-2">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-20 rounded-lg" />
        ))}
      </div>
    </div>
  )
}
