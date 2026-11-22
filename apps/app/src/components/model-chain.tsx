// The model chain editor: one role's ordered models, where the next one takes
// over when a model fails. Used by the global chains on Settings and by an
// agent's own chains on its page. A step is a drag handle, one search over
// every connector's models, a reasoning select and a remove button.
import * as React from "react"
import { Link } from "@tanstack/react-router"
import { BrainIcon, ChevronDownIcon, CircleAlertIcon, GripVerticalIcon, PlusIcon, XIcon } from "lucide-react"

import { connectorIcon } from "@/components/connector-icon"
import { ModelIcon } from "@/components/model-icon"
import { SimpleSelect, type Option } from "@/components/simple-select"
import { Button } from "@/components/ui/button"
import { Command, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { listConnectorModels } from "@/lib/api/settings"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"
import { REASONING_EFFORTS, type ModelChainLink } from "@/shared/constants"

export type Role = "default" | "fast" | "vision"
export const ROLES: Role[] = ["default", "fast", "vision"]
export const ROLE_HELP = { default: "settings.role.default", fast: "settings.role.fast", vision: "settings.role.vision" } as const
export const MAX_CHAIN = 6

// `key` is client only: it lets a row's DOM node, and focus, follow its item on reorder.
export type KeyedLink = ModelChainLink & { key: string }
export type ModelConnector = { id: string; label: string; icon?: string }
export type ModelCatalog = { state: "loading" } | { state: "ok"; models: string[] } | { state: "error"; error: string }
// Every model connector's list, fetched the first time any step's search opens.
export type Catalogs = { byId: Record<string, ModelCatalog>; load: () => void }

// The effort select's value for "no effort set", which leaves the provider's default.
const DEFAULT_EFFORT = "default"

export const stripKeys = (chain: KeyedLink[]): ModelChainLink[] => chain.map(({ connectorId, model, effort }) => ({ connectorId, model, ...(effort ? { effort } : {}) }))

// Stable client keys for chain rows.
export function useLinkKeys() {
  const prefix = React.useId()
  const count = React.useRef(0)
  return React.useCallback((chain: ModelChainLink[]): KeyedLink[] => chain.map((link) => ({ ...link, key: `${prefix}link-${count.current++}` })), [prefix])
}

// The model lists of the page's connectors. Nothing is fetched until a search
// opens, because the closed steps only show the connector name.
export function useCatalogs(options: Option[]): Catalogs {
  const [byId, setById] = React.useState<Record<string, ModelCatalog>>({})
  const requested = React.useRef(new Set<string>())
  const ids = options.map((o) => o.value).join(",")
  const load = React.useCallback(() => {
    for (const id of ids ? ids.split(",") : []) {
      if (requested.current.has(id)) continue
      requested.current.add(id)
      setById((c) => ({ ...c, [id]: { state: "loading" } }))
      listConnectorModels({ data: { id } })
        .then((r) => setById((c) => ({ ...c, [id]: { state: "ok", models: [...new Set(r.models)] } })))
        .catch((e: unknown) => {
          requested.current.delete(id)
          setById((c) => ({ ...c, [id]: { state: "error", error: e instanceof Error ? e.message : String(e) } }))
        })
    }
  }, [ids])
  return { byId, load }
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
  catalogs: Catalogs
  newKey: (chain: ModelChainLink[]) => KeyedLink[]
}) {
  const t = useT()
  const list = React.useRef<HTMLOListElement>(null)
  const [dragging, setDragging] = React.useState<string | null>(null)
  const [announce, setAnnounce] = React.useState("")
  const known = new Set(options.map((o) => o.value))
  const effortOptions = [
    { value: DEFAULT_EFFORT, label: t("settings.effort.default") },
    ...REASONING_EFFORTS.map((e) => ({ value: e, label: t(`settings.effort.${e}`) })),
  ].map((o) => ({ ...o, icon: <BrainIcon aria-hidden="true" className="text-muted-foreground" /> }))
  const setLink = (i: number, patch: Partial<ModelChainLink>) => onChange(chain.map((x, j) => (j === i ? { ...x, ...patch } : x)))
  const focusLater = (selector: string) => requestAnimationFrame(() => list.current?.querySelector<HTMLElement>(selector)?.focus())

  const move = (from: number, to: number) => {
    const next = [...chain]
    const [item] = next.splice(from, 1)
    next.splice(to, 0, item!)
    onChange(next)
    setAnnounce(t("settings.models.moved", { model: item!.model || t("settings.models.pickModel"), n: to + 1 }))
  }

  // While a handle is held, the row follows the pointer across the middle of its neighbours.
  // Positions come from offsetTop (the list is the offset parent), which ignores a row's slide animation.
  // The dragged row itself doesn't count, or it would swap the moment it's pressed.
  const dragTo = (clientY: number) => {
    const from = chain.findIndex((x) => x.key === dragging)
    if (from < 0 || !list.current) return
    const y = clientY - list.current.getBoundingClientRect().top
    const rows = [...list.current.children] as HTMLElement[]
    const to = rows.filter((row, j) => j !== from && row.offsetTop + row.offsetHeight / 2 < y).length
    if (to !== from) move(from, to)
  }

  // A move re-inserts the dragged row, which can drop the handle's pointer capture, so the drag listens on the document.
  const dragToLatest = React.useRef(dragTo)
  dragToLatest.current = dragTo
  React.useEffect(() => {
    if (!dragging) return
    const follow = (e: PointerEvent) => dragToLatest.current(e.clientY)
    const stop = () => setDragging(null)
    document.addEventListener("pointermove", follow)
    document.addEventListener("pointerup", stop)
    document.addEventListener("pointercancel", stop)
    return () => {
      document.removeEventListener("pointermove", follow)
      document.removeEventListener("pointerup", stop)
      document.removeEventListener("pointercancel", stop)
    }
  }, [dragging])

  // Rows slide to their new place instead of jumping (FLIP), unless motion is reduced.
  const tops = React.useRef(new Map<string, number>())
  React.useLayoutEffect(() => {
    const rows = [...(list.current?.children ?? [])] as HTMLElement[]
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches
    const next = new Map<string, number>()
    for (const row of rows) {
      const key = row.dataset.key!
      const top = row.offsetTop
      next.set(key, top)
      const before = tops.current.get(key)
      if (!still && before !== undefined && before !== top) {
        row.animate([{ transform: `translateY(${before - top}px)` }, { transform: "none" }], { duration: 180, easing: "cubic-bezier(0.215, 0.61, 0.355, 1)" })
      }
    }
    tops.current = next
  }, [chain])

  return (
    <>
      <ol ref={list} className="relative flex flex-col gap-4 @2xl/main:gap-2">
        {chain.map((link, i) => {
          const missing = !known.has(link.connectorId)
          // With no model connectors at all, the page already says so: skip the per-step note.
          const showMissing = missing && options.length > 0
          const missingId = `${link.key}-missing`
          const keyAttr = CSS.escape(link.key)
          return (
            <li
              key={link.key}
              data-key={link.key}
              className={cn(
                "grid grid-cols-[2rem_minmax(0,1fr)_auto] items-center gap-x-1.5 gap-y-1.5 rounded-lg transition-[background-color,box-shadow] duration-150 @2xl/main:grid-cols-[2rem_minmax(0,1fr)_9.5rem_auto]",
                dragging === link.key && "bg-muted ring-1 ring-border",
              )}
            >
              <button
                type="button"
                data-chain-grip={link.key}
                aria-label={t("settings.models.reorder", { role, n: i + 1 })}
                className="flex h-8 cursor-grab touch-none items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 active:cursor-grabbing disabled:cursor-default disabled:opacity-50 pointer-coarse:h-10"
                disabled={chain.length === 1}
                onPointerDown={(e) => {
                  if (e.button !== 0) return
                  e.preventDefault()
                  setDragging(link.key)
                }}
                onKeyDown={(e) => {
                  const to = e.key === "ArrowUp" ? i - 1 : e.key === "ArrowDown" ? i + 1 : -1
                  if (to < 0 || to >= chain.length) return
                  e.preventDefault()
                  move(i, to)
                  focusLater(`[data-chain-grip="${keyAttr}"]`)
                }}
              >
                <GripVerticalIcon aria-hidden="true" className="size-4" />
              </button>
              <ModelSearch
                role={role}
                n={i + 1}
                link={link}
                chain={chain}
                options={options}
                catalogs={catalogs}
                describedBy={showMissing ? missingId : undefined}
                onPick={(connectorId, model) => setLink(i, { connectorId, model })}
              />
              <SimpleSelect
                aria-label={t("settings.models.stepEffort", { role, n: i + 1 })}
                className="col-start-2 row-start-2 w-full @2xl/main:col-start-3 @2xl/main:row-start-1"
                value={link.effort ?? DEFAULT_EFFORT}
                options={effortOptions}
                onChange={(v) => setLink(i, { effort: v === DEFAULT_EFFORT ? undefined : (v as ModelChainLink["effort"]) })}
              />
              <Button
                variant="ghost"
                size="icon"
                className="col-start-3 row-start-1 @2xl/main:col-start-4"
                data-chain-remove={link.key}
                aria-label={t("settings.models.remove", { role, n: i + 1 })}
                disabled={chain.length === 1}
                onClick={() => {
                  // The focused button goes with its row. Hand focus to the step that takes its place: its remove button, or its model once it's the only step left.
                  const after = (chain[i + 1] ?? chain[i - 1])!
                  onChange(chain.filter((_, j) => j !== i))
                  focusLater(`[data-chain-${chain.length === 2 ? "model" : "remove"}="${CSS.escape(after.key)}"]`)
                }}
              >
                <XIcon aria-hidden="true" />
              </Button>
              {showMissing && (
                <p id={missingId} className="col-span-2 col-start-2 flex items-start gap-1.5 text-caption text-destructive-fg @2xl/main:col-span-3">
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
      <span role="status" className="sr-only">
        {announce}
      </span>
      <Button
        variant="outline"
        size="sm"
        className="self-start"
        disabled={chain.length >= MAX_CHAIN}
        onClick={() => {
          const last = chain.at(-1)
          const [added] = newKey([{ connectorId: last && known.has(last.connectorId) ? last.connectorId : (options[0]?.value ?? "cliproxy"), model: "", ...(last?.effort ? { effort: last.effort } : {}) }])
          onChange([...chain, added!])
          focusLater(`[data-chain-model="${CSS.escape(added!.key)}"]`)
        }}
      >
        <PlusIcon data-icon="inline-start" />
        {t("settings.models.add")}
      </Button>
    </>
  )
}

// One searchable list of every model on every connector, grouped by connector,
// with the models this chain already uses on top. Picking a model picks its
// connector too. Any typed id is offered per connector, because a gateway can
// serve models it doesn't list.
function ModelSearch({
  role,
  n,
  link,
  chain,
  options,
  catalogs,
  describedBy,
  onPick,
}: {
  role: Role
  n: number
  link: KeyedLink
  chain: KeyedLink[]
  options: Option[]
  catalogs: Catalogs
  describedBy?: string
  onPick: (connectorId: string, model: string) => void
}) {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const [search, setSearch] = React.useState("")
  const typed = search.trim()
  const connector = options.find((o) => o.value === link.connectorId)
  const label = connector ? connector.label : t("settings.models.missing", { id: link.connectorId })
  const seen = new Set<string>([`${link.connectorId}::${link.model}`])
  const inUse = chain.filter((x) => {
    const id = `${x.connectorId}::${x.model}`
    if (!x.model || seen.has(id) || !options.some((o) => o.value === x.connectorId)) return false
    seen.add(id)
    return true
  })
  const notes = options.flatMap((o) => {
    const cat = catalogs.byId[o.value]
    return cat && (cat.state !== "ok" || cat.models.length === 0) ? [{ o, cat }] : []
  })
  const pick = (c: string, m: string) => {
    onPick(c, m)
    setOpen(false)
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (next) {
          setSearch("")
          catalogs.load()
        }
      }}
    >
      <PopoverTrigger
        data-chain-model={link.key}
        aria-label={t("settings.models.stepModel", { role, n })}
        aria-invalid={!connector}
        aria-describedby={describedBy}
        title={link.model ? `${link.model} (${label})` : undefined}
        className="flex h-8 min-w-0 cursor-pointer items-center gap-2 rounded-lg border border-input bg-background ps-2.5 pe-2 text-start text-sm transition-[border-color,background-color] duration-150 outline-none select-none hover:border-foreground/40 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 aria-invalid:border-destructive pointer-coarse:h-10"
      >
        {link.model ? <ModelIcon model={link.model} /> : null}
        <span className={cn("min-w-0 flex-1 truncate font-mono", !link.model && "font-sans text-muted-foreground")}>{link.model || t("settings.models.pickModel")}</span>
        <span className={cn("hidden max-w-36 shrink-0 truncate text-caption sm:inline", connector ? "text-muted-foreground" : "text-destructive-fg")}>{label}</span>
        <ChevronDownIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-(--anchor-width) min-w-80 gap-0 p-0">
        <Command loop query={search} onQueryChange={setSearch}>
          <CommandInput placeholder={t("settings.models.search")} autoCapitalize="none" autoCorrect="off" spellCheck={false} />
          {/* A connector with no models to list has an empty group, which the list hides, so its state sits above the list. */}
          {!typed && notes.length > 0 && (
            <ul className="flex flex-col gap-1 border-b px-3 py-2 text-caption text-muted-foreground">
              {notes.map(({ o, cat }) => (
                <li key={o.value} className="flex min-w-0 items-start gap-1.5">
                  {cat.state === "loading" ? <Spinner aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" /> : <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />}
                  <span className="min-w-0 break-words">
                    <span className="font-medium text-foreground">{o.label}</span>{" "}
                    {cat.state === "loading" ? t("settings.models.loading") : cat.state === "error" ? t("settings.models.listFailed", { error: cat.error }) : t("settings.models.noneListed")}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <CommandList className="max-h-80">
            {inUse.length > 0 && !typed && (
              <CommandGroup heading={t("settings.models.inChain")}>
                {inUse.map((x) => (
                  <CommandItem key={`use-${x.key}`} value={`use::${x.connectorId}::${x.model}`} onSelect={() => pick(x.connectorId, x.model)} className="font-mono">
                    <ModelIcon model={x.model} />
                    <span className="min-w-0 flex-1 truncate">{x.model}</span>
                    <span className="max-w-32 shrink-0 truncate font-sans text-caption text-muted-foreground">{options.find((o) => o.value === x.connectorId)?.label}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {options.map((o) => {
              const cat = catalogs.byId[o.value]
              const models = cat?.state === "ok" ? cat.models : []
              return (
                <CommandGroup
                  key={o.value}
                  heading={
                    <span className="flex items-center gap-1.5">
                      {o.icon}
                      {o.label}
                    </span>
                  }
                >
                  {models.map((m) => (
                    <CommandItem key={m} value={`${o.value}::${m}`} keywords={[m, o.label]} data-checked={o.value === link.connectorId && m === link.model} onSelect={() => pick(o.value, m)} className="font-mono">
                      <ModelIcon model={m} />
                      <span className="min-w-0 flex-1 truncate">{m}</span>
                    </CommandItem>
                  ))}
                  {typed && !models.includes(typed) && (
                    <CommandItem value={`custom::${o.value}::${typed}`} keywords={[typed]} onSelect={() => pick(o.value, typed)}>
                      <ModelIcon model={typed} />
                      <span className="min-w-0 flex-1 truncate">{t("settings.models.useCustom", { id: typed })}</span>
                    </CommandItem>
                  )}
                </CommandGroup>
              )
            })}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
