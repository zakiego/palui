// Does utusan still answer this chat? (PLAN 5.1, 18.) Both devices on the shared
// number receive every message, so chats are split, never shared: a chat moves
// to Palui only after its utusan binding is gone. utusan.json is mounted
// read-only in compose; this never writes.
//
// utusan's shape (src/config/schema.ts): { bindings: [{ id, channel: "whatsapp" |
// "web", peer: { kind: "direct" | "group", id: "<jid>" }, stub?, ... }] }. A stub
// binding still replies ("not configured"), so it counts as bound.
import { readFileSync } from "node:fs"

export type UtusanBinding = "bound" | "free" | "unknown"

// Where compose mounts ~/.utusan/utusan.json (see compose.yaml).
export const UTUSAN_CONFIG_PATH = "/utusan/utusan.json"

// Compare JIDs leniently: utusan peer ids are normally the full remoteJid, but a
// hand-edited binding may hold bare digits or carry a device suffix.
function sameChat(a: string, b: string): boolean {
  const norm = (j: string) => {
    const [local = "", server = ""] = j.trim().toLowerCase().split("@")
    return { digits: local.split(":")[0]?.replace(/\D/g, "") ?? "", server }
  }
  const x = norm(a)
  const y = norm(b)
  if (!x.digits || x.digits !== y.digits) return false
  // Bare digits on either side match any person JID with those digits, never a group.
  if (!x.server || !y.server) return x.server !== "g.us" && y.server !== "g.us"
  return x.server === y.server
}

// `jids`: every form of the chat we know. A WhatsApp DM arrives as a phone JID or
// a LID with the other form alongside, and utusan may bind either, so all are checked.
export function checkUtusan(jids: string | (string | undefined)[], file = UTUSAN_CONFIG_PATH): { state: UtusanBinding; bindingId?: string } {
  const forms = (Array.isArray(jids) ? jids : [jids]).filter((j): j is string => !!j?.trim())
  let raw: unknown
  try {
    raw = JSON.parse(readFileSync(file, "utf8"))
  } catch {
    return { state: "unknown" }
  }
  if (!raw || typeof raw !== "object") return { state: "unknown" }
  const bindings = (raw as { bindings?: unknown }).bindings
  if (bindings === undefined) return { state: "free" }
  if (!Array.isArray(bindings)) return { state: "unknown" }
  for (const b of bindings) {
    if (!b || typeof b !== "object") continue
    const { id, channel, peer } = b as { id?: unknown; channel?: unknown; peer?: { id?: unknown } }
    if (channel !== "whatsapp" || typeof peer?.id !== "string") continue
    const peerId = peer.id
    if (forms.some((j) => sameChat(peerId, j))) return { state: "bound", ...(typeof id === "string" ? { bindingId: id } : {}) }
  }
  return { state: "free" }
}

export function utusanBindsChat(jids: string | (string | undefined)[], file = UTUSAN_CONFIG_PATH): UtusanBinding {
  return checkUtusan(jids, file).state
}
