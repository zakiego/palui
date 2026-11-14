// Package testutil holds helpers shared by the Go tests (the counterparts of
// packages/core/src/chat/testing.ts and reminders/test-helpers.ts).
package testutil

import (
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// TestKey is TEST_KEY in chat/testing.ts.
const TestKey = "test-master-key-test-master-key-1234"

// NOW is Tuesday 2026-11-10 10:00 WITA (Asia/Makassar, UTC+8).
var NOW = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC).UnixMilli()

const (
	WITA = "Asia/Makassar"
	MIN  = int64(60_000)
	HOUR = int64(3600_000)
)

// DB is a migrated in-memory database closed when the test ends.
func DB(t testing.TB) *db.DB {
	t.Helper()
	d := db.Memory()
	t.Cleanup(func() { d.Close() })
	return d
}

// Setup is reminders/test-helpers.ts setup(): a Telegram chat and a WhatsApp chat.
func Setup(t testing.TB) (d *db.DB, chatID, otherChatID string) {
	t.Helper()
	d = DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, NOW)
	Must(t, err)
	other, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "628123@s.whatsapp.net"}, NOW)
	Must(t, err)
	return d, chat.ID, other.ID
}

// SeedOptions are seedConnector's options.
type SeedOptions struct {
	Type    string
	Enabled *bool
	Status  string
	Config  *jsonx.Object
	Secrets *jsonx.Map[string]
	Trust   string
}

// SeedConnector is seedConnector: a connector row in the given state,
// bypassing the wizard.
func SeedConnector(t testing.TB, q db.Q, id, kind string, opts SeedOptions, now int64) {
	t.Helper()
	typ := opts.Type
	if typ == "" {
		typ = id
	}
	enabled := true
	if opts.Enabled != nil {
		enabled = *opts.Enabled
	}
	var trust *string
	if opts.Trust != "" {
		trust = &opts.Trust
	}
	_, err := connectors.Upsert(q, connectors.UpsertInput{ID: id, Manifest: connectors.RowManifest{ID: typ, Kind: kind, Name: id}, Enabled: &enabled, Config: opts.Config, Secrets: opts.Secrets, Trust: trust}, TestKey, now)
	Must(t, err)
	status := opts.Status
	if status == "" {
		status = "on"
		if !enabled {
			status = "paused"
		}
	}
	_, err = q.Exec("UPDATE connectors SET status = ? WHERE id = ?", status, id)
	Must(t, err)
}

// Must fails the test on err.
func Must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }
