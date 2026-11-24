package proactive

// The proactive engine (RFC 6): a scheduler extension. Each sweep
//
//	ticks      claims agents whose next_tick_at passed (lease, then the
//	           next tick time moves on) and runs a tick in the background:
//	           collect, reconcile with the ledger, decide, send
//	timed      claims scheduled nudges whose wake_at passed and sends them
//	           from a template, no model (RFC 7); a snoozed chat nudge goes
//	           back to pending and pokes its agent's tick
//	summaries  claims a morning or evening summary once per day by
//	           inserting its nudge row (the key is unique)
//
// Engine implements reminders.Extension, Recoverer and Idler.

import (
	"context"
	"database/sql"
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
	// SourceTimeout bounds one source read.
	SourceTimeout = 20 * time.Second
	// HorizonMs is how far ahead the calendar is read (RFC 5.1).
	HorizonMs = 36 * 3600_000
	// MergeWindowMs: timed nudges this close together go out as one message.
	MergeWindowMs = 5 * 60_000
	// SummaryLateMs: a summary more than this late (downtime) is skipped.
	SummaryLateMs = 2 * 3600_000
	// UrgentWithinMs: a change to an event this soon passes quiet hours.
	UrgentWithinMs = 3 * 3600_000
	// OutboxTTLMs: a proactive message older than this isn't worth sending.
	OutboxTTLMs = 6 * 3600_000
	// HoldTicks: a pending nudge the model keeps skipping is sent after this
	// many ticks, from its template.
	HoldTicks = 2
	// FailNoticeAfter: a source failing this many ticks in a row tells the
	// owner, once a day.
	FailNoticeAfter = 3
)

// Options wire the engine.
type Options struct {
	DB  db.Q
	Log *logx.Logger
	// Unix ms; default time.Now.
	Clock   func() int64
	Sources Sources
	// Models resolves the agent's model chain (the supervisor).
	Models models.ModelHost
	// Profile is the agent's memory profile for judging importance; nil or
	// "" skips it.
	Profile func(ctx context.Context, a db.Agent, chatID string) string
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
	profiles  map[string]profileEntry
	poke      func()
}

type profileEntry struct {
	day  string
	text string
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
	return &Engine{o: o, log: log, busy: map[string]bool{}, profiles: map[string]profileEntry{}}
}

// OnPoke sets what CheckNow calls to wake the scheduler.
func (e *Engine) OnPoke(fn func()) { e.poke = fn }

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

// Recover releases what a crash left claimed. A timed nudge's send and its
// status move are one transaction, so a firing row was never sent.
func (e *Engine) Recover(now int64) error {
	if _, err := e.o.DB.Exec("UPDATE agent_proactive SET tick_lease_until = NULL WHERE tick_lease_until IS NOT NULL"); err != nil {
		return err
	}
	_, err := e.o.DB.Exec("UPDATE nudges SET status = ?, updated_at = ? WHERE status = ? AND kind NOT IN (?, ?)", StatusScheduled, now, StatusFiring, KindSumMorning, KindSumEvening)
	if err != nil {
		return err
	}
	// A summary claimed but never sent: let the next sweep try again.
	_, err = e.o.DB.Exec("DELETE FROM nudges WHERE status = ? AND kind IN (?, ?)", StatusFiring, KindSumMorning, KindSumEvening)
	return err
}

// CheckNow ticks an agent on the next sweep ("cek sekarang").
func (e *Engine) CheckNow(agentID string, now int64) error {
	res, err := e.o.DB.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ? AND enabled = 1", now, agentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return bad("Proactive is off for this agent")
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

// NextDue is the earliest of the next tick, the next timed nudge and the next
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
	if err := q.QueryRow("SELECT min(wake_at) FROM nudges WHERE status IN (?, ?) AND wake_at IS NOT NULL", StatusScheduled, StatusSnoozed).Scan(&n); err != nil {
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
	loc := loadTZ(tz)
	t := time.UnixMilli(now).In(loc).AddDate(0, 0, 1)
	m := minutesOf(hhmm)
	return time.Date(t.Year(), t.Month(), t.Day(), m/60, m%60, 0, 0, loc).UnixMilli()
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
		// Timed nudges first: they're the ones with a minute that matters.
		sent, err := e.fireTimed(r, cfg, tz, now)
		if err != nil {
			e.log.Error("proactive timed nudges failed", logx.Fields{"agent_id": r.AgentID, "err": err})
		}
		n += sent
		if r.NextTickAt != nil && *r.NextTickAt <= now && (r.TickLeaseUntil == nil || *r.TickLeaseUntil <= now) {
			res, err := q.Exec("UPDATE agent_proactive SET tick_lease_until = ?, next_tick_at = ? WHERE agent_id = ? AND enabled = 1 AND next_tick_at <= ? AND (tick_lease_until IS NULL OR tick_lease_until <= ?)",
				now+TickLeaseMs, now+int64(cfg.CheckEveryMin)*60_000, r.AgentID, now, now)
			if err != nil {
				return n, err
			}
			if c, _ := res.RowsAffected(); c == 1 {
				agentID := r.AgentID
				if e.spawn("tick:"+agentID, func() { e.tick(agentID) }) {
					n++
				}
			}
		}
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
			key := "sum:" + strings.TrimPrefix(which, "sum_") + ":" + date
			c := Candidate{Key: key, Kind: which, Subject: "summary", Snapshot: Snapshot{Date: date}}
			if _, found, err := nudgeByKey(q, r.AgentID, key); err != nil || found {
				continue
			}
			if _, err := insertNudge(q, r.AgentID, c, StatusFiring, nil, nil, now); err != nil {
				continue // another sweep claimed it
			}
			agentID, kind := r.AgentID, which
			if e.spawn("summary:"+agentID+":"+kind, func() { e.summary(agentID, kind, key) }) {
				n++
			}
		}
	}
	if e.o.Serial {
		e.drain()
	}
	return n, nil
}

// fireTimed sends this agent's due timed nudges (RFC 7) and turns due
// snoozed chat nudges back to pending.
func (e *Engine) fireTimed(r db.AgentProactive, cfg Config, tz string, now int64) (int, error) {
	q := e.o.DB
	// A snoozed chat nudge is checked against the source again first.
	res, err := q.Exec("UPDATE nudges SET status = ?, wake_at = NULL, updated_at = ? WHERE agent_id = ? AND status = ? AND kind = ? AND wake_at <= ?", StatusPending, now, r.AgentID, StatusSnoozed, KindWAWaiting, now)
	if err != nil {
		return 0, err
	}
	if c, _ := res.RowsAffected(); c > 0 {
		if _, err := q.Exec("UPDATE agent_proactive SET next_tick_at = ? WHERE agent_id = ?", now, r.AgentID); err != nil {
			return 0, err
		}
	}
	var due int
	if err := q.QueryRow("SELECT count(*) FROM nudges WHERE agent_id = ? AND status IN (?, ?) AND wake_at <= ?", r.AgentID, StatusScheduled, StatusSnoozed, now).Scan(&due); err != nil || due == 0 {
		return 0, err
	}
	rows, err := q.WQuery("UPDATE nudges SET status = ?, updated_at = ? WHERE agent_id = ? AND status IN (?, ?) AND wake_at <= ? RETURNING "+db.NudgeCols, StatusFiring, now, r.AgentID, StatusScheduled, StatusSnoozed, now+MergeWindowMs)
	claimed, err := db.All(rows, err, db.ScanNudge)
	if err != nil {
		return 0, err
	}
	sort.Slice(claimed, func(i, j int) bool { return deref(claimed[i].WakeAt) < deref(claimed[j].WakeAt) })
	var send []db.Nudge
	quiet := cfg.QuietHours.InQuiet(localMinute(now, tz))
	for _, n := range claimed {
		s := snapshotOf(n)
		switch {
		case s.Start != 0 && s.Start <= now:
			err = setStatus(q, n.ID, StatusExpired, "the event already started", now)
		case quiet && !cfg.QuietHours.UrgentPasses:
			err = setStatus(q, n.ID, StatusExpired, "quiet hours", now)
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
	lines := make([]string, len(send))
	for i, n := range send {
		lines[i] = Line(n, tz, now)
	}
	return len(send), e.deliver(r, send, strings.Join(lines, "\n\n"), now)
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
	if r.ChatID == nil {
		return nil
	}
	q := e.o.DB
	chat, err := CheckTarget(q, *r.ChatID)
	if err != nil {
		return e.pause(r.AgentID, err.Error(), now)
	}
	hasChat := false
	for _, n := range list {
		hasChat = hasChat || n.Kind == KindWAWaiting
	}
	expires := now + OutboxTTLMs
	err = q.Tx(func(tx db.Q) error {
		row, err := outbox.Enqueue(tx, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: text, Actions: Buttons(list[0], hasChat), Format: "plain"}, Source: OutboxSourcePrefix + r.AgentID, ExpiresAt: &expires}, now)
		if err != nil {
			return err
		}
		for _, n := range list {
			esc := any(nil)
			if snapshotOf(n).Escalation {
				esc = now
			}
			if _, err := tx.Exec("UPDATE nudges SET status = ?, sent_at = ?, outbox_id = ?, escalated_at = coalesce(?, escalated_at), wake_at = NULL, updated_at = ? WHERE id = ?", StatusSent, now, row.ID, esc, now, n.ID); err != nil {
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

// pause stops an agent's proactive until the owner saves again (RFC 11).
func (e *Engine) pause(agentID, reason string, now int64) error {
	q := e.o.DB
	if _, err := q.Exec("UPDATE agent_proactive SET paused_reason = ?, tick_lease_until = NULL, updated_at = ? WHERE agent_id = ?", reason, now, agentID); err != nil {
		return err
	}
	_, err := events.Log(q, events.Input{Level: "warn", Kind: "proactive.paused", Message: "Proactive paused for agent " + agentID + ": " + reason}, now)
	return err
}

// SourceResult is one source's outcome on a tick (proactive_ticks.sources).
type SourceResult struct {
	OK    bool   `json:"ok"`
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

func (e *Engine) tick(agentID string) {
	q := e.o.DB
	now := e.o.Clock()
	tickID := secret.NewID("ptk")
	results := map[string]SourceResult{}
	var tokens int64
	found, sent := 0, 0
	var tickErr error
	defer func() {
		raw, _ := jsonx.Marshal(results)
		var msg *string
		if tickErr != nil {
			s := tickErr.Error()
			msg = &s
			e.log.Error("proactive tick failed", logx.Fields{"agent_id": agentID, "err": tickErr})
		}
		end := e.o.Clock()
		_, _ = q.Exec("INSERT INTO proactive_ticks (id, agent_id, started_at, finished_at, sources, found, sent, tokens, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", tickID, agentID, now, end, string(raw), found, sent, tokens, msg)
		_, _ = q.Exec("UPDATE agent_proactive SET last_tick_at = ?, tick_lease_until = NULL WHERE agent_id = ?", now, agentID)
	}()
	row, cfg, ok, err := Get(q, agentID)
	if err != nil || !ok || !row.Enabled || row.PausedReason != nil {
		tickErr = err
		return
	}
	agent, ok, err := agents.Get(q, agentID)
	if err != nil || !ok {
		tickErr = err
		return
	}
	if row.ChatID == nil {
		tickErr = e.pause(agentID, "No chat to send to", now)
		return
	}
	if _, err := CheckTarget(q, *row.ChatID); err != nil {
		tickErr = e.pause(agentID, err.Error(), now)
		return
	}
	tz := e.tz()
	ctx := context.Background()
	var cands []Candidate
	okCal, okWA := false, false
	if cal := cfg.Calendar; cal != nil {
		sctx, cancel := context.WithTimeout(ctx, SourceTimeout)
		evs, err := e.o.Sources.Events(sctx, *cal, agentID, now, now+HorizonMs, tz)
		cancel()
		if err != nil {
			results["calendar"] = SourceResult{Error: clip(err.Error(), 300)}
		} else {
			okCal = true
			cc := CalendarCandidates(evs, *cal)
			cands = append(cands, cc...)
			results["calendar"] = SourceResult{OK: true, Count: len(evs)}
		}
	}
	if wa := cfg.WhatsApp; wa != nil {
		sctx, cancel := context.WithTimeout(ctx, SourceTimeout)
		chats, err := e.o.Sources.Waiting(sctx, *wa, agentID, now-int64(wa.MaxAgeDays)*24*3600_000)
		cancel()
		if err != nil {
			results["whatsapp"] = SourceResult{Error: clip(err.Error(), 300)}
		} else {
			okWA = true
			wc := WhatsAppCandidates(chats, *wa, now)
			cands = append(cands, wc...)
			results["whatsapp"] = SourceResult{OK: true, Count: len(chats)}
		}
	}
	for _, c := range cands {
		if c.Kind != KindCalEvent {
			found++
		}
	}
	seed := row.LastTickAt == nil
	if err := q.Tx(func(tx db.Q) error { return reconcile(tx, agentID, cfg, cands, okCal, okWA, seed, now) }); err != nil {
		tickErr = err
		return
	}
	e.failNotice(row, results, tz, now)
	if okWA {
		if err := e.triage(ctx, row, cfg, agent, tz, now, &tokens); err != nil {
			tickErr = err
			return
		}
	}
	sent, tickErr = e.decideAndSend(ctx, row, cfg, agent, tz, now, &tokens)
}

// failNotice tells the owner once a day when a source failed the last
// FailNoticeAfter ticks in a row (this one included).
func (e *Engine) failNotice(r db.AgentProactive, results map[string]SourceResult, tz string, now int64) {
	q := e.o.DB
	for name, res := range results {
		if res.OK {
			continue
		}
		rows, err := q.Query("SELECT sources FROM proactive_ticks WHERE agent_id = ? ORDER BY started_at DESC LIMIT ?", r.AgentID, FailNoticeAfter-1)
		if err != nil {
			return
		}
		failed := 1
		for rows.Next() {
			var raw string
			if rows.Scan(&raw) != nil {
				continue
			}
			var m map[string]SourceResult
			if jsonUnmarshal(raw, &m) == nil {
				if r, ok := m[name]; ok && !r.OK {
					failed++
				}
			}
		}
		rows.Close()
		if failed < FailNoticeAfter {
			continue
		}
		kind := "proactive.source_failed." + name
		var x int
		dayStart := atOn("00:00", tz, now)
		err = q.QueryRow("SELECT 1 FROM events WHERE kind = ? AND at >= ? AND message LIKE ? LIMIT 1", kind, dayStart, "%"+r.AgentID+"%").Scan(&x)
		if !noRows(err) {
			continue // already told today, or the check failed: don't risk a repeat
		}
		label := map[string]string{"calendar": "Kalender", "whatsapp": "WhatsApp"}[name]
		_, _ = events.Log(q, events.Input{Level: "warn", Kind: kind, Message: "Proactive source " + name + " keeps failing for agent " + r.AgentID + ": " + res.Error}, now)
		if r.ChatID == nil {
			continue
		}
		chat, err := CheckTarget(q, *r.ChatID)
		if err != nil {
			continue
		}
		text := fmt.Sprintf("⚠️ %s nggak kebaca dari tadi, jadi aku belum bisa ngabarin dari situ. Error-nya: %s", label, Field(res.Error))
		expires := now + OutboxTTLMs
		if _, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chat.ID, Channel: chat.Channel, ConnectorID: chat.ConnectorID, Payload: db.OutboxPayload{Text: text, Format: "plain"}, Source: "system", ExpiresAt: &expires}, now); err == nil {
			outbox.Notify()
		}
	}
}

// reconcile merges this tick's candidates into the ledger (RFC 6, step 3).
func reconcile(q db.Q, agentID string, cfg Config, cands []Candidate, okCal, okWA, seed bool, now int64) error {
	seen := map[string]bool{}
	for _, c := range cands {
		seen[c.Key] = true
		if c.Kind != KindCalEvent {
			m, err := muted(q, agentID, c.Subject)
			if err != nil {
				return err
			}
			if m {
				continue
			}
		}
		ex, found, err := nudgeByKey(q, agentID, c.Key)
		if err != nil {
			return err
		}
		snap, err := jsonx.Marshal(c.Snapshot)
		if err != nil {
			return err
		}
		switch c.Kind {
		case KindCalEvent:
			if !found {
				_, err = insertNudge(q, agentID, c, StatusTracking, nil, nil, now)
				break
			}
			prev := snapshotOf(ex)
			moved := prev.Start != c.Snapshot.Start || strings.TrimSpace(prev.Location) != strings.TrimSpace(c.Snapshot.Location)
			if moved && ex.Status == StatusTracking && cfg.Calendar != nil && cfg.Calendar.NotifyChanges && c.Snapshot.Start > now {
				ch := c
				ch.Kind = KindCalChanged
				ch.Key = fmt.Sprintf("cal:%s:changed:%d:%s", c.Snapshot.EventID, c.Snapshot.Start, shortHash(c.Snapshot.Location))
				ch.Snapshot.PrevStart = prev.Start
				ch.Snapshot.PrevLocation = prev.Location
				if _, dup, err := nudgeByKey(q, agentID, ch.Key); err != nil {
					return err
				} else if !dup {
					if m, err := muted(q, agentID, ch.Subject); err != nil {
						return err
					} else if !m {
						if _, err := insertNudge(q, agentID, ch, StatusPending, nil, nil, now); err != nil {
							return err
						}
					}
				}
			}
			_, err = q.Exec("UPDATE nudges SET status = ?, snapshot = ?, updated_at = ? WHERE id = ?", StatusTracking, string(snap), now, ex.ID)
		case KindCalDepart, KindCalStart:
			if c.Snapshot.Start <= now {
				break
			}
			wake := max(c.WakeAt, now)
			if !found {
				_, err = insertNudge(q, agentID, c, StatusScheduled, &wake, nil, now)
				break
			}
			if ex.Status == StatusScheduled && (deref(ex.WakeAt) != wake || string(ex.Snapshot) != string(snap)) {
				_, err = q.Exec("UPDATE nudges SET wake_at = ?, snapshot = ?, updated_at = ? WHERE id = ?", wake, string(snap), now, ex.ID)
			}
		case KindWAWaiting:
			if !found {
				if seed {
					r := "already waiting when proactive started"
					_, err = insertNudge(q, agentID, c, StatusDropped, nil, &r, now)
				} else {
					_, err = insertNudge(q, agentID, c, StatusPending, nil, nil, now)
				}
				break
			}
			wa := cfg.WhatsApp
			escalate := ex.Status == StatusSent && ex.EscalatedAt == nil && wa != nil && wa.EscalateHours > 0 && ex.SentAt != nil && now-*ex.SentAt >= int64(wa.EscalateHours)*3600_000
			switch {
			case escalate:
				c.Snapshot.Escalation = true
				snap, _ = jsonx.Marshal(c.Snapshot)
				_, err = q.Exec("UPDATE nudges SET status = ?, snapshot = ?, updated_at = ? WHERE id = ?", StatusPending, string(snap), now, ex.ID)
			case ex.Status == StatusPending || ex.Status == StatusSent || ex.Status == StatusSnoozed:
				// Keep the latest preview and count; the escalation mark and the
				// triage verdict stay (a higher count makes triage look again).
				prev := snapshotOf(ex)
				c.Snapshot.Escalation, c.Snapshot.TriagedCount, c.Snapshot.Gist = prev.Escalation, prev.TriagedCount, prev.Gist
				snap, _ = jsonx.Marshal(c.Snapshot)
				_, err = q.Exec("UPDATE nudges SET snapshot = ? WHERE id = ?", string(snap), ex.ID)
			case ex.Status == StatusDropped && ex.Reason != nil && strings.HasPrefix(*ex.Reason, ReasonClosed) && c.Snapshot.Count > snapshotOf(ex).Count:
				// Judged finished, then they wrote again without a reply in
				// between: judge it again.
				_, err = q.Exec("UPDATE nudges SET status = ?, reason = NULL, snapshot = ?, updated_at = ? WHERE id = ?", StatusPending, string(snap), now, ex.ID)
			}
		}
		if err != nil {
			return err
		}
	}
	// What a source that answered no longer shows.
	if okWA {
		rows, err := q.Query("SELECT id, key FROM nudges WHERE agent_id = ? AND kind = ? AND status IN (?, ?, ?)", agentID, KindWAWaiting, StatusPending, StatusSent, StatusSnoozed)
		if err != nil {
			return err
		}
		var gone []string
		for rows.Next() {
			var id, key string
			if err := rows.Scan(&id, &key); err != nil {
				rows.Close()
				return err
			}
			if !seen[key] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		for _, id := range gone {
			if err := setStatus(q, id, StatusDone, "no longer waiting for a reply", now); err != nil {
				return err
			}
		}
	}
	if okCal {
		rows, err := q.Query("SELECT id, key, kind FROM nudges WHERE agent_id = ? AND ((kind IN (?, ?) AND status = ?) OR (kind = ? AND status = ?))", agentID, KindCalDepart, KindCalStart, StatusScheduled, KindCalEvent, StatusTracking)
		if err != nil {
			return err
		}
		type g struct{ id, kind string }
		var gone []g
		for rows.Next() {
			var id, key, kind string
			if err := rows.Scan(&id, &key, &kind); err != nil {
				rows.Close()
				return err
			}
			if !seen[key] {
				gone = append(gone, g{id, kind})
			}
		}
		rows.Close()
		for _, x := range gone {
			status, reason := StatusDropped, "the event was cancelled or moved out of range"
			if x.kind == KindCalEvent {
				reason = "no longer in range"
			}
			if err := setStatus(q, x.id, status, reason, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func shortHash(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

func jsonUnmarshal(s string, v any) error { return db.RawJSON(s).Decode(v) }

// decideAndSend tells the owner what's pending, within quiet hours and the
// rate limit (RFC 8, 9). It returns how many nudges went out.
func (e *Engine) decideAndSend(ctx context.Context, r db.AgentProactive, cfg Config, agent db.Agent, tz string, now int64, tokens *int64) (int, error) {
	q := e.o.DB
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND status = ? ORDER BY created_at, id", r.AgentID, StatusPending)
	pending, err := db.All(rows, err, db.ScanNudge)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	urgent := func(n db.Nudge) bool {
		s := snapshotOf(n)
		return n.Kind == KindCalChanged && s.Start-now <= UrgentWithinMs
	}
	if cfg.QuietHours.InQuiet(localMinute(now, tz)) {
		var keep []db.Nudge
		if cfg.QuietHours.UrgentPasses {
			for _, n := range pending {
				if urgent(n) {
					keep = append(keep, n)
				}
			}
		}
		if pending = keep; len(pending) == 0 {
			return 0, nil
		}
	}
	var recent int
	if err := q.QueryRow("SELECT count(DISTINCT outbox_id) FROM nudges WHERE agent_id = ? AND sent_at >= ? AND outbox_id IS NOT NULL AND kind NOT IN (?, ?)", r.AgentID, now-3600_000, KindSumMorning, KindSumEvening).Scan(&recent); err != nil {
		return 0, err
	}
	if recent >= cfg.MaxPerHour {
		var keep []db.Nudge
		for _, n := range pending {
			if urgent(n) {
				keep = append(keep, n)
			}
		}
		if pending = keep; len(pending) == 0 {
			return 0, nil
		}
	}
	important := map[string]bool{}
	for _, c := range cfg.Contacts {
		important[c.Subject] = true
	}
	mustTell := func(n db.Nudge) bool {
		return important[n.Subject] || n.Kind == KindCalChanged || snapshotOf(n).Escalation
	}
	holdMs := int64(HoldTicks*cfg.CheckEveryMin) * 60_000

	var send []db.Nudge
	text := ""
	model, ok, err := agentModel(q, e.o.Models, agent, cfg.ModelRole, now, e.o.Clock, tokens)
	if err != nil {
		return 0, err
	}
	var decision Decision
	decided := false
	if ok {
		in := DecideInput{Agent: agent, TZ: tz, Now: now, Items: pending, Important: important}
		in.ToldToday, _ = e.toldToday(r.AgentID, tz, now)
		if cfg.ContactsAuto {
			in.Profile = e.profile(ctx, agent, deref2(r.ChatID), tz, now)
		}
		decision, err = Decide(ctx, model, in)
		if err != nil {
			e.log.Warn("proactive decide failed, using templates", logx.Fields{"agent_id": r.AgentID, "err": err})
		} else {
			decided = true
		}
	}
	var extra []string
	if decided {
		inSend := map[string]bool{}
		for _, k := range decision.Send {
			inSend[k] = true
		}
		for _, n := range pending {
			switch {
			case inSend[n.Key]:
				send = append(send, n)
			case mustTell(n):
				send = append(send, n)
				extra = append(extra, Line(n, tz, now))
			case decision.Drop[n.Key] != "":
				if err := setStatus(q, n.ID, StatusDropped, "model: "+decision.Drop[n.Key], now); err != nil {
					return 0, err
				}
			case now-n.CreatedAt >= holdMs:
				send = append(send, n)
				extra = append(extra, Line(n, tz, now))
			}
		}
		text = decision.Text
		if len(decision.Send) == 0 {
			text = ""
		}
	} else {
		for _, n := range pending {
			if mustTell(n) || now-n.CreatedAt >= holdMs {
				send = append(send, n)
				extra = append(extra, Line(n, tz, now))
			}
		}
	}
	if len(send) == 0 {
		return 0, nil
	}
	parts := []string{}
	if text != "" {
		parts = append(parts, text)
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		for _, n := range send {
			parts = append(parts, Line(n, tz, now))
		}
	}
	return len(send), e.deliver(r, send, MessageText(strings.Join(parts, "\n\n")), now)
}

func deref2(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// toldToday is today's sent nudges as short lines.
func (e *Engine) toldToday(agentID, tz string, now int64) ([]string, error) {
	rows, err := e.o.DB.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND status IN (?, ?) AND sent_at >= ? AND kind NOT IN (?, ?) ORDER BY sent_at LIMIT 30", agentID, StatusSent, StatusDone, atOn("00:00", tz, now), KindSumMorning, KindSumEvening)
	list, err := db.All(rows, err, db.ScanNudge)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(list))
	for i, n := range list {
		out[i] = Line(n, tz, now)
	}
	return out, nil
}

// profile is the agent's memory profile, read once a day.
func (e *Engine) profile(ctx context.Context, a db.Agent, chatID, tz string, now int64) string {
	if e.o.Profile == nil {
		return ""
	}
	day := localDate(now, tz)
	e.mu.Lock()
	p, ok := e.profiles[a.ID]
	e.mu.Unlock()
	if ok && p.day == day {
		return p.text
	}
	text := e.o.Profile(ctx, a, chatID)
	e.mu.Lock()
	e.profiles[a.ID] = profileEntry{day: day, text: text}
	e.mu.Unlock()
	return text
}

// summary builds and sends the morning or evening message (RFC 9). Its nudge
// row was inserted as firing by the sweep that claimed it.
func (e *Engine) summary(agentID, kind, key string) {
	q := e.o.DB
	now := e.o.Clock()
	var tokens int64
	nudge, ok, err := nudgeByKey(q, agentID, key)
	if err != nil || !ok {
		return
	}
	fail := func(err error) {
		e.log.Error("proactive summary failed", logx.Fields{"agent_id": agentID, "kind": kind, "err": err})
		_ = setStatus(q, nudge.ID, StatusDropped, clip(err.Error(), 200), now)
	}
	row, cfg, ok, err := Get(q, agentID)
	if err != nil || !ok || !row.Enabled {
		_ = setStatus(q, nudge.ID, StatusDropped, "proactive is off", now)
		return
	}
	agent, ok, err := agents.Get(q, agentID)
	if err != nil || !ok {
		fail(fmt.Errorf("agent missing: %v", err))
		return
	}
	tz := e.tz()
	morning := kind == KindSumMorning
	facts, held, err := e.summaryFacts(context.Background(), row, cfg, agentID, tz, now, morning)
	if err != nil {
		fail(err)
		return
	}
	text := ""
	if model, ok, err := agentModel(q, e.o.Models, agent, cfg.ModelRole, now, e.o.Clock, &tokens); err == nil && ok {
		text, err = Summarize(context.Background(), model, SummaryInput{Agent: agent, TZ: tz, Now: now, Morning: morning, Facts: facts})
		if err != nil {
			e.log.Warn("proactive summary wording failed, using the list", logx.Fields{"agent_id": agentID, "err": err})
		}
	}
	if text == "" {
		head := "Pagi! Ini hari ini:"
		if !morning {
			head = "Buat besok:"
		}
		if len(facts) == 0 {
			text = map[bool]string{true: "Pagi! Hari ini kosong, nggak ada jadwal atau chat yang nunggu.", false: "Besok kosong, nggak ada jadwal."}[morning]
		} else {
			text = head + "\n" + strings.Join(facts, "\n")
		}
	}
	list := append([]db.Nudge{nudge}, held...)
	if err := e.deliver(row, list, MessageText(text), now); err != nil {
		fail(err)
	}
}

// summaryFacts are the plain lines a summary is made of. Morning: today's
// events, reminders due today, chats waiting, and what quiet hours held
// (returned so it's marked sent with the summary). Evening: tomorrow's events
// and when to leave for the first one.
func (e *Engine) summaryFacts(ctx context.Context, r db.AgentProactive, cfg Config, agentID, tz string, now int64, morning bool) ([]string, []db.Nudge, error) {
	q := e.o.DB
	var facts []string
	dayStart := atOn("00:00", tz, now)
	from, to := dayStart, dayStart+24*3600_000
	if !morning {
		from, to = to, to+24*3600_000
	}
	if cal := cfg.Calendar; cal != nil {
		sctx, cancel := context.WithTimeout(ctx, SourceTimeout)
		evs, err := e.o.Sources.Events(sctx, *cal, agentID, from, to, tz)
		cancel()
		if err != nil {
			facts = append(facts, "(Kalender nggak kebaca: "+Field(err.Error())+")")
		}
		firstDepart := int64(0)
		for _, ev := range evs {
			line := "- " + Field(ev.Title)
			if ev.AllDay {
				line += " (seharian)"
			} else {
				line = "- " + clock(ev.Start, tz) + " " + Field(ev.Title)
				if isPhysical(ev.Location) && firstDepart == 0 {
					firstDepart = ev.Start - int64(cal.DepartLeadMin)*60_000
				}
			}
			if ev.Location != "" {
				line += " di " + Field(ev.Location)
			}
			facts = append(facts, line)
		}
		if !morning && firstDepart != 0 {
			facts = append(facts, "- Berangkat paling lambat "+clock(firstDepart, tz))
		}
	}
	if morning {
		rems, err := remindersBetween(q, agentID, from, to)
		if err != nil {
			return nil, nil, err
		}
		for _, rm := range rems {
			facts = append(facts, "- Pengingat "+clock(rm.at, tz)+": "+Field(rm.text))
		}
		if wa := cfg.WhatsApp; wa != nil {
			sctx, cancel := context.WithTimeout(ctx, SourceTimeout)
			chats, err := e.o.Sources.Waiting(sctx, *wa, agentID, now-int64(wa.MaxAgeDays)*24*3600_000)
			cancel()
			if err != nil {
				facts = append(facts, "(WhatsApp nggak kebaca: "+Field(err.Error())+")")
			} else {
				// Muted chats and ones triage judged finished are left out.
				var lines []string
				for _, c := range WhatsAppCandidates(chats, *wa, now) {
					if m, _ := muted(q, agentID, c.Subject); m {
						continue
					}
					line := "- " + Field(c.Snapshot.Name) + " nunggu balasan dari " + when(c.Snapshot.AwaitingSince, tz, now)
					if n, ok, _ := nudgeByKey(q, agentID, c.Key); ok {
						if n.Status == StatusDropped && n.Reason != nil && strings.HasPrefix(*n.Reason, ReasonClosed) {
							continue
						}
						if g := snapshotOf(n).Gist; g != "" {
							line += ": " + Field(g)
						}
					}
					lines = append(lines, line)
				}
				if len(lines) > 3 {
					lines = append(lines[:3], fmt.Sprintf("- Dan %d chat lain yang belum kebales", len(lines)-3))
				}
				facts = append(facts, lines...)
			}
		}
	}
	rows, err := q.Query("SELECT "+db.NudgeCols+" FROM nudges WHERE agent_id = ? AND status = ? ORDER BY created_at, id", agentID, StatusPending)
	held, err := db.All(rows, err, db.ScanNudge)
	if err != nil {
		return nil, nil, err
	}
	if !morning {
		held = nil
	}
	for _, n := range held {
		if n.Kind != KindWAWaiting { // waiting chats are already listed above
			facts = append(facts, "- "+Line(n, tz, now))
		}
	}
	return facts, held, nil
}

type reminderFact struct {
	at   int64
	text string
}

// remindersBetween is the pending reminders due in a window, in the chats
// this agent serves.
func remindersBetween(q db.Q, agentID string, from, to int64) ([]reminderFact, error) {
	where, args, err := agents.ChatsServedBy(q, agentID)
	if err != nil {
		return nil, err
	}
	all := append([]any{from, to}, args...)
	rows, err := q.Query(`SELECT fire_at, text FROM reminders WHERE status = 'pending' AND fire_at >= ? AND fire_at < ? AND chat_id IN (SELECT "chats"."id" FROM chats WHERE `+where+`) ORDER BY fire_at LIMIT 20`, all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reminderFact
	for rows.Next() {
		var f reminderFact
		if err := rows.Scan(&f.at, &f.text); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

var _ reminders.Extension = (*Engine)(nil)
