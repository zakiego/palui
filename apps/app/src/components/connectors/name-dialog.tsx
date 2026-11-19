// Asks for a new connector's name before its page opens, for a `multiple`
// connector with no presets: the name becomes the id, and the id is the prefix
// of every tool name, so it has to be right before the draft exists.
import * as React from "react"
import type { ConnectorManifest } from "@/shared/connectors/define"

import { newConnectorId } from "@/components/connectors/targets"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useT } from "@/lib/i18n"

export const NAME_MAX = 80

export function NameDialog({
  manifest,
  existing,
  onSubmit,
  onClose,
}: {
  manifest: ConnectorManifest
  existing: { id: string }[]
  onSubmit: (name: string, id: string) => void
  onClose: () => void
}) {
  const t = useT()
  const [name, setName] = React.useState("")
  const clean = name.trim()
  const id = newConnectorId(manifest, existing, undefined, clean || manifest.name)

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("connectors.nameTitle")}</DialogTitle>
          <DialogDescription className="[overflow-wrap:anywhere]">{t("connectors.nameHint", { id })}</DialogDescription>
        </DialogHeader>
        <form
          id="connector-name-form"
          onSubmit={(e) => {
            e.preventDefault()
            if (clean) onSubmit(clean, id)
          }}
        >
          <Field>
            <FieldLabel htmlFor="connector-name">{t("connectors.field.name")}</FieldLabel>
            <Input
              id="connector-name"
              dir="auto"
              autoFocus
              required
              autoComplete="off"
              maxLength={NAME_MAX}
              placeholder={manifest.name}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
        </form>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="connector-name-form" disabled={!clean}>
            {t("connectors.nameContinue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
