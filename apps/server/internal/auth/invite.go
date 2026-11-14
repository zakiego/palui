package auth

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// Owner invite links (owner-invite.ts). `palui owner invite` makes a
// single-use token, valid for 15 minutes; only its sha256 is stored. Accepting
// it creates the one owner user on first use; on a later link (recovery) it
// shuts out every old session and passkey. The Go build signs the owner in
// directly (no internal password); the invite page then asks for a passkey.

const (
	InviteTTLMs   int64 = 15 * 60_000
	OwnerUsername       = "owner"
	ownerEmail          = "owner@palui.invalid"
)

// InviteError is a refused invite; its text is shown as is.
type InviteError struct{ Msg string }

func (e *InviteError) Error() string { return e.Msg }

// InviteURL is inviteUrl.
func InviteURL(appURL, token string) string {
	return strings.TrimRight(appURL, "/") + "/app/invite/" + token
}

// Invite is a fresh invite.
type Invite struct {
	Token     string `json:"token"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expiresAt"`
}

// CreateOwnerInvite is createOwnerInvite.
func CreateOwnerInvite(q db.Q, appURL string, now int64) (Invite, error) {
	token := secret.RandomToken(24)
	expiresAt := now + InviteTTLMs
	if _, err := q.Exec("INSERT INTO owner_invites (id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)", secret.NewID("inv"), secret.Sha256(token), expiresAt, now); err != nil {
		return Invite{}, err
	}
	return Invite{Token: token, URL: InviteURL(appURL, token), ExpiresAt: expiresAt}, nil
}

// InviteState is InviteState: {ok: true, expiresAt, recovery} or {ok: false, problem}.
type InviteState struct {
	OK        bool
	ExpiresAt int64
	Recovery  bool
	Problem   string
}

func (s InviteState) MarshalJSON() ([]byte, error) {
	if s.OK {
		return []byte(`{"ok":true,"expiresAt":` + itoa(s.ExpiresAt) + `,"recovery":` + boolText(s.Recovery) + `}`), nil
	}
	return []byte(`{"ok":false,"problem":` + quote(s.Problem) + `}`), nil
}

// InspectInvite is inspectInvite.
func InspectInvite(q db.Q, token string, now int64) (InviteState, error) {
	var expiresAt int64
	var usedAt sql.NullInt64
	err := q.QueryRow("SELECT expires_at, used_at FROM owner_invites WHERE token_hash = ?", secret.Sha256(token)).Scan(&expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return InviteState{Problem: "This link isn't valid."}, nil
	}
	if err != nil {
		return InviteState{}, err
	}
	if usedAt.Valid && usedAt.Int64 != 0 {
		return InviteState{Problem: "This link was already used."}, nil
	}
	if expiresAt <= now {
		return InviteState{Problem: "This link expired."}, nil
	}
	exists, err := OwnerExists(q)
	if err != nil {
		return InviteState{}, err
	}
	return InviteState{OK: true, ExpiresAt: expiresAt, Recovery: exists}, nil
}

// FindOwner is findOwner: the oldest user.
func FindOwner(q db.Q) (User, bool, error) {
	u, err := scanUser(q.QueryRow("SELECT " + userCols + " FROM user ORDER BY created_at ASC LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	return u, err == nil, err
}

// OwnerExists is ownerExists.
func OwnerExists(q db.Q) (bool, error) {
	_, ok, err := FindOwner(q)
	return ok, err
}

// Accepted is what accepting an invite did.
type Accepted struct {
	UserID   string
	Username string
	Created  bool
}

// AcceptOwnerInvite marks the invite used and creates or recovers the owner,
// in one transaction. The single UPDATE is the claim: two tabs racing on one
// link can't both win.
func AcceptOwnerInvite(q db.Q, token string, now int64) (Accepted, error) {
	var out Accepted
	err := q.Tx(func(tx db.Q) error {
		res, err := tx.Exec("UPDATE owner_invites SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?", now, secret.Sha256(token), now)
		n, err := db.Changes(res, err)
		if err != nil {
			return err
		}
		if n == 0 {
			return &InviteError{Msg: "This link is no longer valid. Run `palui owner invite` for a new one."}
		}
		existing, found, err := FindOwner(tx)
		if err != nil {
			return err
		}
		if !found {
			id := secret.NewID("usr")
			if _, err := tx.Exec("INSERT INTO user (id, name, email, email_verified, username, display_username, created_at, updated_at) VALUES (?, 'Owner', ?, 0, ?, ?, ?, ?)", id, ownerEmail, OwnerUsername, OwnerUsername, now, now); err != nil {
				return err
			}
			out = Accepted{UserID: id, Username: OwnerUsername, Created: true}
			return nil
		}
		// Recovery: whoever holds an old session or passkey (a lost phone) is
		// shut out. The invite page asks for a new passkey right after.
		if _, err := tx.Exec("DELETE FROM session WHERE user_id = ?", existing.ID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM passkey WHERE user_id = ?", existing.ID); err != nil {
			return err
		}
		username := OwnerUsername
		if existing.Username != nil {
			username = *existing.Username
		}
		out = Accepted{UserID: existing.ID, Username: username, Created: false}
		return nil
	})
	return out, err
}
