package outbox_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestEnqueueWritesAPendingRowWithItsOptions(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	exp, notBefore := int64(9000), int64(500)
	row, err := outbox.Enqueue(d, outbox.EnqueueInput{ChatID: chatID, Channel: "telegram", ConnectorID: "tgbot-x", Payload: db.OutboxPayload{Text: "hi", Actions: []db.OutboxAction{{ID: "rem:done:r1", Label: "Selesai"}}, Format: "markdown"}, Source: "reminder:r1", ExpiresAt: &exp, NotBefore: &notBefore}, 100)
	testutil.Must(t, err)
	if row.NextAttemptAt != 500 || *row.ExpiresAt != 9000 || *row.ConnectorID != "tgbot-x" || row.Source != "reminder:r1" || row.CreatedAt != 100 {
		t.Fatalf("%+v", row)
	}
	var raw string
	testutil.Must(t, d.QueryRow("SELECT payload FROM outbox WHERE id = ?", row.ID).Scan(&raw))
	if raw != `{"text":"hi","actions":[{"id":"rem:done:r1","label":"Selesai"}],"format":"markdown"}` {
		t.Fatal(raw)
	}
}

func TestListenersArePoked(t *testing.T) {
	n := 0
	off := outbox.OnEnqueued(func() { n++ })
	outbox.Notify()
	off()
	outbox.Notify()
	if n != 1 {
		t.Fatal(n)
	}
}
