package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

const appURL = "http://localhost:4890"

type env struct {
	t   *testing.T
	d   *db.DB
	s   *httpapi.Server
	h   http.Handler
	now atomic.Int64
}

func newEnv(t *testing.T, tweak func(*httpapi.Options)) *env {
	t.Helper()
	e := &env{t: t, d: testutil.DB(t)}
	e.now.Store(time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC).UnixMilli())
	o := httpapi.Options{
		DB: e.d, Config: config.Config{AppURL: appURL, DataDir: t.TempDir(), Mode: "dev"}, MasterKey: testutil.TestKey,
		Clock: e.now.Load, SessionRecheck: 30 * time.Millisecond, Keepalive: 60 * time.Millisecond, EventPoll: time.Hour,
	}
	if tweak != nil {
		tweak(&o)
	}
	if o.DB != e.d {
		e.d = o.DB
	}
	s, err := httpapi.New(o)
	testutil.Must(t, err)
	t.Cleanup(s.CloseStreams)
	e.s, e.h = s, s.Handler()
	return e
}

type reqOpt func(*http.Request)

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func sameOrigin() reqOpt        { return header("origin", appURL) }
func cookie(token string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token}) }
}
func withCookie(name, value string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: value}) }
}

func (e *env) do(method, path, body string, opts ...reqOpt) *http.Response {
	e.t.Helper()
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
	e.h.ServeHTTP(rec, r)
	return rec.Result()
}

// doFrom is do from a given remote address, without a body.
func (e *env) doFrom(remote, method, path string, opts ...reqOpt) *http.Response {
	e.t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = remote
	for _, o := range opts {
		o(r)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec.Result()
}

func readBody(r *http.Response) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func decode(t *testing.T, r *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readBody(r)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// signIn creates the owner through an invite and returns a session token.
func (e *env) signIn() string {
	e.t.Helper()
	inv, err := auth.CreateOwnerInvite(e.d, appURL, e.now.Load())
	testutil.Must(e.t, err)
	res, err := auth.AcceptOwnerInvite(e.d, inv.Token, e.now.Load())
	testutil.Must(e.t, err)
	token, _, err := auth.CreateSession(e.d, res.UserID, "", "", e.now.Load())
	testutil.Must(e.t, err)
	return token
}

func sessionCookieOf(r *http.Response) *http.Cookie {
	for _, c := range r.Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}
