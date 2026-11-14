package reminders

import "sync"

// "Something on the schedule changed": reminder and job writes call
// NotifyScheduleChanged so the scheduler re-arms its timer right away instead
// of waiting for the next sweep. In-process only.
var (
	sigMu        sync.Mutex
	sigListeners = map[int]func(){}
	sigNext      int
)

// OnScheduleChanged subscribes fn; the returned func unsubscribes.
func OnScheduleChanged(fn func()) func() {
	sigMu.Lock()
	defer sigMu.Unlock()
	sigNext++
	id := sigNext
	sigListeners[id] = fn
	return func() {
		sigMu.Lock()
		defer sigMu.Unlock()
		delete(sigListeners, id)
	}
}

// NotifyScheduleChanged calls every listener. A listener must never break the
// writer, so its panics are swallowed.
func NotifyScheduleChanged() {
	sigMu.Lock()
	fns := make([]func(), 0, len(sigListeners))
	for _, l := range sigListeners {
		fns = append(fns, l)
	}
	sigMu.Unlock()
	for _, l := range fns {
		func() {
			defer func() { _ = recover() }()
			l()
		}()
	}
}
