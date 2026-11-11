// Add connector page: the catalog of every connector Palui can set up, grouped
// by kind. A connector with presets (the MCP server) gets its own section that
// splits "Custom" (any URL, set up by hand) from its ready-made presets.
// Picking one opens its page, /connectors/<id>, as a draft until it's saved.
import { z } from "zod"
import * as React from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { ArrowLeftIcon, PlusIcon, SearchIcon } from "lucide-react"
import type { ConnectorManifest } from "@palui/core/connectors/define"

import { AppError } from "@/components/app-error"
import { connectorIcon } from "@/components/connector-icon"
import { CONNECTOR_GROUPS } from "@/components/connectors/shared"
import { newConnectorId } from "@/components/connectors/targets"
import { PageHeader } from "@/components/page-header"
import { PageHeaderSkeleton, PagePending } from "@/components/page-pending"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Item, ItemContent, ItemDescription, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Skeleton } from "@/components/ui/skeleton"
import { getConnectorsPage } from "@/lib/fns/connectors"
import { useT } from "@/lib/i18n"
import { manifests } from "@/lib/manifests"
import { presetHost, type Preset } from "@/lib/presets"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/_app/connectors_/new")({
  // Set when adding from an agent's page: the new tool connector serves only it.
  validateSearch: z.object({ agent: z.string().max(100).optional().catch(undefined) }),
  loader: () => getConnectorsPage(),
  head: () => ({ meta: [{ title: "Add connector · Palui" }] }),
  component: NewConnectorPage,
  pendingComponent: NewConnectorSkeleton,
  errorComponent: AppError,
})

const matches = (q: string, ...parts: (string | null | undefined)[]) => !q || parts.some((p) => p?.toLowerCase().includes(q))

function NewConnectorPage() {
  const data = Route.useLoaderData()
  const t = useT()
  const navigate = useNavigate()
  const { agent } = Route.useSearch()
  const [query, setQuery] = React.useState("")
  const q = query.trim().toLowerCase()

  const pick = (m: ConnectorManifest, preset?: Preset) => {
    const existing = !m.multiple ? data.items.find((i) => i.type === m.id) : undefined
    if (existing) return void navigate({ to: "/connectors/$id", params: { id: existing.id } })
    void navigate({ to: "/connectors/$id", params: { id: newConnectorId(m, data.items, preset) }, search: { type: m.id, preset: preset?.id, agent: m.kind === "tools" ? agent : undefined } })
  }
  const isAdded = (m: ConnectorManifest) => !m.multiple && data.items.some((i) => i.type === m.id)

  const sections = CONNECTOR_GROUPS.flatMap(({ kind, label }) => {
    const ofKind = manifests.filter((m) => m.kind === kind)
    const plain = ofKind.filter((m) => !m.presets?.length && matches(q, m.name, m.id, m.summary))
    const out: React.ReactNode[] = []
    if (plain.length) {
      out.push(
        <Section key={kind} id={`new-${kind}`} title={t(label)}>
          <Grid>
            {plain.map((m) => (
              <Choice key={m.id} manifest={m} title={m.name} description={m.summary} added={isAdded(m)} onPick={() => pick(m)} />
            ))}
          </Grid>
        </Section>,
      )
    }
    for (const m of ofKind.filter((m) => m.presets?.length)) {
      const custom = t("connectors.custom", { name: m.name })
      const showCustom = matches(q, custom, m.name, m.id, m.summary, t("connectors.customHint"))
      const presets = m.presets!.filter((p) => matches(q, p.label, presetHost(p), m.name))
      if (!showCustom && !presets.length) continue
      out.push(
        <Section key={m.id} id={`new-${m.id}`} title={m.name} description={m.summary}>
          {showCustom && (
            <Sub title={t("connectors.customGroup")}>
              <Grid>
                <Choice manifest={m} icon={PlusIcon} title={custom} description={t("connectors.customHint")} dashed onPick={() => pick(m)} />
              </Grid>
            </Sub>
          )}
          {presets.length > 0 && (
            <Sub title={t("connectors.presetGroup")} note={t("connectors.presetHint")}>
              <Grid>
                {presets.map((p) => (
                  <Choice key={p.id} manifest={m} title={p.label} description={presetHost(p) ?? m.name} oneLine onPick={() => pick(m, p)} />
                ))}
              </Grid>
            </Sub>
          )}
        </Section>,
      )
    }
    return out
  })

  return (
    <>
      <div className="flex flex-col gap-3">
        <Button variant="ghost" size="sm" className="-ms-2.5 self-start text-muted-foreground" render={<Link to="/connectors" />}>
          <ArrowLeftIcon data-icon="inline-start" />
          {t("nav.connectors")}
        </Button>
        <PageHeader title={t("connectors.add")} />
      </div>
      <InputGroup className="max-w-md">
        <InputGroupAddon>
          <SearchIcon aria-hidden="true" />
        </InputGroupAddon>
        <InputGroupInput type="search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("connectors.catalog")} aria-label={t("connectors.catalog")} />
      </InputGroup>
      {sections.length ? (
        <div className="flex flex-col gap-8">{sections}</div>
      ) : (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyTitle>{t("command.empty")}</EmptyTitle>
          </EmptyHeader>
        </Empty>
      )}
    </>
  )
}

function Section({ id, title, description, children }: { id: string; title: string; description?: string; children: React.ReactNode }) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1 px-1">
        <h2 id={id} className="text-overline text-muted-foreground uppercase">
          {title}
        </h2>
        {description && <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">{description}</p>}
      </div>
      {children}
    </section>
  )
}

function Sub({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <h3 className="flex flex-wrap items-baseline gap-x-2 px-1 text-sm font-medium">
        {title}
        {note && <span className="font-normal text-muted-foreground">{note}</span>}
      </h3>
      {children}
    </div>
  )
}

function Grid({ children }: { children: React.ReactNode }) {
  return <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{children}</div>
}

function Choice({
  manifest,
  icon,
  title,
  description,
  added,
  dashed,
  oneLine,
  onPick,
}: {
  manifest: ConnectorManifest
  icon?: React.ComponentType<React.SVGProps<SVGSVGElement>>
  title: string
  description: string
  added?: boolean
  dashed?: boolean
  oneLine?: boolean
  onPick: () => void
}) {
  const t = useT()
  const Icon = icon ?? connectorIcon(manifest.icon)
  return (
    <Item
      variant="outline"
      className={cn("min-w-0 cursor-pointer flex-nowrap bg-card text-start hover:bg-muted", dashed && "border-dashed")}
      render={<button type="button" onClick={onPick} />}
    >
      <ItemMedia variant="icon">
        <Icon aria-hidden="true" />
      </ItemMedia>
      <ItemContent className="min-w-0 gap-0.5">
        <ItemTitle className="w-full min-w-0 flex-wrap gap-x-2 gap-y-1">
          <span className="min-w-0 break-words">{title}</span>
          {added && <Badge variant="outline">{t("connectors.added")}</Badge>}
        </ItemTitle>
        <ItemDescription className={oneLine ? "truncate" : "line-clamp-2 break-words"}>{description}</ItemDescription>
      </ItemContent>
    </Item>
  )
}

function NewConnectorSkeleton() {
  return (
    <PagePending page="nav.connectors">
      <PageHeaderSkeleton title="w-44" />
      <Skeleton className="h-9 w-full max-w-md" />
      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-16" />
        ))}
      </div>
    </PagePending>
  )
}
