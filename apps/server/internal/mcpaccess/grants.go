// Package mcpaccess is who may call Palui's own MCP server (/mcp): personal
// tokens the owner creates for CLI agents (mcporter, Claude Code), and OAuth
// 2.1 connections for clients that can't send a header (claude.ai custom
// connectors). Both are rows in mcp_grants. Tokens are random, shown once, and
// stored only as hashes.
//
// The OAuth side (oauth.go) is a small authorization server for /mcp alone:
// dynamic client registration (RFC 7591), the authorization code grant with
// PKCE S256, refresh token rotation, and revocation (RFC 7009). Consent happens
// in the dashboard, so a connection needs the owner's passkey session.
package mcpaccess

import (
	"errors"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	KindToken = "token"
	KindOAuth = "oauth"

	// Token prefixes, so a leaked token says where it came from.
	TokenPrefix   = "plt_"
	AccessPrefix  = "pla_"
	RefreshPrefix = "plr_"

	// MaxNameLen caps a personal token's name (UTF-16 units).
	MaxNameLen = 60
	// MaxTokens caps personal tokens per user.
	MaxTokens = 50
	// touchEvery throttles the last_used_at write.
	touchEvery = 60_000
)

// ErrNotFound is a grant that doesn't exist (or isn't the caller's).
var ErrNotFound = errors.New("not found")

// Error is a refusal the owner reads (a bad name, too many tokens).
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func newToken(prefix string) string { return prefix + secret.RandomToken(32) }

func hint(token string) string { return token[len(token)-4:] }

// HashToken is what the hash columns store: SHA-256, base64url, no padding.
func HashToken(token string) string { return auth.HashToken(token) }

// CreateToken makes a personal token for userID. The token is returned once;
// only its hash and last four characters are kept.
func CreateToken(q db.Q, userID, name string, now int64) (string, db.MCPGrant, error) {
	name = strx.Trim(name)
	if name == "" {
		return "", db.MCPGrant{}, &Error{"Give the token a name, like the machine or agent that uses it."}
	}
	if strx.Len16(name) > MaxNameLen {
		return "", db.MCPGrant{}, &Error{"Keep the name under 60 characters."}
	}
	var count int
	if err := q.QueryRow("SELECT count(*) FROM mcp_grants WHERE user_id = ? AND kind = ?", userID, KindToken).Scan(&count); err != nil {
		return "", db.MCPGrant{}, err
	}
	if count >= MaxTokens {
		return "", db.MCPGrant{}, &Error{"You have 50 tokens already. Revoke one you don't use first."}
	}
	token := newToken(TokenPrefix)
	h := hint(token)
	g, _, err := db.One(q.WQueryRow(
		"INSERT INTO mcp_grants (id, kind, name, user_id, token_hint, access_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING "+db.MCPGrantCols,
		secret.NewID("mcpg"), KindToken, name, userID, h, HashToken(token), now,
	), db.ScanMCPGrant)
	if err != nil {
		return "", db.MCPGrant{}, err
	}
	return token, g, nil
}

// GrantView is a grant as the dashboard and CLI list it.
type GrantView struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	TokenHint  *string `json:"tokenHint"`
	ClientURI  *string `json:"clientUri"`
	CreatedAt  int64   `json:"createdAt"`
	LastUsedAt *int64  `json:"lastUsedAt"`
	// When an OAuth connection lapses unless the client refreshes it; null for
	// personal tokens, which last until revoked.
	ExpiresAt *int64 `json:"expiresAt"`
}

// List is userID's grants that still work, newest first.
func List(q db.Q, userID string, now int64) ([]GrantView, error) {
	rows, err := q.Query(`SELECT g.id, g.kind, g.name, g.token_hint, c.client_uri, g.created_at, g.last_used_at, g.refresh_expires_at
		FROM mcp_grants g LEFT JOIN oauth_clients c ON c.id = g.client_id
		WHERE g.user_id = ? AND (g.kind = ? OR g.refresh_expires_at > ?)
		ORDER BY g.created_at DESC, g.id DESC`, userID, KindToken, now)
	return db.All(rows, err, func(s db.Scanner) (GrantView, error) {
		var v GrantView
		err := s.Scan(&v.ID, &v.Kind, &v.Name, &v.TokenHint, &v.ClientURI, &v.CreatedAt, &v.LastUsedAt, &v.ExpiresAt)
		return v, err
	})
}

// Revoke deletes one of userID's grants. Its tokens stop working at once.
func Revoke(q db.Q, userID, id string) error {
	res, err := q.Exec("DELETE FROM mcp_grants WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Caller is who a bearer token belongs to.
type Caller struct {
	Grant db.MCPGrant
	User  auth.ViewerUser
}

// Viewer is the caller as the dashboard's server functions see a session.
func (c *Caller) Viewer() *auth.Viewer {
	return &auth.Viewer{SessionID: "mcp:" + c.Grant.ID, User: c.User, SessionCreatedAt: c.Grant.CreatedAt}
}

// Verify resolves a bearer token to its grant and user, or nil when the
// token is unknown, expired, revoked, or its user is gone. A personal token
// never expires; an OAuth access token lasts AccessTTL.
func Verify(q db.Q, token string, now int64) (*Caller, error) {
	if !strings.HasPrefix(token, TokenPrefix) && !strings.HasPrefix(token, AccessPrefix) {
		return nil, nil
	}
	g, ok, err := db.One(q.QueryRow("SELECT "+db.MCPGrantCols+" FROM mcp_grants WHERE access_hash = ?", HashToken(token)), db.ScanMCPGrant)
	if err != nil || !ok {
		return nil, err
	}
	if g.AccessExpiresAt != nil && *g.AccessExpiresAt <= now {
		return nil, nil
	}
	var u auth.ViewerUser
	var username *string
	err = q.QueryRow("SELECT id, name, username FROM user WHERE id = ?", g.UserID).Scan(&u.ID, &u.Name, &username)
	if err != nil {
		if errors.Is(err, errNoRows) {
			return nil, nil
		}
		return nil, err
	}
	u.Username = u.Name
	if username != nil {
		u.Username = *username
	}
	return &Caller{Grant: g, User: u}, nil
}

// Touch records that a grant was used, at most once a minute.
func Touch(q db.Q, g db.MCPGrant, now int64) error {
	if g.LastUsedAt != nil && now-*g.LastUsedAt < touchEvery {
		return nil
	}
	_, err := q.Exec("UPDATE mcp_grants SET last_used_at = ? WHERE id = ?", now, g.ID)
	return err
}
