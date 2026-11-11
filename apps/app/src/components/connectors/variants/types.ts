// The data every Connectors page layout renders, straight from the page loader.
import type { getConnectorsPage } from "@/lib/fns/connectors"

export type ConnectorItem = Awaited<ReturnType<typeof getConnectorsPage>>["items"][number]
export type ConnectorLiveMap = Awaited<ReturnType<typeof getConnectorsPage>>["live"]

export type ConnectorLayoutProps = {
  items: ConnectorItem[]
  live: ConnectorLiveMap
  onOpen: (id: string) => void
}

// The live detail is shown only when it adds something to the stored one.
export function detailFor(item: ConnectorItem, live: ConnectorLiveMap): string | null {
  const l = live[item.id]
  return item.statusDetail ?? (l && l.detail !== item.statusDetail ? l.detail : null)
}
