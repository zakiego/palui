// Dev only: the small insert helpers every fixture uses. Rows go in through the
// same tables the dashboard reads, so a fixture tests the real queries and
// components, never a mocked shape.
import { ensureChat, ensureWebChat, type ChatRow } from "@palui/core/chats"
import { masterKey } from "@palui/core/config"
import { saveAttachment } from "@palui/core/attachments/service"
import { setConnectorStatus, upsertConnector } from "@palui/core/connectors/store"
import { getSetting, type Settings, setSetting } from "@palui/core/settings"
import { addUsage, usageKeys } from "@palui/core/budget/index"
import { localToUtc, utcToLocal } from "@palui/core/reminders/time"
import { createRoutine, setPaused, type RoutineInput } from "@palui/core/routines/service"
import {
  agents,
  chats,
  events,
  hooks,
  instructions,
  jobs,
  messages,
  newId,
  pendingApprovals,
  preferences,
  reminders,
  routineOccurrences,
  routines,
  runs,
  schema,
  toolCalls,
  type Channel,
  type ConnectorStatus,
  type DB,
  type EventLevel,
  type RoutineOccurrenceStatus,
  type RoutineSourceState,
  type Trust,
} from "@palui/db"
import { encrypt } from "@palui/db/crypto"
import { crc32, deflateSync } from "node:zlib"
import { eq } from "drizzle-orm"
import { manifestFor } from "@/lib/manifests"

export const MIN = 60_000
export const H = 3600_000
export const D = 24 * H

// Passkeys a fixture adds carry this prefix, so a reset never touches a real one.
export const FIXTURE_PASSKEY_PREFIX = "fx_"

export type Kit = ReturnType<typeof createKit>

// A 48 px gradient PNG as a data URL: the shape a browser-resized avatar photo has.
export function gradientAvatar(px = 48): string {
  const rows: Buffer[] = []
  for (let y = 0; y < px; y++) {
    const row = Buffer.alloc(1 + px * 3)
    for (let x = 0; x < px; x++) row.set([Math.round((255 * x) / px), Math.round((255 * y) / px), 180], 1 + x * 3)
    rows.push(row)
  }
  const chunk = (type: string, data: Buffer) => {
    const body = Buffer.concat([Buffer.from(type), data])
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length)
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(crc32(body))
    return Buffer.concat([len, body, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(px, 0)
  ihdr.writeUInt32BE(px, 4)
  ihdr.set([8, 2, 0, 0, 0], 8)
  const png = Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(Buffer.concat(rows))), chunk("IEND", Buffer.alloc(0))])
  return `data:image/png;base64,${png.toString("base64")}`
}

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

    // Raw insert, so a fixture can use values at the form limits.
    agent(values: { slug: string; name: string; persona: string; ownerPowers?: "off" | "approve"; guestTools?: "none" | "safe"; dailyTokenCap?: number; recallContainer?: string | null; telegramConnectorId?: string | null; avatarImage?: string | null; avatarIcon?: string | null; avatarColor?: string | null; tokensToday?: number }) {
      const id = newId("agt")
      const { tokensToday, ...row } = values
      db.insert(agents).values({ id, ...row, createdAt: now, updatedAt: now }).run()
      if (tokensToday) addUsage(db, usageKeys.agent(id, now, getSetting(db, "timezone")), tokensToday, now)
      return id
    },

    // A chat answered by an agent (PLAN 19.2).
    assign(chat: ChatRow, agentId: string, allowed = true) {
      db.update(chats).set({ agentId, allowed }).where(eq(chats.id, chat.id)).run()
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

    // No agentId means Palui's (PLAN 19.1).
    instruction(values: { title: string; body: string; enabled?: boolean; agentId?: string; createdAt?: number }) {
      const at = values.createdAt ?? now
      db.insert(instructions).values({ id: newId("ins"), title: values.title, body: values.body, enabled: values.enabled ?? true, agentId: values.agentId ?? null, createdAt: at, updatedAt: at }).run()
    },

    // Raw insert: fixtures may use values the service would normalize or refuse.
    preference(prefKey: string, value: string) {
      db.insert(preferences).values({ key: prefKey, value, updatedAt: now }).run()
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

    // Through the service, so the stored config and schedule are what the agent
    // would have made. `paused`: true for open-ended, a time to pause until.
    routine(chatId: string, input: RoutineInput, opts: { state?: RoutineSourceState; paused?: true | number; lastError?: string; createdAt?: number } = {}) {
      const at = opts.createdAt ?? now
      const r = createRoutine(db, chatId, input, at, opts.state ? { state: opts.state, nextRefreshAt: now + 12 * H } : undefined)
      if (opts.paused) setPaused(db, r.id, true, opts.paused === true ? null : opts.paused, now)
      // Raw: a fixture may store a config the service would refuse today.
      if (opts.lastError) db.update(routines).set({ lastError: opts.lastError }).where(eq(routines.id, r.id)).run()
      return r.id
    },

    // A tool source's cached times for today and tomorrow, as a read would leave them.
    toolState(tz: string, times: { key: string; label: string; time: string }[], days = 2): RoutineSourceState {
      const today = utcToLocal(now, tz).slice(0, 10)
      const dates = Array.from({ length: days }, (_, i) => utcToLocal(localToUtc(`${today}T12:00`, tz) + i * D, tz).slice(0, 10))
      return { entries: dates.flatMap((date) => times.map((t) => ({ date, key: t.key, label: t.label, at: localToUtc(`${date}T${t.time}`, tz) }))), fetched: dates, failures: 0, lastOkAt: now }
    },

    routineOccurrence(routineId: string, chatId: string, values: { label: string; at: number; status: RoutineOccurrenceStatus; answer?: string; note?: string; followUps?: number; nextFollowUpAt?: number | null; deadlineAt?: number | null; fallback?: boolean }) {
      const answeredAt = values.answer ? values.at + 7 * MIN : null
      db.insert(routineOccurrences)
        .values({
          id: newId("rocc"),
          routineId,
          chatId,
          key: `${values.at}:${values.label}`,
          label: values.label,
          scheduledAt: values.at,
          firedAt: values.at,
          status: values.status,
          answer: values.answer ?? null,
          answeredAt,
          answers: values.answer ? [{ answer: values.answer, ...(values.note ? { text: values.note } : {}), at: answeredAt! }] : [],
          followUps: values.followUps ?? 0,
          nextFollowUpAt: values.nextFollowUpAt ?? null,
          deadlineAt: values.deadlineAt ?? null,
          fallback: values.fallback ?? false,
          createdAt: values.at,
          updatedAt: answeredAt ?? values.at,
        })
        .run()
    },

    setting<K extends keyof Settings>(settingKey: K, value: Settings[K]) {
      setSetting(db, settingKey, value, now)
    },
  }
}
