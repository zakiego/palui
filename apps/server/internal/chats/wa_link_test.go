package chats_test

import (
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	phoneJID = "628999@s.whatsapp.net"
	lid      = "444@lid"
)

func waDM(t *testing.T, d db.Q, jid string, allowed bool) db.Chat {
	t.Helper()
	c, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: jid, Kind: "dm", Allowed: &allowed}, testutil.NOW)
	testutil.Must(t, err)
	return c
}

func chatCount(t *testing.T, d db.Q) int {
	t.Helper()
	var n int
	testutil.Must(t, d.QueryRow("SELECT COUNT(*) FROM chats").Scan(&n))
	return n
}

func externalID(t *testing.T, d db.Q, id string) string {
	t.Helper()
	c, ok, err := chats.Get(d, id)
	testutil.Must(t, err)
	if !ok {
		return ""
	}
	return c.ExternalID
}

func TestLinkWhatsAppDM(t *testing.T) {
	t.Run("moves the phone chat to the LID when the LID has none", func(t *testing.T) {
		d := testutil.DB(t)
		phone := waDM(t, d, phoneJID, false)
		changed, err := chats.LinkWhatsAppDM(d, "", lid, phoneJID)
		testutil.Must(t, err)
		if !changed || externalID(t, d, phone.ID) != lid || chatCount(t, d) != 1 {
			t.Fatalf("changed=%v ext=%q n=%d", changed, externalID(t, d, phone.ID), chatCount(t, d))
		}
		// Running again is a no-op.
		if changed, _ := chats.LinkWhatsAppDM(d, "", lid, phoneJID); changed {
			t.Fatal("changed twice")
		}
	})

	t.Run("folds an unused LID chat into the contact's phone chat", func(t *testing.T) {
		d := testutil.DB(t)
		phone := waDM(t, d, phoneJID, false)
		stray := waDM(t, d, lid, false)
		_, err := d.Exec("INSERT INTO contacts (id, name, channel, address, chat_id, created_at, updated_at) VALUES ('con_1', 'Guest', 'whatsapp', '628999', ?, 1, 1)", phone.ID)
		testutil.Must(t, err)
		changed, err := chats.LinkWhatsAppDM(d, "", lid, phoneJID)
		testutil.Must(t, err)
		if !changed || chatCount(t, d) != 1 || externalID(t, d, phone.ID) != lid || externalID(t, d, stray.ID) != "" {
			t.Fatalf("n=%d phone=%q", chatCount(t, d), externalID(t, d, phone.ID))
		}
		var sessions int
		testutil.Must(t, d.QueryRow("SELECT COUNT(*) FROM sessions WHERE chat_id = ?", stray.ID).Scan(&sessions))
		if sessions != 0 {
			t.Fatal("left the stray session")
		}
	})

	t.Run("folds an unused phone chat into the LID chat in use and moves its contact", func(t *testing.T) {
		d := testutil.DB(t)
		phone := waDM(t, d, phoneJID, false)
		used := waDM(t, d, lid, true)
		_, err := d.Exec("INSERT INTO contacts (id, name, channel, address, chat_id, created_at, updated_at) VALUES ('con_1', 'Guest', 'whatsapp', '628999', ?, 1, 1)", phone.ID)
		testutil.Must(t, err)
		_, err = chats.LinkWhatsAppDM(d, "", lid, phoneJID)
		testutil.Must(t, err)
		if chatCount(t, d) != 1 || externalID(t, d, used.ID) != lid {
			t.Fatalf("n=%d", chatCount(t, d))
		}
		var chatID string
		testutil.Must(t, d.QueryRow("SELECT chat_id FROM contacts WHERE id = 'con_1'").Scan(&chatID))
		if chatID != used.ID {
			t.Fatalf("contact still on %s", chatID)
		}
	})

	t.Run("leaves two chats that both have messages", func(t *testing.T) {
		d := testutil.DB(t)
		phone := waDM(t, d, phoneJID, false)
		other := waDM(t, d, lid, false)
		for _, c := range []db.Chat{phone, other} {
			_, err := d.Exec("INSERT INTO messages (id, chat_id, role, content, text, created_at) VALUES (?, ?, 'user', '{}', 'halo', 1)", secret.NewID("msg"), c.ID)
			testutil.Must(t, err)
		}
		changed, err := chats.LinkWhatsAppDM(d, "", lid, phoneJID)
		testutil.Must(t, err)
		if changed || chatCount(t, d) != 2 || externalID(t, d, phone.ID) != phoneJID {
			t.Fatalf("changed=%v n=%d", changed, chatCount(t, d))
		}
	})

	t.Run("ignores groups and a missing alt", func(t *testing.T) {
		d := testutil.DB(t)
		waDM(t, d, phoneJID, false)
		if changed, _ := chats.LinkWhatsAppDM(d, "", lid, ""); changed {
			t.Fatal("changed without an alt")
		}
		if changed, _ := chats.LinkWhatsAppDM(d, "", lid, "123@g.us"); changed {
			t.Fatal("changed for a group id")
		}
	})
}
