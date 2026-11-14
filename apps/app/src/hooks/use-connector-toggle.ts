// Pause or resume a connector: no confirmation, a Sonner toast with Undo
// (PLAN 4.2, 11.5). Used by the connector cards, Home and ⌘K.
import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { toast } from "sonner"
import { pauseConnectorFn, resumeConnectorFn } from "@/lib/api/connectors"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

export function useConnectorToggle() {
  const router = useRouter()
  const t = useT()
  const apply = React.useCallback(
    async (c: { id: string; label: string }, on: boolean, undoable = true): Promise<boolean> => {
      try {
        const res = on ? await resumeConnectorFn({ data: { id: c.id } }) : await pauseConnectorFn({ data: { id: c.id } })
        void router.invalidate()
        toast.success(on ? t("connectors.resumed", { name: c.label }) : t("connectors.paused", { name: c.label }), {
          description: res.runtimeReachable ? undefined : t("runtime.down"),
          action: undoable ? { label: t("common.undo"), onClick: () => void apply(c, !on, false) } : undefined,
        })
        return true
      } catch (err) {
        toast.error(errorMessage(err))
        void router.invalidate()
        return false
      }
    },
    [router, t],
  )
  // Resolves false when the change didn't apply, so an optimistic Switch can roll back.
  return React.useCallback((c: { id: string; label: string }, on: boolean) => apply(c, on), [apply])
}
