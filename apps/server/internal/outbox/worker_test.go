package outbox_test

// Port of outbox/worker.test.ts.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/outbox"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type harness struct {
	db      *db.DB
	handles map[string]*conntest.FakeChannel
	tgChat  db.Chat
	worker  *outbox.Worker
	now     atomic.Int64
}

func (h *harness) advance(ms int64) { h.now.Add(ms) }

func (h *harness) row(t *testing.T, id string) db.Outbox {
	t.Helper()
	r, err := db.ScanOutbox(h.db.QueryRow("SELECT "+db.OutboxCols+" FROM outbox WHERE id = ?", id))
	testutil.Must(t, err)
	return r
}

func setup(t *testing.T) *harness {
	h := &harness{db: testutil.DB(t), handles: map[string]*conntest.FakeChannel{"telegram": conntest.NewFakeChannel("telegram"), "whatsapp": conntest.NewFakeChannel("whatsapp")}}
	h.now.Store(1_000_000)
	testutil.SeedConnector(t, h.db, "telegram", "channel", testutil.SeedOptions{}, 1)
	testutil.SeedConnector(t, h.db, "whatsapp", "channel", testutil.SeedOptions{}, 1)
	var err error
	h.tgChat, err = chats.Ensure(h.db, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, h.now.Load())
	testutil.Must(t, err)
	h.worker = outbox.NewWorker(outbox.WorkerOptions{DB: h.db, Channel: func(ch string) outbox.Channel {
		if c, ok := h.handles[ch]; ok {
			return c
		}
		return nil
	}, Clock: func() int64 { return h.now.Load() }})
	return h
}

func enqueue(t *testing.T, q db.Q, chatID, channel, text, source string, expiresAt *int64, now int64) db.Outbox {
	t.Helper()
	r, err := outbox.Enqueue(q, outbox.EnqueueInput{ChatID: chatID, Channel: channel, Payload: db.OutboxPayload{Text: text}, Source: source, ExpiresAt: expiresAt}, now)
	testutil.Must(t, err)
	return r
}

func owners(t *testing.T, d *db.DB) {
	c, err := owner.CreateClaim(d, "telegram", 1)
	testutil.Must(t, err)
	_, err = owner.ClaimOwner(d, owner.ClaimInput{Channel: "telegram", Nonce: c.Nonce, ExternalID: "42"}, 2)
	testutil.Must(t, err)
	_, err = owner.SetWhatsAppOwner(d, "08123456789", 3)
	testutil.Must(t, err)
}

func ptr(v int64) *int64 { return &v }

func hasEvent(t *testing.T, d *db.DB, kind, contains string) bool {
	var n int
	testutil.Must(t, d.QueryRow("SELECT count(*) FROM events WHERE kind = ? AND message LIKE ?", kind, "%"+contains+"%").Scan(&n))
	return n > 0
}

func TestClaimsSendsAndAcks(t *testing.T) {
	h := setup(t)
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "halo", "system", nil, h.now.Load())
	h.worker.Tick()
	sent := h.handles["telegram"].Sent()
	if len(sent) != 1 || sent[0].To != "42" || sent[0].Payload.Text != "halo" {
		t.Fatal(sent)
	}
	row := h.row(t, r.ID)
	if row.Status != "sent" || *row.ChannelMsgID != "telegram-1" {
		t.Fatalf("%+v", row)
	}
	h.worker.Tick()
	if len(h.handles["telegram"].Sent()) != 1 {
		t.Fatal("sent twice")
	}
}

func TestBackoffLadder(t *testing.T) {
	h := setup(t)
	h.handles["telegram"].SetFailNext(10)
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "system", nil, h.now.Load())
	var waits []int64
	for i := 0; i < 6; i++ {
		h.worker.Tick()
		row := h.row(t, r.ID)
		if row.Status != "pending" {
			t.Fatal(row.Status)
		}
		waits = append(waits, row.NextAttemptAt-h.now.Load())
		// Not due yet: nothing happens.
		h.worker.Tick()
		if h.row(t, r.ID).Attempts != int64(i+1) {
			t.Fatal(h.row(t, r.ID).Attempts)
		}
		h.advance(row.NextAttemptAt - h.now.Load())
	}
	want := []int64{5_000, 30_000, 120_000, 600_000, 1_800_000, 1_800_000}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatal(waits)
		}
	}
}

func TestExpiresWithEvent(t *testing.T) {
	h := setup(t)
	h.handles["telegram"].SetFailNext(10)
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "reminder:r1", ptr(h.now.Load()+10_000), h.now.Load())
	var outboundCalls int
	h.worker = outbox.NewWorker(outbox.WorkerOptions{DB: h.db, Channel: func(ch string) outbox.Channel { return h.handles[ch] }, Clock: func() int64 { return h.now.Load() }, OutboundFailed: func(db.Q, db.Outbox, int64) { outboundCalls++ }})
	h.worker.Tick()
	h.advance(20_000)
	h.worker.Tick()
	if h.row(t, r.ID).Status != "failed" || !hasEvent(t, h.db, "outbox.failed", "") || outboundCalls != 1 {
		t.Fatal(h.row(t, r.ID).Status, outboundCalls)
	}
}

func TestPausedChannelWaitsThenSends(t *testing.T) {
	h := setup(t)
	testutil.Must(t, connectors.Pause(h.db, "telegram", "test", h.now.Load()))
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "system", nil, h.now.Load())
	for i := 0; i < 3; i++ {
		h.worker.Tick()
		h.advance(60_000)
	}
	if row := h.row(t, r.ID); row.Status != "pending" || row.Attempts != 0 {
		t.Fatalf("%+v", row)
	}
	testutil.SeedConnector(t, h.db, "telegram", "channel", testutil.SeedOptions{}, 1)
	h.worker.Tick()
	if h.row(t, r.ID).Status != "sent" {
		t.Fatal(h.row(t, r.ID).Status)
	}
}

func TestRemovedChannelReroutesOrCancels(t *testing.T) {
	h := setup(t)
	owners(t, h.db)
	testutil.Must(t, settings.Set(h.db, "fallbackChannel", "whatsapp", 1))
	a := enqueue(t, h.db, h.tgChat.ID, "telegram", "a", "system", nil, h.now.Load())
	testutil.Must(t, connectors.Delete(h.db, "telegram", h.now.Load()))
	h.worker.Tick()
	if row := h.row(t, a.ID); row.Status != "sent" || row.Channel != "whatsapp" || *row.FallbackFrom != "telegram" {
		t.Fatalf("%+v", row)
	}
	if to := h.handles["whatsapp"].Sent()[0].To; to != "628123456789@s.whatsapp.net" {
		t.Fatal(to)
	}
	testutil.Must(t, settings.Set(h.db, "fallbackChannel", "none", 1))
	b := enqueue(t, h.db, h.tgChat.ID, "telegram", "b", "system", nil, h.now.Load())
	moved, cancelled, err := outbox.RerouteChannelRows(h.db, "telegram", h.now.Load(), nil)
	testutil.Must(t, err)
	if moved != 0 || cancelled != 1 || h.row(t, b.ID).Status != "cancelled" || !hasEvent(t, h.db, "outbox.reroute", "cancelled") {
		t.Fatal(moved, cancelled)
	}
}

func TestStuckSendingGoesBack(t *testing.T) {
	h := setup(t)
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "system", nil, h.now.Load())
	_, err := h.db.Exec("UPDATE outbox SET status = 'sending', claimed_at = ? WHERE id = ?", h.now.Load(), r.ID)
	testutil.Must(t, err)
	h.advance(4 * 60_000)
	h.worker.Tick()
	if h.row(t, r.ID).Status != "sending" {
		t.Fatal("reset early")
	}
	h.advance(2 * 60_000)
	h.worker.Tick()
	if h.row(t, r.ID).Status != "sent" {
		t.Fatal(h.row(t, r.ID).Status)
	}
}

func TestReminderFallsBackAfterTwoFailures(t *testing.T) {
	h := setup(t)
	owners(t, h.db)
	testutil.Must(t, settings.Set(h.db, "fallbackChannel", "whatsapp", 1))
	h.handles["telegram"].SetFailNext(99)
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "bayar listrik", "reminder:r1", ptr(h.now.Load()+86_400_000), h.now.Load())
	h.worker.Tick()
	if row := h.row(t, r.ID); row.Attempts != 1 || row.Channel != "telegram" {
		t.Fatalf("%+v", row)
	}
	h.advance(5_000)
	h.worker.Tick()
	// Moved and sent right away on the fallback.
	h.worker.Tick()
	if row := h.row(t, r.ID); row.Status != "sent" || row.Channel != "whatsapp" || *row.FallbackFrom != "telegram" {
		t.Fatalf("%+v", row)
	}
	sent := h.handles["whatsapp"].Sent()
	if len(sent) != 1 || sent[0].Payload.Text != "bayar listrik" || !hasEvent(t, h.db, "outbox.fallback", "") {
		t.Fatal(sent)
	}
}

func TestReleasesClaimsOnStop(t *testing.T) {
	h := setup(t)
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "system", nil, h.now.Load())
	_, err := h.db.Exec("UPDATE outbox SET status = 'sending', claimed_at = ? WHERE id = ?", h.now.Load(), r.ID)
	testutil.Must(t, err)
	h.worker.Stop()
	if h.row(t, r.ID).Status != "pending" {
		t.Fatal(h.row(t, r.ID).Status)
	}
}

func TestPausedRowsDontExpire(t *testing.T) {
	h := setup(t)
	testutil.Must(t, connectors.Pause(h.db, "telegram", "test", h.now.Load()))
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "telat", "reminder:r1", ptr(h.now.Load()+24*3600_000), h.now.Load())
	for i := 0; i < 30; i++ {
		h.advance(3600_000)
		h.worker.Tick()
	}
	if row := h.row(t, r.ID); row.Status != "pending" || row.Attempts != 0 {
		t.Fatalf("%+v", row)
	}
	testutil.SeedConnector(t, h.db, "telegram", "channel", testutil.SeedOptions{}, 1)
	h.worker.Tick()
	if h.row(t, r.ID).Status != "sent" {
		t.Fatal(h.row(t, r.ID).Status)
	}
}

func TestHungSendStaysInSending(t *testing.T) {
	d := testutil.DB(t)
	now := int64(1_000_000)
	testutil.SeedConnector(t, d, "telegram", "channel", testutil.SeedOptions{}, 1)
	testutil.SeedConnector(t, d, "whatsapp", "channel", testutil.SeedOptions{}, 1)
	tgChat, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, now)
	waChat, _ := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "628123456789@s.whatsapp.net"}, now)
	wa := conntest.NewFakeChannel("whatsapp")
	block := make(chan struct{})
	defer close(block)
	hung := conntest.NewFakeChannel("telegram")
	hung.SendFn = func(ctx context.Context, to string, p db.OutboxPayload) (connectors.SendResult, error) {
		<-block
		return connectors.SendResult{}, errors.New("closed")
	}
	handles := map[string]outbox.Channel{"telegram": hung, "whatsapp": wa}
	w := outbox.NewWorker(outbox.WorkerOptions{DB: d, Channel: func(ch string) outbox.Channel { return handles[ch] }, Clock: func() int64 { return now }, SendTimeoutMs: 50})
	a := enqueue(t, d, tgChat.ID, "telegram", "a", "system", nil, now)
	b := enqueue(t, d, tgChat.ID, "telegram", "b", "system", nil, now)
	var slow sync.WaitGroup
	slow.Add(1)
	go func() { defer slow.Done(); w.Tick() }()
	status := func(id string) db.Outbox {
		r, err := db.ScanOutbox(d.QueryRow("SELECT "+db.OutboxCols+" FROM outbox WHERE id = ?", id))
		testutil.Must(t, err)
		return r
	}
	deadline := time.Now().Add(2 * time.Second)
	for status(a.ID).Status != "sending" {
		if time.Now().After(deadline) {
			t.Fatal("never claimed")
		}
		time.Sleep(time.Millisecond)
	}
	// WhatsApp sends while the Telegram send hangs.
	c := enqueue(t, d, waChat.ID, "whatsapp", "c", "system", nil, now)
	w.Tick()
	if status(c.ID).Status != "sent" {
		t.Fatal(status(c.ID).Status)
	}
	slow.Wait()
	// Still in flight: not retried, so it can't arrive twice.
	if r := status(a.ID); r.Status != "sending" || r.Attempts != 0 {
		t.Fatalf("%+v", r)
	}
	// The rest of the hung channel's batch went back without using an attempt.
	if r := status(b.ID); r.Status != "pending" || r.Attempts != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestLateSettles(t *testing.T) {
	d := testutil.DB(t)
	now := int64(1_000_000)
	testutil.SeedConnector(t, d, "telegram", "channel", testutil.SeedOptions{}, 1)
	chat, _ := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, now)
	type settle struct {
		ok  chan connectors.SendResult
		err chan error
	}
	var mu sync.Mutex
	var settles []settle
	slowCh := conntest.NewFakeChannel("telegram")
	slowCh.SendFn = func(ctx context.Context, to string, p db.OutboxPayload) (connectors.SendResult, error) {
		s := settle{make(chan connectors.SendResult, 1), make(chan error, 1)}
		mu.Lock()
		settles = append(settles, s)
		mu.Unlock()
		select {
		case r := <-s.ok:
			return r, nil
		case e := <-s.err:
			return connectors.SendResult{}, e
		}
	}
	w := outbox.NewWorker(outbox.WorkerOptions{DB: d, Channel: func(string) outbox.Channel { return slowCh }, Clock: func() int64 { return now }, SendTimeoutMs: 20})
	row := func(id string) db.Outbox {
		r, err := db.ScanOutbox(d.QueryRow("SELECT "+db.OutboxCols+" FROM outbox WHERE id = ?", id))
		testutil.Must(t, err)
		return r
	}
	wait := func(cond func() bool) {
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatal("timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	a := enqueue(t, d, chat.ID, "telegram", "a", "system", nil, now)
	w.Tick()
	mu.Lock()
	settles[0].ok <- connectors.SendResult{ChannelMsgID: "m1"}
	mu.Unlock()
	wait(func() bool {
		r := row(a.ID)
		return r.Status == "sent" && r.ChannelMsgID != nil && *r.ChannelMsgID == "m1"
	})
	b := enqueue(t, d, chat.ID, "telegram", "b", "system", nil, now)
	w.Tick()
	mu.Lock()
	settles[1].err <- errors.New("socket closed")
	mu.Unlock()
	wait(func() bool { r := row(b.ID); return r.Status == "pending" && r.Attempts == 1 })
}

func TestNeverReroutesARowBeingSent(t *testing.T) {
	h := setup(t)
	owners(t, h.db)
	testutil.Must(t, settings.Set(h.db, "fallbackChannel", "whatsapp", 1))
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "system", nil, h.now.Load())
	_, err := h.db.Exec("UPDATE outbox SET status = 'sending', claimed_at = ? WHERE id = ?", h.now.Load(), r.ID)
	testutil.Must(t, err)
	moved, cancelled, err := outbox.RerouteChannelRows(h.db, "telegram", h.now.Load(), nil)
	testutil.Must(t, err)
	if moved != 0 || cancelled != 0 {
		t.Fatal(moved, cancelled)
	}
	if row := h.row(t, r.ID); row.Status != "sending" || row.Channel != "telegram" {
		t.Fatalf("%+v", row)
	}
}

// --- Go additions: outbound kill switch, reply linking, start and wake on enqueue -------

func TestOutboundKillSwitchAndReplyLinking(t *testing.T) {
	h := setup(t)
	ob, err := settings.GetOutbound(h.db)
	testutil.Must(t, err)
	ob.Enabled = false
	testutil.Must(t, settings.Set(h.db, "outbound", ob, 1))
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "hi", "outbound:ob_1", nil, h.now.Load())
	_, err = h.db.Exec("INSERT INTO messages (id, chat_id, session_id, role, content, text, status, run_id, created_at) VALUES ('msg_1', ?, ?, 'assistant', '{}', 'x', 'handled', 'run_9', 1)", h.tgChat.ID, h.tgChat.SessionID)
	testutil.Must(t, err)
	reply := enqueue(t, h.db, h.tgChat.ID, "telegram", "x", "reply:run_9", nil, h.now.Load())
	h.worker.Tick()
	if row := h.row(t, r.ID); row.Status != "cancelled" || *row.Error != "outbound turned off" {
		t.Fatalf("%+v", row)
	}
	var linked string
	testutil.Must(t, h.db.QueryRow("SELECT channel_msg_id FROM messages WHERE id = 'msg_1'").Scan(&linked))
	if h.row(t, reply.ID).Status != "sent" || !strings.HasPrefix(linked, "telegram-") {
		t.Fatal(linked)
	}
}

func TestStartWakesOnEnqueue(t *testing.T) {
	h := setup(t)
	h.worker.Start()
	defer h.worker.Stop()
	r := enqueue(t, h.db, h.tgChat.ID, "telegram", "now", "system", nil, h.now.Load())
	outbox.Notify()
	deadline := time.Now().Add(2 * time.Second)
	for h.row(t, r.ID).Status != "sent" {
		if time.Now().After(deadline) {
			t.Fatal("not sent")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if st, _ := outbox.ChannelState(h.db, "web", nil); st != outbox.StateDown {
		t.Fatal(st)
	}
	if st, _ := outbox.ChannelState(h.db, "gone", nil); st != outbox.StateRemoved {
		t.Fatal(st)
	}
}
