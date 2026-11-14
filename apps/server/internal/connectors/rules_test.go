package connectors_test

// connectors/rules.ts has no test file of its own; these cover its rules.

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestOwnerChat(t *testing.T) {
	d := testutil.DB(t)
	if _, ok, _ := connectors.OwnerChat(d, "telegram", 1); ok {
		t.Fatal("no owner yet")
	}
	if _, ok, _ := connectors.OwnerChat(d, "web", 1); ok {
		t.Fatal("no web chat yet")
	}
	web, err := chats.EnsureWebChat(d, 1)
	testutil.Must(t, err)
	if c, ok, _ := connectors.OwnerChat(d, "web", 1); !ok || c.ID != web.ID {
		t.Fatal("web chat")
	}
	_, err = owner.SetWhatsAppOwner(d, "08123456789", 1)
	testutil.Must(t, err)
	made, ok, err := connectors.OwnerChat(d, "whatsapp", 2)
	if err != nil || !ok || made.ExternalID != "628123456789@s.whatsapp.net" || made.Kind != "dm" {
		t.Fatalf("%+v %v", made, err)
	}
	again, _, _ := connectors.OwnerChat(d, "whatsapp", 3)
	if again.ID != made.ID {
		t.Fatal("made twice")
	}
}

func TestSelfChannelOffUndo(t *testing.T) {
	d := testutil.DB(t)
	tg := conntest.NewFakeChannel("telegram")
	wa := conntest.NewFakeChannel("whatsapp")
	web := conntest.NewFakeChannel("web")
	running := []connectors.ChannelEntry{{ID: "telegram", Kind: "channel", Channel: tg}, {ID: "whatsapp", Kind: "channel", Channel: wa}, {ID: "web", Kind: "channel", Channel: web}, {ID: "notes", Kind: "tools"}}
	way, ok, err := connectors.SelfChannelOffUndo(d, "telegram", running, "telegram")
	testutil.Must(t, err)
	if !ok || way != "kirim `/connectors on telegram` dari WhatsApp" {
		t.Fatal(way)
	}
	wa.Up = false
	if _, ok, _ := connectors.SelfChannelOffUndo(d, "telegram", running, "telegram"); ok {
		t.Fatal("would lock the owner out")
	}
	_, err = d.Exec("INSERT INTO user (id, name, email, email_verified, created_at, updated_at) VALUES ('u1', 'Owner', 'o@x', 1, 1, 1)")
	testutil.Must(t, err)
	_, err = d.Exec("INSERT INTO passkey (id, public_key, user_id, credential_id, counter, device_type, backed_up) VALUES ('pk1', 'k', 'u1', 'c', 0, 'singleDevice', 0)")
	testutil.Must(t, err)
	wa.Up = true
	way, ok, _ = connectors.SelfChannelOffUndo(d, "telegram", running, "telegram")
	if !ok || way != "kirim `/connectors on telegram` dari WhatsApp, atau nyalakan lagi dari dashboard" {
		t.Fatal(way)
	}
	for ch, want := range map[string]string{"telegram": "Telegram", "whatsapp": "WhatsApp", "web": "web chat", "x": "x"} {
		if connectors.ChannelName(ch) != want {
			t.Fatal(ch)
		}
	}
}
