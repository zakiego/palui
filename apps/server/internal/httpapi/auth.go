package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

// The auth routes that replace Better Auth's /api/auth handler. Only what the
// passkey-only dashboard needs exists (the TS allow-list, now as the route
// list): everything else under /api/auth is 404. Every request is rate
// limited per client IP and path; every POST must come from APP_URL.
//
//	GET  /api/auth/session                       null | {session, user}
//	POST /api/auth/sign-out                      {success: true}
//	GET  /api/auth/passkey/register-options      PublicKeyCredentialCreationOptionsJSON (signed in)
//	POST /api/auth/passkey/register              {response, name?} -> passkey item (signed in, fresh session)
//	GET  /api/auth/passkey/authenticate-options  PublicKeyCredentialRequestOptionsJSON
//	POST /api/auth/passkey/authenticate          {response} -> {session, user}, sets the cookie
//	GET  /api/auth/passkey/list                  [passkey item] (signed in)
//	POST /api/auth/passkey/rename                {id, name} -> {passkey} (signed in)
//
// Errors are {code, message} with the status (Better Auth's shape).

type authRoute struct {
	method string
	fn     func(s *Server, w http.ResponseWriter, r *http.Request) error
}

var authRoutes = map[string]authRoute{
	"/session":                      {http.MethodGet, (*Server).authSession},
	"/sign-out":                     {http.MethodPost, (*Server).authSignOut},
	"/passkey/register-options":     {http.MethodGet, (*Server).authRegisterOptions},
	"/passkey/register":             {http.MethodPost, (*Server).authRegister},
	"/passkey/authenticate-options": {http.MethodGet, (*Server).authAuthenticateOptions},
	"/passkey/authenticate":         {http.MethodPost, (*Server).authAuthenticate},
	"/passkey/list":                 {http.MethodGet, (*Server).authList},
	"/passkey/rename":               {http.MethodPost, (*Server).authRename},
}

func authError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func (s *Server) authRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/auth"), "/")
	route, ok := authRoutes[path]
	if !ok || r.URL.RawPath != "" {
		authError(w, 404, "NOT_FOUND", "Not found")
		return
	}
	if r.Method != route.method {
		authError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	// The real client IP from the proxy, in a header the client can't set.
	ip := auth.ClientIP(r.Header)
	r.Header.Set(auth.ClientIPHeader, ip)
	if r.Method == http.MethodPost && !auth.SameOrigin(r, s.o.Config.AppURL) {
		authError(w, 403, "INVALID_ORIGIN", "Invalid origin")
		return
	}
	if allowed, retry := s.o.Limiter.Allow(ip, path); !allowed {
		w.Header().Set("X-Retry-After", strconv.FormatInt(retry, 10))
		writeJSON(w, 429, map[string]string{"code": "TOO_MANY_REQUESTS", "message": "Too many requests. Please try again later."})
		return
	}
	if err := route.fn(s, w, r); err != nil {
		var ae *auth.Error
		var he *Error
		switch {
		case errors.As(err, &ae):
			authError(w, ae.Status, ae.Code, ae.Message)
		case errors.As(err, &he):
			code := he.Code
			if code == "" {
				code = "BAD_REQUEST"
			}
			authError(w, he.Status, code, he.Message)
		default:
			s.o.Log.Error("auth request failed", logx.Fields{"path": path, "error": err})
			authError(w, 500, "INTERNAL_SERVER_ERROR", "Internal error")
		}
	}
}

// SessionBody is GET /api/auth/session's body when signed in.
type SessionBody struct {
	Session SessionInfo     `json:"session"`
	User    auth.ViewerUser `json:"user"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	ExpiresAt int64  `json:"expiresAt"`
	CreatedAt int64  `json:"createdAt"`
}

func sessionBody(f *auth.Found) SessionBody {
	v := f.Viewer()
	return SessionBody{Session: SessionInfo{ID: f.Session.ID, ExpiresAt: f.Session.ExpiresAt, CreatedAt: f.Session.CreatedAt}, User: v.User}
}

func (s *Server) authSession(w http.ResponseWriter, r *http.Request) error {
	token := auth.TokenFrom(r)
	f, err := auth.GetSession(s.o.DB, token, true, s.now())
	if err != nil {
		return err
	}
	if f == nil {
		if token != "" {
			http.SetCookie(w, auth.ClearCookie(s.o.Config.AppURL, auth.CookieName))
		}
		writeJSON(w, 200, nil)
		return nil
	}
	if f.Refreshed {
		http.SetCookie(w, auth.SessionCookie(s.o.Config.AppURL, token))
	}
	writeJSON(w, 200, sessionBody(f))
	return nil
}

func (s *Server) authSignOut(w http.ResponseWriter, r *http.Request) error {
	if err := auth.DeleteSession(s.o.DB, auth.TokenFrom(r)); err != nil {
		return err
	}
	http.SetCookie(w, auth.ClearCookie(s.o.Config.AppURL, auth.CookieName))
	writeJSON(w, 200, map[string]bool{"success": true})
	return nil
}

func (s *Server) mustViewer(w http.ResponseWriter, r *http.Request) (*auth.Viewer, error) {
	v, err := s.Viewer(w, r)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, auth.ErrUnauthorized
	}
	return v, nil
}

func challengeToken(r *http.Request) string {
	c, err := r.Cookie(auth.ChallengeCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) authRegisterOptions(w http.ResponseWriter, r *http.Request) error {
	v, err := s.mustViewer(w, r)
	if err != nil {
		return err
	}
	opts, token, err := s.rp.RegisterOptions(s.o.DB, v, s.now())
	if err != nil {
		return err
	}
	http.SetCookie(w, auth.ChallengeCookieFor(s.o.Config.AppURL, token))
	writeJSON(w, 200, opts)
	return nil
}

type registerBody struct {
	Response json.RawMessage `json:"response"`
	Name     *string         `json:"name"`
}

func isObject(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	return strings.HasPrefix(t, "{")
}

func (s *Server) authRegister(w http.ResponseWriter, r *http.Request) error {
	v, err := s.mustViewer(w, r)
	if err != nil {
		return err
	}
	var body registerBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !isObject(body.Response) {
		return &Error{Status: 400, Code: "VALIDATION_ERROR", Message: "response is required"}
	}
	name := ""
	if body.Name != nil {
		name = *body.Name
	}
	pk, err := s.rp.Register(s.o.DB, v, challengeToken(r), body.Response, name, s.now())
	http.SetCookie(w, auth.ClearCookie(s.o.Config.AppURL, auth.ChallengeCookie))
	if err != nil {
		return err
	}
	writeJSON(w, 200, auth.PasskeyItem{ID: pk.ID, Name: pk.Name, DeviceType: pk.DeviceType, BackedUp: pk.BackedUp, CreatedAt: pk.CreatedAt})
	return nil
}

func (s *Server) authAuthenticateOptions(w http.ResponseWriter, r *http.Request) error {
	v, err := s.Viewer(w, r)
	if err != nil {
		return err
	}
	opts, token, err := s.rp.AuthenticateOptions(s.o.DB, v, s.now())
	if err != nil {
		return err
	}
	http.SetCookie(w, auth.ChallengeCookieFor(s.o.Config.AppURL, token))
	writeJSON(w, 200, opts)
	return nil
}

func (s *Server) authAuthenticate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Response json.RawMessage `json:"response"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !isObject(body.Response) {
		return &Error{Status: 400, Code: "VALIDATION_ERROR", Message: "response is required"}
	}
	userID, err := s.rp.Authenticate(s.o.DB, challengeToken(r), body.Response, s.now())
	http.SetCookie(w, auth.ClearCookie(s.o.Config.AppURL, auth.ChallengeCookie))
	if err != nil {
		return err
	}
	return s.signIn(w, r, userID)
}

// signIn starts a session for userID, sets the cookie and answers {session, user}.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, userID string) error {
	token, _, err := auth.CreateSession(s.o.DB, userID, r.Header.Get(auth.ClientIPHeader), r.UserAgent(), s.now())
	if err != nil {
		return &auth.Error{Status: 500, Code: "UNABLE_TO_CREATE_SESSION", Message: "Unable to create session"}
	}
	f, err := auth.GetSession(s.o.DB, token, false, s.now())
	if err != nil || f == nil {
		return &auth.Error{Status: 500, Code: "UNABLE_TO_CREATE_SESSION", Message: "Unable to create session"}
	}
	http.SetCookie(w, auth.SessionCookie(s.o.Config.AppURL, token))
	writeJSON(w, 200, sessionBody(f))
	return nil
}

func (s *Server) authList(w http.ResponseWriter, r *http.Request) error {
	v, err := s.mustViewer(w, r)
	if err != nil {
		return err
	}
	list, err := auth.ListPasskeys(s.o.DB, v.User.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, list)
	return nil
}

func (s *Server) authRename(w http.ResponseWriter, r *http.Request) error {
	v, err := s.mustViewer(w, r)
	if err != nil {
		return err
	}
	var body struct {
		ID   *string `json:"id"`
		Name *string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.ID == nil || body.Name == nil {
		return &Error{Status: 400, Code: "VALIDATION_ERROR", Message: "id and name are required"}
	}
	item, err := auth.RenamePasskey(s.o.DB, v.User.ID, *body.ID, *body.Name)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"passkey": item})
	return nil
}
