// Package gcal is the Google Calendar connector (connectors/src/gcal): the
// owner's own OAuth client, a signed single-use state, token refresh and
// read-only calendar reads over plain REST.
package gcal

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

const (
	GoogleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenURL = "https://oauth2.googleapis.com/token"
	CalendarAPI    = "https://www.googleapis.com/calendar/v3"
	CalendarScope  = "https://www.googleapis.com/auth/calendar.readonly"
	StateTTLMs     = 10 * 60_000
	stateDomain    = "palui:oauth:google"
)

// GoogleError is a Google answer: Status 0 for a network failure.
type GoogleError struct {
	Status  int
	Code    string
	Message string
}

func (e *GoogleError) Error() string  { return e.Message }
func (e *GoogleError) JSName() string { return "GoogleError" }

// RedirectURIFor is redirectUriFor.
func RedirectURIFor(appURL string) string {
	return strings.TrimRight(appURL, "/") + "/api/oauth/google/callback"
}

var b64 = base64.RawURLEncoding

func sign(masterKey, payload string) string {
	m := hmac.New(sha256.New, []byte(masterKey))
	m.Write([]byte(stateDomain + ":" + payload))
	return b64.EncodeToString(m.Sum(nil))
}

// IssuedState is a signed state with its nonce.
type IssuedState struct {
	State     string
	Nonce     string
	ExpiresAt int64
}

// IssueState signs {c, e, n} for one connector, valid 10 minutes.
func IssueState(masterKey, connectorID string, now int64) IssuedState {
	nb := make([]byte, 16)
	_, _ = rand.Read(nb)
	nonce := b64.EncodeToString(nb)
	exp := now + StateTTLMs
	payloadJSON := jsonx.Stringify(obj("c", connectorID, "e", exp, "n", nonce))
	payload := b64.EncodeToString([]byte(payloadJSON))
	return IssuedState{State: payload + "." + sign(masterKey, payload), Nonce: nonce, ExpiresAt: exp}
}

// SignState is a signed state only (tests).
func SignState(masterKey, connectorID string, now int64) string {
	return IssueState(masterKey, connectorID, now).State
}

// NonceConfig is the connector config patch holding the one outstanding nonce.
func NonceConfig(s IssuedState) *jsonx.Object {
	return obj("oauthNonce", s.Nonce, "oauthNonceExp", s.ExpiresAt)
}

// StateResult is verifyState's answer.
type StateResult struct {
	OK          bool   `json:"ok"`
	ConnectorID string `json:"connectorId,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
	// malformed, bad-signature or expired.
	Reason string `json:"reason,omitempty"`
}

// VerifyState is verifyState.
func VerifyState(masterKey, state string, now int64) StateResult {
	dot := strings.LastIndex(state, ".")
	if dot <= 0 {
		return StateResult{Reason: "malformed"}
	}
	payload := state[:dot]
	a, b := []byte(state[dot+1:]), []byte(sign(masterKey, payload))
	if len(a) != len(b) || subtle.ConstantTimeCompare(a, b) != 1 {
		return StateResult{Reason: "bad-signature"}
	}
	raw, err := decodeLoose(payload)
	if err != nil {
		return StateResult{Reason: "malformed"}
	}
	var p struct {
		C *string  `json:"c"`
		E *float64 `json:"e"`
		N *string  `json:"n"`
	}
	if json.Unmarshal(raw, &p) != nil || p.C == nil || p.E == nil || p.N == nil {
		return StateResult{Reason: "malformed"}
	}
	if int64(*p.E) <= now {
		return StateResult{Reason: "expired"}
	}
	return StateResult{OK: true, ConnectorID: *p.C, Nonce: *p.N}
}

func decodeLoose(s string) ([]byte, error) {
	s = strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(s), "=")
	return b64.DecodeString(s)
}

// ConsumeNonce takes the stored nonce off the connector if it matches. The
// read and the write share one transaction, so two callbacks can't both win.
func ConsumeNonce(d *db.DB, connectorID, nonce string, now int64) (bool, error) {
	won := false
	err := d.Tx(func(tx db.Q) error {
		row, found, err := connectors.Get(tx, connectorID)
		if err != nil || !found {
			return err
		}
		cfg := row.Config
		if cfg == nil {
			cfg = jsonx.NewMap[any]()
		}
		n, hasN := cfg.Get("oauthNonce")
		exp, hasExp := cfg.Get("oauthNonceExp")
		if !hasN && !hasExp {
			return nil
		}
		rest := cfg.Clone()
		rest.Delete("oauthNonce")
		rest.Delete("oauthNonceExp")
		b, err := jsonx.Marshal(rest)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE connectors SET config = ?, updated_at = ? WHERE id = ?", string(b), now, connectorID); err != nil {
			return err
		}
		ns, ok1 := n.(string)
		ef, ok2 := exp.(float64)
		if !ok1 || !ok2 || int64(ef) <= now {
			return nil
		}
		won = len(ns) == len(nonce) && subtle.ConstantTimeCompare([]byte(ns), []byte(nonce)) == 1
		return nil
	})
	return won, err
}

// AuthURLInput is buildAuthUrl's input.
type AuthURLInput struct {
	ClientID    string
	RedirectURI string
	State       string
	LoginHint   string
}

// BuildAuthURL asks for calendar.readonly, offline access and consent.
func BuildAuthURL(in AuthURLInput) string {
	params := [][2]string{
		{"client_id", in.ClientID}, {"redirect_uri", in.RedirectURI}, {"response_type", "code"}, {"scope", CalendarScope},
		{"access_type", "offline"}, {"prompt", "consent"}, {"include_granted_scopes", "true"}, {"state", in.State},
	}
	if in.LoginHint != "" {
		params = append(params, [2]string{"login_hint", in.LoginHint})
	}
	return GoogleAuthURL + "?" + formEncode(params)
}

func formEncode(params [][2]string) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = url.QueryEscape(p[0]) + "=" + url.QueryEscape(p[1])
	}
	return strings.Join(parts, "&")
}

// GoogleAuthURLFor is googleAuthUrl: the URL the dashboard opens for the
// "oauth" wizard step. It stores the nonce on the connector.
func GoogleAuthURLFor(d *db.DB, connectorID, masterKey, appURL string, now int64) (string, error) {
	row, found, err := connectors.Get(d, connectorID)
	if err != nil {
		return "", err
	}
	if !found || row.Type != Manifest.ID {
		return "", fmt.Errorf("No Google Calendar connector %s", connectorID)
	}
	secrets, err := connectors.Secrets(row, masterKey)
	if err != nil {
		return "", err
	}
	clientID, _ := secrets.Get("clientId")
	if clientID == "" {
		return "", fmt.Errorf("Save the OAuth client ID first")
	}
	email := ""
	if row.Config != nil {
		v, _ := row.Config.Get("email")
		email, _ = v.(string)
	}
	issued := IssueState(masterKey, connectorID, now)
	if err := connectors.PatchConfig(d, connectorID, NonceConfig(issued), now); err != nil {
		return "", err
	}
	return BuildAuthURL(AuthURLInput{ClientID: clientID, RedirectURI: RedirectURIFor(appURL), State: issued.State, LoginHint: email}), nil
}

func client(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}

func tokenCall(ctx context.Context, body [][2]string, hc *http.Client) (*jsonx.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", GoogleTokenURL, strings.NewReader(formEncode(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("accept", "application/json")
	res, err := client(hc).Do(req)
	if err != nil {
		return nil, &GoogleError{0, "network", "Can't reach Google: " + err.Error()}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	data, err := jsonx.ParseObject(raw)
	if err != nil {
		data = jsonx.NewMap[any]()
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		code := fmt.Sprintf("http_%d", res.StatusCode)
		if s, ok := getStr(data, "error"); ok {
			code = s
		}
		desc, _ := getStr(data, "error_description")
		hint := "Google token error " + code
		switch code {
		case "invalid_grant":
			hint = "Google access was revoked or expired. Connect with Google again."
		case "invalid_client":
			hint = "Google rejected the OAuth client ID or secret."
		case "redirect_uri_mismatch":
			hint = "The redirect URI isn't registered on the OAuth client."
		}
		if desc != "" {
			hint = hint + " (" + desc + ")"
		}
		return nil, &GoogleError{res.StatusCode, code, hint}
	}
	return data, nil
}

func getStr(o *jsonx.Object, k string) (string, bool) {
	if o == nil {
		return "", false
	}
	v, _ := o.Get(k)
	s, ok := v.(string)
	return s, ok
}

func expiresIn(o *jsonx.Object) float64 {
	v, ok := o.Get("expires_in")
	if !ok || v == nil {
		return 3600
	}
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f
		}
	}
	return 3600
}

// Tokens is exchangeCode's answer.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    float64
	Scope        string
}

// ExchangeCode is exchangeCode.
func ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string, hc *http.Client) (Tokens, error) {
	data, err := tokenCall(ctx, [][2]string{{"grant_type", "authorization_code"}, {"code", code}, {"client_id", clientID}, {"client_secret", clientSecret}, {"redirect_uri", redirectURI}}, hc)
	if err != nil {
		return Tokens{}, err
	}
	t := Tokens{ExpiresIn: expiresIn(data)}
	if v, ok := data.Get("access_token"); ok && v != nil {
		t.AccessToken = fmt.Sprint(v)
	}
	t.RefreshToken, _ = getStr(data, "refresh_token")
	t.Scope, _ = getStr(data, "scope")
	return t, nil
}

// RefreshAccessToken is refreshAccessToken: the token and when it expires.
func RefreshAccessToken(ctx context.Context, clientID, clientSecret, refreshToken string, hc *http.Client, now int64) (string, int64, error) {
	data, err := tokenCall(ctx, [][2]string{{"grant_type", "refresh_token"}, {"refresh_token", refreshToken}, {"client_id", clientID}, {"client_secret", clientSecret}}, hc)
	if err != nil {
		return "", 0, err
	}
	token := ""
	if v, ok := data.Get("access_token"); ok && v != nil {
		token = fmt.Sprint(v)
	}
	if token == "" {
		return "", 0, &GoogleError{500, "no_token", "Google returned no access token"}
	}
	return token, now + int64(expiresIn(data)*1000), nil
}

// PrimaryEmail is the primary calendar's id, which is the account email.
func PrimaryEmail(ctx context.Context, accessToken string, hc *http.Client) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", CalendarAPI+"/calendars/primary", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("authorization", "Bearer "+accessToken)
	res, err := client(hc).Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return ""
	}
	var body struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(res.Body).Decode(&body) != nil || !strings.Contains(body.ID, "@") {
		return ""
	}
	return body.ID
}

// CallbackResult is what the callback page shows.
type CallbackResult struct {
	OK          bool   `json:"ok"`
	Detail      string `json:"detail"`
	ConnectorID string `json:"connectorId,omitempty"`
}

// HandleGoogleCallback is GET /api/oauth/google/callback?code&state (or
// ?error): verify the state, consume its nonce, exchange the code, store the
// refresh token encrypted and learn the email. M8's route calls it with the
// request's query.
func HandleGoogleCallback(ctx context.Context, d *db.DB, query url.Values, masterKey, appURL string, hc *http.Client, now int64) (CallbackResult, error) {
	state := query.Get("state")
	if state == "" {
		return CallbackResult{Detail: "The sign-in link is missing its state. Start again from the dashboard."}, nil
	}
	v := VerifyState(masterKey, state, now)
	if !v.OK {
		if v.Reason == "expired" {
			return CallbackResult{Detail: "The sign-in link expired (10 minutes). Start again."}, nil
		}
		return CallbackResult{Detail: "The sign-in link isn't valid. Start again."}, nil
	}
	id := v.ConnectorID
	row, found, err := connectors.Get(d, id)
	if err != nil {
		return CallbackResult{}, err
	}
	if !found || row.Type != Manifest.ID {
		return CallbackResult{ConnectorID: id, Detail: "That Calendar connector no longer exists."}, nil
	}
	won, err := ConsumeNonce(d, id, v.Nonce, now)
	if err != nil {
		return CallbackResult{}, err
	}
	if !won {
		return CallbackResult{ConnectorID: id, Detail: "That sign-in link was already used or replaced by a newer one. Start again from the dashboard."}, nil
	}
	if e := query.Get("error"); e != "" {
		if e == "access_denied" {
			return CallbackResult{ConnectorID: id, Detail: "You cancelled the Google sign-in."}, nil
		}
		return CallbackResult{ConnectorID: id, Detail: "Google said: " + e}, nil
	}
	code := query.Get("code")
	if code == "" {
		return CallbackResult{ConnectorID: id, Detail: "Google didn't send a code."}, nil
	}
	secrets, err := connectors.Secrets(row, masterKey)
	if err != nil {
		return CallbackResult{}, err
	}
	clientID, _ := secrets.Get("clientId")
	clientSecret, _ := secrets.Get("clientSecret")
	if clientID == "" || clientSecret == "" {
		return CallbackResult{ConnectorID: id, Detail: "Save the OAuth client ID and secret first."}, nil
	}
	tokens, err := ExchangeCode(ctx, clientID, clientSecret, code, RedirectURIFor(appURL), hc)
	if err != nil {
		return CallbackResult{ConnectorID: id, Detail: err.Error()}, nil
	}
	if tokens.RefreshToken == "" {
		return CallbackResult{ConnectorID: id, Detail: "Google didn't return a refresh token. Remove Palui at myaccount.google.com/permissions and connect again."}, nil
	}
	if tokens.Scope != "" && !contains(strings.Split(tokens.Scope, " "), CalendarScope) {
		return CallbackResult{ConnectorID: id, Detail: "Calendar access wasn't granted. Tick the calendar permission when Google asks."}, nil
	}
	email := PrimaryEmail(ctx, tokens.AccessToken, hc)
	sec := jsonx.NewMap[string]()
	sec.Set("refreshToken", tokens.RefreshToken)
	in := connectors.UpsertInput{ID: id, Manifest: Manifest.Row(), Secrets: sec}
	if email != "" {
		in.Config = obj("email", email)
	}
	if _, err := connectors.Upsert(d, in, masterKey, now); err != nil {
		return CallbackResult{}, err
	}
	detail := "Connected to Google Calendar"
	if email != "" {
		detail = "Connected as " + email
	}
	return CallbackResult{OK: true, ConnectorID: id, Detail: detail}, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}
