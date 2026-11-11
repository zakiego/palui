// A fake Baileys socket for tests: the test emits connection and message events
// and inspects what the runtime sent. Nothing here touches the network.
import type { WAMessage } from "baileys"
import type { WaConnectionUpdate, WaContent, WaEventMap, WaSocket } from "./socket"

type Listener = (arg: never) => void

export class FakeSocket implements WaSocket {
  private listeners = new Map<string, Set<Listener>>()
  sent: { jid: string; content: WaContent; options?: { quoted?: WAMessage } }[] = []
  presence: { type: string; jid?: string }[] = []
  pairingRequests: string[] = []
  pairingCode = "ABCD1234"
  ended = false
  loggedOut = false
  user?: { id: string; lid?: string; name?: string }
  authState: WaSocket["authState"]
  lids = new Map<string, string>()
  private nextId = 1

  constructor(opts: { registered?: boolean } = {}) {
    this.authState = { creds: opts.registered ? { registered: true, account: {} } : { registered: false } }
  }

  signalRepository = {
    lidMapping: {
      getPNForLID: async (lid: string) => this.lids.get(lid) ?? null,
      getLIDForPN: async (pn: string) => [...this.lids.entries()].find(([, p]) => p === pn)?.[0] ?? null,
    },
  }

  ev = {
    on: <E extends keyof WaEventMap>(event: E, fn: (arg: WaEventMap[E]) => void) => {
      const set = this.listeners.get(event) ?? new Set()
      set.add(fn as Listener)
      this.listeners.set(event, set)
    },
    off: <E extends keyof WaEventMap>(event: E, fn: (arg: WaEventMap[E]) => void) => {
      this.listeners.get(event)?.delete(fn as Listener)
    },
  }

  listenerCount(event: keyof WaEventMap): number {
    return this.listeners.get(event)?.size ?? 0
  }

  emit<E extends keyof WaEventMap>(event: E, arg: WaEventMap[E]) {
    for (const fn of [...(this.listeners.get(event) ?? [])]) (fn as (a: WaEventMap[E]) => void)(arg)
  }

  open(userId = "628980001472:3@s.whatsapp.net") {
    this.user = { id: userId, lid: "111222333:3@lid", name: "Palui" }
    this.authState.creds.registered = true
    this.authState.creds.account = {}
    this.emit("connection.update", { connection: "open" })
  }

  close(statusCode: number) {
    const error = Object.assign(new Error(`closed ${statusCode}`), { output: { statusCode } })
    const update: WaConnectionUpdate = { connection: "close", lastDisconnect: { error, date: new Date() } }
    this.emit("connection.update", update)
  }

  qr(value: string) {
    this.emit("connection.update", { qr: value })
  }

  message(m: Partial<WAMessage> & { key: WAMessage["key"] }, type = "notify") {
    this.emit("messages.upsert", { messages: [m as WAMessage], type })
  }

  async sendMessage(jid: string, content: WaContent, options?: { quoted?: WAMessage }) {
    this.sent.push(options ? { jid, content, options } : { jid, content })
    return { key: { remoteJid: jid, fromMe: true, id: `OUT${this.nextId++}` }, message: "text" in content ? { conversation: content.text } : {} } as WAMessage
  }

  async requestPairingCode(phone: string) {
    this.pairingRequests.push(phone)
    return this.pairingCode
  }

  async sendPresenceUpdate(type: "composing" | "paused" | "available" | "unavailable", jid?: string) {
    this.presence.push(jid ? { type, jid } : { type })
  }

  async logout() {
    this.loggedOut = true
    this.close(401)
  }

  end() {
    this.ended = true
  }
}
