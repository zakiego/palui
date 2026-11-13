// The avatar field on the agent page and the new-agent sheet (PLAN 19.7).
import * as React from "react"
import { CheckIcon, ImageUpIcon, XIcon } from "lucide-react"
import { toast } from "sonner"
import { Radio, RadioGroup } from "@ui-sdk/react/radio"

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
  const icon = agent.avatarIcon ?? (agent.isDefault ? null : "bot")
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
      <RadioGroup
        aria-label={t("agents.avatar.icon")}
        value={icon}
        onValueChange={(v) => onIcon(String(v))}
        className="grid max-w-md grid-cols-8 gap-2"
      >
        {Object.entries(AVATAR_ICONS).map(([name, Icon]) => {
          const on = icon === name
          return (
            <Radio
              key={name}
              value={name}
              aria-label={name.replace(/-/g, " ")}
              data-avatar-tone={on ? color : "zinc"}
              // The default agent can clear its icon to go back to the Palui mark.
              onClick={on && agent.isDefault ? () => onIcon(null) : undefined}
              className="flex aspect-square cursor-pointer items-center justify-center rounded-md bg-(--avatar-bg) text-(--avatar-fg) ring-foreground transition-[box-shadow] duration-150 data-checked:ring-2 [&_svg]:size-4"
            >
              <Icon aria-hidden="true" />
            </Radio>
          )
        })}
      </RadioGroup>
      <RadioGroup
        aria-label={t("agents.avatar.color")}
        value={color}
        onValueChange={(v) => onColor(v === "zinc" ? null : String(v))}
        className="flex flex-wrap gap-2"
      >
        {AVATAR_COLORS.map((c) => (
          <Radio
            key={c}
            value={c}
            aria-label={c}
            data-avatar-tone={c}
            className="group/swatch flex size-8 cursor-pointer items-center justify-center rounded-full border border-border bg-(--avatar-bg) text-(--avatar-fg) ring-foreground data-checked:ring-2 [&_svg]:size-4"
          >
            <CheckIcon aria-hidden="true" className="hidden group-data-checked/swatch:block" />
          </Radio>
        ))}
      </RadioGroup>
    </FieldSet>
  )
}
