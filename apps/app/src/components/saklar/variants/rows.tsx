// Saklar layout 2, "Rows": dense rows in the style of Raycast or macOS
// settings (PLAN 11.7 variant 2, the density axis). One line per connector with
// its status inline; the chevron opens the details in place.
import * as React from "react"
import { ChevronDownIcon } from "lucide-react"

import { connectorIcon } from "@/components/connector-icon"
import { ConnectorStatusBadge } from "@/components/connector-status"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Item, ItemActions, ItemContent, ItemDescription, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { useT } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { SAKLAR_GROUPS, SaklarSwitch } from "../shared"
import { detailFor, type SaklarItem, type SaklarLayoutProps, type SaklarLive } from "./types"

export function SaklarRows({ items, live, onOpen }: SaklarLayoutProps) {
  const t = useT()
  return SAKLAR_GROUPS.map(({ kind, label }) => {
    const rows = items.filter((i) => i.kind === kind)
    if (!rows.length) return null
    return (
      <section key={kind} aria-labelledby={`group-${kind}`} className="flex flex-col gap-2">
        <h2 id={`group-${kind}`} className="px-1 text-overline text-muted-foreground uppercase">
          {t(label)} <span className="tabular-nums">{rows.length}</span>
        </h2>
        <Card className="gap-0 py-0">
          {rows.map((item, i) => (
            <React.Fragment key={item.id}>
              {i > 0 && <Separator />}
              <ConnectorRow item={item} live={live} onOpen={() => onOpen(item.id)} />
            </React.Fragment>
          ))}
        </Card>
      </section>
    )
  })
}

function ConnectorRow({ item, live, onOpen }: { item: SaklarItem; live: SaklarLive; onOpen: () => void }) {
  const t = useT()
  const m = manifestFor(item.type)
  const Icon = connectorIcon(m?.icon)
  const setUp = item.status !== "not_set_up"
  const detail = detailFor(item, live)
  const panelId = `row-details-${item.id}`
  return (
    <Collapsible>
      <Item size="sm" className="flex-nowrap rounded-none py-1.5 pe-1.5">
        <ItemMedia variant="icon">
          <Icon aria-hidden="true" />
        </ItemMedia>
        <ItemContent className="min-w-0 gap-0.5">
          <ItemTitle className="w-full min-w-0 flex-wrap gap-x-2 gap-y-1">
            <span className="min-w-0 break-words">{item.label}</span>
            <ConnectorStatusBadge status={item.status} />
          </ItemTitle>
          <ItemDescription className="line-clamp-1 break-all">{detail ?? m?.summary ?? item.type}</ItemDescription>
        </ItemContent>
        <ItemActions className="gap-0">
          {setUp ? (
            <SaklarSwitch item={item} />
          ) : (
            <Button variant="outline" size="sm" className="me-1.5" onClick={onOpen} aria-label={`${t("saklar.continueSetup")} ${item.label}`}>
              {t("saklar.continueSetup")}
            </Button>
          )}
          <CollapsibleTrigger
            render={<Button variant="ghost" size="icon" className="group/chevron size-11" aria-controls={panelId} />}
            aria-label={t("saklar.details", { name: item.label })}
          >
            <ChevronDownIcon aria-hidden="true" className="transition-transform duration-150 ease-emphasized group-data-panel-open/chevron:rotate-180" />
          </CollapsibleTrigger>
        </ItemActions>
      </Item>
      <CollapsibleContent id={panelId} className="border-t bg-muted/40">
        <div className="flex flex-col gap-3 px-3 py-3 ps-14">
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-caption">
            <dt className="text-muted-foreground">{t("saklar.field.id")}</dt>
            <dd className="min-w-0 font-mono break-all">{item.id}</dd>
            <dt className="text-muted-foreground">{t("saklar.field.what")}</dt>
            <dd className="min-w-0 text-pretty">{m?.summary ?? item.type}</dd>
            {detail && (
              <>
                <dt className="text-muted-foreground">{t("saklar.field.detail")}</dt>
                <dd className="min-w-0 break-words">{detail}</dd>
              </>
            )}
            {item.status === "paused" && m?.pauseNote && (
              <>
                <dt className="text-muted-foreground">{t("saklar.field.whilePaused")}</dt>
                <dd className="min-w-0 text-pretty">{m.pauseNote}</dd>
              </>
            )}
          </dl>
          <div className="flex flex-wrap items-center gap-2">
            {item.trust === "external" && <Badge variant="outline">{t("saklar.external")}</Badge>}
            <Button variant="outline" size="sm" className="ms-auto" onClick={onOpen} aria-label={`${setUp ? t("common.open") : t("saklar.continueSetup")} ${item.label}`}>
              {setUp ? t("common.open") : t("saklar.continueSetup")}
            </Button>
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

export function SaklarRowsSkeleton() {
  return [0, 1].map((g) => (
    <div key={g} className="flex flex-col gap-2">
      <Skeleton className="h-4 w-20" />
      <Skeleton className="h-40 rounded-xl" />
    </div>
  ))
}
