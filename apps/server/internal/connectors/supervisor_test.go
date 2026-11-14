package connectors_test

// Port of connectors/supervisor.test.ts.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type fake struct {
	runtime  *connectors.Runtime
	mu       sync.Mutex
	starts   int
	stops    int
	actions  []string
	ctxs     []connectors.Context
	manifest connectors.Manifest
}

func (f *fake) count() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

type behavior struct {
	fail         func() bool
	reportStatus string
	channel      connectors.ChannelHandle
	panicStart   bool
	panicStop    bool
}

func fakeRuntime(id, kind string, b behavior) *fake {
	f := &fake{}
	f.manifest = connectors.Manifest{ID: id, Kind: kind, Name: id, Icon: "x", Builtin: kind == "skill"}
	f.runtime = &connectors.Runtime{Manifest: f.manifest,
		Test: func(connectors.Context) (connectors.TestResult, error) {
			return connectors.TestResult{OK: true, Detail: "fine"}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			f.mu.Lock()
			f.starts++
			f.mu.Unlock()
			if b.panicStart {
				panic("boom")
			}
			if b.fail != nil && b.fail() {
				return nil, fmt.Errorf("%s exploded", id)
			}
			if b.reportStatus != "" {
				d := "waiting for pairing"
				c.SetStatus(b.reportStatus, &d)
			}
			return &connectors.RunningConnector{
				Stop: func(context.Context) error {
					f.mu.Lock()
					f.stops++
					f.mu.Unlock()
					if b.panicStop {
						panic("stop boom")
					}
					return nil
				},
				Channel: b.channel,
				Action: func(ctx context.Context, name string, in *jsonx.Object) (*jsonx.Object, error) {
					o := jsonx.NewMap[any]()
					o.Set("did", name)
					return o, nil
				},
			}, nil
		},
	}
	return f
}

type harness struct {
	db  *db.DB
	sup *connectors.Supervisor
	now atomic.Int64
}

func (h *harness) advance(ms int64) { h.now.Add(ms) }

func setup(t *testing.T, registry map[string]*fake, extra func(*connectors.SupervisorOptions)) *harness {
	h := &harness{db: testutil.DB(t)}
	h.now.Store(1_000_000)
	reg := map[string]*connectors.Runtime{}
	for k, v := range registry {
		reg[k] = v.runtime
	}
	o := connectors.SupervisorOptions{DB: h.db, Registry: reg, MasterKey: testutil.TestKey, DataDir: "/tmp/palui-test", Clock: func() int64 { return h.now.Load() }}
	if extra != nil {
		extra(&o)
	}
	h.sup = connectors.NewSupervisor(o)
	t.Cleanup(h.sup.Stop)
	return h
}

func row(t *testing.T, d *db.DB, id string) db.Connector {
	t.Helper()
	r, ok, err := connectors.Get(d, id)
	if err != nil || !ok {
		t.Fatalf("no row %s: %v", id, err)
	}
	return r
}

func seed(t *testing.T, d *db.DB, id, kind string, o testutil.SeedOptions) {
	testutil.SeedConnector(t, d, id, kind, o, 1)
}

func runningIDs(s *connectors.Supervisor) string {
	var ids []string
	for _, e := range s.Running() {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func TestSupervisorStartsAndKeepsReportedStatus(t *testing.T) {
	a := fakeRuntime("alpha", "tools", behavior{})
	wa := fakeRuntime("whatsapp", "channel", behavior{reportStatus: "connecting", channel: conntest.NewFakeChannel("whatsapp")})
	h := setup(t, map[string]*fake{"alpha": a, "whatsapp": wa}, nil)
	seed(t, h.db, "alpha", "tools", testutil.SeedOptions{Status: "connecting"})
	seed(t, h.db, "whatsapp", "channel", testutil.SeedOptions{Status: "connecting"})
	h.sup.Reconcile()
	if row(t, h.db, "alpha").Status != "on" {
		t.Fatal(row(t, h.db, "alpha").Status)
	}
	w := row(t, h.db, "whatsapp")
	if w.Status != "connecting" || *w.StatusDetail != "waiting for pairing" {
		t.Fatal(w.Status)
	}
	if runningIDs(h.sup) != "alpha,whatsapp" || h.sup.ChannelFor("whatsapp") == nil {
		t.Fatal(runningIDs(h.sup))
	}
	h.sup.Reconcile()
	if s, _ := a.count(); s != 1 {
		t.Fatal(s)
	}
}

func TestSupervisorPutsBackRunningStatusOnConnect(t *testing.T) {
	a := fakeRuntime("alpha", "tools", behavior{})
	wa := fakeRuntime("whatsapp", "channel", behavior{reportStatus: "connecting", channel: conntest.NewFakeChannel("whatsapp")})
	h := setup(t, map[string]*fake{"alpha": a, "whatsapp": wa}, nil)
	seed(t, h.db, "alpha", "tools", testutil.SeedOptions{})
	seed(t, h.db, "whatsapp", "channel", testutil.SeedOptions{})
	h.sup.Reconcile()
	testutil.Must(t, connectors.Connect(h.db, "alpha", 2))
	testutil.Must(t, connectors.Connect(h.db, "whatsapp", 2))
	if row(t, h.db, "alpha").Status != "connecting" {
		t.Fatal("connect")
	}
	h.sup.Reconcile()
	if row(t, h.db, "alpha").Status != "on" {
		t.Fatal(row(t, h.db, "alpha").Status)
	}
	if w := row(t, h.db, "whatsapp"); w.Status != "connecting" || *w.StatusDetail != "waiting for pairing" {
		t.Fatal(w.Status)
	}
	if s, _ := a.count(); s != 1 {
		t.Fatal(s)
	}
	if s, _ := wa.count(); s != 1 {
		t.Fatal(s)
	}
}

func TestSupervisorRetriesNowOnConnectOrConfigChange(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	bad := fakeRuntime("bad", "tools", behavior{fail: failing.Load})
	h := setup(t, map[string]*fake{"bad": bad}, nil)
	seed(t, h.db, "bad", "tools", testutil.SeedOptions{})
	h.sup.Reconcile()
	h.advance(5_000)
	h.sup.Reconcile()
	if s, _ := bad.count(); s != 2 {
		t.Fatal(s)
	}
	// Now 30 s into the ladder. A reconcile on its own still waits.
	h.sup.Reconcile()
	if s, _ := bad.count(); s != 2 {
		t.Fatal(s)
	}
	testutil.Must(t, connectors.Connect(h.db, "bad", 3))
	h.sup.Reconcile()
	if s, _ := bad.count(); s != 3 || row(t, h.db, "bad").Status != "error" {
		t.Fatal(s, row(t, h.db, "bad").Status)
	}
	failing.Store(false)
	cfg := jsonx.NewMap[any]()
	cfg.Set("fixed", true)
	_, err := connectors.Upsert(h.db, connectors.UpsertInput{ID: "bad", Manifest: bad.manifest.Row(), Config: cfg}, testutil.TestKey, 4)
	testutil.Must(t, err)
	h.sup.Reconcile()
	if s, _ := bad.count(); s != 4 || row(t, h.db, "bad").Status != "on" {
		t.Fatal(s, row(t, h.db, "bad").Status)
	}
}

func TestSupervisorPauseResumeAndConfigChange(t *testing.T) {
	a := fakeRuntime("alpha", "tools", behavior{})
	h := setup(t, map[string]*fake{"alpha": a}, nil)
	seed(t, h.db, "alpha", "tools", testutil.SeedOptions{})
	h.sup.Reconcile()
	testutil.Must(t, connectors.Pause(h.db, "alpha", "test", 2))
	// Pausing applies from the next turn even before the reconcile.
	if len(h.sup.Running()) != 0 {
		t.Fatal("still running")
	}
	h.sup.Reconcile()
	if _, st := a.count(); st != 1 || row(t, h.db, "alpha").Status != "paused" {
		t.Fatal(st)
	}
	testutil.Must(t, connectors.Resume(h.db, "alpha", "test", 3))
	h.sup.Reconcile()
	if s, _ := a.count(); s != 2 || row(t, h.db, "alpha").Status != "on" {
		t.Fatal(s)
	}
	cfg := jsonx.NewMap[any]()
	cfg.Set("x", 2)
	_, err := connectors.Upsert(h.db, connectors.UpsertInput{ID: "alpha", Manifest: a.manifest.Row(), Config: cfg}, testutil.TestKey, 4)
	testutil.Must(t, err)
	h.sup.Reconcile()
	if s, st := a.count(); s != 3 || st != 2 {
		t.Fatal(s, st)
	}
}

func TestSupervisorWaitsForRunningTurnBeforeStopping(t *testing.T) {
	a := fakeRuntime("alpha", "tools", behavior{})
	var busy atomic.Bool
	busy.Store(true)
	h := setup(t, map[string]*fake{"alpha": a}, func(o *connectors.SupervisorOptions) { o.InUse = func(string) bool { return busy.Load() } })
	seed(t, h.db, "alpha", "tools", testutil.SeedOptions{})
	h.sup.Reconcile()
	testutil.Must(t, connectors.Pause(h.db, "alpha", "test", 2))
	h.sup.Reconcile()
	if _, st := a.count(); st != 0 {
		t.Fatal(st)
	}
	busy.Store(false)
	h.sup.Reconcile()
	if _, st := a.count(); st != 1 {
		t.Fatal(st)
	}
}

func TestSupervisorBackoffAndOneAlert(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	bad := fakeRuntime("bad", "tools", behavior{fail: failing.Load})
	tgChannel := conntest.NewFakeChannel("telegram")
	tg := fakeRuntime("telegram", "channel", behavior{channel: tgChannel})
	h := setup(t, map[string]*fake{"bad": bad, "telegram": tg}, nil)
	claim, err := owner.CreateClaim(h.db, "telegram", 1)
	testutil.Must(t, err)
	_, err = owner.ClaimOwner(h.db, owner.ClaimInput{Channel: "telegram", Nonce: claim.Nonce, ExternalID: "42"}, 2)
	testutil.Must(t, err)
	seed(t, h.db, "telegram", "channel", testutil.SeedOptions{})
	seed(t, h.db, "bad", "tools", testutil.SeedOptions{Status: "connecting"})
	h.sup.Reconcile()
	if row(t, h.db, "bad").Status != "error" {
		t.Fatal("not error")
	}
	if s, _ := bad.count(); s != 1 {
		t.Fatal(s)
	}
	// Backoff: 5 s, 30 s, 2 min, 10 min cap.
	for _, w := range []int64{5_000, 30_000, 120_000, 600_000, 600_000} {
		h.advance(w - 1)
		h.sup.Reconcile()
		before, _ := bad.count()
		h.advance(1)
		h.sup.Reconcile()
		if after, _ := bad.count(); after != before+1 {
			t.Fatalf("wait %d: %d -> %d", w, before, after)
		}
	}
	alerts := systemOutbox(t, h.db)
	if len(alerts) != 1 || !strings.Contains(alerts[0].Payload.Text, "bad") || alerts[0].Channel != "telegram" {
		t.Fatal(alerts)
	}
	if n := countEvents(t, h.db, "alert"); n != 1 {
		t.Fatal(n)
	}
	h.advance(connectors.ErrorAlertAfterMs)
	h.sup.Reconcile()
	if len(systemOutbox(t, h.db)) != 1 {
		t.Fatal("alerted twice")
	}
	failing.Store(false)
	h.advance(600_000)
	h.sup.Reconcile()
	if r := row(t, h.db, "bad"); r.Status != "on" || r.ErrorSince != nil || r.AlertedAt != nil {
		t.Fatalf("%+v", r)
	}
}

func systemOutbox(t *testing.T, d *db.DB) []db.Outbox {
	rows, err := d.Query("SELECT " + db.OutboxCols + " FROM outbox WHERE source = 'system'")
	out, err := db.All(rows, err, db.ScanOutbox)
	testutil.Must(t, err)
	return out
}

func countEvents(t *testing.T, d *db.DB, kind string) int {
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM events WHERE kind = ?", kind).Scan(&n))
	return n
}

func TestSupervisorBuiltinsAndThrowingStop(t *testing.T) {
	skill := fakeRuntime("reminders", "skill", behavior{panicStop: true})
	h := setup(t, map[string]*fake{"reminders": skill}, nil)
	testutil.Must(t, h.sup.EnsureBuiltins([]connectors.Manifest{skill.manifest}))
	if r := row(t, h.db, "reminders"); !r.Enabled || r.Status != "connecting" {
		t.Fatalf("%+v", r)
	}
	h.sup.Reconcile()
	if row(t, h.db, "reminders").Status != "on" {
		t.Fatal("not on")
	}
	// A second boot doesn't touch it; a panicking stop doesn't take the process down.
	testutil.Must(t, h.sup.EnsureBuiltins([]connectors.Manifest{skill.manifest}))
	testutil.Must(t, connectors.Pause(h.db, "reminders", "test", 2))
	h.sup.Reconcile()
	if _, st := skill.count(); st != 1 {
		t.Fatal(st)
	}
}

func TestSupervisorRemoves(t *testing.T) {
	tg := fakeRuntime("telegram", "channel", behavior{channel: conntest.NewFakeChannel("telegram")})
	h := setup(t, map[string]*fake{"telegram": tg}, nil)
	seed(t, h.db, "telegram", "channel", testutil.SeedOptions{})
	h.sup.Reconcile()
	var rerouted []string
	res := h.sup.Remove(context.Background(), "telegram", func(ch string) { rerouted = append(rerouted, ch) })
	if !res.OK {
		t.Fatal(res)
	}
	if _, st := tg.count(); st != 1 || strings.Join(rerouted, ",") != "telegram" {
		t.Fatal(st, rerouted)
	}
	if _, ok, _ := connectors.Get(h.db, "telegram"); ok {
		t.Fatal("row kept")
	}
	if len(h.sup.Status()) != 0 {
		t.Fatal(h.sup.Status())
	}
}

// --- errors after start and remove ----------------------------------------------------

type extra struct {
	restartOnError *bool
	unlinkOnRemove bool
}

func live(id, kind string, ex extra, startFails func() bool) *fake {
	f := &fake{}
	f.manifest = connectors.Manifest{ID: id, Kind: kind, Name: id, Icon: "x", RestartOnError: ex.restartOnError, UnlinkOnRemove: ex.unlinkOnRemove}
	f.runtime = &connectors.Runtime{Manifest: f.manifest,
		Test: func(connectors.Context) (connectors.TestResult, error) { return connectors.TestResult{OK: true}, nil },
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			if startFails != nil && startFails() {
				return nil, fmt.Errorf("%s exploded", id)
			}
			f.mu.Lock()
			f.ctxs = append(f.ctxs, c)
			f.mu.Unlock()
			return &connectors.RunningConnector{
				Stop: func(context.Context) error {
					f.mu.Lock()
					f.stops++
					f.mu.Unlock()
					return nil
				},
				Action: func(ctx context.Context, name string, in *jsonx.Object) (*jsonx.Object, error) {
					f.mu.Lock()
					f.actions = append(f.actions, name)
					f.mu.Unlock()
					if name != "unlink" {
						return nil, fmt.Errorf("Unknown %s action: %s", id, name)
					}
					o := jsonx.NewMap[any]()
					o.Set("ok", true)
					return o, nil
				},
			}, nil
		},
	}
	return f
}

func (f *fake) ctx(i int) connectors.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctxs[i]
}

func (f *fake) nctx() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ctxs)
}

func bootLive(t *testing.T, registry map[string]*fake, onRemoved func(row db.Connector, unlinked bool) error) *harness {
	return setup(t, registry, func(o *connectors.SupervisorOptions) {
		o.BackoffMs = []int64{20, 40, 80}
		o.OnRemoved = onRemoved
	})
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func detail(s string) *string { return &s }

func TestSupervisorRestartsChannelOnReportedError(t *testing.T) {
	tg := live("telegram", "channel", extra{}, nil)
	h := bootLive(t, map[string]*fake{"telegram": tg}, nil)
	seed(t, h.db, "telegram", "channel", testutil.SeedOptions{})
	h.sup.Reconcile()
	if tg.nctx() != 1 {
		t.Fatal(tg.nctx())
	}
	tg.ctx(0).SetStatus("error", detail("409: Conflict: terminated by other getUpdates request"))
	waitUntil(t, func() bool { _, st := tg.count(); return st == 1 })
	if h.sup.IsRunning("telegram") {
		t.Fatal("still running")
	}
	if r := row(t, h.db, "telegram"); r.Status != "error" || *r.StatusDetail != "409: Conflict: terminated by other getUpdates request" {
		t.Fatalf("%+v", r)
	}
	// A report from the dead handle is ignored.
	tg.ctx(0).SetStatus("on", nil)
	if row(t, h.db, "telegram").Status != "error" {
		t.Fatal("dead handle wrote")
	}
	// The retry timer fires without waiting for the 30 s reconcile.
	h.advance(20)
	waitUntil(t, func() bool { return tg.nctx() == 2 && h.sup.IsRunning("telegram") })
	// Crashing again right away moves up the ladder (40 ms, not 20 ms).
	tg.ctx(1).SetStatus("error", detail("409 again"))
	waitUntil(t, func() bool { return !h.sup.IsRunning("telegram") })
	h.advance(20)
	h.sup.Reconcile()
	if tg.nctx() != 2 {
		t.Fatal(tg.nctx())
	}
	h.advance(20)
	h.sup.Reconcile()
	waitUntil(t, func() bool { return tg.nctx() == 3 })
}

func TestSupervisorLeavesToolConnectorsAndOptOutsAlone(t *testing.T) {
	no := false
	mcp := live("mcp", "tools", extra{}, nil)
	wa := live("whatsapp", "channel", extra{restartOnError: &no}, nil)
	h := bootLive(t, map[string]*fake{"mcp": mcp, "whatsapp": wa}, nil)
	seed(t, h.db, "mcp", "tools", testutil.SeedOptions{})
	seed(t, h.db, "whatsapp", "channel", testutil.SeedOptions{})
	h.sup.Reconcile()
	mcp.ctx(0).SetStatus("error", detail("listTools failed"))
	wa.ctx(0).SetStatus("error", detail("unlinked"))
	time.Sleep(10 * time.Millisecond)
	if _, st := mcp.count(); st != 0 {
		t.Fatal(st)
	}
	if _, st := wa.count(); st != 0 {
		t.Fatal(st)
	}
	if !h.sup.IsRunning("mcp") {
		t.Fatal("mcp stopped")
	}
}

func TestSupervisorRetriesFailedStartWithoutPoke(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	bad := live("bad", "tools", extra{}, failing.Load)
	h := bootLive(t, map[string]*fake{"bad": bad}, nil)
	seed(t, h.db, "bad", "tools", testutil.SeedOptions{})
	h.sup.Reconcile()
	if row(t, h.db, "bad").Status != "error" {
		t.Fatal("not error")
	}
	failing.Store(false)
	h.advance(20)
	waitUntil(t, func() bool { return h.sup.IsRunning("bad") })
}

func TestSupervisorUnlinksOnlyWhenDeclared(t *testing.T) {
	no := false
	tg := live("telegram", "channel", extra{}, nil)
	wa := live("whatsapp", "channel", extra{unlinkOnRemove: true, restartOnError: &no}, nil)
	type removed struct {
		id       string
		unlinked bool
	}
	var mu sync.Mutex
	var gone []removed
	h := bootLive(t, map[string]*fake{"telegram": tg, "whatsapp": wa}, func(r db.Connector, unlinked bool) error {
		mu.Lock()
		gone = append(gone, removed{r.ID, unlinked})
		mu.Unlock()
		return nil
	})
	seed(t, h.db, "telegram", "channel", testutil.SeedOptions{})
	seed(t, h.db, "whatsapp", "channel", testutil.SeedOptions{})
	h.sup.Reconcile()
	r1 := h.sup.Remove(context.Background(), "telegram", func(string) {})
	if !r1.OK || r1.Detail != nil || len(tg.actions) != 0 {
		t.Fatal(r1)
	}
	h.sup.Remove(context.Background(), "whatsapp", func(string) {})
	if strings.Join(wa.actions, ",") != "unlink" {
		t.Fatal(wa.actions)
	}
	// A paused WhatsApp: nothing running to unlink, but its session is still cleaned up.
	off := false
	seed(t, h.db, "whatsapp", "channel", testutil.SeedOptions{Enabled: &off})
	h.sup.Reconcile()
	h.sup.Remove(context.Background(), "whatsapp", func(string) {})
	if strings.Join(wa.actions, ",") != "unlink" {
		t.Fatal(wa.actions)
	}
	want := []removed{{"telegram", false}, {"whatsapp", true}, {"whatsapp", false}}
	if fmt.Sprint(gone) != fmt.Sprint(want) {
		t.Fatal(gone)
	}
}

// --- Go-specific isolation and the rest of the API --------------------------------------

func TestSupervisorIsolatesPanicsAndAnswersActionsAndTests(t *testing.T) {
	boom := fakeRuntime("boom", "tools", behavior{panicStart: true})
	a := fakeRuntime("alpha", "tools", behavior{})
	h := setup(t, map[string]*fake{"boom": boom, "alpha": a}, nil)
	seed(t, h.db, "boom", "tools", testutil.SeedOptions{})
	seed(t, h.db, "alpha", "tools", testutil.SeedOptions{})
	seed(t, h.db, "ghost", "tools", testutil.SeedOptions{Type: "nope"})
	h.sup.Reconcile()
	if r := row(t, h.db, "boom"); r.Status != "error" || !strings.Contains(*r.StatusDetail, "start panicked") {
		t.Fatalf("%+v", r)
	}
	if r := row(t, h.db, "ghost"); r.Status != "error" || *r.StatusDetail != `No runtime for connector type "nope" in this build` {
		t.Fatalf("%+v", r)
	}
	out, err := h.sup.Action(context.Background(), "alpha", "pair", nil)
	if err != nil || jsonx.Stringify(out) != `{"did":"pair"}` {
		t.Fatal(out, err)
	}
	_, err = h.sup.Action(context.Background(), "boom", "pair", nil)
	var se *connectors.StatusError
	if !errors.As(err, &se) || se.Status != 409 || se.Msg != "Connector boom isn't running" {
		t.Fatal(err)
	}
	if r := h.sup.Test("alpha"); !r.OK || r.Detail != "fine" {
		t.Fatal(r)
	}
	if r := h.sup.Test("missing"); r.OK || r.Detail != "No connector missing" {
		t.Fatal(r)
	}
	st := h.sup.Status()
	if len(st) != 3 {
		t.Fatal(st)
	}
	for _, s := range st {
		if s.ID == "alpha" && (!s.Running || s.Status != "on" || jsonx.Stringify(s.Live) != "{}") {
			t.Fatalf("%+v", s)
		}
	}
	if _, ok := h.sup.Get("alpha"); !ok {
		t.Fatal("get")
	}
	_ = events.Now
}

func TestSupervisorRunningOrder(t *testing.T) {
	a := fakeRuntime("a", "tools", behavior{})
	b := fakeRuntime("b", "tools", behavior{})
	c := fakeRuntime("c", "tools", behavior{})
	h := setup(t, map[string]*fake{"a": a, "b": b, "c": c}, nil)
	for _, id := range []string{"c", "a", "b"} {
		testutil.SeedConnector(t, h.db, id, "tools", testutil.SeedOptions{}, 5)
	}
	_, err := h.db.Exec("UPDATE connectors SET sort_order = 1 WHERE id = 'a'")
	testutil.Must(t, err)
	h.sup.Reconcile()
	var ids []string
	for _, e := range h.sup.Running() {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "b,c,a" {
		t.Fatal(ids)
	}
}
