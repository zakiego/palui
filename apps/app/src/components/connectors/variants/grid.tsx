// Connectors page layout 1, "Cards": a card grid grouped by kind, a Switch per card
// (PLAN 11.7 variant 1, the closest to TASTE.md and shadcn). The card isn't a
// button; the Switch and "Open" are its two controls.
import { connectorIcon } from "@/components/connector-icon"
import { ConnectorStatusBadge } from "@/components/connector-status"
import { IconTile } from "@/components/icon-tile"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useT } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { CONNECTOR_GROUPS, ConnectorSwitch } from "../shared"
import { detailFor, type ConnectorItem, type ConnectorLayoutProps, type ConnectorLiveMap } from "./types"

export function ConnectorGrid({ items, live, onOpen }: ConnectorLayoutProps) {
  const t = useT()
  return CONNECTOR_GROUPS.map(({ kind, label }) => {
    const rows = items.filter((i) => i.kind === kind)
    if (!rows.length) return null
    return (
      <section key={kind} aria-labelledby={`group-${kind}`} className="flex flex-col gap-3">
        <h2 id={`group-${kind}`} className="text-heading">
          {t(label)} <span className="font-normal text-muted-foreground tabular-nums">{rows.length}</span>
        </h2>
        <div className="grid gap-3 @2xl/main:grid-cols-2 @5xl/main:grid-cols-3">
          {rows.map((item) => (
            <ConnectorCard key={item.id} item={item} live={live} onOpen={() => onOpen(item.id)} />
          ))}
        </div>
      </section>
    )
  })
}

function ConnectorCard({ item, live, onOpen }: { item: ConnectorItem; live: ConnectorLiveMap; onOpen: () => void }) {
  const t = useT()
  const m = manifestFor(item.type)
  const Icon = connectorIcon(m?.icon)
  const setUp = item.status !== "not_set_up"
  const detail = detailFor(item, live)
  return (
    <Card size="sm" className="justify-between">
      <CardHeader>
        <div className="flex min-w-0 items-start gap-3">
          <IconTile>
            <Icon />
          </IconTile>
          <div className="flex min-w-0 flex-col gap-1">
            <CardTitle className="min-w-0 break-words">{item.label}</CardTitle>
            <CardDescription className="line-clamp-2 text-pretty">{m?.summary ?? item.type}</CardDescription>
          </div>
        </div>
        {setUp && (
          <CardAction className="-me-1.5 -mt-1.5">
            <ConnectorSwitch item={item} />
          </CardAction>
        )}
      </CardHeader>
      <CardFooter className="flex-wrap gap-2 border-t pt-(--card-spacing) pb-(--card-spacing)">
        <ConnectorStatusBadge status={item.status} />
        {item.trust === "external" && <Badge variant="outline">{t("connectors.external")}</Badge>}
        {detail && (
          <span className="min-w-0 flex-1 basis-32 truncate text-caption text-muted-foreground" title={detail}>
            {detail}
          </span>
        )}
        {item.status === "paused" && m?.pauseNote && <span className="w-full text-caption text-pretty text-muted-foreground">{m.pauseNote}</span>}
        <Button variant="outline" size="sm" className="ms-auto" onClick={onOpen} aria-label={`${setUp ? t("common.open") : t("connectors.continueSetup")} ${item.label}`}>
          {setUp ? t("common.open") : t("connectors.continueSetup")}
        </Button>
      </CardFooter>
    </Card>
  )
}

export function ConnectorGridSkeleton() {
  return [0, 1].map((g) => (
    <div key={g} className="flex flex-col gap-3">
      <Skeleton className="h-5 w-24" />
      <div className="grid gap-3 @2xl/main:grid-cols-2 @5xl/main:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-36 rounded-xl" />
        ))}
      </div>
    </div>
  ))
}
