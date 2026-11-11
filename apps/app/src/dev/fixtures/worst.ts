// Dev only: the realistic worst case for every value the dashboard renders.
// Each string is either something a real person or channel produces (a long
// hyphenated name, a JID, a pasted URL, a stack trace) or sits at the limit the
// dashboard's own validation accepts. Nothing here is random filler: if a row
// looks broken, a real owner can get there too.
//
// The different failures are spread across the first rows of each list, the
// way real data mixes them, instead of stacked into one row.
import { DEFAULT_PERSONA } from "@palui/core/settings"
import { D, H, MIN, type Kit } from "./kit"

// Limits the dashboard enforces (the zod schemas in src/lib/fns). A value that
// claims to be "at the limit" is checked against it, so the fixture can't
// drift past what the forms accept.
const LIMITS = { attachmentTitle: 200, attachmentDescription: 1000, attachmentFilename: 200, connectorLabel: 80, connectorId: 63, jobName: 80, rrule: 300, reminderText: 500, hookName: 60, model: 120, persona: 8000, webMessage: 8000, toolName: 64, approvalArgs: 300, toolResult: 500, statusDetail: 300, jobResult: 500, instructionTitle: 80, instructionBody: 1000, preferenceKey: 40, preferenceValue: 300 }

function within(value: string, limit: number): string {
  if (value.length > limit) throw new Error(`fixture value is ${value.length} characters, over the limit of ${limit}: ${value.slice(0, 40)}`)
  return value
}

// Repeats a paragraph up to exactly `limit` characters.
function fill(paragraph: string, limit: number): string {
  return paragraph.repeat(Math.ceil(limit / paragraph.length)).slice(0, limit)
}

const LONG_URL = "https://example.com/workspaces/northwind-industries-holdings/projects/q3-launch/docs/9f8e7d6c5b4a?tab=comments&filter=unresolved&sort=newest"
const EMAIL = "bartholomew.fitzgerald@northwind-industries-holdings.example.com"
// The start of a pasted base64 screenshot: one long string with no place to wrap.
const PASTED_BLOB = "iVBORw0KGgoAAAANSUhEUgAAAoAAAAHgCAYAAAA10dzkAAAACXBIWXMAAAsTAAALEwEAmpwYAAAAB3RJTUUH6AsLBAUNq0p8bwAAIABJREFUeJzs3XmYXFWd//H3ube6es2+dxKykLCEsIQl7CCLgCIqKuKCOm7jMuo4zjg6zvxmxnFmdHTUcR03VFxQEVBAZBVkX8KSQBISsu9Jd5LudHe6q+qe3x9V3el0upPuTnVXdefzep5+uurWrVun7r1V93vP8j0G"
const PARAGRAPH = "Tolong cek lagi angka penjualan bulan ini dibandingkan bulan lalu, terutama untuk unit rumah tipe 36 di Banjarbaru dan Martapura. Kalau ada selisih lebih dari lima persen, catat penyebabnya dan kirim ringkasannya ke grup sebelum rapat Senin pagi. "
const STACK = [
  "TypeError: Cannot read properties of undefined (reading 'accessToken')",
  "    at refreshGoogleToken (file:///app/packages/connectors/src/gcal/runtime.ts:142:31)",
  "    at async Object.start (file:///app/packages/connectors/src/gcal/runtime.ts:61:5)",
  "    at async Supervisor.startConnector (file:///app/apps/runtime/src/supervisor.ts:233:9)",
  "    at async Supervisor.reconcile (file:///app/apps/runtime/src/supervisor.ts:188:7)",
  "    at async Timeout._onTimeout (file:///app/apps/runtime/src/main.ts:97:5)",
].join("\n")
const RRULE_LONG = `FREQ=MINUTELY;INTERVAL=5;BYHOUR=${Array.from({ length: 24 }, (_, i) => i).join(",")};BYMINUTE=${Array.from({ length: 12 }, (_, i) => i * 5).join(",")};BYDAY=MO,TU,WE,TH,FR,SA,SU;BYMONTH=${Array.from({ length: 12 }, (_, i) => i + 1).join(",")};BYMONTHDAY=${Array.from({ length: 31 }, (_, i) => i + 1).join(",")}`
const MODEL_LONG = "accounts/fireworks/models/llama-v3p3-70b-instruct-turbo-preview-0925-extended-context-128k-northwind-finetune-2026-11"
const TZ_LONG = "America/North_Dakota/New_Salem"
const TOOL_LONG = "mcp-northwind-kb__search_internal_knowledge_base_by_department_x"

const MARKDOWN_STRESS = [
  "# Rekap Q3 (heading yang tidak didukung)",
  "",
  "| Kabupaten | Deal masuk | Survei | Closing | Catatan |",
  "| --- | --- | --- | --- | --- |",
  "| Banjarbaru | 1,284 | 97 | 12 | menunggu KPR |",
  "| Hulu Sungai Selatan | 0 | 0 | 0 | belum ada agen |",
  "",
  `Gambar yang kamu tempel tadi: \`${PASTED_BLOB}\``,
  "",
  "1. **Langkah pertama** yang cukup panjang sampai harus turun ke baris kedua bahkan di layar lebar, supaya kelihatan apakah nomor dan teksnya tetap sejajar",
  "   - sub-poin a",
  "   - sub-poin b dengan `kode inline`",
  "2. *Langkah kedua*",
  "",
  "```",
  `curl -sS -X POST "${LONG_URL}" -H "authorization: Bearer $NORTHWIND_TOKEN" -H "content-type: application/json" --data '{"stage":"masuk","week":"2026-W46","agents":["banjarbaru","martapura","pelaihari"]}'`,
  "```",
  "",
  `Selengkapnya di ${LONG_URL} atau [buka dokumen Q3 Board Deck — FINAL (revised) v12 [approved by legal].pdf](${LONG_URL}).`,
  "",
  "Teks mentah yang harus tampil apa adanya: <script>alert(1)</script> &amp; <b>tebal</b> ![gambar](https://example.com/x.png)",
].join("\n")

export function worst(k: Kit) {
  const { now } = k

  // --- settings: translated chrome, every list at its maximum ------------------
  k.setting("uiLanguage", "id")
  k.setting("language", "id")
  k.setting("timezone", TZ_LONG)
  k.setting("persona", fill(`${DEFAULT_PERSONA}\nSelalu sebut sumber data, tanggal pengambilan, dan zona waktunya. Jangan pernah menghapus apa pun tanpa persetujuan.\n\n`, LIMITS.persona))
  k.setting("fallbackChannel", "none")
  k.setting("budgets", { maxSteps: 50, turnTimeoutMs: 600_000, maxTools: 128, dailyTokenCap: 100_000_000, maxRunsPerHour: 1000 })
  const sixSteps = [
    { connectorId: "cliproxy", model: within(MODEL_LONG, LIMITS.model), effort: "xhigh" as const },
    { connectorId: "ai-gateway", model: "claude-sonnet-4-6", effort: "none" as const },
    // A connector that was removed after the chain was saved.
    { connectorId: "openrouter", model: "anthropic/claude-opus-4.1:thinking" },
    { connectorId: "cliproxy", model: "gemini-2.5-flash-preview-with-a-long-model-id" },
    { connectorId: "ai-gateway", model: "o" },
    { connectorId: "cliproxy", model: "claude-haiku-4-5-20251001" },
  ]
  k.setting("modelRoles", { default: sixSteps, fast: [{ connectorId: "openrouter", model: "x" }], vision: sixSteps })

  // --- connectors: every status, the badges that stack, labels of every shape --
  k.connector("telegram", "telegram", { status: "on", secrets: { botToken: "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqqrr" } })
  k.connector("whatsapp", "whatsapp", {
    status: "error",
    detail: within(
      "Connection closed (440): another session took over this linked device (session 6281234567890123:47@s.whatsapp.net, ref 2@k8ZbQ1vE0mYxN3pLr7sTqUwVcXyAaBbCcDdEeFfGgHhIiJjKk==), so Palui paused instead of fighting it. Unlink the old device in WhatsApp > Linked devices, then pair again.",
      LIMITS.statusDetail,
    ),
  })
  k.connector("web", "web", { status: "on" })
  k.connector("cliproxy", "cliproxy", { status: "on", secrets: { apiKey: "sk-cliproxy-0123456789abcdef" } })
  // Enabled but paused: the owner wants it on and it isn't, so it needs attention.
  k.connector("ai-gateway", "ai-gateway", { status: "paused", enabled: true, detail: "Paused by the daily token cap (100,000,000 of 100,000,000 used)", secrets: { apiKey: "zkg_0123456789abcdef" } })
  k.connector("recall", "recall", { status: "connecting", secrets: { apiKey: "rcl_0123456789a91f" } })
  k.connector(within("mcp-northwind-industries-holdings-internal-knowledge-base-prod", LIMITS.connectorId), "mcp", {
    label: within("Northwind Industries Holdings internal knowledge base (production, read-only)", LIMITS.connectorLabel),
    status: "error",
    trust: "external",
    detail: `401 Unauthorized from ${LONG_URL}`,
    config: { url: LONG_URL },
  })
  k.connector("mcp-j", "mcp", { label: "J", status: "on", config: { url: "https://j.example.com/mcp" } })
  k.connector("mcp-files", "mcp", { label: "خادم الملفات المشتركة", status: "on", trust: "external", config: { url: "https://files.example.com/mcp" } })
  k.connector("mcp-fox", "mcp", { label: "🦊", status: "paused", config: { url: "https://fox.example.com/mcp" } })
  // A label with no break opportunity: someone pasted the host as the name.
  k.connector("mcp-host", "mcp", { label: within("mcp.internal.northwind-industries-holdings.example.com/v2/sse", LIMITS.connectorLabel), status: "on", config: { url: "https://mcp.internal.northwind-industries-holdings.example.com/v2/sse" } })
  k.connector("mcp-rnd", "mcp", { label: '<b>R&D</b> **beta** &amp; "quotes"', status: "on", config: { url: "https://rnd.example.com/mcp" } })
  k.connector("mcp-de", "mcp", { label: "Benachrichtigungseinstellungen", status: "connecting", config: { url: "https://de.example.com/mcp" } })
  k.connector("mcp-notion", "mcp", { label: "Notion", status: "on", config: { url: "https://notion.example.com/mcp" } })
  // Same label as the one above: only the id tells them apart.
  k.connector("mcp-notion-2", "mcp", { label: "Notion", status: "error", detail: "ECONNREFUSED 127.0.0.1:8931", config: { url: "http://127.0.0.1:8931/mcp" } })
  k.connector("mcp-vn", "mcp", { label: "Đặng Thị Ngọc Hân: lịch làm việc", status: "on", config: { url: "https://vn.example.com/mcp" } })
  k.connector("mcp-linear", "mcp", { label: "Linear", status: "on", trust: "external", config: { url: "https://mcp.linear.app/mcp" } })
  k.connector("mcp-vorneo", "mcp", { label: "Vorneo", status: "on", config: { url: "https://vorneo.id/api/mcp/" } })
  k.connector("mcp-keuangan", "mcp", { label: "Keuangan", status: "paused", config: { url: "https://keuangan.example.com/mcp" } })
  k.connector("mcp-todomap", "mcp", { label: "Todo Map", status: "on", config: { url: "https://todo.example.com/api/mcp" } })
  k.connector("gcal", "gcal", { status: "not_set_up" })
  k.connector("t3code", "t3code", {
    status: "error",
    detail: "T3 Code rejected Palui's token (it lasts 30 days). On the Mac, run t3 auth pairing create --ttl 10m and paste the new code in this connector.",
    secrets: { pairingCode: "0123456789abcdefghijklmnopqrstuv" },
    config: { url: "http://host.docker.internal:3773", baseRef: "release/2026-11-northwind-industries-holdings-hotfix", model: "claudeAgent/claude-opus-5-5" },
  })
  k.connector("reminders", "reminders", { status: "on" })
  k.connector("jobs", "jobs", { status: "on" })
  k.connector("chats", "chats", { status: "on" })
  k.connector("webfetch", "webfetch", { status: "on" })
  k.connector("attachments", "attachments", { status: "on" })
  k.connector("voice", "voice", { status: "not_set_up" })
  k.connector("hooks", "hooks", { status: "on" })
  k.connector("notes", "notes", { status: "on" })
  k.connector("remote-mcp", "remote-mcp", { status: "paused" })

  // --- owner notes: at the limits, a URL that never breaks, mixed scripts -------
  k.instruction({
    title: within("Jadwal sholat untuk kota default, atau koordinat kalau sedang di luar Indonesia", LIMITS.instructionTitle),
    body: fill("Untuk pertanyaan jadwal sholat, pakai tools mcp-jadwal-sholat. Kalau server itu mati, pakai https://sholat.zakiego.com/api/v1/jadwal?kota=banjarmasin&tanggal=2026-11-11&format=json&metode=kemenag lewat web fetch.\n\n", LIMITS.instructionBody),
    createdAt: k.now - 40 * D,
  })
  k.instruction({ title: "J", body: "Selalu jawab singkat.", createdAt: k.now - 2 * D })
  k.instruction({ title: "خادم الملفات: الملخص اليومي", body: "Ringkas file baru di server berkas setiap pagi.", enabled: false, createdAt: k.now - D })
  k.preference(within("default_city_when_travelling_outside_kal", LIMITS.preferenceKey), "Banjarmasin")
  k.preference("home_coordinates", within(fill("-3.3194374, 114.5907765; ", LIMITS.preferenceValue), LIMITS.preferenceValue))
  k.preference("x", "y")
  k.preference("nama_panggilan", "Đặng Thị Ngọc Hân 🌴")

  // --- chats: names of every shape, and every age a "last message" can have ----
  const web = k.webChat()
  const say = (chat: ReturnType<Kit["chat"]>, text: string, at: number, role: "user" | "assistant" = "user") => k.message(chat, role, text, at)

  const aleksandra = k.chat("whatsapp", "48600123456@s.whatsapp.net", "Aleksandra Wiśniewska-Kowalczyk")
  say(aleksandra, LONG_URL, now - 10_000)

  // A phone with the wrong clock: the newest message is from the future.
  const jo = k.chat("telegram", "1000000001", "Jo")
  say(jo, "ok", now + 2 * H)

  const family = k.chat("telegram", "-1001234567890", "Keluarga Besar Bani H. Abdurrahman Siddiq 👨‍👩‍👧‍👦 Banjarmasin, Martapura, Banjarbaru, Pelaihari (arisan, haul, dan info acara)", { kind: "group" })
  // Seven characters, then emoji: the list's 140-character preview cut lands in the middle of one.
  say(family, `Wkwkwk ${"😂".repeat(80)}`, now - 50_000)

  // No title yet: the list falls back to the raw JID.
  const noTitleGroup = k.chat("whatsapp", "120363041234567890123456@g.us", "", { kind: "group" })
  say(noTitleGroup, "**Rekap** minggu ini:\n\n1. *3 deal* masuk\n2. `bayar listrik` besok", now - 59 * MIN, "assistant")
  const noTitleDm = k.chat("whatsapp", "258431789012345678@lid", "")
  say(noTitleDm, "", now - 23 * H, "assistant")

  const single = k.chat("whatsapp", "6281200000001@s.whatsapp.net", "J")
  say(single, "Line one\nLine two\nLine three", now - 26 * H)
  const vietnamese = k.chat("telegram", "1000000002", "Đặng Thị Ngọc Hân")
  say(vietnamese, "Ừ, được rồi. ก็ที่นี่ผู้ใหญ่ชี้ กตัญญูรู้คุณ", now - 12 * D)
  const cjk = k.chat("whatsapp", "8613800138000@s.whatsapp.net", "王秀英")
  say(cjk, "明天上午九点在办公室开会，请带上第三季度的销售报告和所有客户的联系方式，谢谢。", now - 29 * D)
  const arabic = k.chat("whatsapp", "966500000000@s.whatsapp.net", "نور الهدى عبد الرحمن")
  say(arabic, "مرحبا، هل يمكنك تذكيري باجتماع الغد الساعة 9؟ (Q3 report)", now - 31 * D)
  const fox = k.chat("telegram", "1000000003", "🦊 Fox")
  say(fox, "🦊", now - 11 * 30 * D)
  const priya = k.chat("whatsapp", "919800000000@s.whatsapp.net", "👩🏽‍💻 Priya")
  say(priya, EMAIL, now - 3 * 365 * D)
  // A channel that sent no timestamp: the row stores zero.
  const sam = k.chat("whatsapp", "6281200000002@s.whatsapp.net", "  Sam   Lee ", { createdAt: 0 })
  say(sam, "p", 0)
  const christopher = k.chat("telegram", "1000000004", "Christopher Alexander Montgomery III")
  say(christopher, fill(PARAGRAPH, 600), now - 2 * D)
  for (const [i, channel] of (["whatsapp", "telegram", "whatsapp"] as const).entries()) {
    const budi = k.chat(channel, `budi-${i}`, "Budi")
    say(budi, "ok sip 👍", now - (3 + i) * D)
  }
  const script = k.chat("telegram", "1000000005", "<script>alert(1)</script>")
  say(script, "<img src=x onerror=alert(1)> &amp; **bold**", now - 6 * D)
  const twoLines = k.chat("whatsapp", "6281200000003@s.whatsapp.net", "Line one\nLine two")
  say(twoLines, "y", now - 7 * D)
  const handle = k.chat("telegram", "1000000006", "@northwind_industries_support_bot")
  say(handle, "IMG_20250914_183022_HDR_portrait_edited_edited.HEIC", now - 8 * D)
  const lower = k.chat("whatsapp", "6281200000004@s.whatsapp.net", "dana")
  say(lower, "Q3 Board Deck — FINAL (revised) v12 [approved by legal].pdf", now - 9 * D)
  const irish = k.chat("telegram", "1000000007", "Seán O'Brien-Ó Súilleabháin")
  say(irish, "9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f", now - 10 * D)
  const link = k.chat("telegram", "-1009876543210", "t.me/northwind_industries_holdings_announcements_indonesia", { kind: "group" })
  say(link, "first.last+billing-notifications@example.com", now - 13 * D)
  // Not on the WhatsApp allowlist, and a chat that has never had a message.
  k.chat("whatsapp", "6281200000005@s.whatsapp.net", "Konstantin Oberhauser-Wettstein", { allowed: false, createdAt: now - 14 * D })
  k.chat("telegram", "1000000008", "María José de la Cruz y Fernández", { createdAt: now - 15 * D })

  // --- the web transcript: every message shape in one conversation -------------
  let t = now - 3 * H
  const next = (step = MIN) => (t += step)

  const promptAt = next()
  const bigRun = k.run({ chatId: web.id, trigger: "message", model: within(MODEL_LONG, LIMITS.model), tokensIn: 98_765_432, tokensOut: 1_234_567, tokensCached: 87_654_321, steps: 50, status: "ok", startedAt: t, ms: 3 * D })
  // A one-letter prompt, linked to its run so the run page shows it.
  k.message(web, "user", "p", promptAt, { runId: bigRun })
  const toolStatuses = ["ok", "error", "denied", "pending"] as const
  const toolClasses = ["read", "write", "destructive"] as const
  for (let i = 0; i < 30; i++) {
    k.tool({
      runId: bigRun,
      connectorId: i % 2 ? "mcp-northwind-industries-holdings-internal-knowledge-base-prod" : "reminders",
      tool: i % 5 === 0 ? within(TOOL_LONG, LIMITS.toolName) : i % 5 === 1 ? "x__y" : "reminders__ingatkan",
      class: toolClasses[i % 3]!,
      // A pasted blob in the arguments: one string with no place to wrap.
      args: i === 0 ? { query: PASTED_BLOB, filters: { department: "Platform Infrastructure", url: LONG_URL }, limit: 1284 } : i === 2 ? null : { text: "bayar listrik", when: "besok 09:00" },
      resultPreview: i === 0 ? within(fill(`{"id":"doc_9f8e7d6c5b4a","title":"Q3 Board Deck — FINAL (revised) v12 [approved by legal].pdf","url":"${LONG_URL}"},`, LIMITS.toolResult), LIMITS.toolResult) : i % 4 === 3 ? null : "Besok, Rabu 11 Nov, 09.00 WITA",
      status: toolStatuses[i % 4]!,
      ms: i === 0 ? 3_723_000 : i === 1 ? 0 : i === 2 ? null : 85 + i * 400,
      createdAt: t + i,
    })
  }
  k.message(web, "assistant", MARKDOWN_STRESS, next(), { runId: bigRun })
  k.message(web, "user", `${LONG_URL} ${PASTED_BLOB}`, next())
  // A run that only called tools and said nothing.
  k.message(web, "assistant", "", next())
  k.message(web, "user", fill(PARAGRAPH, LIMITS.webMessage), next())
  // The model's reply is stored whole; only the channel send is split at 4,096.
  k.message(web, "assistant", fill(`${PARAGRAPH}\n\n`, 12_000), next())
  k.message(web, "user", "مرحبا، هل يمكنك تذكيري باجتماع الغد الساعة 9؟ (Q3 report, room B-12)", next())
  k.message(web, "assistant", "بالتأكيد. سأذكّرك غدًا الساعة 09:00 (Asia/Riyadh) باجتماع **Q3 report** في الغرفة B-12.", next())
  k.message(web, "user", '{"event":"deployment.succeeded","repository":"northwind-industries-holdings/platform-infrastructure","sha":"9f8e7d6c5b4a4c3d8e2f1a0b9c8d7e6f9f8e7d6c","url":"' + LONG_URL + '","actor":"' + EMAIL + '"}', next(), { senderId: "hook:github-actions-deploy" })
  k.message(web, "user", "🦊", next())
  k.message(web, "user", Array.from({ length: 40 }, (_, i) => `${i + 1}. beli ${["beras", "gula", "minyak goreng", "telur", "kopi"][i % 5]}`).join("\n"), next())
  k.message(web, "assistant", "Đặng Thị Ngọc Hân nhắn: ก็ที่นี่ผู้ใหญ่ชี้ กตัญญูรู้คุณ. Ừ, được rồi.", next())
  k.message(web, "user", "ini belum dibalas", next(), { status: "pending" })
  k.message(web, "user", "ini diabaikan", next(), { status: "ignored", ignoreReason: "not on the allowlist" })

  // Three approvals waiting at once, one of them already past its deadline.
  k.approval({ runId: bigRun, chatId: web.id, tool: within(TOOL_LONG, LIMITS.toolName), toolClass: "destructive", argsPreview: within(fill(`{"ids":["${PASTED_BLOB.slice(0, 120)}"],"confirm":true,"url":"${LONG_URL}"}`, LIMITS.approvalArgs), LIMITS.approvalArgs), expiresAt: now + 10 * MIN, createdAt: now - 2 * MIN })
  k.approval({ runId: bigRun, chatId: web.id, tool: "mcp-linear__delete_issue", toolClass: "write", argsPreview: '{"id":"PAL-123"}', expiresAt: now - 12 * D, createdAt: now - 12 * D - 10 * MIN })
  k.approval({ runId: bigRun, chatId: web.id, tool: "x__y", toolClass: "read", argsPreview: null, expiresAt: now + 45_000, createdAt: now - MIN })

  // --- jobs: listed oldest first, so the order here is the order on screen -----
  let jobOrder = 0
  const job = (values: Parameters<Kit["job"]>[0]) => k.job({ ...values, createdAt: now - D + jobOrder++ })

  const cappedJob = job({
    name: within("daily_sales_recap_vorneo_banjarbaru_martapura_pelaihari_tanah_laut_q4_2026_final", LIMITS.jobName),
    kind: "job",
    rrule: within(RRULE_LONG, LIMITS.rrule),
    tz: TZ_LONG,
    prompt: "Recap.",
    chatId: family.id,
    status: "running",
    nextRunAt: now + 45_000,
    lastRunAt: now - 5 * MIN,
    lastStatus: "error",
    lastResult: within(fill(`${STACK.split("\n")[0]} while fetching ${LONG_URL} `, LIMITS.jobResult - 1) + "…", LIMITS.jobResult),
    dailyTokenCap: 1000,
  })
  job({ name: "J", kind: "heartbeat", rrule: "FREQ=DAILY", tz: "UTC", prompt: "x", chatId: jo.id, enabled: false, dailyTokenCap: 5_000_000 })
  const briefing = job({ name: within("Rekap penjualan harian Vorneo untuk Banjarbaru, Martapura, Pelaihari dan sekitar", LIMITS.jobName), kind: "briefing", rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", tz: "Asia/Makassar", prompt: "Brief me.", chatId: aleksandra.id, nextRunAt: now + 10 * H, lastRunAt: now - 14 * H, lastStatus: "ok", lastResult: "ok", dailyTokenCap: 5_000_000 })
  // Ran, but the runtime never wrote a status.
  job({ name: "تذكير يومي بتقرير المبيعات", rrule: "FREQ=WEEKLY;BYDAY=MO", tz: "Asia/Riyadh", prompt: "x", chatId: arabic.id, nextRunAt: now + 3 * D, lastRunAt: now - 4 * D, lastStatus: null, lastResult: null })
  // A status the dashboard has no tone for.
  job({ name: "Budget watcher", rrule: "FREQ=HOURLY", tz: "Asia/Makassar", prompt: "x", chatId: web.id, nextRunAt: now + 20 * MIN, lastRunAt: now - 40 * MIN, lastStatus: "budget_exceeded_daily_token_cap_reached", lastResult: "Stopped: the daily cap of 100,000,000 tokens was reached at 23:59." })
  // Its chat was removed after the job was made.
  job({ name: "Orphaned job", rrule: "FREQ=DAILY", tz: "Asia/Makassar", prompt: "x", chatId: "chat_0mhremoved0000000000", nextRunAt: now + 2 * H, lastRunAt: now - 22 * H, lastStatus: "ok", lastResult: "**Markdown** in a result\nwith a second line and a `code` span." })
  // The runtime was down for 12 days: enabled, and long overdue.
  job({ name: "Overdue", rrule: "FREQ=DAILY", tz: "Asia/Makassar", prompt: "x", chatId: web.id, nextRunAt: now - 12 * D, lastRunAt: now - 13 * D, lastStatus: "aborted", lastResult: "" })
  job({ name: "Tax filing reminder", rrule: "FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=31;BYHOUR=23;BYMINUTE=30", tz: "America/Los_Angeles", prompt: "x", chatId: web.id, nextRunAt: Date.UTC(2030, 0, 1, 7, 30), lastRunAt: Date.UTC(2023, 0, 1, 7, 30), lastStatus: "ok", lastResult: "Done." })
  job({ name: "🔥🔥🔥", rrule: "FREQ=DAILY", tz: "Asia/Makassar", prompt: "x", chatId: fox.id, nextRunAt: now + H })
  job({ name: "Daily briefing", kind: "briefing", rrule: "FREQ=DAILY;BYHOUR=7", tz: "Asia/Makassar", prompt: "x", chatId: web.id, nextRunAt: now + 10 * H })
  job({ name: "Daily briefing", kind: "briefing", rrule: "FREQ=DAILY;BYHOUR=7", tz: "Asia/Makassar", prompt: "x", chatId: web.id, nextRunAt: now + 10 * H })
  job({ name: "<script>alert(1)</script>", rrule: "FREQ=DAILY", tz: "Asia/Makassar", prompt: "x", chatId: script.id, nextRunAt: now + H })
  job({ name: "Đặng Thị Ngọc Hân nhắc việc", rrule: "FREQ=DAILY", tz: "Asia/Ho_Chi_Minh", prompt: "x", chatId: vietnamese.id, nextRunAt: now + H, lastRunAt: 0, lastStatus: "ok", lastResult: "ok" })
  job({ name: "Benachrichtigungseinstellungen-Synchronisierung", kind: "heartbeat", rrule: "FREQ=MINUTELY;INTERVAL=30", tz: "Europe/Berlin", prompt: "x", chatId: web.id, nextRunAt: now + 30 * MIN, lastRunAt: now - 30 * MIN, lastStatus: "running" })

  // --- runs: every status and trigger, the extremes of every number ------------
  // 142% of a 1,000 token cap, today.
  k.run({ chatId: family.id, jobId: cappedJob, trigger: "schedule", model: "claude-sonnet-4-6", tokensIn: 1300, tokensOut: 120, status: "budget", error: "Daily token cap reached for this job (1,420 of 1,000).", startedAt: now - 4 * MIN, ms: 59_999 })
  k.run({ chatId: aleksandra.id, trigger: "message", model: null, status: "running", startedAt: now - 5000, ms: null })
  k.run({ chatId: web.id, trigger: "approval", model: "claude-sonnet-4-6", tokensIn: 12, tokensOut: 1, tainted: true, status: "awaiting_approval", startedAt: now - 2 * MIN, ms: 0 })
  k.run({ chatId: noTitleGroup.id, trigger: "message", model: within(MODEL_LONG, LIMITS.model), tokensIn: 1_284_000, tokensOut: 99_999, tainted: true, status: "error", error: `${STACK}\n    caused by: 502 Bad Gateway from ${LONG_URL}`, startedAt: now - 6 * MIN, ms: 999 })
  // No chat and no job: a webhook that never matched one.
  k.run({ trigger: "hook", model: "o", tokensIn: 0, tokensOut: 0, status: "aborted", error: "", startedAt: now - 7 * MIN, ms: 1000 })
  k.run({ chatId: arabic.id, trigger: "reminder", model: "claude-haiku-4-5-20251001", tokensIn: 999, tokensOut: 1, status: "ok", startedAt: now - 8 * MIN, ms: 9_999 })
  // The job and the chat were deleted later: only their ids are left.
  k.run({ jobId: "job_0mhremoved0000000000", trigger: "schedule", model: "claude-sonnet-4-6", tokensIn: 21_000, tokensOut: 1400, status: "ok", resultPreview: within(fill(PARAGRAPH, LIMITS.jobResult), LIMITS.jobResult), startedAt: now - 9 * MIN, ms: 15_000 })
  k.run({ chatId: "chat_0mhremoved0000000000", trigger: "message", model: "claude-sonnet-4-6", tokensIn: 4000, tokensOut: 300, status: "ok", startedAt: now - 10 * MIN, ms: 60_000 })
  k.run({ chatId: jo.id, jobId: briefing, trigger: "schedule", model: "claude-sonnet-4-6", tokensIn: 1, tokensOut: 0, status: "ok", startedAt: now + 2 * H, ms: 86_400_000 })
  k.run({ chatId: priya.id, trigger: "message", model: "gpt-4-0613", tokensIn: 8000, tokensOut: 2000, status: "ok", startedAt: now - 3 * 365 * D, ms: 4200 })
  // The last two weeks for the usage chart: one day a hundred times the others,
  // a few days with nothing, and more models than the breakdown shows.
  for (let day = 1; day < 14; day++) {
    if (day === 4 || day === 5 || day === 11) continue
    for (let i = 0; i < 3; i++) {
      k.run({
        chatId: [aleksandra.id, family.id, cjk.id, christopher.id][(day + i) % 4]!,
        trigger: "message",
        model: `model-${String((day * 3 + i) % 26).padStart(2, "0")}-preview`,
        tokensIn: day === 2 ? 40_000_000 : 3000 + day * 700 + i * 200,
        tokensOut: day === 2 ? 400_000 : 200 + i * 40,
        status: "ok",
        startedAt: now - day * D - i * H,
        ms: 2000 + i * 300,
      })
    }
  }

  // --- reminders ----------------------------------------------------------------
  const remind = (values: Parameters<Kit["reminder"]>[0]) => k.reminder(values)
  // Upcoming, soonest first. The first ones are already overdue.
  remind({ chatId: family.id, text: within(fill(`Kirim laporan mingguan ke ${EMAIL} lewat ${LONG_URL} lalu konfirmasi ke grup. `, LIMITS.reminderText), LIMITS.reminderText), tz: TZ_LONG, fireAt: now - 12 * D, status: "pending", smartPrompt: "Summarize the week first." })
  remind({ chatId: jo.id, text: "x", tz: "UTC", fireAt: now - 30_000, status: "firing" })
  remind({ chatId: "chat_0mhremoved0000000000", text: "Bayar listrik\nBayar air\nBayar internet", tz: "Asia/Makassar", fireAt: now + 40_000, status: "pending" })
  remind({ chatId: arabic.id, text: "تذكير: اجتماع الغد الساعة 9 (Q3 report)", tz: "Asia/Riyadh", fireAt: now + 59 * MIN, status: "pending" })
  remind({ chatId: noTitleGroup.id, text: LONG_URL, tz: "Asia/Makassar", fireAt: now + 5 * H, status: "pending" })
  for (let i = 0; i < 3; i++) remind({ chatId: [aleksandra.id, cjk.id, link.id][i]!, text: "Standup", tz: "Asia/Makassar", fireAt: now + 20 * H, status: "pending" })
  remind({ chatId: script.id, text: "<script>alert(1)</script> &amp; **bold**", tz: "Asia/Makassar", fireAt: now + 2 * D, status: "pending" })
  remind({ chatId: fox.id, text: "🦊", tz: "Asia/Makassar", fireAt: now + 3 * D, status: "pending", smartPrompt: "x" })
  remind({ chatId: vietnamese.id, text: "Đặng Thị Ngọc Hân: gọi lại cho khách", tz: "Asia/Ho_Chi_Minh", fireAt: now + 11 * 30 * D, status: "pending" })
  // New Year's Eve in Los Angeles is already January 1 in the owner's zone.
  remind({ chatId: christopher.id, text: "File taxes", tz: "America/Los_Angeles", fireAt: Date.UTC(2030, 0, 1, 7, 30), status: "pending" })
  // Recurring.
  remind({ chatId: family.id, text: within(fill(PARAGRAPH, LIMITS.reminderText), LIMITS.reminderText), rrule: within(RRULE_LONG, LIMITS.rrule), tz: TZ_LONG, fireAt: now + 4 * MIN, status: "pending", smartPrompt: "x" })
  remind({ chatId: jo.id, text: "y", rrule: "FREQ=DAILY", tz: "UTC", fireAt: now + 6 * H, status: "pending" })
  remind({ chatId: "chat_0mhremoved0000000000", text: "Standup", rrule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", tz: "Asia/Makassar", fireAt: now + 20 * H, status: "pending", lastFiredAt: now - 4 * H })
  remind({ chatId: arabic.id, text: "صلاة الجمعة", rrule: "FREQ=WEEKLY;BYDAY=FR;BYHOUR=11;BYMINUTE=45", tz: "Asia/Riyadh", fireAt: now + 2 * D, status: "firing" })
  remind({ chatId: christopher.id, text: "Renew the domain", rrule: "FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=31", tz: "America/Los_Angeles", fireAt: Date.UTC(2030, 0, 1, 7, 30), status: "pending" })
  // Done and cancelled, newest first.
  remind({ chatId: family.id, text: within(fill(PARAGRAPH, LIMITS.reminderText), LIMITS.reminderText), tz: TZ_LONG, fireAt: now - H, status: "done", lastFiredAt: now - H, updatedAt: now - 1000 })
  remind({ chatId: jo.id, text: "z", tz: "UTC", fireAt: now - 2 * H, status: "cancelled", updatedAt: now - 2000 })
  remind({ chatId: "chat_0mhremoved0000000000", text: "Standup", rrule: within(RRULE_LONG, LIMITS.rrule), tz: "Asia/Makassar", fireAt: now - 3 * D, status: "cancelled", updatedAt: now - 3000 })
  remind({ chatId: priya.id, text: "Old one", tz: "Asia/Makassar", fireAt: now - 3 * 365 * D, status: "done", lastFiredAt: now - 3 * 365 * D, updatedAt: now - 4000 })
  remind({ chatId: sam.id, text: "No timestamp", tz: "UTC", fireAt: 0, status: "done", updatedAt: now - 5000 })

  // --- events: written last, so these are the first rows of the Activity list ---
  const events: Parameters<Kit["event"]>[0][] = [
    { at: now - 3 * 365 * D, level: "info", connectorId: "telegram", message: "on" },
    { at: 0, level: "warn", connectorId: "whatsapp", message: "Clock was reset: this event has no timestamp" },
    { at: now - 20 * MIN, level: "info", connectorId: "mcp-files", message: "مرحبا: تم الاتصال بالخادم (connected in 85 ms)" },
    { at: now - 19 * MIN, level: "debug", connectorId: "cliproxy", message: "🦊" },
    { at: now - 18 * MIN, level: "info", connectorId: "mcp-rnd", message: "<script>alert(1)</script> &amp; **bold**" },
    // A connector that was removed last week still has rows here.
    { at: now - 17 * MIN, level: "warn", connectorId: "mcp-removed-last-week", kind: "remove", message: "Removed, credentials deleted" },
    { at: now - 16 * MIN, level: "info", connectorId: null, message: "" },
    { at: now - 15 * MIN, level: "error", connectorId: null, kind: "system", message: `Backup failed: ENOSPC: no space left on device, write '/data/backups/palui-predeploy-2026-11-11T04-05-13-123Z-9f8e7d6c5b4a4c3d8e2f1a0b9c8d7e6f.db.gz'` },
    { at: now - 14 * MIN, level: "info", connectorId: "hooks", kind: "hook", message: `{"event":"deployment.succeeded","repository":"northwind-industries-holdings/platform-infrastructure","sha":"9f8e7d6c5b4a4c3d8e2f1a0b9c8d7e6f9f8e7d6c","screenshot":"${PASTED_BLOB}"}` },
    { at: now + D, level: "info", connectorId: "jobs", kind: "job", message: "Scheduled ahead of time by a host with the wrong clock" },
    { at: now - 13 * MIN, level: "debug", connectorId: "x", message: "y" },
    { at: now - 12 * MIN, level: "warn", connectorId: "ai-gateway", kind: "status", message: "paused: Paused by the daily token cap (100,000,000 of 100,000,000 used)" },
    { at: now - 11 * MIN, level: "error", connectorId: "mcp-northwind-industries-holdings-internal-knowledge-base-prod", kind: "status", message: `error: ${fill(`${STACK}\n`, 2000)}` },
  ]
  for (const e of events) k.event(e)

  // --- settings lists -----------------------------------------------------------
  k.passkey({ name: null, createdAt: null })
  k.passkey({ name: "J", deviceType: "singleDevice", backedUp: false, createdAt: now - 3 * 365 * D })
  k.passkey({ name: "Aleksandra Wiśniewska-Kowalczyk's MacBook Pro 16-inch (Touch ID), synced through iCloud Keychain", createdAt: now - 20_000 })
  k.passkey({ name: "هاتف نور الهدى", createdAt: now - 12 * D })
  k.passkey({ name: "YubiKey-5C-NFC-FIPS-serial-0123456789-office-backup-key-do-not-lose", deviceType: "singleDevice", backedUp: false, createdAt: now + 2 * H })
  k.passkey({ name: "   ", createdAt: 0 })

  k.hook({ name: within("github-actions-deploy-notifications-northwind-industries-pro", LIMITS.hookName), chatId: family.id, mode: "summarize", lastAt: now - 20_000, slug: "9f8e7d6c5b4a4c3d" })
  k.hook({ name: "J", chatId: jo.id, lastAt: null })
  k.hook({ name: "تنبيهات النشر", chatId: arabic.id, lastAt: now - 3 * 365 * D })
  // Its chat is gone, and it has been switched off.
  k.hook({ name: "Sentry alerts for the Vorneo production project (payments)", chatId: "chat_0mhremoved0000000000", mode: "summarize", enabled: false, lastAt: 0 })
  k.hook({ name: "<b>Stripe</b> &amp; friends", chatId: noTitleGroup.id, lastAt: now + 2 * H })
  k.hook({ name: "Uptime", chatId: aleksandra.id, lastAt: now - 12 * D })

  // --- saved attachments: the longest title, description and file name the tools allow ---
  k.attachment({
    chatId: aleksandra.id,
    title: within(fill("Umowa najmu mieszkania Wiśniewska-Kowalczyk, aneks nr 3 z podpisami obu stron i pieczęcią notariusza ", LIMITS.attachmentTitle), LIMITS.attachmentTitle),
    description: within(fill(PARAGRAPH, LIMITS.attachmentDescription), LIMITS.attachmentDescription),
    kind: "pdf",
    filename: within(`${"umowa_najmu_aneks_nr_3_podpisana_0123456789abcdefghijklmnopqrstuvwxyz_".repeat(3).slice(0, LIMITS.attachmentFilename - 4)}.pdf`, LIMITS.attachmentFilename),
    size: 10 * 1024 * 1024,
    createdAt: now - 20_000,
  })
  k.attachment({ chatId: arabic.id, title: "بطاقة الهوية", description: "", kind: "jpeg", size: 1, createdAt: now - 3 * 365 * D })
  k.attachment({ chatId: "chat_0mhremoved0000000000", title: "J", kind: "jpeg", filename: null, size: 999_999, createdAt: now + 2 * H })
  k.attachment({ chatId: cjk.id, title: "身份证 王秀英 正反面", description: "身份证正反面照片，有效期至2036年。", kind: "jpeg", filename: "身份证_王秀英.jpg", size: 1536, createdAt: now - 12 * D })
}
