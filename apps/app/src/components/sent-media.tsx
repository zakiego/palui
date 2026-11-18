// The files a reply's run sent with send_media, shown above the reply in the
// chat transcript (they reach the chat before the text). Only what was sent:
// the bytes stay on the server, so each row is the kind, name, size, caption
// and whether the channel took it.
import type * as React from "react"
import { FileTextIcon, FilmIcon, ImageIcon, MicIcon, MusicIcon, StickerIcon } from "lucide-react"

import { Item, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { StatusText, type StatusTone } from "@/components/ui/status-text"
import type { SentMediaKind, SentMediaView } from "@/lib/api/chats"
import { fileSize } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

const ICON: Record<SentMediaKind, React.ReactNode> = {
  image: <ImageIcon />,
  video: <FilmIcon />,
  voice: <MicIcon />,
  audio: <MusicIcon />,
  document: <FileTextIcon />,
  sticker: <StickerIcon />,
}

const TONE: Record<SentMediaView["status"], StatusTone> = {
  sent: "success",
  failed: "destructive",
  cancelled: "secondary",
  pending: "outline",
  sending: "outline",
}

export function SentMedia({ media, className }: { media: SentMediaView[]; className?: string }) {
  const t = useT()
  if (media.length === 0) return null
  return (
    <ItemGroup aria-label={t("chats.sentMedia")} className={cn("max-w-[min(100%,28rem)] gap-2", className)}>
      {media.map((m) => {
        const kind = ICON[m.kind] ? m.kind : "document"
        return (
          <Item key={m.id} role="listitem" variant="outline" size="sm" className="min-w-0 flex-nowrap items-start bg-card">
            <ItemMedia variant="icon" className="size-8 rounded-md bg-muted text-muted-foreground">
              {ICON[kind]}
            </ItemMedia>
            <ItemContent className="min-w-0">
              <ItemTitle dir="auto" title={m.filename} className="line-clamp-2 w-full [overflow-wrap:anywhere]">
                {m.filename || t(`chats.media.${kind}`)}
              </ItemTitle>
              <ItemDescription className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span>
                  {t(`chats.media.${kind}`)} · <span className="tabular-nums">{fileSize(m.size)}</span>
                </span>
                <StatusText tone={TONE[m.status] ?? "outline"}>{t(`chats.media.${m.status}`)}</StatusText>
              </ItemDescription>
              {m.caption && (
                <p dir="auto" className="line-clamp-3 text-sm whitespace-pre-wrap [overflow-wrap:anywhere]">
                  {m.caption}
                </p>
              )}
              {m.status === "failed" && m.error && (
                <p title={m.error} className="line-clamp-2 text-xs text-destructive-fg [overflow-wrap:anywhere]">
                  {m.error}
                </p>
              )}
            </ItemContent>
          </Item>
        )
      })}
    </ItemGroup>
  )
}
