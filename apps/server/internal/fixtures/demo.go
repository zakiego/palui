package fixtures

import (
	"fmt"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// demo.ts: the data the dashboard was designed against. Every connector
// state, a few long names, a Markdown reply, a pending approval. It reads well
// on purpose; worst.go is the one that goes looking for trouble.
func demo(k *Kit) {
	now := k.Now
	sec := func(kv ...string) [][2]string {
		var out [][2]string
		for i := 0; i < len(kv); i += 2 {
			out = append(out, [2]string{kv[i], kv[i+1]})
		}
		return out
	}

	k.Connector("telegram", "telegram", ConnectorOpts{Status: "on", Secrets: sec("botToken", "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqqrr")})
	k.Connector("whatsapp", "whatsapp", ConnectorOpts{Status: "error", Detail: ptr("Connection closed (440): another session took over this linked device, so Palui paused instead of fighting it")})
	k.Connector("cliproxy", "cliproxy", ConnectorOpts{Status: "on", Secrets: sec("apiKey", "sk-cliproxy-0123456789abcdef")})
	k.Connector("gateway-litellm", "gateway", ConnectorOpts{Config: obj("baseUrl", "https://llm.example.com"), Status: "paused", Secrets: sec("apiKey", "sk-gateway-0123456789abcdef")})
	k.Connector("recall", "recall", ConnectorOpts{Status: "connecting", Secrets: sec("apiKey", "rcl_0123456789a91f")})
	k.Connector("t3code", "t3code", ConnectorOpts{Status: "on", Secrets: sec("pairingCode", "0123456789abcdef"), Config: obj("url", "http://host.docker.internal:3773")})
	k.Connector("mcp-northwind", "mcp", ConnectorOpts{Label: ptr("Northwind pipeline and property listings for every kabupaten in Jawa Timur"), Status: "on", Config: obj("url", "https://northwind.example.com/mcp")})
	k.Connector("mcp-linear", "mcp", ConnectorOpts{Label: ptr("Linear 🚀"), Status: "on", Trust: ptr("external"), Config: obj("url", "https://mcp.linear.app/mcp")})
	k.Connector("reminders", "reminders", ConnectorOpts{Status: "on"})
	k.Connector("jobs", "jobs", ConnectorOpts{Status: "paused"})
	k.Connector("flows", "flows", ConnectorOpts{Status: "on"})
	k.Connector("chats", "chats", ConnectorOpts{Status: "on"})
	k.Connector("webfetch", "webfetch", ConnectorOpts{Status: "on"})
	k.Connector("notes", "notes", ConnectorOpts{Status: "on"})
	k.Connector("settings", "settings", ConnectorOpts{Status: "on"})
	k.Connector("remote-mcp", "remote-mcp", ConnectorOpts{Status: "on"})
	k.Connector("mcp-jadwal-sholat", "mcp", ConnectorOpts{Label: ptr("jadwal-sholat"), Status: "on", Trust: ptr("external"), Config: obj("url", "https://sholat.example.com/mcp", "authMode", "none", "addedFromChat", true, "noPrivateData", true)})
	k.Connector("printer", "printer", ConnectorOpts{Status: "on", Config: obj("url", "ipp://host.docker.internal:631/printers/Office_Printer")})
	k.Connector("wa-inbox", "wa-inbox", ConnectorOpts{Label: ptr("WhatsApp pribadi"), Status: "on", Trust: ptr("external"), Config: obj("linkedNumber", "6281234567890")})
	k.Connector("ics-kantor", "ics", ConnectorOpts{Label: ptr("Kalender kantor"), Status: "on", Trust: ptr("external"), Secrets: sec("url", "https://calendar.example.com/ical/kantor/private-0123456789abcdef/basic.ics")})
	k.Instruction(Instruction{Title: "Jadwal sholat", Body: "Untuk pertanyaan jadwal sholat, pakai tools mcp-jadwal-sholat. Kalau server itu mati, pakai JSON API di https://sholat.example.com/api/v1 lewat web fetch.", CreatedAt: ptr(now - 3*H)})
	k.Instruction(Instruction{Title: "Ringkasan pagi", Body: "Mulai dengan cuaca, lalu agenda, lalu pengingat hari ini.", Enabled: ptr(false), CreatedAt: ptr(now - 2*H)})
	k.CustomCommand(CustomCommand{Name: "report-fix", Description: "Debug report, lalu T3 Code yang perbaiki", Prompt: "Start a T3 Code thread in the palui project to find and fix the problem in the debug report below. Owner's note: {{args}}\n\nPut the whole report in the task. Reply with the thread link only.", AttachReport: true, CreatedAt: ptr(now - 2*H)})
	k.CustomCommand(CustomCommand{Name: "pagi", Prompt: "Kasih ringkasan pagi: cuaca Surabaya, agenda hari ini, lalu pengingat.", Enabled: ptr(false), CreatedAt: ptr(now - H)})
	k.Preference("default_city", "Surabaya")
	k.Preference("timezone", "Asia/Makassar")
	k.Connector("attachments", "attachments", ConnectorOpts{Status: "on"})
	k.Connector("voice", "voice", ConnectorOpts{Status: "not_set_up"})

	web := k.WebChat()
	tg := k.Chat("telegram", "987654321", "Rizky 🌴 (Telegram DM with a rather long display name)", ChatOpts{})
	wa := k.Chat("whatsapp", "6281234567890@s.whatsapp.net", "", ChatOpts{})

	runID := k.Run(Run{ChatID: &web.ID, Trigger: "message", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](18234), TokensOut: ptr[int64](912), Status: "ok", StartedAt: now - 2*H, Ms: ptr[int64](8342)})
	k.Message(web, "user", "ingetin bayar listrik besok jam 9, terus cek pipeline northwind minggu ini ya", now-2*H)
	k.Tool(Tool{RunID: runID, ConnectorID: ptr("mcp-northwind"), Tool: "mcp-northwind__list_deals_for_the_current_week_grouped_by_stage_and_agent", Class: "read", Args: obj("stage", "masuk", "week", "2026-W46"), ResultPreview: ptr(`[{"id":"deal_01","title":"Rumah 2 lantai di Malang"}]`), Status: "ok", Ms: ptr[int64](1240), CreatedAt: now - 2*H + 1000})
	k.Tool(Tool{RunID: runID, ConnectorID: ptr("reminders"), Tool: "reminders__set_reminder", Class: "write", Args: obj("text", "bayar listrik", "when", "besok 09:00"), ResultPreview: ptr("Besok, Rabu 11 Nov, 09.00 WITA"), Status: "ok", Ms: ptr[int64](85), CreatedAt: now - 2*H + 2000})
	k.Message(web, "assistant", "Siap. Pengingat: Besok, Rabu 11 Nov, 09.00 WITA.\nPipeline Northwind minggu ini: 3 deal masuk, 1 survei.", now-2*H+9000, MsgExtra{RunID: &runID})
	k.Approval(Approval{RunID: runID, ChatID: web.ID, Tool: "mcp-linear__delete_issue", ToolClass: "destructive", ArgsPreview: ptr(`{"id":"PAL-123"}`), ExpiresAt: now + 10*MIN, CreatedAt: now})
	k.Message(tg, "user", "https://example.com/a/very/long/url/that/never/breaks/naturally/0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz", now-H)

	for i := int64(0); i < 14; i++ {
		chat := wa.ID
		if i%3 != 0 {
			chat = tg.ID
		}
		model := "gemini-2.5-flash-preview-with-a-long-model-id"
		if i%4 != 0 {
			model = "claude-sonnet-4-6"
		}
		status := "ok"
		var errText *string
		if i == 3 {
			status = "error"
			errText = ptr("429 from CLIProxyAPI: all 4 Claude accounts are rate limited; failed over to gateway-litellm which was paused")
		}
		k.Run(Run{ChatID: &chat, Trigger: "message", Model: &model, TokensIn: ptr(4000 + i*1500), TokensOut: ptr(300 + i*40), Status: status, Error: errText, StartedAt: now - i*22*H, Ms: ptr(2000 + i*300)})
	}

	jobID := k.Job(Job{Name: "Daily briefing", Kind: "briefing", RRule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", TZ: "Asia/Makassar", Prompt: "Brief me.", ChatID: tg.ID, NextRunAt: ptr(now + 10*H), LastRunAt: ptr(now - 14*H), LastStatus: ptr("ok"), LastResult: ptr("Pagi! Hari ini 2 pengingat, 3 deal Northwind masuk, cuaca Malang cerah berawan 31°C."), DailyTokenCap: ptr[int64](50_000)})
	k.Job(Job{Name: "Heartbeat with a name long enough to wrap onto a second line on a phone", Kind: "heartbeat", RRule: "FREQ=MINUTELY;INTERVAL=30;BYHOUR=8,9,10,11,12,13,14,15,16,17,18,19,20,21,22", TZ: "Asia/Makassar", Prompt: "NOTHING unless it matters.", ChatID: tg.ID, Enabled: ptr(false), DailyTokenCap: ptr[int64](30_000)})
	k.Run(Run{ChatID: &tg.ID, JobID: &jobID, Trigger: "schedule", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](21000), TokensOut: ptr[int64](1400), Status: "ok", StartedAt: now - H, Ms: ptr[int64](15000)})

	// Flows (RFC flows): a morning briefing, a prayer check-in and a weekly recap.
	briefing := k.Flow(tg, js(briefingDef), FlowOpts{})
	k.Flow(tg, js(sholatDef), FlowOpts{})
	k.FlowRun(briefing, FlowRun{
		Status:      "done",
		TriggerID:   ptr("main"),
		StartedAt:   now - 4*H,
		ScheduledAt: ptr(now - 4*H),
		Taint:       []string{"steps.weather"},
		Trigger:     js(`{"kind":"schedule","label":"Briefing","time":"06.00"}`),
		Steps: []FlowStep{
			{StepID: "prayer", Kind: "tool", Status: "ok", Output: js(`{"ok":true,"data":{"times":{"fajr":"04:39","dhuhr":"12:01"}}}`), Ms: ptr[int64](820)},
			{StepID: "weather", Kind: "tool", Status: "ok", Tainted: true, Output: js(`{"ok":true,"data":{"current_condition":[{"temp_C":"27"}]}}`), Ms: ptr[int64](1450)},
			{StepID: "reminders", Kind: "tool", Status: "ok", Output: js(`{"ok":true,"data":{"reminders":[{"text":"Bayar listrik"}]}}`), Ms: ptr[int64](12)},
			{StepID: "today", Kind: "set", Status: "ok", Output: js(`{"count":1}`)},
			{StepID: "send", Kind: "message", Status: "ok", Output: obj("text", "Selamat pagi! Rabu 11 Nov\n\nSholat: Subuh 04:39, Zuhur 12:01")},
		},
	})
	recapV2 := js(recapDef).(*jsonx.Object)
	recapV2.Set("description", "Rekap mingguan dengan semangat")
	recap := k.Flow(tg, js(recapDef), FlowOpts{Versions: []FlowVersion{{Definition: recapV2, Note: ptr("tambah deskripsi")}}})
	k.FlowRun(recap, FlowRun{Status: "failed", TriggerID: ptr("weekly"), StartedAt: now - 3*D, Error: ptr("Step recap: Tool flows__recap isn't available (connector off or tool removed)."),
		Steps: []FlowStep{{StepID: "recap", Kind: "tool", Status: "error", Error: ptr("Tool flows__recap isn't available (connector off or tool removed).")}}})

	rem := func(text string, inHours int64, rrule *string, status string) {
		k.Reminder(Reminder{ChatID: tg.ID, Text: text, RRule: rrule, TZ: "Asia/Makassar", FireAt: now + inHours*H, Status: status})
	}
	rem("Bayar listrik", 15, nil, "pending")
	rem("Kirim laporan mingguan ke tim Northwind, lengkap dengan rekap deal yang masuk, survei yang sudah dijadwalkan, dan catatan follow up untuk setiap agen 📊", 40, nil, "pending")
	rem("Standup", 20, ptr("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"), "pending")
	rem("Old one", -30, nil, "done")

	// A reply with the Markdown the model actually sends (bold, lists, code, a link).
	k.Message(web, "user", "rekap minggu ini dong", now-30*MIN)
	k.Message(web, "assistant",
		"**Rekap minggu ini** (W46):\n\n1. *3 deal* masuk di Northwind, 1 survei dijadwalkan\n2. Pengingat `bayar listrik` besok jam 09.00\n3. Detail di https://northwind.example.com/deals?week=2026-W46\n\n- Telegram: on\n- WhatsApp: **error 440**, perlu pairing ulang\n\n```\npalui connectors on whatsapp\n```\n\nLihat [dashboard Palui](https://palui.zakiego.com/app/connectors) untuk detail.",
		now-29*MIN)

	titles := []string{"Keluarga besar 👨‍👩‍👧‍👦 Surabaya", "مجموعة العائلة", "東京出張チーム", "A", "Northwind agents: Malang, Sidoarjo, Batu, Pasuruan, Lumajang and Probolinggo", "🔥🔥🔥", "supercalifragilisticexpialidocious_channel_without_spaces_0123456789"}
	for i := int64(0); i < 18; i++ {
		channel := "whatsapp"
		if i%2 != 0 {
			channel = "telegram"
		}
		c := k.Chat(channel, fmt.Sprintf("demo-%d", i), titles[i%int64(len(titles))], ChatOpts{})
		text := "Tolong cek lagi angka penjualan bulan ini dibandingkan bulan lalu, terutama untuk unit rumah tipe 36 di Malang"
		if i%3 != 0 {
			text = "ok sip 👍"
		}
		k.Message(c, "user", text, now-(i+3)*3*H)
	}

	k.Run(Run{ChatID: &web.ID, Trigger: "message", Model: ptr("claude-opus-4-1-20250805-thinking-extended-context"), TokensIn: ptr[int64](98_765_432), TokensOut: ptr[int64](1_234_567), Status: "ok", StartedAt: now - 5*MIN, Ms: ptr[int64](3_723_000)})
	for i := int64(0); i < 24; i++ {
		text := fmt.Sprintf("Telepon pak RT soal iuran keamanan dan kebersihan bulan %d 🏠", i+1)
		if i%5 != 0 {
			text = fmt.Sprintf("Follow up deal #%d", 1000+i)
		}
		var rrule *string
		if i%6 == 0 {
			rrule = ptr("FREQ=MONTHLY;BYMONTHDAY=1")
		}
		rem(text, 50+i*7, rrule, "pending")
	}
	for i := int64(0); i < 40; i++ {
		connector, kind, level := "cliproxy", "status", "warn"
		if i%3 != 0 {
			connector = "mcp-northwind"
		}
		if i%4 != 0 {
			kind = "tool"
		}
		if i%9 != 0 {
			level = "info"
		}
		message := "status change"
		if i%4 != 0 {
			message = fmt.Sprintf("mcp-northwind__list_deals_for_the_current_week_grouped_by_stage_and_agent ok in %d ms", 100+i*13)
		}
		k.Event(Event{At: now - (i+10)*15*MIN, ConnectorID: &connector, Kind: kind, Level: level, Message: message})
	}
	for i := int64(0); i < 6; i++ {
		connector := "whatsapp"
		if i%2 != 0 {
			connector = "telegram"
		}
		level := "info"
		if i == 1 {
			level = "error"
		} else if i == 4 {
			level = "warn"
		}
		message := fmt.Sprintf("status change %d", i)
		if i == 1 {
			message = "error: 401 Unauthorized from api.telegram.org (the bot token was revoked in @BotFather)"
		}
		k.Event(Event{At: now - i*10*MIN, ConnectorID: &connector, Kind: "status", Level: level, Message: message})
	}

	k.Attachment(Attachment{ChatID: wa.ID, Title: "Tiket pesawat SUB-CGK 12 Des", Description: "Lion Air JT-581, berangkat 12 Des 2026 06.10 WITA, kode booking XKQ9PL.", Kind: "pdf", Filename: "e-ticket-XKQ9PL.pdf", Size: ptr[int64](184_320), CreatedAt: ptr(now - 3*H)})
	k.Attachment(Attachment{ChatID: tg.ID, Title: "KTP Budi", Description: "Foto KTP, berlaku seumur hidup.", Kind: "png", Size: ptr[int64](412_000), CreatedAt: ptr(now - 26*H)})
	k.Attachment(Attachment{ChatID: tg.ID, Title: "Struk servis motor", Description: "Bengkel resmi Malang, ganti oli dan kampas rem, Rp 385.000.", Kind: "pdf", Filename: "struk_servis_2026-11-01.pdf", Size: ptr[int64](96_000), CreatedAt: ptr(now - 9*D)})
	// Outbound: one contact on Always allow, one on Always ask.
	adik := k.Chat("whatsapp", "6282200001001@s.whatsapp.net", "Rizky", ChatOpts{Allowed: ptr(false)})
	k.Contact(adik, Contact{Name: "Rizky", Address: "6282200001001", Policy: "allow", Note: ptr("Adik")},
		[]Send{{Text: "Halo, ini asisten Rizky. Rizky lagi otw ya.", Status: "sent", At: now - 40*MIN, ApprovedBy: "policy"}},
		[]Scheduled{{Text: "Halo, ini asisten Rizky. Ingetin jemput jam 8 ya.", FireAt: now + 6*H}})
	ibu := k.Chat("whatsapp", "6281111111111@s.whatsapp.net", "Ibu", ChatOpts{Allowed: ptr(false)})
	k.Contact(ibu, Contact{Name: "Ibu", Address: "6281111111111"},
		[]Send{{Text: "Bu, Rizky pulang Jumat ya.", Status: "sent", At: now - 2*D}},
		[]Scheduled{{Text: "Kabar mingguan dari Rizky.", FireAt: now + 2*D, RRule: ptr("FREQ=WEEKLY;BYDAY=FR")}})
	// An HTTP API credential. Last, so its rows don't shift any earlier event id.
	k.Connector("http-elevenlabs", "http", ConnectorOpts{
		Label:   ptr("ElevenLabs"),
		Status:  "on",
		Trust:   ptr("external"),
		Config:  obj("domains", "api.elevenlabs.io", "auth", "header", "headerName", "xi-api-key", "valueTemplate", "{value}", "policy", "allow"),
		Secrets: sec("value", "sk_demo_not_a_real_key"),
	})
}
