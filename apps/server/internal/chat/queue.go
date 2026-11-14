package chat

import (
	"fmt"
	"sync"
)

// The per-chat queue (queue.ts, PLAN 8.3): one turn at a time per chat.
// Messages that arrive while a turn runs are batched into the next turn.
// Other work for the chat (approval resumes, jobs, hooks) runs in order
// between turns. Each chat with work drains in its own goroutine.

type queueItem struct {
	// A message item when run is nil.
	messageID string
	run       func()
}

type chatState struct {
	running bool
	items   []queueItem
}

// Queue is createChatQueue's result.
type Queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	chats   map[string]*chatState
	paused  bool
	runTurn func(chatID string, messageIDs []string)
	onError func(chatID string, err error)
}

// NewQueue is createChatQueue(runTurn, onError). A panic in runTurn or a task
// is reported to onError and the queue goes on.
func NewQueue(runTurn func(chatID string, messageIDs []string), onError func(chatID string, err error)) *Queue {
	q := &Queue{chats: map[string]*chatState{}, runTurn: runTurn, onError: onError}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *Queue) guard(chatID string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("%v", r)
			}
			q.onError(chatID, err)
		}
	}()
	fn()
}

func (q *Queue) drain(chatID string, st *chatState) {
	q.mu.Lock()
	for len(st.items) > 0 && !q.paused {
		first := st.items[0]
		if first.run == nil {
			// Take every message waiting at the head: they become one turn.
			var ids []string
			for len(st.items) > 0 && st.items[0].run == nil {
				ids = append(ids, st.items[0].messageID)
				st.items = st.items[1:]
			}
			q.mu.Unlock()
			q.guard(chatID, func() { q.runTurn(chatID, ids) })
			q.mu.Lock()
			continue
		}
		st.items = st.items[1:]
		q.mu.Unlock()
		q.guard(chatID, first.run)
		q.mu.Lock()
	}
	st.running = false
	if len(st.items) == 0 && q.chats[chatID] == st {
		delete(q.chats, chatID)
	}
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *Queue) push(chatID string, item queueItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	st := q.chats[chatID]
	if st == nil {
		st = &chatState{}
		q.chats[chatID] = st
	}
	if item.run == nil {
		for _, i := range st.items {
			if i.run == nil && i.messageID == item.messageID {
				return
			}
		}
	}
	st.items = append(st.items, item)
	if !st.running && !q.paused {
		st.running = true
		go q.drain(chatID, st)
	}
}

// PushMessage queues a message for the chat's next turn (deduplicated).
func (q *Queue) PushMessage(chatID, messageID string) {
	q.push(chatID, queueItem{messageID: messageID})
}

// PushTask queues work to run in the chat's order.
func (q *Queue) PushTask(chatID string, run func()) {
	q.push(chatID, queueItem{run: run})
}

// RunInChat runs fn in the chat's order and waits for its result. Like the TS
// promise, it waits for as long as the queue is paused.
func RunInChat[T any](q *Queue, chatID string, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	q.PushTask(chatID, func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				err, ok := p.(error)
				if !ok {
					err = fmt.Errorf("%v", p)
				}
				r.err = err
			}
			done <- r
		}()
		r.v, r.err = fn()
	})
	r := <-done
	return r.v, r.err
}

// Busy says whether the chat's queue is running.
func (q *Queue) Busy(chatID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	st := q.chats[chatID]
	return st != nil && st.running
}

// Pause stops starting new work (drain on shutdown). Running items finish.
func (q *Queue) Pause() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.paused = true
}

func (q *Queue) idleLocked() bool {
	for _, st := range q.chats {
		if st.running || len(st.items) > 0 {
			return false
		}
	}
	return true
}

// Idle waits until no chat has work running or waiting.
func (q *Queue) Idle() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.idleLocked() {
		q.cond.Wait()
	}
}

// Active says whether anything is running (shutdown waits for this).
func (q *Queue) Active() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, st := range q.chats {
		if st.running {
			return true
		}
	}
	return false
}
