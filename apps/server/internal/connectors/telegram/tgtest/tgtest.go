// Package tgtest is telegram/test-transport.ts: a fake Bot API transport that
// records every outgoing call and answers with canned results, so tests never
// reach api.telegram.org.
//
// Long polling: a getUpdates call with a timeout parks until Feed hands it
// updates or the bot stops. Feed returns once the bot asks for the next batch,
// which grammY's loop (and the Go port) only does after handling the last one.
package tgtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// FakeMe is FAKE_ME.
var FakeMe = map[string]any{
	"id": 999, "is_bot": true, "first_name": "Palui", "username": "palui_test_bot",
	"can_join_groups": true, "can_read_all_group_messages": false, "supports_inline_queries": false,
}

// APIError makes a handler "fail" (FakeApiError).
type APIError struct {
	Code        int
	Description string
}

// Handler answers one call: a JSON-able result or an APIError.
type Handler func(payload *jsonx.Object) any

// Call is one recorded call.
type Call struct {
	Method  string
	Payload *jsonx.Object
}

type batch struct {
	updates []telegram.Update
	done    chan struct{}
}

// Fake is FakeTelegram.
type Fake struct {
	mu       sync.Mutex
	calls    []Call
	handlers map[string]Handler
	clients  int
	nextID   int64
	queue    chan batch
	pending  chan struct{}
	polling  chan struct{}
	polled   bool
}

func New() *Fake {
	return &Fake{handlers: map[string]Handler{}, nextID: 100, queue: make(chan batch), polling: make(chan struct{})}
}

// On overrides a method; return an APIError to make Telegram fail.
func (f *Fake) On(method string, h Handler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

// NewTransport is createBot: Options.NewTransport.
func (f *Fake) NewTransport(token string) telegram.Transport {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients++
	return &transport{f: f}
}

// Clients is how many transports (bots) were created.
func (f *Fake) Clients() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clients
}

// Calls copies every recorded call.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CallsTo is the payloads sent to one method, in order.
func (f *Fake) CallsTo(method string) []*jsonx.Object {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*jsonx.Object
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c.Payload)
		}
	}
	return out
}

// WaitPolling blocks until the bot made its first long-poll call.
func (f *Fake) WaitPolling() { <-f.polling }

// Feed hands one batch of updates to the parked getUpdates and returns once
// the bot asks for the next batch (the batch was handled).
func (f *Fake) Feed(updates ...telegram.Update) {
	b := batch{updates: updates, done: make(chan struct{})}
	f.queue <- b
	<-b.done
}

// FeedJSON is Feed with an update written as JSON.
func (f *Fake) FeedJSON(raw string) {
	var u telegram.Update
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		panic(err)
	}
	f.Feed(u)
}

type transport struct{ f *Fake }

func (t *transport) Call(ctx context.Context, method string, payload *jsonx.Object) (json.RawMessage, error) {
	f := t.f
	if payload == nil {
		payload = jsonx.NewMap[any]()
	}
	f.mu.Lock()
	_, custom := f.handlers[method]
	f.mu.Unlock()
	if method == "getUpdates" && !custom {
		if _, long := payload.Get("timeout"); !long {
			return json.RawMessage("[]"), nil
		}
		// A new long poll means the last batch was handled.
		f.mu.Lock()
		if f.pending != nil {
			close(f.pending)
			f.pending = nil
		}
		if !f.polled {
			f.polled = true
			close(f.polling)
		}
		f.mu.Unlock()
		select {
		case b := <-f.queue:
			f.mu.Lock()
			f.pending = b.done
			f.mu.Unlock()
			raw, _ := json.Marshal(b.updates)
			return raw, nil
		case <-ctx.Done():
			f.mu.Lock()
			if f.pending != nil {
				close(f.pending)
				f.pending = nil
			}
			f.mu.Unlock()
			return nil, &telegram.HTTPError{Method: method, Err: errors.New("aborted")}
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, Call{Method: method, Payload: payload.Clone()})
	h := f.handlers[method]
	f.mu.Unlock()
	var result any
	if h != nil {
		result = h(payload)
	} else {
		result = f.defaultResult(method, payload)
	}
	if e, ok := result.(*APIError); ok {
		return nil, &telegram.APIError{Method: method, Code: e.Code, Description: e.Description}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("fake result: %w", err)
	}
	return raw, nil
}

func (f *Fake) defaultResult(method string, p *jsonx.Object) any {
	switch method {
	case "getMe":
		return FakeMe
	case "deleteWebhook", "sendChatAction", "answerCallbackQuery":
		return true
	case "getFile":
		id, _ := p.Get("file_id")
		return map[string]any{"file_id": id, "file_unique_id": fmt.Sprintf("u-%v", id), "file_path": fmt.Sprintf("files/%v", id)}
	case "sendMessage", "sendPhoto", "sendDocument", "sendVideo", "sendVoice", "sendAudio", "sendSticker":
		f.mu.Lock()
		id := f.nextID
		f.nextID++
		f.mu.Unlock()
		chatID, _ := p.Get("chat_id")
		text, _ := p.Get("text")
		return map[string]any{"message_id": id, "date": 0, "chat": map[string]any{"id": strx.Number(fmt.Sprint(chatID)), "type": "private", "first_name": "X"}, "text": text}
	}
	return true
}
