// Dev only: fills $DATA_DIR/palui.db with worst-case demo data (long names,
// emoji, unbreakable ids, many rows, every connector state) so each dashboard
// page can be checked for overflow and empty-vs-full states.
//   DATA_DIR=/tmp/palui-demo pnpm tsx scripts/seed-demo.ts
// Refuses to run when NODE_ENV is production.
import { ensureChat, ensureWebChat } from "@palui/core/chats"
import { config, masterKey } from "@palui/core/config"
import { logEvent } from "@palui/core/events"
import { setConnectorStatus, upsertConnector } from "@palui/core/saklar/store"
import { dbPath, jobs, messages, newId, openDb, pendingApprovals, reminders, runs, toolCalls } from "@palui/db"
import { manifestFor } from "../src/lib/manifests"

if (config().mode === "prod") throw new Error("seed-demo is for dev databases only")

const db = openDb(dbPath(config().dataDir))
const key = masterKey()
const now = Date.now()
const H = 3600_000

const add = (id: string, type: string, opts: { label?: string; status: "on" | "paused" | "error" | "connecting" | "not_set_up"; detail?: string; secrets?: Record<string, string>; config?: Record<string, unknown>; trust?: "own" | "external" }) => {
  upsertConnector(db, { id, manifest: manifestFor(type)!, label: opts.label, secrets: opts.secrets, config: opts.config, trust: opts.trust, enabled: opts.status !== "paused" && opts.status !== "not_set_up" }, key)
  if (opts.status !== "not_set_up") setConnectorStatus(db, id, opts.status, opts.detail ?? null)
}

add("telegram", "telegram", { status: "on", secrets: { botToken: "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqqrr" } })
add("whatsapp", "whatsapp", { status: "error", detail: "Connection closed (440): another session took over this linked device, so Palui paused instead of fighting it" })
add("cliproxy", "cliproxy", { status: "on", secrets: { apiKey: "sk-cliproxy-0123456789abcdef" } })
add("ai-gateway", "ai-gateway", { status: "paused", secrets: { apiKey: "zkg_0123456789abcdef" } })
add("recall", "recall", { status: "connecting", secrets: { apiKey: "rcl_0123456789a91f" } })
add("mcp-vorneo", "mcp", { label: "Vorneo pipeline and property listings for every kabupaten in Kalimantan Selatan", status: "on", config: { url: "https://vorneo.id/mcp" } })
add("mcp-linear", "mcp", { label: "Linear 🚀", status: "on", trust: "external", config: { url: "https://mcp.linear.app/mcp" } })
add("reminders", "reminders", { status: "on" })
add("jobs", "jobs", { status: "paused" })
add("webfetch", "webfetch", { status: "on" })
add("voice", "voice", { status: "not_set_up" })

const web = ensureWebChat(db)
const tg = ensureChat(db, { channel: "telegram", externalId: "987654321", title: "Zaki 🌴 (Telegram DM with a rather long display name)" })
const wa = ensureChat(db, { channel: "whatsapp", externalId: "6281234567890@s.whatsapp.net", title: "" })

const runId = newId("run")
db.insert(runs).values({ id: runId, chatId: web.id, trigger: "message", model: "claude-sonnet-4-6", tokensIn: 18234, tokensOut: 912, status: "ok", startedAt: now - 2 * H, ms: 8342 }).run()
db.insert(messages).values({ id: newId("msg"), chatId: web.id, sessionId: web.sessionId, role: "user", content: { text: "" }, text: "ingetin bayar listrik besok jam 9, terus cek pipeline vorneo minggu ini ya", createdAt: now - 2 * H }).run()
db.insert(toolCalls).values({ id: newId("tc"), runId, connectorId: "mcp-vorneo", tool: "mcp-vorneo__list_deals_for_the_current_week_grouped_by_stage_and_agent", class: "read", args: { stage: "masuk", week: "2026-W46" }, resultPreview: '[{"id":"deal_01","title":"Rumah 2 lantai di Banjarbaru"}]', status: "ok", ms: 1240, createdAt: now - 2 * H + 1000 }).run()
db.insert(toolCalls).values({ id: newId("tc"), runId, connectorId: "reminders", tool: "reminders__ingatkan", class: "write", args: { text: "bayar listrik", when: "besok 09:00" }, resultPreview: "Besok, Rabu 11 Nov, 09.00 WITA", status: "ok", ms: 85, createdAt: now - 2 * H + 2000 }).run()
db.insert(messages).values({ id: newId("msg"), chatId: web.id, sessionId: web.sessionId, role: "assistant", content: { text: "" }, text: "Siap. Pengingat: Besok, Rabu 11 Nov, 09.00 WITA.\nPipeline Vorneo minggu ini: 3 deal masuk, 1 survei.", runId, createdAt: now - 2 * H + 9000 }).run()
db.insert(pendingApprovals).values({ id: newId("apr"), runId, chatId: web.id, approvalId: "ap_1", tool: "mcp-linear__delete_issue", toolClass: "destructive", argsPreview: '{"id":"PAL-123"}', messages: [], expiresAt: now + 10 * 60_000, createdAt: now }).run()
db.insert(messages).values({ id: newId("msg"), chatId: tg.id, sessionId: tg.sessionId, role: "user", content: { text: "" }, text: "https://example.com/a/very/long/url/that/never/breaks/naturally/0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz", createdAt: now - H }).run()

for (let i = 0; i < 14; i++) {
  db.insert(runs).values({ id: newId("run"), chatId: i % 3 ? tg.id : wa.id, jobId: null, trigger: "message", model: i % 4 ? "claude-sonnet-4-6" : "gemini-2.5-flash-preview-with-a-long-model-id", tokensIn: 4000 + i * 1500, tokensOut: 300 + i * 40, status: i === 3 ? "error" : "ok", error: i === 3 ? "429 from CLIProxyAPI: all 4 Claude accounts are rate limited; failed over to ai-gateway which was paused" : null, startedAt: now - i * 22 * H, ms: 2000 + i * 300 }).run()
}

const jobId = newId("job")
db.insert(jobs).values({ id: jobId, name: "Daily briefing", kind: "briefing", rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", tz: "Asia/Makassar", localStart: "2026-01-01T07:00:00", prompt: "Brief me.", chatId: tg.id, nextRunAt: now + 10 * H, lastRunAt: now - 14 * H, lastStatus: "ok", lastResult: "Pagi! Hari ini 2 pengingat, 3 deal Vorneo masuk, cuaca Banjarbaru cerah berawan 31°C.", dailyTokenCap: 50_000, createdAt: now, updatedAt: now }).run()
db.insert(jobs).values({ id: newId("job"), name: "Heartbeat with a name long enough to wrap onto a second line on a phone", kind: "heartbeat", rrule: "FREQ=MINUTELY;INTERVAL=30;BYHOUR=8,9,10,11,12,13,14,15,16,17,18,19,20,21,22", tz: "Asia/Makassar", localStart: "2026-01-01T08:00:00", prompt: "NOTHING unless it matters.", chatId: tg.id, enabled: false, dailyTokenCap: 30_000, createdAt: now, updatedAt: now }).run()
db.insert(runs).values({ id: newId("run"), chatId: tg.id, jobId, trigger: "schedule", model: "claude-sonnet-4-6", tokensIn: 21000, tokensOut: 1400, status: "ok", startedAt: now - 1 * H, ms: 15000 }).run()

const rem = (text: string, inHours: number, rrule: string | null = null, status: "pending" | "done" | "cancelled" = "pending") =>
  db.insert(reminders).values({ id: newId("rem"), chatId: tg.id, text, rrule, tz: "Asia/Makassar", localStart: "2026-11-11T09:00:00", fireAt: now + inHours * H, scheduledFor: now + inHours * H, status, createdAt: now, updatedAt: now }).run()
rem("Bayar listrik", 15)
rem("Kirim laporan mingguan ke tim Vorneo, lengkap dengan rekap deal yang masuk, survei yang sudah dijadwalkan, dan catatan follow up untuk setiap agen 📊", 40)
rem("Standup", 20, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR")
rem("Old one", -30, null, "done")

// A reply with the Markdown the model actually sends (bold, lists, code, a link).
db.insert(messages).values({ id: newId("msg"), chatId: web.id, sessionId: web.sessionId, role: "user", content: { text: "" }, text: "rekap minggu ini dong", createdAt: now - 30 * 60_000 }).run()
db.insert(messages).values({
  id: newId("msg"),
  chatId: web.id,
  sessionId: web.sessionId,
  role: "assistant",
  content: { text: "" },
  text: "**Rekap minggu ini** (W46):\n\n1. *3 deal* masuk di Vorneo, 1 survei dijadwalkan\n2. Pengingat `bayar listrik` besok jam 09.00\n3. Detail di https://vorneo.id/deals?week=2026-W46\n\n- Telegram: on\n- WhatsApp: **error 440**, perlu pairing ulang\n\n```\npalui saklar on whatsapp\n```\n\nLihat [dashboard Palui](https://palui.zakiego.com/app/saklar) untuk detail.",
  createdAt: now - 29 * 60_000,
}).run()

// Worst case for the chat list: many chats, long and non-Latin titles, emoji.
const titles = ["Keluarga besar 👨‍👩‍👧‍👦 Banjarmasin", "مجموعة العائلة", "東京出張チーム", "A", "Vorneo agents: Banjarbaru, Martapura, Pelaihari, Tanah Laut, Barito Kuala and Hulu Sungai", "🔥🔥🔥", "supercalifragilisticexpialidocious_channel_without_spaces_0123456789"]
for (let i = 0; i < 18; i++) {
  const c = ensureChat(db, { channel: i % 2 ? "telegram" : "whatsapp", externalId: `demo-${i}`, title: titles[i % titles.length] })
  db.insert(messages).values({ id: newId("msg"), chatId: c.id, sessionId: c.sessionId, role: "user", content: { text: "" }, text: i % 3 ? "ok sip 👍" : "Tolong cek lagi angka penjualan bulan ini dibandingkan bulan lalu, terutama untuk unit rumah tipe 36 di Banjarbaru", createdAt: now - (i + 3) * 3 * H }).run()
}

// Huge counts: a heavy run today, and a long tail of reminders and events.
db.insert(runs).values({ id: newId("run"), chatId: web.id, trigger: "message", model: "claude-opus-4-1-20250805-thinking-extended-context", tokensIn: 98_765_432, tokensOut: 1_234_567, status: "ok", startedAt: now - 5 * 60_000, ms: 3_723_000 }).run()
for (let i = 0; i < 24; i++) rem(i % 5 ? `Follow up deal #${1000 + i}` : `Telepon pak RT soal iuran keamanan dan kebersihan bulan ${i + 1} 🏠`, 50 + i * 7, i % 6 ? null : "FREQ=MONTHLY;BYMONTHDAY=1")
for (let i = 0; i < 40; i++) logEvent(db, { connectorId: i % 3 ? "mcp-vorneo" : "cliproxy", kind: i % 4 ? "tool" : "status", level: i % 9 ? "info" : "warn", message: i % 4 ? `mcp-vorneo__list_deals_for_the_current_week_grouped_by_stage_and_agent ok in ${100 + i * 13} ms` : "status change" }, now - (i + 10) * 900_000)

for (let i = 0; i < 6; i++) logEvent(db, { connectorId: i % 2 ? "telegram" : "whatsapp", kind: "status", level: i === 1 ? "error" : i === 4 ? "warn" : "info", message: i === 1 ? "error: 401 Unauthorized from api.telegram.org (the bot token was revoked in @BotFather)" : `status change ${i}` }, now - i * 600_000)

console.log(`seeded ${dbPath(config().dataDir)}`)
