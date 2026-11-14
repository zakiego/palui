// Package apitest is the shared test kit for packages that register dashboard
// RPC handlers: a server on a migrated in-memory database (or one the test
// passes in), a signed-in owner, and request helpers.
package apitest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// AppURL is the origin every test server is configured with.
const AppURL = "http://localhost:4890"

// Env is one test server.
type Env struct {
	T       testing.TB
	DB      *db.DB
	Server  *httpapi.Server
	Handler http.Handler
	Now     atomic.Int64
}

// New builds a server. tweak may replace the database (a copy of the seeded
// one, for example), set a Runtime, or change anything else in the options.
func New(t testing.TB, tweak func(*httpapi.Options)) *Env {
	t.Helper()
	e := &Env{T: t}
	e.Now.Store(time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC).UnixMilli())
	o := httpapi.Options{
		Config:    config.Config{AppURL: AppURL, DataDir: t.TempDir(), Mode: "dev"},
		MasterKey: testutil.TestKey,
		Clock:     e.Now.Load, SessionRecheck: 30 * time.Millisecond, Keepalive: 60 * time.Millisecond, EventPoll: time.Hour,
	}
	if tweak != nil {
		tweak(&o)
	}
	if o.DB == nil {
		o.DB = testutil.DB(t)
	}
	e.DB = o.DB
	s, err := httpapi.New(o)
	testutil.Must(t, err)
	t.Cleanup(s.CloseStreams)
	e.Server, e.Handler = s, s.Handler()
	return e
}

// ReqOpt changes a request before it is sent.
type ReqOpt func(*http.Request)

// Header sets one header.
func Header(k, v string) ReqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

// SameOrigin marks the request as coming from the dashboard.
func SameOrigin() ReqOpt { return Header("origin", AppURL) }

// Cookie sends the session cookie.
func Cookie(token string) ReqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token}) }
}

// Do sends one request to the server.
func (e *Env) Do(method, path, body string, opts ...ReqOpt) *http.Response {
	e.T.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "192.0.2.1:1234"
	if body != "" {
		r.Header.Set("content-type", "application/json")
	}
	for _, o := range opts {
		o(r)
	}
	rec := httptest.NewRecorder()
	e.Handler.ServeHTTP(rec, r)
	return rec.Result()
}

// SignIn creates the owner through an invite and returns a session token.
func (e *Env) SignIn() string {
	e.T.Helper()
	inv, err := auth.CreateOwnerInvite(e.DB, AppURL, e.Now.Load())
	testutil.Must(e.T, err)
	res, err := auth.AcceptOwnerInvite(e.DB, inv.Token, e.Now.Load())
	testutil.Must(e.T, err)
	token, _, err := auth.CreateSession(e.DB, res.UserID, "", "", e.Now.Load())
	testutil.Must(e.T, err)
	return token
}

// Get calls a GET function the way the dashboard does: the input, when there
// is one, goes in ?input= as JSON. It returns the status and the body text.
func (e *Env) Get(token, name, input string) (int, string) {
	e.T.Helper()
	path := "/api/rpc/" + name
	if input != "" {
		path += "?input=" + url.QueryEscape(input)
	}
	var opts []ReqOpt
	if token != "" {
		opts = append(opts, Cookie(token))
	}
	res := e.Do(http.MethodGet, path, "", opts...)
	return res.StatusCode, Body(res)
}

// Post calls a POST function from the dashboard's origin with a JSON body.
func (e *Env) Post(token, name, input string) (int, string) {
	e.T.Helper()
	opts := []ReqOpt{SameOrigin()}
	if token != "" {
		opts = append(opts, Cookie(token))
	}
	res := e.Do(http.MethodPost, "/api/rpc/"+name, input, opts...)
	return res.StatusCode, Body(res)
}

// Body reads a response body.
func Body(r *http.Response) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}
