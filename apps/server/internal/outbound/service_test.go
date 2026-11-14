package outbound_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/settings"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

var (
	NOW  = tu.NOW
	WITA = tu.WITA
	MIN  = tu.MIN
	HOUR = tu.HOUR
	SIG  = settings.DefaultOutboundSignature
)

const AGENT = "agt_test"

func setup(t *testing.T) (*db.DB, db.Chat) {
	d := tu.DB(t)
	owner, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, NOW)
	tu.Must(t, err)
	return d, owner
}

func meta(owner db.Chat) outbound.SendMeta {
	id := owner.ID
	return outbound.SendMeta{Origin: "chat", ApprovedBy: "owner", OwnerChatID: &id}
}

func add(t *testing.T, d db.Q, in outbound.AddContactInput) db.Contact {
	t.Helper()
	if in.AgentID == "" {
		in.AgentID = AGENT
	}
	c, err := outbound.AddContact(d, in, NOW)
	tu.Must(t, err)
	return c
}

func send(t *testing.T, d db.Q, c db.Contact, text string, m outbound.SendMeta, hold, now int64) outbound.SendResult {
	t.Helper()
	r, err := outbound.SendNow(d, c, text, m, hold, now)
	tu.Must(t, err)
	return r
}

func sched(t *testing.T, d db.Q, c db.Contact, text, local, rr string, m outbound.SendMeta) db.OutboundMessage {
	t.Helper()
	r, err := outbound.ScheduleSend(d, c, text, outbound.ScheduleInput{LocalStart: local, TZ: WITA, RRule: rr}, m, NOW)
	tu.Must(t, err)
	return r
}

func outboxRow(t *testing.T, d db.Q, where string, args ...any) db.Outbox {
	t.Helper()
	o, ok, err := db.One(d.QueryRow("SELECT "+db.OutboxCols+" FROM outbox WHERE "+where, args...), db.ScanOutbox)
	tu.Must(t, err)
	if !ok {
		t.Fatalf("no outbox row where %s", where)
	}
	return o
}

func outboxRows(t *testing.T, d db.Q, where string, args ...any) []db.Outbox {
	t.Helper()
	rows, err := d.Query("SELECT "+db.OutboxCols+" FROM outbox WHERE "+where+" ORDER BY rowid", args...)
	out, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(t, err)
	return out
}

func obm(t *testing.T, d db.Q, id string) db.OutboundMessage {
	t.Helper()
	o, _, err := outbound.GetOutbound(d, id)
	tu.Must(t, err)
	return o
}

func setOutbound(t *testing.T, d db.Q, f func(*settings.Outbound)) {
	t.Helper()
	o := settings.Defaults().Outbound
	f(&o)
	tu.Must(t, settings.Set(d, "outbound", o, NOW))
}

func wantErr(t *testing.T, err error, sub string) {
	t.Helper()
	var oe *outbound.OutboundError
	if !errors.As(err, &oe) || !strings.Contains(err.Error(), sub) {
		t.Fatalf("want OutboundError with %q, got %v", sub, err)
	}
}

func TestPhoneNumbers(t *testing.T) {
	for in, want := range map[string]string{
		"082200001001": "6282200001001", "0822 0000-1001": "6282200001001", "+62 822 0000 1001": "6282200001001", "6282200001001": "6282200001001",
		"+62 0822 0000 1001": "6282200001001", "82200001001": "6282200001001", "+1 415 555 0100": "14155550100", "0065 9123 4567": "6591234567",
		"Zaki": "", "0812": "", "14155550100": "",
	} {
		if got := outbound.NormalizePhone(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if outbound.FormatPhone("6282200001001") != "+62 822-0000-1001" {
		t.Fatal(outbound.FormatPhone("6282200001001"))
	}
	if got := outbound.PhonesIn("chat Zaki (082200001001) sekarang"); !reflect.DeepEqual(got, []string{"6282200001001"}) {
		t.Fatal(got)
	}
}

func TestContacts(t *testing.T) {
	t.Run("adds a number as a chat Palui won't answer, and finds it by name or number", func(t *testing.T) {
		d, _ := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		if c.Channel != "whatsapp" || c.Address != "6282200001001" || c.Policy != "ask" {
			t.Fatalf("%+v", c)
		}
		chat, _, err := chats.Get(d, c.ChatID)
		tu.Must(t, err)
		if chat.ExternalID != "6282200001001@s.whatsapp.net" || chat.Allowed || chat.Title != "Zaki" {
			t.Fatalf("%+v", chat)
		}
		for ref, want := range map[string]bool{"zaki": true, "+62 822 0000 1001": true, "Budi": false} {
			f, ok, err := outbound.FindContact(d, AGENT, ref)
			tu.Must(t, err)
			if ok != want || (ok && f.ID != c.ID) {
				t.Fatal(ref)
			}
		}
	})
	t.Run("keeps an existing chat's settings", func(t *testing.T) {
		d, _ := setup(t)
		yes := true
		existing, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "6281111111111@s.whatsapp.net", Title: "Ibu", Allowed: &yes}, NOW)
		tu.Must(t, err)
		c := add(t, d, outbound.AddContactInput{Name: "Ibu", Phone: "081111111111"})
		chat, _, _ := chats.Get(d, existing.ID)
		if c.ChatID != existing.ID || !chat.Allowed {
			t.Fatal("changed the chat")
		}
	})
	t.Run("refuses duplicate names and numbers, groups and the web chat", func(t *testing.T) {
		d, _ := setup(t)
		add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		try := func(in outbound.AddContactInput) error {
			in.AgentID = AGENT
			_, err := outbound.AddContact(d, in, NOW)
			return err
		}
		wantErr(t, try(outbound.AddContactInput{Name: "zaki", Phone: "081111111111"}), "already a contact named")
		wantErr(t, try(outbound.AddContactInput{Name: "Other", Phone: "6282200001001"}), "already saved as Zaki")
		group, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "-100", Kind: "group"}, NOW)
		wantErr(t, try(outbound.AddContactInput{Name: "Grup", ChatID: group.ID}), "groups")
		web, _ := chats.Ensure(d, chats.EnsureInput{Channel: "web", ExternalID: "web"}, NOW)
		wantErr(t, try(outbound.AddContactInput{Name: "Web", ChatID: web.ID}), "web chat")
		wantErr(t, try(outbound.AddContactInput{Name: "Bad", Phone: "12345"}), "isn't a phone number")
		wantErr(t, try(outbound.AddContactInput{Name: " "}), "needs a name")
		wantErr(t, try(outbound.AddContactInput{Name: "X"}), "Give a phone number or a chat.")
	})
	t.Run("adds a Telegram contact from an existing DM", func(t *testing.T) {
		d, _ := setup(t)
		dm, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "777", Title: "Budi"}, NOW)
		c := add(t, d, outbound.AddContactInput{Name: "Budi", ChatID: dm.ID})
		if c.Channel != "telegram" || c.Address != "777" || c.ChatID != dm.ID {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("matches the owner's words by whole name or number", func(t *testing.T) {
		m := func(text string) bool { return outbound.MentionsContact("Zaki", "whatsapp", "6282200001001", text) }
		if !m("chat Zaki sekarang") || !m("kabarin zaki ya") || !m("chat 0822-0000-1001") || m("chat Zakiyah sekarang") || m("chat dia sekarang") || m("") {
			t.Fatal("mentions")
		}
	})
	t.Run("updates a contact's policy", func(t *testing.T) {
		d, _ := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		u, err := outbound.UpdateContact(d, c.ID, outbound.ContactPatch{Policy: tu.Ptr("allow"), Note: tu.Ptr(" teman ")}, NOW)
		tu.Must(t, err)
		if u.Policy != "allow" || *u.Note != "teman" {
			t.Fatalf("%+v", u)
		}
		var kind string
		tu.Must(t, d.QueryRow("SELECT kind FROM events WHERE kind = 'outbound.policy'").Scan(&kind))
		_, err = outbound.UpdateContact(d, "con_missing", outbound.ContactPatch{}, NOW)
		wantErr(t, err, "No contact con_missing.")
	})
}

func TestSending(t *testing.T) {
	t.Run("holds a now send for the undo window and can cancel it", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		r := send(t, d, c, "Halo, ini asisten Zaki. Dia lagi otw.", meta(owner), 15_000, NOW)
		row := outboxRow(t, d, "id = ?", r.OutboxID)
		if row.ChatID != c.ChatID || row.Source != "outbound:"+r.Row.ID || row.Status != "pending" || row.NextAttemptAt != NOW+15_000 || row.Payload.Text != "Halo, ini asisten Zaki. Dia lagi otw.\n\n"+SIG {
			t.Fatalf("%+v", row)
		}
		if h, ok, _ := outbound.NewestHeld(d, owner.ID, NOW+1000); !ok || h.ID != r.Row.ID {
			t.Fatal("not held")
		}
		if res, _ := outbound.Cancel(d, r.Row.ID, NOW+1000); !res.OK {
			t.Fatalf("%+v", res)
		}
		if outboxRow(t, d, "id = ?", r.OutboxID).Status != "cancelled" {
			t.Fatal("not cancelled")
		}
		if _, ok, _ := outbound.NewestHeld(d, owner.ID, NOW+2000); ok {
			t.Fatal("still held")
		}
		if res, _ := outbound.Cancel(d, r.Row.ID, NOW+2000); res.OK {
			t.Fatal("cancelled twice")
		}
	})
	t.Run("enforces per-contact and total limits over a rolling day", func(t *testing.T) {
		d, owner := setup(t)
		setOutbound(t, d, func(o *settings.Outbound) { o.PerContactPerDay, o.PerDay = 2, 3 })
		a := add(t, d, outbound.AddContactInput{Name: "A", Phone: "081111111111"})
		b := add(t, d, outbound.AddContactInput{Name: "B", Phone: "082222222222"})
		send(t, d, a, "1", meta(owner), 0, NOW)
		second := send(t, d, a, "2", meta(owner), 15_000, NOW)
		_, err := outbound.SendNow(d, a, "3", meta(owner), 0, NOW)
		wantErr(t, err, "2 messages to A")
		// A cancelled send doesn't count.
		_, err = outbound.Cancel(d, second.Row.ID, NOW)
		tu.Must(t, err)
		send(t, d, a, "3", meta(owner), 0, NOW)
		send(t, d, b, "1", meta(owner), 0, NOW)
		_, err = outbound.SendNow(d, b, "2", meta(owner), 0, NOW)
		wantErr(t, err, "3 messages to contacts")
		// A day later the window has moved on.
		send(t, d, b, "2", meta(owner), 0, NOW+24*HOUR+MIN)
	})
	t.Run("kill switch refuses sends and cancels waiting ones", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		r := send(t, d, c, "otw", meta(owner), 15_000, NOW)
		n, err := outbound.SetEnabled(d, false, NOW)
		tu.Must(t, err)
		if n != 1 || outboxRow(t, d, "id = ?", r.OutboxID).Status != "cancelled" {
			t.Fatal(n)
		}
		_, err = outbound.SendNow(d, c, "otw", meta(owner), 0, NOW)
		wantErr(t, err, "turned off")
		_, err = outbound.ScheduleSend(d, c, "otw", outbound.ScheduleInput{LocalStart: "2026-11-10T20:00", TZ: WITA}, meta(owner), NOW)
		wantErr(t, err, "turned off")
		_, err = outbound.SetEnabled(d, true, NOW)
		tu.Must(t, err)
		send(t, d, c, "otw", meta(owner), 0, NOW)
	})
	t.Run("removing a contact cancels its scheduled and held sends", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		held := send(t, d, c, "otw", meta(owner), 15_000, NOW)
		later := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner))
		n, err := outbound.RemoveContact(d, c.ID, NOW)
		tu.Must(t, err)
		if n != 2 || outboxRow(t, d, "id = ?", held.OutboxID).Status != "cancelled" || obm(t, d, later.ID).Status != "cancelled" {
			t.Fatal(n)
		}
		if all, _ := outbound.ListContacts(d, nil); len(all) != 0 {
			t.Fatal(all)
		}
		_, err = outbound.SendNow(d, c, "x", meta(owner), 0, NOW)
		wantErr(t, err, "removed")
	})
	t.Run("refuses a time already past today instead of rolling to tomorrow", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		// NOW is 10:00 WITA.
		_, err := outbound.ScheduleSend(d, c, "pagi", outbound.ScheduleInput{LocalStart: "2026-11-10T08:00", TZ: WITA}, meta(owner), NOW)
		wantErr(t, err, "08:00 today has already passed")
		_, err = outbound.ScheduleSend(d, c, "x", outbound.ScheduleInput{LocalStart: "2026-11-10T20:00", TZ: "Bad/Zone"}, meta(owner), NOW)
		wantErr(t, err, "Unknown time zone")
		if r := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner)); r.FireAt != NOW+10*HOUR {
			t.Fatal(r.FireAt)
		}
	})
	t.Run("finds a held send from a reply quoting its notice", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		r := send(t, d, c, "otw", meta(owner), 15_000, NOW)
		tu.Must(t, outbound.HoldNotice(d, r.Row, c, r.SendAt, NOW))
		notice := outboxRow(t, d, "source = ?", "outbound-notice:"+r.Row.ID)
		if notice.ChatID != owner.ID || !strings.Contains(notice.Payload.Text, "15 detik") || !reflect.DeepEqual(notice.Payload.Actions, []db.OutboxAction{{ID: "ob:cancel:" + r.Row.ID, Label: "Batalin"}}) {
			t.Fatalf("%+v", notice)
		}
		_, err := d.Exec("UPDATE outbox SET channel_msg_id = 'm1' WHERE id = ?", notice.ID)
		tu.Must(t, err)
		if h, _ := outbound.HeldFromNotice(d, owner.ID, "m1"); h != r.Row.ID {
			t.Fatal(h)
		}
	})
}

func TestSignature(t *testing.T) {
	setupContact := func(t *testing.T) (*db.DB, db.Chat, db.Contact) {
		d, owner := setup(t)
		return d, owner, add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
	}
	final := func(t *testing.T, d db.Q, text string) string {
		t.Helper()
		s, err := outbound.FinalText(d, text, false)
		tu.Must(t, err)
		return s
	}
	save := func(t *testing.T, d db.Q, f func(*outbound.SettingsPatch)) {
		t.Helper()
		o := settings.Defaults().Outbound
		p := outbound.SettingsPatch{PerContactPerDay: o.PerContactPerDay, PerDay: o.PerDay, HoldSeconds: o.HoldSeconds, SignatureOn: o.SignatureOn, Signature: o.Signature}
		f(&p)
		_, err := outbound.SaveSettings(d, p, NOW)
		tu.Must(t, err)
	}
	t.Run("is on by default with the default text, after one blank line", func(t *testing.T) {
		d, owner, c := setupContact(t)
		if SIG != "\u2014\n_Dikirim otomatis oleh asisten AI Zaki. Balasan ke nomor ini tidak dibaca, silakan hubungi Zaki langsung_" {
			t.Fatal(SIG)
		}
		r := send(t, d, c, "  otw  ", meta(owner), 0, NOW)
		if r.Row.Text != "otw\n\n"+SIG || outboxRow(t, d, "id = ?", r.OutboxID).Payload.Text != "otw\n\n"+SIG {
			t.Fatal(r.Row.Text)
		}
	})
	t.Run("uses the owner's own text, and nothing when turned off", func(t *testing.T) {
		d, owner, c := setupContact(t)
		save(t, d, func(p *outbound.SettingsPatch) { p.Signature = "(dikirim asisten Zaki)" })
		if r := send(t, d, c, "otw", meta(owner), 0, NOW); r.Row.Text != "otw\n\n(dikirim asisten Zaki)" {
			t.Fatal(r.Row.Text)
		}
		save(t, d, func(p *outbound.SettingsPatch) { p.SignatureOn = false })
		if r := send(t, d, c, "otw", meta(owner), 0, NOW); r.Row.Text != "otw" {
			t.Fatal(r.Row.Text)
		}
	})
	t.Run("leaves it off a message the owner opted out", func(t *testing.T) {
		d, owner, c := setupContact(t)
		m := meta(owner)
		m.NoSignature = true
		if r := send(t, d, c, "otw", m, 0, NOW); r.Row.Text != "otw" {
			t.Fatal(r.Row.Text)
		}
		if r := sched(t, d, c, "malam", "2026-11-10T20:00", "", m); r.Text != "malam" {
			t.Fatal(r.Text)
		}
	})
	t.Run("isn't added twice: current, earlier defaults, typed by hand, or an edited one", func(t *testing.T) {
		d, owner, c := setupContact(t)
		if final(t, d, "otw\n\n"+SIG) != "otw\n\n"+SIG {
			t.Fatal("signed twice")
		}
		for _, old := range settings.LegacyOutboundSignatures {
			if final(t, d, "otw\n\n"+old) != "otw\n\n"+old {
				t.Fatal(old)
			}
		}
		// Typed by hand without the dash line or the italics.
		if strings.Contains(final(t, d, "otw\n\nPesan otomatis dari Palui, asisten Zaki. Balasan ke nomor ini tidak dibaca"), SIG) {
			t.Fatal("hand-typed legacy signed")
		}
		if strings.Contains(final(t, d, "otw\n_dikirim otomatis oleh asisten ai zaki. balasan ke nomor ini tidak dibaca, silakan hubungi zaki langsung_ "), "\u2014") {
			t.Fatal("hand-typed signed")
		}
		// A message that only mentions part of it still gets signed.
		if final(t, d, "Balasan ke nomor ini tidak dibaca?") != "Balasan ke nomor ini tidak dibaca?\n\n"+SIG {
			t.Fatal("partial")
		}
		// After the owner edits it, a text with the previous one isn't signed again.
		before := send(t, d, c, "pagi", meta(owner), 0, NOW).Row.Text
		save(t, d, func(p *outbound.SettingsPatch) { p.Signature = "\u2014\n_Asisten Zaki_" })
		o, _ := settings.GetOutbound(d)
		if !reflect.DeepEqual(o.PastSignatures, []string{SIG}) {
			t.Fatal(o.PastSignatures)
		}
		if final(t, d, before) != before || final(t, d, "siang") != "siang\n\n\u2014\n_Asisten Zaki_" {
			t.Fatal("edited")
		}
	})
	t.Run("counts against the 2000 character cap", func(t *testing.T) {
		d, _, _ := setupContact(t)
		save(t, d, func(p *outbound.SettingsPatch) { p.Signature = strings.Repeat("s", 150) })
		if len(final(t, d, strings.Repeat("a", 1800))) != 1800+2+150 {
			t.Fatal("length")
		}
		_, err := outbound.FinalText(d, strings.Repeat("a", 1900), false)
		wantErr(t, err, "2000 characters, signature included")
		_, err = outbound.FinalText(d, " ", false)
		wantErr(t, err, "The message is empty.")
	})
	t.Run("refuses an empty signature that is turned on", func(t *testing.T) {
		d, _, _ := setupContact(t)
		_, err := outbound.SaveSettings(d, outbound.SettingsPatch{SignatureOn: true, Signature: "  "}, NOW)
		wantErr(t, err, "Write the signature")
	})
	t.Run("is in the cancel notice the owner sees", func(t *testing.T) {
		d, owner, c := setupContact(t)
		r := send(t, d, c, "otw", meta(owner), 15_000, NOW)
		tu.Must(t, outbound.HoldNotice(d, r.Row, c, r.SendAt, NOW))
		if n := outboxRow(t, d, "source = ?", "outbound-notice:"+r.Row.ID); n.Payload.Text != "Pesan ke Zaki (+62 822-0000-1001) kekirim 15 detik lagi:\n\notw\n\n"+SIG {
			t.Fatal(n.Payload.Text)
		}
	})
	t.Run("scheduled and repeating sends store the signed text and send it once", func(t *testing.T) {
		d, owner, c := setupContact(t)
		ext := outbound.NewScheduler(d, logx.Silent)
		one := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner))
		weekly := sched(t, d, c, "kabar", "2026-11-13T11:00", "FREQ=WEEKLY;BYDAY=FR", meta(owner))
		if one.Text != "malam\n\n"+SIG || weekly.Text != "kabar\n\n"+SIG {
			t.Fatal(one.Text)
		}
		for _, at := range []int64{one.FireAt, weekly.FireAt, weekly.FireAt + 7*24*HOUR} {
			_, err := ext.Sweep(at)
			tu.Must(t, err)
		}
		texts := func(id string) []string {
			var out []string
			for _, r := range outboxRows(t, d, "source = ?", "outbound:"+id) {
				out = append(out, r.Payload.Text)
			}
			return out
		}
		if !reflect.DeepEqual(texts(one.ID), []string{"malam\n\n" + SIG}) || !reflect.DeepEqual(texts(weekly.ID), []string{"kabar\n\n" + SIG, "kabar\n\n" + SIG}) {
			t.Fatal(texts(weekly.ID))
		}
	})
	t.Run("a repeat saved with an earlier signature keeps one signature", func(t *testing.T) {
		d, owner, c := setupContact(t)
		ext := outbound.NewScheduler(d, logx.Silent)
		old := settings.LegacyOutboundSignatures[0]
		r := sched(t, d, c, "kabar\n\n"+old, "2026-11-13T11:00", "FREQ=WEEKLY;BYDAY=FR", meta(owner))
		if r.Text != "kabar\n\n"+old {
			t.Fatal(r.Text)
		}
		_, err := ext.Sweep(r.FireAt)
		tu.Must(t, err)
		if outboxRow(t, d, "source = ?", "outbound:"+r.ID).Payload.Text != "kabar\n\n"+old {
			t.Fatal("re-signed")
		}
	})
}

func TestOutboundScheduler(t *testing.T) {
	t.Run("fires a one-off once at its time", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		ext := outbound.NewScheduler(d, logx.Silent)
		r := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner))
		if due, ok, _ := ext.NextDue(); !ok || due != r.FireAt {
			t.Fatal(due)
		}
		for at, want := range map[int64]int{r.FireAt - 1: 0} {
			if n, _ := ext.Sweep(at); n != want {
				t.Fatal(n)
			}
		}
		if n, _ := ext.Sweep(r.FireAt); n != 1 {
			t.Fatal(n)
		}
		if n, _ := ext.Sweep(r.FireAt + 1000); n != 0 {
			t.Fatal(n)
		}
		sent := outboxRows(t, d, "source = ?", "outbound:"+r.ID)
		if len(sent) != 1 || sent[0].ChatID != c.ChatID || obm(t, d, r.ID).Status != "done" {
			t.Fatal(sent)
		}
	})
	t.Run("moves a series to its next occurrence", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "X", Phone: "082200001001"})
		ext := outbound.NewScheduler(d, logx.Silent)
		// Friday 2026-11-13 11:00 WITA, weekly.
		r := sched(t, d, c, "kabar", "2026-11-13T11:00", "FREQ=WEEKLY;BYDAY=FR", meta(owner))
		_, err := ext.Sweep(r.FireAt)
		tu.Must(t, err)
		if a := obm(t, d, r.ID); a.Status != "scheduled" || a.FireAt != r.FireAt+7*24*HOUR {
			t.Fatalf("%+v", a)
		}
	})
	t.Run("skips an occurrence over the limit or long overdue and tells the owner", func(t *testing.T) {
		d, owner := setup(t)
		setOutbound(t, d, func(o *settings.Outbound) { o.PerContactPerDay = 1 })
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		ext := outbound.NewScheduler(d, logx.Silent)
		send(t, d, c, "pagi", meta(owner), 0, NOW)
		r := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner))
		_, err := ext.Sweep(r.FireAt)
		tu.Must(t, err)
		if len(outboxRows(t, d, "source = ?", "outbound:"+r.ID)) != 0 {
			t.Fatal("sent over the limit")
		}
		notices := outboxRows(t, d, "chat_id = ?", owner.ID)
		if !strings.Contains(notices[len(notices)-1].Payload.Text, "nggak aku kirim") {
			t.Fatal(notices)
		}
		late := sched(t, d, c, "late", "2026-11-11T20:00", "", meta(owner))
		_, err = ext.Sweep(late.FireAt + 31*MIN)
		tu.Must(t, err)
		if len(outboxRows(t, d, "source = ?", "outbound:"+late.ID)) != 0 || obm(t, d, late.ID).Status != "done" {
			t.Fatal("late sent")
		}
	})
	t.Run("cancels a send whose contact is gone and recovers firing rows", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		ext := outbound.NewScheduler(d, logx.Silent)
		r := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner))
		_, err := d.Exec("UPDATE outbound_messages SET status = 'firing' WHERE id = ?", r.ID)
		tu.Must(t, err)
		tu.Must(t, ext.Recover(NOW))
		if obm(t, d, r.ID).Status != "scheduled" {
			t.Fatal("not recovered")
		}
		_, err = d.Exec("UPDATE contacts SET removed_at = 1 WHERE id = ?", c.ID)
		tu.Must(t, err)
		_, err = ext.Sweep(r.FireAt)
		tu.Must(t, err)
		if obm(t, d, r.ID).Status != "cancelled" {
			t.Fatal("not cancelled")
		}
	})
}

func newWorker(d *db.DB, wa *conntest.FakeChannel) *outbox.Worker {
	return outbox.NewWorker(outbox.WorkerOptions{DB: d, Channel: func(ch string) outbox.Channel {
		if ch == "whatsapp" {
			return wa
		}
		return nil
	}, Log: logx.Silent, Clock: func() int64 { return NOW }})
}

func TestOutboxWorkerAndOutboundRows(t *testing.T) {
	t.Run("cancels at send time when the kill switch is off, and never reroutes to the owner", func(t *testing.T) {
		d, owner := setup(t)
		tu.SeedConnector(t, d, "whatsapp", "channel", tu.SeedOptions{}, NOW)
		wa := conntest.NewFakeChannel("whatsapp")
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		r := send(t, d, c, "otw", meta(owner), 0, NOW)
		setOutbound(t, d, func(o *settings.Outbound) { o.Enabled = false })
		newWorker(d, wa).Tick()
		if len(wa.Sent()) != 0 || outboxRow(t, d, "id = ?", r.OutboxID).Status != "cancelled" {
			t.Fatal("sent")
		}
	})
	t.Run("sends to the contact's WhatsApp JID", func(t *testing.T) {
		d, owner := setup(t)
		tu.SeedConnector(t, d, "whatsapp", "channel", tu.SeedOptions{}, NOW)
		wa := conntest.NewFakeChannel("whatsapp")
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		send(t, d, c, "otw", meta(owner), 0, NOW)
		newWorker(d, wa).Tick()
		if !reflect.DeepEqual(wa.Sent(), []conntest.Sent{{To: "6282200001001@s.whatsapp.net", Payload: db.OutboxPayload{Text: "otw\n\n" + SIG, Format: "plain"}}}) {
			t.Fatalf("%+v", wa.Sent())
		}
	})
	t.Run("tells the owner when a send to a contact gives up", func(t *testing.T) {
		d, owner := setup(t)
		c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
		r := send(t, d, c, "otw", meta(owner), 0, NOW)
		row := outboxRow(t, d, "id = ?", r.OutboxID)
		row.Error = tu.Ptr(strings.Repeat("e", 200))
		tu.Must(t, outbound.OutboundFailed(d, row, NOW))
		notices := outboxRows(t, d, "chat_id = ? AND source = 'system'", owner.ID)
		if len(notices) != 1 || notices[0].Payload.Text != "Pesan ke Zaki gagal terkirim ("+strings.Repeat("e", 120)+")." {
			t.Fatal(notices)
		}
		tu.Must(t, outbound.OutboundFailed(d, db.Outbox{Source: "reminder:x"}, NOW))
		if len(outboxRows(t, d, "source = 'system'")) != 1 {
			t.Fatal("noticed a reminder")
		}
	})
}

func TestContactsPerAgent(t *testing.T) {
	t.Run("keeps each agent's list apart, and the same number can be in both", func(t *testing.T) {
		d, _ := setup(t)
		a := add(t, d, outbound.AddContactInput{AgentID: "agt_a", Name: "Zaki", Phone: "082200001001"})
		b := add(t, d, outbound.AddContactInput{AgentID: "agt_b", Name: "Zaki", Phone: "082200001001"})
		if a.ChatID != b.ChatID {
			t.Fatal("two chats")
		}
		for agent, want := range map[string]string{"agt_a": a.ID, "agt_b": b.ID, "agt_c": ""} {
			f, _, _ := outbound.FindContact(d, agent, "Zaki")
			if f.ID != want {
				t.Fatal(agent)
			}
		}
		aa := "agt_a"
		la, _ := outbound.ListContacts(d, &aa)
		all, _ := outbound.ListContacts(d, nil)
		if len(la) != 1 || len(all) != 2 {
			t.Fatal(len(la), len(all))
		}
	})
	t.Run("counts the per-contact limit per agent's contact, and the daily total across agents", func(t *testing.T) {
		d, owner := setup(t)
		setOutbound(t, d, func(o *settings.Outbound) { o.PerContactPerDay, o.PerDay = 5, 2 })
		a := add(t, d, outbound.AddContactInput{AgentID: "agt_a", Name: "Zaki", Phone: "082200001001"})
		b := add(t, d, outbound.AddContactInput{AgentID: "agt_b", Name: "Zaki", Phone: "082200001001"})
		send(t, d, a, "1", meta(owner), 0, NOW)
		send(t, d, b, "1", meta(owner), 0, NOW)
		_, err := outbound.SendNow(d, a, "2", meta(owner), 0, NOW)
		wantErr(t, err, "2 messages to contacts")
	})
	t.Run("deleting an agent removes its contacts and cancels what's waiting", func(t *testing.T) {
		d, owner := setup(t)
		agent, err := agents.Create(d, agents.Input{Slug: "arisan", Name: tu.Ptr("Arisan"), Persona: tu.Ptr("x")}, NOW)
		tu.Must(t, err)
		c := add(t, d, outbound.AddContactInput{AgentID: agent.ID, Name: "Zaki", Phone: "082200001001"})
		held := send(t, d, c, "otw", meta(owner), 15_000, NOW)
		later := sched(t, d, c, "malam", "2026-11-10T20:00", "", meta(owner))
		if held.Row.AgentID == nil || *held.Row.AgentID != agent.ID {
			t.Fatal("agent")
		}
		_, err = agents.Delete(d, agent.ID, NOW)
		tu.Must(t, err)
		id := agent.ID
		if l, _ := outbound.ListContacts(d, &id); len(l) != 0 {
			t.Fatal(l)
		}
		if outboxRow(t, d, "id = ?", held.OutboxID).Status != "cancelled" || obm(t, d, later.ID).Status != "cancelled" {
			t.Fatal("not cancelled")
		}
	})
}

func TestRouterAdapter(t *testing.T) {
	d, owner := setup(t)
	c := add(t, d, outbound.AddContactInput{Name: "Zaki", Phone: "082200001001"})
	r := send(t, d, c, "otw", meta(owner), 15_000, NOW)
	rt := outbound.Router{}
	if id, _ := rt.NewestHeld(d, owner.ID, NOW+1); id != r.Row.ID {
		t.Fatal(id)
	}
	if o, ok, _ := rt.OwnerChatID(d, r.Row.ID); !ok || o != owner.ID {
		t.Fatal(o)
	}
	if _, ok, _ := rt.OwnerChatID(d, "obm_missing"); ok {
		t.Fatal("found missing")
	}
	if m, _ := rt.Cancel(d, r.Row.ID, NOW+1); m != "Oke, pesan ke Zaki dibatalin." {
		t.Fatal(m)
	}
	reminders.NotifyScheduleChanged()
}
