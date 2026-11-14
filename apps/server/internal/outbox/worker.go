package outbox

// The outbox worker (PLAN 8.7, 6, 4.3), from outbox/worker.ts: claim, send
// through the channel handle, ack, retry with backoff. Delivery is at least
// once: a row is only `sent` after the channel acks it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

var OutboxBackoffMs = []int64{5_000, 30_000, 120_000, 600_000, 1_800_000}

const (
	StuckSendingMs = 5 * 60_000
	// A send that takes longer counts as a failed attempt.
	SendTimeoutMs = 30_000
	// Rows on a paused channel don't expire; their expiry moves this far past
	// now on every tick while paused.
	PausedExpiryGraceMs = 60 * 60_000
	stopWaitMs          = 10_000
	batch               = 20
	tickMs              = 2_000
)

// Channel states as the worker sees them.
//
//	ready:   running and reachable, send.
//	paused:  disabled by the owner, rows wait without using attempts.
//	down:    enabled but not running or reachable, sends fail and back off.
//	removed: no connector row, rows go to the fallback or get cancelled.
const (
	StateReady   = "ready"
	StatePaused  = "paused"
	StateDown    = "down"
	StateRemoved = "removed"
)

type WorkerOptions struct {
	DB *db.DB
	// The channel's main connector.
	Channel func(ch string) Channel
	// A specific connector's channel (an agent's Telegram bot), for rows with connector_id set.
	Connector     func(id string) Channel
	Log           *logx.Logger
	Clock         func() int64
	SendTimeoutMs int64
	// outbound/service.ts outboundFailed (M4): tells the owner a message to a
	// contact gave up. Called for every expired row; nil does nothing.
	OutboundFailed func(q db.Q, row db.Outbox, now int64)
}

func safeReachable(ch Channel) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return ch.Reachable()
}

// ChannelState: `ch` is a connector id; the channel's main connector shares the channel's name.
func ChannelState(q db.Q, ch string, handle Channel) (string, error) {
	if ch == db.ChannelWeb {
		if handle != nil {
			return StateReady, nil
		}
		return StateDown, nil
	}
	var enabled bool
	var status string
	err := q.QueryRow("SELECT enabled, status FROM connectors WHERE id = ?", ch).Scan(&enabled, &status)
	if err != nil {
		if errors.Is(err, errNoRows) {
			return StateRemoved, nil
		}
		return "", err
	}
	if !enabled || status == "paused" {
		return StatePaused, nil
	}
	if handle == nil {
		return StateDown, nil
	}
	if safeReachable(handle) {
		return StateReady, nil
	}
	return StateDown, nil
}

func BackoffFor(attempts int64) int64 {
	i := min(max(attempts, 1)-1, int64(len(OutboxBackoffMs)-1))
	return OutboxBackoffMs[i]
}

type target struct{ channel, chatID string }

// fallbackTarget is the fallback channel when it's on (and, when asked, ready)
// and has an owner chat.
func fallbackTarget(q db.Q, from string, handleFor func(string) Channel, requireReady bool, now int64) (*target, error) {
	fb, err := settings.Get[string](q, "fallbackChannel")
	if err != nil {
		return nil, err
	}
	if fb == "none" || fb == from {
		return nil, nil
	}
	var enabled bool
	if err := q.QueryRow("SELECT enabled FROM connectors WHERE id = ?", fb).Scan(&enabled); err != nil {
		if errors.Is(err, errNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	if requireReady {
		var h Channel
		if handleFor != nil {
			h = handleFor(fb)
		}
		st, err := ChannelState(q, fb, h)
		if err != nil || st != StateReady {
			return nil, err
		}
	}
	chat, ok, err := chats.OwnerChat(q, fb, now)
	if err != nil || !ok {
		return nil, err
	}
	return &target{fb, chat.ID}, nil
}

// RerouteChannelRows moves waiting rows of a channel to the fallback channel,
// or cancels them (PLAN 4.3). Only `pending` rows: one in `sending` is in
// flight and may still be delivered.
func RerouteChannelRows(q db.Q, from string, now int64, handleFor func(string) Channel) (moved, cancelled int, err error) {
	rows, err := q.Query("SELECT "+db.OutboxCols+" FROM outbox WHERE channel = ? AND status = 'pending'", from)
	list, err := db.All(rows, err, db.ScanOutbox)
	if err != nil || len(list) == 0 {
		return 0, 0, err
	}
	tg, err := fallbackTarget(q, from, handleFor, false, now)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range list {
		// A message to a contact never goes to the owner's chat instead.
		if tg != nil && !strings.HasPrefix(r.Source, "outbound:") {
			if _, err := q.Exec("UPDATE outbox SET channel = ?, chat_id = ?, fallback_from = ?, status = 'pending', claimed_at = NULL, next_attempt_at = ? WHERE id = ?", tg.channel, tg.chatID, from, now, r.ID); err != nil {
				return moved, cancelled, err
			}
			moved++
		} else {
			if _, err := q.Exec("UPDATE outbox SET status = 'cancelled', error = ?, claimed_at = NULL WHERE id = ?", "channel "+from+" removed", r.ID); err != nil {
				return moved, cancelled, err
			}
			cancelled++
		}
	}
	msg := fmt.Sprintf("%d waiting message(s) cancelled: %s was removed and no fallback channel is on", cancelled, from)
	if tg != nil {
		msg = fmt.Sprintf("%d waiting message(s) moved from %s to %s", moved, from, tg.channel)
	}
	data := map[string]int{"moved": moved, "cancelled": cancelled}
	_, err = events.Log(q, events.Input{ConnectorID: &from, Kind: "outbox.reroute", Level: "warn", Message: msg, Data: orderedCounts(data)}, now)
	return moved, cancelled, err
}

func orderedCounts(m map[string]int) any {
	return struct {
		Moved     int `json:"moved"`
		Cancelled int `json:"cancelled"`
	}{m["moved"], m["cancelled"]}
}

// Worker is createOutboxWorker.
type Worker struct {
	o           WorkerOptions
	sendTimeout time.Duration

	// tickMu keeps the synchronous half of ticks (housekeeping, claims) from
	// interleaving, as the single-threaded TS did.
	tickMu   sync.Mutex
	mu       sync.Mutex
	busy     map[string]chan struct{}
	stopped  bool
	stopping bool
	unsub    func()
	ticker   *time.Ticker
	done     chan struct{}
	late     sync.WaitGroup
}

func NewWorker(o WorkerOptions) *Worker {
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	if o.Log == nil {
		o.Log = logx.Silent
	}
	timeout := o.SendTimeoutMs
	if timeout == 0 {
		timeout = SendTimeoutMs
	}
	return &Worker{o: o, sendTimeout: time.Duration(timeout) * time.Millisecond, busy: map[string]chan struct{}{}, stopped: true}
}

func (w *Worker) channel(ch string) Channel {
	if w.o.Channel == nil {
		return nil
	}
	return w.o.Channel(ch)
}

func (w *Worker) connector(id string) Channel {
	if w.o.Connector == nil {
		return nil
	}
	return w.o.Connector(id)
}

func (w *Worker) resetStuck(now int64) error {
	res, err := w.o.DB.Exec("UPDATE outbox SET status = 'pending', claimed_at = NULL WHERE status = 'sending' AND claimed_at < ?", now-StuckSendingMs)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		w.o.Log.Warn("outbox rows stuck in sending went back to pending", logx.Fields{"count": n})
	}
	return nil
}

func (w *Worker) expire(now int64) error {
	q := w.o.DB
	rows, err := q.Query("SELECT id, channel FROM outbox WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at < ?", now)
	if err != nil {
		return err
	}
	type due struct{ id, channel string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.channel); err != nil {
			rows.Close()
			return err
		}
		list = append(list, d)
	}
	rows.Close()
	if len(list) == 0 {
		return nil
	}
	states := map[string]string{}
	stateOf := func(ch string) string {
		if s, ok := states[ch]; ok {
			return s
		}
		s, _ := ChannelState(q, ch, w.channel(ch))
		states[ch] = s
		return s
	}
	// Only an owner pause (the switch) stops the clock. A connector that reports
	// `paused` itself may stay that way for days.
	ownerPaused := func(ch string) bool {
		if stateOf(ch) != StatePaused {
			return false
		}
		var enabled bool
		if err := q.QueryRow("SELECT enabled FROM connectors WHERE id = ?", ch).Scan(&enabled); err != nil {
			return false
		}
		return !enabled
	}
	var waiting, dead []any
	for _, d := range list {
		if ownerPaused(d.channel) {
			waiting = append(waiting, d.id)
		} else {
			dead = append(dead, d.id)
		}
	}
	if len(waiting) > 0 {
		args := append([]any{now + PausedExpiryGraceMs}, waiting...)
		if _, err := q.Exec("UPDATE outbox SET expires_at = ? WHERE id IN ("+marks(len(waiting))+")", args...); err != nil {
			return err
		}
	}
	if len(dead) == 0 {
		return nil
	}
	failed, err := q.WQuery("UPDATE outbox SET status = 'failed', claimed_at = NULL, error = coalesce(error, 'expired') WHERE status = 'pending' AND id IN ("+marks(len(dead))+") RETURNING "+db.OutboxCols, dead...)
	gone, err := db.All(failed, err, db.ScanOutbox)
	if err != nil {
		return err
	}
	for _, r := range gone {
		if w.o.OutboundFailed != nil {
			w.o.OutboundFailed(q, r, now)
		}
		e := "expired"
		if r.Error != nil {
			e = *r.Error
		}
		ch := r.Channel
		data := struct {
			OutboxID string `json:"outboxId"`
		}{r.ID}
		_, _ = events.Log(q, events.Input{ConnectorID: &ch, Kind: "outbox.failed", Level: "error", Message: fmt.Sprintf("Gave up sending %s after %d attempt(s): %s", r.Source, r.Attempts, e), Data: data}, now)
	}
	return nil
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?, ", n), ", ") }

func distinct(q db.Q, query string) ([]string, error) {
	rows, err := q.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (w *Worker) handleRemoved(now int64) error {
	q := w.o.DB
	chans, err := distinct(q, "SELECT DISTINCT channel FROM outbox WHERE status = 'pending' AND connector_id IS NULL")
	if err != nil {
		return err
	}
	for _, ch := range chans {
		if st, _ := ChannelState(q, ch, w.channel(ch)); st == StateRemoved {
			if _, _, err := RerouteChannelRows(q, ch, now, w.channel); err != nil {
				return err
			}
		}
	}
	// An agent bot that was removed: its rows try the channel's main connector.
	bots, err := distinct(q, "SELECT DISTINCT connector_id FROM outbox WHERE status = 'pending' AND connector_id IS NOT NULL")
	if err != nil {
		return err
	}
	for _, id := range bots {
		if st, _ := ChannelState(q, id, w.connector(id)); id != "" && st == StateRemoved {
			if _, err := q.Exec("UPDATE outbox SET connector_id = NULL WHERE connector_id = ? AND status = 'pending'", id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) claim(now int64, sendable, bots []string) ([]db.Outbox, error) {
	if len(sendable) == 0 && len(bots) == 0 {
		return nil, nil
	}
	var parts []string
	var args []any
	if len(sendable) > 0 {
		parts = append(parts, "(connector_id IS NULL AND channel IN ("+marks(len(sendable))+"))")
		for _, s := range sendable {
			args = append(args, s)
		}
	} else {
		parts = append(parts, "0")
	}
	if len(bots) > 0 {
		parts = append(parts, "connector_id IN ("+marks(len(bots))+")")
		for _, b := range bots {
			args = append(args, b)
		}
	} else {
		parts = append(parts, "0")
	}
	all := append([]any{now, now}, args...)
	// Idempotent claim: only rows still pending flip to sending.
	rows, err := w.o.DB.WQuery("UPDATE outbox SET status = 'sending', claimed_at = ? WHERE status = 'pending' AND id IN (SELECT id FROM outbox WHERE status = 'pending' AND next_attempt_at <= ? AND ("+strings.Join(parts, " OR ")+") ORDER BY created_at LIMIT "+fmt.Sprint(batch)+") RETURNING "+db.OutboxCols, all...)
	return db.All(rows, err, db.ScanOutbox)
}

type sendRes struct {
	res SendResult
	err error
}

func safeSend(h Channel, to string, payload db.OutboxPayload) (r SendResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("send panicked: %v", p)
		}
	}()
	return h.Send(context.Background(), to, payload)
}

// sendOne returns "ok", "failed" or "timeout" (the channel didn't answer in time).
func (w *Worker) sendOne(row db.Outbox) string {
	q := w.o.DB
	var handle Channel
	if row.ConnectorID != nil {
		handle = w.connector(*row.ConnectorID)
	} else {
		handle = w.channel(row.Channel)
	}
	// The kill switch holds at send time too, whatever enqueued the row.
	if strings.HasPrefix(row.Source, "outbound:") {
		ob, err := settings.GetOutbound(q)
		if err == nil && !ob.Enabled {
			_, _ = q.Exec("UPDATE outbox SET status = 'cancelled', error = 'outbound turned off', claimed_at = NULL WHERE id = ?", row.ID)
			return "failed"
		}
	}
	chat, ok, err := chats.Get(q, row.ChatID)
	if err != nil {
		w.fail(row, err.Error(), w.o.Clock())
		return "failed"
	}
	if !ok {
		w.fail(row, fmt.Sprintf("chat %s not found", row.ChatID), w.o.Clock())
		return "failed"
	}
	if handle == nil {
		name := row.Channel
		if row.ConnectorID != nil {
			name = *row.ConnectorID
		}
		w.fail(row, fmt.Sprintf("channel %s isn't running", name), w.o.Clock())
		return "failed"
	}
	results := make(chan sendRes, 1)
	go func() {
		r, err := safeSend(handle, chat.ExternalID, row.Payload)
		results <- sendRes{r, err}
	}()
	timer := time.NewTimer(w.sendTimeout)
	defer timer.Stop()
	select {
	case r := <-results:
		if r.err != nil {
			w.fail(row, r.err.Error(), w.o.Clock())
			return "failed"
		}
		w.markSent(row, r.res, false)
		return "ok"
	case <-timer.C:
		// The send can't be cancelled and may still land. Leave the row in
		// `sending`: a late success marks it sent, a late failure fails it, and if
		// it never settles the stuck-sending sweep gives it back.
		w.late.Add(1)
		go func() {
			defer w.late.Done()
			r := <-results
			if r.err == nil {
				w.markSent(row, r.res, true)
				return
			}
			var status string
			if err := q.QueryRow("SELECT status FROM outbox WHERE id = ?", row.ID).Scan(&status); err == nil && status == "sending" {
				w.fail(row, r.err.Error(), w.o.Clock())
			}
		}()
		w.o.Log.Warn("send timed out, waiting for it to settle", logx.Fields{"outbox_id": row.ID, "channel": row.Channel})
		return "timeout"
	}
}

func (w *Worker) markSent(row db.Outbox, res SendResult, onlyIfSending bool) {
	var msgID *string
	if res.ChannelMsgID != "" {
		id := res.ChannelMsgID
		msgID = &id
	}
	where := "id = ?"
	if onlyIfSending {
		where += " AND status = 'sending'"
	}
	if _, err := w.o.DB.Exec("UPDATE outbox SET status = 'sent', channel_msg_id = ?, sent_at = ?, claimed_at = NULL, error = NULL WHERE "+where, msgID, w.o.Clock(), row.ID); err != nil {
		w.o.Log.Error("marking a row sent failed", logx.Fields{"outbox_id": row.ID, "error": err})
	}
	if res.ChannelMsgID != "" {
		w.linkChannelMsg(row, res.ChannelMsgID)
	}
}

// release gives a claimed row back without using an attempt.
func (w *Worker) release(row db.Outbox) {
	_, _ = w.o.DB.Exec("UPDATE outbox SET status = 'pending', claimed_at = NULL WHERE id = ? AND status = 'sending'", row.ID)
}

// sendChannel sends one channel's rows in order. After a timeout the channel
// is treated as hung for this round: the rest go back to pending.
func (w *Worker) sendChannel(rows []db.Outbox) {
	hung := false
	for _, r := range rows {
		w.mu.Lock()
		stopping := w.stopping
		w.mu.Unlock()
		if stopping || hung {
			w.release(r)
			continue
		}
		if w.sendOne(r) == "timeout" {
			hung = true
		}
	}
}

// linkChannelMsg remembers what the channel called the message, for replies that quote it.
func (w *Worker) linkChannelMsg(row db.Outbox, msgID string) {
	var err error
	switch {
	case strings.HasPrefix(row.Source, "approval:"):
		_, err = w.o.DB.Exec("UPDATE pending_approvals SET prompt_msg_id = ? WHERE id = ?", msgID, strings.TrimPrefix(row.Source, "approval:"))
	case strings.HasPrefix(row.Source, "reply:"):
		_, err = w.o.DB.Exec("UPDATE messages SET channel_msg_id = ? WHERE run_id = ? AND role = 'assistant' AND channel_msg_id IS NULL", msgID, strings.TrimPrefix(row.Source, "reply:"))
	}
	if err != nil {
		w.o.Log.Warn("linking channel message id failed", logx.Fields{"error": err})
	}
}

func (w *Worker) fail(row db.Outbox, msg string, now int64) {
	q := w.o.DB
	attempts := row.Attempts + 1
	// Reminders fall back to the other channel after N failures (PLAN 6).
	if strings.HasPrefix(row.Source, "reminder:") && row.FallbackFrom == nil {
		after, err := settings.Get[int64](q, "reminderFallbackAfterAttempts")
		if err == nil && attempts >= after {
			tg, err := fallbackTarget(q, row.Channel, w.channel, true, now)
			if err == nil && tg != nil {
				if _, err := q.Exec("UPDATE outbox SET status = 'pending', channel = ?, chat_id = ?, fallback_from = ?, attempts = ?, next_attempt_at = ?, claimed_at = NULL, error = ? WHERE id = ?", tg.channel, tg.chatID, row.Channel, attempts, now, msg, row.ID); err != nil {
					w.o.Log.Error("outbox fallback failed", logx.Fields{"error": err})
					return
				}
				ch := row.Channel
				data := struct {
					OutboxID string `json:"outboxId"`
				}{row.ID}
				_, _ = events.Log(q, events.Input{ConnectorID: &ch, Kind: "outbox.fallback", Level: "warn", Message: fmt.Sprintf("%s moved from %s to %s after %d failed attempts", row.Source, row.Channel, tg.channel, attempts), Data: data}, now)
				w.Wake()
				return
			}
		}
	}
	if _, err := q.Exec("UPDATE outbox SET status = 'pending', attempts = ?, next_attempt_at = ?, claimed_at = NULL, error = ? WHERE id = ?", attempts, now+BackoffFor(attempts), strx.Slice16(msg, 500), row.ID); err != nil {
		w.o.Log.Error("outbox fail update failed", logx.Fields{"error": err})
	}
	w.o.Log.Warn("outbox send failed", logx.Fields{"outbox_id": row.ID, "channel": row.Channel, "attempts": attempts, "error": msg})
}

// Tick runs housekeeping and claims, then one send loop per channel (or agent
// bot, keyed "bot:<id>") unless one is already running. It returns once the
// loops this tick started have settled.
func (w *Worker) Tick() {
	var started []chan struct{}
	func() {
		w.tickMu.Lock()
		defer w.tickMu.Unlock()
		defer func() {
			if r := recover(); r != nil {
				w.o.Log.Error("outbox tick failed", logx.Fields{"error": fmt.Sprint(r)})
			}
		}()
		now := w.o.Clock()
		if err := w.resetStuck(now); err != nil {
			w.o.Log.Error("outbox tick failed", logx.Fields{"error": err})
			return
		}
		if err := w.expire(now); err != nil {
			w.o.Log.Error("outbox tick failed", logx.Fields{"error": err})
			return
		}
		if err := w.handleRemoved(now); err != nil {
			w.o.Log.Error("outbox tick failed", logx.Fields{"error": err})
			return
		}
		q := w.o.DB
		isBusy := func(key string) bool {
			w.mu.Lock()
			defer w.mu.Unlock()
			_, ok := w.busy[key]
			return ok
		}
		chans, err := distinct(q, "SELECT DISTINCT channel FROM outbox WHERE status = 'pending' AND connector_id IS NULL")
		if err != nil {
			w.o.Log.Error("outbox tick failed", logx.Fields{"error": err})
			return
		}
		var sendable []string
		for _, ch := range chans {
			if isBusy(ch) {
				continue
			}
			if st, _ := ChannelState(q, ch, w.channel(ch)); st == StateReady || st == StateDown {
				sendable = append(sendable, ch)
			}
		}
		bots, err := distinct(q, "SELECT DISTINCT connector_id FROM outbox WHERE status = 'pending' AND connector_id IS NOT NULL")
		if err != nil {
			w.o.Log.Error("outbox tick failed", logx.Fields{"error": err})
			return
		}
		var sendableBots []string
		for _, id := range bots {
			if isBusy("bot:" + id) {
				continue
			}
			if st, _ := ChannelState(q, id, w.connector(id)); st == StateReady || st == StateDown {
				sendableBots = append(sendableBots, id)
			}
		}
		rows, err := w.claim(now, sendable, sendableBots)
		if err != nil {
			w.o.Log.Error("outbox tick failed", logx.Fields{"error": err})
			return
		}
		var keys []string
		byKey := map[string][]db.Outbox{}
		for _, r := range rows {
			key := r.Channel
			if r.ConnectorID != nil {
				key = "bot:" + *r.ConnectorID
			}
			if _, ok := byKey[key]; !ok {
				keys = append(keys, key)
			}
			byKey[key] = append(byKey[key], r)
		}
		for _, key := range keys {
			list := byKey[key]
			done := make(chan struct{})
			w.mu.Lock()
			w.busy[key] = done
			w.mu.Unlock()
			started = append(started, done)
			go func(key string, list []db.Outbox, done chan struct{}) {
				defer func() {
					if r := recover(); r != nil {
						w.o.Log.Error("outbox send loop failed", logx.Fields{"channel": key, "error": fmt.Sprint(r)})
					}
					w.mu.Lock()
					delete(w.busy, key)
					w.mu.Unlock()
					close(done)
					// More may be waiting (a full batch, or rows that arrived meanwhile).
					if len(list) == batch {
						w.Wake()
					}
				}()
				w.sendChannel(list)
			}(key, list, done)
		}
	}()
	for _, d := range started {
		<-d
	}
}

// Wake ticks soon (in the background) unless the worker is stopped.
func (w *Worker) Wake() {
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if !stopped {
		go w.Tick()
	}
}

// Start subscribes to enqueues, ticks every 2 s and once now.
func (w *Worker) Start() {
	w.mu.Lock()
	w.stopped, w.stopping = false, false
	w.unsub = OnEnqueued(w.Wake)
	w.ticker = time.NewTicker(tickMs * time.Millisecond)
	w.done = make(chan struct{})
	t, done := w.ticker, w.done
	w.mu.Unlock()
	go func() {
		for {
			select {
			case <-t.C:
				w.Tick()
			case <-done:
				return
			}
		}
	}()
	go w.Tick()
}

// Stop lets sends in flight finish (each capped by the send timeout) up to a
// limit, then releases claims so the next process sends them.
func (w *Worker) Stop() {
	w.mu.Lock()
	w.stopped, w.stopping = true, true
	if w.unsub != nil {
		w.unsub()
		w.unsub = nil
	}
	if w.ticker != nil {
		w.ticker.Stop()
		close(w.done)
		w.ticker = nil
	}
	var loops []chan struct{}
	for _, d := range w.busy {
		loops = append(loops, d)
	}
	w.mu.Unlock()
	deadline := time.After(stopWaitMs * time.Millisecond)
wait:
	for _, d := range loops {
		select {
		case <-d:
		case <-deadline:
			break wait
		}
	}
	_, _ = w.o.DB.Exec("UPDATE outbox SET status = 'pending', claimed_at = NULL WHERE status = 'sending'")
}
