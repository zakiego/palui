package channelutil

import "time"

// Timers is setTimeout and setInterval, injectable so tests drive them with
// testutil.FakeClock. Same method set as reminders.Timers.
type Timers interface {
	SetTimeout(fn func(), ms int64) any
	ClearTimeout(h any)
	SetInterval(fn func(), ms int64) any
	ClearInterval(h any)
}

// RealTimers runs callbacks on their own goroutines.
type RealTimers struct{}

type intervalHandle struct{ stop chan struct{} }

func (RealTimers) SetTimeout(fn func(), ms int64) any {
	return time.AfterFunc(time.Duration(ms)*time.Millisecond, fn)
}

func (RealTimers) ClearTimeout(h any) {
	if t, ok := h.(*time.Timer); ok {
		t.Stop()
	}
}

func (RealTimers) SetInterval(fn func(), ms int64) any {
	h := &intervalHandle{stop: make(chan struct{})}
	t := time.NewTicker(time.Duration(ms) * time.Millisecond)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fn()
			case <-h.stop:
				return
			}
		}
	}()
	return h
}

func (RealTimers) ClearInterval(h any) {
	if i, ok := h.(*intervalHandle); ok {
		select {
		case <-i.stop:
		default:
			close(i.stop)
		}
	}
}
