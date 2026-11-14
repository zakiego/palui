// Package auth replaces Better Auth (apps/app/src/server/better-auth.ts,
// session.ts, owner-invite.ts, stream-guard.ts) on the same tables: `user`,
// `session`, `passkey`, `verification` and `owner_invites`. One owner, created
// by `palui owner invite`; passkeys are the only way in from the browser.
//
// Sessions keep Better Auth's rules: an opaque random token in an httpOnly,
// SameSite=Lax cookie (Secure when APP_URL is https), 30 day expiry, refreshed
// at most once a day. Unlike Better Auth, the `session.token` column holds the
// token's SHA-256 (base64url), not the token: a database read never yields a
// working cookie. Rows Better Auth wrote hold raw tokens, which can't be the
// hash of anything a client sends, so old sessions simply stop working and the
// owner signs in again.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

const (
	// SessionExpiresInMs is Better Auth's session.expiresIn (30 days).
	SessionExpiresInMs int64 = 30 * 24 * 60 * 60 * 1000
	// SessionUpdateAgeMs: a session is refreshed at most once a day.
	SessionUpdateAgeMs int64 = 24 * 60 * 60 * 1000
	// SessionFreshAgeMs: adding a passkey needs a session younger than this
	// (Better Auth's freshSessionMiddleware default).
	SessionFreshAgeMs int64 = 24 * 60 * 60 * 1000
	// CookieName is the session cookie. New with the Go build (Better Auth's
	// was palui.session_token), so old cookies are ignored.
	CookieName = "palui_session"
	// ClientIPHeader carries the real client IP, set by the server from the
	// proxy's headers and never trusted from the client.
	ClientIPHeader = "x-palui-client-ip"
)

// User is a `user` row.
type User struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	EmailVerified   bool    `json:"emailVerified"`
	Image           *string `json:"image"`
	Username        *string `json:"username"`
	DisplayUsername *string `json:"displayUsername"`
	CreatedAt       int64   `json:"createdAt"`
	UpdatedAt       int64   `json:"updatedAt"`
}

const userCols = "id, name, email, email_verified, image, username, display_username, created_at, updated_at"

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.EmailVerified, &u.Image, &u.Username, &u.DisplayUsername, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// Session is a `session` row without the token hash.
type Session struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	ExpiresAt int64   `json:"expiresAt"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	IPAddress *string `json:"ipAddress"`
	UserAgent *string `json:"userAgent"`
}

const sessionCols = "id, user_id, expires_at, created_at, updated_at, ip_address, user_agent"

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt, &s.IPAddress, &s.UserAgent)
	return s, err
}

// Viewer is who is calling (session.ts Viewer).
type Viewer struct {
	SessionID string     `json:"sessionId"`
	User      ViewerUser `json:"user"`
	// When the session was created (the fresh-session check).
	SessionCreatedAt int64 `json:"-"`
}

type ViewerUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

func viewerOf(s Session, u User) *Viewer {
	username := u.Name
	if u.Username != nil {
		username = *u.Username
	}
	return &Viewer{SessionID: s.ID, User: ViewerUser{ID: u.ID, Username: username, Name: u.Name}, SessionCreatedAt: s.CreatedAt}
}

// HashToken is what the `session.token` column stores for a token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString is Better Auth's generateRandomString(n) over the given
// alphabet (default a-z, A-Z, 0-9), without modulo bias.
func RandomString(n int, alphabet string) string {
	if alphabet == "" {
		alphabet = alnum
	}
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	limit := 256 - 256%len(alphabet)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, b := range buf {
			if int(b) < limit {
				out = append(out, alphabet[int(b)%len(alphabet)])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CreateSession starts a session for userID and returns the cookie token.
func CreateSession(q db.Q, userID, ip, userAgent string, now int64) (string, Session, error) {
	token := RandomString(32, "")
	s, err := scanSession(q.WQueryRow("INSERT INTO session (id, expires_at, token, created_at, updated_at, ip_address, user_agent, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING "+sessionCols,
		secret.NewID("ses"), now+SessionExpiresInMs, HashToken(token), now, now, nullable(ip), nullable(userAgent), userID))
	return token, s, err
}

// Found is a live session and its user.
type Found struct {
	Session Session
	User    User
	// Refreshed: the expiry moved forward, so the cookie should be set again.
	Refreshed bool
}

// Viewer is the Viewer for this session.
func (f *Found) Viewer() *Viewer { return viewerOf(f.Session, f.User) }

// GetSession is Better Auth's getSession: the session for a cookie token, or
// nil when there is none or it expired (an expired row is deleted). With
// refresh, a session whose expiry was last moved more than a day ago gets a
// new 30 day expiry.
func GetSession(q db.Q, token string, refresh bool, now int64) (*Found, error) {
	if token == "" {
		return nil, nil
	}
	hash := HashToken(token)
	s, err := scanSession(q.QueryRow("SELECT "+sessionCols+" FROM session WHERE token = ?", hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.ExpiresAt < now {
		_, err := q.Exec("DELETE FROM session WHERE id = ?", s.ID)
		return nil, err
	}
	u, err := scanUser(q.QueryRow("SELECT "+userCols+" FROM user WHERE id = ?", s.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := &Found{Session: s, User: u}
	if refresh && s.ExpiresAt-SessionExpiresInMs+SessionUpdateAgeMs <= now {
		updated, err := scanSession(q.WQueryRow("UPDATE session SET expires_at = ?, updated_at = ? WHERE id = ? RETURNING "+sessionCols, now+SessionExpiresInMs, now, s.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		f.Session, f.Refreshed = updated, true
	}
	return f, nil
}

// DeleteSession signs a token out. Missing is fine.
func DeleteSession(q db.Q, token string) error {
	if token == "" {
		return nil
	}
	_, err := q.Exec("DELETE FROM session WHERE token = ?", HashToken(token))
	return err
}

// SessionAlive is stream-guard.ts sessionAlive: the row exists and hasn't
// expired.
func SessionAlive(q db.Q, sessionID string, now int64) bool {
	var id string
	err := q.QueryRow("SELECT id FROM session WHERE id = ? AND expires_at > ?", sessionID, now).Scan(&id)
	return err == nil
}

// --- cookies --------------------------------------------------------------------------

// Secure is useSecureCookies: APP_URL is https.
func Secure(appURL string) bool {
	u, err := url.Parse(appURL)
	return err == nil && u.Scheme == "https"
}

// SessionCookie is the Set-Cookie for a session token (maxAge 30 days).
func SessionCookie(appURL, token string) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: token, Path: "/", MaxAge: int(SessionExpiresInMs / 1000), HttpOnly: true, Secure: Secure(appURL), SameSite: http.SameSiteLaxMode}
}

// ClearCookie expires a cookie.
func ClearCookie(appURL, name string) *http.Cookie {
	return &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: Secure(appURL), SameSite: http.SameSiteLaxMode}
}

// TokenFrom reads the session token cookie.
func TokenFrom(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ClientIP is withClientIp: cf-connecting-ip, the first x-forwarded-for hop,
// x-real-ip, else "local".
func ClientIP(h http.Header) string {
	if v := h.Get("cf-connecting-ip"); v != "" {
		return v
	}
	if v := h.Get("x-forwarded-for"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	if v := h.Get("x-real-ip"); v != "" {
		return v
	}
	return "local"
}
