// The avatar field on the agent page and the new-agent sheet (PLAN 19.7).
import * as React from "react"
import { CheckIcon, ImageUpIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { AgentAvatar, AVATAR_COLORS, AVATAR_ICONS, resizeAvatar } from "@/components/agent-avatar"
import { Button } from "@/components/ui/button"
import { FieldDescription, FieldLegend, FieldSet } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

// A photo, or an icon on a color (PLAN 19.7). A photo wins while it's set.
export function AvatarPicker({
  agent,
  onImage,
  onIcon,
  onColor,
}: {
  agent: { isDefault: boolean; avatarImage: string | null; avatarIcon: string | null; avatarColor: string | null }
  onImage: (v: string | null) => void
  onIcon: (v: string | null) => void
  onColor: (v: string | null) => void
}) {
  const t = useT()
  const fileRef = React.useRef<HTMLInputElement>(null)
  const [busy, setBusy] = React.useState(false)
  const pickFile = async (file: File | undefined) => {
    if (!file) return
    if (!/^image\//.test(file.type)) {
      toast.error(t("agents.avatar.notImage"))
      return
    }
    setBusy(true)
    try {
      onImage(await resizeAvatar(file))
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
      if (fileRef.current) fileRef.current.value = ""
    }
  }
  const color = agent.avatarColor ?? "zinc"
  return (
    <FieldSet>
      <FieldLegend variant="label">{t("agents.avatar")}</FieldLegend>
      <div className="flex items-center gap-3">
        <AgentAvatar agent={agent} size="lg" />
        <div className="flex flex-wrap gap-2">
          <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp,image/heic" className="sr-only" tabIndex={-1} onChange={(e) => void pickFile(e.target.files?.[0])} />
          <Button variant="outline" size="sm" disabled={busy} onClick={() => fileRef.current?.click()}>
            {busy ? <Spinner data-icon="inline-start" /> : <ImageUpIcon data-icon="inline-start" />}
            {agent.avatarImage ? t("agents.avatar.change") : t("agents.avatar.upload")}
          </Button>
          {agent.avatarImage && (
            <Button variant="ghost" size="sm" onClick={() => onImage(null)}>
              <XIcon data-icon="inline-start" />
              {t("agents.avatar.remove")}
            </Button>
          )}
        </div>
      </div>
      {agent.avatarImage && <FieldDescription>{t("agents.avatar.photoWins")}</FieldDescription>}
      <div role="radiogroup" aria-label={t("agents.avatar.icon")} className="grid max-w-md grid-cols-8 gap-1.5">
        {Object.entries(AVATAR_ICONS).map(([name, Icon]) => {
          const on = (agent.avatarIcon ?? (agent.isDefault ? null : "bot")) === name
          return (
            <button
              key={name}
              type="button"
              role="radio"
              aria-checked={on}
              aria-label={name.replace(/-/g, " ")}
              data-avatar-tone={on ? color : "zinc"}
              onClick={() => onIcon(on && agent.isDefault ? null : name)}
              className="flex aspect-square items-center justify-center rounded-md bg-(--avatar-bg) text-(--avatar-fg) outline-offset-2 ring-foreground transition-[box-shadow] focus-visible:outline-2 focus-visible:outline-focus-ring aria-checked:ring-2 [&_svg]:size-4"
            >
              <Icon aria-hidden="true" />
            </button>
          )
        })}
      </div>
      <div role="radiogroup" aria-label={t("agents.avatar.color")} className="flex flex-wrap gap-1.5">
        {AVATAR_COLORS.map((c) => {
          const on = color === c
          return (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={on}
              aria-label={c}
              data-avatar-tone={c}
              onClick={() => onColor(c === "zinc" ? null : c)}
              className="flex size-8 items-center justify-center rounded-full border border-border bg-(--avatar-bg) text-(--avatar-fg) outline-offset-2 ring-foreground focus-visible:outline-2 focus-visible:outline-focus-ring aria-checked:ring-2 [&_svg]:size-4"
            >
              {on && <CheckIcon aria-hidden="true" />}
            </button>
          )
        })}
      </div>
    </FieldSet>
  )
}
