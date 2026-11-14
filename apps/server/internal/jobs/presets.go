package jobs

// Job presets (jobs/presets.ts, PLAN 7). Each returns a CreateInput for
// Create; the dashboard offers them as one-click starters.

import (
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/reminders"
)

// SilentToken is what the heartbeat answers when nothing matters; the engine
// then sends nothing.
const SilentToken = "NOTHING"

// HeartbeatRRule: every 30 minutes from 08:00 to 22:00 inclusive (the 30
// half-hours from 08:00 to 22:30 minus the last one; BYSETPOS keeps 1 to 29).
var HeartbeatRRule = func() string {
	hours := make([]string, 15)
	for i := range hours {
		hours[i] = strconv.Itoa(i + 8)
	}
	pos := make([]string, 29)
	for i := range pos {
		pos[i] = strconv.Itoa(i + 1)
	}
	return "FREQ=DAILY;BYHOUR=" + strings.Join(hours, ",") + ";BYMINUTE=0,30;BYSETPOS=" + strings.Join(pos, ",")
}()

const DailyBriefingPrompt = `Buat briefing pagi singkat buat Zaki untuk hari ini. Pakai tools yang tersedia, lewati bagian yang tool-nya nggak ada atau gagal, jangan mengarang.

1. Pengingat hari ini (list_reminders): jam dan isinya.
2. Profil dan konteks dari recall kalau ada (get_profile): hal penting yang lagi berjalan.
3. Tugas dari todomap lewat MCP kalau tersedia: yang jatuh tempo hari ini atau telat.
4. Pipeline vorneo lewat MCP kalau tersedia: deal yang butuh tindakan hari ini.
5. Cuaca hari ini di kota Zaki lewat web fetch: satu baris.
6. Kalender hari ini kalau kalender terhubung.

Format: bahasa Indonesia santai, poin-poin pendek, maksimal sekitar 12 baris. Mulai dengan sapaan satu baris. Jangan ulangi instruksi ini.`

const HeartbeatPrompt = `Kamu sedang cek berkala (heartbeat). Lihat pengingat yang akan datang (list_reminders) dan inbox atau notifikasi dari MCP tools yang tersedia.

Kirim pesan hanya kalau ada yang benar-benar perlu perhatian Zaki dalam 30 menit ke depan atau sesuatu yang mendesak dan baru. Kalau ada, tulis singkat (1 sampai 3 baris) dalam bahasa Indonesia santai.

Kalau tidak ada yang penting, jawab persis ` + SilentToken + ` saja, tanpa kata lain. Jangan mengulang hal yang sudah dikirim sebelumnya.`

func todayAt(tz string, now int64, hhmm string) string {
	return reminders.UTCToLocal(now, tz)[:10] + "T" + hhmm + ":00"
}

func ptr[T any](v T) *T { return &v }

func DailyBriefing(chatID, tz string, now int64) CreateInput {
	return CreateInput{
		Name:          "Briefing pagi",
		Kind:          "briefing",
		RRule:         "FREQ=DAILY",
		TZ:            tz,
		LocalStart:    todayAt(tz, now, "07:00"),
		Prompt:        DailyBriefingPrompt,
		ChatID:        chatID,
		ModelRole:     "default",
		ToolScope:     nil,
		DailyTokenCap: ptr(int64(50_000)),
	}
}

func Heartbeat(chatID, tz string, now int64) CreateInput {
	return CreateInput{
		Name:          "Heartbeat",
		Kind:          "heartbeat",
		RRule:         HeartbeatRRule,
		TZ:            tz,
		LocalStart:    todayAt(tz, now, "08:00"),
		Prompt:        HeartbeatPrompt,
		ChatID:        chatID,
		ModelRole:     "fast",
		ToolScope:     nil,
		DailyTokenCap: ptr(int64(100_000)),
	}
}
