// Package events ports packages/core/src/events.ts. The `events` table is the
// single source for status: the dashboard (SSE), /health and chat read it.
// Writers also notify in-process listeners.
package events

import (
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
)

type Input struct {
	// "debug", "info" (default), "warn", "error".
	Level       string
	ConnectorID *string
	Kind        string
	Message     string
	// A JSON object (map, struct or *jsonx.Object), or nil.
	Data any
}

type Listener func(e db.Event)

var (
	mu        sync.Mutex
	listeners = map[int]Listener{}
	nextID    int
)

// OnEvent subscribes; the returned func unsubscribes.
func OnEvent(fn Listener) func() {
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

func notify(e db.Event) {
	mu.Lock()
	fns := make([]Listener, 0, len(listeners))
	for _, l := range listeners {
		fns = append(fns, l)
	}
	mu.Unlock()
	for _, l := range fns {
		func() {
			// A listener must never break the writer.
			defer func() { _ = recover() }()
			l(e)
		}()
	}
}

// Log writes an event with its message and data redacted.
func Log(q db.Q, e Input, now int64) (db.Event, error) {
	level := e.Level
	if level == "" {
		level = "info"
	}
	var data db.NullJSON[*jsonx.Object]
	if e.Data != nil {
		obj, err := jsonx.ToObject(e.Data)
		if err != nil {
			return db.Event{}, err
		}
		if obj != nil {
			data = db.SomeJSON(logx.RedactDeep(obj).(*jsonx.Object))
		}
	}
	row, err := db.ScanEvent(q.WQueryRow("INSERT INTO events (at, level, connector_id, kind, message, data) VALUES (?, ?, ?, ?, ?, ?) RETURNING "+db.EventCols,
		now, level, e.ConnectorID, e.Kind, logx.Redact(e.Message), data))
	if err != nil {
		return db.Event{}, err
	}
	notify(row)
	return row, nil
}

// Now is time.Now in Unix ms, for callers without an explicit clock.
func Now() int64 { return time.Now().UnixMilli() }

type ListOptions struct {
	AfterID     *int64
	ConnectorID string
	Level       string
	// Default 100.
	Limit int
}

// List returns events newest first.
func List(q db.Q, opts ListOptions) ([]db.Event, error) {
	var where []string
	var args []any
	if opts.AfterID != nil {
		where = append(where, "id > ?")
		args = append(args, *opts.AfterID)
	}
	if opts.ConnectorID != "" {
		where = append(where, "connector_id = ?")
		args = append(args, opts.ConnectorID)
	}
	if opts.Level != "" {
		where = append(where, "level = ?")
		args = append(args, opts.Level)
	}
	query := "SELECT " + db.EventCols + " FROM events"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 100
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := q.Query(query, args...)
	return db.All(rows, err, db.ScanEvent)
}

// DefaultRetentionMs is 90 days.
const DefaultRetentionMs = 90 * 24 * 3600_000

// Prune deletes events older than olderThanMs and returns how many.
func Prune(q db.Q, olderThanMs int64, now int64) (int64, error) {
	return db.Changes(q.Exec("DELETE FROM events WHERE at < ?", now-olderThanMs))
}
