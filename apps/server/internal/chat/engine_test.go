package chat_test

// Ports engine.test.ts (part 1: router, commands, queue, turn).

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func listEvents(t *testing.T, q db.Q) []db.Event {
	return need(events.List(q, events.ListOptions{Limit: 500}))
}

func waMsg(chatID, sender, text string, mod ...func(m *connectors.InboundMessage)) connectors.InboundMessage {
	m := connectors.InboundMessage{Channel: "whatsapp", ConnectorID: "whatsapp", ChatKind: "dm", ChatExternalID: chatID, SenderExternalID: sender, Text: text}
	for _, f := range mod {
		f(&m)
	}
	return m
}

func TestRouterOwnership(t *testing.T) {
	t.Run("drops non-owners and fromMe without storing them, with a reason event", func(t *testing.T) {
		h := basic(t, nil, extra{})
		if r := h.send("halo", func(m *connectors.InboundMessage) { m.SenderExternalID, m.ChatExternalID = "99", "99" }); r != nil {
			t.Fatal(r)
		}
		if r := h.send("halo", func(m *connectors.InboundMessage) { m.FromMe = true }); r != nil {
			t.Fatal(r)
		}
		eq(t, count(t, h.DB, "messages"), 0)
		var reasons []string
		for _, e := range listEvents(t, h.DB) {
			if e.Kind == "router.ignore" {
				reasons = append(reasons, e.Message)
			}
		}
		contains(t, strings.Join(reasons, "|"), "not the owner")
		contains(t, strings.Join(reasons, "|"), "fromMe")
	})

	t.Run("accepts the WhatsApp owner by phone JID, learns the LID, then accepts LID-only messages", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.ModelChain(t, d, "cliproxy")
		need(owner.SetWhatsAppOwner(d, "08123456789", 1))
		wa := conntest.NewFakeChannel("whatsapp")
		m := models.NewScripted(models.Say("hai"), models.Say("hai lagi"))
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", m), "whatsapp": conntest.ChannelRuntime("whatsapp", wa)}, Rows: []chattest.Row{{"cliproxy", "model"}, {"whatsapp", "channel"}}})
		if r := need(h.Engine.Inbound(waMsg("777@lid", "777@lid", "halo"))); r != nil {
			t.Fatal(r)
		}
		if r := need(h.Engine.Inbound(waMsg("555@lid", "555@lid", "halo", func(m *connectors.InboundMessage) { m.SenderAltID = "628123456789@s.whatsapp.net" }))); r == nil {
			t.Fatal("dropped")
		}
		id, _, _ := owner.GetOwner(d, "whatsapp")
		eq(t, *id.AltID, "555@lid")
		if r := need(h.Engine.Inbound(waMsg("555@lid", "555@lid", "halo"))); r == nil {
			t.Fatal("dropped")
		}
		h.Engine.Idle()
		if len(m.Prompts) < 1 {
			t.Fatal("no prompt")
		}
	})

	t.Run("reopens a WhatsApp DM that was left out of the allowlist", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.ModelChain(t, d, "cliproxy")
		need(owner.SetWhatsAppOwner(d, "08123456789", 1))
		no := false
		need(chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "628123456789@s.whatsapp.net", Kind: "dm", Allowed: &no}, 1))
		m := models.NewScripted(models.Say("halo"))
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", m), "whatsapp": conntest.ChannelRuntime("whatsapp", conntest.NewFakeChannel("whatsapp"))}, Rows: []chattest.Row{{"cliproxy", "model"}, {"whatsapp", "channel"}}})
		r := need(h.Engine.Inbound(waMsg("628123456789@s.whatsapp.net", "628123456789@s.whatsapp.net", "halo")))
		h.Engine.Idle()
		eq(t, messageByID(t, d, r.MessageID).Status, "handled")
	})

	t.Run("keeps a new WhatsApp group off until the owner picks an agent from inside it", func(t *testing.T) {
		d := testutil.DB(t)
		need(owner.SetWhatsAppOwner(d, "08123456789", 1))
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"whatsapp": conntest.ChannelRuntime("whatsapp", conntest.NewFakeChannel("whatsapp"))}, Rows: []chattest.Row{{"whatsapp", "channel"}}})
		own := func(text string, addressed bool) connectors.InboundMessage {
			return waMsg("123@g.us", "628123456789@s.whatsapp.net", text, func(m *connectors.InboundMessage) { m.ChatKind, m.Addressed = "group", addressed })
		}
		if r := need(h.Engine.Inbound(own("halo", false))); r != nil {
			t.Fatal(r)
		}
		r := need(h.Engine.Inbound(own("halo @Palui", true)))
		h.Engine.Idle()
		eq(t, messageByID(t, d, r.MessageID).Status, "handled")
		// The group only hears that no agent is set; the choices go to the owner's DM.
		rows := outboxRows(t, d)
		toDm, toGroup := rows[len(rows)-2], rows[len(rows)-1]
		eq(t, toGroup.Payload.Text, "Aku belum di-setup di grup ini, belum ada agent yang dipilih.\nPilihan agent-nya udah aku kirim ke chat pribadi kamu.")
		notContains(t, toGroup.Payload.Text, "/agent palui")
		dm := found(chats.Get(d, toDm.ChatID))
		eq(t, dm.Kind+" "+dm.ExternalID, "dm 628123456789@s.whatsapp.net")
		contains(t, toDm.Payload.Text, "• /agent palui: Palui, cuma jawab kamu")
		eq(t, chatWhere(t, d, "external_id = ?", "123@g.us").Allowed, false)
		sent := count(t, d, "outbox")
		for _, text := range []string{"/status", "/Users/me/notes.txt"} {
			m := need(h.Engine.Inbound(own(text, false)))
			h.Engine.Idle()
			eq(t, messageByID(t, d, m.MessageID).Status, "ignored")
		}
		eq(t, count(t, d, "outbox"), sent)
		need(h.Engine.Inbound(own("/agent palui", false)))
		h.Engine.Idle()
		g := chatWhere(t, d, "external_id = ?", "123@g.us")
		if !g.Allowed || g.AgentID != nil {
			t.Fatalf("%+v", g)
		}
		eq(t, lastText(t, d), "Oke, Palui yang jawab di sini, cuma buat kamu.")
	})

	t.Run("never answers a guest or a button in a WhatsApp group that's off", func(t *testing.T) {
		d := testutil.DB(t)
		need(owner.SetWhatsAppOwner(d, "08123456789", 1))
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"whatsapp": conntest.ChannelRuntime("whatsapp", conntest.NewFakeChannel("whatsapp"))}, Rows: []chattest.Row{{"whatsapp", "channel"}}})
		no := false
		c := need(chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "123@g.us", Kind: "group", Allowed: &no}, 1))
		r := need(h.Engine.Inbound(waMsg("123@g.us", "628123456789@s.whatsapp.net", "", func(m *connectors.InboundMessage) {
			m.ChatKind = "group"
			m.Callback = &connectors.InboundCallback{Data: "approve:apr_x"}
		})))
		h.Engine.Idle()
		row := messageByID(t, d, r.MessageID)
		eq(t, row.ChatID, c.ID)
		eq(t, row.Status, "ignored")
		contains(t, *row.IgnoreReason, "allowlist")
		if r := need(h.Engine.Inbound(waMsg("123@g.us", "62899@s.whatsapp.net", "halo", func(m *connectors.InboundMessage) { m.ChatKind, m.Addressed = "group", true }))); r != nil {
			t.Fatal(r)
		}
	})
}

func TestCommands(t *testing.T) {
	t.Run("/status works while the model is off and the model-off text is fixed", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.OwnTelegram(t, d)
		chattest.ModelChain(t, d, "cliproxy")
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}, Rows: []chattest.Row{{"telegram", "channel"}}})
		need(h.Engine.Inbound(tgMsg("tolong cek cuaca")))
		need(h.Engine.Inbound(tgMsg("/status")))
		h.Engine.Idle()
		texts := outboxTexts(t, d)
		if !has(texts, chat.ModelOffText) {
			t.Fatal(texts)
		}
		for _, x := range texts {
			if strings.HasPrefix(x, "Model:") {
				contains(t, x, "cliproxy/m (off)")
			}
		}
	})

	t.Run("/status shows the daily token cap, or tanpa batas when it is 0", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.OwnTelegram(t, d)
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))}, Rows: []chattest.Row{{"telegram", "channel"}}})
		tokenLine := func() string {
			for _, l := range strings.Split(lastText(t, d), "\n") {
				if strings.HasPrefix(l, "Token hari ini:") {
					return l
				}
			}
			return ""
		}
		need(h.Engine.Inbound(tgMsg("/status")))
		h.Engine.Idle()
		eq(t, tokenLine(), "Token hari ini: 0 / 2.000.000")
		setBudget(t, d, func(b *settings.Budgets) { b.DailyTokenCap = 0 })
		need(h.Engine.Inbound(tgMsg("/status")))
		h.Engine.Idle()
		eq(t, tokenLine(), "Token hari ini: 0 / tanpa batas")
	})

	t.Run("/new auto-saves then rotates the session", func(t *testing.T) {
		var saved []connectors.SaveInput
		mem := &connectors.MemoryHandle{AutoSave: true, Save: func(ctx context.Context, in connectors.SaveInput) error { saved = append(saved, in); return nil }}
		h := basic(t, []models.Step{models.Say("halo juga")}, extra{registry: map[string]*connectors.Runtime{"recall": conntest.MemoryRuntime(mem)}, rows: []chattest.Row{{"recall", "memory"}}})
		h.sendIdle("halo palui")
		c := firstChat(t, h.DB)
		h.sendIdle("/new")
		after := found(chats.Get(h.DB, c.ID))
		if *after.SessionID == *c.SessionID {
			t.Fatal("same session")
		}
		eq(t, len(saved), 1)
		eq(t, saved[0].Content, "Owner: halo palui\nPalui: halo juga")
		if !regexp.MustCompile("^" + *c.SessionID + ":msg_.+-msg_.+$").MatchString(saved[0].SourceID) {
			t.Fatal(saved[0].SourceID)
		}
	})

	t.Run("/stop aborts the running turn", func(t *testing.T) {
		gate := chattest.NewGate()
		h := basic(t, []models.Step{gate.After(models.Say("telat"))}, extra{})
		h.send("tulis esai panjang")
		chattest.Wait(20 * time.Millisecond)
		h.send("/stop")
		chattest.Wait(20 * time.Millisecond)
		gate.Release()
		h.Engine.Idle()
		eq(t, allRuns(t, h.DB)[0].Status, "aborted")
		if !has(outboxTexts(t, h.DB), "Oke, aku stop.") {
			t.Fatal(outboxTexts(t, h.DB))
		}
	})

	t.Run("/connectors lists with Telegram buttons and turns connectors on and off", func(t *testing.T) {
		h := basic(t, nil, extra{registry: map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools())}, rows: []chattest.Row{{"alpha", "tools"}}})
		h.sendIdle("/connectors")
		rows := outboxRows(t, h.DB)
		list := rows[len(rows)-1]
		contains(t, list.Payload.Text, "alpha: nyala")
		hit := false
		for _, a := range list.Payload.Actions {
			if a.ID == "connector:off:alpha" && a.Label == "Matikan alpha" {
				hit = true
			}
		}
		eq(t, hit, true)
		h.sendIdle("", func(m *connectors.InboundMessage) {
			m.Callback = &connectors.InboundCallback{Data: "connector:off:alpha"}
		})
		row := found(connectors.Get(h.DB, "alpha"))
		if row.Enabled || row.Status != "paused" {
			t.Fatalf("%+v", row)
		}
		h.sendIdle("/connectors on alpha")
		row = found(connectors.Get(h.DB, "alpha"))
		if !row.Enabled || row.Status != "on" {
			t.Fatalf("%+v", row)
		}
	})

	t.Run("/connectors off <channel> from that channel needs another way back", func(t *testing.T) {
		h := basic(t, nil, extra{})
		h.sendIdle("/connectors off telegram")
		contains(t, lastText(t, h.DB), "Gak bisa matiin Telegram dari sini")
		eq(t, found(connectors.Get(h.DB, "telegram")).Enabled, true)
		// A passkey makes the dashboard a way back.
		insert(t, h.DB, "user", kv{"id", "u1", "name", "Zaki", "email", "z@x", "email_verified", 0, "created_at", 1, "updated_at", 1})
		insert(t, h.DB, "passkey", kv{"id", "p1", "public_key", "k", "user_id", "u1", "credential_id", "c1", "counter", 0, "device_type", "singleDevice", "backed_up", 0})
		h.sendIdle("/connectors off telegram")
		contains(t, lastText(t, h.DB), "telegram dimatikan. Buat nyalain lagi: nyalakan lagi dari dashboard")
		eq(t, found(connectors.Get(h.DB, "telegram")).Enabled, false)
	})

	t.Run("/report sends a paste-ready snapshot of the chat without reaching the model", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("halo juga")}, extra{})
		h.sendIdle("halo palui")
		c := firstChat(t, h.DB)
		run := allRuns(t, h.DB)[0]
		h.sendIdle("/report reminder gak kekirim")
		rows := outboxRows(t, h.DB)
		sent := rows[len(rows)-1]
		eq(t, sent.Payload.Format, "plain")
		report := sent.Payload.Text
		saved := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports")))
		eq(t, saved.ChatID, c.ID)
		eq(t, *saved.SessionID, *c.SessionID)
		eq(t, saved.Note, "reminder gak kekirim")
		eq(t, saved.Text, report)
		if !strings.HasPrefix(report, "Palui debug report "+saved.ID+".") || !strings.HasPrefix(saved.ID, "rpt_") {
			t.Fatal(report)
		}
		contains(t, report, "Owner says: reminder gak kekirim")
		contains(t, report, fmt.Sprintf("chat: %s (telegram, dm", c.ID))
		contains(t, report, "session: "+*c.SessionID)
		contains(t, report, "/app/chats?chat="+c.ID)
		contains(t, report, fmt.Sprintf("• %s ok ", run.ID))
		contains(t, report, "owner run=")
		contains(t, report, ": halo palui")
		contains(t, report, "bot run=")
		notContains(t, report, ": /report")
		eq(t, len(allRuns(t, h.DB)), 1)
	})

	t.Run("/report sent as a reply names the message the owner pointed at", func(t *testing.T) {
		h := basic(t, nil, extra{})
		h.sendIdle("/report", func(m *connectors.InboundMessage) {
			m.Quoted = &connectors.InboundQuote{From: "self", Text: "Aku nggak bisa bikin gambar.\nPencariannya kelamaan"}
		})
		saved := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports")))
		contains(t, saved.Text, "Owner replied to the bot's reply with this report, so that's most likely what went wrong: Aku nggak bisa bikin gambar. Pencariannya kelamaan\n")
		notContains(t, saved.Text, "Owner says:")

		h.sendIdle("/report")
		latest := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports ORDER BY created_at DESC, rowid DESC LIMIT 1")))
		notContains(t, latest.Text, "Owner replied to")
	})

	t.Run("/report without a note asks for one next time, after the report", func(t *testing.T) {
		h := basic(t, nil, extra{})
		h.sendIdle("/report")
		saved := need(db.ScanReport(h.DB.QueryRow("SELECT " + db.ReportCols + " FROM reports")))
		eq(t, saved.Note, "")
		notContains(t, saved.Text, "Owner says:")
		texts := outboxTexts(t, h.DB)
		eq(t, texts[0], saved.Text)
		eq(t, texts[1], "Lain kali tulis juga apa yang salah, biar lebih gampang dilacak: /report <apa yang salah>")
	})

	t.Run("/connectors off telegram is allowed when WhatsApp is reachable, and says how to undo", func(t *testing.T) {
		wa := conntest.NewFakeChannel("whatsapp")
		h := basic(t, nil, extra{registry: map[string]*connectors.Runtime{"whatsapp": conntest.ChannelRuntime("whatsapp", wa)}, rows: []chattest.Row{{"whatsapp", "channel"}}})
		h.sendIdle("/connectors off telegram")
		eq(t, lastText(t, h.DB), "telegram dimatikan. Buat nyalain lagi: kirim `/connectors on telegram` dari WhatsApp.")
		eq(t, found(connectors.Get(h.DB, "telegram")).Enabled, false)
	})
}

func TestQueueBatchesMidTurnMessages(t *testing.T) {
	gate := chattest.NewGate()
	h := basic(t, []models.Step{gate.After(models.Say("satu")), models.Say("dua")}, extra{})
	h.send("pesan pertama")
	chattest.Wait(20 * time.Millisecond)
	h.send("pesan kedua")
	h.send("pesan ketiga")
	gate.Release()
	h.Engine.Idle()
	eq(t, len(h.m.Prompts), 2)
	second := prompt(h.m, 1)
	contains(t, second, "pesan kedua")
	contains(t, second, "pesan ketiga")
	eq(t, len(allRuns(t, h.DB)), 2)
	jsonEq(t, outboxTexts(t, h.DB), `["satu","dua"]`)
}

func addTool(calls *int) connectors.PaluiTool {
	return connectors.PaluiTool{Class: "read", Tool: conntest.FnTool("add", func(in *jsonx.Object) (any, error) {
		if calls != nil {
			*calls++
		}
		a, _ := in.Get("a")
		b, _ := in.Get("b")
		return objOf("sum", a.(float64)+b.(float64)), nil
	})}
}

func TestTurn(t *testing.T) {
	t.Run("namespaces tools, runs them, records runs and tool calls, and caps steps", func(t *testing.T) {
		calls := 0
		h := basic(t, []models.Step{models.CallTool("alpha__add", objOf("a", 1, "b", 2), "c1"), models.CallTool("alpha__add", objOf("a", 3, "b", 4), "c2"), models.CallTool("alpha__add", objOf("a", 5, "b", 6), "c3")},
			extra{registry: map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools("add", addTool(&calls)))}, rows: []chattest.Row{{"alpha", "tools"}}})
		setBudget(t, h.DB, func(b *settings.Budgets) { b.MaxSteps = 2 })
		h.sendIdle("hitung dong")
		eq(t, calls, 1)
		jsonEq(t, h.m.Choices, `["auto","none"]`)
		contains(t, prompt(h.m, 1), "almost out of time or steps")
		notContains(t, prompt(h.m, 0), "almost out of time or steps")
		run := allRuns(t, h.DB)[0]
		eq(t, run.Status, "error")
		eq(t, run.Steps, int64(2))
		eq(t, run.TokensIn, int64(20))
		eq(t, run.TokensOut, int64(10))
		eq(t, *run.ModelConnectorID, "cliproxy")
		tc := allToolCalls(t, h.DB)
		eq(t, len(tc), 1)
		eq(t, fmt.Sprintf("%s %s %s", tc[0].Tool, *tc[0].ConnectorID, tc[0].Status), "alpha__add alpha ok")
		eq(t, *tc[0].ResultPreview, `{"sum":3}`)
		notice := lastText(t, h.DB)
		contains(t, notice, "kena batas 2 langkah")
		contains(t, notice, "Yang sempat jalan: add.")
	})

	t.Run("the last allowed step answers with text", func(t *testing.T) {
		h := basic(t, []models.Step{models.CallTool("alpha__add", objOf("a", 1, "b", 2), "c1"), models.Say("hasilnya 3")},
			extra{registry: map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools("add", addTool(nil)))}, rows: []chattest.Row{{"alpha", "tools"}}})
		setBudget(t, h.DB, func(b *settings.Budgets) { b.MaxSteps = 2 })
		h.sendIdle("hitung dong")
		run := allRuns(t, h.DB)[0]
		eq(t, run.Status, "ok")
		eq(t, run.Steps, int64(2))
		jsonEq(t, outboxTexts(t, h.DB), `["hasilnya 3"]`)
	})

	t.Run("asks for the reply early when the next step probably won't fit in the time left", func(t *testing.T) {
		h := basic(t, []models.Step{chattest.Sleep(450*time.Millisecond, models.CallTool("alpha__look", objOf(), "c1")), models.Say("ini yang udah aku cek")},
			extra{registry: map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools("look", tool("read", ret(objOf("ok", true)))))}, rows: []chattest.Row{{"alpha", "tools"}}, turnTimeoutMs: 1000})
		h.sendIdle("cek dong")
		jsonEq(t, h.m.Choices, `["auto","none"]`)
		eq(t, allRuns(t, h.DB)[0].Status, "ok")
		jsonEq(t, outboxTexts(t, h.DB), `["ini yang udah aku cek"]`)
	})

	t.Run("tells the owner when a turn times out, and saves the notice for the next turn", func(t *testing.T) {
		h := basic(t, []models.Step{models.CallTool("alpha__look", objOf(), "c1"), chattest.Sleep(600*time.Millisecond, models.Say("telat"))},
			extra{registry: map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools("look", tool("read", ret(objOf("ok", true)))))}, rows: []chattest.Row{{"alpha", "tools"}}, turnTimeoutMs: 300})
		h.sendIdle("cek dong")
		run := allRuns(t, h.DB)[0]
		eq(t, run.Status, "aborted")
		eq(t, *run.Error, "turn timed out")
		notice := lastText(t, h.DB)
		contains(t, notice, "kepotong")
		contains(t, notice, "Yang sempat jalan: look.")
		contains(t, notice, "Belum ada yang diubah.")
		rows, err := h.DB.Query("SELECT text FROM messages WHERE role = 'assistant'")
		saved := need(db.All(rows, err, func(s db.Scanner) (string, error) { var x string; return x, s.Scan(&x) }))
		jsonEq(t, saved, jsonx.Stringify([]string{notice}))
	})

	t.Run("fails over to the next connector on 429 and on 5xx", func(t *testing.T) {
		for _, status := range []int{429, 503} {
			d := testutil.DB(t)
			chattest.OwnTelegram(t, d)
			chattest.ModelChain(t, d, "cliproxy", "ai-gateway")
			a := models.NewScripted(models.Fail(models.NewHTTPError(status)), models.Fail(models.NewHTTPError(status)))
			b := models.NewScripted(models.Say("dari gateway"))
			h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", a), "ai-gateway": chattest.Model("ai-gateway", b), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))},
				Rows: []chattest.Row{{"cliproxy", "model"}, {"ai-gateway", "model"}, {"telegram", "channel"}}})
			need(h.Engine.Inbound(tgMsg("halo")))
			h.Engine.Idle()
			jsonEq(t, outboxTexts(t, d), `["dari gateway"]`)
			run := allRuns(t, d)[0]
			eq(t, run.Status, "ok")
			eq(t, *run.ModelConnectorID, "ai-gateway")
		}
	})

	t.Run("doesn't fail over on a 400", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.OwnTelegram(t, d)
		chattest.ModelChain(t, d, "cliproxy", "ai-gateway")
		a := models.NewScripted(models.Fail(models.NewHTTPError(400)), models.Fail(models.NewHTTPError(400)))
		b := models.NewScripted(models.Say("dari gateway"))
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", a), "ai-gateway": chattest.Model("ai-gateway", b), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))},
			Rows: []chattest.Row{{"cliproxy", "model"}, {"ai-gateway", "model"}, {"telegram", "channel"}}})
		need(h.Engine.Inbound(tgMsg("halo")))
		h.Engine.Idle()
		eq(t, len(b.Prompts), 0)
		eq(t, allRuns(t, d)[0].Status, "error")
		contains(t, lastText(t, d), "Waduh, gagal jawab")
	})

	t.Run("skips a paused model connector in the chain", func(t *testing.T) {
		d := testutil.DB(t)
		chattest.OwnTelegram(t, d)
		chattest.ModelChain(t, d, "cliproxy", "ai-gateway")
		a := models.NewScripted(models.Say("dari cliproxy"))
		b := models.NewScripted(models.Say("dari gateway"))
		h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"cliproxy": chattest.Model("cliproxy", a), "ai-gateway": chattest.Model("ai-gateway", b), "telegram": conntest.ChannelRuntime("telegram", conntest.NewFakeChannel("telegram"))},
			Rows: []chattest.Row{{"cliproxy", "model"}, {"ai-gateway", "model"}, {"telegram", "channel"}}})
		need(h.Engine.Inbound(tgMsg("/connectors off cliproxy")))
		need(h.Engine.Inbound(tgMsg("halo")))
		h.Engine.Idle()
		eq(t, lastText(t, d), "dari gateway")
	})

	t.Run("pauses turns when the runs-per-hour cap is hit and tells the owner", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("1"), models.Say("2")}, extra{})
		setBudget(t, h.DB, func(b *settings.Budgets) { b.MaxRunsPerHour = 1 })
		h.sendIdle("satu")
		h.sendIdle("dua")
		eq(t, len(h.m.Prompts), 1)
		contains(t, lastText(t, h.DB), "Turn lagi dijeda sampai 15.00")
		reason := need(settings.Get[*string](h.DB, "turnsPausedReason"))
		contains(t, *reason, "1 run per jam")
	})

	t.Run("runs a scheduled job without destructive tools and stays silent on the silent token", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("hi"), models.Say("NOTHING")}, extra{registry: map[string]*connectors.Runtime{
			"alpha": toolsRT("alpha", tools("read", tool("read", ret(1)), "nuke", tool("destructive", ret(1)))),
		}, rows: []chattest.Row{{"alpha", "tools"}}})
		h.sendIdle("init")
		c := firstChat(t, h.DB)
		before := len(outboxTexts(t, h.DB))
		r := need(h.Engine.RunTurn(chat.TurnOptions{ChatID: c.ID, Trigger: "schedule", Prompt: "heartbeat", SilentToken: "NOTHING"}))
		eq(t, r.Status, "ok")
		jsonEq(t, h.m.Offered[len(h.m.Offered)-1], `["alpha__read"]`)
		eq(t, len(outboxTexts(t, h.DB)), before)
	})

	t.Run("runs a flow prompt step with its own run id, only the tools it allowed, and no destructive ones", func(t *testing.T) {
		h := basic(t, []models.Step{models.Say("ok"), models.Say("NOTHING")}, extra{registry: map[string]*connectors.Runtime{
			"alpha": toolsRT("alpha", tools("read", tool("read", ret(1)), "other", tool("read", ret(1)), "nuke", tool("destructive", ret(1)))),
		}, rows: []chattest.Row{{"alpha", "tools"}}})
		h.sendIdle("init")
		c := firstChat(t, h.DB)
		no := false
		r := need(h.Engine.RunTurn(chat.TurnOptions{ChatID: c.ID, Trigger: "flow", Prompt: "x", RunID: "run_flowstep1", ToolNames: &[]string{"alpha__read", "alpha__nuke"}, Deliver: &no, UntrustedInput: true}))
		eq(t, r.RunID, "run_flowstep1")
		eq(t, r.Status, "ok")
		eq(t, r.Tainted, true)
		jsonEq(t, h.m.Offered[len(h.m.Offered)-1], `["alpha__read"]`)
		all := need(h.Engine.RunTurn(chat.TurnOptions{ChatID: c.ID, Trigger: "flow", Prompt: "y", ToolNames: &[]string{"*"}, SilentToken: "NOTHING"}))
		eq(t, all.Status, "ok")
		jsonEq(t, h.m.Offered[len(h.m.Offered)-1], `["alpha__read","alpha__other"]`)
	})

	t.Run("shows later turns the tool calls earlier turns made", func(t *testing.T) {
		h := basic(t, []models.Step{models.CallTool("alpha__add", objOf("a", 1, "b", 2), "c1"), models.Say("hasilnya 3"), models.Say("sama-sama"), models.Say("halo lagi")},
			extra{registry: map[string]*connectors.Runtime{"alpha": toolsRT("alpha", tools("add", addTool(nil)))}, rows: []chattest.Row{{"alpha", "tools"}}})
		h.sendIdle("1 tambah 2?")
		h.sendIdle("makasih")
		roles := func(p []models.Message) string {
			var r []string
			for _, m := range p {
				r = append(r, m.Role())
			}
			return strings.Join(r, ",")
		}
		next := h.m.Prompts[2]
		eq(t, roles(next), "system,user,assistant,tool,assistant,user")
		contains(t, jsonx.Stringify(next[2]), `"toolCallId":"c1"`)
		contains(t, jsonx.Stringify(next[3]), "sum")
		testutil.Must(t, connectors.Pause(h.DB, "alpha", "", 1))
		h.Sup.Reconcile()
		h.sendIdle("halo")
		eq(t, roles(h.m.Prompts[3]), "system,user,assistant,user,assistant,user")
	})
}

var _ = base64.StdEncoding
