// Saklar (PLAN 4, 11.4): every connector grouped by kind. Health and control are
// one element: each connector shows its state and is one tap from off. Pause
// never asks; remove (in the sheet) always does. The layout is the Explore pick
// (PLAN 11.7); `?v=1|2|3` shows the picker to flip between the three variants.
import * as React from "react"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { PlusIcon, ToggleRightIcon } from "lucide-react"
import { z } from "zod"
import type { ConnectorManifest } from "@palui/core/saklar/define"

import { AppError } from "@/components/app-error"
import { connectorIcon } from "@/components/connector-icon"
import { PageHeader } from "@/components/page-header"
import { RuntimeAlert } from "@/components/runtime-alert"
import { ConnectorSheet, type SheetTarget } from "@/components/saklar/connector-sheet"
import { DEFAULT_SAKLAR_LAYOUT, SAKLAR_LAYOUTS } from "@/components/saklar/layouts"
import { PrototypePicker } from "@/components/saklar/prototype-picker"
import { SAKLAR_GROUPS, StatusAnnouncer } from "@/components/saklar/shared"
import type { SaklarItem } from "@/components/saklar/variants/types"
import { slugify } from "@/lib/slug"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { useLiveRefresh } from "@/hooks/use-live-events"
import { getSaklar } from "@/lib/fns/saklar"
import { useT } from "@/lib/i18n"
import { manifestFor, manifests } from "@/lib/manifests"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/saklar")({
  validateSearch: z.object({
    open: z.string().max(64).optional(),
    // The Explore picker: 1 Cards, 2 Rows, 3 Board. Absent means the pick.
    v: z.number().int().min(1).max(SAKLAR_LAYOUTS.length).optional().catch(undefined),
  }),
  loader: () => getSaklar(),
  head: () => ({ meta: [{ title: "Saklar · Palui" }] }),
  component: SaklarPage,
  pendingComponent: SaklarSkeleton,
  errorComponent: AppError,
})

type Item = SaklarItem
const GROUPS = SAKLAR_GROUPS

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

function newTarget(m: ConnectorManifest, existing: Item[], preset?: NonNullable<ConnectorManifest["presets"]>[number]): SheetTarget {
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
    trust: (preset?.config?.trust as "own" | "external" | undefined) ?? m.trust ?? "own",
  }
}

function SaklarPage() {
  const data = Route.useLoaderData()
  const { open: openId, v } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const router = useRouter()
  const t = useT()
  const [catalogOpen, setCatalogOpen] = React.useState(false)
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

  const startNew = (m: ConnectorManifest, preset?: NonNullable<ConnectorManifest["presets"]>[number]) => {
    setCatalogOpen(false)
    const existing = !m.multiple ? data.items.find((i) => i.type === m.id) : undefined
    if (existing) return setOpen(existing.id)
    const next = newTarget(m, data.items, preset)
    setDraft(next)
    setOpen(next.id)
  }

  const visible = data.items
  const layoutIndex = v ? v - 1 : DEFAULT_SAKLAR_LAYOUT
  const { Layout } = SAKLAR_LAYOUTS[layoutIndex]

  return (
    <>
      <PageHeader
        title={t("saklar.title")}
        description={t("saklar.description")}
        actions={
          <Button onClick={() => setCatalogOpen(true)}>
            <PlusIcon data-icon="inline-start" />
            {t("saklar.add")}
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
            <EmptyTitle>{t("saklar.empty")}</EmptyTitle>
            <EmptyDescription>{t("saklar.emptyBody")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={() => setCatalogOpen(true)}>
              <PlusIcon data-icon="inline-start" />
              {t("saklar.add")}
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
          names={SAKLAR_LAYOUTS.map((l) => l.name)}
          active={layoutIndex}
          onChange={(i) => void navigate({ search: (prev) => ({ ...prev, v: i + 1 }), replace: true })}
        />
      )}
      <Catalog open={catalogOpen} onOpenChange={setCatalogOpen} items={data.items} onPick={startNew} />
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

function Catalog({ open, onOpenChange, items, onPick }: { open: boolean; onOpenChange: (o: boolean) => void; items: Item[]; onPick: (m: ConnectorManifest, preset?: NonNullable<ConnectorManifest["presets"]>[number]) => void }) {
  const t = useT()
  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title={t("saklar.add")} description={t("saklar.catalog")}>
      <Command>
      <CommandInput placeholder={t("saklar.catalog")} />
      <CommandList>
        <CommandEmpty>{t("command.empty")}</CommandEmpty>
        {GROUPS.map(({ kind, label }) => {
          const list = manifests.filter((m) => m.kind === kind)
          if (!list.length) return null
          return (
            <CommandGroup key={kind} heading={t(label)}>
              {list.flatMap((m) => {
                const Icon = connectorIcon(m.icon)
                const added = !m.multiple && items.some((i) => i.type === m.id)
                const main = (
                  <CommandItem key={m.id} value={`${m.name} ${m.id} ${m.summary}`} onSelect={() => onPick(m)}>
                    <Icon aria-hidden="true" />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate">{m.name}</span>
                      <span className="truncate text-caption text-muted-foreground">{m.summary}</span>
                    </span>
                    {added && <Badge variant="outline">{t("saklar.added")}</Badge>}
                  </CommandItem>
                )
                const presets = (m.presets ?? []).map((p) => (
                  <CommandItem key={`${m.id}-${p.id}`} value={`${p.label} ${m.name} preset`} onSelect={() => onPick(m, p)}>
                    <Icon aria-hidden="true" />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate">{p.label}</span>
                      <span className="truncate text-caption text-muted-foreground">{t("saklar.presetHint", { name: m.name })}</span>
                    </span>
                  </CommandItem>
                ))
                return [main, ...presets]
              })}
            </CommandGroup>
          )
        })}
      </CommandList>
      </Command>
    </CommandDialog>
  )
}

function SaklarSkeleton() {
  const { v } = Route.useSearch()
  const { Skeleton: LayoutSkeleton } = SAKLAR_LAYOUTS[v ? v - 1 : DEFAULT_SAKLAR_LAYOUT]
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <Skeleton className="h-8 w-28" />
          <Skeleton className="h-4 w-72 max-w-full" />
        </div>
        <Skeleton className="h-8 w-36" />
      </div>
      <div className="flex flex-col gap-6">
        <LayoutSkeleton />
      </div>
    </>
  )
}
