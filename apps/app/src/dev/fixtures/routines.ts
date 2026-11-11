// Dev only: routines for the demo and worst fixtures. Every config goes through
// the routines service, so the page shows what the agent would have created.
import { localToUtc, utcToLocal } from "@palui/core/reminders/time"
import type { RoutineOccurrenceStatus } from "@palui/db"
import { D, H, MIN, type Kit } from "./kit"

const TZ = "Asia/Makassar"

const PRAYERS = [
  { key: "times.fajr", label: "Subuh", time: "04:36" },
  { key: "times.dhuhr", label: "Dzuhur", time: "12:02" },
  { key: "times.asr", label: "Ashar", time: "15:27" },
  { key: "times.maghrib", label: "Maghrib", time: "18:13" },
  { key: "times.isha", label: "Isya", time: "19:25" },
]

// The live "Sholat 5 waktu" routine, as the agent made it.
const SHOLAT = {
  name: "Sholat 5 waktu",
  tz: TZ,
  source: {
    kind: "tool" as const,
    connectorId: "mcp-jadwal-sholat",
    tool: "get_prayer_times",
    args: { city: "banjarmasin", date: "{{date}}" },
    refreshAt: "00:05",
    items: PRAYERS.map((p) => ({ field: p.key, label: p.label })),
  },
  action: {
    kind: "checkin" as const,
    text: "🕌 Udah sholat {{label}}? ({{time}})",
    answers: [
      { id: "done", label: "Sudah", words: ["udah", "alhamdulillah"], closes: true },
      { id: "not_yet", label: "Belum", closes: false },
    ],
    followUp: { everyMin: 15, max: 4, text: "Pengingat ke-{{n}}: sholat {{label}} jangan lupa ya." },
    deadline: { kind: "next" as const },
    missedText: "Sholat {{label}} ({{time}}) kelewat, dicatat ya.",
  },
  recap: { kind: "weekly" as const, day: "SU" as const, time: "20:00" },
}

// Local wall time `daysAgo` days back, in UTC ms.
function localAt(now: number, daysAgo: number, time: string): number {
  const date = utcToLocal(now - daysAgo * D, TZ).slice(0, 10)
  return localToUtc(`${date}T${time}`, TZ)
}

export function demoRoutines(k: Kit, chats: { tg: string; wa: string; web: string }) {
  const { now } = k
  const sholat = k.routine(chats.tg, SHOLAT, { state: k.toolState(TZ, PRAYERS), createdAt: now - 9 * D })
  // A week of log: mostly done, a missed Subuh now and then, the latest one still open.
  const pattern: RoutineOccurrenceStatus[] = ["done", "done", "missed", "done", "done", "done", "done"]
  for (let day = 0; day < 8; day++) {
    PRAYERS.forEach((p, i) => {
      const at = localAt(now, day, p.time)
      if (at > now) return
      const status = i === 0 ? pattern[day % 7]! : day === 3 && i === 2 ? "missed" : "done"
      const next = PRAYERS[i + 1] ? localAt(now, day, PRAYERS[i + 1]!.time) : localAt(now, day - 1, PRAYERS[0]!.time)
      if (next > now) {
        k.routineOccurrence(sholat, chats.tg, { label: p.label, at, status: "open", followUps: 1, nextFollowUpAt: now + 9 * MIN, deadlineAt: next, answer: "not_yet", note: "bentar lagi, masih di jalan" })
        return
      }
      k.routineOccurrence(sholat, chats.tg, { label: p.label, at, status, answer: status === "done" ? "done" : undefined, note: day === 1 && i === 3 ? "alhamdulillah, berjamaah di masjid" : undefined, followUps: status === "missed" ? 4 : i % 3 === 0 ? 1 : 0, deadlineAt: next })
    })
  }

  const obat = k.routine(
    chats.wa,
    {
      name: "Minum obat",
      tz: TZ,
      source: { kind: "times", times: [{ time: "08:00", label: "pagi" }, { time: "20:00", label: "malam" }] },
      action: {
        kind: "checkin",
        text: "💊 Obat {{label}} udah diminum? Jawab: {{answers}}",
        answers: [
          { id: "taken", label: "Udah minum", words: ["udah"], closes: true },
          { id: "skip", label: "Skip", closes: true },
          { id: "later", label: "Nanti", closes: false },
        ],
        followUp: { everyMin: 15, max: 2 },
        deadline: { kind: "after", minutes: 120 },
      },
      recap: { kind: "daily", time: "21:30" },
    },
    { paused: localToUtc(`${utcToLocal(now + D, TZ).slice(0, 10)}T07:00`, TZ), createdAt: now - 20 * D },
  )
  for (let day = 1; day < 6; day++) {
    k.routineOccurrence(obat, chats.wa, { label: "pagi", at: localAt(now, day, "08:00"), status: "done", answer: day === 4 ? "skip" : "taken" })
    k.routineOccurrence(obat, chats.wa, { label: "malam", at: localAt(now, day, "20:00"), status: day === 2 ? "missed" : "done", answer: day === 2 ? undefined : "taken" })
  }

  const review = k.routine(chats.web, {
    name: "Weekly review",
    tz: TZ,
    source: { kind: "rrule", rrule: "FREQ=WEEKLY;BYDAY=FR", start: "2026-10-02T16:00", label: "Review" },
    action: { kind: "prompt", prompt: "Rangkum minggu ini dari catatan dan pengingat yang selesai, lalu tanya apa prioritas minggu depan." },
  })
  k.routineOccurrence(review, chats.web, { label: "Review", at: now - 5 * D, status: "sent" })
  k.routineOccurrence(review, chats.web, { label: "Review", at: now - 12 * D, status: "failed" })

  k.routine(chats.tg, {
    name: "Minum air",
    tz: TZ,
    days: ["MO", "TU", "WE", "TH", "FR"],
    source: { kind: "times", times: ["09:00", "11:00", "14:00", "16:00"].map((time) => ({ time })) },
    action: { kind: "message", text: "💧 Minum air dulu, {{time}}." },
  })
}

export function worstRoutines(k: Kit, chats: { family: string; vietnamese: string; gone: string }) {
  const { now } = k
  // A tool source whose connector was removed, a long failing error, and no cached data.
  k.routine(
    chats.family,
    {
      ...SHOLAT,
      name: "Sholat lima waktu, cek tiap waktu sampai waktu berikutnya, rekap tiap minggu ok",
      source: { ...SHOLAT.source, connectorId: "mcp-jadwal-sholat-lama-yang-sudah-dihapus-dari-connectors", tool: "get_prayer_times_for_city_and_date_with_method" },
    },
    { lastError: "2026-11-12: fetch failed: getaddrinfo ENOTFOUND sholat.zakiego.com | 2026-11-13: Connector mcp-jadwal-sholat-lama-yang-sudah-dihapus-dari-connectors isn't running. Turn it on in Connectors." },
  )
  // Every answer option at its limit, and a label that wraps.
  const habit = k.routine(chats.vietnamese, {
    name: "Đặng Thị Ngọc Hân: tập thể dục buổi sáng 🏃‍♀️",
    tz: "Asia/Ho_Chi_Minh",
    source: { kind: "times", times: Array.from({ length: 24 }, (_, h) => ({ time: `${String(h).padStart(2, "0")}:30`, label: `Giờ thứ ${h + 1} trong ngày` })) },
    action: {
      kind: "checkin",
      text: "Đã tập chưa? {{answers}}",
      answers: Array.from({ length: 6 }, (_, i) => ({ id: `answer_${i}`, label: `Lựa chọn số ${i + 1} khá dài`, closes: i < 3 })),
      followUp: { everyMin: 5, max: 20 },
      deadline: { kind: "after", minutes: 2880 },
    },
    recap: { kind: "daily", time: "23:59" },
  })
  for (let i = 0; i < 300; i++) {
    const status: RoutineOccurrenceStatus = (["done", "missed", "skipped", "done", "open"] as const)[i % 5]!
    k.routineOccurrence(habit, chats.vietnamese, { label: `Giờ thứ ${(i % 24) + 1} trong ngày`, at: now - i * H, status, answer: status === "done" ? `answer_${i % 3}` : undefined, note: i === 0 ? "x".repeat(200) : undefined, followUps: i % 21, nextFollowUpAt: status === "open" ? now + 5 * MIN : null })
  }
  // A chat that no longer exists, paused open-ended, and a disabled connector.
  k.routine(
    chats.gone,
    { name: "J", tz: TZ, source: { kind: "tool", connectorId: "mcp-keuangan", tool: "list_bills", list: { path: "data.items", at: "due_date", label: "name" } }, action: { kind: "message", text: "Tagihan {{label}} jatuh tempo {{date}}." } },
    { paused: true },
  )
}
