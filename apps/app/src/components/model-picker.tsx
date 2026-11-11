// A searchable model picker for a model chain step. It lists the ids the
// connector's gateway offers, and still takes any id typed into the search,
// because a gateway can serve models it doesn't list.
import * as React from "react"
import { ChevronDownIcon, CircleAlertIcon } from "lucide-react"

import { ModelIcon } from "@/components/model-icon"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type ModelCatalog = { state: "loading" } | { state: "ok"; models: string[] } | { state: "error"; error: string }

export function ModelPicker({
  value,
  catalog,
  onChange,
  className,
  "aria-label": ariaLabel,
}: {
  value: string
  catalog: ModelCatalog | undefined
  onChange: (model: string) => void
  className?: string
  "aria-label"?: string
}) {
  const t = useT()
  const [open, setOpen] = React.useState(false)
  const [search, setSearch] = React.useState("")
  const models = catalog?.state === "ok" ? catalog.models : []
  const typed = search.trim()
  const pick = (model: string) => {
    onChange(model)
    setOpen(false)
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (next) setSearch("")
      }}
    >
      <PopoverTrigger
        aria-label={ariaLabel}
        title={value || undefined}
        className={cn(
          "flex h-8 min-w-0 cursor-pointer items-center gap-2 rounded-lg border border-input bg-background ps-2.5 pe-2 text-start text-sm transition-[border-color,background-color] duration-150 outline-none select-none hover:border-foreground/40 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 pointer-coarse:h-10",
          className,
        )}
      >
        {value ? <ModelIcon model={value} /> : null}
        <span className={cn("min-w-0 flex-1 truncate font-mono", !value && "font-sans text-muted-foreground")}>{value || t("settings.models.pickModel")}</span>
        <ChevronDownIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-(--anchor-width) min-w-72 gap-0 p-0">
        <Command loop>
          <CommandInput value={search} onValueChange={setSearch} placeholder={t("settings.models.search")} autoCapitalize="none" autoCorrect="off" spellCheck={false} />
          <CommandList>
            {catalog?.state === "loading" && (
              <div className="flex items-center gap-2 px-2 py-3 text-caption text-muted-foreground">
                <Spinner />
                {t("settings.models.loading")}
              </div>
            )}
            {catalog?.state === "error" && (
              <p className="flex items-start gap-1.5 px-2 py-3 text-caption text-muted-foreground">
                <CircleAlertIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
                <span className="min-w-0 break-words">{t("settings.models.listFailed", { error: catalog.error })}</span>
              </p>
            )}
            {catalog?.state === "ok" && !typed && <CommandEmpty>{t("settings.models.noneListed")}</CommandEmpty>}
            {models.length > 0 && (
              <CommandGroup>
                {models.map((m) => (
                  <CommandItem key={m} value={m} data-checked={m === value} onSelect={() => pick(m)} className="font-mono">
                    <ModelIcon model={m} />
                    <span className="min-w-0 flex-1 truncate">{m}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {typed && !models.includes(typed) && (
              <CommandGroup forceMount>
                <CommandItem forceMount value={`custom:${typed}`} onSelect={() => pick(typed)}>
                  <ModelIcon model={typed} />
                  <span className="min-w-0 flex-1 truncate">{t("settings.models.useCustom", { id: typed })}</span>
                </CommandItem>
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
