// Package chat ports packages/core/src/chat: the inbox, the router, the
// per-chat queue, the turn context, the turn loop (the part the TS build got
// from the AI SDK's streamText), approvals, compaction, the chat commands, the
// /report snapshot and the engine that ties them together (PLAN 8).
package chat

import (
	"encoding/json"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// TurnEvent is control.ts's TurnEvent: what the web chat SSE stream and the
// dashboard transcript see of a turn. Type is turn-start, text-delta,
// tool-call, tool-result, approval, message or turn-end; the other fields
// belong to the types that name them.
type TurnEvent struct {
	Type       string
	ChatID     string
	RunID      string
	Delta      string
	ToolCallID string
	Tool       string
	Class      string
	// tool-call: the parsed input as JSON.
	Args        json.RawMessage
	Preview     string
	Ms          int64
	Status      string
	ApprovalID  string
	ArgsPreview string
	MessageID   string
	Role        string
	Text        string
	// turn-end: nil when the turn had no error.
	Error *string
}

// MarshalJSON writes the TS object for the event's type, keys in TS order.
func (e TurnEvent) MarshalJSON() ([]byte, error) {
	o := jsonx.NewMap[any]()
	o.Set("type", e.Type)
	o.Set("chatId", e.ChatID)
	switch e.Type {
	case "turn-start":
		o.Set("runId", e.RunID)
	case "text-delta":
		o.Set("runId", e.RunID)
		o.Set("delta", e.Delta)
	case "tool-call":
		o.Set("runId", e.RunID)
		o.Set("toolCallId", e.ToolCallID)
		o.Set("tool", e.Tool)
		o.Set("class", e.Class)
		if len(e.Args) > 0 {
			o.Set("args", e.Args)
		}
	case "tool-result":
		o.Set("runId", e.RunID)
		o.Set("toolCallId", e.ToolCallID)
		o.Set("tool", e.Tool)
		o.Set("preview", e.Preview)
		o.Set("ms", e.Ms)
		o.Set("status", e.Status)
	case "approval":
		o.Set("runId", e.RunID)
		o.Set("approvalId", e.ApprovalID)
		o.Set("tool", e.Tool)
		o.Set("class", e.Class)
		o.Set("argsPreview", e.ArgsPreview)
	case "message":
		o.Set("messageId", e.MessageID)
		o.Set("role", e.Role)
		o.Set("text", e.Text)
	case "turn-end":
		o.Set("runId", e.RunID)
		o.Set("status", e.Status)
		if e.Error != nil {
			o.Set("error", *e.Error)
		}
	}
	return jsonx.Marshal(o)
}

// Bus is the turn bus (bus.ts): in-process fan-out of TurnEvents. Listeners
// run in the publisher's goroutine, in subscription order; a panicking one is
// skipped so a broken subscriber never breaks a turn.
type Bus struct {
	mu        sync.Mutex
	next      int
	listeners map[int]func(TurnEvent)
	order     []int
}

// NewBus is createTurnBus.
func NewBus() *Bus { return &Bus{listeners: map[int]func(TurnEvent){}} }

// DefaultBus is the process-wide turnBus.
var DefaultBus = NewBus()

// Publish sends e to every listener.
func (b *Bus) Publish(e TurnEvent) {
	b.mu.Lock()
	fns := make([]func(TurnEvent), 0, len(b.order))
	for _, id := range b.order {
		fns = append(fns, b.listeners[id])
	}
	b.mu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() { _ = recover() }()
			fn(e)
		}()
	}
}

// Subscribe adds a listener and returns its unsubscribe func.
func (b *Bus) Subscribe(fn func(TurnEvent)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	b.listeners[id] = fn
	b.order = append(b.order, id)
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.listeners[id]; !ok {
			return
		}
		delete(b.listeners, id)
		for i, x := range b.order {
			if x == id {
				b.order = append(b.order[:i], b.order[i+1:]...)
				break
			}
		}
	}
}

// Size is the number of listeners.
func (b *Bus) Size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.listeners)
}
