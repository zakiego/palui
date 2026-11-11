// Job presets (PLAN 7). Each returns a CreateJobInput for createJob(); the
// dashboard offers them as one-click starters.
import { utcToLocal } from "../reminders/time"
import type { CreateJobInput } from "./service"

// The token the heartbeat answers when nothing matters; the engine then sends nothing.
export const SILENT_TOKEN = "NOTHING"

// Every 30 minutes from 08:00 to 22:00 inclusive: the 30 half-hours from 08:00 to
// 22:30 minus the last one (BYSETPOS keeps positions 1 to 29).
export const HEARTBEAT_RRULE = `FREQ=DAILY;BYHOUR=${Array.from({ length: 15 }, (_, i) => i + 8).join(",")};BYMINUTE=0,30;BYSETPOS=${Array.from(
  { length: 29 },
  (_, i) => i + 1,
).join(",")}`

export const DAILY_BRIEFING_PROMPT = `Buat briefing pagi singkat buat Zaki untuk hari ini. Pakai tools yang tersedia, lewati bagian yang tool-nya nggak ada atau gagal, jangan mengarang.

1. Pengingat hari ini (daftar_pengingat): jam dan isinya.
2. Profil dan konteks dari recall kalau ada (get_profile): hal penting yang lagi berjalan.
3. Tugas dari todomap lewat MCP kalau tersedia: yang jatuh tempo hari ini atau telat.
4. Pipeline vorneo lewat MCP kalau tersedia: deal yang butuh tindakan hari ini.
5. Cuaca hari ini di kota Zaki lewat web fetch: satu baris.
6. Kalender hari ini kalau kalender terhubung.

Format: bahasa Indonesia santai, poin-poin pendek, maksimal sekitar 12 baris. Mulai dengan sapaan satu baris. Jangan ulangi instruksi ini.`

export const HEARTBEAT_PROMPT = `Kamu sedang cek berkala (heartbeat). Lihat pengingat yang akan datang (daftar_pengingat) dan inbox atau notifikasi dari MCP tools yang tersedia.

Kirim pesan hanya kalau ada yang benar-benar perlu perhatian Zaki dalam 30 menit ke depan atau sesuatu yang mendesak dan baru. Kalau ada, tulis singkat (1 sampai 3 baris) dalam bahasa Indonesia santai.

Kalau tidak ada yang penting, jawab persis ${SILENT_TOKEN} saja, tanpa kata lain. Jangan mengulang hal yang sudah dikirim sebelumnya.`

function todayAt(tz: string, now: number, hhmm: string): string {
  return `${utcToLocal(now, tz).slice(0, 10)}T${hhmm}:00`
}

export function dailyBriefing(chatId: string, tz: string, now = Date.now()): CreateJobInput {
  return {
    name: "Briefing pagi",
    kind: "briefing",
    rrule: "FREQ=DAILY",
    tz,
    localStart: todayAt(tz, now, "07:00"),
    prompt: DAILY_BRIEFING_PROMPT,
    chatId,
    modelRole: "default",
    toolScope: null,
    dailyTokenCap: 50_000,
  }
}

export function heartbeat(chatId: string, tz: string, now = Date.now()): CreateJobInput {
  return {
    name: "Heartbeat",
    kind: "heartbeat",
    rrule: HEARTBEAT_RRULE,
    tz,
    localStart: todayAt(tz, now, "08:00"),
    prompt: HEARTBEAT_PROMPT,
    chatId,
    modelRole: "fast",
    toolScope: null,
    dailyTokenCap: 100_000,
  }
}
