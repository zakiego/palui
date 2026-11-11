// Fixtures for flow tests (not imported by production code): the worked
// examples from RFC flows section 9, and a tool list they resolve against.
import type { ToolInfo } from "./validate"

export const EXAMPLE_TOOLS: ToolInfo[] = [
  { name: "mcp-jadwal-sholat__get_prayer_times", connectorId: "mcp-jadwal-sholat", tool: "get_prayer_times", class: "read", untrusted: true, connectorType: "mcp" },
  { name: "webfetch__ambil_halaman", connectorId: "webfetch", tool: "ambil_halaman", class: "read", untrusted: true, connectorType: "webfetch" },
  { name: "reminders__daftar_pengingat", connectorId: "reminders", tool: "daftar_pengingat", class: "read", untrusted: false, connectorType: "reminders" },
  { name: "reminders__ingatkan", connectorId: "reminders", tool: "ingatkan", class: "write", untrusted: false, connectorType: "reminders" },
  { name: "reminders__batal_pengingat", connectorId: "reminders", tool: "batal_pengingat", class: "destructive", untrusted: false, connectorType: "reminders" },
  { name: "mcp-github__get_commit_status", connectorId: "mcp-github", tool: "get_commit_status", class: "read", untrusted: true, connectorType: "mcp" },
  { name: "mcp-github__add_comment", connectorId: "mcp-github", tool: "add_comment", class: "write", untrusted: true, connectorType: "mcp", flowApproval: "every_run" },
  { name: "mcp-keuangan__list_bills", connectorId: "mcp-keuangan", tool: "list_bills", class: "read", untrusted: false, connectorType: "mcp" },
  { name: "mcp-keuangan__mark_paid", connectorId: "mcp-keuangan", tool: "mark_paid", class: "write", untrusted: false, connectorType: "mcp" },
  { name: "notes__simpan_catatan", connectorId: "notes", tool: "simpan_catatan", class: "write", untrusted: false, connectorType: "notes" },
  { name: "flows__ringkasan", connectorId: "flows", tool: "ringkasan", class: "read", untrusted: false, connectorType: "flows" },
]

// 9a. Morning briefing.
export const BRIEFING = {
  name: "Briefing pagi",
  triggers: [{ id: "main", kind: "schedule", start: "prayer", source: { kind: "times", times: [{ time: "06:00", label: "Briefing" }] } }],
  steps: [
    { id: "prayer", kind: "tool", tool: "mcp-jadwal-sholat__get_prayer_times", args: { city: "Banjarmasin", date: "{{now.date}}" }, onError: "continue" },
    { id: "weather", kind: "tool", tool: "webfetch__ambil_halaman", args: { url: "https://wttr.in/Banjarmasin?format=j1" }, parse: { field: "text", as: "json" }, onError: "continue" },
    { id: "reminders", kind: "tool", tool: "reminders__daftar_pengingat", args: { filter: "upcoming", limit: 20 } },
    { id: "today", kind: "set", values: { count: 'count(steps.reminders.data.reminders, "date", now.date)' } },
    {
      id: "send",
      kind: "message",
      text: 'Selamat pagi! {{now.date | date}}\n\nSholat: Subuh {{steps.prayer.data.times.fajr | default("-")}}, Zuhur {{steps.prayer.data.times.dhuhr | default("-")}}, Asar {{steps.prayer.data.times.asr | default("-")}}, Magrib {{steps.prayer.data.times.maghrib | default("-")}}, Isya {{steps.prayer.data.times.isha | default("-")}}\nCuaca: {{steps.weather.data.current_condition.0.temp_C | default("?")}}°C, {{steps.weather.data.current_condition.0.weatherDesc.0.value | default("nggak kebaca")}}\n\nPengingat hari ini ({{vars.count}}):\n{{steps.reminders.data.reminders | lines("text")}}',
    },
  ],
}

// 9b. GitHub PR merged, check CI, notify.
export const PR_MERGED = {
  name: "PR merged",
  triggers: [{ id: "gh", kind: "webhook", hookId: "hk_github_palui", start: "is_merge" }],
  steps: [
    { id: "is_merge", kind: "if", when: 'trigger.payload.action == "closed" and trigger.payload.pull_request.merged == true', then: "settle", else: null },
    { id: "settle", kind: "wait", minutes: 5 },
    {
      id: "ci",
      kind: "tool",
      tool: "mcp-github__get_commit_status",
      args: { owner: "zakiego", repo: "palui", ref: "{{trigger.payload.pull_request.merge_commit_sha}}" },
      retry: { max: 2, backoffSec: 60 },
      onError: "ci_unknown",
    },
    { id: "green", kind: "if", when: 'steps.ci.data.state == "success"', then: "ok", else: "not_ok" },
    { id: "ok", kind: "message", next: null, text: '✅ PR #{{trigger.payload.pull_request.number}} "{{trigger.payload.pull_request.title | truncate(80)}}" merged, CI hijau.' },
    { id: "not_ok", kind: "message", next: null, text: "⚠️ PR #{{trigger.payload.pull_request.number}} merged tapi CI {{steps.ci.data.state}}. Cek: {{trigger.payload.pull_request.html_url}}" },
    { id: "ci_unknown", kind: "message", text: "PR #{{trigger.payload.pull_request.number}} merged, tapi status CI nggak kebaca: {{steps.ci.error | truncate(120)}}" },
  ],
}

// 9c. Bill reminder.
export const BILLS = {
  name: "Tagihan",
  triggers: [{ id: "daily", kind: "schedule", start: "bills", source: { kind: "times", times: [{ time: "09:00", label: "Cek tagihan" }] } }],
  steps: [
    { id: "bills", kind: "tool", tool: "mcp-keuangan__list_bills", args: { status: "unpaid" } },
    { id: "per_bill", kind: "each", items: "steps.bills.data.items", where: "daysUntil(item.due) <= 3 and daysUntil(item.due) >= 0", key: "{{item.id}}:{{item.due}}", max: 20, body: "ask", next: null },
    {
      id: "ask",
      kind: "checkin",
      text: "Tagihan {{item.name | truncate(40)}} Rp{{item.amount}} jatuh tempo {{item.due | date}}. Udah bayar?",
      answers: [
        { id: "paid", label: "Udah", words: ["udah", "sudah", "lunas"], closes: true },
        { id: "later", label: "Belum", words: ["belum", "nanti"], closes: false },
      ],
      followUp: { everyMin: 240, max: 3, text: "🔁 {{item.name | truncate(40)}} belum dibayar ya? Jatuh tempo {{item.due | date}}." },
      deadline: { kind: "after", minutes: 4320 },
      missedText: "Tagihan {{item.name | truncate(40)}} lewat tanpa konfirmasi.",
      on: { paid: "mark" },
      next: null,
    },
    { id: "mark", kind: "set", scope: "flow", values: { last_paid: "{{item.name}} {{now.date}}" } },
  ],
}

// 9d. Weekly prayer recap with encouragement.
export const RECAP = {
  name: "Rekap sholat",
  triggers: [{ id: "weekly", kind: "schedule", start: "recap", source: { kind: "rrule", rrule: "FREQ=WEEKLY;BYDAY=SU", start: "2026-11-15T20:00" } }],
  steps: [
    { id: "recap", kind: "tool", tool: "flows__ringkasan", args: { flow: "Sholat 5 waktu", days: 7 } },
    { id: "any_missed", kind: "if", when: "steps.recap.data.missed > 0 or number(steps.recap.data.byAnswer.not_yet) > 0", then: "cheer", else: "clean" },
    {
      id: "cheer",
      kind: "prompt",
      prompt:
        "Tulis pesan pendek (maks 4 kalimat), hangat dan nggak menggurui, buat Zaki tentang rekap sholatnya minggu ini. Sebut angkanya sekali, terus kasih semangat yang konkret buat minggu depan. Bahasa santai.",
      input: { recap: "{{steps.recap.data}}" },
      tools: [],
      model: "fast",
      onError: "plain",
    },
    { id: "send_cheer", kind: "message", text: "{{steps.cheer.text}}", next: null },
    { id: "plain", kind: "message", text: "{{steps.recap.data.text}}", next: null },
    { id: "clean", kind: "message", text: "Minggu ini {{steps.recap.data.total}} sholat, semuanya kecatat sudah. Mantap, pertahankan! 🤲" },
  ],
}

// 9e. "Sholat 5 waktu" as a flow (the migration output, with the recap guard).
export const SHOLAT = {
  name: "Sholat 5 waktu",
  tz: "Asia/Makassar",
  triggers: [
    {
      id: "main",
      kind: "schedule",
      start: "ask",
      lateGraceMin: 60,
      source: {
        kind: "tool",
        connectorId: "mcp-jadwal-sholat",
        tool: "get_prayer_times",
        args: { city: "Banjarmasin", date: "{{date}}" },
        refreshAt: "00:05",
        items: [
          { field: "times.fajr", label: "Subuh", offsetMin: 0 },
          { field: "times.dhuhr", label: "Zuhur", offsetMin: 0 },
          { field: "times.asr", label: "Asar", offsetMin: 0 },
          { field: "times.maghrib", label: "Magrib", offsetMin: 0 },
          { field: "times.isha", label: "Isya", offsetMin: 0 },
        ],
      },
    },
    { id: "recap", kind: "schedule", start: "recap_read", source: { kind: "rrule", rrule: "FREQ=WEEKLY;BYDAY=SU", start: "2026-11-15T20:00" } },
  ],
  steps: [
    {
      id: "ask",
      kind: "checkin",
      text: "Udah sholat {{trigger.label}} ({{trigger.time}})?",
      answers: [
        { id: "done", label: "Sudah", words: ["udah", "udh", "dah", "done", "selesai", "beres"], closes: true },
        { id: "not_yet", label: "Belum", words: ["blm", "not yet", "nanti"], closes: false },
      ],
      followUp: { everyMin: 15, max: 4 },
      deadline: { kind: "next" },
      next: null,
    },
    { id: "recap_read", kind: "tool", tool: "flows__ringkasan", args: { flow: "self", days: 7, trigger: "main" } },
    { id: "recap_any", kind: "if", when: "steps.recap_read.data.total > 0", then: "recap_send", else: null },
    { id: "recap_send", kind: "message", text: "{{steps.recap_read.data.text}}" },
  ],
}

export const EXAMPLES = { BRIEFING, PR_MERGED, BILLS, RECAP, SHOLAT }
