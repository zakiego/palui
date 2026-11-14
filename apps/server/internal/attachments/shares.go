package attachments

import (
	"regexp"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// Temporary links to a saved attachment. A link is `${APP_URL}/api/share/<token>`:
// "public" opens for anyone with it, "private" also needs a dashboard sign-in.
// Links always expire (7 days at most) and can be revoked. Only the token's
// sha256 is stored.

// ShareDurations in ms, keyed "1h", "24h", "7d".
var ShareDurations = map[string]int64{"1h": 60 * 60 * 1000, "24h": 24 * 60 * 60 * 1000, "7d": 7 * 24 * 60 * 60 * 1000}

// ShareDurationKeys in the TS order.
var ShareDurationKeys = []string{"1h", "24h", "7d"}

// ShareView is a share without its token hash.
type ShareView struct {
	ID           string `json:"id"`
	AttachmentID string `json:"attachmentId"`
	Visibility   string `json:"visibility"`
	CreatedBy    string `json:"createdBy"`
	ExpiresAt    int64  `json:"expiresAt"`
	RevokedAt    *int64 `json:"revokedAt"`
	OpenCount    int64  `json:"openCount"`
	LastOpenedAt *int64 `json:"lastOpenedAt"`
	CreatedAt    int64  `json:"createdAt"`
}

func toView(r db.Share) ShareView {
	return ShareView{ID: r.ID, AttachmentID: r.AttachmentID, Visibility: r.Visibility, CreatedBy: r.CreatedBy, ExpiresAt: r.ExpiresAt, RevokedAt: r.RevokedAt, OpenCount: r.OpenCount, LastOpenedAt: r.LastOpenedAt, CreatedAt: r.CreatedAt}
}

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}$`)

func ShareURL(appURL, token string) string { return appURL + "/api/share/" + token }

type ShareInput struct {
	AttachmentID string
	// "public" or "private".
	Visibility string
	// A ShareDurations key.
	Duration  string
	CreatedBy string
}

// CreatedShare is a new link and its token, shown once.
type CreatedShare struct {
	Share ShareView `json:"share"`
	Token string    `json:"token"`
}

// CreateShare returns ok false for a missing attachment.
func CreateShare(q db.Q, in ShareInput, now int64) (CreatedShare, bool, error) {
	if _, ok, err := Get(q, in.AttachmentID); err != nil || !ok {
		return CreatedShare{}, false, err
	}
	token := secret.RandomToken(24)
	row := db.Share{
		ID: secret.NewID("shr"), AttachmentID: in.AttachmentID, TokenHash: secret.Sha256(token), Visibility: in.Visibility,
		CreatedBy: in.CreatedBy, ExpiresAt: now + ShareDurations[in.Duration], CreatedAt: now,
	}
	_, err := q.Exec("INSERT INTO attachment_shares ("+db.ShareCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		row.ID, row.AttachmentID, row.TokenHash, row.Visibility, row.CreatedBy, row.ExpiresAt, row.RevokedAt, row.OpenCount, row.LastOpenedAt, row.CreatedAt)
	if err != nil {
		return CreatedShare{}, false, err
	}
	return CreatedShare{Share: toView(row), Token: token}, true, nil
}

// Resolved is a token lookup: OK with the share and file, or a Reason
// ("not_found", "expired", "revoked").
type Resolved struct {
	OK         bool
	Reason     string
	Share      ShareView
	Attachment db.AttachmentMeta
}

// ResolveShare looks a token up. It doesn't count the open; the route calls
// RecordOpen once it serves the file.
func ResolveShare(q db.Q, token string, now int64) (Resolved, error) {
	if !tokenRe.MatchString(token) {
		return Resolved{Reason: "not_found"}, nil
	}
	row, ok, err := db.One(q.QueryRow("SELECT "+db.ShareCols+" FROM attachment_shares WHERE token_hash = ?", secret.Sha256(token)), db.ScanShare)
	if err != nil {
		return Resolved{}, err
	}
	if !ok {
		return Resolved{Reason: "not_found"}, nil
	}
	if row.RevokedAt != nil {
		return Resolved{Reason: "revoked"}, nil
	}
	if row.ExpiresAt <= now {
		return Resolved{Reason: "expired"}, nil
	}
	a, ok, err := Get(q, row.AttachmentID)
	if err != nil {
		return Resolved{}, err
	}
	if !ok {
		return Resolved{Reason: "not_found"}, nil
	}
	return Resolved{OK: true, Share: toView(row), Attachment: a}, nil
}

func RecordOpen(q db.Q, shareID string, now int64) error {
	_, err := q.Exec("UPDATE attachment_shares SET open_count = open_count + 1, last_opened_at = ? WHERE id = ?", now, shareID)
	return err
}

const live = "revoked_at IS NULL AND expires_at > ?"

// ListActiveShares are the links that still open, newest first.
func ListActiveShares(q db.Q, attachmentID string, now int64) ([]ShareView, error) {
	rows, err := q.Query("SELECT "+db.ShareCols+" FROM attachment_shares WHERE attachment_id = ? AND "+live+" ORDER BY created_at DESC, id DESC", attachmentID, now)
	all, err := db.All(rows, err, db.ScanShare)
	if err != nil {
		return nil, err
	}
	out := make([]ShareView, len(all))
	for i, r := range all {
		out[i] = toView(r)
	}
	return out, nil
}

// ActiveShareCounts are live links per attachment, for the Attachments table.
func ActiveShareCounts(q db.Q, now int64) (map[string]int64, error) {
	rows, err := q.Query("SELECT s.attachment_id, count(*) FROM attachment_shares s INNER JOIN saved_attachments a ON a.id = s.attachment_id WHERE s.revoked_at IS NULL AND s.expires_at > ? GROUP BY s.attachment_id", now)
	type pair struct {
		id string
		n  int64
	}
	all, err := db.All(rows, err, func(s db.Scanner) (pair, error) {
		var p pair
		return p, s.Scan(&p.id, &p.n)
	})
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, p := range all {
		out[p.id] = p.n
	}
	return out, nil
}

func RevokeShare(q db.Q, shareID string, now int64) (bool, error) {
	n, err := db.Changes(q.Exec("UPDATE attachment_shares SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", now, shareID))
	return n > 0, err
}

// RevokeShares revokes every live link to one attachment and returns how many.
func RevokeShares(q db.Q, attachmentID string, now int64) (int64, error) {
	return db.Changes(q.Exec("UPDATE attachment_shares SET revoked_at = ? WHERE attachment_id = ? AND "+live, now, attachmentID, now))
}
