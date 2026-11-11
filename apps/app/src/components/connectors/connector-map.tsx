// The Home connector summary (PLAN 11.4, 11.6): a connector map that follows the
// Connectors page pick (Cards: grouped by kind, an icon tile per connector). Palui sits
// at the root and every connector hangs off one wire, with its status and its
// Switch, so Home is one tap from off too.
// Motion: the wires draw once ever (a localStorage flag), then stay static. A
// busy connector shows a Spinner badge, never a pulse. Reduced motion: instant.
import * as React from "react"

import { connectorIcon } from "@/components/connector-icon"
import { ConnectorStatusBadge } from "@/components/connector-status"
import { IconTile } from "@/components/icon-tile"
import { PaluiMark } from "@/components/palui-mark"
import { useT, type Key } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { cn } from "@/lib/utils"
import { CONNECTOR_GROUPS, ConnectorSwitch } from "./shared"

export type MapConnector = { id: string; type: string; kind: string; label: string; status: string; statusDetail: string | null; enabled: boolean }

const DRAWN_KEY = "palui.homeMap.drawn"

type Row = { key: string; type: "hub" } | { key: string; type: "group"; label: Key } | { key: string; type: "conn"; c: MapConnector }

// True only on the first visit ever: the flag is set as the draw starts.
function useDrawOnce(): boolean {
  const [draw, setDraw] = React.useState(false)
  React.useEffect(() => {
    try {
      if (localStorage.getItem(DRAWN_KEY)) return
      localStorage.setItem(DRAWN_KEY, "1")
      setDraw(true)
    } catch {
      // Storage blocked: skip the draw, the static map is the same picture.
    }
  }, [])
  return draw
}

export function ConnectorMap({ connectors }: { connectors: MapConnector[] }) {
  const t = useT()
  const draw = useDrawOnce()
  const rows: Row[] = [{ key: "hub", type: "hub" }]
  for (const g of CONNECTOR_GROUPS) {
    const list = connectors.filter((c) => c.kind === g.kind)
    if (!list.length) continue
    rows.push({ key: `g-${g.kind}`, type: "group", label: g.label })
    for (const c of list) rows.push({ key: c.id, type: "conn", c })
  }
  const last = rows.length - 1
  return (
    <ul className="@container/map flex flex-col" data-draw={draw ? "" : undefined} aria-label={t("home.connectors")}>
      {rows.map((row, i) => (
        <li key={row.key} className="relative flex min-h-9 items-center gap-3" style={{ "--i": i } as React.CSSProperties}>
          {/* The wire: a trunk segment per row (half for the root and the last
              row), plus a branch to each connector. */}
          <span aria-hidden="true" className="relative flex w-9 shrink-0 self-stretch">
            {i !== last && <span className={cn("map-trunk absolute start-[17.5px] w-px", i === 0 ? "top-1/2 bottom-0" : "inset-y-0")} />}
            {i === last && i !== 0 && <span className="map-trunk absolute start-[17.5px] top-0 h-1/2 w-px" />}
            {row.type === "conn" && <span className="map-branch absolute start-[18px] -end-3 top-1/2 h-px" />}
            {row.type === "hub" && (
              <PaluiMark className="relative m-auto size-9" />
            )}
          </span>
          {row.type === "hub" && <span className="text-sm font-medium">{t("app.name")}</span>}
          {row.type === "group" && <h3 className="pt-2 text-overline text-muted-foreground uppercase">{t(row.label)}</h3>}
          {row.type === "conn" && <MapNode c={row.c} />}
        </li>
      ))}
    </ul>
  )
}

function MapNode({ c }: { c: MapConnector }) {
  const Icon = connectorIcon(manifestFor(c.type)?.icon)
  const setUp = c.status !== "not_set_up"
  return (
    <div className="flex min-w-0 flex-1 items-center gap-2.5 py-1">
      <IconTile size="sm" className="border bg-card">
        <Icon />
      </IconTile>
      {/* Names wrap to two lines rather than truncate (a phone has no hover
          for a title). In a narrow card the status drops under the name. */}
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="line-clamp-2 text-sm break-words">{c.label}</span>
        {c.statusDetail && <span className="line-clamp-2 text-caption break-words text-muted-foreground">{c.statusDetail}</span>}
        <ConnectorStatusBadge status={c.status} className="@sm/map:hidden" />
      </div>
      <ConnectorStatusBadge status={c.status} className="hidden @sm/map:inline-flex" />
      {setUp ? <ConnectorSwitch item={c} className="-me-2" /> : <span className="size-11 shrink-0" aria-hidden="true" />}
    </div>
  )
}
