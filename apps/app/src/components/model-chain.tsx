// The model chain editor: one role's ordered models, where the next one takes
// over when a model fails. Used by the global chains on Settings and by an
// agent's own chains on its page.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { ArrowDownIcon, ArrowUpIcon, BrainIcon, CircleAlertIcon, EllipsisIcon, PlusIcon, TrashIcon } from "lucide-react"

import { connectorIcon } from "@/components/connector-icon"
import { ModelPicker, type ModelCatalog } from "@/components/model-picker"
import { SimpleSelect, type Option } from "@/components/simple-select"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { listConnectorModels } from "@/lib/api/settings"
import { useT } from "@/lib/i18n"
import { REASONING_EFFORTS, type ModelChainLink } from "@/shared/constants"

export type Role = "default" | "fast" | "vision"
export const ROLES: Role[] = ["default", "fast", "vision"]
export const ROLE_HELP = { default: "settings.role.default", fast: "settings.role.fast", vision: "settings.role.vision" } as const
export const MAX_CHAIN = 6

// `key` is client only: it lets a row's DOM node, and focus, follow its item on reorder.
export type KeyedLink = ModelChainLink & { key: string }
export type ModelConnector = { id: string; label: string; icon?: string }

// The effort select's value for "no effort set", which leaves the provider's default.
const DEFAULT_EFFORT = "default"

export const stripKeys = (chain: KeyedLink[]): ModelChainLink[] => chain.map(({ connectorId, model, effort }) => ({ connectorId, model, ...(effort ? { effort } : {}) }))

// Stable client keys for chain rows.
export function useLinkKeys() {
  const prefix = React.useId()
  const count = React.useRef(0)
  return React.useCallback((chain: ModelChainLink[]): KeyedLink[] => chain.map((link) => ({ ...link, key: `${prefix}link-${count.current++}` })), [prefix])
}

// The model list of every connector the chains use, fetched once per connector.
export function useCatalogs(chains: KeyedLink[][], known: Set<string>) {
  const [catalogs, setCatalogs] = React.useState<Record<string, ModelCatalog>>({})
  const used = [...new Set(chains.flatMap((c) => c.map((x) => x.connectorId)))].filter((id) => known.has(id)).sort().join(",")
  const requested = React.useRef(new Set<string>())
  React.useEffect(() => {
    for (const id of used ? used.split(",") : []) {
      if (requested.current.has(id)) continue
      requested.current.add(id)
      setCatalogs((c) => ({ ...c, [id]: { state: "loading" } }))
      listConnectorModels({ data: { id } })
        .then((r) => setCatalogs((c) => ({ ...c, [id]: { state: "ok", models: r.models } })))
        .catch((e: unknown) => setCatalogs((c) => ({ ...c, [id]: { state: "error", error: e instanceof Error ? e.message : String(e) } })))
    }
  }, [used])
  return catalogs
}

export function connectorOptions(models: ModelConnector[]): Option[] {
  return models.map((m) => {
    const Icon = connectorIcon(m.icon)
    return { value: m.id, label: m.label, icon: <Icon aria-hidden="true" className="text-muted-foreground" /> }
  })
}

export function ModelChain({
  role,
  chain,
  onChange,
  options,
  catalogs,
  newKey,
}: {
  role: Role
  chain: KeyedLink[]
  onChange: (chain: KeyedLink[]) => void
  options: Option[]
  catalogs: Record<string, ModelCatalog>
  newKey: (chain: ModelChainLink[]) => KeyedLink[]
}) {
  const t = useT()
  const known = new Set(options.map((o) => o.value))
  const effortOptions = [
    { value: DEFAULT_EFFORT, label: t("settings.effort.default") },
    ...REASONING_EFFORTS.map((e) => ({ value: e, label: t(`settings.effort.${e}`) })),
  ].map((o) => ({ ...o, icon: <BrainIcon aria-hidden="true" className="text-muted-foreground" /> }))
  const setLink = (i: number, patch: Partial<ModelChainLink>) => onChange(chain.map((x, j) => (j === i ? { ...x, ...patch } : x)))

  // Remove unmounts the row that holds the open menu's trigger, so the menu has nowhere to return focus. Move it by hand, after the menu's own attempt.
  const focusAfterRemove = React.useRef<string | null>(null)
  React.useEffect(() => {
    const key = focusAfterRemove.current
    if (!key) return
    focusAfterRemove.current = null
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-chain-actions="${CSS.escape(key)}"]`)?.focus())
  }, [chain])

  return (
    <>
      <ol className="flex flex-col gap-2">
        {chain.map((link, i) => {
          const missing = !known.has(link.connectorId)
          // With no model connectors at all, the page already says so: skip the per-step note.
          const showMissing = missing && options.length > 0
          const missingId = `${link.key}-missing`
          return (
            <li key={link.key} className="grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-2 @2xl/main:grid-cols-[1.25rem_11rem_minmax(0,1fr)_9.5rem_auto]">
              <span className="font-mono text-caption text-muted-foreground tabular-nums">{i + 1}.</span>
              <SimpleSelect
                aria-label={t("settings.models.stepConnector", { role, n: i + 1 })}
                aria-invalid={missing}
                aria-describedby={showMissing ? missingId : undefined}
                className="col-span-2 w-full @2xl/main:col-span-1"
                value={link.connectorId}
                options={missing ? [{ value: link.connectorId, label: t("settings.models.missing", { id: link.connectorId }) }, ...options] : options}
                onChange={(v) => setLink(i, { connectorId: v })}
              />
              <ModelPicker
                aria-label={t("settings.models.stepModel", { role, n: i + 1 })}
                className="col-span-2 col-start-2 @2xl/main:col-span-1 @2xl/main:col-start-auto"
                value={link.model}
                catalog={catalogs[link.connectorId]}
                onChange={(v) => setLink(i, { model: v })}
              />
              <SimpleSelect
                aria-label={t("settings.models.stepEffort", { role, n: i + 1 })}
                className="col-start-2 w-full @2xl/main:col-start-auto"
                value={link.effort ?? DEFAULT_EFFORT}
                options={effortOptions}
                onChange={(v) => setLink(i, { effort: v === DEFAULT_EFFORT ? undefined : (v as ModelChainLink["effort"]) })}
              />
              <DropdownMenu>
                <DropdownMenuTrigger data-chain-actions={link.key} render={<Button variant="ghost" size="icon" aria-label={t("settings.models.stepActions", { role, n: i + 1 })} />}>
                  <EllipsisIcon aria-hidden="true" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-auto min-w-40">
                  <DropdownMenuItem className="pointer-coarse:py-2.5" disabled={i === 0} onSelect={() => onChange(swap(chain, i, i - 1))}>
                    <ArrowUpIcon aria-hidden="true" />
                    {t("settings.models.up")}
                  </DropdownMenuItem>
                  <DropdownMenuItem className="pointer-coarse:py-2.5" disabled={i === chain.length - 1} onSelect={() => onChange(swap(chain, i, i + 1))}>
                    <ArrowDownIcon aria-hidden="true" />
                    {t("settings.models.down")}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    className="pointer-coarse:py-2.5"
                    variant="destructive"
                    disabled={chain.length === 1}
                    onSelect={() => {
                      focusAfterRemove.current = (chain[i + 1] ?? chain[i - 1])?.key ?? null
                      onChange(chain.filter((_, j) => j !== i))
                    }}
                  >
                    <TrashIcon aria-hidden="true" />
                    {t("connectors.remove")}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              {showMissing && (
                <p id={missingId} className="col-span-2 col-start-2 flex items-start gap-1.5 text-caption text-destructive-fg @2xl/main:col-span-4">
                  <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
                  <span className="min-w-0 break-words">
                    {t("settings.models.notSetUp", { id: link.connectorId })}{" "}
                    <Link to="/connectors" className="underline underline-offset-2">
                      {t("nav.connectors")}
                    </Link>
                    .
                  </span>
                </p>
              )}
            </li>
          )
        })}
      </ol>
      <Button
        variant="outline"
        size="sm"
        className="self-start"
        disabled={chain.length >= MAX_CHAIN}
        onClick={() => {
          const last = chain.at(-1)
          onChange([...chain, ...newKey([{ connectorId: options[0]?.value ?? "ai-gateway", model: last?.model ?? "", ...(last?.effort ? { effort: last.effort } : {}) }])])
        }}
      >
        <PlusIcon data-icon="inline-start" />
        {t("settings.models.add")}
      </Button>
    </>
  )
}

function swap<T>(list: T[], a: number, b: number): T[] {
  const next = [...list]
  ;[next[a], next[b]] = [next[b]!, next[a]!]
  return next
}
