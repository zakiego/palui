// Package outbox ports packages/core/src/outbox/enqueue.ts: writing to the
// outbox. Every outbound message goes through here; the outbox worker (M2)
// claims, sends, acks and retries.
package outbox

import (
	"sync"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

type EnqueueInput struct {
	ChatID  string
	Channel string
	// The agent bot to send through (chats.connectorId); "" means the channel's main connector.
	ConnectorID string
	Payload     db.OutboxPayload
	// "reply:<runId>", "reminder:<id>", "job:<id>", "approval:<id>", "system"
	Source string
	// Give up (status failed) after this time. Reminders: 24 h.
	ExpiresAt *int64
	// Not before this time (default now).
	NotBefore *int64
}

func Enqueue(q db.Q, in EnqueueInput, now int64) (db.Outbox, error) {
	var connectorID *string
	if in.ConnectorID != "" {
		connectorID = &in.ConnectorID
	}
	next := now
	if in.NotBefore != nil {
		next = *in.NotBefore
	}
	return db.ScanOutbox(q.WQueryRow("INSERT INTO outbox (id, chat_id, channel, connector_id, payload, status, attempts, next_attempt_at, source, expires_at, created_at) VALUES (?, ?, ?, ?, ?, 'pending', 0, ?, ?, ?, ?) RETURNING "+db.OutboxCols,
		secret.NewID("out"), in.ChatID, in.Channel, connectorID, db.JSON[db.OutboxPayload]{V: in.Payload}, next, in.Source, in.ExpiresAt, now))
}

var (
	mu        sync.Mutex
	listeners = map[int]func(){}
	nextID    int
)

// OnEnqueued subscribes the worker so a new row is sent at once instead of on
// the next tick. The returned func unsubscribes.
func OnEnqueued(fn func()) func() {
	mu.Lock()
	defer mu.Unlock()
	nextID++
	id := nextID
	listeners[id] = fn
	return func() {
		mu.Lock()
		defer mu.Unlock()
		delete(listeners, id)
	}
}

// Notify pokes every subscriber.
func Notify() {
	mu.Lock()
	fns := make([]func(), 0, len(listeners))
	for _, l := range listeners {
		fns = append(fns, l)
	}
	mu.Unlock()
	for _, l := range fns {
		l()
	}
}
