package rpcsetup_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"

	_ "github.com/zakiego/palui/apps/server/internal/httpapi/rpcsetup"
)

// FakeConnector is one connector of the fake runtime (the golden's "fake"):
// enabled ones run with a fixed live state and fixed action answers.
type FakeConnector struct {
	ID      string                    `json:"id"`
	Enabled bool                      `json:"enabled"`
	Live    *jsonx.Object             `json:"live"`
	Test    connectors.TestResult     `json:"test"`
	Actions *jsonx.Map[*jsonx.Object] `json:"actions"`
}

// fakeRuntime is the in-process control API over its own database and a
// supervisor whose connectors answer as the specs say. The server's database
// is separate, as the generator's fake ControlClient was.
func fakeRuntime(t *testing.T, specs []FakeConnector) *httpapi.Runtime {
	t.Helper()
	hdb := testutil.DB(t)
	registry := map[string]*connectors.Runtime{}
	for i, sp := range specs {
		sp := sp
		enabled := sp.Enabled
		testutil.SeedConnector(t, hdb, sp.ID, "tools", testutil.SeedOptions{Enabled: &enabled}, int64(i+1))
		registry[sp.ID] = &connectors.Runtime{
			Manifest: connectors.Manifest{ID: sp.ID, Kind: "tools", Name: sp.ID},
			Test:     func(connectors.Context) (connectors.TestResult, error) { return sp.Test, nil },
			Start: func(connectors.Context) (*connectors.RunningConnector, error) {
				h := &connectors.RunningConnector{Live: func() *jsonx.Object { return sp.Live }}
				if sp.Actions != nil {
					h.Action = func(_ context.Context, name string, _ *jsonx.Object) (*jsonx.Object, error) {
						a, ok := sp.Actions.Get(name)
						if !ok {
							return nil, &connectors.StatusError{Status: 404, Msg: "Unknown action " + name}
						}
						if res, ok := a.Get("result"); ok {
							return jsonx.ToObject(res)
						}
						status, _ := a.Get("status")
						msg, _ := a.Get("error")
						return nil, &connectors.StatusError{Status: int(status.(float64)), Msg: msg.(string)}
					}
				}
				return h, nil
			},
		}
	}
	h := chattest.New(t, chattest.Options{DB: hdb, Registry: registry})
	return &httpapi.Runtime{DB: h.DB, Engine: h.Engine, Sup: h.Sup, Bus: h.Bus, StartedAt: 0, Clock: h.Clock.Now}
}

// seededCopy copies testdata/seeded.db into dir and opens it.
func seededCopy(t *testing.T, dir string) *db.DB {
	t.Helper()
	src, err := os.Open(filepath.Join("..", "..", "..", "testdata", "seeded.db"))
	testutil.Must(t, err)
	defer src.Close()
	file := filepath.Join(dir, "palui.db")
	dst, err := os.Create(file)
	testutil.Must(t, err)
	_, err = io.Copy(dst, src)
	testutil.Must(t, err)
	testutil.Must(t, dst.Close())
	d, err := db.Open(file, db.OpenOptions{})
	testutil.Must(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

// writeBackup writes a file of n bytes under dir/backups with an mtime in seconds.
func writeBackup(t *testing.T, dir, name string, n int, mtime float64) {
	t.Helper()
	testutil.Must(t, os.MkdirAll(filepath.Join(dir, "backups"), 0o755))
	f := filepath.Join(dir, "backups", name)
	testutil.Must(t, os.WriteFile(f, make([]byte, n), 0o644))
	at := time.UnixMilli(int64(mtime * 1000))
	testutil.Must(t, os.Chtimes(f, at, at))
}

// env is a server on db with the owner signed in.
func env(t *testing.T, d *db.DB, dir string, rt *httpapi.Runtime, now int64, masterKey string) (*apitest.Env, string) {
	t.Helper()
	e := apitest.New(t, func(o *httpapi.Options) {
		o.DB = d
		o.Config.DataDir = dir
		if masterKey != "" {
			o.MasterKey = masterKey
		}
		if rt != nil {
			o.Runtime = rt
		}
	})
	if now != 0 {
		e.Now.Store(now)
	}
	return e, e.SignIn()
}
