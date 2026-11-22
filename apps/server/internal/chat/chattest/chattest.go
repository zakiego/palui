// Package chattest is the engine harness of chat/testing.ts (harness,
// scriptedModel helpers, tgMessage) for the Go tests of the engine and of the
// later milestones that drive it. Not imported by runtime code.
package chattest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Start is the harness clock's default: Date.UTC(2026, 10, 10, 6, 0, 0).
var Start = time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC).UnixMilli()

// Clock is the harness's { now } object.
type Clock struct {
	mu  sync.Mutex
	now int64
}

func NewClock(now int64) *Clock { return &Clock{now: now} }

func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Set(now int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func (c *Clock) Add(ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += ms
}

// Row is a connector row to seed (enabled and on): {id, kind}.
type Row [2]string

// Options are HarnessOptions. Rows keep their order (it is the connectors' order).
type Options struct {
	DB            *db.DB
	Registry      map[string]*connectors.Runtime
	Rows          []Row
	Deps          chat.Deps
	ApprovalTTLMs int64
	TurnTimeoutMs int64
	Clock         *Clock
	Log           *logx.Logger
	// Waits before a failed message turn runs again (default: an hour, so a
	// test fires retries itself with Engine.RetryNow).
	RetryDelaysMs []int64
}

// Harness is what harness() returns.
type Harness struct {
	DB     *db.DB
	Sup    *connectors.Supervisor
	Engine *chat.Engine
	Bus    *chat.Bus
	Clock  *Clock
}

// New is harness(o): a supervisor and an engine on the database, connectors started.
func New(t testing.TB, o Options) *Harness {
	t.Helper()
	d := o.DB
	if d == nil {
		d = testutil.DB(t)
	}
	clock := o.Clock
	if clock == nil {
		clock = NewClock(Start)
	}
	for _, r := range o.Rows {
		var n int
		if err := d.QueryRow("SELECT count(*) FROM connectors WHERE id = ?", r[0]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			testutil.SeedConnector(t, d, r[0], r[1], testutil.SeedOptions{}, 1)
		}
	}
	log := o.Log
	if log == nil {
		log = logx.Silent
	}
	retryDelays := o.RetryDelaysMs
	if retryDelays == nil {
		retryDelays = []int64{3_600_000, 3_600_000, 3_600_000, 3_600_000}
	}
	services := &connectors.Services{}
	var engine *chat.Engine
	sup := connectors.NewSupervisor(connectors.SupervisorOptions{
		DB: d, Registry: o.Registry, Services: services, Log: log, MasterKey: testutil.TestKey, DataDir: "/tmp/palui-test",
		Clock: clock.Now,
		InUse: func(id string) bool { return engine != nil && engine.InUse(id) },
		OnOnline: func(string) {
			if engine != nil {
				engine.RetryNow()
			}
		},
	})
	bus := chat.NewBus()
	engine = chat.NewEngine(chat.EngineOptions{
		DB: d, Host: sup, Log: log, Clock: clock.Now, Bus: bus, Deps: o.Deps,
		AppURL: config.Get().AppURL, DataDir: config.Get().DataDir,
		ApprovalTTLMs: o.ApprovalTTLMs, TurnTimeoutMs: o.TurnTimeoutMs, SendWaitMs: 20, RetryDelay: time.Millisecond, RetryDelaysMs: retryDelays,
	})
	*services = *engine.Services
	sup.Reconcile()
	t.Cleanup(func() {
		engine.Stop(100)
		sup.Stop()
	})
	return &Harness{DB: d, Sup: sup, Engine: engine, Bus: bus, Clock: clock}
}

// OwnTelegram makes Telegram user 42 the owner (ownTelegram in the TS tests).
func OwnTelegram(t testing.TB, q db.Q) {
	t.Helper()
	c, err := owner.CreateClaim(q, "telegram", 1)
	testutil.Must(t, err)
	_, err = owner.ClaimOwner(q, owner.ClaimInput{Channel: "telegram", Nonce: c.Nonce, ExternalID: "42"}, 2)
	testutil.Must(t, err)
}

// ModelChain points every role at model "m" on these connectors (modelChain).
func ModelChain(t testing.TB, q db.Q, ids ...string) {
	t.Helper()
	links := make([]settings.ModelChainLink, len(ids))
	for i, id := range ids {
		links[i] = settings.ModelChainLink{ConnectorID: id, Model: "m"}
	}
	testutil.Must(t, settings.Set(q, "modelRoles", settings.ModelRoles{Default: links, Fast: links, Vision: links}, 1))
}

// Model is a model runtime over a scripted model.
func Model(id string, m models.LanguageModel) *connectors.Runtime {
	return conntest.ModelRuntime(id, func() models.LanguageModel { return m }, nil)
}

// Gate is a test gate: steps wait on it until Release (or the turn's context ends).
type Gate struct {
	once        sync.Once
	ch          chan struct{}
	reachedOnce sync.Once
	reached     chan struct{}
}

func NewGate() *Gate { return &Gate{ch: make(chan struct{}), reached: make(chan struct{})} }

func (g *Gate) Release() { g.once.Do(func() { close(g.ch) }) }

// Reached is closed once a step first waits on the gate.
func (g *Gate) Reached() <-chan struct{} { return g.reached }

// Wait blocks until Release; ok false when ctx ended first.
func (g *Gate) Wait(ctx context.Context) bool {
	g.reachedOnce.Do(func() { close(g.reached) })
	select {
	case <-g.ch:
		return true
	case <-ctx.Done():
		return false
	}
}

// After is a step that waits for the gate, then plays s.
func (g *Gate) After(s models.Step) models.Step {
	return models.Step{Fn: func(ctx context.Context, opts models.CallOptions) ([]models.Event, error) {
		if !g.Wait(ctx) {
			return nil, ctx.Err()
		}
		return s.Events, nil
	}}
}

// Sleep is a step that waits d (or until the call is cancelled), then plays s.
func Sleep(d time.Duration, s models.Step) models.Step {
	return models.Step{Fn: func(ctx context.Context, opts models.CallOptions) ([]models.Event, error) {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return s.Events, nil
	}}
}

// Wait sleeps d of real time (the TS tests' setTimeout pauses).
func Wait(d time.Duration) { time.Sleep(d) }
