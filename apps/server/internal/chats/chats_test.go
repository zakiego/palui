package chats_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// chats.ts has no test file of its own (owner.test.ts covers Ensure and
// RotateSession); these pin search, read and resolve.

func TestSearchReadAndResolve(t *testing.T) {
	d, chatID, otherID := testutil.Setup(t)
	_, err := d.Exec("UPDATE chats SET title = 'Grup 50%_off', last_message_at = 5 WHERE id = ?", chatID)
	testutil.Must(t, err)
	for i, role := range []string{"user", "assistant", "tool", "user"} {
		_, err := d.Exec("INSERT INTO messages (id, chat_id, role, content, text, created_at) VALUES (?, ?, ?, '{}', ?, ?)", secret.NewID("msg"), chatID, role, "pesan "+role, int64(i+1))
		testutil.Must(t, err)
	}
	hits, err := chats.SearchChats(d, chats.Search{})
	testutil.Must(t, err)
	if len(hits) != 2 || hits[0].ID != chatID || hits[0].MessageCount != 3 || hits[1].ID != otherID {
		t.Fatalf("%+v", hits)
	}
	if hits, _ := chats.SearchChats(d, chats.Search{Query: "%_"}); len(hits) != 1 || hits[0].ID != chatID {
		t.Fatalf("%+v", hits)
	}
	if hits, _ := chats.SearchChats(d, chats.Search{Channel: "whatsapp"}); len(hits) != 1 || hits[0].ID != otherID {
		t.Fatalf("%+v", hits)
	}
	if hits, _ := chats.SearchChats(d, chats.Search{Scope: &chats.Cond{SQL: "id = ?", Args: []any{otherID}}}); len(hits) != 1 {
		t.Fatalf("%+v", hits)
	}
	msgs, err := chats.ReadMessages(d, chatID, chats.MessageQuery{Limit: 2})
	testutil.Must(t, err)
	if len(msgs) != 2 || msgs[0].CreatedAt != 2 || msgs[1].CreatedAt != 4 {
		t.Fatalf("%+v", msgs)
	}
	if msgs, _ := chats.ReadMessages(d, chatID, chats.MessageQuery{Query: "ASSISTANT"}); len(msgs) != 1 {
		t.Fatalf("%+v", msgs)
	}
	if msgs, _ := chats.ReadMessages(d, chatID, chats.MessageQuery{Before: testutil.Ptr(int64(4)), Since: testutil.Ptr(int64(2))}); len(msgs) != 1 || msgs[0].CreatedAt != 2 {
		t.Fatalf("%+v", msgs)
	}
	if r, _ := chats.Resolve(d, "42", ""); len(r) != 1 || r[0].ID != chatID {
		t.Fatalf("%+v", r)
	}
	if r, _ := chats.Resolve(d, chatID, "whatsapp"); len(r) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestAckAndReplyMode(t *testing.T) {
	d, chatID, _ := testutil.Setup(t)
	c, _, err := chats.SetAck(d, chatID, chats.AckPatch{On: testutil.Ptr(false), Emoji: testutil.Ptr("👀")})
	testutil.Must(t, err)
	if c.Ack || c.AckEmoji != "👀" {
		t.Fatalf("%+v", c)
	}
	if c, _, _ := chats.SetAck(d, chatID, chats.AckPatch{}); c.ID != chatID {
		t.Fatal("empty patch")
	}
	if c, _, _ := chats.SetReplyMode(d, chatID, "all"); c.ReplyMode != "all" {
		t.Fatal(c.ReplyMode)
	}
	web, err := chats.EnsureWebChat(d, 1)
	testutil.Must(t, err)
	if web.Channel != "web" || web.Title != "Palui (web)" || web.ExternalID != "web" {
		t.Fatalf("%+v", web)
	}
}
