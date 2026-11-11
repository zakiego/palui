// Dev only: the small insert helpers every fixture uses. Rows go in through the
// same tables the dashboard reads, so a fixture tests the real queries and
// components, never a mocked shape.
import { ensureChat, ensureWebChat, type ChatRow } from "@palui/core/chats"
import { masterKey } from "@palui/core/config"
import { saveAttachment } from "@palui/core/attachments/service"
import { setConnectorStatus, upsertConnector } from "@palui/core/connectors/store"
import { type Settings, setSetting } from "@palui/core/settings"
import {
  chats,
  events,
  hooks,
  jobs,
  messages,
  newId,
  pendingApprovals,
  reminders,
  runs,
  schema,
  toolCalls,
  type Channel,
  type ConnectorStatus,
  type DB,
  type EventLevel,
  type Trust,
} from "@palui/db"
import { encrypt } from "@palui/db/crypto"
import { eq } from "drizzle-orm"
import { manifestFor } from "@/lib/manifests"

export const MIN = 60_000
export const H = 3600_000
export const D = 24 * H

// Passkeys a fixture adds carry this prefix, so a reset never touches a real one.
export const FIXTURE_PASSKEY_PREFIX = "fx_"

export type Kit = ReturnType<typeof createKit>

type ConnectorOpts = {
  label?: string
  status: ConnectorStatus
  detail?: string
  // Defaults to "running unless paused or a draft". Set it to make an enabled
  // connector that is paused anyway, which the header counts as attention.
  enabled?: boolean
  secrets?: Record<string, string>
  config?: Record<string, unknown>
  trust?: Trust
}

export function createKit(db: DB, now = Date.now()) {
  const key = masterKey()
  const owner = db.select({ id: schema.user.id }).from(schema.user).limit(1).get()

  return {
    db,
    now,
    key,

    connector(id: string, type: string, opts: ConnectorOpts) {
      const manifest = manifestFor(type)
      if (!manifest) throw new Error(`fixture: unknown connector type "${type}"`)
      const enabled = opts.enabled ?? (opts.status !== "paused" && opts.status !== "not_set_up")
      upsertConnector(db, { id, manifest, label: opts.label, secrets: opts.secrets, config: opts.config, trust: opts.trust, enabled }, key, now)
      if (opts.status !== "not_set_up") setConnectorStatus(db, id, opts.status, opts.detail ?? null, now)
    },

    webChat(): ChatRow {
      return ensureWebChat(db, now)
    },

    chat(channel: Channel, externalId: string, title: string, opts: { kind?: "dm" | "group"; allowed?: boolean; createdAt?: number } = {}): ChatRow {
      const row = ensureChat(db, { channel, externalId, title, kind: opts.kind, allowed: opts.allowed }, opts.createdAt ?? now)
      // ensureChat skips an empty title on update and never sets these two.
      if (opts.createdAt !== undefined) db.update(chats).set({ createdAt: opts.createdAt }).where(eq(chats.id, row.id)).run()
      return row
    },

    message(chat: ChatRow, role: "user" | "assistant", text: string, at: number, extra: Partial<typeof messages.$inferInsert> = {}) {
      const id = newId("msg")
      db.insert(messages).values({ id, chatId: chat.id, sessionId: chat.sessionId, role, content: { text }, text, createdAt: at, ...extra }).run()
      return id
    },

    run(values: Omit<typeof runs.$inferInsert, "id">) {
      const id = newId("run")
      db.insert(runs).values({ id, ...values }).run()
      return id
    },

    tool(values: Omit<typeof toolCalls.$inferInsert, "id">) {
      db.insert(toolCalls).values({ id: newId("tc"), ...values }).run()
    },

    approval(values: Omit<typeof pendingApprovals.$inferInsert, "id" | "messages" | "approvalId">) {
      const id = newId("apr")
      db.insert(pendingApprovals).values({ id, approvalId: id, messages: [], ...values }).run()
    },

    reminder(values: Omit<typeof reminders.$inferInsert, "id" | "scheduledFor" | "localStart" | "createdAt" | "updatedAt"> & { updatedAt?: number }) {
      db.insert(reminders).values({ id: newId("rem"), scheduledFor: values.fireAt, localStart: "2026-11-11T09:00:00", createdAt: now, updatedAt: now, ...values }).run()
    },

    job(values: Omit<typeof jobs.$inferInsert, "id" | "localStart" | "createdAt" | "updatedAt"> & { createdAt?: number }) {
      const id = newId("job")
      db.insert(jobs).values({ id, localStart: "2026-01-01T07:00:00", createdAt: now, updatedAt: now, ...values }).run()
      return id
    },

    // Straight into the table: no redaction and no listeners, so a fixture can
    // write thousands of rows and keep its exact strings.
    event(values: { at: number; message: string; level?: EventLevel; connectorId?: string | null; kind?: string }) {
      db.insert(events).values({ at: values.at, level: values.level ?? "info", connectorId: values.connectorId ?? null, kind: values.kind ?? "tool", message: values.message }).run()
    },

    hook(values: { name: string; chatId: string; mode?: "forward" | "summarize"; enabled?: boolean; lastAt?: number | null; createdAt?: number; slug?: string }) {
      const id = newId("hook")
      db.insert(hooks)
        .values({
          id,
          slug: values.slug ?? id.slice(-16),
          name: values.name,
          secret: encrypt("phk_fixture-secret-not-a-real-one", key),
          mode: values.mode ?? "forward",
          chatId: values.chatId,
          enabled: values.enabled ?? true,
          lastAt: values.lastAt ?? null,
          createdAt: values.createdAt ?? now,
        })
        .run()
    },

    // Only when an owner exists (a passkey row needs a user). The key material
    // is fake: these rows are for the Settings list, they can't sign anyone in.
    passkey(values: { name: string | null; deviceType?: "singleDevice" | "multiDevice"; backedUp?: boolean; createdAt?: number | null }) {
      if (!owner) return
      const id = `${FIXTURE_PASSKEY_PREFIX}${newId("pk")}`
      db.insert(schema.passkey)
        .values({
          id,
          name: values.name,
          publicKey: "fixture",
          userId: owner.id,
          credentialID: id,
          counter: 0,
          deviceType: values.deviceType ?? "multiDevice",
          backedUp: values.backedUp ?? true,
          transports: "internal",
          createdAt: values.createdAt === null ? null : new Date(values.createdAt ?? now),
        })
        .run()
    },

    // Fake bytes behind the right magic number, so the type check passes.
    attachment(values: { chatId: string; title: string; description?: string; kind: "pdf" | "jpeg"; filename?: string | null; size?: number; createdAt?: number }) {
      const magic = values.kind === "pdf" ? Buffer.from("%PDF-1.7\n") : Buffer.from([0xff, 0xd8, 0xff, 0xe0])
      const bytes = Buffer.alloc(Math.max(values.size ?? 2048, magic.length + 8))
      magic.copy(bytes)
      bytes.write(newId("fx"), magic.length)
      const mimeType = values.kind === "pdf" ? "application/pdf" : "image/jpeg"
      saveAttachment(db, { chatId: values.chatId, title: values.title, description: values.description, mimeType, filename: values.filename, bytes }, key, values.createdAt ?? now)
    },

    setting<K extends keyof Settings>(settingKey: K, value: Settings[K]) {
      setSetting(db, settingKey, value, now)
    },
  }
}
