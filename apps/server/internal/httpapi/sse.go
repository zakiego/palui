package httpapi

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// UnauthorizedFrame is stream-guard.ts UNAUTHORIZED_FRAME: sent once before
// closing, so the client can tell a revoked session from a dropped connection.
const UnauthorizedFrame = "event: unauthorized\ndata: {}\n\n"

// EventBatch is nextEvents' default limit.
const EventBatch = 200

func sseHeaders(h http.Header) {
	h.Set("content-type", "text/event-stream; charset=utf-8")
	h.Set("cache-control", "no-cache, no-transform")
	h.Set("connection", "keep-alive")
	// Cloudflare and nginx buffer responses unless told not to.
	h.Set("x-accel-buffering", "no")
}

// stream is one open SSE response: writes are serialized, and Done closes when
// the client leaves, the server shuts down, or the session guard revokes it.
type stream struct {
	w    http.ResponseWriter
	f    http.Flusher
	mu   sync.Mutex
	ctx  context.Context
	stop context.CancelFunc
	// Set when the guard closed it: the unauthorized frame went out.
	revoked bool
}

func (st *stream) write(s string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.ctx.Err() != nil {
		return false
	}
	if _, err := fmt.Fprint(st.w, s); err != nil {
		st.stop()
		return false
	}
	if st.f != nil {
		st.f.Flush()
	}
	return true
}

// openStream starts an SSE response for a signed-in viewer and runs the
// session guard (guardStream): the session is checked every SessionRecheck,
// and once it's gone or expired (a recovery invite deletes them all) the
// stream sends UnauthorizedFrame and closes. Call done when the handler returns.
func (s *Server) openStream(w http.ResponseWriter, r *http.Request, v *auth.Viewer) (*stream, func()) {
	sseHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	if f != nil {
		f.Flush()
	}
	ctx, cancel := context.WithCancel(r.Context())
	st := &stream{w: w, f: f, ctx: ctx, stop: cancel}
	s.streams.Add(1)
	stopOnShutdown := context.AfterFunc(s.streamCtx, cancel)
	go func() {
		defer Recover(s.o.Log, "stream guard")
		t := time.NewTicker(s.o.SessionRecheck)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !auth.SessionAlive(s.o.DB, v.SessionID, s.now()) {
					st.mu.Lock()
					if ctx.Err() == nil {
						_, _ = fmt.Fprint(w, UnauthorizedFrame)
						if f != nil {
							f.Flush()
						}
						st.revoked = true
					}
					st.mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	return st, func() {
		stopOnShutdown()
		cancel()
		s.streams.Done()
	}
}

// --- /api/events/stream ---------------------------------------------------------------

// LatestEventID is latestEventId.
func LatestEventID(q db.Q) (int64, error) {
	var id int64
	err := q.QueryRow("SELECT COALESCE(MAX(id), 0) FROM events").Scan(&id)
	return id, err
}

// NextEvents is nextEvents: rows after afterID, oldest first, and the next cursor.
func NextEvents(q db.Q, afterID int64, limit int) ([]db.Event, int64, error) {
	if limit == 0 {
		limit = EventBatch
	}
	rows, err := q.Query("SELECT "+db.EventCols+" FROM events WHERE id > ? ORDER BY id ASC LIMIT ?", afterID, limit)
	list, err := db.All(rows, err, db.ScanEvent)
	if err != nil {
		return nil, afterID, err
	}
	if len(list) == 0 {
		return []db.Event{}, afterID, nil
	}
	return list, list[len(list)-1].ID, nil
}

// StatusSummary is statusSummary: "All on" or "2 need attention". Paused is
// a choice, so it isn't attention; an error, or something enabled that isn't
// running, is.
type StatusSummary struct {
	Total     int         `json:"total"`
	On        int         `json:"on"`
	Attention []Attention `json:"attention"`
}

type Attention struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

func GetStatusSummary(q db.Q) (StatusSummary, error) {
	rows, err := connectors.List(q)
	if err != nil {
		return StatusSummary{}, err
	}
	out := StatusSummary{Attention: []Attention{}}
	for _, c := range rows {
		if c.Status == "not_set_up" {
			continue
		}
		out.Total++
		if c.Status == "on" {
			out.On++
		}
		if c.Status == "error" || (c.Enabled && c.Status == "paused") {
			out.Attention = append(out.Attention, Attention{ID: c.ID, Label: c.Label, Status: c.Status})
		}
	}
	return out, nil
}

func frame(event string, data any, id *int64) string {
	body, err := jsonx.Marshal(data)
	if err != nil {
		body = []byte("null")
	}
	head := ""
	if id != nil {
		head = fmt.Sprintf("id: %d\n", *id)
	}
	return head + "event: " + event + "\ndata: " + string(body) + "\n\n"
}

// afterParam is Number(last-event-id ?? ?after ?? NaN): a finite number or nil.
func afterParam(r *http.Request) *int64 {
	raw, ok := "", false
	if v, has := r.Header["Last-Event-Id"]; has && len(v) > 0 {
		raw, ok = v[0], true
	} else if r.URL.Query().Has("after") {
		raw, ok = r.URL.Query().Get("after"), true
	}
	if !ok {
		return nil
	}
	n := strx.Number(raw)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	// id > 1.5 is id > 1 for integer ids.
	f := int64(math.Floor(n))
	return &f
}

// eventsStream is GET /api/events/stream: the summary first, then new events
// rows as `events` frames whose id is the cursor (EventSource sends it back
// as Last-Event-ID), and the summary again after a batch that touched a
// connector. Rows are pushed from events.OnEvent in process; the stream still
// catches up from the table on connect and polls it as a backstop.
func (s *Server) eventsStream(w http.ResponseWriter, r *http.Request) {
	v := s.requireUserFromRequest(w, r)
	if v == nil {
		return
	}
	var cursor int64
	if a := afterParam(r); a != nil {
		cursor = *a
	} else {
		id, err := LatestEventID(s.o.DB)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "Internal error"})
			return
		}
		cursor = id
	}
	wake := make(chan struct{}, 1)
	unsub := events.OnEvent(func(db.Event) {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	defer unsub()
	st, done := s.openStream(w, r, v)
	defer done()

	summary := func() bool {
		sum, err := GetStatusSummary(s.o.DB)
		if err != nil {
			return false
		}
		return st.write(frame("summary", sum, nil))
	}
	if !summary() {
		return
	}
	lastWrite := time.Now()
	flush := func() bool {
		for {
			rows, next, err := NextEvents(s.o.DB, cursor, EventBatch)
			if err != nil {
				s.o.Log.Error("events stream query failed", logx.Fields{"error": err})
				return false
			}
			if len(rows) == 0 {
				return true
			}
			cursor = next
			id := cursor
			if !st.write(frame("events", rows, &id)) {
				return false
			}
			lastWrite = time.Now()
			for _, row := range rows {
				if row.ConnectorID != nil {
					if !summary() {
						return false
					}
					break
				}
			}
			if len(rows) < EventBatch {
				return true
			}
		}
	}
	if !flush() {
		return
	}
	poll := time.NewTicker(s.o.EventPoll)
	defer poll.Stop()
	tick := s.o.Keepalive / 3
	if tick <= 0 || tick > time.Second {
		tick = time.Second
	}
	keep := time.NewTicker(tick)
	defer keep.Stop()
	for {
		select {
		case <-st.ctx.Done():
			return
		case <-wake:
			if !flush() {
				return
			}
		case <-poll.C:
			if !flush() {
				return
			}
		case <-keep.C:
			if time.Since(lastWrite) >= s.o.Keepalive {
				if !st.write(": keepalive\n\n") {
					return
				}
				lastWrite = time.Now()
			}
		}
	}
}

// --- /api/chat/stream -------------------------------------------------------------------

// ChatStreamBuffer is how many turn events may wait for a slow client before
// the stream is dropped (the client reconnects); the bus never blocks a turn.
const ChatStreamBuffer = 1024

// chatStream is GET /api/chat/stream: the turn bus (text deltas, tool calls,
// approvals) as unnamed `data:` frames of TurnEvent JSON, the old runtime
// /control/stream passed through the app.
func (s *Server) chatStream(w http.ResponseWriter, r *http.Request) {
	v := s.requireUserFromRequest(w, r)
	if v == nil {
		return
	}
	if s.o.Runtime == nil || s.o.Runtime.Bus == nil {
		writeJSON(w, 503, map[string]string{"error": RuntimeDown})
		return
	}
	ch := make(chan []byte, ChatStreamBuffer)
	overflow := make(chan struct{})
	var once sync.Once
	unsub := s.o.Runtime.Bus.Subscribe(func(e chat.TurnEvent) {
		b, err := jsonx.Marshal(e)
		if err != nil {
			return
		}
		select {
		case ch <- b:
		default:
			once.Do(func() { close(overflow) })
		}
	})
	defer unsub()
	st, done := s.openStream(w, r, v)
	defer done()
	if !st.write(": connected\n\n") {
		return
	}
	keep := time.NewTicker(s.o.Keepalive)
	defer keep.Stop()
	for {
		select {
		case <-st.ctx.Done():
			return
		case <-overflow:
			s.o.Log.Warn("chat stream dropped a slow client", nil)
			return
		case b := <-ch:
			if !st.write("data: " + string(b) + "\n\n") {
				return
			}
		case <-keep.C:
			if !st.write(": keepalive\n\n") {
				return
			}
		}
	}
}
