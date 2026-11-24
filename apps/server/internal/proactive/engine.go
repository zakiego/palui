package proactive

// The proactive engine (docs/rfc/proactive-prompt.md 4): a scheduler
// extension. Each sweep
//
//	timed      claims scheduled items whose wake_at passed and sends their
//	           stored text, no model (RFC 6); a snoozed item goes back to
//	           pending and pokes its agent's tick
//	ticks      claims agents whose next_tick_at passed (lease, then the next
//	           tick time moves on; inside quiet hours it moves to their end)
//	           and runs a tick in the background: resolve the mentions, skip
//	           when a replay shows nothing changed, else run the prompt,
//	           reconcile its items with the ledger, deliver
//	summaries  claims a morning or evening summary once per day by inserting
//	           its nudge row (the key is unique)
//
// Engine implements reminders.Extension, Recoverer and Idler.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/settings"
)

const (
	// TickLeaseMs: a tick that runs longer is presumed dead.
	TickLeaseMs = 5 * 60_000
	// MergeWindowMs: timed items this close together go out as one message.
	MergeWindowMs = 5 * 60_000
	// TimedLateMs: a timed item claimed later than this (downtime) expires.
	TimedLateMs = 10 * 60_000
	// SummaryLateMs: a summary more than this late (downtime) is skipped.
	SummaryLateMs = 2 * 3600_000
	// OutboxTTLMs: a proactive message older than this isn't worth sending.
	OutboxTTLMs = 6 * 3600_000
	// FullRunEveryMs: replay may skip the model, but not for longer than this.
	FullRunEveryMs = 2 * 3600_000
	// FailPauseAfter failed runs in a row pause proactive.
	FailPauseAfter = 3
	// MaxUrgentPerDay caps the items a day that may pass quiet hours.
	MaxUrgentPerDay = 3
)

// Options wire the engine.
type Options struct {
	DB  db.Q
	Log *logx.Logger
	// Unix ms; default time.Now.
	Clock func() int64
	// Tools lists and opens the agent's tools; nil uses DefaultToolHost.
	Tools ToolHost
	// Models resolves the agent's model chain (the supervisor).
	Models models.ModelHost
	// Serial runs ticks and summaries at the end of Sweep, in order (tests).
	Serial bool
}

// Engine is the proactive scheduler extension.
type Engine struct {
	o         Options
	log       *logx.Logger
	mu        sync.Mutex
	busy      map[string]bool
	wg        sync.WaitGroup
	queue     []func()
	lastPrune int64
	poke      func()
}

// New makes an engine.
func New(o Options) *Engine {
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	log := o.Log
	if log == nil {
		log = logx.Silent
	}
	return &Engine{o: o, log: log, busy: map[string]bool{}}
}

// OnPoke sets what CheckNow calls to wake the scheduler.
func (e *Engine) OnPoke(fn func()) { e.poke = fn }

func (e *Engine) tools() ToolHost {
	if e.o.Tools != nil {
		return e.o.Tools
	}
	return DefaultToolHost()
}

func (e *Engine) spawn(key string, fn func()) bool {
	e.mu.Lock()
	if e.busy[key] {
		e.mu.Unlock()
		return false
	}
	e.busy[key] = true
	e.mu.Unlock()
	e.wg.Add(1)
	run := func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.busy, key)
			e.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				e.log.Error("proactive task crashed", logx.Fields{"task": key, "err": fmt.Sprint(r)})
			}
		}()
		fn()
	}
	if e.o.Serial {
		e.mu.Lock()
		e.queue = append(e.queue, run)
		e.mu.Unlock()
		return true
	}
	go run()
	return true
}

func (e *Engine) drain() {
	for {
		e.mu.Lock()
		if len(e.queue) == 0 {
			e.mu.Unlock()
			return
		}
		fn := e.queue[0]
		e.queue = e.queue[1:]
		e.mu.Unlock()
		fn()
	}
}

// Idle waits for the background work a sweep started.
func (e *Engine) Idle() {
	e.drain()
	e.wg.Wait()
}

// Recover releases what a crash left claimed. A timed item's send and its
// status move are one transaction, so a firing row was never sent.
func (e *Engine) Recover(now int64) error {
	if _, err := e.o.DB.Exec("UPDATE agent_proactive SET tick_lease_until = NULL WHERE tick_lease_until IS NOT NULL"); err != nil {
		return err
	}
	if _, err := e.o.DB.Exec("UPDATE nudges SET status = ?, updated_at = ? WHERE status = ? AND kind NOT IN (?, ?)", StatusScheduled, now, StatusFiring, KindSumMorning, KindSumEvening); err != nil {
		return err
	}
	// A summary claimed but never sent: let the next sweep try again.
	_, err := e.o.DB.Exec("DELETE FROM nudges WHERE status = ? AND kind IN (?, ?)", StatusFiring, KindSumMorning, KindSumEvening)
	return err
}

// CheckNow ticks an agent on the next sweep ("cek sekarang").
func (e *Engine) CheckNow(agentID string, now int64) error {
	res, err := e.o.DB.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ? AND enabled = 1 AND paused_reason IS NULL", now, agentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return bad("Proactive is off or paused for this agent")
	}
	if e.poke != nil {
		e.poke()
	}
	return nil
}

func (e *Engine) tz() string {
	tz, err := settings.Timezone(e.o.DB)
	if err != nil || tz == "" {
		return "UTC"
	}
	return tz
}

// NextDue is the earliest of the next tick, the next timed item and the next
// summary of any enabled agent.
func (e *Engine) NextDue() (int64, bool, error) {
	q := e.o.DB
	var best int64
	found := false
	take := func(v int64) {
		if !found || v < best {
			best, found = v, true
		}
	}
	var n sql.NullInt64
	if err := q.QueryRow("SELECT min(next_tick_at) FROM agent_proactive WHERE enabled = 1 AND paused_reason IS NULL").Scan(&n); err != nil {
		return 0, false, err
	}
	if n.Valid {
		take(n.Int64)
	}
	if err := q.QueryRow("SELECT min(n.wake_at) FROM nudges n JOIN agent_proactive p ON p.agent_id = n.agent_id WHERE p.enabled = 1 AND p.paused_reason IS NULL AND n.status IN (?, ?) AND n.wake_at IS NOT NULL", StatusScheduled, StatusSnoozed).Scan(&n); err != nil {
		return 0, false, err
	}
	if n.Valid {
		take(n.Int64)
	}
	rows, err := Enabled(q)
	if err != nil {
		return 0, false, err
	}
	if len(rows) > 0 {
		tz := e.tz()
		now := e.o.Clock()
		for _, r := range rows {
			if r.PausedReason != nil {
				continue
			}
			cfg, err := ParseConfig(r.Config)
			if err != nil {
				continue
			}
			for _, s := range []Summary{cfg.Morning, cfg.Evening} {
				if s.On {
					take(nextAt(s.At, tz, now))
				}
			}
		}
	}
	return best, found, nil
}

// atOn is "HH:MM" on the local day of ms.
func atOn(hhmm, tz string, ms int64) int64 {
	loc := loadTZ(tz)
	t := time.UnixMilli(ms).In(loc)
	m := minutesOf(hhmm)
	return time.Date(t.Year(), t.Month(), t.Day(), m/60, m%60, 0, 0, loc).UnixMilli()
}

// nextAt is the next "HH:MM" at or after now (today's if it hasn't passed by
// more than the late window, else tomorrow's).
func nextAt(hhmm, tz string, now int64) int64 {
	today := atOn(hhmm, tz, now)
	if today >= now-SummaryLateMs {
		return max(today, now)
	}
	return tomorrowAt(hhmm, tz, now)
}

func tomorrowAt(hhmm, tz string, now int64) int64 {
	loc := loadTZ(tz)
	t := time.UnixMilli(now).In(loc).AddDate(0, 0, 1)
	m := minutesOf(hhmm)
	return time.Date(t.Year(), t.Month(), t.Day(), m/60, m%60, 0, 0, loc).UnixMilli()
}

// quietEnd is when the quiet hours around now end.
func quietEnd(q QuietHours, tz string, now int64) int64 {
	end := atOn(q.End, tz, now)
	if end <= now {
		end = tomorrowAt(q.End, tz, now)
	}
	return end
}

func localDate(ms int64, tz string) string {
	return time.UnixMilli(ms).In(loadTZ(tz)).Format("2006-01-02")
}

func localMinute(ms int64, tz string) int {
	t := time.UnixMilli(ms).In(loadTZ(tz))
	return t.Hour()*60 + t.Minute()
}

// Sweep is the scheduler's pass.
func (e *Engine) Sweep(now int64) (int, error) {
	q := e.o.DB
	if now-e.lastPrune > 24*3600_000 {
		e.lastPrune = now
		if err := Prune(q, now); err != nil {
			e.log.Error("proactive prune failed", logx.Fields{"err": err})
		}
	}
	n := 0
	rows, err := Enabled(q)
	if err != nil {
		return 0, err
	}
	tz := e.tz()
	for _, r := range rows {
		if r.PausedReason != nil {
			continue
		}
		cfg, err := ParseConfig(r.Config)
		if err != nil {
			e.log.Error("proactive config unreadable", logx.Fields{"agent_id": r.AgentID, "err": err})
			continue
		}
		// Timed items first: they're the ones with a minute that matters.
		sent, err := e.fireTimed(r, cfg, tz, now)
		if err != nil {
			e.log.Error("proactive timed items failed", logx.Fields{"agent_id": r.AgentID, "err": err})
		}
		n += sent
		// Then summaries: a summary due now is this sweep's run.
		summarized := false
		for _, which := range []string{KindSumMorning, KindSumEvening} {
			s := cfg.Morning
			if which == KindSumEvening {
				s = cfg.Evening
			}
			if !s.On {
				continue
			}
			at := atOn(s.At, tz, now)
			if now < at || now-at > SummaryLateMs {
				continue
			}
			date := localDate(now, tz)
			mode := strings.TrimPrefix(which, "sum_")
			key := "sum:" + mode + ":" + date
			if _, found, err := nudgeByKey(q, r.AgentID, key); err != nil || found {
				continue
			}
			if _, err := insertNudge(q, r.AgentID, key, which, "summary", Snapshot{Date: date}, StatusFiring, nil, now); err != nil {
				continue // another sweep claimed it
			}
			// One run per agent at a time: a summary shares the tick's slot, so
			// the two never deliver the same pending items twice.
			agentID := r.AgentID
			if e.spawn("tick:"+agentID, func() { e.tick(agentID, mode, key) }) {
				n++
				summarized = true
			} else if _, err := q.Exec("DELETE FROM nudges WHERE agent_id = ? AND key = ? AND status = ?", agentID, key, StatusFiring); err != nil {
				// A run is still going: let the next sweep claim the summary again.
				return n, err
			}
		}
		if summarized && r.NextTickAt != nil && *r.NextTickAt <= now {
			// The summary is this run: the next plain one comes a period later.
			if _, err := q.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ?", now+int64(cfg.CheckEveryMin)*60_000, r.AgentID); err != nil {
				return n, err
			}
		} else if r.NextTickAt != nil && *r.NextTickAt <= now && (r.TickLeaseUntil == nil || *r.TickLeaseUntil <= now) {
			if cfg.QuietHours.InQuiet(localMinute(now, tz)) {
				// No runs in quiet hours (RFC 9): look again when they end.
				if _, err := q.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ?", quietEnd(cfg.QuietHours, tz, now), r.AgentID); err != nil {
					return n, err
				}
			} else {
				res, err := q.Exec("UPDATE agent_proactive SET tick_lease_until = ?, next_tick_at = ? WHERE agent_id = ? AND enabled = 1 AND next_tick_at <= ? AND (tick_lease_until IS NULL OR tick_lease_until <= ?)",
					now+TickLeaseMs, now+int64(cfg.CheckEveryMin)*60_000, r.AgentID, now, now)
				if err != nil {
					return n, err
				}
				if c, _ := res.RowsAffected(); c == 1 {
					agentID := r.AgentID
					if e.spawn("tick:"+agentID, func() { e.tick(agentID, SummaryNone, "") }) {
						n++
					} else if _, err := q.Exec("UPDATE agent_proactive SET tick_lease_until = NULL WHERE agent_id = ?", agentID); err != nil {
						// A summary run holds the slot; this tick is skipped, not stuck.
						return n, err
					}
				}
			}
		}
	}
	if e.o.Serial {
		e.drain()
	}
	return n, nil
}

// fireTimed sends this agent's due timed items (RFC 6) and turns due snoozed
// items back to pending.
func (e *Engine) fireTimed(r db.AgentProactive, cfg Config, tz string, now int64) (int, error) {
	q := e.o.DB
	res, err := q.Exec("UPDATE nudges SET status = ?, wake_at = NULL, updated_at = ? WHERE agent_id = ? AND status = ? AND wake_at <= ?", StatusPending, now, r.AgentID, StatusSnoozed, now)
	if err != nil {
		return 0, err
	}
	if c, _ := res.RowsAffected(); c > 0 {
		// The next run sees it open again and may close it; else it's sent.
		if _, err := q.Exec("UPDATE agent_proactive SET next_tick_at = min(coalesce(next_tick_at, ?), ?) WHERE agent_id = ?", now, now, r.AgentID); err != nil {
			return 0, err
		}
	}
	var due int
	if err := q.QueryRow("SELECT count(*) FROM nudges WHERE agent_id = ? AND status = ? AND wake_at <= ?", r.AgentID, StatusScheduled, now).Scan(&due); err != nil || due == 0 {
		return 0, err
	}
	rows, err := q.WQuery("UPDATE nudges SET status = ?, updated_at = ? WHERE agent_id = ? AND status = ? AND wake_at <= ? RETURNING "+db.NudgeCols, StatusFiring, now, r.AgentID, StatusScheduled, now+MergeWindowMs)
	claimed, err := db.All(rows, err, db.ScanNudge)
	if err != nil {
		return 0, err
	}
	sort.Slice(claimed, func(i, j int) bool { return deref(claimed[i].WakeAt) < deref(claimed[j].WakeAt) })
	quiet := cfg.QuietHours.InQuiet(localMinute(now, tz))
	var send []db.Nudge
	for _, n := range claimed {
		switch {
		case deref(n.WakeAt) < now-TimedLateMs:
			err = setStatus(q, n.ID, StatusExpired, "too late to send", now)
		case quiet && !(cfg.QuietHours.UrgentPasses && snapshotOf(n).Urgent):
			// Held for the morning summary.
			_, err = q.Exec("UPDATE nudges SET status = ?, wake_at = NULL, reason = 'quiet hours', updated_at = ? WHERE id = ?", StatusPending, now, n.ID)
		default:
			send = append(send, n)
		}
		if err != nil {
			return 0, err
		}
	}
	if len(send) == 0 {
		return 0, nil
	}
	return len(send), e.deliver(r, send, joinTexts(send), now)
}

func joinTexts(list []db.Nudge) string {
	parts := make([]string, 0, len(list))
	for _, n := range list {
		if t := snapshotOf(n).Text; t != "" {
			parts = append(parts, t)
		}
	}
	return MessageText(strings.Join(parts, "\n\n"))
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// deliver enqueues one message for these nudges and marks them sent, in one
// transaction.
func (e *Engine) deliver(r db.AgentProactive, list []db.Nudge, text string, now int64) error {
	if r.ChatID == nil || len(list) == 0 {
		return nil
	}
	q := e.o.DB
	chat, err := CheckTarget(q, *r.ChatID)
	if err != nil {
		return e.pause(r, err.Error(), now)
	}
	if strings.TrimSpace(text) == "" {
		text = "…"
	}
	var actions []db.OutboxAction
	for _, n := range list {
		if n.Kind == KindItem {
			actions = Buttons(n, Language(q))
			break
		}
	}
	expires := now + OutboxTTLMs
	err = q.Tx(func(tx db.Q) error {
		row, err := outbox.Enqueue(tx, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: text, Actions: actions, Format: "plain"}, Source: OutboxSourcePrefix + r.AgentID, ExpiresAt: &expires}, now)
		if err != nil {
			return err
		}
		for _, n := range list {
			if _, err := tx.Exec("UPDATE nudges SET status = ?, sent_at = ?, outbox_id = ?, wake_at = NULL, reason = NULL, updated_at = ? WHERE id = ?", StatusSent, now, row.ID, now, n.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		outbox.Notify()
	}
	return err
}

// pause stops an agent's proactive until the owner saves again.
func (e *Engine) pause(r db.AgentProactive, reason string, now int64) error {
	q := e.o.DB
	if _, err := q.Exec("UPDATE agent_proactive SET paused_reason = ?, tick_lease_until = NULL, updated_at = ? WHERE agent_id = ?", reason, now, r.AgentID); err != nil {
		return err
	}
	_, err := events.Log(q, events.Input{Level: "warn", Kind: "proactive.paused", Message: "Proactive paused for agent " + r.AgentID + ": " + reason}, now)
	return err
}

// TickLog is a tick's proactive_ticks.sources: what it called and why it
// did or didn't ask the model.
type TickLog struct {
	Mentions []string `json:"mentions"`
	Calls    []Call   `json:"calls"`
	// Full: the model ran. Skipped says why it didn't.
	Full    bool     `json:"full"`
	Skipped string   `json:"skipped,omitempty"`
	State   string   `json:"state,omitempty"`
	Ignored []string `json:"ignored,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

// Skip reasons in TickLog.
const (
	SkipUnchanged = "unchanged"
	SkipNoModel   = "no model or over budget"
)

// tick runs the prompt once (RFC 4). summary is SummaryNone for a normal
// tick, or the summary to write; summaryKey is its claimed nudge.
func (e *Engine) tick(agentID, summary, summaryKey string) {
	q := e.o.DB
	now := e.o.Clock()
	tickID := secret.NewID("ptk")
	var log TickLog
	var tokens int64
	found, sent := 0, 0
	var tickErr error
	var summaryNudge *db.Nudge
	if summary != SummaryNone {
		n, ok, err := nudgeByKey(q, agentID, summaryKey)
		if err != nil || !ok {
			return
		}
		summaryNudge = &n
		log.Summary = summary
	}
	defer func() {
		raw, _ := jsonx.Marshal(log)
		var msg *string
		if tickErr != nil {
			s := clip(tickErr.Error(), 600)
			msg = &s
			e.log.Warn("proactive run failed", logx.Fields{"agent_id": agentID, "err": tickErr})
		}
		end := e.o.Clock()
		_, _ = q.Exec("INSERT INTO proactive_ticks (id, agent_id, started_at, finished_at, sources, found, sent, tokens, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", tickID, agentID, now, end, string(raw), found, sent, tokens, msg)
		_, _ = q.Exec("UPDATE agent_proactive SET last_tick_at = ?, tick_lease_until = NULL WHERE agent_id = ?", now, agentID)
		if summaryNudge != nil && tickErr != nil {
			_ = setStatus(q, summaryNudge.ID, StatusDropped, clip(tickErr.Error(), 200), now)
		}
		if tickErr != nil {
			e.failed(agentID, tickErr, now)
		}
	}()
	row, cfg, ok, err := Get(q, agentID)
	if err != nil || !ok || !row.Enabled || row.PausedReason != nil {
		tickErr = err
		if summaryNudge != nil && err == nil {
			_ = setStatus(q, summaryNudge.ID, StatusDropped, "proactive is off", now)
		}
		return
	}
	agent, ok, err := agents.Get(q, agentID)
	if err != nil || !ok {
		tickErr = fmt.Errorf("agent missing: %v", err)
		return
	}
	if row.ChatID == nil {
		tickErr = e.pause(row, "No chat to send to", now)
		return
	}
	if _, err := CheckTarget(q, *row.ChatID); err != nil {
		tickErr = e.pause(row, err.Error(), now)
		return
	}
	tz := e.tz()
	ctx := context.Background()
	host := e.tools()
	if host == nil {
		tickErr = errors.New("no tool host is wired")
		return
	}
	log.Mentions = Mentions(cfg.Prompt)
	list, err := host.Tools(ctx, agentID, *row.ChatID, now)
	if err != nil {
		tickErr = err
		return
	}
	tools, err := Resolve(log.Mentions, list)
	if err != nil {
		tickErr = err
		return
	}
	in, err := e.runInput(agent, row, cfg, tools, tz, summary, now)
	if err != nil {
		tickErr = err
		return
	}
	log.State = stateHash(in)
	if summary == SummaryNone {
		if prev, ok := e.lastFull(agentID); ok && prev.log.State == log.State && now-prev.at < FullRunEveryMs && Replay(ctx, tools, prev.log.Calls) {
			log.Skipped = SkipUnchanged
			log.Calls = prev.log.Calls
			sent, tickErr = e.deliverPending(row, cfg, tz, now)
			return
		}
	}
	model, ok, err := agentModel(q, e.o.Models, agent, cfg.ModelRole, now, e.o.Clock, &tokens)
	if err != nil {
		tickErr = err
		return
	}
	var out Output
	if ok {
		res, err := Run(ctx, model, in)
		log.Calls = res.Calls
		if err != nil {
			tickErr = err
			// Items already scheduled still fire; held ones wait.
			if summary == SummaryNone {
				_, _ = e.deliverPending(row, cfg, tz, now)
			} else {
				e.sendSummary(row, *summaryNudge, "", in.Held, now)
				summaryNudge = nil
			}
			return
		}
		log.Full = true
		log.Ignored = res.Output.Skipped
		out = res.Output
		found = len(out.Items)
		if err := q.Tx(func(tx db.Q) error { return apply(tx, agentID, out, tz, now) }); err != nil {
			tickErr = err
			return
		}
	} else {
		log.Skipped = SkipNoModel
	}
	if summary != SummaryNone {
		// Whatever is pending now, held overnight or found just now, goes
		// out with the summary.
		held, err := pendingNudges(q, agentID)
		if err != nil {
			tickErr = err
			return
		}
		sent = len(held)
		e.sendSummary(row, *summaryNudge, out.Summary, held, now)
		summaryNudge = nil
		return
	}
	sent, tickErr = e.deliverPending(row, cfg, tz, now)
}

// runInput gathers what a run sees.
func (e *Engine) runInput(agent db.Agent, row db.AgentProactive, cfg Config, tools map[string]Tool, tz, summary string, now int64) (RunInput, error) {
	q := e.o.DB
	in := RunInput{Agent: agent, Config: cfg, Tools: tools, TZ: tz, Now: now, Language: Language(q), Summary: summary}
	var err error
	if in.Notes, err = Notes(q, agent.ID, now); err != nil {
		return in, err
	}
	if in.Mutes, err = Mutes(q, agent.ID); err != nil {
		return in, err
	}
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND kind = ? AND status IN (?, ?, ?, ?) AND updated_at >= ? ORDER BY updated_at DESC, id LIMIT ?", agent.ID, KindItem, StatusPending, StatusScheduled, StatusSnoozed, StatusSent, now-OpenWindowMs, MaxOpenShown)
	if in.Open, err = db.All(rows, err, db.ScanNudge); err != nil {
		return in, err
	}
	if summary != SummaryNone {
		if in.Held, err = pendingNudges(q, agent.ID); err != nil {
			return in, err
		}
	}
	return in, nil
}

func pendingNudges(q db.Q, agentID string) ([]db.Nudge, error) {
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND status = ? ORDER BY created_at, id", agentID, StatusPending)
	return db.All(rows, err, db.ScanNudge)
}

// stateHash fingerprints what a run sees besides the tools: the prompt, the
// notes, the mutes and the open items. A replay skip needs it unchanged.
func stateHash(in RunInput) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", in.Config.Prompt, in.Language)
	for _, n := range in.Notes {
		fmt.Fprintf(h, "n:%s:%s\x00", n.ID, n.Text)
	}
	for _, m := range in.Mutes {
		fmt.Fprintf(h, "m:%s\x00", m.Subject)
	}
	for _, n := range in.Open {
		fmt.Fprintf(h, "o:%s:%s:%d\x00", n.Key, n.Status, deref(n.WakeAt))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

type fullTick struct {
	at  int64
	log TickLog
}

// lastFull is the agent's last tick that ran the model without an error.
func (e *Engine) lastFull(agentID string) (fullTick, bool) {
	rows, err := e.o.DB.Query("SELECT started_at, sources FROM proactive_ticks WHERE agent_id = ? AND error IS NULL ORDER BY started_at DESC LIMIT 20", agentID)
	if err != nil {
		return fullTick{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		var raw string
		if rows.Scan(&at, &raw) != nil {
			continue
		}
		var l TickLog
		if db.RawJSON(raw).Decode(&l) != nil {
			continue
		}
		if l.Full && l.Summary == "" {
			return fullTick{at: at, log: l}, true
		}
	}
	return fullTick{}, false
}

// apply reconciles a run's answer with the ledger and the memory (RFC 6, 8).
func apply(q db.Q, agentID string, out Output, tz string, now int64) error {
	urgentToday := -1
	for _, it := range out.Items {
		m, err := muted(q, agentID, it.Subject)
		if err != nil {
			return err
		}
		if m {
			continue
		}
		if it.Urgent {
			if urgentToday < 0 {
				if err := q.QueryRow("SELECT count(*) FROM nudges WHERE agent_id = ? AND created_at >= ? AND json_extract(snapshot, '$.urgent') = 1", agentID, atOn("00:00", tz, now)).Scan(&urgentToday); err != nil {
					return err
				}
			}
			if urgentToday >= MaxUrgentPerDay {
				it.Urgent = false
			} else {
				urgentToday++
			}
		}
		snap := Snapshot{Text: it.Text, Urgent: it.Urgent}
		ex, found, err := nudgeByKey(q, agentID, it.Key)
		if err != nil {
			return err
		}
		status := StatusPending
		var wake *int64
		if it.SendAt > 0 {
			status = StatusScheduled
			w := it.SendAt
			wake = &w
		}
		if !found {
			if _, err := insertNudge(q, agentID, it.Key, KindItem, it.Subject, snap, status, wake, now); err != nil {
				return err
			}
			continue
		}
		if ex.Status != StatusPending && ex.Status != StatusScheduled {
			continue // said already, or the owner acted on it: never twice
		}
		raw, err := jsonx.Marshal(snap)
		if err != nil {
			return err
		}
		if _, err := q.Exec("UPDATE nudges SET status = ?, wake_at = ?, snapshot = ?, subject = ?, updated_at = ? WHERE id = ?", status, wake, string(raw), it.Subject, now, ex.ID); err != nil {
			return err
		}
	}
	for _, c := range out.Closed {
		ex, found, err := nudgeByKey(q, agentID, c.Key)
		if err != nil {
			return err
		}
		if !found || ex.Kind != KindItem {
			continue
		}
		why := "resolved"
		if c.Why != "" {
			why = c.Why
		}
		switch ex.Status {
		case StatusPending, StatusScheduled, StatusSnoozed:
			err = setStatus(q, ex.ID, StatusDropped, "model: "+why, now)
		case StatusSent:
			err = setStatus(q, ex.ID, StatusDone, "model: "+why, now)
		}
		if err != nil {
			return err
		}
	}
	for _, id := range out.Forget {
		if _, err := ForgetNote(q, agentID, id, true); err != nil {
			return err
		}
	}
	for _, r := range out.Remember {
		if _, err := AddNote(q, agentID, r.Text, NoteSourceModel, r.Days, now); err != nil {
			var ce *ConfigError
			if !errors.As(err, &ce) {
				return err
			}
		}
	}
	return nil
}

// deliverPending sends what's pending as one message, within quiet hours and
// the rate limit (RFC 2). It returns how many items went out.
func (e *Engine) deliverPending(r db.AgentProactive, cfg Config, tz string, now int64) (int, error) {
	q := e.o.DB
	pending, err := pendingNudges(q, r.AgentID)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	urgentOnly := func(list []db.Nudge) []db.Nudge {
		var keep []db.Nudge
		for _, n := range list {
			if snapshotOf(n).Urgent {
				keep = append(keep, n)
			}
		}
		return keep
	}
	if cfg.QuietHours.InQuiet(localMinute(now, tz)) {
		if !cfg.QuietHours.UrgentPasses {
			return 0, nil
		}
		pending = urgentOnly(pending)
	}
	var recent int
	if err := q.QueryRow("SELECT count(DISTINCT outbox_id) FROM nudges WHERE agent_id = ? AND sent_at >= ? AND outbox_id IS NOT NULL AND kind NOT IN (?, ?)", r.AgentID, now-3600_000, KindSumMorning, KindSumEvening).Scan(&recent); err != nil {
		return 0, err
	}
	if recent >= cfg.MaxPerHour {
		pending = urgentOnly(pending)
	}
	var list []db.Nudge
	for _, n := range pending {
		if n.Kind == KindItem {
			list = append(list, n)
		}
	}
	if len(list) == 0 {
		return 0, nil
	}
	return len(list), e.deliver(r, list, joinTexts(list), now)
}

// sendSummary sends a summary with the held items, the model's text or a
// plain list when there's none.
func (e *Engine) sendSummary(r db.AgentProactive, nudge db.Nudge, text string, held []db.Nudge, now int64) {
	q := e.o.DB
	if text == "" {
		text = fallbackSummary(nudge.Kind == KindSumMorning, held, Language(q))
	}
	list := append([]db.Nudge{nudge}, held...)
	if err := e.deliver(r, list, MessageText(text), now); err != nil {
		e.log.Error("proactive summary failed", logx.Fields{"agent_id": r.AgentID, "err": err})
		_ = setStatus(q, nudge.ID, StatusDropped, clip(err.Error(), 200), now)
	}
}

func fallbackSummary(morning bool, held []db.Nudge, lang string) string {
	en := lang == "en"
	if len(held) == 0 {
		switch {
		case morning && en:
			return "Morning! Nothing to tell you right now."
		case morning:
			return "Pagi! Belum ada yang perlu dikabarin."
		case en:
			return "Evening! Nothing to tell you for tomorrow."
		}
		return "Malam! Belum ada yang perlu dikabarin buat besok."
	}
	head := "Pagi! Ini yang ketahan:"
	switch {
	case morning && en:
		head = "Morning! Here's what was held:"
	case !morning && en:
		head = "Evening! Still open:"
	case !morning:
		head = "Malam! Yang masih kebuka:"
	}
	lines := []string{head}
	for _, n := range held {
		if t := Field(snapshotOf(n).Text); t != "" {
			lines = append(lines, "- "+t)
		}
	}
	return strings.Join(lines, "\n")
}

// failed handles a failed run: one owner notice a day, and a pause after
// FailPauseAfter failures in a row (RFC 3).
func (e *Engine) failed(agentID string, runErr error, now int64) {
	q := e.o.DB
	row, _, ok, err := Get(q, agentID)
	if err != nil || !ok || !row.Enabled || row.PausedReason != nil {
		return
	}
	rows, err := q.Query("SELECT error FROM proactive_ticks WHERE agent_id = ? ORDER BY started_at DESC LIMIT ?", agentID, FailPauseAfter)
	if err != nil {
		return
	}
	failedRuns := 0
	for rows.Next() {
		var msg sql.NullString
		if rows.Scan(&msg) != nil || !msg.Valid {
			break
		}
		failedRuns++
	}
	rows.Close()
	reason := clip(runErr.Error(), 300)
	en := Language(q) == "en"
	if failedRuns >= FailPauseAfter {
		_ = e.pause(row, "Proactive failed "+fmt.Sprint(FailPauseAfter)+" runs in a row: "+reason, now)
		text := "⚠️ Proactive aku matiin dulu, " + fmt.Sprint(FailPauseAfter) + "x gagal berturut-turut: " + Field(reason) + ". Benerin di tab Proactive, terus simpan lagi."
		if en {
			text = "⚠️ I paused proactive after " + fmt.Sprint(FailPauseAfter) + " failed runs in a row: " + Field(reason) + ". Fix it on the Proactive tab and save again."
		}
		e.notify(row, text, now)
		return
	}
	kind := "proactive.run_failed"
	var x int
	dayStart := atOn("00:00", e.tz(), now)
	err = q.QueryRow("SELECT 1 FROM events WHERE kind = ? AND at >= ? AND message LIKE ? LIMIT 1", kind, dayStart, "%"+agentID+"%").Scan(&x)
	if !noRows(err) {
		return // already told today, or the check failed: don't risk a repeat
	}
	_, _ = events.Log(q, events.Input{Level: "warn", Kind: kind, Message: "Proactive run failed for agent " + agentID + ": " + reason}, now)
	text := "⚠️ Proactive gagal jalan: " + Field(reason)
	if en {
		text = "⚠️ Proactive couldn't run: " + Field(reason)
	}
	e.notify(row, text, now)
}

// notify sends the owner a system message in the target chat.
func (e *Engine) notify(r db.AgentProactive, text string, now int64) {
	if r.ChatID == nil {
		return
	}
	q := e.o.DB
	chat, err := CheckTarget(q, *r.ChatID)
	if err != nil {
		return
	}
	expires := now + OutboxTTLMs
	if _, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: MessageText(text), Format: "plain"}, Source: "system", ExpiresAt: &expires}, now); err == nil {
		outbox.Notify()
	}
}

// TestResult is a test run's answer: what it called, what it would send and
// remember, and what it cost. Nothing is written.
type TestResult struct {
	Calls    []Call     `json:"calls"`
	Items    []TestItem `json:"items"`
	Closed   []string   `json:"closed"`
	Remember []string   `json:"remember"`
	Forget   []string   `json:"forget"`
	Ignored  []string   `json:"ignored"`
	Tokens   int64      `json:"tokens"`
	Error    string     `json:"error,omitempty"`
}

// TestItem is one item a test run would send.
type TestItem struct {
	Key     string `json:"key"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	SendAt  *int64 `json:"sendAt"`
	Urgent  bool   `json:"urgent"`
	// Muted, or already said (the ledger would drop it).
	Dropped string `json:"dropped,omitempty"`
}

// TestRun plays the prompt (the saved one, or prompt when given) for real
// against the tools and the model, without writing nudges, notes or messages.
// Mention problems come back as an error the tab shows as is.
func (e *Engine) TestRun(ctx context.Context, agentID string, prompt *string, now int64) (TestResult, error) {
	q := e.o.DB
	out := TestResult{Calls: []Call{}, Items: []TestItem{}, Closed: []string{}, Remember: []string{}, Forget: []string{}, Ignored: []string{}}
	row, cfg, _, err := Get(q, agentID)
	if err != nil {
		return out, err
	}
	if prompt != nil {
		cfg.Prompt = *prompt
		if err := cfg.Validate(); err != nil {
			return out, err
		}
	}
	agent, ok, err := agents.Get(q, agentID)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, bad("That agent doesn't exist")
	}
	host := e.tools()
	if host == nil {
		return out, bad("Proactive can't reach the tools right now")
	}
	chatID := ""
	if row.ChatID != nil {
		chatID = *row.ChatID
	}
	list, err := host.Tools(ctx, agentID, chatID, now)
	if err != nil {
		return out, err
	}
	tools, err := Resolve(Mentions(cfg.Prompt), list)
	if err != nil {
		return out, err
	}
	in, err := e.runInput(agent, row, cfg, tools, e.tz(), SummaryNone, now)
	if err != nil {
		return out, err
	}
	model, ok, err := agentModel(q, e.o.Models, agent, cfg.ModelRole, now, e.o.Clock, &out.Tokens)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, bad("No model is available, or a token budget is used up for today")
	}
	res, err := Run(ctx, model, in)
	out.Calls = append(out.Calls, res.Calls...)
	if err != nil {
		out.Error = clip(err.Error(), 600)
		return out, nil
	}
	for _, it := range res.Output.Items {
		ti := TestItem{Key: it.Key, Subject: it.Subject, Text: it.Text, Urgent: it.Urgent}
		if it.SendAt > 0 {
			at := it.SendAt
			ti.SendAt = &at
		}
		if m, err := muted(q, agentID, it.Subject); err == nil && m {
			ti.Dropped = "muted"
		} else if ex, found, err := nudgeByKey(q, agentID, it.Key); err == nil && found && ex.Status != StatusPending && ex.Status != StatusScheduled {
			ti.Dropped = "already " + ex.Status
		}
		out.Items = append(out.Items, ti)
	}
	for _, c := range res.Output.Closed {
		out.Closed = append(out.Closed, c.Key)
	}
	for _, r := range res.Output.Remember {
		out.Remember = append(out.Remember, r.Text)
	}
	out.Forget = append(out.Forget, res.Output.Forget...)
	out.Ignored = append(out.Ignored, res.Output.Skipped...)
	return out, nil
}

var _ reminders.Extension = (*Engine)(nil)
