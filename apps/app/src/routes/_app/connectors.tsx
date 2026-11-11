// Connectors page (PLAN 4, 11.4): every connector grouped by kind. Health and control are
// one element: each connector shows its state and is one tap from off. Pause
// never asks; remove (in the sheet) always does. The layout is the Explore pick
// (PLAN 11.7); `?v=1|2|3` shows the picker to flip between the three variants.
// Adding one happens on /connectors/new, which comes back with `?add=<type>`
// (and `&preset=<id>`) to open the setup sheet here.
import * as React from "react"
import { createFileRoute, Link, useNavigate, useRouter } from "@tanstack/react-router"
import { PlusIcon, ToggleRightIcon } from "lucide-react"
import { z } from "zod"
import type { ConnectorManifest } from "@palui/core/connectors/define"

import { AppError } from "@/components/app-error"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending } from "@/components/page-pending"
import { RuntimeAlert } from "@/components/runtime-alert"
import { ConnectorSheet, type SheetTarget } from "@/components/connectors/connector-sheet"
import { DEFAULT_CONNECTOR_LAYOUT, CONNECTOR_LAYOUTS } from "@/components/connectors/layouts"
import { PrototypePicker } from "@/components/connectors/prototype-picker"
import { StatusAnnouncer } from "@/components/connectors/shared"
import type { ConnectorItem } from "@/components/connectors/variants/types"
import { slugify } from "@/lib/slug"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getConnectorsPage } from "@/lib/fns/connectors"
import { useT } from "@/lib/i18n"
import { manifestFor } from "@/lib/manifests"
import { presetFor, type Preset } from "@/lib/presets"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/connectors")({
  validateSearch: z.object({
    open: z.string().max(64).optional(),
    // Set by /connectors/new: start a new connector of this type, from this preset.
    add: z.string().max(64).optional().catch(undefined),
    preset: z.string().max(64).optional().catch(undefined),
    // The Explore picker: 1 Cards, 2 Rows, 3 Board. Absent means the pick.
    v: z.number().int().min(1).max(CONNECTOR_LAYOUTS.length).optional().catch(undefined),
  }),
  loader: () => getConnectorsPage(),
  head: () => ({ meta: [{ title: "Connectors · Palui" }] }),
  component: ConnectorsPage,
  pendingComponent: ConnectorsSkeleton,
  errorComponent: AppError,
})

type Item = ConnectorItem

function toTarget(item: Item): SheetTarget {
  return {
    id: item.id,
    type: item.type,
    label: item.label,
    isNew: false,
    status: item.status,
    statusDetail: item.statusDetail,
    config: item.config,
    secrets: item.secrets,
    secretsError: item.secretsError,
    trust: item.trust,
  }
}

function newTarget(m: ConnectorManifest, existing: Item[], preset?: Preset): SheetTarget {
  let id = m.id
  let label = m.name
  if (m.multiple) {
    const base = `${m.id}-${slugify(preset?.id ?? "server")}`
    id = base
    for (let n = 2; existing.some((e) => e.id === id); n++) id = `${base}-${n}`
    label = preset?.label ?? "New server"
  }
  return {
    id,
    type: m.id,
    label,
    isNew: true,
    status: "not_set_up",
    statusDetail: null,
    config: (preset?.config ?? {}) as SheetTarget["config"],
    secrets: {},
    secretsError: null,
    trust: preset?.trust ?? m.trust ?? "own",
  }
}

function ConnectorsPage() {
  const data = Route.useLoaderData()
  const { open: openId, add, preset, v } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const router = useRouter()
  const t = useT()
  const [draft, setDraft] = React.useState<SheetTarget | null>(null)

  useLiveRefresh(() => void router.invalidate(), (e) => !!e.connectorId)

  // The open sheet follows ?open=<id>, so a link (or Back) can open one.
  const openItem = openId ? data.items.find((i) => i.id === openId) : undefined
  const target = openItem ? toTarget(openItem) : draft && draft.id === openId ? draft : null
  // The sheet keeps its last connector while it closes, so the exit animation
  // (and a phone's drag to dismiss) plays instead of the content vanishing.
  const lastTarget = React.useRef(target)
  if (target) lastTarget.current = target
  const shownTarget = target ?? lastTarget.current
  const setOpen = (id: string | undefined) => void navigate({ search: (prev) => ({ ...prev, open: id }), replace: !id })

  // Coming back from /connectors/new: build the draft once and swap ?add for ?open.
  const started = React.useRef<string | null>(null)
  React.useEffect(() => {
    if (!add) return
    const key = `${add}:${preset ?? ""}`
    if (started.current === key) return
    started.current = key
    const m = manifestFor(add)
    const existing = m && !m.multiple ? data.items.find((i) => i.type === m.id) : undefined
    const next = m && !existing ? newTarget(m, data.items, presetFor(m, preset)) : null
    if (next) setDraft(next)
    void navigate({ search: (prev) => ({ ...prev, add: undefined, preset: undefined, open: existing?.id ?? next?.id }), replace: true })
  }, [add, preset, data.items, navigate])

  const visible = data.items
  const layoutIndex = v ? v - 1 : DEFAULT_CONNECTOR_LAYOUT
  const { Layout } = CONNECTOR_LAYOUTS[layoutIndex]

  return (
    <>
      <PageHeader
        title={t("connectors.title")}
        description={t("connectors.description")}
        actions={
          <Button render={<Link to="/connectors/new" />}>
            <PlusIcon data-icon="inline-start" />
            {t("connectors.add")}
          </Button>
        }
      />
      {!data.runtimeUp && <RuntimeAlert />}

      {visible.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ToggleRightIcon />
            </EmptyMedia>
            <EmptyTitle>{t("connectors.empty")}</EmptyTitle>
            <EmptyDescription>{t("connectors.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button render={<Link to="/connectors/new" />}>
              <PlusIcon data-icon="inline-start" />
              {t("connectors.add")}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <div className={cn("flex flex-col gap-6", v && "pb-20")}>
          <Layout key={layoutIndex} items={visible} live={data.live} onOpen={setOpen} />
        </div>
      )}

      <StatusAnnouncer items={visible} />
      {v && (
        <PrototypePicker
          names={CONNECTOR_LAYOUTS.map((l) => l.name)}
          active={layoutIndex}
          onChange={(i) => void navigate({ search: (prev) => ({ ...prev, v: i + 1 }), replace: true })}
        />
      )}
      <ConnectorSheet
        open={!!openId && !!target}
        onOpenChange={(o) => {
          if (!o) setOpen(undefined)
        }}
        target={shownTarget}
        manifest={shownTarget ? manifestFor(shownTarget.type) : undefined}
        appUrl={data.appUrl}
      />
    </>
  )
}

function ConnectorsSkeleton() {
  const { v } = Route.useSearch()
  const { Skeleton: LayoutSkeleton } = CONNECTOR_LAYOUTS[v ? v - 1 : DEFAULT_CONNECTOR_LAYOUT]
  return (
    <PagePending page="nav.connectors">
      <PageHeaderSkeleton title="w-36" description="w-80" action="w-36" />
      <div className="flex flex-col gap-6">
        <LayoutSkeleton />
      </div>
    </PagePending>
  )
}
