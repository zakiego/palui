package mcpaccess

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var errNoRows = sql.ErrNoRows

const (
	// AccessTTL is an OAuth access token's life; RefreshTTL a refresh token's.
	AccessTTL  int64 = 60 * 60 * 1000
	RefreshTTL int64 = 30 * 24 * 60 * 60 * 1000
	// RefreshGrace keeps the refresh token a rotation replaced working a little
	// longer, so a client that lost the response to a refresh can retry.
	RefreshGrace int64 = 2 * 60 * 1000
	// RequestTTL is how long the consent page has; CodeTTL how long the client
	// has to exchange the code after the owner allows it.
	RequestTTL int64 = 10 * 60 * 1000
	CodeTTL    int64 = 60 * 1000
	// UnusedClientTTL: a registered client with no connection is pruned after a day.
	UnusedClientTTL int64 = 24 * 60 * 60 * 1000
	// MaxClients caps registered clients, since registration is open.
	MaxClients = 200

	maxRedirectURIs = 10
	maxURILen       = 2000
	maxClientName   = 100
	maxScope        = 300
	maxState        = 1000
)

// Paths, relative to APP_URL.
const (
	MCPPath           = "/mcp"
	AuthorizePath     = "/api/oauth/authorize"
	TokenPath         = "/api/oauth/token"
	RegisterPath      = "/api/oauth/register"
	RevokePath        = "/api/oauth/revoke"
	ConsentPath       = "/app/oauth/consent"
	ResourceMetaPath  = "/.well-known/oauth-protected-resource"
	AuthServerMetaPth = "/.well-known/oauth-authorization-server"
)

// Auth methods a client may register with.
var authMethods = []string{"none", "client_secret_post", "client_secret_basic"}

// Scopes don't gate anything: a connection can do what the owner can. The list
// is advertised because some clients send these and expect them known.
var Scopes = []string{"palui", "offline_access", "claudeai"}

// OAuthError is an OAuth error response: {error, error_description}.
type OAuthError struct {
	Status      int
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oerr(status int, code, desc string) *OAuthError {
	return &OAuthError{Status: status, Code: code, Description: desc}
}

// ResourceURL is the protected resource: <APP_URL>/mcp.
func ResourceURL(appURL string) string { return appURL + MCPPath }

// ResourceMetadata is RFC 9728 protected resource metadata for /mcp.
func ResourceMetadata(appURL string) *jsonx.Object {
	return obj(
		"resource", ResourceURL(appURL),
		"authorization_servers", []any{appURL},
		"scopes_supported", anyList(Scopes),
		"bearer_methods_supported", []any{"header"},
		"resource_name", "Palui Agent",
	)
}

// AuthServerMetadata is RFC 8414 authorization server metadata. The issuer is
// APP_URL itself (no path), so the document lives at the root well-known URL.
func AuthServerMetadata(appURL string) *jsonx.Object {
	return obj(
		"issuer", appURL,
		"authorization_endpoint", appURL+AuthorizePath,
		"token_endpoint", appURL+TokenPath,
		"registration_endpoint", appURL+RegisterPath,
		"revocation_endpoint", appURL+RevokePath,
		"scopes_supported", anyList(Scopes),
		"response_types_supported", []any{"code"},
		"response_modes_supported", []any{"query"},
		"grant_types_supported", []any{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported", anyList(authMethods),
		"revocation_endpoint_auth_methods_supported", anyList(authMethods),
		"code_challenge_methods_supported", []any{"S256"},
		"authorization_response_iss_parameter_supported", true,
	)
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func anyList(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// --- registration ---------------------------------------------------------------------

// RegisterInput is the RFC 7591 request body (the fields Palui reads).
type RegisterInput struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

// Register stores a new client and returns the RFC 7591 response. Unused
// clients older than a day are pruned first.
func Register(q db.Q, in RegisterInput, now int64) (*jsonx.Object, error) {
	if len(in.RedirectURIs) == 0 {
		return nil, oerr(400, "invalid_redirect_uri", "redirect_uris is required")
	}
	if len(in.RedirectURIs) > maxRedirectURIs {
		return nil, oerr(400, "invalid_redirect_uri", "at most 10 redirect_uris")
	}
	for _, u := range in.RedirectURIs {
		if err := checkRedirectURI(u); err != nil {
			return nil, oerr(400, "invalid_redirect_uri", err.Error())
		}
	}
	method := in.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	if !slices.Contains(authMethods, method) {
		return nil, oerr(400, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic")
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return nil, oerr(400, "invalid_client_metadata", "unsupported grant type "+g)
		}
	}
	for _, r := range in.ResponseTypes {
		if r != "code" {
			return nil, oerr(400, "invalid_client_metadata", "unsupported response type "+r)
		}
	}
	name := strx.Slice16(strx.Trim(in.ClientName), maxClientName)
	if name == "" {
		name = "MCP client"
	}
	var clientURI *string
	if u := strx.Trim(in.ClientURI); u != "" && len(u) <= maxURILen {
		if p, err := url.Parse(u); err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != "" {
			clientURI = &u
		}
	}

	if _, err := q.Exec(`DELETE FROM oauth_clients WHERE created_at < ?
		AND id NOT IN (SELECT client_id FROM mcp_grants WHERE client_id IS NOT NULL)`, now-UnusedClientTTL); err != nil {
		return nil, err
	}
	var count int
	if err := q.QueryRow("SELECT count(*) FROM oauth_clients").Scan(&count); err != nil {
		return nil, err
	}
	if count >= MaxClients {
		return nil, oerr(503, "temporarily_unavailable", "too many registered clients; try again tomorrow")
	}

	id := "plc_" + secret.RandomToken(16)
	var clientSecret string
	var secretHash *string
	if method != "none" {
		clientSecret = "pls_" + secret.RandomToken(32)
		h := HashToken(clientSecret)
		secretHash = &h
	}
	_, err := q.Exec("INSERT INTO oauth_clients ("+db.OAuthClientCols+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, name, clientURI, db.JSON[[]string]{V: in.RedirectURIs}, method, secretHash, now)
	if err != nil {
		return nil, err
	}
	grants := in.GrantTypes
	if len(grants) == 0 {
		grants = []string{"authorization_code", "refresh_token"}
	}
	out := obj(
		"client_id", id,
		"client_id_issued_at", now/1000,
		"client_name", name,
		"redirect_uris", anyList(in.RedirectURIs),
		"grant_types", anyList(grants),
		"response_types", []any{"code"},
		"token_endpoint_auth_method", method,
	)
	if clientURI != nil {
		out.Set("client_uri", *clientURI)
	}
	if s := strx.Trim(in.Scope); s != "" {
		out.Set("scope", strx.Slice16(s, maxScope))
	}
	if clientSecret != "" {
		out.Set("client_secret", clientSecret)
		out.Set("client_secret_expires_at", 0)
	}
	return out, nil
}

// checkRedirectURI accepts https anywhere, http only on a loopback host (a CLI
// listening for the callback), and a private-use scheme (a desktop app).
func checkRedirectURI(raw string) error {
	if len(raw) > maxURILen {
		return errors.New("redirect_uri is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return errors.New("redirect_uri must be an absolute URL")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("redirect_uri must not have a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return errors.New("redirect_uri needs a host")
		}
	case "http":
		if !isLoopback(u.Hostname()) {
			return errors.New("http redirect_uris must point at localhost")
		}
	case "javascript", "data", "file", "vbscript", "blob", "about":
		return errors.New("redirect_uri scheme isn't allowed")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redirectMatches: exact match, or for http loopback URIs any port (RFC 8252
// 7.3: a native app picks a free port when it starts).
func redirectMatches(registered []string, got string) bool {
	if slices.Contains(registered, got) {
		return true
	}
	g, err := url.Parse(got)
	if err != nil || g.Scheme != "http" || !isLoopback(g.Hostname()) {
		return false
	}
	for _, r := range registered {
		p, err := url.Parse(r)
		if err != nil || p.Scheme != "http" || !isLoopback(p.Hostname()) {
			continue
		}
		if p.Hostname() == g.Hostname() && p.Path == g.Path && p.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

// GetClient is one client, or ok false.
func GetClient(q db.Q, id string) (db.OAuthClient, bool, error) {
	return db.One(q.QueryRow("SELECT "+db.OAuthClientCols+" FROM oauth_clients WHERE id = ?", id), db.ScanOAuthClient)
}

// --- authorize ------------------------------------------------------------------------

// AuthorizeResult is where /api/oauth/authorize sends the browser: either the
// consent page (RequestID set) or back to the client with an error.
type AuthorizeResult struct {
	RequestID string
	// Redirect is the client's redirect_uri with error and state, when the
	// request is bad in a way the client should hear about.
	Redirect string
}

// PageError is a bad authorize request that can't be sent back to the client
// (unknown client, redirect_uri not registered): the browser shows it.
type PageError struct{ Message string }

func (e *PageError) Error() string { return e.Message }

// Authorize checks an authorization request and stores it for the consent page.
func Authorize(q db.Q, v url.Values, appURL string, now int64) (AuthorizeResult, error) {
	clientID := v.Get("client_id")
	if clientID == "" {
		return AuthorizeResult{}, &PageError{"The app didn't say who it is (client_id is missing)."}
	}
	c, ok, err := GetClient(q, clientID)
	if err != nil {
		return AuthorizeResult{}, err
	}
	if !ok {
		return AuthorizeResult{}, &PageError{"This app isn't registered with Palui anymore. Remove the connector in the app and add it again."}
	}
	redirect := v.Get("redirect_uri")
	if redirect == "" {
		if len(c.RedirectURIs.V) != 1 {
			return AuthorizeResult{}, &PageError{"The app didn't say where to return (redirect_uri is missing)."}
		}
		redirect = c.RedirectURIs.V[0]
	} else if !redirectMatches(c.RedirectURIs.V, redirect) {
		return AuthorizeResult{}, &PageError{"The app asked to return somewhere it didn't register. Nothing was shared."}
	}
	state := v.Get("state")
	if len(state) > maxState {
		return AuthorizeResult{}, &PageError{"The request is malformed (state is too long)."}
	}
	back := func(code, desc string) (AuthorizeResult, error) {
		return AuthorizeResult{Redirect: withParams(redirect, "error", code, "error_description", desc, "state", state, "iss", appURL)}, nil
	}
	if v.Get("response_type") != "code" {
		return back("unsupported_response_type", "only response_type=code is supported")
	}
	challenge := v.Get("code_challenge")
	if challenge == "" {
		return back("invalid_request", "code_challenge is required (PKCE)")
	}
	if v.Get("code_challenge_method") != "S256" {
		return back("invalid_request", "code_challenge_method must be S256")
	}
	if len(challenge) < 43 || len(challenge) > 128 {
		return back("invalid_request", "code_challenge has the wrong length")
	}
	if res := v["resource"]; len(res) > 0 {
		for _, r := range res {
			if !resourceOK(appURL, r) {
				return back("invalid_target", "this server only grants access to "+ResourceURL(appURL))
			}
		}
	}
	scope := strx.Slice16(strx.Trim(v.Get("scope")), maxScope)

	if _, err := q.Exec("DELETE FROM oauth_requests WHERE expires_at < ?", now); err != nil {
		return AuthorizeResult{}, err
	}
	id := "plq_" + secret.RandomToken(16)
	_, err = q.Exec("INSERT INTO oauth_requests ("+db.OAuthRequestCols+") VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?)",
		id, c.ID, redirect, nullStr(state), nullStr(scope), challenge, now, now+RequestTTL)
	if err != nil {
		return AuthorizeResult{}, err
	}
	return AuthorizeResult{RequestID: id}, nil
}

// resourceOK accepts the /mcp URL (with or without a trailing slash) or the
// origin itself, which some clients send.
func resourceOK(appURL, r string) bool {
	r = strings.TrimSuffix(r, "/")
	return r == ResourceURL(appURL) || r == appURL
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func withParams(base string, kv ...string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// --- consent --------------------------------------------------------------------------

// ErrRequestGone is a consent request that expired, was answered already, or
// never existed.
var ErrRequestGone = errors.New("This sign-in request expired or was already answered. Start again from the app.")

// RequestView is what the consent page shows.
type RequestView struct {
	ID           string  `json:"id"`
	ClientName   string  `json:"clientName"`
	ClientURI    *string `json:"clientUri"`
	RedirectHost string  `json:"redirectHost"`
	Scope        *string `json:"scope"`
	ExpiresAt    int64   `json:"expiresAt"`
}

func pendingRequest(q db.Q, id string, now int64) (db.OAuthRequest, db.OAuthClient, error) {
	r, ok, err := db.One(q.QueryRow("SELECT "+db.OAuthRequestCols+" FROM oauth_requests WHERE id = ?", id), db.ScanOAuthRequest)
	if err != nil {
		return r, db.OAuthClient{}, err
	}
	if !ok || r.UserID != nil || r.ExpiresAt <= now {
		return r, db.OAuthClient{}, ErrRequestGone
	}
	c, ok, err := GetClient(q, r.ClientID)
	if err != nil {
		return r, c, err
	}
	if !ok {
		return r, c, ErrRequestGone
	}
	return r, c, nil
}

// GetRequest is a pending request for the consent page.
func GetRequest(q db.Q, id string, now int64) (RequestView, error) {
	r, c, err := pendingRequest(q, id, now)
	if err != nil {
		return RequestView{}, err
	}
	return RequestView{ID: r.ID, ClientName: c.Name, ClientURI: c.ClientURI, RedirectHost: redirectHost(r.RedirectURI), Scope: r.Scope, ExpiresAt: r.ExpiresAt}, nil
}

func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Host != "" {
		return u.Host
	}
	return u.Scheme + ":"
}

// Decide answers a pending request. Allow issues a one-time code bound to
// userID; deny drops the request. Either way the result is the URL the browser
// goes to next (the client's redirect_uri).
func Decide(q db.Q, id, userID string, allow bool, appURL string, now int64) (string, error) {
	var out string
	err := q.Tx(func(tx db.Q) error {
		r, _, err := pendingRequest(tx, id, now)
		if err != nil {
			return err
		}
		state := ""
		if r.State != nil {
			state = *r.State
		}
		if !allow {
			if _, err := tx.Exec("DELETE FROM oauth_requests WHERE id = ?", id); err != nil {
				return err
			}
			out = withParams(r.RedirectURI, "error", "access_denied", "error_description", "the owner said no", "state", state, "iss", appURL)
			return nil
		}
		code := "plo_" + secret.RandomToken(32)
		if _, err := tx.Exec("UPDATE oauth_requests SET user_id = ?, code_hash = ?, expires_at = ? WHERE id = ?",
			userID, HashToken(code), now+CodeTTL, id); err != nil {
			return err
		}
		out = withParams(r.RedirectURI, "code", code, "state", state, "iss", appURL)
		return nil
	})
	return out, err
}

// --- token ----------------------------------------------------------------------------

// TokenResponse is a successful token endpoint answer.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope,omitempty"`
}

// ClientAuth is how the client authenticated at the token endpoint.
type ClientAuth struct {
	ID     string
	Secret string
	// Basic is true when the credentials came in an Authorization header.
	Basic bool
}

func authenticateClient(q db.Q, a ClientAuth) (db.OAuthClient, error) {
	if a.ID == "" {
		return db.OAuthClient{}, oerr(401, "invalid_client", "client_id is required")
	}
	c, ok, err := GetClient(q, a.ID)
	if err != nil {
		return c, err
	}
	if !ok {
		return c, oerr(401, "invalid_client", "unknown client")
	}
	if c.AuthMethod == "none" {
		return c, nil
	}
	if a.Secret == "" || c.SecretHash == nil || !secret.SafeEqual(HashToken(a.Secret), *c.SecretHash) {
		return c, oerr(401, "invalid_client", "client authentication failed")
	}
	return c, nil
}

// Exchange runs the token endpoint for the authorization_code and
// refresh_token grants.
func Exchange(q db.Q, form url.Values, a ClientAuth, now int64) (TokenResponse, error) {
	var out TokenResponse
	// An OAuth refusal still commits: a used code stays used, and a replayed
	// refresh token's connection stays cut. Only a database error rolls back.
	var refusal *OAuthError
	err := q.Tx(func(tx db.Q) error {
		c, err := authenticateClient(tx, a)
		if err == nil {
			switch form.Get("grant_type") {
			case "authorization_code":
				out, err = exchangeCode(tx, c, form, now)
			case "refresh_token":
				out, err = refresh(tx, c, form, now)
			case "":
				err = oerr(400, "invalid_request", "grant_type is required")
			default:
				err = oerr(400, "unsupported_grant_type", "use authorization_code or refresh_token")
			}
		}
		if errors.As(err, &refusal) {
			return nil
		}
		return err
	})
	if err != nil {
		return TokenResponse{}, err
	}
	if refusal != nil {
		return TokenResponse{}, refusal
	}
	return out, nil
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return secret.SafeEqual(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

func exchangeCode(tx db.Q, c db.OAuthClient, form url.Values, now int64) (TokenResponse, error) {
	code := form.Get("code")
	if code == "" {
		return TokenResponse{}, oerr(400, "invalid_request", "code is required")
	}
	r, ok, err := db.One(tx.QueryRow("SELECT "+db.OAuthRequestCols+" FROM oauth_requests WHERE code_hash = ?", HashToken(code)), db.ScanOAuthRequest)
	if err != nil {
		return TokenResponse{}, err
	}
	if !ok || r.UserID == nil {
		return TokenResponse{}, oerr(400, "invalid_grant", "the code is unknown or was used already")
	}
	// One use, whatever happens next.
	if _, err := tx.Exec("DELETE FROM oauth_requests WHERE id = ?", r.ID); err != nil {
		return TokenResponse{}, err
	}
	if r.ClientID != c.ID {
		return TokenResponse{}, oerr(400, "invalid_grant", "the code was issued to another client")
	}
	if r.ExpiresAt <= now {
		return TokenResponse{}, oerr(400, "invalid_grant", "the code expired")
	}
	if ru := form.Get("redirect_uri"); ru != "" && ru != r.RedirectURI {
		return TokenResponse{}, oerr(400, "invalid_grant", "redirect_uri doesn't match the authorization request")
	}
	if !pkceOK(form.Get("code_verifier"), r.CodeChallenge) {
		return TokenResponse{}, oerr(400, "invalid_grant", "code_verifier doesn't match the code_challenge")
	}
	if _, err := tx.Exec("DELETE FROM mcp_grants WHERE kind = ? AND refresh_expires_at <= ?", KindOAuth, now); err != nil {
		return TokenResponse{}, err
	}
	access, refreshTok := newToken(AccessPrefix), newToken(RefreshPrefix)
	scope := ""
	if r.Scope != nil {
		scope = *r.Scope
	}
	_, err = tx.Exec(`INSERT INTO mcp_grants (id, kind, name, user_id, client_id, scope, token_hint, access_hash, access_expires_at, refresh_hash, refresh_expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		secret.NewID("mcpg"), KindOAuth, c.Name, *r.UserID, c.ID, nullStr(scope),
		HashToken(access), now+AccessTTL, HashToken(refreshTok), now+RefreshTTL, now)
	if err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: AccessTTL / 1000, RefreshToken: refreshTok, Scope: scope}, nil
}

func refresh(tx db.Q, c db.OAuthClient, form url.Values, now int64) (TokenResponse, error) {
	tok := form.Get("refresh_token")
	if tok == "" {
		return TokenResponse{}, oerr(400, "invalid_request", "refresh_token is required")
	}
	h := HashToken(tok)
	g, ok, err := db.One(tx.QueryRow("SELECT "+db.MCPGrantCols+" FROM mcp_grants WHERE kind = ? AND (refresh_hash = ? OR prev_refresh_hash = ?)", KindOAuth, h, h), db.ScanMCPGrant)
	if err != nil {
		return TokenResponse{}, err
	}
	if !ok {
		return TokenResponse{}, oerr(400, "invalid_grant", "the refresh token is unknown or was revoked")
	}
	if g.ClientID == nil || *g.ClientID != c.ID {
		return TokenResponse{}, oerr(400, "invalid_grant", "the refresh token was issued to another client")
	}
	if g.RefreshExpiresAt == nil || *g.RefreshExpiresAt <= now {
		return TokenResponse{}, oerr(400, "invalid_grant", "the refresh token expired")
	}
	viaPrev := g.RefreshHash == nil || *g.RefreshHash != h
	if viaPrev {
		// An old refresh token: fine right after a rotation (a retry), a sign
		// of theft after that. Past the grace window the connection is cut.
		if g.RotatedAt == nil || now-*g.RotatedAt > RefreshGrace {
			if _, err := tx.Exec("DELETE FROM mcp_grants WHERE id = ?", g.ID); err != nil {
				return TokenResponse{}, err
			}
			return TokenResponse{}, oerr(400, "invalid_grant", "the refresh token was already used")
		}
	}
	access, refreshTok := newToken(AccessPrefix), newToken(RefreshPrefix)
	prev := g.RefreshHash
	if viaPrev {
		prev = g.PrevRefreshHash
	}
	_, err = tx.Exec(`UPDATE mcp_grants SET access_hash = ?, access_expires_at = ?, refresh_hash = ?, refresh_expires_at = ?, prev_refresh_hash = ?, rotated_at = ?
		WHERE id = ?`, HashToken(access), now+AccessTTL, HashToken(refreshTok), now+RefreshTTL, prev, now, g.ID)
	if err != nil {
		return TokenResponse{}, err
	}
	scope := ""
	if g.Scope != nil {
		scope = *g.Scope
	}
	return TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: AccessTTL / 1000, RefreshToken: refreshTok, Scope: scope}, nil
}

// RevokeToken is RFC 7009: an access or refresh token of this client ends its
// connection. Unknown tokens are not an error.
func RevokeToken(q db.Q, a ClientAuth, token string) error {
	c, err := authenticateClient(q, a)
	if err != nil {
		return err
	}
	h := HashToken(token)
	_, err = q.Exec("DELETE FROM mcp_grants WHERE kind = ? AND client_id = ? AND (access_hash = ? OR refresh_hash = ? OR prev_refresh_hash = ?)", KindOAuth, c.ID, h, h, h)
	return err
}
