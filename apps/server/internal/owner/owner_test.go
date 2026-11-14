package owner_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/owner.test.ts.

func claim(t *testing.T, d db.Q, channel, nonce, ext string, name *string, now int64) bool {
	t.Helper()
	ok, err := owner.ClaimOwner(d, owner.ClaimInput{Channel: channel, Nonce: nonce, ExternalID: ext, DisplayName: name}, now)
	testutil.Must(t, err)
	return ok
}

func isOwner(t *testing.T, d db.Q, s owner.Sender) bool {
	t.Helper()
	ok, err := owner.IsOwner(d, s)
	testutil.Must(t, err)
	return ok
}

func TestTelegramClaimAcceptsAFreshNonceOnceAndOnlyOnItsChannel(t *testing.T) {
	d := testutil.DB(t)
	c, err := owner.CreateClaim(d, "telegram", 1000)
	testutil.Must(t, err)
	if claim(t, d, "whatsapp", c.Nonce, "1", nil, 2000) {
		t.Fatal("wrong channel")
	}
	if !claim(t, d, "telegram", c.Nonce, "42", testutil.Ptr("Zaki"), 2000) {
		t.Fatal("refused")
	}
	if claim(t, d, "telegram", c.Nonce, "43", nil, 3000) {
		t.Fatal("reused")
	}
	o, _, _ := owner.GetOwner(d, "telegram")
	if o.ExternalID != "42" || *o.DisplayName != "Zaki" {
		t.Fatalf("%+v", o)
	}
	if !isOwner(t, d, owner.Sender{Channel: "telegram", ExternalID: "42"}) || isOwner(t, d, owner.Sender{Channel: "telegram", ExternalID: "43"}) {
		t.Fatal("isOwner")
	}
}

func TestTelegramClaimRejectsAnExpiredNonce(t *testing.T) {
	d := testutil.DB(t)
	c, _ := owner.CreateClaim(d, "telegram", 1000)
	if claim(t, d, "telegram", c.Nonce, "42", nil, c.ExpiresAt) {
		t.Fatal("expired accepted")
	}
}

func TestPhoneToJid(t *testing.T) {
	if owner.PhoneToJid("0812-3456-789") != "628123456789@s.whatsapp.net" || owner.PhoneToJid("+62 812 3456 789") != "628123456789@s.whatsapp.net" {
		t.Fatal("normalize")
	}
}

func TestWhatsAppMatchesJidLearnsLidThenMatchesLidOnly(t *testing.T) {
	d := testutil.DB(t)
	_, err := owner.SetWhatsAppOwner(d, "08123456789", 1)
	testutil.Must(t, err)
	if isOwner(t, d, owner.Sender{Channel: "whatsapp", ExternalID: "999@lid"}) {
		t.Fatal("stranger")
	}
	if !isOwner(t, d, owner.Sender{Channel: "whatsapp", ExternalID: "555@lid", AltID: "628123456789@s.whatsapp.net"}) {
		t.Fatal("alt id")
	}
	o, _, _ := owner.GetOwner(d, "whatsapp")
	if o.AltID == nil || *o.AltID != "555@lid" {
		t.Fatal("lid not learned")
	}
	if !isOwner(t, d, owner.Sender{Channel: "whatsapp", ExternalID: "555@lid"}) || isOwner(t, d, owner.Sender{Channel: "whatsapp", ExternalID: "628999@s.whatsapp.net"}) {
		t.Fatal("lid only")
	}
	if !isOwner(t, d, owner.Sender{Channel: "web", ExternalID: "anyone"}) {
		t.Fatal("web")
	}
}

func TestCreatesAChatWithASessionRotatesItAndEnqueues(t *testing.T) {
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42", Title: "Zaki"}, 1)
	testutil.Must(t, err)
	if chat.SessionID == nil {
		t.Fatal("no session")
	}
	again, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 2)
	if again.ID != chat.ID {
		t.Fatal("new chat")
	}
	r, err := chats.RotateSession(d, chat.ID, 5000)
	testutil.Must(t, err)
	if r.Closed == nil || *r.Closed.ClosedAt != 5000 || r.Opened.ID == *chat.SessionID {
		t.Fatalf("%+v", r)
	}
	row, err := outbox.Enqueue(d, outbox.EnqueueInput{ChatID: chat.ID, Channel: "telegram", Payload: db.OutboxPayload{Text: "hi"}, Source: "system"}, 100)
	testutil.Must(t, err)
	if row.Status != "pending" || row.Attempts != 0 || row.NextAttemptAt != 100 || row.ConnectorID != nil {
		t.Fatalf("%+v", row)
	}
	var raw string
	testutil.Must(t, d.QueryRow("SELECT payload FROM outbox WHERE id = ?", row.ID).Scan(&raw))
	if raw != `{"text":"hi"}` {
		t.Fatal(raw)
	}
}
