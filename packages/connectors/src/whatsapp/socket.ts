// The slice of a Baileys socket the runtime uses, plus the real factory. Tests pass
// a fake socket that implements this interface; the real one is only created when
// the runtime starts for real, and Baileys is imported lazily so tests never load it.
import { mkdir, readFile, rm } from "node:fs/promises"
import { join } from "node:path"
import type { WAMessage, WAMessageKey } from "baileys"

export type WaConnectionUpdate = {
  connection?: "connecting" | "open" | "close"
  qr?: string
  isNewLogin?: boolean
  lastDisconnect?: { error: Error | undefined; date?: Date }
}

export type WaUpsert = { messages: WAMessage[]; type: string }

export type WaEventMap = {
  "connection.update": WaConnectionUpdate
  "messages.upsert": WaUpsert
}

export type WaContent = { text: string } | { react: { text: string; key: WAMessageKey } }

export type WaSocket = {
  ev: {
    on<E extends keyof WaEventMap>(event: E, fn: (arg: WaEventMap[E]) => void): void
    off<E extends keyof WaEventMap>(event: E, fn: (arg: WaEventMap[E]) => void): void
  }
  user?: { id: string; lid?: string; name?: string }
  authState: { creds: { registered?: boolean; account?: unknown; me?: unknown } }
  sendMessage(jid: string, content: WaContent, options?: { quoted?: WAMessage }): Promise<WAMessage | undefined>
  requestPairingCode(phoneNumber: string): Promise<string>
  sendPresenceUpdate(type: "composing" | "paused" | "available" | "unavailable", jid?: string): Promise<void>
  presenceSubscribe?(jid: string): Promise<void>
  groupMetadata?(jid: string): Promise<{ subject?: string }>
  logout(msg?: string): Promise<void>
  end(error: Error | undefined): void
  signalRepository?: { lidMapping?: { getPNForLID(lid: string): Promise<string | null>; getLIDForPN(pn: string): Promise<string | null> } }
}

export type SocketFactory = (opts: { authDir: string }) => Promise<WaSocket>
export type MediaDownloader = (msg: WAMessage, sock: WaSocket) => Promise<Buffer>

// A session is linked once pairing succeeded: Baileys then stores `account` (QR and
// code flows both). `registered` is only set by the code flow.
export function isLinked(creds: WaSocket["authState"]["creds"]): boolean {
  return !!creds.account || creds.registered === true
}

export type SessionState = "none" | "linked" | "incomplete"

// "incomplete": an interrupted pairing left creds with `me` but no account. Baileys
// then tries to log in instead of showing a QR, and pairing never starts (utusan
// waSession.ts). Such a session is worthless and safe to delete.
export async function inspectSession(authDir: string): Promise<SessionState> {
  let creds: { registered?: unknown; account?: unknown; me?: unknown }
  try {
    creds = JSON.parse(await readFile(join(authDir, "creds.json"), "utf8")) as typeof creds
  } catch {
    return "none"
  }
  if (creds.account || creds.registered === true) return "linked"
  return creds.me ? "incomplete" : "none"
}

export async function clearIncompleteSession(authDir: string): Promise<SessionState> {
  const state = await inspectSession(authDir)
  if (state === "incomplete") await rm(authDir, { recursive: true, force: true })
  return state
}

export const createBaileysSocket: SocketFactory = async ({ authDir }) => {
  const baileys = await import("baileys")
  const { default: pino } = await import("pino")
  await mkdir(authDir, { recursive: true })
  const { state, saveCreds } = await baileys.useMultiFileAuthState(authDir)
  const { version } = await baileys.fetchLatestBaileysVersion()
  const sock = baileys.makeWASocket({
    version,
    auth: state,
    logger: pino({ level: "silent" }),
    browser: baileys.Browsers.ubuntu("Chrome"),
    // Keeps the phone the primary "online" device, so notifications stay there.
    markOnlineOnConnect: false,
    syncFullHistory: false,
  })
  sock.ev.on("creds.update", saveCreds)
  return sock as unknown as WaSocket
}

export const downloadBaileysMedia: MediaDownloader = async (msg) => {
  const baileys = await import("baileys")
  return (await baileys.downloadMediaMessage(msg, "buffer", {})) as Buffer
}
