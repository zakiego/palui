package conntest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
)

// ChannelCtx is channel-utils/testing.ts testContext: a connector context on
// a memory database with every status, event, config patch and inbound
// message recorded. ClaimOwner and IsOwner are the real owner functions;
// PatchConfig merges into Config like the TS ctx.config spread.
type ChannelCtx struct {
	*FakeCtx
	mu      sync.Mutex
	inbound []connectors.InboundMessage
	cancel  context.CancelFunc
	// Now is the clock ClaimOwner uses (default the wall clock).
	Now func() int64
}

// NewChannelCtx builds a ChannelCtx. dataDir "" is a fresh temp dir.
func NewChannelCtx(t testing.TB, id, typ string, config *jsonx.Object, secrets map[string]string, dataDir string) *ChannelCtx {
	f := NewCtx(t, nil, id, config, secrets)
	f.Type = typ
	if dataDir != "" {
		f.DataDir = dataDir
	}
	c := &ChannelCtx{FakeCtx: f, Now: func() int64 { return time.Now().UnixMilli() }}
	ctx, cancel := context.WithCancel(context.Background())
	c.Ctx, c.cancel = ctx, cancel
	t.Cleanup(cancel)
	c.Services = &connectors.Services{
		Inbound: func(_ context.Context, msg connectors.InboundMessage) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.inbound = append(c.inbound, msg)
			return nil
		},
		ClaimOwner: func(_ context.Context, in connectors.ClaimInput) (bool, error) {
			name := in.DisplayName
			return owner.ClaimOwner(c.DB, owner.ClaimInput{Channel: in.Channel, Nonce: in.Nonce, ExternalID: in.ExternalID, DisplayName: &name}, c.Now())
		},
		IsOwner: func(channel, externalID, altID string) bool {
			ok, _ := owner.IsOwner(c.DB, owner.Sender{Channel: channel, ExternalID: externalID, AltID: altID})
			return ok
		},
	}
	patch := c.PatchConfig
	c.PatchConfig = func(p *jsonx.Object) {
		patch(p)
		c.mu.Lock()
		defer c.mu.Unlock()
		next := c.Config.Clone()
		next.Assign(p)
		c.Config = next
	}
	return c
}

// Abort cancels Ctx (the TS abort controller).
func (c *ChannelCtx) Abort() { c.cancel() }

// Inbound copies the inbound messages so far.
func (c *ChannelCtx) Inbound() []connectors.InboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]connectors.InboundMessage(nil), c.inbound...)
}

// InboundCount is len(Inbound()).
func (c *ChannelCtx) InboundCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inbound)
}

// StatusList copies the statuses so far.
func (f *FakeCtx) StatusList() []Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Status(nil), f.Statuses...)
}

// EventList copies the events so far.
func (f *FakeCtx) EventList() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.Events...)
}

// LastStatusFull is statuses.at(-1) (zero when none).
func (f *FakeCtx) LastStatusFull() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Statuses) == 0 {
		return Status{}
	}
	return f.Statuses[len(f.Statuses)-1]
}

// Until waits for cond like the TS tests' until(): it lets goroutines run
// (the TS setImmediate loop) and fails after 5 s. It never drives timers.
func Until(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

// Settle lets goroutines in flight finish (the TS settle(): a few rounds of
// setImmediate). Use it before asserting that something did NOT happen.
func Settle() {
	for range 20 {
		time.Sleep(time.Millisecond)
	}
}

// ConfigNow is the config with every patch so far applied.
func (c *ChannelCtx) ConfigNow() *jsonx.Object {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Config.Clone()
}
