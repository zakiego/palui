// Connector edge-case rules (PLAN 4.3) that need more than one table to decide.
import { and, desc, eq, inArray } from "drizzle-orm"
import { chats, passkey, type Channel, type DB } from "@palui/db"
import { ensureChat, type ChatRow } from "../chats"
import { getOwner } from "../owner"
import type { ChannelHandle } from "./define"
import type { ConnectorRow } from "./store"

// The owner's DM on a channel: the chat they last wrote from, or one made from
// the owner identity (a Telegram user id is also their private chat id; a
// WhatsApp phone JID is a valid DM address).
export function ownerChat(db: DB, channel: Channel): ChatRow | undefined {
  if (channel === "web") return db.select().from(chats).where(eq(chats.channel, "web")).get()
  const owner = getOwner(db, channel)
  if (!owner) return undefined
  const ids = [owner.externalId, owner.altId].filter((x): x is string => !!x)
  const existing = db
    .select()
    .from(chats)
    .where(and(eq(chats.channel, channel), eq(chats.connectorId, ""), inArray(chats.externalId, ids), eq(chats.kind, "dm")))
    .orderBy(desc(chats.lastMessageAt))
    .get()
  if (existing) return existing
  return ensureChat(db, { channel, externalId: owner.externalId, kind: "dm", title: owner.displayName ?? "" })
}

export function dashboardReachable(db: DB): boolean {
  return !!db.select({ id: passkey.id }).from(passkey).limit(1).get()
}

export type ChannelEntry = { id: string; row: Pick<ConnectorRow, "kind">; handle: { channel?: ChannelHandle } }

// `/connectors off <channel>` sent from that same channel: allowed only when another
// channel is reachable or the dashboard is (a passkey exists). Returns the way
// back for the reply, or null when turning it off would lock the owner out.
export function selfChannelOffUndo(db: DB, from: Channel, running: ChannelEntry[], targetId: string): string | null {
  const others: string[] = []
  for (const e of running) {
    const ch = e.handle.channel
    if (!ch || e.id === targetId || ch.channel === from || ch.channel === "web") continue
    let ok = false
    try {
      ok = ch.reachable()
    } catch {
      ok = false
    }
    if (ok) others.push(ch.channel)
  }
  const ways: string[] = []
  if (others.length) ways.push(`kirim \`/connectors on ${targetId}\` dari ${others.map(channelName).join(" atau ")}`)
  if (dashboardReachable(db)) ways.push("nyalakan lagi dari dashboard")
  return ways.length ? ways.join(", atau ") : null
}

export function channelName(ch: string): string {
  return ch === "telegram" ? "Telegram" : ch === "whatsapp" ? "WhatsApp" : ch === "web" ? "web chat" : ch
}
