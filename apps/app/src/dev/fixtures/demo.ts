// Dev only: the data the dashboard was designed against. Every connector
// state, a few long names, a Markdown reply, a pending approval. It reads well
// on purpose; `worst.ts` is the one that goes looking for trouble.
import { BRIEFING, RECAP, SHOLAT } from "@palui/core/flows/test-helpers"
import { D, H, MIN, type Kit } from "./kit"

export function demo(k: Kit) {
  const { now } = k

  k.connector("telegram", "telegram", { status: "on", secrets: { botToken: "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqqrr" } })
  k.connector("whatsapp", "whatsapp", { status: "error", detail: "Connection closed (440): another session took over this linked device, so Palui paused instead of fighting it" })
  k.connector("cliproxy", "cliproxy", { status: "on", secrets: { apiKey: "sk-cliproxy-0123456789abcdef" } })
  k.connector("ai-gateway", "ai-gateway", { status: "paused", secrets: { apiKey: "zkg_0123456789abcdef" } })
  k.connector("recall", "recall", { status: "connecting", secrets: { apiKey: "rcl_0123456789a91f" } })
  k.connector("t3code", "t3code", { status: "on", secrets: { pairingCode: "0123456789abcdef" }, config: { url: "http://host.docker.internal:3773" } })
  k.connector("mcp-vorneo", "mcp", { label: "Vorneo pipeline and property listings for every kabupaten in Kalimantan Selatan", status: "on", config: { url: "https://vorneo.id/mcp" } })
  k.connector("mcp-linear", "mcp", { label: "Linear 🚀", status: "on", trust: "external", config: { url: "https://mcp.linear.app/mcp" } })
  k.connector("reminders", "reminders", { status: "on" })
  k.connector("jobs", "jobs", { status: "paused" })
  k.connector("flows", "flows", { status: "on" })
  k.connector("chats", "chats", { status: "on" })
  k.connector("webfetch", "webfetch", { status: "on" })
  k.connector("notes", "notes", { status: "on" })
  k.connector("settings", "settings", { status: "on" })
  k.connector("remote-mcp", "remote-mcp", { status: "on" })
  k.connector("mcp-jadwal-sholat", "mcp", { label: "jadwal-sholat", status: "on", trust: "external", config: { url: "https://sholat.zakiego.com/mcp", authMode: "none", addedFromChat: true } })
  k.instruction({ title: "Jadwal sholat", body: "Untuk pertanyaan jadwal sholat, pakai tools mcp-jadwal-sholat. Kalau server itu mati, pakai JSON API di https://sholat.zakiego.com/api/v1 lewat web fetch.", createdAt: now - 3 * H })
  k.instruction({ title: "Ringkasan pagi", body: "Mulai dengan cuaca, lalu agenda, lalu pengingat hari ini.", enabled: false, createdAt: now - 2 * H })
  k.preference("default_city", "Banjarmasin")
  k.preference("timezone", "Asia/Makassar")
  k.connector("attachments", "attachments", { status: "on" })
  k.connector("voice", "voice", { status: "not_set_up" })

  const web = k.webChat()
  const tg = k.chat("telegram", "987654321", "Zaki 🌴 (Telegram DM with a rather long display name)")
  const wa = k.chat("whatsapp", "6281234567890@s.whatsapp.net", "")

  const runId = k.run({ chatId: web.id, trigger: "message", model: "claude-sonnet-4-6", tokensIn: 18234, tokensOut: 912, status: "ok", startedAt: now - 2 * H, ms: 8342 })
  k.message(web, "user", "ingetin bayar listrik besok jam 9, terus cek pipeline vorneo minggu ini ya", now - 2 * H)
  k.tool({ runId, connectorId: "mcp-vorneo", tool: "mcp-vorneo__list_deals_for_the_current_week_grouped_by_stage_and_agent", class: "read", args: { stage: "masuk", week: "2026-W46" }, resultPreview: '[{"id":"deal_01","title":"Rumah 2 lantai di Banjarbaru"}]', status: "ok", ms: 1240, createdAt: now - 2 * H + 1000 })
  k.tool({ runId, connectorId: "reminders", tool: "reminders__set_reminder", class: "write", args: { text: "bayar listrik", when: "besok 09:00" }, resultPreview: "Besok, Rabu 11 Nov, 09.00 WITA", status: "ok", ms: 85, createdAt: now - 2 * H + 2000 })
  k.message(web, "assistant", "Siap. Pengingat: Besok, Rabu 11 Nov, 09.00 WITA.\nPipeline Vorneo minggu ini: 3 deal masuk, 1 survei.", now - 2 * H + 9000, { runId })
  k.approval({ runId, chatId: web.id, tool: "mcp-linear__delete_issue", toolClass: "destructive", argsPreview: '{"id":"PAL-123"}', expiresAt: now + 10 * MIN, createdAt: now })
  k.message(tg, "user", "https://example.com/a/very/long/url/that/never/breaks/naturally/0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz", now - H)

  for (let i = 0; i < 14; i++) {
    k.run({
      chatId: i % 3 ? tg.id : wa.id,
      trigger: "message",
      model: i % 4 ? "claude-sonnet-4-6" : "gemini-2.5-flash-preview-with-a-long-model-id",
      tokensIn: 4000 + i * 1500,
      tokensOut: 300 + i * 40,
      status: i === 3 ? "error" : "ok",
      error: i === 3 ? "429 from CLIProxyAPI: all 4 Claude accounts are rate limited; failed over to ai-gateway which was paused" : null,
      startedAt: now - i * 22 * H,
      ms: 2000 + i * 300,
    })
  }

  const jobId = k.job({ name: "Daily briefing", kind: "briefing", rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", tz: "Asia/Makassar", prompt: "Brief me.", chatId: tg.id, nextRunAt: now + 10 * H, lastRunAt: now - 14 * H, lastStatus: "ok", lastResult: "Pagi! Hari ini 2 pengingat, 3 deal Vorneo masuk, cuaca Banjarbaru cerah berawan 31°C.", dailyTokenCap: 50_000 })
  k.job({ name: "Heartbeat with a name long enough to wrap onto a second line on a phone", kind: "heartbeat", rrule: "FREQ=MINUTELY;INTERVAL=30;BYHOUR=8,9,10,11,12,13,14,15,16,17,18,19,20,21,22", tz: "Asia/Makassar", prompt: "NOTHING unless it matters.", chatId: tg.id, enabled: false, dailyTokenCap: 30_000 })
  k.run({ chatId: tg.id, jobId, trigger: "schedule", model: "claude-sonnet-4-6", tokensIn: 21000, tokensOut: 1400, status: "ok", startedAt: now - H, ms: 15000 })

  // Flows (RFC flows): a morning briefing, a prayer check-in and a weekly recap.
  const briefing = k.flow(tg, BRIEFING)
  k.flow(tg, SHOLAT)
  k.flowRun(briefing, {
    status: "done",
    triggerId: "main",
    startedAt: now - 4 * H,
    scheduledAt: now - 4 * H,
    taint: ["steps.weather"],
    trigger: { kind: "schedule", label: "Briefing", time: "06.00" },
    steps: [
      { stepId: "prayer", kind: "tool", status: "ok", output: { ok: true, data: { times: { fajr: "04:39", dhuhr: "12:01" } } }, ms: 820 },
      { stepId: "weather", kind: "tool", status: "ok", tainted: true, output: { ok: true, data: { current_condition: [{ temp_C: "27" }] } }, ms: 1450 },
      { stepId: "reminders", kind: "tool", status: "ok", output: { ok: true, data: { reminders: [{ text: "Bayar listrik" }] } }, ms: 12 },
      { stepId: "today", kind: "set", status: "ok", output: { count: 1 } },
      { stepId: "send", kind: "message", status: "ok", output: { text: "Selamat pagi! Rabu 11 Nov\n\nSholat: Subuh 04:39, Zuhur 12:01" } },
    ],
  })
  const recap = k.flow(tg, RECAP, { versions: [{ definition: { ...RECAP, description: "Rekap mingguan dengan semangat" }, note: "tambah deskripsi" }] })
  k.flowRun(recap, { status: "failed", triggerId: "weekly", startedAt: now - 3 * D, error: "Step recap: Tool flows__recap isn't available (connector off or tool removed).", steps: [{ stepId: "recap", kind: "tool", status: "error", error: "Tool flows__recap isn't available (connector off or tool removed)." }] })

  const rem = (text: string, inHours: number, rrule: string | null = null, status: "pending" | "done" = "pending") => k.reminder({ chatId: tg.id, text, rrule, tz: "Asia/Makassar", fireAt: now + inHours * H, status })
  rem("Bayar listrik", 15)
  rem("Kirim laporan mingguan ke tim Vorneo, lengkap dengan rekap deal yang masuk, survei yang sudah dijadwalkan, dan catatan follow up untuk setiap agen 📊", 40)
  rem("Standup", 20, "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR")
  rem("Old one", -30, null, "done")

  // A reply with the Markdown the model actually sends (bold, lists, code, a link).
  k.message(web, "user", "rekap minggu ini dong", now - 30 * MIN)
  k.message(
    web,
    "assistant",
    "**Rekap minggu ini** (W46):\n\n1. *3 deal* masuk di Vorneo, 1 survei dijadwalkan\n2. Pengingat `bayar listrik` besok jam 09.00\n3. Detail di https://vorneo.id/deals?week=2026-W46\n\n- Telegram: on\n- WhatsApp: **error 440**, perlu pairing ulang\n\n```\npalui connectors on whatsapp\n```\n\nLihat [dashboard Palui](https://palui.zakiego.com/app/connectors) untuk detail.",
    now - 29 * MIN,
  )

  const titles = ["Keluarga besar 👨‍👩‍👧‍👦 Banjarmasin", "مجموعة العائلة", "東京出張チーム", "A", "Vorneo agents: Banjarbaru, Martapura, Pelaihari, Tanah Laut, Barito Kuala and Hulu Sungai", "🔥🔥🔥", "supercalifragilisticexpialidocious_channel_without_spaces_0123456789"]
  for (let i = 0; i < 18; i++) {
    const c = k.chat(i % 2 ? "telegram" : "whatsapp", `demo-${i}`, titles[i % titles.length]!)
    k.message(c, "user", i % 3 ? "ok sip 👍" : "Tolong cek lagi angka penjualan bulan ini dibandingkan bulan lalu, terutama untuk unit rumah tipe 36 di Banjarbaru", now - (i + 3) * 3 * H)
  }

  k.run({ chatId: web.id, trigger: "message", model: "claude-opus-4-1-20250805-thinking-extended-context", tokensIn: 98_765_432, tokensOut: 1_234_567, status: "ok", startedAt: now - 5 * MIN, ms: 3_723_000 })
  for (let i = 0; i < 24; i++) rem(i % 5 ? `Follow up deal #${1000 + i}` : `Telepon pak RT soal iuran keamanan dan kebersihan bulan ${i + 1} 🏠`, 50 + i * 7, i % 6 ? null : "FREQ=MONTHLY;BYMONTHDAY=1")
  for (let i = 0; i < 40; i++) {
    k.event({
      at: now - (i + 10) * 15 * MIN,
      connectorId: i % 3 ? "mcp-vorneo" : "cliproxy",
      kind: i % 4 ? "tool" : "status",
      level: i % 9 ? "info" : "warn",
      message: i % 4 ? `mcp-vorneo__list_deals_for_the_current_week_grouped_by_stage_and_agent ok in ${100 + i * 13} ms` : "status change",
    })
  }
  for (let i = 0; i < 6; i++) {
    k.event({
      at: now - i * 10 * MIN,
      connectorId: i % 2 ? "telegram" : "whatsapp",
      kind: "status",
      level: i === 1 ? "error" : i === 4 ? "warn" : "info",
      message: i === 1 ? "error: 401 Unauthorized from api.telegram.org (the bot token was revoked in @BotFather)" : `status change ${i}`,
    })
  }

  k.attachment({ chatId: wa.id, title: "Tiket pesawat BDJ-CGK 12 Des", description: "Lion Air JT-581, berangkat 12 Des 2026 06.10 WITA, kode booking XKQ9PL.", kind: "pdf", filename: "e-ticket-XKQ9PL.pdf", size: 184_320, createdAt: now - 3 * H })
  k.attachment({ chatId: tg.id, title: "KTP Zaki", description: "Foto KTP, berlaku seumur hidup.", kind: "jpeg", size: 412_000, createdAt: now - 26 * H })
  k.attachment({ chatId: tg.id, title: "Struk servis motor", description: "Bengkel resmi Banjarbaru, ganti oli dan kampas rem, Rp 385.000.", kind: "pdf", filename: "struk_servis_2026-11-01.pdf", size: 96_000, createdAt: now - 9 * D })
}
