package flows_test

// events.test.ts (event triggers, message.to, message.contact)

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/settings"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

const ASK = `{
  "name": "Olahraga",
  "triggers": [{ "id": "main", "kind": "schedule", "start": "ask", "source": { "kind": "times", "times": [{ "time": "06:00", "label": "pagi" }] } }],
  "steps": [{ "id": "ask", "kind": "checkin", "text": "Udah olahraga?", "deadline": { "kind": "after", "minutes": 60 }, "followUp": { "everyMin": 30, "max": 0 } }]
}`

const STREAK = `{
  "name": "Streak",
  "triggers": [{ "id": "miss", "kind": "event", "event": "checkin.missed", "flow": "Olahraga", "start": "count" }],
  "steps": [
    { "id": "count", "kind": "set", "scope": "flow", "values": { "missed": "flow.missed + 1" } },
    { "id": "warn", "kind": "if", "when": "flow.missed >= 2", "then": "say", "else": null },
    { "id": "say", "kind": "message", "text": "Udah {{flow.missed}} kali kelewat ({{trigger.label}})." }
  ]
}`

func TestEventTriggers(t *testing.T) {
	t.Run("starts a listening flow when another flow's check-in is missed", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		h.setNow(at("2026-11-10T20:00"))
		h.create(ASK)
		streak := h.create(STREAK).Flow
		for _, day := range []string{"11", "12"} {
			h.run(at("2026-11-" + day + "T06:00"))
			h.run(at("2026-11-" + day + "T07:00"))
		}
		runs := runsOf(t, h.db, streak.ID)
		var got []string
		for _, r := range runs {
			got = append(got, r.TriggerKind+":"+r.Status)
		}
		eq(t, got, []string{"event:done", "event:done"}, "runs")
		tr := runs[0].Trigger
		for k, want := range map[string]any{"event": "checkin.missed", "flow": "Olahraga", "step": "ask", "depth": 1.0, "label": "pagi"} {
			if v, _ := tr.Get(k); v != want {
				t.Errorf("trigger.%s = %v", k, v)
			}
		}
		all := texts(t, h.db)
		eq(t, all[len(all)-1], "Udah 2 kali kelewat (pagi).", "text")
	})
	t.Run("stops an event chain deeper than 3", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		flow := h.create(`{"name":"Loop","triggers":[{"id":"again","kind":"event","event":"run.done","flow":"self","start":"m"}],"steps":[{"id":"m","kind":"set","values":{"x":"1"}}]}`).Flow
		h.start(flow.ID, "manual", "m0", "")
		for i := 0; i < 6; i++ {
			h.run(h.now())
		}
		runs := runsOf(t, h.db, flow.ID)
		var depths []float64
		for _, r := range runs {
			d, _ := r.Trigger.Get("depth")
			n, _ := d.(float64)
			depths = append(depths, n)
		}
		eq(t, depths, []float64{0, 1, 2, 3, 4}, "depths")
		last := runs[len(runs)-1]
		if last.Status != "skipped" || deref(last.SkipReason) != "chain_depth" {
			t.Errorf("last %s %s", last.Status, deref(last.SkipReason))
		}
	})
	t.Run("passes run.failed from one flow to another", func(t *testing.T) {
		h := newHarness(t, harnessOptions{connectors: exampleConnectors(map[string]runFn{"reminders__list_reminders": fails("down")})})
		a := h.create(`{"name":"A","steps":[{"id":"t","kind":"tool","tool":"reminders__list_reminders","retry":{"max":0,"backoffSec":5}}]}`).Flow
		b := h.create(`{"name":"B","triggers":[{"id":"f","kind":"event","event":"run.failed","flow":"A","start":"m"}],"steps":[{"id":"m","kind":"message","text":"A gagal: {{trigger.error}}"}]}`).Flow
		h.start(a.ID, "manual", "m1", "")
		h.run(h.now())
		r := runsOf(t, h.db, b.ID)[0]
		ev, _ := r.Trigger.Get("event")
		er, _ := r.Trigger.Get("error")
		if r.Status != "done" || ev != "run.failed" || er != "Step t: down" {
			t.Errorf("run %s %v %v", r.Status, ev, er)
		}
	})
}

func TestMessageToAnotherChat(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	family, err := chats.Ensure(h.db, chats.EnsureInput{Channel: "telegram", ExternalID: "-1001", Kind: "group", Title: "Keluarga", Allowed: ptrTrue()}, h.now())
	tu.Must(t, err)
	saved := h.create(fmt.Sprintf(`{"name":"Kabar","steps":[{"id":"m","kind":"message","text":"Halo keluarga","to":%q}]}`, family.ID))
	if !saved.NeedsApproval {
		t.Fatal("no approval needed")
	}
	eq(t, saved.WriteCalls, js(fmt.Sprintf(`[{"stepId":"m","kind":"message","to":%q}]`, family.ID)), "write calls")
	h.start(saved.Flow.ID, "manual", "m1", "")
	h.run(h.now())
	eq(t, runsOf(t, h.db, saved.Flow.ID)[0].Status, "awaiting_approval", "status")
	_, err = flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
	tu.Must(t, err)
	h.run(h.now())
	n := 0
	for _, s := range texts(t, h.db) {
		if s == "Halo keluarga" {
			n++
		}
	}
	eq(t, n, 1, "sent once")
}

func addZaki(t *testing.T, h *harness) db.Contact {
	t.Helper()
	a, err := agents.GetDefault(h.db, h.now())
	tu.Must(t, err)
	c, err := outbound.AddContact(h.db, outbound.AddContactInput{AgentID: a.ID, Name: "Zaki", Phone: "082200001001"}, h.now())
	tu.Must(t, err)
	return c
}

func outboxTo(t *testing.T, q db.Q, chatID string) []db.Outbox {
	t.Helper()
	rows, err := q.Query("SELECT "+db.OutboxCols+" FROM outbox WHERE chat_id = ? ORDER BY rowid", chatID)
	list, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(t, err)
	return list
}

func outboxTexts(list []db.Outbox) []string {
	out := []string{}
	for _, r := range list {
		out = append(out, r.Payload.Text)
	}
	return out
}

func lastVersionPrompt(t *testing.T, q db.Q) db.Outbox {
	t.Helper()
	rows, err := q.Query("SELECT " + db.OutboxCols + " FROM outbox WHERE source LIKE 'flowver:%' ORDER BY rowid")
	list, err := db.All(rows, err, db.ScanOutbox)
	tu.Must(t, err)
	return list[len(list)-1]
}

func TestMessageToContact(t *testing.T) {
	sig := settings.DefaultOutboundSignature
	t.Run("needs the version approval even for an always-allow contact, then sends through the outbound log", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		zaki := addZaki(t, h)
		allow := "allow"
		_, err := outbound.UpdateContact(h.db, zaki.ID, outbound.ContactPatch{Policy: &allow}, h.now())
		tu.Must(t, err)
		saved := h.create(`{"name":"Kabar Zaki","steps":[{"id":"m","kind":"message","text":"Zaki belum jawab check-in.","contact":"Zaki"}]}`)
		if !saved.NeedsApproval {
			t.Fatal("no approval needed")
		}
		eq(t, saved.WriteCalls, js(fmt.Sprintf(`[{"stepId":"m","kind":"message","to":"contact:%s","label":"Zaki"}]`, zaki.ID)), "write calls")
		h.start(saved.Flow.ID, "manual", "m1", "")
		h.run(h.now())
		eq(t, runsOf(t, h.db, saved.Flow.ID)[0].Status, "awaiting_approval", "status")
		_, err = flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		h.run(h.now())
		sent := outboxTo(t, h.db, zaki.ChatID)
		eq(t, outboxTexts(sent), []string{"Zaki belum jawab check-in.\n\n" + sig}, "sent")
		match(t, sent[0].Source, "^outbound:obm_")
		var origin, approvedBy, contactID string
		tu.Must(t, h.db.QueryRow("SELECT origin, approved_by, contact_id FROM outbound_messages").Scan(&origin, &approvedBy, &contactID))
		if origin != "flow" || approvedBy != "flow_version" || contactID != zaki.ID {
			t.Errorf("outbound row %s %s %s", origin, approvedBy, contactID)
		}
	})
	t.Run("signs the message, and the version approval says so", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		zaki := addZaki(t, h)
		saved := h.create(`{"name":"Kabar","steps":[{"id":"m","kind":"message","text":"halo","contact":"Zaki"}]}`)
		contains(t, lastVersionPrompt(t, h.db).Payload.Text, "Pesan ke kontak dikirim dengan tanda tangan dari halaman Outbound.", "prompt")
		_, err := flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		h.start(saved.Flow.ID, "manual", "m1", "")
		h.run(h.now())
		run := runsOf(t, h.db, saved.Flow.ID)[0]
		steps, err := flows.StepRunsOf(h.db, run.ID)
		tu.Must(t, err)
		eq(t, field(steps[0].Output, "text"), "halo\n\n"+sig, "output")
		rows, err := h.db.Query("SELECT " + db.OutboxCols + " FROM outbox WHERE source LIKE 'outbound-notice:%'")
		notices, err := db.All(rows, err, db.ScanOutbox)
		tu.Must(t, err)
		for _, n := range notices {
			contains(t, n.Payload.Text, sig, "notice")
		}
		eq(t, outboxTexts(outboxTo(t, h.db, zaki.ChatID)), []string{"halo\n\n" + sig}, "sent")
	})
	t.Run("a changed signature text needs no new approval and goes out on the next run", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		zaki := addZaki(t, h)
		saved := h.create(`{"name":"Kabar","steps":[{"id":"m","kind":"message","text":"halo","contact":"Zaki"}]}`)
		_, err := flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		d := settings.Defaults().Outbound
		_, err = outbound.SaveSettings(h.db, outbound.SettingsPatch{PerContactPerDay: d.PerContactPerDay, PerDay: d.PerDay, HoldSeconds: d.HoldSeconds, SignatureOn: d.SignatureOn, Signature: "_Asisten Zaki_"}, h.now())
		tu.Must(t, err)
		h.start(saved.Flow.ID, "manual", "m1", "")
		h.run(h.now())
		eq(t, outboxTexts(outboxTo(t, h.db, zaki.ChatID)), []string{"halo\n\n_Asisten Zaki_"}, "sent")
	})
	t.Run("signature: false sends it bare, and dropping the signature is a change the owner approves", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		zaki := addZaki(t, h)
		signed := h.create(`{"name":"Kabar","steps":[{"id":"m","kind":"message","text":"halo","contact":"Zaki"}]}`)
		_, err := flows.DecideVersion(h.db, signed.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		bare, err := flows.UpdateFlow(h.db, signed.Flow.ID, flows.UpdateInput{Definition: js(`{"name":"Kabar","steps":[{"id":"m","kind":"message","text":"halo","contact":"Zaki","signature":false}]}`), CreatedBy: "test"}, h.now())
		tu.Must(t, err)
		if !bare.NeedsApproval {
			t.Fatal("no approval needed")
		}
		eq(t, bare.WriteCalls, js(fmt.Sprintf(`[{"stepId":"m","kind":"message","to":"contact:%s","label":"Zaki","signature":false}]`, zaki.ID)), "write calls")
		prompt := lastVersionPrompt(t, h.db).Payload.Text
		contains(t, prompt, "kirim pesan ke kontak Zaki (tanpa tanda tangan)", "prompt")
		if strings.Contains(prompt, "dengan tanda tangan") {
			t.Error("prompt mentions the signature")
		}
		_, err = flows.DecideVersion(h.db, bare.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		h.start(signed.Flow.ID, "manual", "m1", "")
		h.run(h.now())
		eq(t, outboxTexts(outboxTo(t, h.db, zaki.ChatID)), []string{"halo"}, "sent")
	})
	t.Run("refuses signature on a message that isn't to a contact", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		_, err := h.createErr(`{"name":"A","steps":[{"id":"m","kind":"message","text":"x","signature":false}]}`)
		if err == nil || !strings.Contains(err.Error(), "signature only applies") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("refuses unknown contacts, templates, and to plus contact", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		addZaki(t, h)
		for def, want := range map[string]string{
			`{"name":"A","steps":[{"id":"m","kind":"message","text":"x","contact":"Budi"}]}`:                                     "No saved contact Budi",
			`{"name":"B","steps":[{"id":"m","kind":"message","text":"x","contact":"{{vars.who}}"}]}`:                             "message.contact is a literal",
			fmt.Sprintf(`{"name":"C","steps":[{"id":"m","kind":"message","text":"x","contact":"Zaki","to":%q}]}`, h.otherChatID): "not both",
		} {
			_, err := h.createErr(def)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v", want, err)
			}
		}
	})
	t.Run("fails the step when the contact was removed or the kill switch is off", func(t *testing.T) {
		h := newHarness(t, harnessOptions{})
		zaki := addZaki(t, h)
		saved := h.create(`{"name":"Kabar","steps":[{"id":"m","kind":"message","text":"halo","contact":"Zaki"}]}`)
		_, err := flows.DecideVersion(h.db, saved.Version.ID, true, "test", h.now())
		tu.Must(t, err)
		_, err = outbound.SetEnabled(h.db, false, h.now())
		tu.Must(t, err)
		h.start(saved.Flow.ID, "manual", "m1", "")
		h.run(h.now())
		match(t, stepLog(t, h.db, runsOf(t, h.db, saved.Flow.ID)[0].ID)[0].Error, "turned off")
		_, err = outbound.SetEnabled(h.db, true, h.now())
		tu.Must(t, err)
		_, err = outbound.RemoveContact(h.db, zaki.ID, h.now())
		tu.Must(t, err)
		h.start(saved.Flow.ID, "manual", "m2", "")
		h.run(h.now())
		match(t, stepLog(t, h.db, runsOf(t, h.db, saved.Flow.ID)[1].ID)[0].Error, "removed")
		eq(t, len(outboxTo(t, h.db, zaki.ChatID)), 0, "sent")
	})
}

func ptrTrue() *bool { b := true; return &b }
