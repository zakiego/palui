package chats

import (
	"github.com/zakiego/palui/apps/server/internal/db"
)

// LinkWhatsAppDM keeps one chat per WhatsApp person. WhatsApp addresses a
// person by a phone JID (628...@s.whatsapp.net) or a LID (...@lid), and an
// inbound DM can arrive on either one while the chat was made on the other
// (an outbound contact added by number, then that person writes from their
// LID). Called with the id the message came on and the sender's other id,
// before the chat lookup:
//
//   - no chat on jid, one on alt: that chat moves to jid.
//   - a chat on both: when one of them was never used (no messages, never
//     allowed, nothing else points at it), it folds into the other, which
//     moves to jid. Two used chats stay apart.
//
// Returns whether anything changed.
func LinkWhatsAppDM(q db.Q, connectorID, jid, alt string) (bool, error) {
	if jid == "" || alt == "" || jid == alt {
		return false, nil
	}
	changed := false
	err := q.Tx(func(tx db.Q) error {
		onAlt, ok, err := dmOn(tx, connectorID, alt)
		if err != nil || !ok {
			return err
		}
		onJid, ok, err := dmOn(tx, connectorID, jid)
		if err != nil {
			return err
		}
		keep := onAlt
		if ok {
			altBare, err := unused(tx, onAlt)
			if err != nil {
				return err
			}
			jidBare, err := unused(tx, onJid)
			if err != nil {
				return err
			}
			drop := onJid
			switch {
			case jidBare:
				// The alt chat is the one the owner set up (or both are bare).
			case altBare:
				keep, drop = onJid, onAlt
			default:
				return nil
			}
			if _, err := tx.Exec("UPDATE contacts SET chat_id = ? WHERE chat_id = ?", keep.ID, drop.ID); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM sessions WHERE chat_id = ?", drop.ID); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM chats WHERE id = ?", drop.ID); err != nil {
				return err
			}
			changed = true
		}
		if keep.ExternalID != jid {
			if _, err := tx.Exec("UPDATE chats SET external_id = ? WHERE id = ?", jid, keep.ID); err != nil {
				return err
			}
			changed = true
		}
		return nil
	})
	return changed, err
}

func dmOn(q db.Q, connectorID, externalID string) (db.Chat, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.ChatCols+" FROM chats WHERE channel = 'whatsapp' AND connector_id = ? AND external_id = ? AND kind = 'dm'", connectorID, externalID), db.ScanChat)
}

// chatRefs are the columns that point at a chat, besides sessions and
// contacts (which a fold moves or drops).
var chatRefs = [][2]string{
	{"messages", "chat_id"},
	{"hooks", "chat_id"},
	{"jobs", "chat_id"},
	{"outbox", "chat_id"},
	{"pending_approvals", "chat_id"},
	{"pending_approvals", "ask_chat_id"},
	{"reminders", "chat_id"},
	{"runs", "chat_id"},
	{"saved_attachments", "chat_id"},
	{"routine_occurrences", "chat_id"},
	{"routines", "chat_id"},
	{"flow_runs", "chat_id"},
	{"flows", "chat_id"},
	{"reports", "chat_id"},
	{"outbound_messages", "owner_chat_id"},
	{"chat_rules", "chat_id"},
	{"chat_rule_changes", "chat_id"},
}

// unused: a chat row nobody ever talked in or set up. Its sessions may exist
// (Ensure opens one) but hold no summary.
func unused(q db.Q, c db.Chat) (bool, error) {
	if c.Allowed || c.LastMessageAt != nil {
		return false, nil
	}
	for _, r := range chatRefs {
		var n int
		if err := q.QueryRow("SELECT EXISTS (SELECT 1 FROM "+r[0]+" WHERE "+r[1]+" = ?)", c.ID).Scan(&n); err != nil {
			return false, err
		}
		if n != 0 {
			return false, nil
		}
	}
	var n int
	if err := q.QueryRow("SELECT EXISTS (SELECT 1 FROM sessions WHERE chat_id = ? AND summary IS NOT NULL)", c.ID).Scan(&n); err != nil {
		return false, err
	}
	if n != 0 {
		return false, nil
	}
	// The deploy notice list names chats by id.
	if err := q.QueryRow("SELECT EXISTS (SELECT 1 FROM settings WHERE value LIKE ?)", "%\""+c.ID+"\"%").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}
