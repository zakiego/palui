package rpcpeople_test

import (
	"reflect"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi/rpcpeople"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestTranscriptShowsMediaUnderTheRunsReply(t *testing.T) {
	d := testutil.DB(t)
	c, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 1)
	testutil.Must(t, err)
	for _, m := range [][]any{{"msg_1", "user", "kirim gambarnya", nil, 1}, {"msg_2", "assistant", "Ini dia", "run_1", 3}, {"msg_3", "assistant", "lagi", "run_1", 4}} {
		_, err := d.Exec("INSERT INTO messages (id, chat_id, session_id, role, content, text, status, run_id, created_at) VALUES (?, ?, ?, ?, '{}', ?, 'handled', ?, ?)", m[0], c.ID, c.SessionID, m[1], m[2], m[3], m[4])
		testutil.Must(t, err)
	}
	enqueue := func(source string, media *db.OutboxMedia, caption string) db.Outbox {
		r, err := outbox.Enqueue(d, outbox.EnqueueInput{ChatID: c.ID, Channel: "telegram", Payload: db.OutboxPayload{Text: caption, Media: media}, Source: source}, 2)
		testutil.Must(t, err)
		return r
	}
	sent := enqueue("media:run_1", &db.OutboxMedia{ID: "med_1", Kind: db.MediaImage, MimeType: "image/jpeg", Filename: "inhaler.jpg", Size: 10}, "Inhaler + spacer")
	_, err = d.Exec("UPDATE outbox SET status = 'sent' WHERE id = ?", sent.ID)
	testutil.Must(t, err)
	enqueue("media:run_1", &db.OutboxMedia{ID: "med_2", Kind: db.MediaVoice, MimeType: "audio/ogg", Filename: "cara-pakai.ogg"}, "")
	enqueue("reply:run_1", nil, "Ini dia")
	enqueue("media:run_other", &db.OutboxMedia{ID: "med_3", Kind: db.MediaImage}, "")
	got, err := rpcpeople.TranscriptMedia(d, c.ID)
	testutil.Must(t, err)
	want := map[string][]string{"msg_2": {"image inhaler.jpg Inhaler + spacer sent", "voice cara-pakai.ogg  pending"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
