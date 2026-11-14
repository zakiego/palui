// Package httpapi is the HTTP side of `palui serve`: /health, the auth routes
// that replace Better Auth, the RPC endpoints the dashboard calls (registered
// with Register; M8b adds the 78 server functions), both SSE streams, the
// attachment, share, hook and OAuth callback routes, and the dashboard SPA
// under /app.
//
// Routes (the edge Worker forwards /app, /api and /health to this server):
//
//	GET  /health                       no auth; 503 only for a database a newer build migrated
//	*    /api/auth/...                 sessions and passkeys (auth.go)
//	GET  /api/events/stream            SSE: summary + events frames (signed in)
//	GET  /api/chat/stream              SSE: turn events (signed in)
//	GET  /api/attachments/{id}         a saved attachment (signed in)
//	GET  /api/share/{token}            a share link (public, or signed in when private)
//	POST /api/hooks/{slug}             inbound webhooks (HMAC signed)
//	GET  /api/oauth/google/callback    the gcal OAuth redirect
//	GET|POST /api/rpc/{name}           dashboard server functions (rpc.go)
//	POST /api/admin/reconcile          loopback only, admin token: the CLI's poke
//	GET  /app/...                      the dashboard
//	GET  /                             302 to /app
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

// Options configure a Server.
type Options struct {
	DB        *db.DB
	Config    config.Config
	MasterKey string
	Log       *logx.Logger
	// Unix ms; default time.Now.
	Clock func() int64
	// The in-process runtime. Nil while it isn't running (a database a newer
	// build migrated): /health still answers, runtime calls fail with 503.
	Runtime *Runtime
	// The dashboard handler (web.Handler); nil serves 404 under /app.
	Web http.Handler
	// AdminToken guards POST /api/admin/reconcile ("" turns it off).
	AdminToken string
	// Rate limiter for /api/auth (default auth.NewLimiter()).
	Limiter *auth.Limiter
	// Tests shorten these: the session re-check of open streams (20 s), the
	// SSE keepalive (15 s) and the events table poll that backs up the
	// in-process push (5 s).
	SessionRecheck time.Duration
	Keepalive      time.Duration
	EventPoll      time.Duration
}

// Server is the HTTP side of `palui serve`.
type Server struct {
	o       Options
	rp      *auth.RP
	mux     *http.ServeMux
	streams sync.WaitGroup
	// Canceled by CloseStreams: every open SSE stream ends.
	streamCtx    context.Context
	closeStreams context.CancelFunc
}

// New builds the server and its routes.
func New(o Options) (*Server, error) {
	if o.Log == nil {
		o.Log = logx.Silent
	}
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	if o.Limiter == nil {
		o.Limiter = auth.NewLimiter()
	}
	if o.SessionRecheck == 0 {
		o.SessionRecheck = SessionRecheck
	}
	if o.Keepalive == 0 {
		o.Keepalive = SSEKeepalive
	}
	if o.EventPoll == 0 {
		o.EventPoll = EventPoll
	}
	rp, err := auth.NewRP(o.Config.AppURL)
	if err != nil {
		return nil, err
	}
	if rp.Off != "" {
		o.Log.Warn("passkeys are off: "+rp.Off+". Sign in with an owner invite (palui owner invite), or set APP_URL to a host name and restart", logx.Fields{"app_url": o.Config.AppURL})
	}
	s := &Server{o: o, rp: rp, mux: http.NewServeMux()}
	s.streamCtx, s.closeStreams = context.WithCancel(context.Background())
	s.routes()
	return s, nil
}

const (
	// SessionRecheck is stream-guard.ts SESSION_RECHECK_MS.
	SessionRecheck = 20 * time.Second
	// SSEKeepalive is the keepalive comment interval of both streams.
	SSEKeepalive = 15 * time.Second
	// EventPoll: events are pushed in process; this poll catches rows another
	// process wrote (the admin CLI).
	EventPoll = 5 * time.Second
	// maxBody caps JSON request bodies (the control API's 1 MB).
	maxBody = 1 << 20
)

func (s *Server) now() int64 { return s.o.Clock() }

// Handler is the root handler with panic recovery.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				s.o.Log.Error("request panicked", logx.Fields{"path": r.URL.Path, "panic": fmt.Sprint(p), "stack": string(debug.Stack())})
				writeJSON(w, 500, map[string]string{"error": "Internal error"})
			}
		}()
		s.mux.ServeHTTP(w, r)
	})
}

// CloseStreams ends every open SSE stream and waits for their handlers (the
// shutdown's "HTTP server and SSE streams" step; http.Server.Shutdown alone
// would wait for them forever).
func (s *Server) CloseStreams() {
	s.closeStreams()
	s.streams.Wait()
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /health", s.health)
	m.HandleFunc("/api/auth/", s.authRoute)
	m.HandleFunc("GET /api/events/stream", s.eventsStream)
	m.HandleFunc("GET /api/chat/stream", s.chatStream)
	m.HandleFunc("GET /api/attachments/{id}", s.attachment)
	m.HandleFunc("GET /api/share/{token}", s.share)
	m.HandleFunc("POST /api/hooks/{slug}", s.hook)
	m.HandleFunc("GET /api/oauth/google/callback", s.googleCallback)
	m.HandleFunc("/api/rpc/{name}", s.rpc)
	m.HandleFunc("POST /api/admin/reconcile", s.adminReconcile)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "NOT_FOUND"})
	})
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app", http.StatusFound)
	})
	app := func(w http.ResponseWriter, r *http.Request) {
		if s.o.Web == nil {
			http.NotFound(w, r)
			return
		}
		s.o.Web.ServeHTTP(w, r)
	}
	m.HandleFunc("/app", app)
	m.HandleFunc("/app/", app)
}

// --- helpers -------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := jsonx.Marshal(v)
	if err != nil {
		body = []byte(`{"error":"Internal error"}`)
		status = 500
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("content-length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// readJSON decodes a JSON body of at most 1 MB into v. An empty body leaves v alone.
func readJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		if err.Error() == "EOF" {
			return nil
		}
		return &Error{Status: 400, Message: "Body isn't valid JSON"}
	}
	return nil
}

// Viewer is the signed-in caller, or nil. A session due for its daily
// refresh is refreshed and the cookie set again on w.
func (s *Server) Viewer(w http.ResponseWriter, r *http.Request) (*auth.Viewer, error) {
	f, err := auth.GetSession(s.o.DB, auth.TokenFrom(r), true, s.now())
	if err != nil || f == nil {
		return nil, err
	}
	if f.Refreshed && w != nil {
		http.SetCookie(w, auth.SessionCookie(s.o.Config.AppURL, auth.TokenFrom(r)))
	}
	return f.Viewer(), nil
}

// requireUserFromRequest is session.ts requireUserFromRequest: a 401
// {error: "UNAUTHORIZED"} when there's no session.
func (s *Server) requireUserFromRequest(w http.ResponseWriter, r *http.Request) *auth.Viewer {
	v, err := s.Viewer(w, r)
	if err != nil {
		s.o.Log.Error("session lookup failed", logx.Fields{"error": err})
	}
	if v == nil {
		writeJSON(w, 401, map[string]string{"error": "UNAUTHORIZED"})
		return nil
	}
	return v
}

// Go runs fn in a goroutine that recovers and logs its own panic, so one bad
// background task can't take the process down.
func Go(log *logx.Logger, name string, fn func()) {
	go func() {
		defer Recover(log, name)
		fn()
	}()
}

// Recover is the deferred half of Go, for loops started elsewhere.
func Recover(log *logx.Logger, name string) {
	if p := recover(); p != nil {
		if log == nil {
			log = logx.Silent
		}
		log.Error("background task panicked", logx.Fields{"task": name, "panic": fmt.Sprint(p), "stack": string(debug.Stack())})
	}
}
