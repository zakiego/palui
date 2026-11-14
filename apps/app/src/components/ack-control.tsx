import * as React from "react"
import { useRouter } from "@tanstack/react-router"
import { BanIcon } from "lucide-react"
import { toast } from "sonner"
import { ACK_EMOJI_CHOICES } from "@/shared/constants"
import { SimpleSelect } from "@/components/simple-select"
import { setChatAckFn } from "@/lib/api/agents"
import { errorMessage } from "@/lib/format"
import { useT } from "@/lib/i18n"

const OFF = "off"

// The reaction a WhatsApp chat gets while the agent thinks: none, or which
// emoji. Same shape as the reply mode select it sits next to.
export function AckControl({ chat }: { chat: { id: string; title: string; ack: boolean; ackEmoji: string } }) {
  const t = useT()
  const router = useRouter()
  const current = chat.ack ? chat.ackEmoji : OFF
  const [value, setValue] = React.useState(current)
  React.useEffect(() => setValue(current), [current])
  // A custom emoji set with /ack stays pickable.
  const known = (ACK_EMOJI_CHOICES as readonly string[]).includes(chat.ackEmoji)
  const emojis = known ? [...ACK_EMOJI_CHOICES] : [chat.ackEmoji, ...ACK_EMOJI_CHOICES]

  const pick = async (next: string) => {
    if (next === value) return
    const before = value
    setValue(next)
    try {
      await setChatAckFn({ data: next === OFF ? { chatId: chat.id, on: false } : { chatId: chat.id, on: true, emoji: next } })
      void router.invalidate()
    } catch (err) {
      setValue(before)
      toast.error(errorMessage(err))
    }
  }

  return (
    <SimpleSelect
      size="sm"
      aria-label={t("chats.ackFor", { name: chat.title })}
      value={value}
      options={[
        { value: OFF, label: t("chats.ackOff"), icon: <BanIcon aria-hidden="true" /> },
        ...emojis.map((e) => ({ value: e, label: t("chats.ack"), icon: <span>{e}</span> })),
      ]}
      onChange={(next) => void pick(next)}
    />
  )
}
