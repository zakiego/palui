// The data every Saklar layout renders, straight from the page loader.
import type { getSaklar } from "@/lib/fns/saklar"

export type SaklarItem = Awaited<ReturnType<typeof getSaklar>>["items"][number]
export type SaklarLive = Awaited<ReturnType<typeof getSaklar>>["live"]

export type SaklarLayoutProps = {
  items: SaklarItem[]
  live: SaklarLive
  onOpen: (id: string) => void
}

// The live detail is shown only when it adds something to the stored one.
export function detailFor(item: SaklarItem, live: SaklarLive): string | null {
  const l = live[item.id]
  return item.statusDetail ?? (l && l.detail !== item.statusDetail ? l.detail : null)
}
