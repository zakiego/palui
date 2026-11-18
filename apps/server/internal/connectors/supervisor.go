package connectors

// The connector supervisor (PLAN 4.4), from connectors/supervisor.ts. Desired
// state: the `connectors` rows (enabled, config_hash). Running state: the
// handles held here. It reconciles on a poke and every 30 s. Each connector is
// isolated: a panic or error in its start, stop or callbacks never takes the
// process down.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

const (
	ReconcileIntervalMs = 30_000
	ErrorAlertAfterMs   = 5 * 60_000
	StopTimeoutMs       = 10_000
	StartTimeoutMs      = 60_000
	// A handle that ran this long before reporting an error starts the ladder over.
	StableRunMs = 10 * 60_000
	TestTimeout = 25 * time.Second
)

var RestartBackoffMs = []int64{5_000, 30_000, 120_000, 600_000}

// RunningEntry is a running connector with its row.
type RunningEntry struct {
	ID     string
	Row    db.Connector
	Handle *RunningConnector
}

type shownStatus struct {
	status string
	detail *string
}

type slot struct {
	id         string
	handle     *RunningConnector
	configHash string
	ctx        context.Context
	cancel     context.CancelFunc
	starting   bool
	failures   int
	nextRetry  int64
	// A disabled connector still used by a running turn waits until it's free.
	stopWhenIdle bool
	// The connector reported its own status during start.
	reported bool
	// Errors reported by a running handle in a row, for the restart ladder.
	crashes   int
	startedAt int64
	// The status this slot last wrote.
	shown *shownStatus
}

type SupervisorOptions struct {
	DB        *db.DB
	Registry  map[string]*Runtime
	Services  *Services
	Log       *logx.Logger
	MasterKey string
	DataDir   string
	Clock     func() int64
	// Whether a running turn uses this connector's tools.
	InUse func(connectorID string) bool
	// Cleanup after a connector is removed, running or not.
	OnRemoved func(row db.Connector, unlinked bool) error
	// Restart backoff (tests); default RestartBackoffMs.
	BackoffMs []int64
	// Called (in its own goroutine) when a connector turns on after being
	// anything else: the engine retries turns that failed meanwhile.
	OnOnline func(connectorID string)
}

// RemovedInfo is passed to OnRemoved.
type RemovedInfo struct{ Unlinked bool }

type Supervisor struct {
	opts    SupervisorOptions
	clock   func() int64
	backoff []int64

	mu      sync.Mutex
	slots   map[string]*slot
	retries map[string]*time.Timer
	stopped bool

	// passMu serializes reconcile passes (the TS promise chain).
	passMu  sync.Mutex
	alertMu sync.Mutex
	ticker  *time.Ticker
	done    chan struct{}
	bg      sync.WaitGroup
}

func NewSupervisor(o SupervisorOptions) *Supervisor {
	s := &Supervisor{opts: o, clock: o.Clock, backoff: o.BackoffMs, slots: map[string]*slot{}, retries: map[string]*time.Timer{}}
	if s.clock == nil {
		s.clock = func() int64 { return time.Now().UnixMilli() }
	}
	if len(s.backoff) == 0 {
		s.backoff = RestartBackoffMs
	}
	if s.opts.Log == nil {
		s.opts.Log = logx.Silent
	}
	if s.opts.Services == nil {
		s.opts.Services = &Services{}
	}
	return s
}

func (s *Supervisor) runtimeFor(row db.Connector) *Runtime { return s.opts.Registry[row.Type] }

func (s *Supervisor) waitFor(n int) int64 {
	i := min(max(n, 1)-1, len(s.backoff)-1)
	return s.backoff[i]
}

func (s *Supervisor) setStatus(id, status string, detail *string) {
	if err := s.writeStatus(id, status, detail); err != nil {
		s.opts.Log.Error("setStatus failed", logx.Fields{"connector_id": id, "error": err})
	}
}

// writeStatus is SetStatus plus OnOnline when the connector just turned on.
func (s *Supervisor) writeStatus(id, status string, detail *string) error {
	was := ""
	if status == "on" && s.opts.OnOnline != nil {
		if row, ok, err := Get(s.opts.DB, id); err == nil && ok {
			was = row.Status
		}
	}
	if err := SetStatus(s.opts.DB, id, status, detail, s.clock()); err != nil {
		return err
	}
	if status == "on" && was != "" && was != "on" && s.opts.OnOnline != nil {
		go s.opts.OnOnline(id)
	}
	return nil
}

// armRetry reconciles at `at` (mu held).
func (s *Supervisor) armRetry(id string, at int64) {
	if t := s.retries[id]; t != nil {
		t.Stop()
	}
	delay := time.Duration(max(0, at-s.clock())) * time.Millisecond
	s.retries[id] = time.AfterFunc(delay, func() {
		s.mu.Lock()
		delete(s.retries, id)
		stopped := s.stopped
		s.mu.Unlock()
		if !stopped {
			go s.Reconcile()
		}
	})
}

func (s *Supervisor) clearRetry(id string) {
	if t := s.retries[id]; t != nil {
		t.Stop()
		delete(s.retries, id)
	}
}

func (s *Supervisor) restartsOnError(row db.Connector) bool {
	if rt := s.runtimeFor(row); rt != nil && rt.Manifest.RestartOnError != nil {
		return *rt.Manifest.RestartOnError
	}
	return row.Kind == "channel"
}

// stopHandle stops a handle, waiting at most StopTimeoutMs and recovering panics.
func (s *Supervisor) stopHandle(id string, h *RunningConnector) {
	if h == nil || h.Stop == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), StopTimeoutMs*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("stop panicked: %v", r)
			}
		}()
		done <- h.Stop(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			s.opts.Log.Warn("connector stop threw", logx.Fields{"connector_id": id, "error": err})
		}
	case <-ctx.Done():
	}
}

// restartAfterError: a running handle reported error (PLAN 4.3). Stop it, keep
// the slot (and its counters) without a handle, and arm the retry.
func (s *Supervisor) restartAfterError(sl *slot, detail *string) {
	s.mu.Lock()
	if s.slots[sl.id] != sl || sl.handle == nil {
		s.mu.Unlock()
		return
	}
	h := sl.handle
	sl.handle = nil
	sl.cancel()
	if s.clock()-sl.startedAt >= StableRunMs {
		sl.crashes = 0
	}
	sl.crashes++
	wait := s.waitFor(sl.crashes)
	sl.nextRetry = s.clock() + wait
	s.armRetry(sl.id, sl.nextRetry)
	s.mu.Unlock()
	d := ""
	if detail != nil {
		d = *detail
	}
	s.opts.Log.Warn("connector reported an error while running; restarting it", logx.Fields{"connector_id": sl.id, "detail": d, "retry_in_ms": wait})
	s.stopHandle(sl.id, h)
}

func (s *Supervisor) buildContext(row db.Connector, ctx context.Context, sl *slot) Context {
	secrets, err := Secrets(row, s.opts.MasterKey)
	if err != nil {
		s.opts.Log.Error("connector secrets unreadable", logx.Fields{"connector_id": row.ID, "error": err})
		secrets = jsonx.NewMap[string]()
	}
	clog := s.opts.Log.Child(logx.Fields{"connector_id": row.ID})
	config := row.Config
	if config == nil {
		config = jsonx.NewMap[any]()
	}
	id := row.ID
	return Context{
		ID: id, Type: row.Type, Config: config, Secrets: secrets, Trust: row.Trust,
		DB: s.opts.DB, Log: clog, DataDir: s.opts.DataDir, Ctx: ctx, Services: s.opts.Services,
		SetStatus: func(status string, detail *string) {
			restart := false
			if sl != nil {
				s.mu.Lock()
				if sl.ctx.Err() != nil {
					s.mu.Unlock()
					return
				}
				sl.reported = true
				// Coming back after a crash: keep showing the error until it's really
				// on, so errorSince keeps counting toward the 5 minute alert.
				if sl.crashes > 0 && status == "connecting" {
					s.mu.Unlock()
					return
				}
				sl.shown = &shownStatus{status, detail}
				restart = status == "error" && sl.handle != nil && !sl.starting && s.restartsOnError(row)
				s.mu.Unlock()
			}
			if err := s.writeStatus(id, status, detail); err != nil {
				clog.Error("setStatus failed", logx.Fields{"error": err})
			}
			if restart {
				go s.restartAfterError(sl, detail)
			}
		},
		Event: func(kind, message, level string, data *jsonx.Object) {
			if level == "" {
				level = "info"
			}
			var d any
			if data != nil {
				d = data
			}
			if _, err := events.Log(s.opts.DB, events.Input{ConnectorID: &id, Kind: kind, Message: message, Level: level, Data: d}, s.clock()); err != nil {
				clog.Error("event failed", logx.Fields{"error": err})
			}
		},
		PatchConfig: func(patch *jsonx.Object) {
			if err := PatchConfig(s.opts.DB, id, patch, s.clock()); err != nil {
				clog.Error("patchConfig failed", logx.Fields{"error": err})
			}
		},
		PatchSecrets: func(patch *jsonx.Map[string]) {
			if err := PatchSecrets(s.opts.DB, id, patch, s.opts.MasterKey, s.clock()); err != nil {
				clog.Error("patchSecrets failed", logx.Fields{"error": err})
			}
		},
	}
}

type startResult struct {
	h   *RunningConnector
	err error
}

func safeStart(rt *Runtime, c Context) (h *RunningConnector, err error) {
	defer func() {
		if r := recover(); r != nil {
			h, err = nil, fmt.Errorf("start panicked: %v", r)
		}
	}()
	h, err = rt.Start(c)
	if err == nil && h == nil {
		err = errors.New("start returned no handle")
	}
	return h, err
}

// prepareSlot is the synchronous half of startSlot: the new slot is in the map
// before anything else can look (mu held by the caller).
func (s *Supervisor) prepareSlot(row db.Connector) *slot {
	ctx, cancel := context.WithCancel(context.Background())
	prev := s.slots[row.ID]
	sl := &slot{id: row.ID, configHash: row.ConfigHash, ctx: ctx, cancel: cancel, starting: true, startedAt: s.clock()}
	if prev != nil {
		sl.failures, sl.crashes = prev.failures, prev.crashes
	}
	s.slots[row.ID] = sl
	return sl
}

func (s *Supervisor) startSlot(row db.Connector, rt *Runtime, sl *slot) {
	s.mu.Lock()
	fresh := sl.failures == 0 && sl.crashes == 0
	ctx := sl.ctx
	s.mu.Unlock()
	// While retrying after failures the row stays in `error`, so errorSince keeps counting.
	if fresh {
		s.setStatus(row.ID, "connecting", nil)
	}
	results := make(chan startResult, 1)
	go func() {
		h, err := safeStart(rt, s.buildContext(row, ctx, sl))
		results <- startResult{h, err}
	}()
	var res startResult
	timer := time.NewTimer(StartTimeoutMs * time.Millisecond)
	select {
	case res = <-results:
		timer.Stop()
	case <-timer.C:
		res = startResult{err: fmt.Errorf("start took longer than %d s", StartTimeoutMs/1000)}
		// A start that finishes after the timeout is stopped right away.
		go func() {
			if late := <-results; late.h != nil {
				s.stopHandle(row.ID, late.h)
			}
		}()
	}
	if res.err == nil {
		s.mu.Lock()
		if sl.ctx.Err() != nil || s.slots[row.ID] != sl {
			s.mu.Unlock()
			// Paused, removed or timed out while starting: don't keep the handle.
			s.stopHandle(row.ID, res.h)
			return
		}
		sl.handle = res.h
		sl.starting = false
		sl.failures = 0
		sl.startedAt = s.clock()
		reported := sl.reported
		if !reported {
			sl.shown = &shownStatus{status: "on"}
		}
		s.mu.Unlock()
		// Keep a status the connector reported itself during start.
		if !reported {
			s.setStatus(row.ID, "on", nil)
		}
		s.opts.Log.Info("connector started", logx.Fields{"connector_id": row.ID})
		return
	}
	s.mu.Lock()
	sl.starting = false
	sl.handle = nil
	if s.slots[row.ID] != sl {
		s.mu.Unlock()
		return
	}
	sl.cancel()
	sl.failures++
	wait := s.waitFor(sl.failures + sl.crashes)
	sl.nextRetry = s.clock() + wait
	s.armRetry(row.ID, sl.nextRetry)
	s.mu.Unlock()
	s.opts.Log.Error("connector start failed", logx.Fields{"connector_id": row.ID, "error": res.err, "retry_in_ms": wait})
	msg := strx.Slice16(res.err.Error(), 300)
	s.setStatus(row.ID, "error", &msg)
}

func (s *Supervisor) stopSlot(sl *slot, reason string) {
	s.mu.Lock()
	s.clearRetry(sl.id)
	sl.cancel()
	h := sl.handle
	sl.handle = nil
	if s.slots[sl.id] == sl {
		delete(s.slots, sl.id)
	}
	s.mu.Unlock()
	if h == nil {
		return
	}
	s.stopHandle(sl.id, h)
	s.opts.Log.Info("connector stopped", logx.Fields{"connector_id": sl.id, "reason": reason})
}

func (s *Supervisor) inUse(id string) bool {
	return s.opts.InUse != nil && s.opts.InUse(id)
}

// reconcileRow decides what one connector needs. Starts go to `starts` instead
// of being awaited, so a slow start never holds up a later poke.
func (s *Supervisor) reconcileRow(row db.Connector, starts *sync.WaitGroup) {
	defer func() {
		if r := recover(); r != nil {
			s.opts.Log.Error("reconcile failed for connector", logx.Fields{"connector_id": row.ID, "error": fmt.Sprint(r)})
		}
	}()
	s.mu.Lock()
	sl := s.slots[row.ID]
	if !row.Enabled {
		if sl != nil && (sl.handle != nil || sl.starting) {
			if s.inUse(row.ID) {
				sl.stopWhenIdle = true
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			s.stopSlot(sl, "disabled")
		} else {
			if sl != nil {
				delete(s.slots, row.ID)
			}
			s.mu.Unlock()
		}
		if row.Status != "paused" && row.Status != "not_set_up" {
			s.setStatus(row.ID, "paused", nil)
		}
		return
	}
	if sl != nil && sl.starting {
		s.mu.Unlock()
		return
	}
	if sl != nil && sl.stopWhenIdle {
		sl.stopWhenIdle = false
	}
	if sl != nil && sl.handle != nil {
		if sl.configHash != row.ConfigHash {
			s.mu.Unlock()
			s.stopSlot(sl, "config changed")
			s.goStart(row, starts)
			return
		}
		// Connect on a running connector with the same config: put back what it last reported.
		shown := sl.shown
		s.mu.Unlock()
		if row.Status == "connecting" && shown != nil {
			s.setStatus(row.ID, shown.status, shown.detail)
		}
		return
	}
	if sl != nil && sl.nextRetry > s.clock() {
		// Waiting out the backoff. A Connect click or new config retries now.
		if row.Status != "connecting" && sl.configHash == row.ConfigHash {
			s.mu.Unlock()
			return
		}
		s.clearRetry(row.ID)
	}
	s.mu.Unlock()
	s.goStart(row, starts)
}

func (s *Supervisor) goStart(row db.Connector, starts *sync.WaitGroup) {
	rt := s.runtimeFor(row)
	if rt == nil {
		msg := fmt.Sprintf("No runtime for connector type %q in this build", row.Type)
		s.setStatus(row.ID, "error", &msg)
		return
	}
	s.mu.Lock()
	sl := s.prepareSlot(row)
	s.mu.Unlock()
	starts.Add(1)
	go func() {
		defer starts.Done()
		s.startSlot(row, rt, sl)
	}()
}

func (s *Supervisor) reconcileOnce(starts *sync.WaitGroup) {
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return
	}
	rows, err := List(s.opts.DB)
	if err != nil {
		s.opts.Log.Error("reconcile could not list connectors", logx.Fields{"error": err})
		return
	}
	byID := map[string]bool{}
	for _, r := range rows {
		byID[r.ID] = true
	}
	// Rows that vanished (removed from the dashboard): stop.
	s.mu.Lock()
	var gone []*slot
	for id, sl := range s.slots {
		if !byID[id] {
			gone = append(gone, sl)
		}
	}
	s.mu.Unlock()
	for _, sl := range gone {
		s.stopSlot(sl, "removed")
	}
	var wg sync.WaitGroup
	for _, row := range rows {
		wg.Add(1)
		go func(row db.Connector) {
			defer wg.Done()
			s.reconcileRow(row, starts)
		}(row)
	}
	wg.Wait()
	s.alertErrors()
}

// Reconcile returns once this pass's decisions are applied and its starts settled.
func (s *Supervisor) Reconcile() {
	var starts sync.WaitGroup
	s.passMu.Lock()
	s.reconcileOnce(&starts)
	s.passMu.Unlock()
	starts.Wait()
	s.alertErrors()
}

// Poke reconciles in the background.
func (s *Supervisor) Poke() { go s.Reconcile() }

// alertErrors: after 5 minutes in `error`, tell the owner once on another
// running channel (PLAN 4.3).
func (s *Supervisor) alertErrors() {
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	now := s.clock()
	rows, err := listDue(s.opts.DB, now)
	if err != nil {
		s.opts.Log.Error("alert check failed", logx.Fields{"error": err})
		return
	}
	for _, row := range rows {
		target, ok := s.otherChannel(row.ID, now)
		detail := "tanpa detail"
		if row.StatusDetail != nil {
			detail = *row.StatusDetail
		}
		text := fmt.Sprintf("Konektor %s error lebih dari 5 menit: %s. Cek dashboard.", row.Label, detail)
		if ok {
			exp := now + 24*3600_000
			if _, err := outbox.Enqueue(s.opts.DB, outbox.EnqueueInput{ChatID: target.chatID, Channel: target.channel, Payload: db.OutboxPayload{Text: text}, Source: "system", ExpiresAt: &exp}, now); err != nil {
				s.opts.Log.Error("alert enqueue failed", logx.Fields{"error": err})
			}
			outbox.Notify()
		}
		if _, err := s.opts.DB.Exec("UPDATE connectors SET alerted_at = ? WHERE id = ?", now, row.ID); err != nil {
			s.opts.Log.Error("alert mark failed", logx.Fields{"error": err})
		}
		msg := "In error for 5 minutes and no other channel is reachable to alert the owner"
		if ok {
			msg = "Owner alerted on " + target.channel
		}
		id := row.ID
		_, _ = events.Log(s.opts.DB, events.Input{ConnectorID: &id, Kind: "alert", Level: "warn", Message: msg}, now)
	}
}

func listDue(q db.Q, now int64) ([]db.Connector, error) {
	rows, err := q.Query("SELECT "+db.ConnectorCols+" FROM connectors WHERE status = 'error' AND error_since <= ? AND alerted_at IS NULL", now-ErrorAlertAfterMs)
	if err != nil {
		return nil, err
	}
	return db.All(rows, err, db.ScanConnector)
}

type alertTarget struct{ channel, chatID string }

func (s *Supervisor) otherChannel(exceptID string, now int64) (alertTarget, bool) {
	for _, e := range s.Running() {
		ch := e.Handle.Channel
		if ch == nil || e.ID == exceptID || ch.Channel() == db.ChannelWeb || e.Row.Type != ch.Channel() || !safeReachable(ch) {
			continue
		}
		chat, ok, err := OwnerChat(s.opts.DB, ch.Channel(), now)
		if err == nil && ok {
			return alertTarget{ch.Channel(), chat.ID}, true
		}
	}
	return alertTarget{}, false
}

// Running is the handles whose row is still enabled right now (pausing applies
// from the next turn even before the reconcile stops the handle), sorted by
// sort order, creation and id: this order becomes the order of the model's tool
// list, and a tool list that reorders itself can't be read from the prompt cache.
func (s *Supervisor) Running() []RunningEntry {
	rows, err := List(s.opts.DB)
	if err != nil {
		return nil
	}
	byID := map[string]db.Connector{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []RunningEntry
	s.mu.Lock()
	for id, sl := range s.slots {
		row, ok := byID[id]
		if sl.handle == nil || !ok || !row.Enabled || sl.stopWhenIdle {
			continue
		}
		out = append(out, RunningEntry{ID: id, Row: row, Handle: sl.handle})
	}
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Row, out[j].Row
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return strx.Compare16(out[i].ID, out[j].ID) < 0
	})
	return out
}

// Get is one running entry.
func (s *Supervisor) Get(id string) (RunningEntry, bool) {
	for _, e := range s.Running() {
		if e.ID == id {
			return e, true
		}
	}
	return RunningEntry{}, false
}

// ConnectorModel implements models.ModelHost.
func (s *Supervisor) ConnectorModel(connectorID, modelID, role string) (models.LanguageModel, bool, error) {
	e, ok := s.Get(connectorID)
	if !ok || e.Handle.Model == nil {
		return nil, false, nil
	}
	lm, err := e.Handle.Model(modelID, role)
	return lm, true, err
}

// ChannelFor is the channel's main connector. Agent bots (another type with
// the same channel) are only reached by id.
func (s *Supervisor) ChannelFor(ch string) ChannelHandle {
	for _, e := range s.Running() {
		if c := e.Handle.Channel; c != nil && c.Channel() == ch && e.Row.Type == ch {
			return c
		}
	}
	return nil
}

func safeLive(h *RunningConnector) (live *jsonx.Object) {
	defer func() {
		if recover() != nil {
			live = jsonx.NewMap[any]()
		}
	}()
	if h == nil || h.Live == nil {
		return jsonx.NewMap[any]()
	}
	if v := h.Live(); v != nil {
		return v
	}
	return jsonx.NewMap[any]()
}

// Status is every row with whether it runs and its live state.
func (s *Supervisor) Status() []LiveStatus {
	rows, err := List(s.opts.DB)
	if err != nil {
		return nil
	}
	out := []LiveStatus{}
	for _, row := range rows {
		s.mu.Lock()
		sl := s.slots[row.ID]
		var h *RunningConnector
		if sl != nil {
			h = sl.handle
		}
		s.mu.Unlock()
		out = append(out, LiveStatus{ID: row.ID, Running: h != nil, Status: row.Status, Detail: row.StatusDetail, Live: safeLive(h)})
	}
	return out
}

// Test runs the runtime's test with a 25 s limit.
func (s *Supervisor) Test(id string) TestResult {
	row, ok, err := Get(s.opts.DB, id)
	if err != nil || !ok {
		return TestResult{OK: false, Detail: "No connector " + id}
	}
	rt := s.runtimeFor(row)
	if rt == nil {
		return TestResult{OK: false, Detail: fmt.Sprintf("No runtime for connector type %q", row.Type)}
	}
	ctx, cancel := context.WithTimeoutCause(context.Background(), TestTimeout, errors.New("test timed out"))
	defer cancel()
	res, err := func() (r TestResult, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("%v", p)
			}
		}()
		if rt.Test == nil {
			return TestResult{}, errors.New("this connector has no test")
		}
		return rt.Test(s.buildContext(row, ctx, nil))
	}()
	if err != nil {
		return TestResult{OK: false, Detail: err.Error()}
	}
	return res
}

// StatusError is an error with an HTTP status for the dashboard.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return e.Msg }

// Action routes a wizard action to the running handle.
func (s *Supervisor) Action(ctx context.Context, id, name string, input *jsonx.Object) (*jsonx.Object, error) {
	s.mu.Lock()
	sl := s.slots[id]
	var h *RunningConnector
	if sl != nil {
		h = sl.handle
	}
	s.mu.Unlock()
	if h == nil {
		// Wizard lookups on a draft come from Test(), which returns the same rows.
		if name == "listTools" || name == "listContainers" {
			r := s.Test(id)
			if !r.OK {
				return nil, &StatusError{400, r.Detail}
			}
			if r.Data == nil {
				return jsonx.NewMap[any](), nil
			}
			return r.Data, nil
		}
		return nil, &StatusError{409, fmt.Sprintf("Connector %s isn't running", id)}
	}
	if h.Action == nil {
		return nil, &StatusError{404, fmt.Sprintf("Connector %s has no actions", id)}
	}
	return safeAction(ctx, h, name, input)
}

func safeAction(ctx context.Context, h *RunningConnector, name string, input *jsonx.Object) (out *jsonx.Object, err error) {
	defer func() {
		if p := recover(); p != nil {
			out, err = nil, fmt.Errorf("action panicked: %v", p)
		}
	}()
	if input == nil {
		input = jsonx.NewMap[any]()
	}
	return h.Action(ctx, name, input)
}

// RemoveResult is remove()'s answer.
type RemoveResult struct {
	OK     bool    `json:"ok"`
	Detail *string `json:"detail"`
}

// Remove (PLAN 4.2): unlink, stop, clean up, delete the row, then reroute its
// outbox rows.
func (s *Supervisor) Remove(ctx context.Context, id string, reroute func(channel string)) RemoveResult {
	row, ok, err := Get(s.opts.DB, id)
	if err != nil || !ok {
		d := "No connector " + id
		return RemoveResult{OK: false, Detail: &d}
	}
	s.mu.Lock()
	sl := s.slots[id]
	var h *RunningConnector
	if sl != nil {
		h = sl.handle
	}
	s.mu.Unlock()
	var details []string
	unlinked := false
	if rt := s.runtimeFor(row); rt != nil && rt.Manifest.UnlinkOnRemove && h != nil && h.Action != nil {
		if _, err := safeAction(ctx, h, "unlink", jsonx.NewMap[any]()); err != nil {
			details = append(details, "unlink failed: "+err.Error())
			s.opts.Log.Warn("unlink failed", logx.Fields{"connector_id": id, "error": err})
		} else {
			unlinked = true
		}
	}
	ch := ""
	if h != nil && h.Channel != nil {
		ch = h.Channel.Channel()
	} else if row.Kind == "channel" {
		ch = row.Type
	}
	if sl != nil {
		s.stopSlot(sl, "removed")
	}
	if s.opts.OnRemoved != nil {
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("%v", p)
				}
			}()
			return s.opts.OnRemoved(row, unlinked)
		}()
		if err != nil {
			details = append(details, "cleanup failed: "+err.Error())
			s.opts.Log.Warn("connector cleanup after remove failed", logx.Fields{"connector_id": id, "error": err})
		}
	}
	if err := Delete(s.opts.DB, id, s.clock()); err != nil {
		d := err.Error()
		return RemoveResult{OK: false, Detail: &d}
	}
	if ch != "" && reroute != nil {
		reroute(ch)
	}
	var detail *string
	if len(details) > 0 {
		d := details[0]
		for _, x := range details[1:] {
			d += "; " + x
		}
		detail = &d
	}
	return RemoveResult{OK: true, Detail: detail}
}

// EnsureBuiltins gives builtin manifests a row on first boot, enabled.
func (s *Supervisor) EnsureBuiltins(manifests []Manifest) error {
	for _, m := range manifests {
		if !m.Builtin {
			continue
		}
		if _, ok, err := Get(s.opts.DB, m.ID); err != nil || ok {
			if err != nil {
				return err
			}
			continue
		}
		on := true
		if _, err := Upsert(s.opts.DB, UpsertInput{ID: m.ID, Manifest: m.Row(), Enabled: &on}, s.opts.MasterKey, s.clock()); err != nil {
			return err
		}
		if err := Connect(s.opts.DB, m.ID, s.clock()); err != nil {
			return err
		}
	}
	return nil
}

// Release: a turn ended; stop connectors that were disabled while in use.
func (s *Supervisor) Release() {
	s.mu.Lock()
	need := false
	for id, sl := range s.slots {
		if sl.stopWhenIdle && !s.inUse(id) {
			need = true
		}
	}
	s.mu.Unlock()
	if need {
		go s.Reconcile()
	}
}

func (s *Supervisor) IsRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.slots[id]
	return sl != nil && sl.handle != nil
}

// Start reconciles now and every 30 s.
func (s *Supervisor) Start() {
	s.mu.Lock()
	s.stopped = false
	s.ticker = time.NewTicker(ReconcileIntervalMs * time.Millisecond)
	s.done = make(chan struct{})
	t, done := s.ticker, s.done
	s.mu.Unlock()
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		for {
			select {
			case <-t.C:
				func() {
					defer func() {
						if p := recover(); p != nil {
							s.opts.Log.Error("reconcile panicked", logx.Fields{"panic": fmt.Sprint(p)})
						}
					}()
					s.Reconcile()
				}()
			case <-done:
				return
			}
		}
	}()
	s.Reconcile()
}

// Stop stops the ticker and every connector.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.ticker != nil {
		s.ticker.Stop()
		close(s.done)
		s.ticker = nil
	}
	for id, t := range s.retries {
		t.Stop()
		delete(s.retries, id)
	}
	s.mu.Unlock()
	s.bg.Wait()
	// Wait for a pass in progress.
	s.passMu.Lock()
	s.passMu.Unlock()
	s.mu.Lock()
	var all []*slot
	for _, sl := range s.slots {
		all = append(all, sl)
	}
	s.mu.Unlock()
	for _, sl := range all {
		s.stopSlot(sl, "shutdown")
	}
}
