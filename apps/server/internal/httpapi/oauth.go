package httpapi

import (
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/mcpaccess"
)

// The OAuth 2.1 authorization server for /mcp (internal/mcpaccess has the
// rules). claude.ai's custom connectors can't send a header, so they find
// these through the 401 /mcp answers with:
//
//	POST /mcp (no token)          401, WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource/mcp"
//	GET  /.well-known/oauth-protected-resource[/mcp]    resource + issuer
//	GET  /.well-known/oauth-authorization-server[/...]  endpoints
//	POST /api/oauth/register      dynamic client registration (open)
//	GET  /api/oauth/authorize     checks the request, 302 to /app/oauth/consent?request=<id>
//	                              (the dashboard page asks for the passkey session and the owner's yes)
//	POST /api/oauth/token         code + PKCE verifier, or refresh token
//	POST /api/oauth/revoke        RFC 7009
//
// Discovery, registration, token and revoke allow any origin: they carry no
// cookies, and browser-based MCP clients call them directly.

func (s *Server) oauthRoutes() {
	m := s.mux
	m.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMetadata)
	m.HandleFunc("GET /.well-known/oauth-protected-resource/{rest...}", s.resourceMetadata)
	m.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServerMetadata)
	m.HandleFunc("GET /.well-known/oauth-authorization-server/{rest...}", s.authServerMetadata)
	m.HandleFunc("OPTIONS /.well-known/", preflight)
	m.HandleFunc("POST "+mcpaccess.RegisterPath, s.oauthRegister)
	m.HandleFunc("GET "+mcpaccess.AuthorizePath, s.oauthAuthorize)
	m.HandleFunc("POST "+mcpaccess.TokenPath, s.oauthToken)
	m.HandleFunc("POST "+mcpaccess.RevokePath, s.oauthRevoke)
	for _, p := range []string{mcpaccess.RegisterPath, mcpaccess.TokenPath, mcpaccess.RevokePath} {
		m.HandleFunc("OPTIONS "+p, preflight)
	}
}

// openCORS marks a response readable from any origin.
func openCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("access-control-allow-origin", "*")
	h.Set("access-control-allow-methods", "GET, POST, DELETE, OPTIONS")
	h.Set("access-control-allow-headers", "Authorization, Content-Type, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
	h.Set("access-control-expose-headers", "WWW-Authenticate, Mcp-Session-Id, Mcp-Protocol-Version")
	h.Set("access-control-max-age", "86400")
}

func preflight(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	w.Header().Set("cache-control", "public, max-age=3600")
	writeJSON(w, 200, mcpaccess.ResourceMetadata(s.o.Config.AppURL))
}

func (s *Server) authServerMetadata(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	w.Header().Set("cache-control", "public, max-age=3600")
	writeJSON(w, 200, mcpaccess.AuthServerMetadata(s.o.Config.AppURL))
}

func writeOAuthError(w http.ResponseWriter, e *mcpaccess.OAuthError) {
	if e.Status == 401 {
		w.Header().Set("www-authenticate", `Basic realm="palui"`)
	}
	w.Header().Set("cache-control", "no-store")
	writeJSON(w, e.Status, map[string]string{"error": e.Code, "error_description": e.Description})
}

// oauthFail writes an OAuth refusal, or a 500 for anything else.
func (s *Server) oauthFail(w http.ResponseWriter, what string, err error) {
	var oe *mcpaccess.OAuthError
	if errors.As(err, &oe) {
		writeOAuthError(w, oe)
		return
	}
	s.o.Log.Error("oauth "+what+" failed", logx.Fields{"error": err})
	writeOAuthError(w, &mcpaccess.OAuthError{Status: 500, Code: "server_error", Description: "something went wrong on the server"})
}

// limited applies the auth rate limit (100 a minute per address and path).
func (s *Server) limited(w http.ResponseWriter, r *http.Request) bool {
	ok, wait := s.o.Limiter.Allow(auth.ClientIP(r.Header), r.URL.Path)
	if ok {
		return false
	}
	w.Header().Set("retry-after", strconv.FormatInt(wait, 10))
	writeOAuthError(w, &mcpaccess.OAuthError{Status: 429, Code: "slow_down", Description: "too many requests"})
	return true
}

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	if s.limited(w, r) {
		return
	}
	var in mcpaccess.RegisterInput
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 64<<10))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeOAuthError(w, &mcpaccess.OAuthError{Status: 400, Code: "invalid_client_metadata", Description: "the body must be a JSON object"})
		return
	}
	out, err := mcpaccess.Register(s.o.DB, in, s.now())
	if err != nil {
		s.oauthFail(w, "register", err)
		return
	}
	w.Header().Set("cache-control", "no-store")
	writeJSON(w, 201, out)
}

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	res, err := mcpaccess.Authorize(s.o.DB, r.URL.Query(), s.o.Config.AppURL, s.now())
	var pe *mcpaccess.PageError
	switch {
	case errors.As(err, &pe):
		authorizePage(w, 400, pe.Message)
		return
	case err != nil:
		s.o.Log.Error("oauth authorize failed", logx.Fields{"error": err})
		authorizePage(w, 500, "Something went wrong on Palui's side. Try again in a minute.")
		return
	case res.Redirect != "":
		http.Redirect(w, r, res.Redirect, http.StatusFound)
		return
	}
	http.Redirect(w, r, mcpaccess.ConsentPath+"?request="+url.QueryEscape(res.RequestID), http.StatusFound)
}

// authorizePage is the plain page for an authorize request Palui can't send
// back to the app.
func authorizePage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Palui</title>`+
		`<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1rem;color:#222}h1{font-size:1.25rem}</style></head>`+
		`<body><h1>Couldn't connect this app</h1><p>`+html.EscapeString(msg)+`</p></body></html>`)
}

// clientAuth reads client credentials from Basic auth or the form.
func clientAuth(r *http.Request) mcpaccess.ClientAuth {
	if id, sec, ok := r.BasicAuth(); ok {
		id, _ = url.QueryUnescape(id)
		sec, _ = url.QueryUnescape(sec)
		return mcpaccess.ClientAuth{ID: id, Secret: sec, Basic: true}
	}
	return mcpaccess.ClientAuth{ID: r.PostForm.Get("client_id"), Secret: r.PostForm.Get("client_secret")}
}

func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, &mcpaccess.OAuthError{Status: 400, Code: "invalid_request", Description: "the body must be application/x-www-form-urlencoded"})
		return false
	}
	return true
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	if s.limited(w, r) || !s.parseForm(w, r) {
		return
	}
	out, err := mcpaccess.Exchange(s.o.DB, r.PostForm, clientAuth(r), s.now())
	if err != nil {
		s.oauthFail(w, "token", err)
		return
	}
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("pragma", "no-cache")
	writeJSON(w, 200, out)
}

func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	if s.limited(w, r) || !s.parseForm(w, r) {
		return
	}
	if err := mcpaccess.RevokeToken(s.o.DB, clientAuth(r), r.PostForm.Get("token")); err != nil {
		s.oauthFail(w, "revoke", err)
		return
	}
	w.WriteHeader(200)
}
