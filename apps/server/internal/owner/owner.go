// Package owner ports packages/core/src/owner.ts: channel ownership. Only the
// owner gets answers; everyone else is dropped silently with the reason logged.
//
// Telegram: claimed with a single-use nonce link (t.me/<bot>?start=<nonce>, 10 min).
// WhatsApp: the owner's phone number is entered in the wizard; the LID is learned
// from the first DM whose alternate id matches that number, then both are stored.
package owner

import (
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

const ClaimTTLMs = 10 * 60_000

type Claim struct {
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"expiresAt"`
}

func CreateClaim(q db.Q, channel string, now int64) (Claim, error) {
	nonce := secret.RandomToken(12)
	expiresAt := now + ClaimTTLMs
	_, err := q.Exec("INSERT INTO claims (nonce, channel, expires_at, created_at) VALUES (?, ?, ?, ?)", nonce, channel, expiresAt, now)
	return Claim{Nonce: nonce, ExpiresAt: expiresAt}, err
}

type ClaimInput struct {
	Channel     string
	Nonce       string
	ExternalID  string
	DisplayName *string
}

// ClaimOwner takes a single-use, unexpired claim on the right channel. It marks
// the claim used and stores the owner identity in one transaction. A new claim
// replaces any previous owner on that channel.
func ClaimOwner(q db.Q, in ClaimInput, now int64) (bool, error) {
	claimed := false
	err := q.Tx(func(tx db.Q) error {
		n, err := db.Changes(tx.Exec("UPDATE claims SET used_at = ?, used_by = ? WHERE nonce = ? AND channel = ? AND used_at IS NULL AND expires_at > ?", now, in.ExternalID, in.Nonce, in.Channel, now))
		if err != nil || n == 0 {
			return err
		}
		if _, err := tx.Exec("DELETE FROM identities WHERE channel = ?", in.Channel); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO identities (id, channel, external_id, display_name, role, created_at) VALUES (?, ?, ?, ?, 'owner', ?)", secret.NewID("idn"), in.Channel, in.ExternalID, in.DisplayName, now); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}

// PhoneToJid normalizes a phone number to a WhatsApp phone JID: digits only,
// a leading 0 becomes 62.
func PhoneToJid(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if strings.HasPrefix(digits, "0") {
		digits = "62" + digits[1:]
	}
	return digits + "@s.whatsapp.net"
}

func SetWhatsAppOwner(q db.Q, phone string, now int64) (db.Identity, error) {
	jid := PhoneToJid(phone)
	var out db.Identity
	err := q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec("DELETE FROM identities WHERE channel = 'whatsapp'"); err != nil {
			return err
		}
		row, err := db.ScanIdentity(tx.WQueryRow("INSERT INTO identities (id, channel, external_id, role, created_at) VALUES (?, 'whatsapp', ?, 'owner', ?) RETURNING "+db.IdentityCols, secret.NewID("idn"), jid, now))
		out = row
		return err
	})
	return out, err
}

func GetOwner(q db.Q, channel string) (db.Identity, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.IdentityCols+" FROM identities WHERE channel = ?", channel), db.ScanIdentity)
}

type Sender struct {
	Channel    string
	ExternalID string
	AltID      string
}

// IsOwner reports whether the sender is the owner. For WhatsApp a sender may
// show up as the phone JID or the LID; when the alt id matches the stored
// number, the LID is learned so later LID-only messages still match.
func IsOwner(q db.Q, in Sender) (bool, error) {
	if in.Channel == db.ChannelWeb {
		return true, nil // the dashboard is behind passkey auth
	}
	var ids []string
	for _, id := range []string{in.ExternalID, in.AltID} {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return false, nil
	}
	var conds []string
	args := []any{in.Channel}
	for _, id := range ids {
		conds = append(conds, "external_id = ?", "alt_id = ?")
		args = append(args, id, id)
	}
	owner, ok, err := db.One(q.QueryRow("SELECT "+db.IdentityCols+" FROM identities WHERE channel = ? AND ("+strings.Join(conds, " OR ")+")", args...), db.ScanIdentity)
	if err != nil || !ok {
		return false, err
	}
	if in.Channel == db.ChannelWhatsApp && owner.AltID == nil {
		for _, id := range ids {
			if strings.HasSuffix(id, "@lid") {
				if _, err := q.Exec("UPDATE identities SET alt_id = ? WHERE id = ?", id, owner.ID); err != nil {
					return true, err
				}
				break
			}
		}
	}
	return true, nil
}
