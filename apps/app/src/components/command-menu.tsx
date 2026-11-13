// ⌘K (PLAN 11.3): page navigation, "turn off <id>" / "turn on <id>",
// "new reminder" and "open chat".
import * as React from "react"
import { useNavigate } from "@tanstack/react-router"
import { BellPlusIcon, MessageSquareIcon, PowerIcon, PowerOffIcon } from "lucide-react"

import { Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator } from "@/components/ui/command"
import { useConnectorToggle } from "@/hooks/use-connector-toggle"
import { useT } from "@/lib/i18n"
import { PAGES } from "@/lib/pages"

export type CommandConnector = { id: string; label: string; status: string; enabled: boolean }

export function CommandMenu({ open, onOpenChange, connectors }: { open: boolean; onOpenChange: (open: boolean) => void; connectors: CommandConnector[] }) {
  const t = useT()
  const navigate = useNavigate()
  const toggle = useConnectorToggle()

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        onOpenChange(!open)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [open, onOpenChange])

  const run = (fn: () => void) => {
    onOpenChange(false)
    fn()
  }
  const live = connectors.filter((c) => c.status !== "not_set_up")

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title={t("command.button")} description={t("command.placeholder")}>
      <Command>
      <CommandInput placeholder={t("command.placeholder")} />
      <CommandEmpty>{t("command.empty")}</CommandEmpty>
      <CommandList>
        <CommandGroup heading={t("command.actions")}>
          <CommandItem value="new reminder" onSelect={() => run(() => void navigate({ to: "/reminders", search: { new: true } }))}>
            <BellPlusIcon aria-hidden="true" />
            {t("command.newReminder")}
          </CommandItem>
          <CommandItem value="open chat" onSelect={() => run(() => void navigate({ to: "/chats" }))}>
            <MessageSquareIcon aria-hidden="true" />
            {t("command.openChat")}
          </CommandItem>
        </CommandGroup>
        {live.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading={t("command.connectors")}>
              {live.map((c) =>
                c.enabled ? (
                  <CommandItem key={`off-${c.id}`} value={`turn off ${c.id} ${c.label}`} onSelect={() => run(() => void toggle(c, false))}>
                    <PowerOffIcon aria-hidden="true" />
                    <span className="min-w-0 truncate">
                      turn off <span className="font-mono">{c.id}</span>
                    </span>
                  </CommandItem>
                ) : (
                  <CommandItem key={`on-${c.id}`} value={`turn on ${c.id} ${c.label}`} onSelect={() => run(() => void toggle(c, true))}>
                    <PowerIcon aria-hidden="true" />
                    <span className="min-w-0 truncate">
                      turn on <span className="font-mono">{c.id}</span>
                    </span>
                  </CommandItem>
                ),
              )}
            </CommandGroup>
          </>
        )}
        <CommandSeparator />
        <CommandGroup heading={t("command.pages")}>
          {PAGES.map(({ to, label, Icon }) => (
            <CommandItem key={to} value={`go ${t(label)} ${to}`} onSelect={() => run(() => void navigate({ to }))}>
              <Icon aria-hidden="true" />
              {t(label)}
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
      </Command>
    </CommandDialog>
  )
}
