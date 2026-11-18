package fixtures

import (
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// worst.ts: the realistic worst case for every value the dashboard renders.
// Each string is either something a real person or channel produces (a long
// hyphenated name, a JID, a pasted URL, a stack trace) or sits at the limit
// the dashboard's own validation accepts. Nothing here is random filler: if a
// row looks broken, a real owner can get there too.
//
// The different failures are spread across the first rows of each list, the
// way real data mixes them, instead of stacked into one row.

// Limits the dashboard enforces (the zod schemas in src/lib/fns). A value
// that claims to be "at the limit" is checked against it, so the fixture
// can't drift past what the forms accept.
var limits = struct {
	flowName, flowDescription, flowSteps, flowTriggers, flowError, agentName, agentSlug, attachmentTitle, attachmentDescription, attachmentFilename,
	connectorLabel, connectorID, jobName, rrule, reminderText, contactName, outboundText, contactNote, hookName, model, persona, webMessage,
	toolName, approvalArgs, toolResult, statusDetail, jobResult, instructionTitle, instructionBody, preferenceKey, preferenceValue, botUsername,
	commandName, commandDescription, commandPrompt, memberName, memberAccounts int
}{80, 200, 40, 8, 300, 40, 24, 200, 1000, 200, 80, 63, 80, 300, 500, 60, 2000, 200, 60, 120, 8000, 8000, 64, 300, 500, 300, 500, 80, 1000, 40, 300, 32,
	32, 120, 4000, 60, 5}

const (
	worstLongURL    = "https://example.com/workspaces/northwind-industries-holdings/projects/q3-launch/docs/9f8e7d6c5b4a?tab=comments&filter=unresolved&sort=newest"
	worstEmail      = "bartholomew.fitzgerald@northwind-industries-holdings.example.com"
	worstPastedBlob = "iVBORw0KGgoAAAANSUhEUgAAAoAAAAHgCAYAAAA10dzkAAAACXBIWXMAAAsTAAALEwEAmpwYAAAAB3RJTUUH6AsLBAUNq0p8bwAAIABJREFUeJzs3XmYXFWd//H3ube6es2+dxKykLCEsIQl7CCLgCIqKuKCOm7jMuo4zjg6zvxmxnFmdHTUcR03VFxQEVBAZBVkX8KSQBISsu9Jd5LudHe6q+qe3x9V3el0upPuTnVXdefzep5+uurWrVun7r1V93vP8j0G"
	worstParagraph  = "Tolong cek lagi angka penjualan bulan ini dibandingkan bulan lalu, terutama untuk unit rumah tipe 36 di Banjarbaru dan Martapura. Kalau ada selisih lebih dari lima persen, catat penyebabnya dan kirim ringkasannya ke grup sebelum rapat Senin pagi. "
	worstModelLong  = "accounts/fireworks/models/llama-v3p3-70b-instruct-turbo-preview-0925-extended-context-128k-northwind-finetune-2026-11"
	worstTZLong     = "America/North_Dakota/New_Salem"
	worstToolLong   = "mcp-northwind-kb__search_internal_knowledge_base_by_department_x"
	worstRemoved    = "chat_0mhremoved0000000000"
	// "Q3 Board Deck <em dash> FINAL (revised) v12 [approved by legal].pdf"
	worstDeck = "Q3 Board Deck — FINAL (revised) v12 [approved by legal].pdf"
)

var worstStack = strings.Join([]string{
	"TypeError: Cannot read properties of undefined (reading 'accessToken')",
	"    at refreshGoogleToken (file:///app/packages/connectors/src/gcal/runtime.ts:142:31)",
	"    at async Object.start (file:///app/packages/connectors/src/gcal/runtime.ts:61:5)",
	"    at async Supervisor.startConnector (file:///app/apps/runtime/src/supervisor.ts:233:9)",
	"    at async Supervisor.reconcile (file:///app/apps/runtime/src/supervisor.ts:188:7)",
	"    at async Timeout._onTimeout (file:///app/apps/runtime/src/main.ts:97:5)",
}, "\n")

var worstStackFirst = strings.SplitN(worstStack, "\n", 2)[0]

func joinInts(from, to, step int) string {
	var parts []string
	for i := from; i < to; i += step {
		parts = append(parts, fmt.Sprint(i))
	}
	return strings.Join(parts, ",")
}

var worstRRuleLong = "FREQ=MINUTELY;INTERVAL=5;BYHOUR=" + joinInts(0, 24, 1) + ";BYMINUTE=" + joinInts(0, 60, 5) + ";BYDAY=MO,TU,WE,TH,FR,SA,SU;BYMONTH=" + joinInts(1, 13, 1) + ";BYMONTHDAY=" + joinInts(1, 32, 1)

var worstMarkdownStress = strings.Join([]string{
	"# Rekap Q3 (heading yang tidak didukung)",
	"",
	"| Kabupaten | Deal masuk | Survei | Closing | Catatan |",
	"| --- | --- | --- | --- | --- |",
	"| Banjarbaru | 1,284 | 97 | 12 | menunggu KPR |",
	"| Hulu Sungai Selatan | 0 | 0 | 0 | belum ada agen |",
	"",
	"Gambar yang kamu tempel tadi: `" + worstPastedBlob + "`",
	"",
	"1. **Langkah pertama** yang cukup panjang sampai harus turun ke baris kedua bahkan di layar lebar, supaya kelihatan apakah nomor dan teksnya tetap sejajar",
	"   - sub-poin a",
	"   - sub-poin b dengan `kode inline`",
	"2. *Langkah kedua*",
	"",
	"```",
	`curl -sS -X POST "` + worstLongURL + `" -H "authorization: Bearer $NORTHWIND_TOKEN" -H "content-type: application/json" --data '{"stage":"masuk","week":"2026-W46","agents":["banjarbaru","martapura","pelaihari"]}'`,
	"```",
	"",
	"Selengkapnya di " + worstLongURL + " atau [buka dokumen " + worstDeck + "](" + worstLongURL + ").",
	"",
	"Teks mentah yang harus tampil apa adanya: <script>alert(1)</script> &amp; <b>tebal</b> ![gambar](https://example.com/x.png)",
}, "\n")

func utcMs(y int, m time.Month, d, h, min int) int64 {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC).UnixMilli()
}

func worst(k *Kit) {
	now := k.Now
	L := limits
	sec := func(kv ...string) [][2]string {
		var out [][2]string
		for i := 0; i < len(kv); i += 2 {
			out = append(out, [2]string{kv[i], kv[i+1]})
		}
		return out
	}
	none := ChatOpts{}

	// --- settings: translated chrome, every list at its maximum ------------------
	k.Setting("uiLanguage", "id")
	k.Setting("language", "id")
	k.Setting("timezone", worstTZLong)
	k.Setting("persona", fill(settings.DefaultPersona+"\nSelalu sebut sumber data, tanggal pengambilan, dan zona waktunya. Jangan pernah menghapus apa pun tanpa persetujuan.\n\n", L.persona))
	k.Setting("fallbackChannel", "none")
	k.Setting("budgets", js(`{"maxSteps":50,"turnTimeoutMs":600000,"maxTools":128,"dailyTokenCap":100000000,"maxRunsPerHour":1000}`))
	sixSteps := func() []any {
		return []any{
			// A connector that was removed after the chain was saved: openrouter.
			obj("connectorId", "cliproxy", "model", within(worstModelLong, L.model), "effort", "xhigh"),
			obj("connectorId", "ai-gateway", "model", "claude-sonnet-4-6", "effort", "none"),
			obj("connectorId", "openrouter", "model", "anthropic/claude-opus-4.1:thinking"),
			obj("connectorId", "cliproxy", "model", "gemini-2.5-flash-preview-with-a-long-model-id"),
			obj("connectorId", "ai-gateway", "model", "o"),
			obj("connectorId", "cliproxy", "model", "claude-haiku-4-5-20251001"),
		}
	}
	k.Setting("modelRoles", obj("default", sixSteps(), "fast", []any{obj("connectorId", "openrouter", "model", "x")}, "vision", sixSteps()))

	// --- connectors: every status, the badges that stack, labels of every shape --
	k.Connector("telegram", "telegram", ConnectorOpts{Status: "on", Secrets: sec("botToken", "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqqrr")})
	k.Connector("whatsapp", "whatsapp", ConnectorOpts{Status: "error", Detail: ptr(within(
		"Connection closed (440): another session took over this linked device (session 6281234567890123:47@s.whatsapp.net, ref 2@k8ZbQ1vE0mYxN3pLr7sTqUwVcXyAaBbCcDdEeFfGgHhIiJjKk==), so Palui paused instead of fighting it. Unlink the old device in WhatsApp > Linked devices, then pair again.",
		L.statusDetail))})
	k.Connector("web", "web", ConnectorOpts{Status: "on"})
	k.Connector("cliproxy", "cliproxy", ConnectorOpts{Status: "on", Secrets: sec("apiKey", "sk-cliproxy-0123456789abcdef")})
	// Enabled but paused: the owner wants it on and it isn't, so it needs attention.
	k.Connector("ai-gateway", "ai-gateway", ConnectorOpts{Status: "paused", Enabled: ptr(true), Detail: ptr("Paused by the daily token cap (100,000,000 of 100,000,000 used)"), Secrets: sec("apiKey", "zkg_0123456789abcdef")})
	k.Connector("recall", "recall", ConnectorOpts{Status: "connecting", Secrets: sec("apiKey", "rcl_0123456789a91f")})
	k.Connector(within("mcp-northwind-industries-holdings-internal-knowledge-base-prod", L.connectorID), "mcp", ConnectorOpts{
		Label:  ptr(within("Northwind Industries Holdings internal knowledge base (production, read-only)", L.connectorLabel)),
		Status: "error",
		Trust:  ptr("external"),
		Detail: ptr("401 Unauthorized from " + worstLongURL),
		Config: obj("url", worstLongURL),
	})
	k.Connector("mcp-j", "mcp", ConnectorOpts{Label: ptr("J"), Status: "on", Config: obj("url", "https://j.example.com/mcp")})
	k.Connector("mcp-files", "mcp", ConnectorOpts{Label: ptr("خادم الملفات المشتركة"), Status: "on", Trust: ptr("external"), Config: obj("url", "https://files.example.com/mcp", "noPrivateData", true)})
	k.Connector("mcp-fox", "mcp", ConnectorOpts{Label: ptr("🦊"), Status: "paused", Config: obj("url", "https://fox.example.com/mcp")})
	// A label with no break opportunity: someone pasted the host as the name.
	k.Connector("mcp-host", "mcp", ConnectorOpts{Label: ptr(within("mcp.internal.northwind-industries-holdings.example.com/v2/sse", L.connectorLabel)), Status: "on", Config: obj("url", "https://mcp.internal.northwind-industries-holdings.example.com/v2/sse", "noPrivateData", true)})
	k.Connector("mcp-rnd", "mcp", ConnectorOpts{Label: ptr(`<b>R&D</b> **beta** &amp; "quotes"`), Status: "on", Config: obj("url", "https://rnd.example.com/mcp")})
	k.Connector("mcp-de", "mcp", ConnectorOpts{Label: ptr("Benachrichtigungseinstellungen"), Status: "connecting", Config: obj("url", "https://de.example.com/mcp")})
	k.Connector("mcp-notion", "mcp", ConnectorOpts{Label: ptr("Notion"), Status: "on", Config: obj("url", "https://notion.example.com/mcp")})
	// Same label as the one above: only the id tells them apart.
	k.Connector("mcp-notion-2", "mcp", ConnectorOpts{Label: ptr("Notion"), Status: "error", Detail: ptr("ECONNREFUSED 127.0.0.1:8931"), Config: obj("url", "http://127.0.0.1:8931/mcp")})
	k.Connector("mcp-vn", "mcp", ConnectorOpts{Label: ptr("Đặng Thị Ngọc Hân: lịch làm việc"), Status: "on", Config: obj("url", "https://vn.example.com/mcp")})
	k.Connector("mcp-linear", "mcp", ConnectorOpts{Label: ptr("Linear"), Status: "on", Trust: ptr("external"), Config: obj("url", "https://mcp.linear.app/mcp")})
	k.Connector("mcp-vorneo", "mcp", ConnectorOpts{Label: ptr("Vorneo"), Status: "on", Config: obj("url", "https://vorneo.id/api/mcp/")})
	k.Connector("mcp-keuangan", "mcp", ConnectorOpts{Label: ptr("Keuangan"), Status: "paused", Config: obj("url", "https://keuangan.example.com/mcp")})
	k.Connector("mcp-todomap", "mcp", ConnectorOpts{Label: ptr("Todo Map"), Status: "on", Config: obj("url", "https://todo.example.com/api/mcp")})
	k.Connector("gcal", "gcal", ConnectorOpts{Status: "not_set_up"})
	k.Connector("t3code", "t3code", ConnectorOpts{
		Status:  "error",
		Detail:  ptr("T3 Code rejected Palui's token (it lasts 30 days). On the Mac, run t3 auth pairing create --ttl 10m and paste the new code in this connector."),
		Secrets: sec("pairingCode", "0123456789abcdefghijklmnopqrstuv"),
		Config:  obj("url", "http://host.docker.internal:3773", "baseRef", "release/2026-11-northwind-industries-holdings-hotfix", "model", "claudeAgent/claude-opus-5-5"),
	})
	k.Connector("reminders", "reminders", ConnectorOpts{Status: "on"})
	k.Connector("jobs", "jobs", ConnectorOpts{Status: "on"})
	k.Connector("flows", "flows", ConnectorOpts{Status: "error", Detail: ptr("5 flows failing, 1 waiting for approval, 3 sources that couldn't be read today: the longest status a skill can report")})
	for _, t := range []string{"chats", "webfetch", "attachments"} {
		k.Connector(t, t, ConnectorOpts{Status: "on"})
	}
	k.Connector("voice", "voice", ConnectorOpts{Status: "not_set_up"})
	for _, t := range []string{"hooks", "notes", "settings"} {
		k.Connector(t, t, ConnectorOpts{Status: "on"})
	}
	k.Connector("remote-mcp", "remote-mcp", ConnectorOpts{Status: "paused"})

	// --- owner notes: at the limits, a URL that never breaks, mixed scripts -------
	k.Instruction(Instruction{
		Title:     within("Jadwal sholat untuk kota default, atau koordinat kalau sedang di luar Indonesia", L.instructionTitle),
		Body:      fill("Untuk pertanyaan jadwal sholat, pakai tools mcp-jadwal-sholat. Kalau server itu mati, pakai https://sholat.zakiego.com/api/v1/jadwal?kota=banjarmasin&tanggal=2026-11-11&format=json&metode=kemenag lewat web fetch.\n\n", L.instructionBody),
		CreatedAt: ptr(now - 40*D),
	})
	k.Instruction(Instruction{Title: "J", Body: "Selalu jawab singkat.", CreatedAt: ptr(now - 2*D)})
	k.Instruction(Instruction{Title: "خادم الملفات: الملخص اليومي", Body: "Ringkas file baru di server berkas setiap pagi.", Enabled: ptr(false), CreatedAt: ptr(now - D)})
	k.Preference(within("default_city_when_travelling_outside_kal", L.preferenceKey), "Banjarmasin")
	k.Preference("home_coordinates", within(fill("-3.3194374, 114.5907765; ", L.preferenceValue), L.preferenceValue))
	k.Preference("x", "y")
	k.Preference("nama_panggilan", "Đặng Thị Ngọc Hân 🌴")

	// --- chats: names of every shape, and every age a "last message" can have ----
	web := k.WebChat()
	say := func(chat db.Chat, text string, at int64) { k.Message(chat, "user", text, at) }
	sayAs := func(chat db.Chat, text string, at int64, role string) { k.Message(chat, role, text, at) }

	aleksandra := k.Chat("whatsapp", "48600123456@s.whatsapp.net", "Aleksandra Wiśniewska-Kowalczyk", ChatOpts{AckEmoji: "👨‍👩‍👧‍👦"})
	say(aleksandra, worstLongURL, now-10_000)

	// A phone with the wrong clock: the newest message is from the future.
	jo := k.Chat("telegram", "1000000001", "Jo", none)
	say(jo, "ok", now+2*H)

	family := k.Chat("telegram", "-1001234567890", "Keluarga Besar Bani H. Abdurrahman Siddiq 👨‍👩‍👧‍👦 Banjarmasin, Martapura, Banjarbaru, Pelaihari (arisan, haul, dan info acara)", ChatOpts{Kind: "group", ReplyMode: "all"})
	// Seven characters, then emoji: the list's 140-character preview cut lands in the middle of one.
	say(family, "Wkwkwk "+strings.Repeat("😂", 80), now-50_000)

	// --- agents (PLAN 19): a name and slug at the limit, a cap already hit, guests in a group ---
	// Its own bot, with the longest username Telegram allows.
	k.Connector("tgbot-arisan", "tgbot", ConnectorOpts{Label: ptr("Arisan bot"), Status: "on", Config: obj("botUsername", within("koordinator_arisan_haul_bani_bot", L.botUsername)), Secrets: sec("botToken", "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqqrs")})
	var readContainers []string
	for i := 1; i <= 5; i++ {
		readContainers = append(readContainers, fmt.Sprintf("kb-arisan-keluarga-bani-abdurrahman-siddiq-banjarmasin-catatan-%d", i))
	}
	arisan := k.Agent(Agent{
		Slug:        within("koordinator-arisan-haul-", L.agentSlug),
		Name:        within("Koordinator Arisan & Haul Bani Abdurrahm", L.agentName),
		Persona:     fill("Kamu koordinator arisan keluarga. Catat siapa yang sudah bayar, ingatkan jadwal haul, dan jawab sopan dalam bahasa Banjar kalau ditanya pakai bahasa Banjar. ", L.persona),
		AvatarIcon:  ptr("chef-hat"),
		AvatarColor: ptr("violet"),
		// Allow-all on, one tool of every other kind, so the Tools tab shows each select value and both group counts.
		DailyTokenCap:   ptr[int64](250_000),
		TokensToday:     312_481,
		ToolPermissions: obj("allowAll", true, "tools", obj("mcp-files__restart_service", "ask", "mcp-files__send_email_to_a_customer_with_a_long_tool_name", "deny", "mcp-files__list_deals", "allow")),
		RecallContainer: ptr("agent-arisan-keluarga-bani-abdurrahman-siddiq-banjarmasin-2026"),
		// Five shared containers at 64 characters each (MAX_READ_CONTAINERS, RECALL_CONTAINER).
		RecallReadContainers: readContainers,
		RecallPerChat:        ptr(true),
		TranscribeVoice:      ptr(true),
		TelegramConnectorID:  ptr("tgbot-arisan"),
		// Its own six-step default chain (with the removed connector) and fast model; vision follows the global one.
		ModelRoles: obj("default", sixSteps(), "fast", []any{obj("connectorId", "ai-gateway", "model", "claude-haiku-4-5-20251001")}),
	})
	// An uploaded photo: a small gradient PNG, the shape a browser-resized photo has.
	cs := k.Agent(Agent{Slug: "cs", Name: "CS", Persona: "Jawab pertanyaan pelanggan Vorneo.", GuestTools: "none", AvatarImage: ptr(GradientAvatar48())})
	k.Assign(family, arisan, true)
	// Members (PLAN 19.4), made-up people and numbers. A name at the limit
	// mixing scripts and an emoji, in two agents, with the most accounts: a
	// phone with a learned LID, a LID only, a 20-digit Telegram id and two
	// more numbers.
	worstAccounts := []MemberAccount{
		{Channel: "whatsapp", ExternalID: "6281200000011@s.whatsapp.net", AltID: ptr("100000000000011@lid")},
		{Channel: "whatsapp", ExternalID: "100000000000012@lid", DisplayName: ptr("Nurul 🧕🏽")},
		{Channel: "telegram", ExternalID: "12345678901234567890"},
		{Channel: "whatsapp", ExternalID: "6281200000013@s.whatsapp.net"},
		{Channel: "whatsapp", ExternalID: "447700900013@s.whatsapp.net"},
	}
	if len(worstAccounts) > L.memberAccounts {
		panic(fixtureErr{fmt.Errorf("%d member accounts, over the limit of %d", len(worstAccounts), L.memberAccounts)})
	}
	nurul := k.Member(Member{Name: within("Nurul Hidayah binti H. Abdul Gani نور الهداية 🧕🏽 Bendahara", L.memberName), Accounts: worstAccounts, Agents: []string{arisan, cs}})
	// One letter, one account: the guest who asks in the family group below,
	// so the people list has to leave out someone who is already a member.
	k.Member(Member{Name: "M", Accounts: []MemberAccount{{Channel: "telegram", ExternalID: "1000000099"}}, Agents: []string{arisan}})
	// In CS only: arisan's page offers them under members of other agents.
	k.Member(Member{Name: "Admin Gudang", Accounts: []MemberAccount{{Channel: "whatsapp", ExternalID: "6281200000014@s.whatsapp.net"}}, Agents: []string{cs}})
	// Agent-scoped MCP: one owned by the agent with the longest name, one by CS,
	// and one whose owner was deleted (serves nobody).
	k.OwnConnector("mcp-files", arisan)
	k.OwnConnector("mcp-vorneo", cs)
	k.OwnConnector("mcp-keuangan", "agt_deleted")
	// Every agent but the default, plus one deleted since.
	k.ShareConnector("mcp-linear", arisan, cs, "agt_deleted")
	// The agent page (/agents/<slug>): instructions at the limits, and (below)
	// a chat with no title and a chat it no longer answers in.
	k.Instruction(Instruction{
		AgentID:   &arisan,
		Title:     within("Rekap setoran arisan tiap tanggal 25, urut dari yang paling lama belum bayar ya.", L.instructionTitle),
		Body:      fill("Kalau ada yang tanya siapa yang belum bayar, sebut nama dan jumlahnya, jangan sebut nomor HP. Daftar lengkap ada di https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_arisan_bani_abdurrahman/edit#gid=0 dan jangan dibagikan ke grup. ", L.instructionBody),
		CreatedAt: ptr(now - 20*D),
	})
	k.Instruction(Instruction{AgentID: &arisan, Title: "Haul", Body: "Haul tahun ini hari Ahad.", Enabled: ptr(false), CreatedAt: ptr(now - 3*D)})
	// Custom commands: every limit at once on one agent's command, a global one
	// with the same name, and a one-letter one with no description that's off.
	k.CustomCommand(CustomCommand{
		AgentID:      &arisan,
		Name:         within("rekap-setoran-arisan-bani-abdurr", L.commandName),
		Description:  within(fill("Rekap setoran arisan bulan ini, urut dari yang paling lama belum bayar, lalu kirim ke grup keluarga. ", L.commandDescription), L.commandDescription),
		Prompt:       fill("Buat rekap setoran arisan dari https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_arisan_bani_abdurrahman/edit#gid=0 untuk {{args}}. ", L.commandPrompt),
		AttachReport: true,
		CreatedAt:    ptr(now - 10*D),
	})
	k.CustomCommand(CustomCommand{Name: "rekap-setoran-arisan-bani-abdurr", Description: "Rekap untuk agent lain", Prompt: "Rekap setoran.", CreatedAt: ptr(now - 9*D)})
	k.CustomCommand(CustomCommand{Name: "x", Prompt: "خلاصة اليوم 🌴", Enabled: ptr(false), CreatedAt: ptr(now - D)})
	k.Message(family, "user", "@palui_bot siapa yang belum bayar arisan bulan ini?", now-40_000, MsgExtra{FromOwner: ptr(false), SenderName: ptr(ptr("Muhammad Fadhlurrahman Al-Banjari bin H. Abdurrahman Siddiq")), SenderID: ptr("1000000099")})
	k.Message(family, "user", "؟", now-30_000, MsgExtra{FromOwner: ptr(false), SenderName: ptr[*string](nil), SenderID: ptr("1000000100")})

	// No title yet: the list falls back to the raw JID.
	// Auto-save on, keeping files forever.
	noTitleGroup := k.Chat("whatsapp", "120363041234567890123456@g.us", "", ChatOpts{Kind: "group", SaveMediaDays: ptr[int64](0)})
	k.Assign(noTitleGroup, arisan, true)
	sayAs(noTitleGroup, "**Rekap** minggu ini:\n\n1. *3 deal* masuk\n2. `bayar listrik` besok", now-59*MIN, "assistant")
	// Nurul may set chat rules for arisan; the members list shows the switch on.
	k.MemberRules(arisan, nurul)
	noTitleDm := k.Chat("whatsapp", "258431789012345678@lid", "", none)
	sayAs(noTitleDm, "", now-23*H, "assistant")

	single := k.Chat("whatsapp", "6281200000001@s.whatsapp.net", "J", none)
	say(single, "Line one\nLine two\nLine three", now-26*H)
	vietnamese := k.Chat("telegram", "1000000002", "Đặng Thị Ngọc Hân", none)
	say(vietnamese, "Ừ, được rồi. ก็ที่นี่ผู้ใหญ่ชี้ กตัญญูรู้คุณ", now-12*D)
	cjk := k.Chat("whatsapp", "8613800138000@s.whatsapp.net", "王秀英", ChatOpts{Ack: ptr(false)})
	say(cjk, "明天上午九点在办公室开会，请带上第三季度的销售报告和所有客户的联系方式，谢谢。", now-29*D)
	arabic := k.Chat("whatsapp", "966500000000@s.whatsapp.net", "نور الهدى عبد الرحمن", none)
	say(arabic, "مرحبا، هل يمكنك تذكيري باجتماع الغد الساعة 9؟ (Q3 report)", now-31*D)
	fox := k.Chat("telegram", "1000000003", "🦊 Fox", none)
	say(fox, "🦊", now-11*30*D)
	priya := k.Chat("whatsapp", "919800000000@s.whatsapp.net", "👩🏽‍💻 Priya", none)
	say(priya, worstEmail, now-3*365*D)
	// A channel that sent no timestamp: the row stores zero.
	sam := k.Chat("whatsapp", "6281200000002@s.whatsapp.net", "  Sam   Lee ", ChatOpts{CreatedAt: ptr[int64](0)})
	say(sam, "p", 0)
	christopher := k.Chat("telegram", "1000000004", "Christopher Alexander Montgomery III", none)
	say(christopher, fill(worstParagraph, 600), now-2*D)
	for i, channel := range []string{"whatsapp", "telegram", "whatsapp"} {
		budi := k.Chat(channel, fmt.Sprintf("budi-%d", i), "Budi", none)
		say(budi, "ok sip 👍", now-int64(3+i)*D)
	}
	script := k.Chat("telegram", "1000000005", "<script>alert(1)</script>", none)
	say(script, "<img src=x onerror=alert(1)> &amp; **bold**", now-6*D)
	twoLines := k.Chat("whatsapp", "6281200000003@s.whatsapp.net", "Line one\nLine two", none)
	say(twoLines, "y", now-7*D)
	handle := k.Chat("telegram", "1000000006", "@northwind_industries_support_bot", none)
	say(handle, "IMG_20250914_183022_HDR_portrait_edited_edited.HEIC", now-8*D)
	lower := k.Chat("whatsapp", "6281200000004@s.whatsapp.net", "dana", none)
	say(lower, worstDeck, now-9*D)
	irish := k.Chat("telegram", "1000000007", "Seán O'Brien-Ó Súilleabháin", none)
	say(irish, "9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f", now-10*D)
	link := k.Chat("telegram", "-1009876543210", "t.me/northwind_industries_holdings_announcements_indonesia", ChatOpts{Kind: "group"})
	say(link, "first.last+billing-notifications@example.com", now-13*D)
	k.Assign(link, arisan, false)
	// A WhatsApp group the owner just added the bot to: off, no agent, one unanswered mention.
	fresh := k.Chat("whatsapp", "120363049876543210987654@g.us", "Finansial Keluarga & Tabungan Haji Bani Abdurrahman 2027", ChatOpts{Kind: "group", Allowed: ptr(false)})
	k.Message(fresh, "user", "Halo @Palui Agent", now-2*MIN)
	// Not on the WhatsApp allowlist, and a chat that has never had a message.
	k.Chat("whatsapp", "6281200000005@s.whatsapp.net", "Konstantin Oberhauser-Wettstein", ChatOpts{Allowed: ptr(false), CreatedAt: ptr(now - 14*D)})
	k.Chat("telegram", "1000000008", "María José de la Cruz y Fernández", ChatOpts{CreatedAt: ptr(now - 15*D)})

	// --- the web transcript: every message shape in one conversation -------------
	t := now - 3*H
	next := func() int64 { t += MIN; return t }

	promptAt := next()
	bigRun := k.Run(Run{ChatID: &web.ID, Trigger: "message", Model: ptr(within(worstModelLong, L.model)), TokensIn: ptr[int64](98_765_432), TokensOut: ptr[int64](1_234_567), TokensCached: ptr[int64](87_654_321), TokensCacheWrite: ptr[int64](9_876_543), Steps: ptr[int64](50), Status: "ok", StartedAt: t, Ms: ptr(3 * D)})
	// A one-letter prompt, linked to its run so the run page shows it.
	k.Message(web, "user", "p", promptAt, MsgExtra{RunID: &bigRun})
	toolStatuses := []string{"ok", "error", "denied", "pending"}
	toolClasses := []string{"read", "write", "destructive"}
	for i := int64(0); i < 30; i++ {
		connector := "reminders"
		if i%2 != 0 {
			connector = "mcp-northwind-industries-holdings-internal-knowledge-base-prod"
		}
		tool := "reminders__set_reminder"
		switch i % 5 {
		case 0:
			tool = within(worstToolLong, L.toolName)
		case 1:
			tool = "x__y"
		}
		// A pasted blob in the arguments: one string with no place to wrap.
		var args any = obj("text", "bayar listrik", "when", "besok 09:00")
		if i == 0 {
			args = obj("query", worstPastedBlob, "filters", obj("department", "Platform Infrastructure", "url", worstLongURL), "limit", 1284)
		} else if i == 2 {
			args = nil
		}
		var preview *string
		if i == 0 {
			preview = ptr(within(fill(`{"id":"doc_9f8e7d6c5b4a","title":"`+worstDeck+`","url":"`+worstLongURL+`"},`, L.toolResult), L.toolResult))
		} else if i%4 != 3 {
			preview = ptr("Besok, Rabu 11 Nov, 09.00 WITA")
		}
		var ms *int64
		switch i {
		case 0:
			ms = ptr[int64](3_723_000)
		case 1:
			ms = ptr[int64](0)
		case 2:
		default:
			ms = ptr(85 + i*400)
		}
		k.Tool(Tool{RunID: bigRun, ConnectorID: &connector, Tool: tool, Class: at(toolClasses, i), Args: args, ResultPreview: preview, Status: at(toolStatuses, i), Ms: ms, CreatedAt: t + i})
	}
	k.Message(web, "assistant", worstMarkdownStress, next(), MsgExtra{RunID: &bigRun})
	k.Message(web, "user", worstLongURL+" "+worstPastedBlob, next())
	// A run that only called tools and said nothing.
	k.Message(web, "assistant", "", next())
	k.Message(web, "user", fill(worstParagraph, L.webMessage), next())
	// The model's reply is stored whole; only the channel send is split at 4,096.
	k.Message(web, "assistant", fill(worstParagraph+"\n\n", 12_000), next())
	k.Message(web, "user", "مرحبا، هل يمكنك تذكيري باجتماع الغد الساعة 9؟ (Q3 report, room B-12)", next())
	k.Message(web, "assistant", "بالتأكيد. سأذكّرك غدًا الساعة 09:00 (Asia/Riyadh) باجتماع **Q3 report** في الغرفة B-12.", next())
	// A run that sent files with send_media: the longest file name and caption,
	// a voice note with no caption, and a send that gave up.
	k.Message(web, "user", "kirim gambar inhaler pakai spacer, sama jadwal imunisasinya", next())
	mediaAt := next()
	mediaRun := k.Run(Run{ChatID: &web.ID, Trigger: "message", Status: "ok", StartedAt: mediaAt})
	k.SentMedia(web, mediaRun, []SentMedia{
		{Kind: "image", MimeType: "image/png", Filename: within(fill("inhaler-dengan-spacer-untuk-anak-cara-pakai-yang-benar-", L.attachmentFilename-4), L.attachmentFilename-4) + ".png", Caption: fill(worstParagraph, 1024), Status: "sent", Size: 9_876_543, At: mediaAt},
		{Kind: "voice", MimeType: "audio/ogg", Filename: "file.ogg", Status: "sent", Size: 48_213, At: mediaAt + 1},
		{Kind: "document", MimeType: "application/pdf", Filename: worstDeck, Caption: "Jadwal imunisasi", Status: "failed", Size: 16_777_000, Error: ptr("Call to 'sendDocument' failed! (413: Request Entity Too Large) " + worstLongURL), At: mediaAt + 2},
	})
	k.Message(web, "assistant", "Udah aku kirim gambarnya ya.", next(), MsgExtra{RunID: &mediaRun})
	k.Message(web, "user", `{"event":"deployment.succeeded","repository":"northwind-industries-holdings/platform-infrastructure","sha":"9f8e7d6c5b4a4c3d8e2f1a0b9c8d7e6f9f8e7d6c","url":"`+worstLongURL+`","actor":"`+worstEmail+`"}`, next(), MsgExtra{SenderID: ptr("hook:github-actions-deploy")})
	k.Message(web, "user", "🦊", next())
	var groceries []string
	for i := 0; i < 40; i++ {
		groceries = append(groceries, fmt.Sprintf("%d. beli %s", i+1, []string{"beras", "gula", "minyak goreng", "telur", "kopi"}[i%5]))
	}
	k.Message(web, "user", strings.Join(groceries, "\n"), next())
	k.Message(web, "assistant", "Đặng Thị Ngọc Hân nhắn: ก็ที่นี่ผู้ใหญ่ชี้ กตัญญูรู้คุณ. Ừ, được rồi.", next())
	k.Message(web, "user", "ini belum dibalas", next(), MsgExtra{Status: "pending"})
	k.Message(web, "user", "ini diabaikan", next(), MsgExtra{Status: "ignored", IgnoreReason: ptr("not on the allowlist")})

	// Three approvals waiting at once, one of them already past its deadline.
	k.Approval(Approval{RunID: bigRun, ChatID: web.ID, Tool: within(worstToolLong, L.toolName), ToolClass: "destructive",
		ArgsPreview: ptr(within(fill(`{"ids":["`+worstPastedBlob[:120]+`"],"confirm":true,"url":"`+worstLongURL+`"}`, L.approvalArgs), L.approvalArgs)), ExpiresAt: now + 10*MIN, CreatedAt: now - 2*MIN})
	k.Approval(Approval{RunID: bigRun, ChatID: web.ID, Tool: "mcp-linear__delete_issue", ToolClass: "write", ArgsPreview: ptr(`{"id":"PAL-123"}`), ExpiresAt: now - 12*D, CreatedAt: now - 12*D - 10*MIN})
	k.Approval(Approval{RunID: bigRun, ChatID: web.ID, Tool: "x__y", ToolClass: "read", ExpiresAt: now + 45_000, CreatedAt: now - MIN})

	// --- jobs: listed oldest first, so the order here is the order on screen -----
	var jobOrder int64
	job := func(v Job) string {
		v.CreatedAt = ptr(now - D + jobOrder)
		jobOrder++
		return k.Job(v)
	}

	cappedJob := job(Job{
		Name:          within("daily_sales_recap_vorneo_banjarbaru_martapura_pelaihari_tanah_laut_q4_2026_final", L.jobName),
		Kind:          "job",
		RRule:         within(worstRRuleLong, L.rrule),
		TZ:            worstTZLong,
		Prompt:        "Recap.",
		ChatID:        family.ID,
		Status:        "running",
		NextRunAt:     ptr(now + 45_000),
		LastRunAt:     ptr(now - 5*MIN),
		LastStatus:    ptr("error"),
		LastResult:    ptr(within(fill(worstStackFirst+" while fetching "+worstLongURL+" ", L.jobResult-1)+"…", L.jobResult)),
		DailyTokenCap: ptr[int64](1000),
	})
	job(Job{Name: "J", Kind: "heartbeat", RRule: "FREQ=DAILY", TZ: "UTC", Prompt: "x", ChatID: jo.ID, Enabled: ptr(false), DailyTokenCap: ptr[int64](5_000_000)})
	briefing := job(Job{Name: within("Rekap penjualan harian Vorneo untuk Banjarbaru, Martapura, Pelaihari dan sekitar", L.jobName), Kind: "briefing", RRule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", TZ: "Asia/Makassar", Prompt: "Brief me.", ChatID: aleksandra.ID, NextRunAt: ptr(now + 10*H), LastRunAt: ptr(now - 14*H), LastStatus: ptr("ok"), LastResult: ptr("ok"), DailyTokenCap: ptr[int64](5_000_000)})
	// Ran, but the runtime never wrote a status.
	job(Job{Name: "تذكير يومي بتقرير المبيعات", RRule: "FREQ=WEEKLY;BYDAY=MO", TZ: "Asia/Riyadh", Prompt: "x", ChatID: arabic.ID, NextRunAt: ptr(now + 3*D), LastRunAt: ptr(now - 4*D)})
	// A status the dashboard has no tone for.
	job(Job{Name: "Budget watcher", RRule: "FREQ=HOURLY", TZ: "Asia/Makassar", Prompt: "x", ChatID: web.ID, NextRunAt: ptr(now + 20*MIN), LastRunAt: ptr(now - 40*MIN), LastStatus: ptr("budget_exceeded_daily_token_cap_reached"), LastResult: ptr("Stopped: the daily cap of 100,000,000 tokens was reached at 23:59.")})
	// Its chat was removed after the job was made.
	job(Job{Name: "Orphaned job", RRule: "FREQ=DAILY", TZ: "Asia/Makassar", Prompt: "x", ChatID: worstRemoved, NextRunAt: ptr(now + 2*H), LastRunAt: ptr(now - 22*H), LastStatus: ptr("ok"), LastResult: ptr("**Markdown** in a result\nwith a second line and a `code` span.")})
	// The runtime was down for 12 days: enabled, and long overdue.
	job(Job{Name: "Overdue", RRule: "FREQ=DAILY", TZ: "Asia/Makassar", Prompt: "x", ChatID: web.ID, NextRunAt: ptr(now - 12*D), LastRunAt: ptr(now - 13*D), LastStatus: ptr("aborted"), LastResult: ptr("")})
	job(Job{Name: "Tax filing reminder", RRule: "FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=31;BYHOUR=23;BYMINUTE=30", TZ: "America/Los_Angeles", Prompt: "x", ChatID: web.ID, NextRunAt: ptr(utcMs(2030, 1, 1, 7, 30)), LastRunAt: ptr(utcMs(2023, 1, 1, 7, 30)), LastStatus: ptr("ok"), LastResult: ptr("Done.")})
	job(Job{Name: "🔥🔥🔥", RRule: "FREQ=DAILY", TZ: "Asia/Makassar", Prompt: "x", ChatID: fox.ID, NextRunAt: ptr(now + H)})
	job(Job{Name: "Daily briefing", Kind: "briefing", RRule: "FREQ=DAILY;BYHOUR=7", TZ: "Asia/Makassar", Prompt: "x", ChatID: web.ID, NextRunAt: ptr(now + 10*H)})
	job(Job{Name: "Daily briefing", Kind: "briefing", RRule: "FREQ=DAILY;BYHOUR=7", TZ: "Asia/Makassar", Prompt: "x", ChatID: web.ID, NextRunAt: ptr(now + 10*H)})
	job(Job{Name: "<script>alert(1)</script>", RRule: "FREQ=DAILY", TZ: "Asia/Makassar", Prompt: "x", ChatID: script.ID, NextRunAt: ptr(now + H)})
	job(Job{Name: "Đặng Thị Ngọc Hân nhắc việc", RRule: "FREQ=DAILY", TZ: "Asia/Ho_Chi_Minh", Prompt: "x", ChatID: vietnamese.ID, NextRunAt: ptr(now + H), LastRunAt: ptr[int64](0), LastStatus: ptr("ok"), LastResult: ptr("ok")})
	job(Job{Name: "Benachrichtigungseinstellungen-Synchronisierung", Kind: "heartbeat", RRule: "FREQ=MINUTELY;INTERVAL=30", TZ: "Europe/Berlin", Prompt: "x", ChatID: web.ID, NextRunAt: ptr(now + 30*MIN), LastRunAt: ptr(now - 30*MIN), LastStatus: ptr("running")})

	// --- flows (RFC flows): the step and trigger caps, every run status, RTL ---
	worstFlows(k, family)

	// --- runs: every status and trigger, the extremes of every number ------------
	// 142% of a 1,000 token cap, today.
	k.Run(Run{ChatID: &family.ID, JobID: &cappedJob, Trigger: "schedule", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](1300), TokensOut: ptr[int64](120), Status: "budget", Error: ptr("Daily token cap reached for this job (1,420 of 1,000)."), StartedAt: now - 4*MIN, Ms: ptr[int64](59_999)})
	k.Run(Run{ChatID: &aleksandra.ID, Trigger: "message", Status: "running", StartedAt: now - 5000})
	k.Run(Run{ChatID: &web.ID, Trigger: "approval", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](12), TokensOut: ptr[int64](1), Tainted: ptr(true), Status: "awaiting_approval", StartedAt: now - 2*MIN, Ms: ptr[int64](0)})
	k.Run(Run{ChatID: &noTitleGroup.ID, Trigger: "message", Model: ptr(within(worstModelLong, L.model)), TokensIn: ptr[int64](1_284_000), TokensOut: ptr[int64](99_999), Tainted: ptr(true), Status: "error", Error: ptr(worstStack + "\n    caused by: 502 Bad Gateway from " + worstLongURL), StartedAt: now - 6*MIN, Ms: ptr[int64](999)})
	// No chat and no job: a webhook that never matched one.
	k.Run(Run{Trigger: "hook", Model: ptr("o"), TokensIn: ptr[int64](0), TokensOut: ptr[int64](0), Status: "aborted", Error: ptr(""), StartedAt: now - 7*MIN, Ms: ptr[int64](1000)})
	k.Run(Run{ChatID: &arabic.ID, Trigger: "reminder", Model: ptr("claude-haiku-4-5-20251001"), TokensIn: ptr[int64](999), TokensOut: ptr[int64](1), Status: "ok", StartedAt: now - 8*MIN, Ms: ptr[int64](9_999)})
	// The job and the chat were deleted later: only their ids are left.
	k.Run(Run{JobID: ptr("job_0mhremoved0000000000"), Trigger: "schedule", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](21_000), TokensOut: ptr[int64](1400), Status: "ok", ResultPreview: ptr(within(fill(worstParagraph, L.jobResult), L.jobResult)), StartedAt: now - 9*MIN, Ms: ptr[int64](15_000)})
	k.Run(Run{ChatID: ptr(worstRemoved), Trigger: "message", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](4000), TokensOut: ptr[int64](300), Status: "ok", StartedAt: now - 10*MIN, Ms: ptr[int64](60_000)})
	k.Run(Run{ChatID: &jo.ID, JobID: &briefing, Trigger: "schedule", Model: ptr("claude-sonnet-4-6"), TokensIn: ptr[int64](1), TokensOut: ptr[int64](0), Status: "ok", StartedAt: now + 2*H, Ms: ptr[int64](86_400_000)})
	k.Run(Run{ChatID: &priya.ID, Trigger: "message", Model: ptr("gpt-4-0613"), TokensIn: ptr[int64](8000), TokensOut: ptr[int64](2000), Status: "ok", StartedAt: now - 3*365*D, Ms: ptr[int64](4200)})
	// The last two weeks for the usage chart: one day a hundred times the others,
	// a few days with nothing, and more models than the breakdown shows.
	usageChats := []string{aleksandra.ID, family.ID, cjk.ID, christopher.ID}
	// Every agent kind for the per-agent breakdown: the longest name, a short one, a deleted one, and none (runs from before agents).
	usageAgents := []*string{&arisan, &cs, ptr("agt_deleted"), nil}
	for day := int64(1); day < 14; day++ {
		if day == 4 || day == 5 || day == 11 {
			continue
		}
		for i := int64(0); i < 3; i++ {
			in, out := 3000+day*700+i*200, 200+i*40
			if day == 2 {
				in, out = 40_000_000, 400_000
			}
			chat := at(usageChats, day+i)
			k.Run(Run{ChatID: &chat, AgentID: at(usageAgents, day+i), Trigger: "message", Model: ptr(fmt.Sprintf("model-%02d-preview", (day*3+i)%26)),
				TokensIn: &in, TokensOut: &out, Status: "ok", StartedAt: now - day*D - i*H, Ms: ptr(2000 + i*300)})
		}
	}

	// --- reminders ----------------------------------------------------------------
	remind := k.Reminder
	// Upcoming, soonest first. The first ones are already overdue.
	remind(Reminder{ChatID: family.ID, Text: within(fill("Kirim laporan mingguan ke "+worstEmail+" lewat "+worstLongURL+" lalu konfirmasi ke grup. ", L.reminderText), L.reminderText), TZ: worstTZLong, FireAt: now - 12*D, Status: "pending", SmartPrompt: ptr("Summarize the week first.")})
	remind(Reminder{ChatID: jo.ID, Text: "x", TZ: "UTC", FireAt: now - 30_000, Status: "firing"})
	remind(Reminder{ChatID: worstRemoved, Text: "Bayar listrik\nBayar air\nBayar internet", TZ: "Asia/Makassar", FireAt: now + 40_000, Status: "pending"})
	remind(Reminder{ChatID: arabic.ID, Text: "تذكير: اجتماع الغد الساعة 9 (Q3 report)", TZ: "Asia/Riyadh", FireAt: now + 59*MIN, Status: "pending"})
	remind(Reminder{ChatID: noTitleGroup.ID, Text: worstLongURL, TZ: "Asia/Makassar", FireAt: now + 5*H, Status: "pending"})
	for _, c := range []string{aleksandra.ID, cjk.ID, link.ID} {
		remind(Reminder{ChatID: c, Text: "Standup", TZ: "Asia/Makassar", FireAt: now + 20*H, Status: "pending"})
	}
	remind(Reminder{ChatID: script.ID, Text: "<script>alert(1)</script> &amp; **bold**", TZ: "Asia/Makassar", FireAt: now + 2*D, Status: "pending"})
	remind(Reminder{ChatID: fox.ID, Text: "🦊", TZ: "Asia/Makassar", FireAt: now + 3*D, Status: "pending", SmartPrompt: ptr("x")})
	remind(Reminder{ChatID: vietnamese.ID, Text: "Đặng Thị Ngọc Hân: gọi lại cho khách", TZ: "Asia/Ho_Chi_Minh", FireAt: now + 11*30*D, Status: "pending"})
	// New Year's Eve in Los Angeles is already January 1 in the owner's zone.
	remind(Reminder{ChatID: christopher.ID, Text: "File taxes", TZ: "America/Los_Angeles", FireAt: utcMs(2030, 1, 1, 7, 30), Status: "pending"})
	// Recurring.
	remind(Reminder{ChatID: family.ID, Text: within(fill(worstParagraph, L.reminderText), L.reminderText), RRule: ptr(within(worstRRuleLong, L.rrule)), TZ: worstTZLong, FireAt: now + 4*MIN, Status: "pending", SmartPrompt: ptr("x")})
	remind(Reminder{ChatID: jo.ID, Text: "y", RRule: ptr("FREQ=DAILY"), TZ: "UTC", FireAt: now + 6*H, Status: "pending"})
	remind(Reminder{ChatID: worstRemoved, Text: "Standup", RRule: ptr("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"), TZ: "Asia/Makassar", FireAt: now + 20*H, Status: "pending", LastFiredAt: ptr(now - 4*H)})
	remind(Reminder{ChatID: arabic.ID, Text: "صلاة الجمعة", RRule: ptr("FREQ=WEEKLY;BYDAY=FR;BYHOUR=11;BYMINUTE=45"), TZ: "Asia/Riyadh", FireAt: now + 2*D, Status: "firing"})
	remind(Reminder{ChatID: christopher.ID, Text: "Renew the domain", RRule: ptr("FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=31"), TZ: "America/Los_Angeles", FireAt: utcMs(2030, 1, 1, 7, 30), Status: "pending"})
	// Done and cancelled, newest first.
	remind(Reminder{ChatID: family.ID, Text: within(fill(worstParagraph, L.reminderText), L.reminderText), TZ: worstTZLong, FireAt: now - H, Status: "done", LastFiredAt: ptr(now - H), UpdatedAt: ptr(now - 1000)})
	remind(Reminder{ChatID: jo.ID, Text: "z", TZ: "UTC", FireAt: now - 2*H, Status: "cancelled", UpdatedAt: ptr(now - 2000)})
	remind(Reminder{ChatID: worstRemoved, Text: "Standup", RRule: ptr(within(worstRRuleLong, L.rrule)), TZ: "Asia/Makassar", FireAt: now - 3*D, Status: "cancelled", UpdatedAt: ptr(now - 3000)})
	remind(Reminder{ChatID: priya.ID, Text: "Old one", TZ: "Asia/Makassar", FireAt: now - 3*365*D, Status: "done", LastFiredAt: ptr(now - 3*365*D), UpdatedAt: ptr(now - 4000)})
	remind(Reminder{ChatID: sam.ID, Text: "No timestamp", TZ: "UTC", FireAt: 0, Status: "done", UpdatedAt: ptr(now - 5000)})

	// --- events: written last, so these are the first rows of the Activity list ---
	for _, e := range []Event{
		{At: now - 3*365*D, Level: "info", ConnectorID: ptr("telegram"), Message: "on"},
		{At: 0, Level: "warn", ConnectorID: ptr("whatsapp"), Message: "Clock was reset: this event has no timestamp"},
		{At: now - 20*MIN, Level: "info", ConnectorID: ptr("mcp-files"), Message: "مرحبا: تم الاتصال بالخادم (connected in 85 ms)"},
		{At: now - 19*MIN, Level: "debug", ConnectorID: ptr("cliproxy"), Message: "🦊"},
		{At: now - 18*MIN, Level: "info", ConnectorID: ptr("mcp-rnd"), Message: "<script>alert(1)</script> &amp; **bold**"},
		// A connector that was removed last week still has rows here.
		{At: now - 17*MIN, Level: "warn", ConnectorID: ptr("mcp-removed-last-week"), Kind: "remove", Message: "Removed, credentials deleted"},
		{At: now - 16*MIN, Level: "info", Message: ""},
		{At: now - 15*MIN, Level: "error", Kind: "system", Message: "Backup failed: ENOSPC: no space left on device, write '/data/backups/palui-predeploy-2026-11-11T04-05-13-123Z-9f8e7d6c5b4a4c3d8e2f1a0b9c8d7e6f.db.gz'"},
		{At: now - 14*MIN, Level: "info", ConnectorID: ptr("hooks"), Kind: "hook", Message: `{"event":"deployment.succeeded","repository":"northwind-industries-holdings/platform-infrastructure","sha":"9f8e7d6c5b4a4c3d8e2f1a0b9c8d7e6f9f8e7d6c","screenshot":"` + worstPastedBlob + `"}`},
		{At: now + D, Level: "info", ConnectorID: ptr("jobs"), Kind: "job", Message: "Scheduled ahead of time by a host with the wrong clock"},
		{At: now - 13*MIN, Level: "debug", ConnectorID: ptr("x"), Message: "y"},
		{At: now - 12*MIN, Level: "warn", ConnectorID: ptr("ai-gateway"), Kind: "status", Message: "paused: Paused by the daily token cap (100,000,000 of 100,000,000 used)"},
		{At: now - 11*MIN, Level: "error", ConnectorID: ptr("mcp-northwind-industries-holdings-internal-knowledge-base-prod"), Kind: "status", Message: "error: " + fill(worstStack+"\n", 2000)},
	} {
		k.Event(e)
	}

	// --- outbound: the longest name, note and text, every send status, a removed contact ---
	k.Setting("outbound", obj("enabled", true, "perContactPerDay", 50, "perDay", 200, "holdSeconds", 120, "signatureOn", true,
		"signature", within("—\n_Dikirim otomatis oleh asisten AI Muhammad Zakiyuddin Munziri. Balasan ke nomor ini tidak dibaca, silakan hubungi beliau langsung di nomornya ya_", 150),
		"pastSignatures", []any{}))
	longName := within("Muhammad Fadhlurrahman Al-Banjari bin H. Abdurrahman Siddiq", L.contactName)
	longContact := k.Chat("whatsapp", "6282200001001@s.whatsapp.net", longName, ChatOpts{Allowed: ptr(false)})
	k.Contact(longContact,
		Contact{Name: longName, Address: "6282200001001", Policy: "allow", Note: ptr(within(fill("Adik ipar, kerja di Banjarbaru, jangan chat sebelum jam 7 pagi. ", L.contactNote), L.contactNote))},
		[]Send{
			{Text: within(fill("Halo, ini asisten Zaki. "+worstParagraph+" ", L.outboundText), L.outboundText), Status: "pending", At: now - 5_000, Held: true, ApprovedBy: "policy"},
			{Text: "Zaki otw ya", Status: "sent", At: now - 20_000, ApprovedBy: "policy"},
			{Text: "Pengingat arisan besok jam 10", Status: "failed", At: now - 2*H, Error: ptr(within(fill(worstStackFirst+" ", 300), 300))},
			{Text: "Dibatalin", Status: "cancelled", At: now - 3*D, Error: ptr("cancelled by the owner")},
			{Text: "Zaki belum jawab check-in sholat Maghrib.", Status: "sending", At: now - 1000, ApprovedBy: "flow_version"},
		},
		[]Scheduled{
			{Text: within(fill("Kabar mingguan: ", L.outboundText), L.outboundText), FireAt: now + 3*D, RRule: ptr(within(worstRRuleLong, L.rrule))},
			{Text: "Jangan lupa jemput jam 8 ya", FireAt: now + 10*H},
		})
	k.Contact(arabic, Contact{Name: "نور الهدى عبد الرحمن", Address: "966500000000"}, []Send{{Text: "مرحبا، زكي في الطريق", Status: "sent", At: now - 31*D}}, nil)
	k.Contact(christopher, Contact{Name: "Christopher Alexander Montgomery III", Address: "1000000004", AgentID: arisan}, nil, []Scheduled{{Text: "Meeting moved to 3pm", FireAt: now + 2*365*D}})
	k.Contact(single, Contact{Name: "J", Address: "6281200000001"}, nil, nil)
	gone := k.Chat("whatsapp", "6281299999999@s.whatsapp.net", "Removed", ChatOpts{Allowed: ptr(false)})
	k.Contact(gone, Contact{Name: "Mantan kontak", Address: "6281299999999", Removed: true}, []Send{{Text: "Pesan lama ke kontak yang udah dihapus", Status: "sent", At: now - 12*D}}, nil)

	// --- settings lists -----------------------------------------------------------
	k.Passkey(Passkey{CreatedAtNull: true})
	k.Passkey(Passkey{Name: ptr("J"), DeviceType: "singleDevice", BackedUp: ptr(false), CreatedAt: ptr(now - 3*365*D)})
	k.Passkey(Passkey{Name: ptr("Aleksandra Wiśniewska-Kowalczyk's MacBook Pro 16-inch (Touch ID), synced through iCloud Keychain"), CreatedAt: ptr(now - 20_000)})
	k.Passkey(Passkey{Name: ptr("هاتف نور الهدى"), CreatedAt: ptr(now - 12*D)})
	k.Passkey(Passkey{Name: ptr("YubiKey-5C-NFC-FIPS-serial-0123456789-office-backup-key-do-not-lose"), DeviceType: "singleDevice", BackedUp: ptr(false), CreatedAt: ptr(now + 2*H)})
	k.Passkey(Passkey{Name: ptr("   "), CreatedAt: ptr[int64](0)})
	// MCP access: the 60-character name cap, a 100-character client name with a
	// long client URI, RTL, one letter, never used and used a second ago.
	k.MCPGrant(MCPGrant{Name: "Claude Code on the office build server in rack B, slot 12 v2", Hint: ptr("Zq9_"), CreatedAt: now - 400*D})
	k.MCPGrant(MCPGrant{Name: "J", Hint: ptr("-_-_"), CreatedAt: now - 20_000, LastUsedAt: ptr(now - 1_000)})
	k.MCPGrant(MCPGrant{Name: "وكيل الذكاء الاصطناعي في المكتب", Hint: ptr("a1B2"), CreatedAt: now - 3*D, LastUsedAt: ptr(now - 2*H)})
	k.MCPGrant(MCPGrant{Name: "Claude", Client: ptr("Claude"), ClientURI: ptr("https://claude.ai"), CreatedAt: now - 10*D, LastUsedAt: ptr(now - 5*MIN)})
	k.MCPGrant(MCPGrant{Name: "Workspace Automation Assistant for the Finance and Operations Team (staging environment, do not use)", Client: ptr("Workspace Automation Assistant for the Finance and Operations Team (staging environment, do not use)"),
		ClientURI: ptr("https://very-long-subdomain-for-an-internal-tool.automation.example.com/integrations/model-context-protocol/callback"), CreatedAt: now - 2*365*D})

	// New version notice on in two chats, plus one that was removed since.
	k.Setting("deployNoticeChats", []string{family.ID, arabic.ID, worstRemoved})

	k.Hook(Hook{Name: within("github-actions-deploy-notifications-northwind-industries-pro", L.hookName), ChatID: family.ID, Mode: "summarize", LastAt: ptr(now - 20_000), Slug: "9f8e7d6c5b4a4c3d"})
	k.Hook(Hook{Name: "J", ChatID: jo.ID})
	k.Hook(Hook{Name: "تنبيهات النشر", ChatID: arabic.ID, LastAt: ptr(now - 3*365*D)})
	// Its chat is gone, and it has been switched off.
	k.Hook(Hook{Name: "Sentry alerts for the Vorneo production project (payments)", ChatID: worstRemoved, Mode: "summarize", Enabled: ptr(false), LastAt: ptr[int64](0)})
	k.Hook(Hook{Name: "<b>Stripe</b> &amp; friends", ChatID: noTitleGroup.ID, LastAt: ptr(now + 2*H)})
	k.Hook(Hook{Name: "Uptime", ChatID: aleksandra.ID, LastAt: ptr(now - 12*D)})

	// --- saved attachments: the longest title, description and file name the tools allow ---
	filename := strx.Slice16(strings.Repeat("umowa_najmu_aneks_nr_3_podpisana_0123456789abcdefghijklmnopqrstuvwxyz_", 3), L.attachmentFilename-4) + ".pdf"
	longest := k.Attachment(Attachment{
		ChatID:      aleksandra.ID,
		Title:       within(fill("Umowa najmu mieszkania Wiśniewska-Kowalczyk, aneks nr 3 z podpisami obu stron i pieczęcią notariusza ", L.attachmentTitle), L.attachmentTitle),
		Description: within(fill(worstParagraph, L.attachmentDescription), L.attachmentDescription),
		Kind:        "pdf",
		Filename:    within(filename, L.attachmentFilename),
		Size:        ptr[int64](10 * 1024 * 1024),
		CreatedAt:   ptr(now - 20_000),
	})
	// Many live links: one about to expire, one opened a huge number of times, some made by the agent.
	k.Share(longest, Share{Visibility: "public", Duration: "1h", CreatedAt: ptr(now - 59*60_000)})
	k.Share(longest, Share{Visibility: "public", Duration: "7d", OpenCount: 1_234_567, CreatedBy: aleksandra.ID})
	for i := int64(0); i < 38; i++ {
		visibility := "public"
		if i%2 != 0 {
			visibility = "private"
		}
		k.Share(longest, Share{Visibility: visibility, Duration: "24h", OpenCount: i, CreatedAt: ptr(now - i*60_000)})
	}
	k.Attachment(Attachment{ChatID: arabic.ID, Title: "بطاقة الهوية", Description: "", Kind: "jpeg", Size: ptr[int64](1), CreatedAt: ptr(now - 3*365*D)})
	k.Attachment(Attachment{ChatID: worstRemoved, Title: "J", Kind: "jpeg", Size: ptr[int64](999_999), CreatedAt: ptr(now + 2*H)})
	// Auto-saved from a group: the longest sender name WhatsApp shows, a caption
	// at the description limit, a sender with no name, and one sent long before it was saved.
	k.Attachment(Attachment{
		ChatID:      noTitleGroup.ID,
		Title:       within(fill("Notulen rapat pengurus RT 07 Kelurahan Guntung Manggis tentang iuran keamanan ", 80), 80),
		Description: within(fill(worstParagraph, L.attachmentDescription), L.attachmentDescription),
		Kind:        "jpeg",
		Size:        ptr[int64](10 * 1024 * 1024),
		SenderID:    "628115550199@s.whatsapp.net",
		SenderName:  within("Sekretaris RT 07 Guntung Manggis 🏘️ (semua catatan rapat dikumpulkan di sini)", 80),
		SentAt:      ptr(now - 50_000),
		CreatedAt:   ptr(now - 40_000),
	})
	k.Attachment(Attachment{ChatID: noTitleGroup.ID, Title: "Dokumen", Kind: "pdf", Filename: worstDeck, Size: ptr[int64](2048), SenderID: "98765432101234@lid", SentAt: ptr(now - 400*D), CreatedAt: ptr(now - 2*D)})
	k.Attachment(Attachment{ChatID: cjk.ID, Title: "身份证 王秀英 正反面", Description: "身份证正反面照片，有效期至2036年。", Kind: "png", Filename: "身份证_王秀英.png", Size: ptr[int64](1536), CreatedAt: ptr(now - 12*D)})
}

// worstFlows is worst.ts's flows block: the step and trigger caps, every run
// status, RTL.
func worstFlows(k *Kit, flowChat db.Chat) {
	now := k.Now
	L := limits
	longName := within("Rekap-penjualan-harian-Vorneo-untuk-Banjarbaru-Martapura-dan-Hulu-Sungai-Selatan", L.flowName)
	bigWhen := `len(steps.deals.data.items) > 3 and not empty(steps.deals.data.items) and steps.deals.data.items.0.stage == "masuk" or daysUntil(steps.deals.data.items.0.due) <= 3`
	steps := func(lastText string) []any {
		var out []any
		for i := 0; i < L.flowSteps; i++ {
			switch i {
			case 0:
				out = append(out, obj("id", "deals", "kind", "tool", "tool", worstToolLong, "args", obj("department", "penjualan-dan-pemasaran-wilayah-kalimantan-selatan", "week", "{{now.date}}", "url", worstLongURL)))
			case 1:
				out = append(out, obj("id", "big_enough", "kind", "if", "when", bigWhen, "then", "s2", "else", nil))
			default:
				text := fill(fmt.Sprintf("Langkah %d: %s", i, worstParagraph), 400)
				if i == 39 && lastText != "" {
					text = lastText
				}
				st := obj("id", fmt.Sprintf("s%d", i), "kind", "message", "text", text)
				if i == 2 {
					st.Set("name", "Kirim ringkasan ke grup sebelum rapat Senin pagi, semua angka sudah dicek ulang")
				}
				out = append(out, st)
			}
		}
		return out
	}
	triggers := func() []any {
		var times []any
		for i := 0; i < 12; i++ {
			times = append(times, obj("time", fmt.Sprintf("%02d:15", 6+i), "label", fmt.Sprintf("Cek %d", i+1)))
		}
		out := []any{
			obj("id", "main", "kind", "schedule", "start", "deals", "source", obj("kind", "times", "times", times)),
			obj("id", "weekly", "kind", "schedule", "start", "deals", "source", obj("kind", "rrule", "rrule", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7,12,17;BYMINUTE=0,30", "start", "2026-11-02T07:00")),
		}
		for i := 0; i < L.flowTriggers-2; i++ {
			out = append(out, obj("id", fmt.Sprintf("cmd%d", i), "kind", "chat", "command", fmt.Sprintf("/rekap_penjualan_%d_banjarbaru", i), "start", "deals"))
		}
		return out
	}
	var notified []any
	for i := 0; i < 40; i++ {
		notified = append(notified, fmt.Sprintf("pr-%d", 1000+i))
	}
	big := k.Flow(flowChat,
		obj("name", longName, "description", within(fill("Ambil deal Vorneo, cek angka, kirim ringkasan ke grup keluarga dan tim. ", L.flowDescription), L.flowDescription), "triggers", triggers(), "steps", steps("")),
		FlowOpts{
			Enabled:   ptr(false),
			Failures:  5,
			LastError: ptr(within(fill("Step deals: "+worstStackFirst+" ", L.flowError), L.flowError)),
			Versions: []FlowVersion{
				{Definition: obj("name", longName, "triggers", triggers(), "steps", steps("Selesai.")), Note: ptr(within(fill("Ganti teks penutup biar lebih singkat. ", 200), 200))},
				{Definition: obj("name", longName, "triggers", triggers(), "steps", steps("")), CreatedBy: "owner:dashboard"},
			},
			State: obj("last_paid", "Listrik 2026-11-12", "streak", 12, "notified", notified),
		})
	var items []any
	for i := 0; i < 200; i++ {
		items = append(items, obj("id", fmt.Sprintf("deal_%d", i), "title", worstParagraph[:80], "stage", "masuk"))
	}
	bigIo := obj("ok", true, "data", obj("items", items))
	threeTexts := []FlowStep{}
	for i := 0; i < 3; i++ {
		threeTexts = append(threeTexts, FlowStep{StepID: fmt.Sprintf("s%d", i+2), Kind: "message", Status: "ok", Output: obj("text", fill(worstParagraph, 400))})
	}
	k.FlowRun(big, FlowRun{
		Status:      "failed",
		TriggerID:   ptr("main"),
		StartedAt:   now - 20*MIN,
		ScheduledAt: ptr(now - 20*MIN),
		Taint:       []string{"steps.deals", "trigger"},
		Trigger:     obj("kind", "schedule", "label", "Cek 1", "time", "06.15", "payload", worstPastedBlob),
		Error:       ptr(within(fill("Step s5: "+worstStack+" ", L.flowError), L.flowError)),
		Steps: append(append([]FlowStep{
			{StepID: "deals", Kind: "tool", Status: "error", Error: ptr("ECONNRESET"), Attempt: 1, Ms: ptr[int64](20_000)},
			{StepID: "deals", Kind: "tool", Status: "ok", Tainted: true, Attempt: 3, Input: obj("tool", worstToolLong, "args", obj("url", worstLongURL)), Output: obj("$truncated", true, "preview", strx.Slice16(str(bigIo), 4096)), Ms: ptr[int64](59_400)},
			{StepID: "big_enough", Kind: "if", Status: "ok", Branch: ptr("then"), Input: obj("when", bigWhen, "explain", `len(steps.deals.data.items) = 200, steps.deals.data.items.0.stage = "masuk"`), Output: obj("result", true)},
		}, threeTexts...), FlowStep{StepID: "s5", Kind: "message", Status: "error", Error: ptr("More than 5 messages in one run.")}),
	})
	k.FlowRun(big, FlowRun{Status: "skipped", SkipReason: ptr("late"), TriggerID: ptr("main"), StartedAt: now - 3*H, ScheduledAt: ptr(now - 6*H)})
	k.FlowRun(big, FlowRun{Status: "awaiting_approval", SkipReason: ptr("not_approved"), TriggerID: ptr("cmd0"), TriggerKind: "chat", StartedAt: now - 2*H, Steps: []FlowStep{{StepID: "deals", Kind: "tool", Status: "waiting", Error: ptr("This version isn't approved yet.")}}})

	noteBody := fill(worstParagraph, 1500)
	rtl := k.Flow(flowChat,
		obj("name", "تذكير الاجتماع الأسبوعي للفريق",
			"triggers", []any{obj("id", "main", "kind", "schedule", "start", "note", "source", obj("kind", "times", "times", []any{obj("time", "09:00")}))},
			"steps", []any{obj("id", "note", "kind", "tool", "tool", "mcp-notes__save_note_with_a_very_long_tool_name", "args", obj("title", "{{now.date}}", "body", noteBody))}),
		FlowOpts{PausedUntil: ptr(now + 3*D), Enabled: ptr(false), UnapprovedWrites: []any{obj("stepId", "note", "kind", "tool", "connectorId", "mcp-northwind-industries-holdings-internal-knowledge-base-prod", "tool", "save_note_with_a_very_long_tool_name", "class", "write", "args", obj("title", "{{now.date}}", "body", noteBody))}},
	)
	k.FlowRun(rtl, FlowRun{Status: "cancelled", TriggerID: ptr("main"), StartedAt: now - D, Error: ptr("The owner didn't approve this version.")})

	bills := k.Flow(flowChat, js(`{"name":"Tagihan","triggers":[{"id":"daily","kind":"schedule","start":"bills","source":{"kind":"times","times":[{"time":"09:00","label":"Cek tagihan"}]}}],"steps":[{"id":"bills","kind":"tool","tool":"mcp-keuangan__list_bills","args":{"status":"unpaid"}},{"id":"per_bill","kind":"each","items":"steps.bills.data.items","where":"daysUntil(item.due) <= 3","key":"{{item.id}}:{{item.due}}","max":50,"body":"ask","next":null},{"id":"ask","kind":"checkin","text":"Tagihan {{item.name | truncate(40)}} jatuh tempo {{item.due | date}}. Udah bayar?","deadline":{"kind":"after","minutes":4320},"next":null}]}`), FlowOpts{})
	parent := k.FlowRun(bills, FlowRun{Status: "done", TriggerID: ptr("daily"), StartedAt: now - 30*MIN, Steps: []FlowStep{
		{StepID: "bills", Kind: "tool", Status: "ok"},
		{StepID: "per_bill", Kind: "each", Status: "ok", Error: ptr("30 item(s) over max 50 were dropped."), Output: js(`{"count":80,"started":50,"deduped":0,"dropped":30}`)},
	}})
	for i := int64(0); i < 20; i++ {
		v := FlowRun{Status: "done", TriggerID: ptr("daily"), ParentRunID: &parent, ItemIndex: ptr(i), StartedAt: now - 30*MIN, Trigger: obj("label", fmt.Sprintf("Tagihan %d", i+1))}
		step := FlowStep{StepID: "ask", Kind: "checkin", Status: "ok", Branch: ptr("next")}
		if i%3 != 0 {
			v.Status = "waiting"
			v.Wait = obj("kind", "checkin", "stepId", "ask", "followUps", 2, "nextFollowUpAt", now+H, "deadlineAt", now+3*D, "answers", []any{}, "askedAt", now-30*MIN)
			v.WakeAt = ptr(now + H)
			step.Status, step.Branch = "waiting", nil
		}
		v.Steps = []FlowStep{step}
		k.FlowRun(bills, v)
	}
}
