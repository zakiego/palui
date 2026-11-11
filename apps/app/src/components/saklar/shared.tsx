// Pieces every Saklar layout shares (PLAN 11.5), so the three Explore variants
// and the Home map differ in arrangement only, never in behavior:
// - SaklarSwitch: pause never asks, the Switch moves at once, a toast offers
//   Undo, aria-label "Pause <name>" / "Resume <name>", a 44 px target.
// - useStatusAnnouncer: one polite announcement per status change, never on load.
import * as React from "react"
import type { ConnectorManifest } from "@palui/core/saklar/define"

import { Switch } from "@/components/ui/switch"
import { useSaklarToggle } from "@/hooks/use-saklar-toggle"
import { useT, type Key } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export const SAKLAR_GROUPS: { kind: ConnectorManifest["kind"]; label: Key }[] = [
  { kind: "channel", label: "saklar.group.channel" },
  { kind: "model", label: "saklar.group.model" },
  { kind: "memory", label: "saklar.group.memory" },
  { kind: "tools", label: "saklar.group.tools" },
  { kind: "skill", label: "saklar.group.skill" },
]

export const STATUS_LABEL: Record<string, Key> = {
  on: "status.on",
  paused: "status.paused",
  error: "status.error",
  connecting: "status.connecting",
  not_set_up: "status.notSetUp",
}

export type SaklarToggleTarget = { id: string; label: string; enabled: boolean; status: string }

// Connectors the owner just flipped here, with the status the flip leads to.
// The toast already says that, so the announcer skips that one status (and only
// that one, so a later error is still announced): one announcement per change
// (PLAN 11.5).
const flippedHere = new Map<string, { at: number; expect: string }>()
const FLIP_QUIET_MS = 5000

// The Switch with the house rules baked in. The wrapper is the 44 px box; the
// Switch's own ::after grows its hit area to the same 44 px.
export function SaklarSwitch({ item, className }: { item: SaklarToggleTarget; className?: string }) {
  const t = useT()
  const toggle = useSaklarToggle()
  // Optimistic: the Switch moves at once, the toast confirms or rolls back.
  const [pending, setPending] = React.useState<boolean | null>(null)
  React.useEffect(() => setPending(null), [item.enabled, item.status])
  const checked = pending ?? item.enabled
  return (
    <span className={cn("flex size-11 shrink-0 items-center justify-center", className)}>
      <Switch
        checked={checked}
        aria-label={checked ? t("saklar.pause", { name: item.label }) : t("saklar.resume", { name: item.label })}
        className="after:-inset-x-1.5 after:-inset-y-[13px]"
        onCheckedChange={(next) => {
          flippedHere.set(item.id, { at: Date.now(), expect: next ? "connecting" : "paused" })
          setPending(next)
          void toggle(item, next).then((ok) => {
            if (!ok) setPending(null)
          })
        }}
      />
    </span>
  )
}

// Returns the text for one `aria-live="polite"` region: "<name>: <status>" for
// the connector whose status just changed somewhere else (the runtime, chat,
// the CLI). Nothing is announced on load, a refresh that changes nothing
// announces nothing, and a flip made here is left to its toast.
export function useStatusAnnouncer(items: { id: string; label: string; status: string }[]): string {
  const t = useT()
  const seen = React.useRef<Map<string, string> | null>(null)
  const [message, setMessage] = React.useState("")
  const signature = items.map((i) => `${i.id}=${i.status}`).join("|")
  React.useEffect(() => {
    const next = new Map(items.map((i) => [i.id, i.status]))
    const prev = seen.current
    seen.current = next
    if (!prev) return
    const now = Date.now()
    const fromHere = (i: { id: string; status: string }) => {
      const f = flippedHere.get(i.id)
      return !!f && f.expect === i.status && now - f.at < FLIP_QUIET_MS
    }
    const changed = items.filter((i) => prev.has(i.id) && prev.get(i.id) !== i.status && !fromHere(i))
    if (!changed.length) return
    setMessage(changed.map((i) => `${i.label}: ${t(STATUS_LABEL[i.status] ?? "status.notSetUp")}`).join(". "))
    // `signature` captures every status, so `items` itself isn't a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature, t])
  return message
}

export function StatusAnnouncer({ items }: { items: { id: string; label: string; status: string }[] }) {
  const message = useStatusAnnouncer(items)
  return (
    <span role="status" aria-live="polite" className="sr-only">
      {message}
    </span>
  )
}
