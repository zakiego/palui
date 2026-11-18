import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { ArchiveIcon, BanIcon } from "lucide-react"
import { toast } from "sonner"
import { SimpleSelect } from "@/components/simple-select"
import { setChatSaveMediaFn } from "@/lib/api/attachments"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

const OFF = "off"
// Retention choices in days; 0 keeps files until someone deletes them.
const DAYS = [7, 30, 90, 365, 0] as const

// Auto-save for one chat: off (the default), or keep every photo and document
// sent there for so many days. Same shape as the reaction select next to it.
export function SaveMediaControl({ chat }: { chat: { id: string; title: string; saveMedia: boolean; saveMediaDays: number } }) {
  const t = useT()
  const router = useRouter()
  const current = chat.saveMedia ? String(chat.saveMediaDays) : OFF
  const [value, setValue] = React.useState(current)
  React.useEffect(() => setValue(current), [current])

  const label = (days: number) => {
    if (days === 0) return t("chats.saveMediaForever")
    if (days === 365) return t("chats.saveMediaYear")
    return t("chats.saveMediaDays", { n: String(days) })
  }

  const pick = async (next: string) => {
    if (next === value) return
    const before = value
    setValue(next)
    try {
      await setChatSaveMediaFn({ data: next === OFF ? { chatId: chat.id, on: false } : { chatId: chat.id, on: true, days: Number(next) } })
      void router.invalidate()
    } catch (err) {
      setValue(before)
      toast.error(errorMessage(err))
    }
  }

  return (
    <SimpleSelect
      size="sm"
      className="w-fit min-w-0"
      aria-label={t("chats.saveMediaFor", { name: chat.title })}
      value={value}
      options={[
        { value: OFF, label: t("chats.saveMediaOff"), icon: <BanIcon aria-hidden="true" /> },
        ...DAYS.map((d) => ({ value: String(d), label: label(d), icon: <ArchiveIcon aria-hidden="true" /> })),
      ]}
      onChange={(next) => void pick(next)}
    />
  )
}
